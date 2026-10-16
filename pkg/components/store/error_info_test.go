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
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/testing/protocmp"
)

func TestErrorWithInfo(t *testing.T) {
	err := errorWithInfo(codes.FailedPrecondition, ReasonDependencyNotRegistered, map[string]string{"dependency": "redpanda"}, "dependency missing")

	st := status.Convert(err)
	if st.Code() != codes.FailedPrecondition || st.Message() != "dependency missing" {
		t.Errorf("status = %v %q, want %v %q", st.Code(), st.Message(), codes.FailedPrecondition, "dependency missing")
	}
	want := []any{&errdetails.ErrorInfo{Reason: ReasonDependencyNotRegistered, Domain: "components.xmc.dev", Metadata: map[string]string{"dependency": "redpanda"}}}
	if diff := cmp.Diff(want, st.Details(), protocmp.Transform()); diff != "" {
		t.Errorf("details mismatch (-want +got):\n%s", diff)
	}
}

func TestRejectionErrorInfo(t *testing.T) {
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
			wantInfo: &errdetails.ErrorInfo{Reason: ReasonDependencyCycle, Domain: ErrorInfoDomain, Metadata: map[string]string{"operation": "SetComponentIntent", "component": "a", "cycle": "a"}},
		},
		{
			name:     "transitive dependency cycle",
			err:      dependencyCycleError("b", []string{"a", "b", "c", "a"}),
			wantMsg:  "dependency cycle detected: a -> b -> c -> a",
			wantInfo: &errdetails.ErrorInfo{Reason: ReasonDependencyCycle, Domain: ErrorInfoDomain, Metadata: map[string]string{"operation": "SetComponentIntent", "component": "b", "cycle": "a,b,c"}},
		},
		{
			name:     "active dependents",
			err:      activeDependentsError("ExecuteDeletion", "redpanda", []string{"ingestion", "query"}),
			wantMsg:  "component redpanda has active dependents: [ingestion query]",
			wantInfo: &errdetails.ErrorInfo{Reason: ReasonActiveDependentsExist, Domain: ErrorInfoDomain, Metadata: map[string]string{"operation": "ExecuteDeletion", "component": "redpanda", "dependents": "ingestion,query"}},
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
