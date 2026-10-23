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

// Cross-backend parity tests for google.rpc.ErrorInfo details on Store rejections.
// Production targets: error_info.go, graph_validator.go, deactivation_engine.go, memory_store.go,
// and spanner_store.go.
// Test type: hermetic cross-backend unit/emulator suite (runs every rejection case against both
// MemoryStore and SpannerStore backed by the in-process Spanner emulator). Kept separate from
// error_info_test.go (which unit-tests the pure ErrorInfo constructor helpers in package store) so
// end-to-end Store RPC rejection parity is verified in package store_test.
package spanner_test

import (
	"context"
	"testing"

	"github.com/google/go-cmp/cmp"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/testing/protocmp"

	componentspb "xmanager_cloud/api/protos/components/v1alpha1"
	"xmanager_cloud/pkg/components/store"
)

// TestRejectionErrorInfoParity runs the same rejections against MemoryStore and SpannerStore so
// both return FailedPrecondition with the same google.rpc.ErrorInfo for a dependency cycle and for
// active dependents blocking a deactivation or a non-cascading deletion.
func TestRejectionErrorInfoParity(t *testing.T) {
	const dep, dependent = "errinfo-dep", "errinfo-dependent"
	tests := []struct {
		name string
		call func(context.Context, store.Store) error
		want *errdetails.ErrorInfo
	}{
		{
			name: "dependency cycle",
			call: func(ctx context.Context, s store.Store) error {
				_, err := s.SetComponentIntent(ctx, dep, intentSpec("v1.0.0", dependent), nil, 2)
				return err
			},
			want: rejectionInfo(store.ReasonDependencyCycle, "SetComponentIntent", dep, "cycle", dep+","+dependent),
		},
		{
			name: "active dependents block deactivation",
			call: func(ctx context.Context, s store.Store) error {
				_, err := s.PrepareDeactivation(ctx, dep, false)
				return err
			},
			want: rejectionInfo(store.ReasonActiveDependentsExist, "PrepareDeactivation", dep, "dependents", dependent),
		},
		{
			name: "active dependents block non-cascading deletion",
			call: func(ctx context.Context, s store.Store) error {
				return s.ExecuteDeletion(ctx, dep, componentspb.DeletionPolicy_DELETION_POLICY_ORPHAN, false)
			},
			want: rejectionInfo(store.ReasonActiveDependentsExist, "ExecuteDeletion", dep, "dependents", dependent),
		},
	}
	for _, backend := range intentStoreBackends() {
		t.Run(backend.name, func(t *testing.T) {
			ctx := context.Background()
			s := backend.newStore(t)
			if _, err := s.SetComponentIntent(ctx, dep, intentSpec("v1.0.0"), nil, 1); err != nil {
				t.Fatalf("SetComponentIntent(%s) error = %v", dep, err)
			}
			if _, err := s.SetComponentIntent(ctx, dependent, intentSpec("v1.0.0", dep), nil, 1); err != nil {
				t.Fatalf("SetComponentIntent(%s) error = %v", dependent, err)
			}
			// Subtests are intentionally sequential (no t.Parallel) because they share one store;
			// every rejection leaves it unchanged.
			for _, tt := range tests {
				t.Run(tt.name, func(t *testing.T) {
					st := status.Convert(tt.call(ctx, s))
					if st.Code() != codes.FailedPrecondition {
						t.Fatalf("error code = %v (%q), want %v", st.Code(), st.Message(), codes.FailedPrecondition)
					}
					if diff := cmp.Diff([]any{tt.want}, st.Details(), protocmp.Transform()); diff != "" {
						t.Errorf("details mismatch (-want +got):\n%s", diff)
					}
				})
			}
		})
	}
}

// rejectionInfo returns the ErrorInfo of a rejection with reason of operation on component, whose
// remaining metadata entry is key=value.
func rejectionInfo(reason, operation, component, key, value string) *errdetails.ErrorInfo {
	return &errdetails.ErrorInfo{
		Reason:   reason,
		Domain:   store.ErrorInfoDomain,
		Metadata: map[string]string{"operation": operation, "component": component, key: value},
	}
}
