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

package spannerdata

import (
	"errors"
	"testing"

	componentspb "xmanager_cloud/api/protos/components/v1alpha1"
	"xmanager_cloud/internal/grpcerr"
	"xmanager_cloud/pkg/components/store"
)

func TestComponentMutationBuilders(t *testing.T) {
	spec := &componentspb.ComponentSpec{
		Type:    componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_1P_COMPONENT,
		Version: "v1.2.0",
	}
	owner := &store.SourceOwner{UID: "uid-a", Generation: 3, Namespace: "xmc", ClusterUID: "cluster-a", Epoch: 2}

	if _, err := ComponentInsertMutations(nil, nil); !errors.Is(err, grpcerr.ErrInvalidArgument) {
		t.Errorf("ComponentInsertMutations(nil) error = %v, want %v", err, grpcerr.ErrInvalidArgument)
	}
	if _, err := ComponentIntentUpdateMutations(nil, nil); !errors.Is(err, grpcerr.ErrInvalidArgument) {
		t.Errorf("ComponentIntentUpdateMutations(nil) error = %v, want %v", err, grpcerr.ErrInvalidArgument)
	}
	if _, err := ComponentObservationUpdateMutation(nil, false); !errors.Is(err, grpcerr.ErrInvalidArgument) {
		t.Errorf("ComponentObservationUpdateMutation(nil) error = %v, want %v", err, grpcerr.ErrInvalidArgument)
	}
	if _, err := ComponentSoftDeleteMutation(nil, nil); !errors.Is(err, grpcerr.ErrInvalidArgument) {
		t.Errorf("ComponentSoftDeleteMutation(nil) error = %v, want %v", err, grpcerr.ErrInvalidArgument)
	}
	if m := ComponentOwnerUpdateMutation(1, nil); m != nil {
		t.Errorf("ComponentOwnerUpdateMutation(1, nil) = %v, want nil", m)
	}
	if m := ComponentOwnerUpdateMutation(1, owner); m == nil {
		t.Error("ComponentOwnerUpdateMutation(1, owner) = nil, want non-nil")
	}

	rec := &store.ComponentRecord{ComponentID: 1, Name: "app", Spec: spec, Dependencies: []string{"db"}}
	if _, err := ComponentInsertMutations(rec, nil); !errors.Is(err, grpcerr.ErrFailedPrecondition) {
		t.Errorf("ComponentInsertMutations(missing dep) error = %v, want %v", err, grpcerr.ErrFailedPrecondition)
	}
	if muts, err := ComponentInsertMutations(rec, map[string]int64{"db": 2}); err != nil || len(muts) != 2 {
		t.Errorf("ComponentInsertMutations() = (%d muts, %v), want (2 muts, nil)", len(muts), err)
	}
	if muts, err := ComponentIntentUpdateMutations(rec, map[string]int64{"db": 2}); err != nil || len(muts) != 3 {
		t.Errorf("ComponentIntentUpdateMutations() = (%d muts, %v), want (3 muts, nil)", len(muts), err)
	}
	if m, err := ComponentStateUpdateMutation(1, componentspb.ComponentState_COMPONENT_STATE_DISABLING, nil); err != nil || m == nil {
		t.Errorf("ComponentStateUpdateMutation() = (%v, %v), want (non-nil, nil)", m, err)
	}
	obsGen := int64(2)
	rec.ObservedGeneration = &obsGen
	if m, err := ComponentObservationUpdateMutation(rec, true); err != nil || m == nil {
		t.Errorf("ComponentObservationUpdateMutation() = (%v, %v), want (non-nil, nil)", m, err)
	}
	if m, err := ComponentSoftDeleteMutation(rec, owner); err != nil || m == nil {
		t.Errorf("ComponentSoftDeleteMutation() = (%v, %v), want (non-nil, nil)", m, err)
	}
}
