// Copyright 2026 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package store

import (
	"testing"

	"github.com/google/go-cmp/cmp"
	"google.golang.org/protobuf/testing/protocmp"

	componentspb "xmanager_cloud/api/protos/components/v1alpha1"
)

func TestApplySpecUpdateMask(t *testing.T) {
	base := &componentspb.ComponentSpec{
		Type:           componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_1P_COMPONENT,
		Version:        "v1.0.0",
		DeletionPolicy: componentspb.DeletionPolicy_DELETION_POLICY_DELETE,
		WrapperRef: &componentspb.WrapperRef{
			CrdGroup:   "test.group",
			CrdVersion: "v1",
			CrdKind:    "TestKind",
			TargetName: "old-target",
		},
		LifecycleControl: &componentspb.LifecycleControl{
			ForceDeletion: false,
		},
		Manifests:   []string{"old manifest"},
		Description: "old description",
		Generation:  1,
	}

	tests := []struct {
		name     string
		base     *componentspb.ComponentSpec
		update   *componentspb.ComponentSpec
		paths    []string
		wantSpec *componentspb.ComponentSpec
	}{
		{
			name:     "NilInputs",
			base:     nil,
			update:   nil,
			paths:    []string{"version"},
			wantSpec: nil,
		},
		{
			name:     "NilUpdate",
			base:     base,
			update:   nil,
			paths:    []string{"version"},
			wantSpec: base,
		},
		{
			name:     "EmptyPaths",
			base:     base,
			update:   &componentspb.ComponentSpec{Version: "v2.0.0"},
			paths:    nil,
			wantSpec: base,
		},
		{
			name: "NilBaseWithUpdate",
			base: nil,
			update: &componentspb.ComponentSpec{
				Version:     "v2.0.0",
				Description: "new desc",
			},
			paths: []string{"version", "description"},
			wantSpec: &componentspb.ComponentSpec{
				Version:     "v2.0.0",
				Description: "new desc",
			},
		},
		{
			name: "UpdateVersionOnly",
			base: base,
			update: &componentspb.ComponentSpec{
				Version:     "v2.0.0",
				Description: "ignored desc",
			},
			paths: []string{"version"},
			wantSpec: &componentspb.ComponentSpec{
				Type:             base.Type,
				Version:          "v2.0.0",
				DeletionPolicy:   base.DeletionPolicy,
				WrapperRef:       base.WrapperRef,
				LifecycleControl: base.LifecycleControl,
				Manifests:        base.Manifests,
				Description:      base.Description,
				Generation:       base.Generation,
			},
		},
		{
			name: "UpdateTypeAndDeletionPolicySnakeCase",
			base: base,
			update: &componentspb.ComponentSpec{
				Type:           componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_3P_WRAPPER,
				DeletionPolicy: componentspb.DeletionPolicy_DELETION_POLICY_ORPHAN,
			},
			paths: []string{"type", "deletion_policy"},
			wantSpec: &componentspb.ComponentSpec{
				Type:             componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_3P_WRAPPER,
				Version:          base.Version,
				DeletionPolicy:   componentspb.DeletionPolicy_DELETION_POLICY_ORPHAN,
				WrapperRef:       base.WrapperRef,
				LifecycleControl: base.LifecycleControl,
				Manifests:        base.Manifests,
				Description:      base.Description,
				Generation:       base.Generation,
			},
		},
		{
			name: "UpdateDeletionPolicyCamelCase",
			base: base,
			update: &componentspb.ComponentSpec{
				DeletionPolicy: componentspb.DeletionPolicy_DELETION_POLICY_ORPHAN,
			},
			paths: []string{"deletionPolicy"},
			wantSpec: &componentspb.ComponentSpec{
				Type:             base.Type,
				Version:          base.Version,
				DeletionPolicy:   componentspb.DeletionPolicy_DELETION_POLICY_ORPHAN,
				WrapperRef:       base.WrapperRef,
				LifecycleControl: base.LifecycleControl,
				Manifests:        base.Manifests,
				Description:      base.Description,
				Generation:       base.Generation,
			},
		},
		{
			name: "UpdateWrapperRefAndLifecycleControl",
			base: base,
			update: &componentspb.ComponentSpec{
				WrapperRef: &componentspb.WrapperRef{
					CrdGroup:   "new.group",
					CrdVersion: "v2",
					CrdKind:    "NewKind",
					TargetName: "new-target",
				},
				LifecycleControl: &componentspb.LifecycleControl{
					ForceDeletion: true,
				},
			},
			paths: []string{"wrapper_ref", "lifecycle_control"},
			wantSpec: &componentspb.ComponentSpec{
				Type:           base.Type,
				Version:        base.Version,
				DeletionPolicy: base.DeletionPolicy,
				WrapperRef: &componentspb.WrapperRef{
					CrdGroup:   "new.group",
					CrdVersion: "v2",
					CrdKind:    "NewKind",
					TargetName: "new-target",
				},
				LifecycleControl: &componentspb.LifecycleControl{
					ForceDeletion: true,
				},
				Manifests:   base.Manifests,
				Description: base.Description,
				Generation:  base.Generation,
			},
		},
		{
			name: "ClearWrapperRefAndLifecycleControlWithCamelCase",
			base: base,
			update: &componentspb.ComponentSpec{
				WrapperRef:       nil,
				LifecycleControl: nil,
			},
			paths: []string{"wrapperRef", "lifecycleControl"},
			wantSpec: &componentspb.ComponentSpec{
				Type:             base.Type,
				Version:          base.Version,
				DeletionPolicy:   base.DeletionPolicy,
				WrapperRef:       nil,
				LifecycleControl: nil,
				Manifests:        base.Manifests,
				Description:      base.Description,
				Generation:       base.Generation,
			},
		},
		{
			name: "UpdateManifestsDescriptionAndGeneration",
			base: base,
			update: &componentspb.ComponentSpec{
				Manifests:   []string{"new manifest 1", "new manifest 2"},
				Description: "brand new description",
				Generation:  5,
			},
			paths: []string{"manifests", "description", "generation"},
			wantSpec: &componentspb.ComponentSpec{
				Type:             base.Type,
				Version:          base.Version,
				DeletionPolicy:   base.DeletionPolicy,
				WrapperRef:       base.WrapperRef,
				LifecycleControl: base.LifecycleControl,
				Manifests:        []string{"new manifest 1", "new manifest 2"},
				Description:      "brand new description",
				Generation:       5,
			},
		},
		{
			name: "ClearManifests",
			base: base,
			update: &componentspb.ComponentSpec{
				Manifests: nil,
			},
			paths: []string{"manifests"},
			wantSpec: &componentspb.ComponentSpec{
				Type:             base.Type,
				Version:          base.Version,
				DeletionPolicy:   base.DeletionPolicy,
				WrapperRef:       base.WrapperRef,
				LifecycleControl: base.LifecycleControl,
				Manifests:        nil,
				Description:      base.Description,
				Generation:       base.Generation,
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := ApplySpecUpdateMask(tc.base, tc.update, tc.paths)
			if diff := cmp.Diff(tc.wantSpec, got, protocmp.Transform()); diff != "" {
				t.Errorf("ApplySpecUpdateMask() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}
