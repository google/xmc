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
	"time"

	"github.com/google/go-cmp/cmp"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/durationpb"

	componentspb "xmanager_cloud/api/protos/components/v1alpha1"
)

func TestIsDeletionProtected(t *testing.T) {
	tests := []struct {
		name   string
		labels map[string]string
		want   bool
	}{
		{
			name:   "NilRecord",
			labels: nil,
			want:   false,
		},
		{
			name:   "StandardLabel_True",
			labels: map[string]string{"xmc.dev/deletion-protection": "true"},
			want:   true,
		},
		{
			name:   "StandardLabel_One",
			labels: map[string]string{"xmc.dev/deletion-protection": "1"},
			want:   true,
		},
		{
			name:   "StandardLabel_Uppercase",
			labels: map[string]string{"xmc.dev/deletion-protection": " TRUE "},
			want:   true,
		},
		{
			name:   "AlternateLabel_True",
			labels: map[string]string{"deletion_protection": "true"},
			want:   true,
		},
		{
			name:   "AlternateLabel_One",
			labels: map[string]string{"deletion_protection": "1"},
			want:   true,
		},
		{
			name:   "StandardLabel_False",
			labels: map[string]string{"xmc.dev/deletion-protection": "false"},
			want:   false,
		},
		{
			name:   "UnrelatedLabels",
			labels: map[string]string{"env": "prod"},
			want:   false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var rec *ComponentRecord
			if tc.labels != nil || tc.name != "NilRecord" {
				rec = &ComponentRecord{Labels: tc.labels}
			}
			if got := IsDeletionProtected(rec); got != tc.want {
				t.Errorf("IsDeletionProtected() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestCalculateDrainTimeout(t *testing.T) {
	// 1. Explicit request override
	reqTimeout := durationpb.New(90 * time.Second)
	if got := CalculateDrainTimeout(nil, nil, reqTimeout); got != 90*time.Second {
		t.Errorf("CalculateDrainTimeout(override) = %v, want 90s", got)
	}

	// 2. Default minimum 30s when unspecified
	rec := &ComponentRecord{Name: "comp-a"}
	if got := CalculateDrainTimeout(rec, nil, nil); got != 30*time.Second {
		t.Errorf("CalculateDrainTimeout(default) = %v, want 30s", got)
	}

	// 3. Target record lifecycle draining timeout
	recWithTimeout := &ComponentRecord{
		Name: "comp-b",
		Spec: &componentspb.ComponentSpec{
			LifecycleControl: &componentspb.LifecycleControl{
				DrainingTimeout: durationpb.New(45 * time.Second),
			},
		},
	}
	if got := CalculateDrainTimeout(recWithTimeout, nil, nil); got != 45*time.Second {
		t.Errorf("CalculateDrainTimeout(spec draining) = %v, want 45s", got)
	}

	// 4. Cascaded dependent with higher timeout
	cascaded := []*ComponentRecord{
		{
			Name: "comp-c",
			Spec: &componentspb.ComponentSpec{
				LifecycleControl: &componentspb.LifecycleControl{
					GracePeriod: durationpb.New(75 * time.Second),
				},
			},
		},
	}
	if got := CalculateDrainTimeout(recWithTimeout, cascaded, nil); got != 75*time.Second {
		t.Errorf("CalculateDrainTimeout(cascaded max) = %v, want 75s", got)
	}
}

func TestEvaluateDeactivationSafety(t *testing.T) {
	components := map[string]*ComponentRecord{
		"root": {
			Name:  "root",
			State: componentspb.ComponentState_COMPONENT_STATE_ENABLED,
		},
		"dep-1": {
			Name:         "dep-1",
			State:        componentspb.ComponentState_COMPONENT_STATE_ENABLED,
			Dependencies: []string{"root"},
		},
		"dep-disabled": {
			Name:         "dep-disabled",
			State:        componentspb.ComponentState_COMPONENT_STATE_DISABLED,
			Dependencies: []string{"root"},
		},
	}

	// 1. Target with active dependent and requestCascade = false -> ErrHasActiveDependents
	res, err := EvaluateDeactivationSafety(components, "root", false, 30*time.Second)
	if status.Code(err) != codes.FailedPrecondition {
		t.Errorf("EvaluateDeactivationSafety(no cascade) got code %v, want FailedPrecondition", status.Code(err))
	}
	if res.IsSafeToDeactivate {
		t.Error("IsSafeToDeactivate should be false when active dependents block")
	}
	if diff := cmp.Diff([]string{"dep-1"}, res.BlockingDependents); diff != "" {
		t.Errorf("BlockingDependents mismatch (-want +got):\n%s", diff)
	}

	// 2. Target with active dependent and requestCascade = true -> IsSafeToDeactivate = true
	resCascade, err := EvaluateDeactivationSafety(components, "root", true, 30*time.Second)
	if err != nil {
		t.Fatalf("EvaluateDeactivationSafety(cascade) unexpected error: %v", err)
	}
	if !resCascade.IsSafeToDeactivate {
		t.Error("IsSafeToDeactivate should be true when cascading")
	}
	if diff := cmp.Diff([]string{"dep-1"}, resCascade.CascadedDeactivations); diff != "" {
		t.Errorf("CascadedDeactivations mismatch (-want +got):\n%s", diff)
	}

	// 3. Leaf component with no dependents -> safe without cascade
	resLeaf, err := EvaluateDeactivationSafety(components, "dep-1", false, 30*time.Second)
	if err != nil {
		t.Fatalf("EvaluateDeactivationSafety(leaf) unexpected error: %v", err)
	}
	if !resLeaf.IsSafeToDeactivate {
		t.Error("Leaf component should be safe to deactivate")
	}

	// 4. Nonexistent component -> NotFound
	_, err = EvaluateDeactivationSafety(components, "nonexistent", false, 30*time.Second)
	if status.Code(err) != codes.NotFound {
		t.Errorf("EvaluateDeactivationSafety(nonexistent) got code %v, want NotFound", status.Code(err))
	}
}

func TestValidateDeletionSafety(t *testing.T) {
	components := map[string]*ComponentRecord{
		"comp-safe": {
			Name:  "comp-safe",
			State: componentspb.ComponentState_COMPONENT_STATE_ENABLED,
		},
		"comp-protected": {
			Name:   "comp-protected",
			State:  componentspb.ComponentState_COMPONENT_STATE_ENABLED,
			Labels: map[string]string{"xmc.dev/deletion-protection": "true"},
		},
		"comp-with-dep": {
			Name:  "comp-with-dep",
			State: componentspb.ComponentState_COMPONENT_STATE_ENABLED,
		},
		"dep-active": {
			Name:         "dep-active",
			State:        componentspb.ComponentState_COMPONENT_STATE_ENABLED,
			Dependencies: []string{"comp-with-dep"},
		},
		"comp-with-protected-dep": {
			Name:  "comp-with-protected-dep",
			State: componentspb.ComponentState_COMPONENT_STATE_ENABLED,
		},
		"dep-protected-child": {
			Name:         "dep-protected-child",
			State:        componentspb.ComponentState_COMPONENT_STATE_ENABLED,
			Dependencies: []string{"comp-with-protected-dep"},
			Labels:       map[string]string{"xmc.dev/deletion-protection": "true"},
		},
		"nil-entry": nil,
	}

	// 1. Safe deletion
	if err := ValidateDeletionSafety(components, "comp-safe", componentspb.DeletionPolicy_DELETION_POLICY_UNSPECIFIED, false); err != nil {
		t.Errorf("ValidateDeletionSafety(comp-safe) unexpected error: %v", err)
	}

	// 2. Protected component without force -> ErrDeletionProtected
	err := ValidateDeletionSafety(components, "comp-protected", componentspb.DeletionPolicy_DELETION_POLICY_UNSPECIFIED, false)
	if status.Code(err) != codes.FailedPrecondition {
		t.Errorf("ValidateDeletionSafety(protected, force=false) got code %v, want FailedPrecondition", status.Code(err))
	}

	// 3. Protected component with force -> success
	if err := ValidateDeletionSafety(components, "comp-protected", componentspb.DeletionPolicy_DELETION_POLICY_UNSPECIFIED, true); err != nil {
		t.Errorf("ValidateDeletionSafety(protected, force=true) unexpected error: %v", err)
	}

	// 4. Component with active dependents without force and non-cascading policy -> ErrHasActiveDependents
	err = ValidateDeletionSafety(components, "comp-with-dep", componentspb.DeletionPolicy_DELETION_POLICY_ORPHAN, false)
	if status.Code(err) != codes.FailedPrecondition {
		t.Errorf("ValidateDeletionSafety(with-dep, non-cascading, force=false) got code %v, want FailedPrecondition", status.Code(err))
	}

	// 5. Component with active dependents with cascading policy -> success
	if err := ValidateDeletionSafety(components, "comp-with-dep", componentspb.DeletionPolicy_DELETION_POLICY_DELETE, false); err != nil {
		t.Errorf("ValidateDeletionSafety(with-dep, cascading, force=false) unexpected error: %v", err)
	}

	// 6. Cascading deletion when an active dependent is deletion-protected -> ErrDeletionProtected
	err = ValidateDeletionSafety(components, "comp-with-protected-dep", componentspb.DeletionPolicy_DELETION_POLICY_DELETE, false)
	if status.Code(err) != codes.FailedPrecondition {
		t.Errorf("ValidateDeletionSafety(cascading with protected dependent, force=false) got code %v, want FailedPrecondition", status.Code(err))
	}

	// 7. Cascading deletion of protected dependent with force -> success
	if err := ValidateDeletionSafety(components, "comp-with-protected-dep", componentspb.DeletionPolicy_DELETION_POLICY_DELETE, true); err != nil {
		t.Errorf("ValidateDeletionSafety(cascading with protected dependent, force=true) unexpected error: %v", err)
	}

	// 8. Component with active dependents with force -> success
	if err := ValidateDeletionSafety(components, "comp-with-dep", componentspb.DeletionPolicy_DELETION_POLICY_ORPHAN, true); err != nil {
		t.Errorf("ValidateDeletionSafety(with-dep, force=true) unexpected error: %v", err)
	}

	// 9. Nil map entry -> ErrComponentNotFound
	err = ValidateDeletionSafety(components, "nil-entry", componentspb.DeletionPolicy_DELETION_POLICY_UNSPECIFIED, false)
	if status.Code(err) != codes.NotFound {
		t.Errorf("ValidateDeletionSafety(nil-entry) got code %v, want NotFound", status.Code(err))
	}

	// 10. Nonexistent component -> ErrComponentNotFound
	err = ValidateDeletionSafety(components, "nonexistent", componentspb.DeletionPolicy_DELETION_POLICY_UNSPECIFIED, false)
	if status.Code(err) != codes.NotFound {
		t.Errorf("ValidateDeletionSafety(nonexistent) got code %v, want NotFound", status.Code(err))
	}
}
