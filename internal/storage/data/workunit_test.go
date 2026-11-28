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
	"errors"
	"testing"

	"github.com/google/go-cmp/cmp"
	"google.golang.org/protobuf/testing/protocmp"
	fieldmaskpb "google.golang.org/protobuf/types/known/fieldmaskpb"
	pb "xmanager_cloud/api/protos/experiment"
	"xmanager_cloud/internal/grpcerr"
	"xmanager_cloud/internal/prototest"
)

func TestWorkUnitsExperimentID(t *testing.T) {
	named := func(names ...string) []*pb.WorkUnit {
		wus := make([]*pb.WorkUnit, len(names))
		for i, name := range names {
			// The execution name is ignored: the XID comes from the work unit name.
			wus[i] = &pb.WorkUnit{Name: name, CurrentWorkUnitExecution: &pb.WorkUnitExecution{Name: "experiments/9/workUnits/1/executions/1"}}
		}
		return wus
	}
	tests := []struct {
		name    string
		wus     []*pb.WorkUnit
		wantXID int64
		wantErr bool
	}{
		{name: "one experiment", wus: named("experiments/7/workUnits/1", "experiments/7/workUnits/2"), wantXID: 7},
		{name: "empty", wus: nil, wantXID: 0},
		{name: "mixed experiments", wus: named("experiments/7/workUnits/1", "experiments/8/workUnits/2"), wantErr: true},
		{name: "unparseable name", wus: named("experiments/7/workUnits/1", "invalid"), wantErr: true},
		{name: "nil work unit", wus: []*pb.WorkUnit{nil}, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			xid, err := WorkUnitsExperimentID(tt.wus)
			if tt.wantErr {
				if !errors.Is(err, grpcerr.ErrInvalidArgument) {
					t.Errorf("WorkUnitsExperimentID() = (%d, %v), want InvalidArgument", xid, err)
				}
				return
			}
			if err != nil || xid != tt.wantXID {
				t.Errorf("WorkUnitsExperimentID() = (%d, %v), want (%d, nil)", xid, err, tt.wantXID)
			}
		})
	}
}

func TestMergeWorkUnits(t *testing.T) {
	current := &pb.WorkUnit{
		Priority: 100,
		Urls:     []*pb.Url{},
		Tags:     []string{"current_tag"},
		Notes:    "Current notes",
	}
	updated := &pb.WorkUnit{
		Priority: 200,
		Urls:     []*pb.Url{{Url: "http://updated.com"}},
		Tags:     []string{"updated_tag"},
		Notes:    "Updated notes",
	}

	testCases := []struct {
		name       string
		current    *pb.WorkUnit
		updated    *pb.WorkUnit
		updateMask *fieldmaskpb.FieldMask
		want       *pb.WorkUnit
		wantErr    bool
	}{
		{
			name:       "update priority",
			current:    current,
			updated:    updated,
			updateMask: &fieldmaskpb.FieldMask{Paths: []string{"priority"}},
			want:       prototest.Modify(current, func(w *pb.WorkUnit) { w.Priority = 200 }),
		},
		{
			name:       "update urls",
			current:    current,
			updated:    updated,
			updateMask: &fieldmaskpb.FieldMask{Paths: []string{"urls"}},
			want:       prototest.Modify(current, func(w *pb.WorkUnit) { w.Urls = updated.Urls }),
		},
		{
			name:       "update tags",
			current:    current,
			updated:    updated,
			updateMask: &fieldmaskpb.FieldMask{Paths: []string{"tags"}},
			want:       prototest.Modify(current, func(w *pb.WorkUnit) { w.Tags = updated.Tags }),
		},
		{
			name:       "update notes",
			current:    current,
			updated:    updated,
			updateMask: &fieldmaskpb.FieldMask{Paths: []string{"notes"}},
			want:       prototest.Modify(current, func(w *pb.WorkUnit) { w.Notes = updated.Notes }),
		},
		{
			name:       "update multiple fields",
			current:    current,
			updated:    updated,
			updateMask: &fieldmaskpb.FieldMask{Paths: []string{"priority", "notes"}},
			want:       prototest.Modify(current, func(w *pb.WorkUnit) { w.Priority = 200; w.Notes = updated.Notes }),
		},
		{
			name:       "nil current",
			current:    nil,
			updated:    updated,
			updateMask: &fieldmaskpb.FieldMask{Paths: []string{"notes"}},
			wantErr:    true,
		},
		{
			name:       "nil updated",
			current:    current,
			updated:    nil,
			updateMask: &fieldmaskpb.FieldMask{Paths: []string{"notes"}},
			wantErr:    true,
		},
		{
			name:       "empty mask",
			current:    current,
			updated:    updated,
			updateMask: &fieldmaskpb.FieldMask{Paths: []string{}},
			wantErr:    true,
		},
		{
			name:       "invalid field",
			current:    current,
			updated:    updated,
			updateMask: &fieldmaskpb.FieldMask{Paths: []string{"invalid_field"}},
			wantErr:    true,
		},
		{
			name:    "update urls with nil",
			current: current,
			updated: &pb.WorkUnit{
				Priority: 200,
				Urls:     nil,
				Tags:     []string{"updated_tag"},
				Notes:    "Updated notes",
			},
			updateMask: &fieldmaskpb.FieldMask{Paths: []string{"urls"}},
			wantErr:    true,
		},
		{
			name:    "update tags with nil",
			current: current,
			updated: &pb.WorkUnit{
				Priority: 200,
				Urls:     []*pb.Url{{Url: "http://updated.com"}},
				Tags:     nil,
				Notes:    "Updated notes",
			},
			updateMask: &fieldmaskpb.FieldMask{Paths: []string{"tags"}},
			wantErr:    true,
		},
		{
			name: "preserves MLflow run url on user urls update",
			current: &pb.WorkUnit{
				Urls: []*pb.Url{
					{Url: "http://user.com", DisplayName: "User"},
					{Url: "http://mlflow/#/experiments/1/runs/r1", DisplayName: "MLflow run", Icon: "timeline"},
				},
			},
			updated: &pb.WorkUnit{
				Urls: []*pb.Url{{Url: "http://new-user.com", DisplayName: "New User"}},
			},
			updateMask: &fieldmaskpb.FieldMask{Paths: []string{"urls"}},
			want: &pb.WorkUnit{
				Urls: []*pb.Url{
					{Url: "http://new-user.com", DisplayName: "New User"},
					{Url: "http://mlflow/#/experiments/1/runs/r1", DisplayName: "MLflow run", Icon: "timeline"},
				},
				Tags:     []string{},
				Priority: pb.Priority_LOW,
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := MergeWorkUnits(tc.current, tc.updated, tc.updateMask)
			if tc.wantErr {
				if err == nil {
					t.Errorf("MergeWorkUnits() succeeded, want error")
				}
				return
			}

			if err != nil {
				t.Fatalf("MergeWorkUnits() failed: %v", err)
			}

			if diff := cmp.Diff(tc.want, got, protocmp.Transform()); diff != "" {
				t.Errorf("MergeWorkUnits() returned unexpected diff (-want +got):\n%s", diff)
			}
		})
	}
}
