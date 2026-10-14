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
	"testing"

	"github.com/google/go-cmp/cmp"
	"google.golang.org/protobuf/testing/protocmp"
	fieldmaskpb "google.golang.org/protobuf/types/known/fieldmaskpb"
	pb "xmanager_cloud/api/protos/experiment"
	"xmanager_cloud/internal/prototest"
)

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
