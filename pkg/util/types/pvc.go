/*
 * This file is part of the KubeVirt project
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 *
 * Copyright 2018 Red Hat, Inc.
 *
 */

package types

import (
	"fmt"
	"strings"

	k8sv1 "k8s.io/api/core/v1"
	storagev1 "k8s.io/api/storage/v1"
	"k8s.io/client-go/tools/cache"

	virtv1 "kubevirt.io/client-go/api/v1"
)

func IsPVCBlockFromStore(store cache.Store, namespace string, claimName string) (pvc *k8sv1.PersistentVolumeClaim, exists bool, isBlockDevice bool, err error) {
	obj, exists, err := store.GetByKey(namespace + "/" + claimName)
	if err != nil || !exists {
		return nil, exists, false, err
	}
	if pvc, ok := obj.(*k8sv1.PersistentVolumeClaim); ok {
		return obj.(*k8sv1.PersistentVolumeClaim), true, isPVCBlock(pvc), nil
	}
	return nil, false, false, fmt.Errorf("this is not a PVC! %v", obj)
}

func isPVCBlock(pvc *k8sv1.PersistentVolumeClaim) bool {
	// We do not need to consider the data in a PersistentVolume (as of Kubernetes 1.9)
	// If a PVC does not specify VolumeMode and the PV specifies VolumeMode = Block
	// the claim will not be bound. So for the sake of a boolean answer, if the PVC's
	// VolumeMode is Block, that unambiguously answers the question
	return pvc.Spec.VolumeMode != nil && *pvc.Spec.VolumeMode == k8sv1.PersistentVolumeBlock
}

func HasSharedAccessMode(accessModes []k8sv1.PersistentVolumeAccessMode) bool {
	for _, accessMode := range accessModes {
		if accessMode == k8sv1.ReadWriteMany {
			return true
		}
	}
	return false
}

func IsPreallocated(annotations map[string]string) bool {
	for a, value := range annotations {
		if strings.Contains(a, "/storage.preallocation") && value == "true" {
			return true
		}
		if strings.Contains(a, "/storage.thick-provisioned") && value == "true" {
			return true
		}
	}
	return false
}

func PVCNameFromVirtVolume(volume *virtv1.Volume) string {
	if volume.DataVolume != nil {
		// TODO, look up the correct PVC name based on the datavolume, right now they match, but that will not always be true.
		return volume.DataVolume.Name
	} else if volume.PersistentVolumeClaim != nil {
		return volume.PersistentVolumeClaim.ClaimName
	}
	return ""
}

func VirtVolumesToPVCMap(volumes []*virtv1.Volume, pvcStore cache.Store, namespace string) (map[string]*k8sv1.PersistentVolumeClaim, error) {
	volumeNamesPVCMap := make(map[string]*k8sv1.PersistentVolumeClaim)
	for _, volume := range volumes {
		claimName := PVCNameFromVirtVolume(volume)
		if claimName == "" {
			return nil, fmt.Errorf("volume %s is not a PVC or Datavolume", volume.Name)
		}
		pvc, exists, _, err := IsPVCBlockFromStore(pvcStore, namespace, claimName)
		if err != nil {
			return nil, fmt.Errorf("failed to get PVC: %v", err)
		}
		if !exists {
			return nil, fmt.Errorf("claim %s not found", claimName)
		}
		volumeNamesPVCMap[volume.Name] = pvc
	}
	return volumeNamesPVCMap, nil
}

// IsWaitForFirstConsumer determines whether the given PersistentVolumeClaim has a binding mode of WaitForFirstConsumer.
func IsWaitForFirstConsumer(pvc *k8sv1.PersistentVolumeClaim, storageClassStore cache.Store) (bool, error) {
	sc, err := GetStorageClass(pvc, storageClassStore)
	if err != nil {
		return false, err
	}

	if sc == nil {
		// Statically provisioned volume
		return false, nil
	}

	isWFFC := sc.VolumeBindingMode != nil && *sc.VolumeBindingMode == storagev1.VolumeBindingWaitForFirstConsumer
	return isWFFC, nil
}

// IsStaticallyProvisioned determines whether the PersistentVolumeClaim is a statically provisioned volume.
func IsStaticallyProvisioned(pvc *k8sv1.PersistentVolumeClaim) bool {
	return pvc.Spec.StorageClassName != nil && *pvc.Spec.StorageClassName == ""
}

// IsDynamicallyProvisioned determines whether the PersistentVolumeClaim is a dynamically provisioned volume.
func IsDynamicallyProvisioned(pvc *k8sv1.PersistentVolumeClaim) bool {
	return pvc.Spec.StorageClassName == nil || *pvc.Spec.StorageClassName != ""
}

// GetStorageClass returns the StorageClass associated with the given PersistentVolumeClaim,
// or nil if it's a statically provisioned volume, with no StorageClass associated.
func GetStorageClass(pvc *k8sv1.PersistentVolumeClaim, storageClassStore cache.Store) (*storagev1.StorageClass, error) {
	if IsStaticallyProvisioned(pvc) {
		return nil, nil
	}

	if pvc.Spec.StorageClassName == nil {
		return getDefaultStorageClass(storageClassStore)
	}

	obj, exists, err := storageClassStore.GetByKey(*pvc.Spec.StorageClassName)
	if err != nil {
		return nil, err
	}
	if !exists {
		return nil, fmt.Errorf("StorageClass %s does not exists", *pvc.Spec.StorageClassName)
	}

	sc, ok := obj.(*storagev1.StorageClass)
	if !ok {
		return nil, fmt.Errorf("failed converting %s to a StorageClass: object is of type %T", *pvc.Spec.StorageClassName, obj)
	}

	return sc, nil
}

// getDefaultStorageClass returns the default StorageClass associated with a cluster,
// or nil if no default StorageClass is configured.
func getDefaultStorageClass(storageClassStore cache.Store) (*storagev1.StorageClass, error) {
	for _, obj := range storageClassStore.List() {
		sc, ok := obj.(*storagev1.StorageClass)
		if !ok {
			return nil, fmt.Errorf("failed converting object to a StorageClass: object is of type %T", obj)
		}

		if sc.Annotations["storageclass.kubernetes.io/is-default-class"] == "true" {
			return sc, nil
		}
	}

	return nil, nil
}
