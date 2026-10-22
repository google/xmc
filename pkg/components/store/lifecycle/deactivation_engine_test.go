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

package lifecycle

import (
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/testing/protocmp"
	"google.golang.org/protobuf/types/known/durationpb"

	componentspb "xmanager_cloud/api/protos/components/v1alpha1"
	"xmanager_cloud/pkg/components/store"
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
			want:   false,
		},
		{
			name:   "StandardLabel_Uppercase",
			labels: map[string]string{"xmc.dev/deletion-protection": " TRUE "},
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
			var rec *store.ComponentRecord
			if tc.labels != nil || tc.name != "NilRecord" {
				rec = &store.ComponentRecord{Labels: tc.labels}
			}
			if got := IsDeletionProtected(rec); got != tc.want {
				t.Errorf("IsDeletionProtected() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestCalculateDrainTimeout(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		rec        *store.ComponentRecord
		cascaded   []*store.ComponentRecord
		reqTimeout *durationpb.Duration
		want       time.Duration
	}{
		{
			name:       "ExplicitRequestOverride",
			reqTimeout: durationpb.New(90 * time.Second),
			want:       90 * time.Second,
		},
		{
			name: "DefaultMinimum30sWhenUnspecified",
			rec:  &store.ComponentRecord{Name: "comp-a"},
			want: 30 * time.Second,
		},
		{
			name: "TargetRecordDrainingTimeout",
			rec: &store.ComponentRecord{
				Name: "comp-b",
				Spec: &componentspb.ComponentSpec{
					LifecycleControl: &componentspb.LifecycleControl{
						DrainingTimeout: durationpb.New(45 * time.Second),
					},
				},
			},
			want: 45 * time.Second,
		},
		{
			name: "CascadedDependentWithHigherGracePeriod",
			rec: &store.ComponentRecord{
				Name: "comp-b",
				Spec: &componentspb.ComponentSpec{
					LifecycleControl: &componentspb.LifecycleControl{
						DrainingTimeout: durationpb.New(45 * time.Second),
					},
				},
			},
			cascaded: []*store.ComponentRecord{
				{
					Name: "comp-c",
					Spec: &componentspb.ComponentSpec{
						LifecycleControl: &componentspb.LifecycleControl{
							GracePeriod: durationpb.New(75 * time.Second),
						},
					},
				},
			},
			want: 75 * time.Second,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := CalculateDrainTimeout(tc.rec, tc.cascaded, tc.reqTimeout)
			if got != tc.want {
				t.Errorf("CalculateDrainTimeout() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestEvaluateDeactivationSafety(t *testing.T) {
	t.Parallel()

	components := map[string]*store.ComponentRecord{
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

	tests := []struct {
		name                   string
		target                 string
		requestCascade         bool
		minDrainFloor          time.Duration
		wantErrCode            codes.Code
		wantSafe               bool
		wantBlockingDependents []string
		wantCascaded           []string
	}{
		{
			name:                   "TargetWithActiveDependent_NoCascade",
			target:                 "root",
			requestCascade:         false,
			minDrainFloor:          30 * time.Second,
			wantErrCode:            codes.FailedPrecondition,
			wantSafe:               false,
			wantBlockingDependents: []string{"dep-1"},
		},
		{
			name:           "TargetWithActiveDependent_WithCascade",
			target:         "root",
			requestCascade: true,
			minDrainFloor:  30 * time.Second,
			wantErrCode:    codes.OK,
			wantSafe:       true,
			wantCascaded:   []string{"dep-1"},
		},
		{
			name:           "LeafComponent_SafeWithoutCascade",
			target:         "dep-1",
			requestCascade: false,
			minDrainFloor:  30 * time.Second,
			wantErrCode:    codes.OK,
			wantSafe:       true,
		},
		{
			name:           "NonexistentComponent",
			target:         "nonexistent",
			requestCascade: false,
			minDrainFloor:  30 * time.Second,
			wantErrCode:    codes.NotFound,
			wantSafe:       false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			res, err := EvaluateDeactivationSafety(components, tc.target, tc.requestCascade, tc.minDrainFloor)
			if status.Code(err) != tc.wantErrCode {
				t.Fatalf("EvaluateDeactivationSafety() error code = %v, want %v (err: %v)", status.Code(err), tc.wantErrCode, err)
			}
			if res == nil {
				return
			}
			if res.IsSafeToDeactivate != tc.wantSafe {
				t.Errorf("res.IsSafeToDeactivate = %v, want %v", res.IsSafeToDeactivate, tc.wantSafe)
			}
			if diff := cmp.Diff(tc.wantBlockingDependents, res.BlockingDependents); tc.wantBlockingDependents != nil && diff != "" {
				t.Errorf("BlockingDependents mismatch (-want +got):\n%s", diff)
			}
			if diff := cmp.Diff(tc.wantCascaded, res.CascadedDeactivations); tc.wantCascaded != nil && diff != "" {
				t.Errorf("CascadedDeactivations mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestValidateDeletionSafety(t *testing.T) {
	components := map[string]*store.ComponentRecord{
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
		"comp-with-disabled-protected-dep": {
			Name:  "comp-with-disabled-protected-dep",
			State: componentspb.ComponentState_COMPONENT_STATE_ENABLED,
		},
		"dep-disabled-protected-child": {
			Name:         "dep-disabled-protected-child",
			State:        componentspb.ComponentState_COMPONENT_STATE_DISABLED,
			Dependencies: []string{"comp-with-disabled-protected-dep"},
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

	// 4. Component with active dependents without force and non-cascading policy -> FailedPrecondition with ErrorInfo reason ACTIVE_DEPENDENTS_EXIST
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

	// 7. Cascading deletion when a DISABLED dependent is deletion-protected -> ErrDeletionProtected
	err = ValidateDeletionSafety(components, "comp-with-disabled-protected-dep", componentspb.DeletionPolicy_DELETION_POLICY_DELETE, false)
	if status.Code(err) != codes.FailedPrecondition {
		t.Errorf("ValidateDeletionSafety(cascading with disabled protected dependent, force=false) got code %v, want FailedPrecondition", status.Code(err))
	}

	// 8. Cascading deletion of protected dependent with force -> success
	if err := ValidateDeletionSafety(components, "comp-with-protected-dep", componentspb.DeletionPolicy_DELETION_POLICY_DELETE, true); err != nil {
		t.Errorf("ValidateDeletionSafety(cascading with protected dependent, force=true) unexpected error: %v", err)
	}

	// 9. Component with active dependents with force -> success
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

func TestActiveDependentsErrorInfo(t *testing.T) {
	err := activeDependentsError("ExecuteDeletion", "redpanda", []string{"ingestion", "query"})
	wantMsg := "component redpanda has active dependents: [ingestion query]"
	wantInfo := &errdetails.ErrorInfo{Reason: store.ReasonActiveDependentsExist, Domain: store.ErrorInfoDomain, Metadata: map[string]string{"operation": "ExecuteDeletion", "component": "redpanda", "dependents": "ingestion,query"}}

	st := status.Convert(err)
	if st.Code() != codes.FailedPrecondition || st.Message() != wantMsg {
		t.Errorf("status = %v %q, want %v %q", st.Code(), st.Message(), codes.FailedPrecondition, wantMsg)
	}
	if diff := cmp.Diff([]any{wantInfo}, st.Details(), protocmp.Transform()); diff != "" {
		t.Errorf("details mismatch (-want +got):\n%s", diff)
	}
}
