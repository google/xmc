// Copyright 2026 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//      http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package validator

import (
	"testing"

	essmsgpb "xmanager_cloud/experiment_state_server/proto/messages_go_proto"
)

func TestValidateJobVolumes(t *testing.T) {
	tests := []struct {
		name    string
		volumes []*essmsgpb.Volume
		wantErr bool
	}{
		{
			name: "valid_all_sources",
			volumes: []*essmsgpb.Volume{
				{Name: "pvc", Source: &essmsgpb.Volume_PersistentVolumeClaim{PersistentVolumeClaim: &essmsgpb.PersistentVolumeClaimVolumeSource{ClaimName: "my-pvc"}}},
				{Name: "gcs", Source: &essmsgpb.Volume_Gcs{Gcs: &essmsgpb.GcsVolumeSource{Bucket: "my-bucket"}}},
				{Name: "host", Source: &essmsgpb.Volume_HostPath{HostPath: &essmsgpb.HostPathVolumeSource{Path: "/tmp"}}},
				{Name: "empty", Source: &essmsgpb.Volume_EmptyDir{EmptyDir: &essmsgpb.EmptyDirVolumeSource{SizeLimitBytes: 1024}}},
				{Name: "secret", Source: &essmsgpb.Volume_Secret{Secret: &essmsgpb.SecretVolumeSource{SecretName: "my-secret"}}},
				{Name: "cm", Source: &essmsgpb.Volume_ConfigMap{ConfigMap: &essmsgpb.ConfigMapVolumeSource{Name: "my-cm"}}},
				{Name: "nfs", Source: &essmsgpb.Volume_Nfs{Nfs: &essmsgpb.NfsVolumeSource{Server: "nfs.local", Path: "/export"}}},
			},
			wantErr: false,
		},
		{
			name: "empty_volume_name",
			volumes: []*essmsgpb.Volume{
				{Name: "", Source: &essmsgpb.Volume_Gcs{Gcs: &essmsgpb.GcsVolumeSource{Bucket: "bucket"}}},
			},
			wantErr: true,
		},
		{
			name: "duplicate_volume_name",
			volumes: []*essmsgpb.Volume{
				{Name: "dup", Source: &essmsgpb.Volume_Gcs{Gcs: &essmsgpb.GcsVolumeSource{Bucket: "bucket"}}},
				{Name: "dup", Source: &essmsgpb.Volume_HostPath{HostPath: &essmsgpb.HostPathVolumeSource{Path: "/tmp"}}},
			},
			wantErr: true,
		},
		{
			name: "empty_claim_name",
			volumes: []*essmsgpb.Volume{
				{Name: "pvc", Source: &essmsgpb.Volume_PersistentVolumeClaim{PersistentVolumeClaim: &essmsgpb.PersistentVolumeClaimVolumeSource{ClaimName: ""}}},
			},
			wantErr: true,
		},
		{
			name: "empty_bucket_name",
			volumes: []*essmsgpb.Volume{
				{Name: "gcs", Source: &essmsgpb.Volume_Gcs{Gcs: &essmsgpb.GcsVolumeSource{Bucket: ""}}},
			},
			wantErr: true,
		},
		{
			name: "gs_prefix_only_bucket_name",
			volumes: []*essmsgpb.Volume{
				{Name: "gcs", Source: &essmsgpb.Volume_Gcs{Gcs: &essmsgpb.GcsVolumeSource{Bucket: "gs://"}}},
			},
			wantErr: true,
		},
		{
			name: "relative_host_path",
			volumes: []*essmsgpb.Volume{
				{Name: "hp", Source: &essmsgpb.Volume_HostPath{HostPath: &essmsgpb.HostPathVolumeSource{Path: "relative/path"}}},
			},
			wantErr: true,
		},
		{
			name: "negative_size_limit",
			volumes: []*essmsgpb.Volume{
				{Name: "ed", Source: &essmsgpb.Volume_EmptyDir{EmptyDir: &essmsgpb.EmptyDirVolumeSource{SizeLimitBytes: -1}}},
			},
			wantErr: true,
		},
		{
			name: "missing_source",
			volumes: []*essmsgpb.Volume{
				{Name: "nosrc"},
			},
			wantErr: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := validateJobVolumes(tc.volumes, "prefix")
			if (err != nil) != tc.wantErr {
				t.Errorf("validateJobVolumes() error = %v, wantErr %v", err, tc.wantErr)
			}
		})
	}
}

func TestValidateVolumeMounts(t *testing.T) {
	validVols := map[string]bool{"my-vol": true, "ram-scratch": true}
	tests := []struct {
		name    string
		mounts  []*essmsgpb.VolumeMount
		wantErr bool
	}{
		{
			name: "valid_mounts",
			mounts: []*essmsgpb.VolumeMount{
				{Name: "my-vol", MountPath: "/mnt/data"},
				{Name: "ram-scratch", MountPath: "/dev/ram-scratch"},
			},
			wantErr: false,
		},
		{
			name: "empty_name",
			mounts: []*essmsgpb.VolumeMount{
				{Name: "", MountPath: "/mnt/data"},
			},
			wantErr: true,
		},
		{
			name: "unknown_volume",
			mounts: []*essmsgpb.VolumeMount{
				{Name: "unknown-vol", MountPath: "/mnt/data"},
			},
			wantErr: true,
		},
		{
			name: "empty_mount_path",
			mounts: []*essmsgpb.VolumeMount{
				{Name: "my-vol", MountPath: ""},
			},
			wantErr: true,
		},
		{
			name: "relative_mount_path",
			mounts: []*essmsgpb.VolumeMount{
				{Name: "my-vol", MountPath: "relative/path"},
			},
			wantErr: true,
		},
		{
			name: "colon_in_mount_path",
			mounts: []*essmsgpb.VolumeMount{
				{Name: "my-vol", MountPath: "/mnt:data"},
			},
			wantErr: true,
		},
		{
			name: "collision_with_termination_log",
			mounts: []*essmsgpb.VolumeMount{
				{Name: "my-vol", MountPath: "/dev/termination-log"},
			},
			wantErr: true,
		},
		{
			name: "duplicate_mount_path",
			mounts: []*essmsgpb.VolumeMount{
				{Name: "my-vol", MountPath: "/mnt/data"},
				{Name: "ram-scratch", MountPath: "/mnt/data"},
			},
			wantErr: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := validateVolumeMounts(tc.mounts, "prefix", validVols)
			if (err != nil) != tc.wantErr {
				t.Errorf("validateVolumeMounts() error = %v, wantErr %v", err, tc.wantErr)
			}
		})
	}
}
