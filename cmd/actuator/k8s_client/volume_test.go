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
	"testing"

	"github.com/google/go-cmp/cmp"
	corev1 "golang/k8s_io/api/v/v0_23/core/v1/v1"
	"golang/protobuf/v2/proto/proto"
	esspb "xmanager_cloud/experiment_state_server/proto/messages_go_proto"
)

func TestConstructJobSet_CustomVolumesAndMounts(t *testing.T) {
	ctx, client, _ := setupTestClient(true /* createNewNamespaces */, &alwaysPassingPreconditionsValidator{})
	workUnit := createWorkUnitWithVolumesAndMounts()
	got, err := client.constructJobSet(ctx, workUnit, testTarget, "test-author", "test-ns", testTime, Priorities{})
	if err != nil {
		t.Fatalf("constructJobSet() returned unexpected error: %v", err)
	}
	if len(got.Spec.ReplicatedJobs) != 1 {
		t.Fatalf("Expected 1 ReplicatedJob, got %d", len(got.Spec.ReplicatedJobs))
	}
	podSpec := got.Spec.ReplicatedJobs[0].Template.Spec.Template.Spec
	verifyCustomVolumes(t, podSpec.Volumes)
	verifyCustomVolumeMounts(t, podSpec.Containers[0].VolumeMounts)
}

func createWorkUnitWithVolumesAndMounts() *esspb.WorkUnit {
	wu := createTestWorkUnit("test-ns")
	job := wu.Executable.GetJobset().GetJobs()[0]
	job.Volumes = []*esspb.Volume{
		{Name: "ram-scratch", Source: &esspb.Volume_EmptyDir{EmptyDir: &esspb.EmptyDirVolumeSource{Medium: "Memory", SizeLimitBytes: 1048576}}},
		{Name: "pvc-data", Source: &esspb.Volume_PersistentVolumeClaim{PersistentVolumeClaim: &esspb.PersistentVolumeClaimVolumeSource{ClaimName: "my-claim", ReadOnly: proto.Bool(true)}}},
		{Name: "gcs-data", Source: &esspb.Volume_Gcs{Gcs: &esspb.GcsVolumeSource{Bucket: "gs://my-bucket", ReadOnly: proto.Bool(false), MountOptions: []string{"implicit-dirs"}}}},
		{Name: "gcs-data-nil", Source: &esspb.Volume_Gcs{Gcs: &esspb.GcsVolumeSource{Bucket: "gs://my-bucket-nil"}}},
		{Name: "host-data", Source: &esspb.Volume_HostPath{HostPath: &esspb.HostPathVolumeSource{Path: "/tmp/host", Type: "Directory"}}},
		{Name: "host-data-nil", Source: &esspb.Volume_HostPath{HostPath: &esspb.HostPathVolumeSource{Path: "/tmp/host-nil"}}},
		{Name: "sec-data-zero", Source: &esspb.Volume_Secret{Secret: &esspb.SecretVolumeSource{SecretName: "my-secret-zero", DefaultMode: proto.Int32(0), Optional: proto.Bool(true)}}},
		{Name: "sec-data-nil", Source: &esspb.Volume_Secret{Secret: &esspb.SecretVolumeSource{SecretName: "my-secret-nil"}}},
		{Name: "cm-data-zero", Source: &esspb.Volume_ConfigMap{ConfigMap: &esspb.ConfigMapVolumeSource{Name: "my-cm-zero", DefaultMode: proto.Int32(0), Optional: proto.Bool(false)}}},
		{Name: "cm-data-nil", Source: &esspb.Volume_ConfigMap{ConfigMap: &esspb.ConfigMapVolumeSource{Name: "my-cm-nil"}}},
	}
	job.Spec.VolumeMounts = []*esspb.VolumeMount{
		{Name: "ram-scratch", MountPath: "/dev/ram-scratch", ReadOnly: proto.Bool(false)},
		{Name: "pvc-data", MountPath: "/mnt/pvc", ReadOnly: proto.Bool(true)},
		{Name: "gcs-data", MountPath: "/mnt/gcs", SubPath: "sub"},
	}
	return wu
}

func verifyCustomVolumes(t *testing.T, volumes []corev1.Volume) {
	t.Helper()
	volMap := make(map[string]corev1.Volume)
	for _, v := range volumes {
		volMap[v.Name] = v
	}
	if volMap["ram-scratch"].EmptyDir == nil || volMap["ram-scratch"].EmptyDir.SizeLimit == nil {
		t.Errorf("Expected overridden ram-scratch volume with SizeLimit, got: %+v", volMap["ram-scratch"])
	}
	if pvc := volMap["pvc-data"].PersistentVolumeClaim; pvc == nil || pvc.ClaimName != "my-claim" || !pvc.ReadOnly {
		t.Errorf("Expected pvc-data volume with ClaimName 'my-claim' and ReadOnly true, got: %+v", pvc)
	}
	if csi := volMap["gcs-data"].CSI; csi == nil || csi.Driver != "gcs.csi.storage.gke.io" || csi.VolumeAttributes["bucketName"] != "my-bucket" || csi.ReadOnly == nil || *csi.ReadOnly {
		t.Errorf("Expected gcs-data CSI volume with ReadOnly false, got: %+v", csi)
	}
	if csi := volMap["gcs-data-nil"].CSI; csi == nil || csi.Driver != "gcs.csi.storage.gke.io" || csi.VolumeAttributes["bucketName"] != "my-bucket-nil" || csi.ReadOnly != nil {
		t.Errorf("Expected gcs-data-nil CSI volume with ReadOnly nil, got: %+v", csi)
	}
	if hp := volMap["host-data"].HostPath; hp == nil || hp.Path != "/tmp/host" || hp.Type == nil || *hp.Type != "Directory" {
		t.Errorf("Expected host-data HostPath volume with Type 'Directory', got: %+v", hp)
	}
	if hp := volMap["host-data-nil"].HostPath; hp == nil || hp.Path != "/tmp/host-nil" || hp.Type != nil {
		t.Errorf("Expected host-data-nil HostPath volume with Type nil, got: %+v", hp)
	}
	if sec := volMap["sec-data-zero"].Secret; sec == nil || sec.SecretName != "my-secret-zero" || sec.DefaultMode == nil || *sec.DefaultMode != 0 || sec.Optional == nil || !*sec.Optional {
		t.Errorf("Expected sec-data-zero Secret volume with DefaultMode 0 and Optional true, got: %+v", sec)
	}
	if sec := volMap["sec-data-nil"].Secret; sec == nil || sec.SecretName != "my-secret-nil" || sec.DefaultMode != nil || sec.Optional != nil {
		t.Errorf("Expected sec-data-nil Secret volume with DefaultMode nil and Optional nil, got: %+v", sec)
	}
	if cm := volMap["cm-data-zero"].ConfigMap; cm == nil || cm.LocalObjectReference.Name != "my-cm-zero" || cm.DefaultMode == nil || *cm.DefaultMode != 0 || cm.Optional == nil || *cm.Optional {
		t.Errorf("Expected cm-data-zero ConfigMap volume with DefaultMode 0 and Optional false, got: %+v", cm)
	}
	if cm := volMap["cm-data-nil"].ConfigMap; cm == nil || cm.LocalObjectReference.Name != "my-cm-nil" || cm.DefaultMode != nil || cm.Optional != nil {
		t.Errorf("Expected cm-data-nil ConfigMap volume with DefaultMode nil and Optional nil, got: %+v", cm)
	}
}

func verifyCustomVolumeMounts(t *testing.T, mounts []corev1.VolumeMount) {
	t.Helper()
	mountMap := make(map[string]corev1.VolumeMount)
	for _, m := range mounts {
		mountMap[m.Name] = m
	}
	if mountMap["pvc-data"].MountPath != "/mnt/pvc" || !mountMap["pvc-data"].ReadOnly {
		t.Errorf("Expected pvc-data mount at /mnt/pvc ReadOnly, got: %+v", mountMap["pvc-data"])
	}
	if mountMap["gcs-data"].MountPath != "/mnt/gcs" || mountMap["gcs-data"].SubPath != "sub" {
		t.Errorf("Expected gcs-data mount at /mnt/gcs with SubPath 'sub', got: %+v", mountMap["gcs-data"])
	}
}

func TestGenerateVolumeMountsForSpec_Collision(t *testing.T) {
	specA := &esspb.ExecutableSpec{
		VolumeMounts: []*esspb.VolumeMount{
			{Name: "/dev/shm", MountPath: "/mnt/custom-shm"},
		},
	}
	mountsA := generateVolumeMountsForSpec(specA)
	if len(mountsA) != 4 {
		t.Errorf("Expected 4 mounts, got %d. Mounts: %+v", len(mountsA), mountsA)
	}

	specB := &esspb.ExecutableSpec{
		VolumeMounts: []*esspb.VolumeMount{
			{Name: "custom-ram", MountPath: "ram-scratch"},
		},
	}
	mountsB := generateVolumeMountsForSpec(specB)
	if len(mountsB) != 4 {
		t.Errorf("Expected 4 mounts, got %d. Mounts: %+v", len(mountsB), mountsB)
	}

	specC := &esspb.ExecutableSpec{
		VolumeMounts: []*esspb.VolumeMount{
			{Name: "ram-scratch", MountPath: "/my-custom-ram"},
			{Name: "new-mount", MountPath: "/dev/ram-scratch"},
		},
	}
	mountsC := generateVolumeMountsForSpec(specC)
	if len(mountsC) != 4 {
		t.Errorf("Expected 4 mounts, got %d. Mounts: %+v", len(mountsC), mountsC)
	}
	hasCustom1 := false
	hasCustom2 := false
	for _, m := range mountsC {
		if m.Name == "ram-scratch" && m.MountPath == "/my-custom-ram" {
			hasCustom1 = true
		}
		if m.Name == "new-mount" && m.MountPath == "/dev/ram-scratch" {
			hasCustom2 = true
		}
	}
	if !hasCustom1 || !hasCustom2 {
		t.Errorf("Expected both custom mounts to be present. Custom1: %t, Custom2: %t. Mounts: %+v", hasCustom1, hasCustom2, mountsC)
	}

	specD := &esspb.ExecutableSpec{
		VolumeMounts: []*esspb.VolumeMount{
			{Name: "new-name", MountPath: "/dev/ram-scratch"},
			{Name: "ram-scratch", MountPath: "/new-path"},
		},
	}
	mountsD := generateVolumeMountsForSpec(specD)
	if len(mountsD) != 4 {
		t.Errorf("Expected 4 mounts, got %d. Mounts: %+v", len(mountsD), mountsD)
	}
	hasCustomD1 := false
	hasCustomD2 := false
	for _, m := range mountsD {
		if m.Name == "new-name" && m.MountPath == "/dev/ram-scratch" {
			hasCustomD1 = true
		}
		if m.Name == "ram-scratch" && m.MountPath == "/new-path" {
			hasCustomD2 = true
		}
	}
	if !hasCustomD1 || !hasCustomD2 {
		t.Errorf("Expected both custom mounts to be present (Case D). Custom1: %t, Custom2: %t. Mounts: %+v", hasCustomD1, hasCustomD2, mountsD)
	}
}

func TestBuildSecretVolumeSource(t *testing.T) {
	tests := []struct {
		name string
		in   *esspb.SecretVolumeSource
		want *corev1.SecretVolumeSource
	}{
		{
			name: "all fields specified",
			in: &esspb.SecretVolumeSource{
				SecretName:  "my-secret",
				DefaultMode: proto.Int32(0644),
				Optional:    proto.Bool(true),
			},
			want: &corev1.SecretVolumeSource{
				SecretName:  "my-secret",
				DefaultMode: proto.Int32(0644),
				Optional:    proto.Bool(true),
			},
		},
		{
			name: "nil optional fields",
			in: &esspb.SecretVolumeSource{
				SecretName: "minimal-secret",
			},
			want: &corev1.SecretVolumeSource{
				SecretName: "minimal-secret",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := buildSecretVolumeSource(tt.in)
			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("buildSecretVolumeSource() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestBuildConfigMapVolumeSource(t *testing.T) {
	tests := []struct {
		name string
		in   *esspb.ConfigMapVolumeSource
		want *corev1.ConfigMapVolumeSource
	}{
		{
			name: "all fields specified",
			in: &esspb.ConfigMapVolumeSource{
				Name:        "my-configmap",
				DefaultMode: proto.Int32(0755),
				Optional:    proto.Bool(false),
			},
			want: &corev1.ConfigMapVolumeSource{
				LocalObjectReference: corev1.LocalObjectReference{Name: "my-configmap"},
				DefaultMode:          proto.Int32(0755),
				Optional:             proto.Bool(false),
			},
		},
		{
			name: "nil optional fields",
			in: &esspb.ConfigMapVolumeSource{
				Name: "minimal-configmap",
			},
			want: &corev1.ConfigMapVolumeSource{
				LocalObjectReference: corev1.LocalObjectReference{Name: "minimal-configmap"},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := buildConfigMapVolumeSource(tt.in)
			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("buildConfigMapVolumeSource() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestBuildNFSVolumeSource(t *testing.T) {
	tests := []struct {
		name string
		in   *esspb.NfsVolumeSource
		want *corev1.NFSVolumeSource
	}{
		{
			name: "read-only explicit true",
			in: &esspb.NfsVolumeSource{
				Server:   "10.0.0.1",
				Path:     "/exports/data",
				ReadOnly: proto.Bool(true),
			},
			want: &corev1.NFSVolumeSource{
				Server:   "10.0.0.1",
				Path:     "/exports/data",
				ReadOnly: true,
			},
		},
		{
			name: "read-only explicit false",
			in: &esspb.NfsVolumeSource{
				Server:   "10.0.0.2",
				Path:     "/exports/scratch",
				ReadOnly: proto.Bool(false),
			},
			want: &corev1.NFSVolumeSource{
				Server:   "10.0.0.2",
				Path:     "/exports/scratch",
				ReadOnly: false,
			},
		},
		{
			name: "read-only nil defaults to false",
			in: &esspb.NfsVolumeSource{
				Server: "10.0.0.3",
				Path:   "/exports/default",
			},
			want: &corev1.NFSVolumeSource{
				Server:   "10.0.0.3",
				Path:     "/exports/default",
				ReadOnly: false,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := buildNFSVolumeSource(tt.in)
			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("buildNFSVolumeSource() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestBuildGCSVolumeSource(t *testing.T) {
	tests := []struct {
		name string
		in   *esspb.GcsVolumeSource
		want *corev1.CSIVolumeSource
	}{
		{
			name: "bucket with gs:// prefix and single mount option",
			in: &esspb.GcsVolumeSource{
				Bucket:       "gs://my-bucket",
				ReadOnly:     proto.Bool(true),
				MountOptions: []string{"implicit-dirs"},
			},
			want: &corev1.CSIVolumeSource{
				Driver:   "gcs.csi.storage.gke.io",
				ReadOnly: proto.Bool(true),
				VolumeAttributes: map[string]string{
					"bucketName":   "my-bucket",
					"mountOptions": "implicit-dirs",
				},
			},
		},
		{
			name: "bucket without gs:// prefix and multiple mount options joined with comma",
			in: &esspb.GcsVolumeSource{
				Bucket:       "my-bucket-no-prefix",
				ReadOnly:     proto.Bool(false),
				MountOptions: []string{"implicit-dirs", "dir-mode=0755", "file-mode=0644", "uid=1000"},
			},
			want: &corev1.CSIVolumeSource{
				Driver:   "gcs.csi.storage.gke.io",
				ReadOnly: proto.Bool(false),
				VolumeAttributes: map[string]string{
					"bucketName":   "my-bucket-no-prefix",
					"mountOptions": "implicit-dirs,dir-mode=0755,file-mode=0644,uid=1000",
				},
			},
		},
		{
			name: "no mount options specified",
			in: &esspb.GcsVolumeSource{
				Bucket: "gs://no-options-bucket",
			},
			want: &corev1.CSIVolumeSource{
				Driver: "gcs.csi.storage.gke.io",
				VolumeAttributes: map[string]string{
					"bucketName": "no-options-bucket",
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := buildGCSVolumeSource(tt.in)
			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("buildGCSVolumeSource() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}
