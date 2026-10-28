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

package postgresdata

import (
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/jackc/pgx/v5/pgtype"
	"google.golang.org/protobuf/testing/protocmp"
	timestamppb "google.golang.org/protobuf/types/known/timestamppb"
	essmsgpb "xmanager_cloud/api/protos/experiment"
)

var (
	testExpTime         = time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC)
	testExpCompleteTime = time.Date(2026, time.January, 1, 1, 0, 0, 0, time.UTC)
)

func TestExperimentProtoAndRowRoundTrip(t *testing.T) {
	fullExp := &essmsgpb.Experiment{
		Name:     "experiments/123",
		Title:    "Test Experiment",
		Creator:  "test-user",
		Priority: 100,
		Urls: []*essmsgpb.Url{
			{Url: "http://test.com", Icon: "test-icon", DisplayName: "test-display-name"},
		},
		Tags:  []string{"tag1", "tag2"},
		Notes: "Test notes",
		Resources: []*essmsgpb.ResourceSet{
			{
				Resources: &essmsgpb.ResourceSet_CpuResources{
					CpuResources: &essmsgpb.CpuResources{
						CpuMillicores: 1000,
						MemoryBytes:   1024,
					},
				},
			},
		},
		Status: &essmsgpb.ExperimentStatus{
			ActiveState: essmsgpb.ExperimentStatus_ACTIVE,
			LaunchState: essmsgpb.ExperimentStatus_LAUNCHING,
			UpdateTime:  timestamppb.New(testExpTime),
		},
		CreateTime:   timestamppb.New(testExpTime),
		UpdateTime:   timestamppb.New(testExpTime),
		CompleteTime: timestamppb.New(testExpCompleteTime),
	}

	minimalExp := &essmsgpb.Experiment{
		Name:       "experiments/456",
		Title:      "Minimal Experiment",
		Creator:    "minimal-user",
		CreateTime: timestamppb.New(testExpTime),
		UpdateTime: timestamppb.New(testExpTime),
	}

	tests := []struct {
		name    string
		exp     *essmsgpb.Experiment
		wantRow ExperimentRow
	}{
		{
			name: "experiment with all fields",
			exp:  fullExp,
			wantRow: ExperimentRow{
				Xid:          123,
				Title:        "Test Experiment",
				Creator:      "test-user",
				Priority:     100,
				Tags:         []string{"tag1", "tag2"},
				Notes:        "Test notes",
				ActiveState:  int64(essmsgpb.ExperimentStatus_ACTIVE),
				LaunchState:  int64(essmsgpb.ExperimentStatus_LAUNCHING),
				CreateTime:   testExpTime,
				UpdateTime:   testExpTime,
				CompleteTime: pgtype.Timestamptz{Time: testExpCompleteTime, Valid: true},
			},
		},
		{
			name: "experiment with optional fields unset",
			exp:  minimalExp,
			wantRow: ExperimentRow{
				Xid:        456,
				Title:      "Minimal Experiment",
				Creator:    "minimal-user",
				Tags:       []string{},
				CreateTime: testExpTime,
				UpdateTime: testExpTime,
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			row, err := ExperimentProtoToRow(tc.exp)
			if err != nil {
				t.Fatalf("ExperimentProtoToRow() failed: %v", err)
			}
			if diff := cmp.Diff(tc.wantRow, row, cmpopts.IgnoreFields(ExperimentRow{}, "Urls", "Resources", "Status")); diff != "" {
				t.Errorf("ExperimentProtoToRow() row diff (-want +got):\n%s", diff)
			}
			if row.Urls == nil || row.Resources == nil || row.Tags == nil {
				t.Errorf("ExperimentProtoToRow() produced nil slice for NOT NULL array column: Urls=%v Resources=%v Tags=%v", row.Urls != nil, row.Resources != nil, row.Tags != nil)
			}
			got, err := ExperimentRowToProto(row)
			if err != nil {
				t.Fatalf("ExperimentRowToProto() failed: %v", err)
			}
			if diff := cmp.Diff(tc.exp, got, protocmp.Transform()); diff != "" {
				t.Errorf("ExperimentRowToProto() diff (-want +got):\n%s", diff)
			}
		})
	}
}

func TestExperimentRowNamedArgs(t *testing.T) {
	exp := &essmsgpb.Experiment{
		Name:   "experiments/123",
		Title:  "Title",
		Status: &essmsgpb.ExperimentStatus{UpdateTime: timestamppb.New(testExpTime)},
	}
	row, err := ExperimentProtoToRow(exp)
	if err != nil {
		t.Fatalf("ExperimentProtoToRow() failed: %v", err)
	}
	if !row.UpdateTime.Equal(testExpTime) {
		t.Errorf("row.UpdateTime = %v, want %v from Status.UpdateTime", row.UpdateTime, testExpTime)
	}
	if args := row.GetInsertArgs(); len(args) != 14 || args["xid"] != int64(123) {
		t.Errorf("GetInsertArgs() = %v, want 14 entries with xid=123", args)
	}
	if args := row.GetUpdateArgs(); len(args) != 7 || args["title"] != "Title" {
		t.Errorf("GetUpdateArgs() = %v, want 7 entries with title=Title", args)
	}
	if args := row.GetStatusUpdateArgs(); len(args) != 6 || args["xid"] != int64(123) {
		t.Errorf("GetStatusUpdateArgs() = %v, want 6 entries with xid=123", args)
	}
	if args := row.GetResourcesUpdateArgs(); len(args) != 3 || args["xid"] != int64(123) {
		t.Errorf("GetResourcesUpdateArgs() = %v, want 3 entries with xid=123", args)
	}
}

func TestExperimentConversionErrors(t *testing.T) {
	corrupt := []byte{0xff, 0xff, 0xff}

	if _, err := ExperimentProtoToRow(&essmsgpb.Experiment{Name: "invalid"}); err == nil {
		t.Error("ExperimentProtoToRow(invalid name) = nil, want error")
	}

	badRows := []ExperimentRow{
		{Xid: 1, Urls: [][]byte{corrupt}, CreateTime: testExpTime, UpdateTime: testExpTime},
		{Xid: 1, Resources: [][]byte{corrupt}, CreateTime: testExpTime, UpdateTime: testExpTime},
		{Xid: 1, Status: corrupt, CreateTime: testExpTime, UpdateTime: testExpTime},
	}
	for i, r := range badRows {
		if _, err := ExperimentRowToProto(r); err == nil {
			t.Errorf("ExperimentRowToProto(badRows[%d]) = nil, want error", i)
		}
	}
}
