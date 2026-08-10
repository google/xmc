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

// Package validator provides request validation for the XManager Cloud services.
package validator

import (
	"fmt"
	"strings"

	"task/go/status"
	essmsgpb "xmanager_cloud/experiment_state_server/proto/messages_go_proto"
)

func validateJobVolumes(volumes []*essmsgpb.Volume, prefix string) (map[string]bool, error) {
	validNames := map[string]bool{
		"ram-scratch":     true,
		"disk-scratch":    true,
		"dev-shm-scratch": true,
	}
	seenCustomNames := make(map[string]bool)
	for i, v := range volumes {
		volPrefix := fmt.Sprintf("%s[%d]", prefix, i)
		if v.GetName() == "" {
			return nil, fmt.Errorf("%s.name cannot be empty: %w", volPrefix, status.ErrInvalidArgument)
		}
		if err := validateDNSLabel(v.GetName(), volPrefix+".name"); err != nil {
			return nil, err
		}
		if seenCustomNames[v.GetName()] {
			return nil, fmt.Errorf("%s.name %q must be unique within the job: %w", volPrefix, v.GetName(), status.ErrInvalidArgument)
		}
		seenCustomNames[v.GetName()] = true
		validNames[v.GetName()] = true
		if err := validateVolumeSource(v, volPrefix); err != nil {
			return nil, err
		}
	}
	return validNames, nil
}

func validateVolumeSource(v *essmsgpb.Volume, prefix string) error {
	if v.GetSource() == nil {
		return fmt.Errorf("%s must have a source specified: %w", prefix, status.ErrInvalidArgument)
	}
	switch s := v.Source.(type) {
	case *essmsgpb.Volume_PersistentVolumeClaim, *essmsgpb.Volume_Gcs, *essmsgpb.Volume_HostPath, *essmsgpb.Volume_Nfs:
		return validateStorageVolume(s, prefix)
	case *essmsgpb.Volume_EmptyDir, *essmsgpb.Volume_Secret, *essmsgpb.Volume_ConfigMap:
		return validateConfigVolume(s, prefix)
	default:
		return fmt.Errorf("%s has unsupported volume source type %T: %w", prefix, s, status.ErrInvalidArgument)
	}
}

func validateStorageVolume(source any, prefix string) error {
	switch s := source.(type) {
	case *essmsgpb.Volume_PersistentVolumeClaim:
		pvc := s.PersistentVolumeClaim
		if pvc.GetClaimName() == "" {
			return fmt.Errorf("%s.persistent_volume_claim.claim_name cannot be empty: %w", prefix, status.ErrInvalidArgument)
		}
		return validateDNSLabel(pvc.GetClaimName(), prefix+".persistent_volume_claim.claim_name")
	case *essmsgpb.Volume_Gcs:
		gcs := s.Gcs
		bucket := strings.TrimPrefix(gcs.GetBucket(), "gs://")
		if bucket == "" {
			return fmt.Errorf("%s.gcs.bucket must be a valid GCS bucket name: %w", prefix, status.ErrInvalidArgument)
		}
		return nil
	case *essmsgpb.Volume_HostPath:
		hp := s.HostPath
		if hp.GetPath() == "" {
			return fmt.Errorf("%s.host_path.path cannot be empty: %w", prefix, status.ErrInvalidArgument)
		}
		if !strings.HasPrefix(hp.GetPath(), "/") {
			return fmt.Errorf("%s.host_path.path %q must be an absolute path: %w", prefix, hp.GetPath(), status.ErrInvalidArgument)
		}
		return nil
	case *essmsgpb.Volume_Nfs:
		nfs := s.Nfs
		if nfs.GetServer() == "" {
			return fmt.Errorf("%s.nfs.server cannot be empty: %w", prefix, status.ErrInvalidArgument)
		}
		if nfs.GetPath() == "" {
			return fmt.Errorf("%s.nfs.path cannot be empty: %w", prefix, status.ErrInvalidArgument)
		}
		return nil
	default:
		return fmt.Errorf("%s has unsupported volume source type %T: %w", prefix, s, status.ErrInvalidArgument)
	}
}

func validateConfigVolume(source any, prefix string) error {
	switch s := source.(type) {
	case *essmsgpb.Volume_EmptyDir:
		ed := s.EmptyDir
		if ed.GetSizeLimitBytes() < 0 {
			return fmt.Errorf("%s.empty_dir.size_limit_bytes cannot be negative: %w", prefix, status.ErrInvalidArgument)
		}
		return nil
	case *essmsgpb.Volume_Secret:
		sec := s.Secret
		if sec.GetSecretName() == "" {
			return fmt.Errorf("%s.secret.secret_name cannot be empty: %w", prefix, status.ErrInvalidArgument)
		}
		return nil
	case *essmsgpb.Volume_ConfigMap:
		cm := s.ConfigMap
		if cm.GetName() == "" {
			return fmt.Errorf("%s.config_map.name cannot be empty: %w", prefix, status.ErrInvalidArgument)
		}
		return nil
	default:
		return fmt.Errorf("%s has unsupported volume source type %T: %w", prefix, s, status.ErrInvalidArgument)
	}
}

func validateVolumeMounts(mounts []*essmsgpb.VolumeMount, prefix string, validVolumes map[string]bool) error {
	seenPaths := make(map[string]bool)
	for i, vm := range mounts {
		mPrefix := fmt.Sprintf("%s.volume_mounts[%d]", prefix, i)
		if vm.GetName() == "" {
			return fmt.Errorf("%s.name cannot be empty: %w", mPrefix, status.ErrInvalidArgument)
		}
		if validVolumes != nil && !validVolumes[vm.GetName()] {
			return fmt.Errorf("%s.name %q must reference a defined job volume or default scratch volume: %w", mPrefix, vm.GetName(), status.ErrInvalidArgument)
		}
		if vm.GetMountPath() == "" {
			return fmt.Errorf("%s.mount_path cannot be empty: %w", mPrefix, status.ErrInvalidArgument)
		}
		if !strings.HasPrefix(vm.GetMountPath(), "/") {
			return fmt.Errorf("%s.mount_path %q must be an absolute path: %w", mPrefix, vm.GetMountPath(), status.ErrInvalidArgument)
		}
		if strings.Contains(vm.GetMountPath(), ":") {
			return fmt.Errorf("%s.mount_path %q cannot contain ':': %w", mPrefix, vm.GetMountPath(), status.ErrInvalidArgument)
		}
		if vm.GetMountPath() == "/dev/termination-log" {
			return fmt.Errorf("%s.mount_path %q collides with reserved termination-log path: %w", mPrefix, vm.GetMountPath(), status.ErrInvalidArgument)
		}
		if seenPaths[vm.GetMountPath()] {
			return fmt.Errorf("%s.mount_path %q cannot be duplicated in the same container: %w", mPrefix, vm.GetMountPath(), status.ErrInvalidArgument)
		}
		seenPaths[vm.GetMountPath()] = true
	}
	return nil
}
