// Copyright 2025 Google LLC
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
package data

import (
	"golang/protobuf/v2/proto/proto"
	"testing"

	"github.com/google/go-cmp/cmp"
	"google.golang.org/protobuf/testing/protocmp"
	lropb "longrunning/operations_go_proto"
	anypb "protobuf/any_go_proto"
	fmpb "protobuf/field_mask_go_proto"
	statuspb "rpc/status_go_proto"
	pb "xmanager_cloud/experiment_state_server/proto/messages_go_proto"
)

func TestMergeOperations(t *testing.T) {
	metadata1, err := anypb.New(&pb.OperationMetadata{ActionType: pb.OperationMetadata_CREATE})
	if err != nil {
		t.Fatalf("Failed to marshal metadata1: %v", err)
	}
	metadata2, err := anypb.New(&pb.OperationMetadata{ActionType: pb.OperationMetadata_STOP})
	if err != nil {
		t.Fatalf("Failed to marshal metadata2: %v", err)
	}
	response, err := anypb.New(&pb.WorkUnit{Name: "experiments/1/workUnits/1"})
	if err != nil {
		t.Fatalf("Failed to marshal response: %v", err)
	}

	baseOp := &lropb.Operation{
		Name:     "experiments/1/workUnits/1/operations/abc",
		Done:     false,
		Metadata: metadata1,
	}

	tests := []struct {
		name       string
		current    *lropb.Operation
		updated    *lropb.Operation
		updateMask *fmpb.FieldMask
		want       *lropb.Operation
		wantErr    bool
	}{
		{
			name:       "update_done",
			current:    baseOp,
			updated:    &lropb.Operation{Done: true},
			updateMask: &fmpb.FieldMask{Paths: []string{"done"}},
			want:       proto.Modify(baseOp, func(o *lropb.Operation) { o.Done = true }),
		},
		{
			name:       "update_metadata",
			current:    baseOp,
			updated:    &lropb.Operation{Metadata: metadata2},
			updateMask: &fmpb.FieldMask{Paths: []string{"metadata"}},
			want:       proto.Modify(baseOp, func(o *lropb.Operation) { o.Metadata = metadata2 }),
		},
		{
			name:       "update_error",
			current:    baseOp,
			updated:    &lropb.Operation{Result: &lropb.Operation_Error{Error: &statuspb.Status{Code: 1, Message: "error"}}},
			updateMask: &fmpb.FieldMask{Paths: []string{"error"}},
			want: proto.Modify(baseOp, func(o *lropb.Operation) {
				o.Result = &lropb.Operation_Error{Error: &statuspb.Status{Code: 1, Message: "error"}}
			}),
		},
		{
			name:       "update_response",
			current:    baseOp,
			updated:    &lropb.Operation{Result: &lropb.Operation_Response{Response: response}},
			updateMask: &fmpb.FieldMask{Paths: []string{"response"}},
			want:       proto.Modify(baseOp, func(o *lropb.Operation) { o.Result = &lropb.Operation_Response{Response: response} }),
		},
		{
			name: "nil_current",
			updated: &lropb.Operation{
				Done: true,
			},
			updateMask: &fmpb.FieldMask{Paths: []string{"done"}},
			wantErr:    true,
		},
		{
			name: "nil_updated",
			current: &lropb.Operation{
				Name: "experiments/1/workUnits/1/operations/abc",
			},
			updateMask: &fmpb.FieldMask{Paths: []string{"done"}},
			wantErr:    true,
		},
		{
			name: "empty_mask",
			current: &lropb.Operation{
				Name: "experiments/1/workUnits/1/operations/abc",
			},
			updated: &lropb.Operation{
				Done: true,
			},
			updateMask: &fmpb.FieldMask{Paths: []string{}},
			wantErr:    true,
		},
		{
			name: "invalid_field",
			current: &lropb.Operation{
				Name: "experiments/1/workUnits/1/operations/abc",
			},
			updated: &lropb.Operation{
				Done: true,
			},
			updateMask: &fmpb.FieldMask{Paths: []string{"invalid_field"}},
			wantErr:    true,
		},
		{
			name: "nil_metadata",
			current: &lropb.Operation{
				Name: "experiments/1/workUnits/1/operations/abc",
			},
			updated: &lropb.Operation{
				Metadata: nil,
			},
			updateMask: &fmpb.FieldMask{Paths: []string{"metadata"}},
			wantErr:    true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := MergeOperations(tc.current, tc.updated, tc.updateMask)
			if tc.wantErr {
				if err == nil {
					t.Errorf("MergeOperations(%v, %v, %v) succeeded, want error", tc.current, tc.updated, tc.updateMask)
				}
				return
			}
			if err != nil {
				t.Fatalf("MergeOperations(%v, %v, %v) returned an unexpected error: %v", tc.current, tc.updated, tc.updateMask, err)
			}

			if diff := cmp.Diff(tc.want, got, protocmp.Transform()); diff != "" {
				t.Errorf("MergeOperations(%v, %v, %v) returned an unexpected diff (-want +got): %v", tc.current, tc.updated, tc.updateMask, diff)
			}
		})
	}
}
