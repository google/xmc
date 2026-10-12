// Copyright 2026 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//	http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.
package k8sclient

import (
	"strings"

	esspb "github.com/google/xmc/api/protos/experiment"
	corev1 "k8s.io/api/core/v1"
	resource "k8s.io/apimachinery/pkg/api/resource"
)

func defaultVolumes() []corev1.Volume {
	return []corev1.Volume{
		{
			Name: "ram-scratch",
			VolumeSource: corev1.VolumeSource{
				EmptyDir: &corev1.EmptyDirVolumeSource{
					Medium: corev1.StorageMediumMemory,
				},
			},
		},
		{
			Name: "disk-scratch",
			VolumeSource: corev1.VolumeSource{
				EmptyDir: &corev1.EmptyDirVolumeSource{
					Medium: corev1.StorageMediumDefault,
				},
			},
		},
		{
			Name: "dev-shm-scratch",
			VolumeSource: corev1.VolumeSource{
				EmptyDir: &corev1.EmptyDirVolumeSource{
					Medium: corev1.StorageMediumMemory,
				},
			},
		},
	}
}

func defaultVolumeMounts() []corev1.VolumeMount {
	return []corev1.VolumeMount{
		{
			Name:      "ram-scratch",
			MountPath: "/dev/ram-scratch",
		},
		{
			Name:      "disk-scratch",
			MountPath: "/dev/disk-scratch",
		},
		{
			Name:      "dev-shm-scratch",
			MountPath: "/dev/shm",
		},
	}
}

func generateVolumesForJob(job *esspb.KubernetesJob) []corev1.Volume {
	volumes := defaultVolumes()
	volIndex := make(map[string]int, len(volumes))
	for i, v := range volumes {
		volIndex[v.Name] = i
	}
	for _, v := range job.GetVolumes() {
		vol := buildVolumeFromProto(v)
		if idx, exists := volIndex[v.GetName()]; exists {
			volumes[idx] = vol
			continue
		}
		volIndex[v.GetName()] = len(volumes)
		volumes = append(volumes, vol)
	}
	return volumes
}

func buildVolumeFromProto(v *esspb.Volume) corev1.Volume {
	vol := corev1.Volume{Name: v.GetName()}
	if v.Source == nil {
		return vol
	}
	switch s := v.Source.(type) {
	case *esspb.Volume_PersistentVolumeClaim:
		vol.PersistentVolumeClaim = buildPVCVolumeSource(s.PersistentVolumeClaim)
	case *esspb.Volume_Gcs:
		vol.CSI = buildGCSVolumeSource(s.Gcs)
	case *esspb.Volume_HostPath:
		vol.HostPath = buildHostPathVolumeSource(s.HostPath)
	case *esspb.Volume_EmptyDir:
		vol.EmptyDir = buildEmptyDirVolumeSource(s.EmptyDir)
	case *esspb.Volume_Secret:
		vol.Secret = buildSecretVolumeSource(s.Secret)
	case *esspb.Volume_ConfigMap:
		vol.ConfigMap = buildConfigMapVolumeSource(s.ConfigMap)
	case *esspb.Volume_Nfs:
		vol.NFS = buildNFSVolumeSource(s.Nfs)
	}
	return vol
}

func buildPVCVolumeSource(pvc *esspb.PersistentVolumeClaimVolumeSource) *corev1.PersistentVolumeClaimVolumeSource {
	var readOnly bool
	if pvc.ReadOnly != nil {
		readOnly = pvc.GetReadOnly()
	}
	return &corev1.PersistentVolumeClaimVolumeSource{
		ClaimName: pvc.GetClaimName(),
		ReadOnly:  readOnly,
	}
}

func buildGCSVolumeSource(gcs *esspb.GcsVolumeSource) *corev1.CSIVolumeSource {
	bucket := strings.TrimPrefix(gcs.GetBucket(), "gs://")
	attrs := map[string]string{
		"bucketName": bucket,
	}
	if len(gcs.GetMountOptions()) > 0 {
		attrs["mountOptions"] = strings.Join(gcs.GetMountOptions(), ",")
	}
	return &corev1.CSIVolumeSource{
		Driver:           "gcs.csi.storage.gke.io",
		ReadOnly:         gcs.ReadOnly,
		VolumeAttributes: attrs,
	}
}

func buildHostPathVolumeSource(hp *esspb.HostPathVolumeSource) *corev1.HostPathVolumeSource {
	src := &corev1.HostPathVolumeSource{
		Path: hp.GetPath(),
	}
	if hp.GetType() != "" {
		hpType := corev1.HostPathType(hp.GetType())
		src.Type = &hpType
	}
	return src
}

func buildEmptyDirVolumeSource(ed *esspb.EmptyDirVolumeSource) *corev1.EmptyDirVolumeSource {
	medium := corev1.StorageMedium(ed.GetMedium())
	var sizeLimit *resource.Quantity
	if ed.GetSizeLimitBytes() > 0 {
		q := resource.NewQuantity(ed.GetSizeLimitBytes(), resource.BinarySI)
		sizeLimit = q
	}
	return &corev1.EmptyDirVolumeSource{
		Medium:    medium,
		SizeLimit: sizeLimit,
	}
}

func buildSecretVolumeSource(sec *esspb.SecretVolumeSource) *corev1.SecretVolumeSource {
	return &corev1.SecretVolumeSource{
		SecretName:  sec.GetSecretName(),
		DefaultMode: sec.DefaultMode,
		Optional:    sec.Optional,
	}
}

func buildConfigMapVolumeSource(cm *esspb.ConfigMapVolumeSource) *corev1.ConfigMapVolumeSource {
	return &corev1.ConfigMapVolumeSource{
		LocalObjectReference: corev1.LocalObjectReference{Name: cm.GetName()},
		DefaultMode:          cm.DefaultMode,
		Optional:             cm.Optional,
	}
}

func buildNFSVolumeSource(nfs *esspb.NfsVolumeSource) *corev1.NFSVolumeSource {
	var readOnly bool
	if nfs.ReadOnly != nil {
		readOnly = nfs.GetReadOnly()
	}
	return &corev1.NFSVolumeSource{
		Server:   nfs.GetServer(),
		Path:     nfs.GetPath(),
		ReadOnly: readOnly,
	}
}

func generateVolumeMountsForSpec(spec *esspb.ExecutableSpec) []corev1.VolumeMount {
	mounts := defaultVolumeMounts()
	nameIndex := make(map[string]int, len(mounts))
	pathIndex := make(map[string]int, len(mounts))
	for i, m := range mounts {
		nameIndex[m.Name] = i
		pathIndex[m.MountPath] = i
	}
	for _, vm := range spec.GetVolumeMounts() {
		m := buildVolumeMountFromProto(vm)
		if idx, exists := nameIndex[vm.GetName()]; exists {
			delete(pathIndex, mounts[idx].MountPath)
			pathIndex[vm.GetMountPath()] = idx
			mounts[idx] = m
			continue
		}
		if idx, exists := pathIndex[vm.GetMountPath()]; exists {
			delete(nameIndex, mounts[idx].Name)
			nameIndex[vm.GetName()] = idx
			mounts[idx] = m
			continue
		}
		nameIndex[vm.GetName()] = len(mounts)
		pathIndex[vm.GetMountPath()] = len(mounts)
		mounts = append(mounts, m)
	}
	return mounts
}

func buildVolumeMountFromProto(vm *esspb.VolumeMount) corev1.VolumeMount {
	var readOnly bool
	if vm.ReadOnly != nil {
		readOnly = vm.GetReadOnly()
	}
	return corev1.VolumeMount{
		Name:      vm.GetName(),
		MountPath: vm.GetMountPath(),
		ReadOnly:  readOnly,
		SubPath:   vm.GetSubPath(),
	}
}
