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

package dependency

import (
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/testing/protocmp"

	componentspb "xmanager_cloud/api/protos/components/v1alpha1"
	"xmanager_cloud/pkg/components/store"
)

func TestBuildDependencyGraph(t *testing.T) {
	now := time.Now().UTC()
	records := []*store.ComponentRecord{
		{
			Name:         "components/comp-a",
			Dependencies: []string{"comp-b", "comp-c"},
		},
		{
			Name:         "comp-b",
			Dependencies: []string{"comp-c"},
		},
		{
			Name:         "comp-c",
			Dependencies: nil,
		},
		{
			Name:         "comp-deleted",
			Dependencies: []string{"comp-a"},
			DeleteTime:   &now,
		},
		{
			Name:         "comp-state-deleted",
			Dependencies: []string{"comp-a"},
			State:        componentspb.ComponentState_COMPONENT_STATE_DELETED,
		},
	}

	got := BuildDependencyGraph(records)
	want := DependencyGraph{
		"comp-a": {"comp-b", "comp-c"},
		"comp-b": {"comp-c"},
		"comp-c": {},
	}

	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("BuildDependencyGraph mismatch (-want +got):\n%s", diff)
	}
}

func TestBuildDependencyGraphFromMap(t *testing.T) {
	now := time.Now().UTC()
	recordMap := map[string]*store.ComponentRecord{
		"comp-a": {
			Name:         "components/comp-a",
			Dependencies: []string{"comp-b", "comp-c"},
		},
		"comp-b": {
			Name:         "comp-b",
			Dependencies: []string{"comp-c"},
		},
		"comp-c": {
			Name:         "comp-c",
			Dependencies: nil,
		},
		"comp-deleted": {
			Name:         "comp-deleted",
			Dependencies: []string{"comp-a"},
			DeleteTime:   &now,
		},
		"comp-state-deleted": {
			Name:         "comp-state-deleted",
			Dependencies: []string{"comp-a"},
			State:        componentspb.ComponentState_COMPONENT_STATE_DELETED,
		},
		"nil-rec": nil,
	}

	got := BuildDependencyGraphFromMap(recordMap)
	want := DependencyGraph{
		"comp-a": {"comp-b", "comp-c"},
		"comp-b": {"comp-c"},
		"comp-c": {},
	}

	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("BuildDependencyGraphFromMap mismatch (-want +got):\n%s", diff)
	}
}

func TestBuildDependentsGraph(t *testing.T) {
	records := []*store.ComponentRecord{
		{
			Name:         "comp-a",
			Dependencies: []string{"comp-b", "comp-c"},
		},
		{
			Name:         "comp-b",
			Dependencies: []string{"comp-c"},
		},
		{
			Name:         "comp-c",
			Dependencies: nil,
		},
	}

	got := BuildDependentsGraph(records)
	want := DependencyGraph{
		"comp-a": nil,
		"comp-b": {"comp-a"},
		"comp-c": {"comp-a", "comp-b"},
	}

	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("BuildDependentsGraph mismatch (-want +got):\n%s", diff)
	}
}

func TestDetectCycles_DelegatesToGraphPackage(t *testing.T) {
	gotPath, gotCycle := DetectCycles(DependencyGraph{
		"a": {"b"},
		"b": {"a"},
	})
	if !gotCycle {
		t.Fatal("DetectCycles() = false, want true")
	}
	if diff := cmp.Diff([]string{"a", "b", "a"}, gotPath); diff != "" {
		t.Errorf("DetectCycles() path mismatch (-want +got):\n%s", diff)
	}
}

func TestValidateProspectiveDependencies(t *testing.T) {
	existing := []*store.ComponentRecord{
		{
			Name:         "comp-a",
			Dependencies: []string{"comp-b"},
		},
		{
			Name:         "comp-b",
			Dependencies: []string{"comp-c"},
		},
		{
			Name:         "comp-c",
			Dependencies: nil,
		},
	}

	// 1. Direct self-cycle (normalized and raw)
	err := ValidateProspectiveDependencies(existing, "comp-d", []string{"comp-d"})
	if status.Code(err) != codes.FailedPrecondition {
		t.Errorf("Direct self cycle got code %v, want FailedPrecondition", status.Code(err))
	}
	err = ValidateProspectiveDependencies(existing, "comp-d", []string{"comp-e,comp-d"})
	if status.Code(err) != codes.FailedPrecondition {
		t.Errorf("Raw self cycle got code %v, want FailedPrecondition", status.Code(err))
	}

	// 2. Transitive cycle (comp-c depending on comp-a)
	err = ValidateProspectiveDependencies(existing, "comp-c", []string{"comp-a"})
	if status.Code(err) != codes.FailedPrecondition {
		t.Errorf("Transitive cycle got code %v, want FailedPrecondition", status.Code(err))
	}

	// 3. Valid new dependency (comp-d depending on comp-a)
	err = ValidateProspectiveDependencies(existing, "comp-d", []string{"comp-a"})
	if err != nil {
		t.Errorf("Valid prospective dependencies returned unexpected error: %v", err)
	}

	// 4. Invalid target name
	err = ValidateProspectiveDependencies(existing, "", []string{"comp-a"})
	if status.Code(err) != codes.InvalidArgument {
		t.Errorf("Empty target name got code %v, want InvalidArgument", status.Code(err))
	}
}

func TestValidateProspectiveDependenciesFromMap(t *testing.T) {
	existingMap := map[string]*store.ComponentRecord{
		"comp-a": {
			Name:         "comp-a",
			Dependencies: []string{"comp-b"},
		},
		"comp-b": {
			Name:         "comp-b",
			Dependencies: []string{"comp-c"},
		},
		"comp-c": {
			Name:         "comp-c",
			Dependencies: nil,
		},
	}

	// 1. Direct self-cycle (normalized and raw)
	err := ValidateProspectiveDependenciesFromMap(existingMap, "comp-d", []string{"comp-d"})
	if status.Code(err) != codes.FailedPrecondition {
		t.Errorf("Direct self cycle got code %v, want FailedPrecondition", status.Code(err))
	}
	err = ValidateProspectiveDependenciesFromMap(existingMap, "comp-d", []string{"comp-e,comp-d"})
	if status.Code(err) != codes.FailedPrecondition {
		t.Errorf("Raw self cycle got code %v, want FailedPrecondition", status.Code(err))
	}

	// 2. Transitive cycle (comp-c depending on comp-a)
	err = ValidateProspectiveDependenciesFromMap(existingMap, "comp-c", []string{"comp-a"})
	if status.Code(err) != codes.FailedPrecondition {
		t.Errorf("Transitive cycle got code %v, want FailedPrecondition", status.Code(err))
	}

	// 3. Valid new dependency (comp-d depending on comp-a)
	err = ValidateProspectiveDependenciesFromMap(existingMap, "comp-d", []string{"comp-a"})
	if err != nil {
		t.Errorf("Valid prospective dependencies returned unexpected error: %v", err)
	}

	// 4. Invalid target name
	err = ValidateProspectiveDependenciesFromMap(existingMap, "", []string{"comp-a"})
	if status.Code(err) != codes.InvalidArgument {
		t.Errorf("Empty target name got code %v, want InvalidArgument", status.Code(err))
	}
}

func TestFindTransitiveDependents_CanonicalizesRoot(t *testing.T) {
	records := []*store.ComponentRecord{
		{
			Name:         "components/comp-a",
			Dependencies: []string{"comp-b"},
		},
		{
			Name:         "comp-b",
			Dependencies: []string{"comp-c"},
		},
		{
			Name:         "comp-c",
			Dependencies: nil,
		},
	}

	dependentsGraph := BuildDependentsGraph(records)

	got := FindTransitiveDependents(dependentsGraph, "components/comp-c")
	want := []string{"comp-a", "comp-b"}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("FindTransitiveDependents(components/comp-c) mismatch (-want +got):\n%s", diff)
	}
}

func TestDependencyCycleErrorInfo(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		wantMsg  string
		wantInfo *errdetails.ErrorInfo
	}{
		{
			name:     "self dependency cycle",
			err:      dependencyCycleError("a", []string{"a", "a"}),
			wantMsg:  "dependency cycle detected: a -> a",
			wantInfo: &errdetails.ErrorInfo{Reason: store.ReasonDependencyCycle, Domain: store.ErrorInfoDomain, Metadata: map[string]string{"operation": "SetComponentIntent", "component": "a", "cycle": "a"}},
		},
		{
			name:     "transitive dependency cycle",
			err:      dependencyCycleError("b", []string{"a", "b", "c", "a"}),
			wantMsg:  "dependency cycle detected: a -> b -> c -> a",
			wantInfo: &errdetails.ErrorInfo{Reason: store.ReasonDependencyCycle, Domain: store.ErrorInfoDomain, Metadata: map[string]string{"operation": "SetComponentIntent", "component": "b", "cycle": "a,b,c"}},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			st := status.Convert(tt.err)
			if st.Code() != codes.FailedPrecondition || st.Message() != tt.wantMsg {
				t.Errorf("status = %v %q, want %v %q", st.Code(), st.Message(), codes.FailedPrecondition, tt.wantMsg)
			}
			if diff := cmp.Diff([]any{tt.wantInfo}, st.Details(), protocmp.Transform()); diff != "" {
				t.Errorf("details mismatch (-want +got):\n%s", diff)
			}
		})
	}
}
