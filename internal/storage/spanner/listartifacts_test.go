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
package spanner

import (
	"context"
	"fmt"
	"sort"
	"testing"
	"time"

	"golang/cmp/cmp"
	"golang/protobuf/v2/testing/protocmp/protocmp"
	timestamppb "protobuf/timestamp_go_proto"
	essmsgpb "xmanager_cloud/experiment_state_server/proto/messages_go_proto"
	"xmanager_cloud/storage/spanner/emulator/emulator"
	"xmanager_cloud/validator/validator"
)

func TestListArtifacts(t *testing.T) {
	ctx := context.Background()
	_, essClient := emulator.Setup(ctx, t)
	storage, err := NewStorage(essClient)
	if err != nil {
		t.Fatalf("NewStorage failed: %v", err)
	}

	// Create parent experiment
	createAndStoreExperiment(ctx, t, storage, blankExperimentProto(1))

	// Create some artifacts
	var artifacts []*essmsgpb.Artifact
	for i := 1; i < 6; i++ {
		art := blankArtifactProto(1, int64(i))
		art.Url.DisplayName = fmt.Sprintf("display-%d", i)
		art.Notes = "test-notes"
		art.Tags = []string{"tag1", "tag2"}
		createdArt := createAndStoreArtifact(ctx, t, storage, art)
		artifacts = append(artifacts, createdArt)
	}

	// List all artifacts
	gotArtifacts, _, err := storage.ListArtifacts(ctx, "", 10, "", "", "", 0, false)
	if err != nil {
		t.Fatalf("ListArtifacts failed: %v", err)
	}
	if len(gotArtifacts) != 5 {
		t.Errorf("ListArtifacts returned %d artifacts, want 5", len(gotArtifacts))
	}

	// Sort artifacts by AID for comparison
	sort.Slice(artifacts, func(i, j int) bool {
		iAID, _ := validator.ValidateArtifactName(artifacts[i].GetName())
		jAID, _ := validator.ValidateArtifactName(artifacts[j].GetName())
		return iAID < jAID
	})
	sort.Slice(gotArtifacts, func(i, j int) bool {
		iAID, _ := validator.ValidateArtifactName(gotArtifacts[i].GetName())
		jAID, _ := validator.ValidateArtifactName(gotArtifacts[j].GetName())
		return iAID < jAID
	})
	if diff := cmp.Diff(artifacts, gotArtifacts, protocmp.Transform(),
		protocmp.IgnoreFields(&essmsgpb.Artifact{}, "acls"),
	); diff != "" {
		t.Errorf("ListArtifacts() returned diff (-want +got):\n%s", diff)
	}
}

func TestListArtifacts_OffsetPagination(t *testing.T) {
	ctx := context.Background()
	_, essClient := emulator.Setup(ctx, t)
	storage, err := NewStorage(essClient)
	if err != nil {
		t.Fatalf("NewStorage failed: %v", err)
	}

	// Create parent experiment
	createAndStoreExperiment(ctx, t, storage, blankExperimentProto(1))

	// Create some artifacts
	for i := 1; i < 6; i++ {
		createAndStoreArtifact(ctx, t, storage, blankArtifactProto(1, int64(i)))
	}

	// List first page
	gotArtifacts1, nextPageToken, err := storage.ListArtifacts(ctx, "", 3, "", "", "artifact_id", 0, false)
	if err != nil {
		t.Fatalf("ListArtifacts failed: %v", err)
	}
	if len(gotArtifacts1) != 3 {
		t.Errorf("ListArtifacts page 1 returned %d artifacts, want 3", len(gotArtifacts1))
	}
	if nextPageToken == "" {
		t.Errorf("ListArtifacts page 1 returned empty nextPageToken, want non-empty")
	}

	// List second page
	gotArtifacts2, _, err := storage.ListArtifacts(ctx, "", 3, nextPageToken, "", "artifact_id", 0, false)
	if err != nil {
		t.Fatalf("ListArtifacts failed: %v", err)
	}
	if len(gotArtifacts2) != 2 {
		t.Errorf("ListArtifacts page 2 returned %d artifacts, want 2", len(gotArtifacts2))
	}
}

func TestListArtifacts_CursorPagination(t *testing.T) {
	ctx := context.Background()
	_, essClient := emulator.Setup(ctx, t)
	storage, err := NewStorage(essClient)
	if err != nil {
		t.Fatalf("NewStorage failed: %v", err)
	}

	// Create parent experiment
	createAndStoreExperiment(ctx, t, storage, blankExperimentProto(1))

	// Create some artifacts
	for i := 1; i < 6; i++ {
		createAndStoreArtifact(ctx, t, storage, blankArtifactProto(1, int64(i)))
	}

	tests := []struct {
		pageSize   int
		wantCounts []int
	}{
		{3, []int{3, 2}},
		{5, []int{5}},
		{1, []int{1, 1, 1, 1, 1}},
	}

	for _, tc := range tests {
		t.Run(fmt.Sprintf("pageSize=%d", tc.pageSize), func(t *testing.T) {
			var gotCounts []int
			var pageToken string
			for i := 0; i < len(tc.wantCounts); i++ {
				artifacts, nextPageToken, err := storage.ListArtifacts(ctx, "", int32(tc.pageSize), pageToken, "", "artifact_id", 0, true)
				if err != nil {
					t.Fatalf("ListArtifacts failed: %v", err)
				}
				gotCounts = append(gotCounts, len(artifacts))
				pageToken = nextPageToken
				if diff := cmp.Diff(tc.wantCounts[i], len(artifacts)); diff != "" {
					t.Errorf("ListArtifacts returned unexpected page count (-want +got):\n%s", diff)
				}
				if i < len(tc.wantCounts)-1 && nextPageToken == "" {
					t.Errorf("ListArtifacts returned empty nextPageToken on page %d, want non-empty", i+1)
				}
			}
			if pageToken != "" {
				t.Errorf("ListArtifacts returned non-empty nextPageToken on last page, want empty")
			}
			if diff := cmp.Diff(tc.wantCounts, gotCounts); diff != "" {
				t.Errorf("ListArtifacts returned unexpected page counts (-want +got):\n%s", diff)
			}
		})
	}
}

func TestListArtifacts_Filter(t *testing.T) {
	ctx := context.Background()
	_, essClient := emulator.Setup(ctx, t)
	storage, err := NewStorage(essClient)
	if err != nil {
		t.Fatalf("NewStorage failed: %v", err)
	}

	// Create parent experiment
	createAndStoreExperiment(ctx, t, storage, blankExperimentProto(1))

	// Create some artifacts
	art1 := blankArtifactProto(1, 1)
	art1.Url.DisplayName = "display-1"
	createAndStoreArtifact(ctx, t, storage, art1)
	art2 := blankArtifactProto(1, 2)
	art2.Url.DisplayName = "display-2"
	createAndStoreArtifact(ctx, t, storage, art2)

	// List with filter
	gotArtifacts, _, err := storage.ListArtifacts(ctx, "", 10, "", `display_name="display-1"`, "", 0, false)
	if err != nil {
		t.Fatalf("ListArtifacts failed: %v", err)
	}
	if len(gotArtifacts) != 1 {
		t.Errorf("ListArtifacts with filter returned %d entries, want 1", len(gotArtifacts))
	}
	if gotArtifacts[0].GetUrl().GetDisplayName() != "display-1" {
		t.Errorf("ListArtifacts with filter returned entry with display name %q, want display-1", gotArtifacts[0].GetUrl().GetDisplayName())
	}
}

func TestListArtifacts_OrderBy(t *testing.T) {
	ctx := context.Background()
	_, essClient := emulator.Setup(ctx, t)
	storage, err := NewStorage(essClient)
	if err != nil {
		t.Fatalf("NewStorage failed: %v", err)
	}

	// Create parent experiment
	createAndStoreExperiment(ctx, t, storage, blankExperimentProto(1))

	// Create some artifacts
	var aids []int64
	for i := 1; i < 4; i++ {
		art := blankArtifactProto(1, int64(i))
		art.Url.Priority = int32(10 - i) // 9,8,7
		created := createAndStoreArtifact(ctx, t, storage, art)
		aid, _ := validator.ValidateArtifactName(created.GetName())
		aids = append(aids, aid)
	}

	// List with orderBy priority desc
	gotArtifacts, _, err := storage.ListArtifacts(ctx, "", 10, "", "", "priority desc", 0, false)
	if err != nil {
		t.Fatalf("ListArtifacts failed: %v", err)
	}
	if len(gotArtifacts) != 3 {
		t.Fatalf("ListArtifacts with orderby returned %d artifacts, want 3", len(gotArtifacts))
	}

	gotAID0, _ := validator.ValidateArtifactName(gotArtifacts[0].GetName())
	gotAID1, _ := validator.ValidateArtifactName(gotArtifacts[1].GetName())
	gotAID2, _ := validator.ValidateArtifactName(gotArtifacts[2].GetName())

	if gotAID0 != aids[0] || gotAID1 != aids[1] || gotAID2 != aids[2] {
		t.Errorf("ListArtifacts with orderBy='priority desc' returned artifacts in wrong order: got %d, %d, %d, want %d, %d, %d", gotAID0, gotAID1, gotAID2, aids[0], aids[1], aids[2])
	}
}

func TestListArtifacts_Complex(t *testing.T) {
	ctx := context.Background()
	_, essClient := emulator.Setup(ctx, t)
	storage, err := NewStorage(essClient)
	if err != nil {
		t.Fatalf("NewStorage failed: %v", err)
	}

	// Create parent experiment
	createAndStoreExperiment(ctx, t, storage, blankExperimentProto(1))

	testData := []struct {
		displayName string
		mimeType    string
	}{
		{"D", "M1"},
		{"C", "M2"},
		{"B", "M1"},
		{"A", "M2"},
		{"E", "M1"},
	}
	artifacts := make([]*essmsgpb.Artifact, len(testData))
	for i, d := range testData {
		art := blankArtifactProto(1, int64(i+1))
		art.Url.DisplayName = d.displayName
		art.MimeType = d.mimeType
		createdArt := createAndStoreArtifact(ctx, t, storage, art)
		artifacts[i] = createdArt
	}

	// List page 1: filter by mime_type=M1, order by display_name desc, page size 2
	filter := `mime_type="M1"`
	orderBy := "display_name desc"
	pageSize := int32(2)

	gotPage1, page1Token, err := storage.ListArtifacts(ctx, "", pageSize, "", filter, orderBy, 0, false)
	if err != nil {
		t.Fatalf("ListArtifacts page 1 failed: %v", err)
	}
	if page1Token == "" {
		t.Errorf("ListArtifacts page 1 got empty next page token, want non-empty")
	}

	wantPage1 := []*essmsgpb.Artifact{artifacts[4], artifacts[0]} // E, D
	cmpOpts := []cmp.Option{
		protocmp.Transform(),
		protocmp.IgnoreFields(&essmsgpb.Artifact{}, "acls"),
	}
	if diff := cmp.Diff(wantPage1, gotPage1, cmpOpts...); diff != "" {
		t.Errorf("ListArtifacts page 1 returned diff (-want +got):\n%s", diff)
	}

	// List page 2
	gotPage2, page2Token, err := storage.ListArtifacts(ctx, "", pageSize, page1Token, filter, orderBy, 0, false)
	if err != nil {
		t.Fatalf("ListArtifacts page 2 failed: %v", err)
	}
	if page2Token != "" {
		t.Errorf("ListArtifacts page 2 got next page token %q, want empty", page2Token)
	}
	wantPage2 := []*essmsgpb.Artifact{artifacts[2]} // B
	if diff := cmp.Diff(wantPage2, gotPage2, cmpOpts...); diff != "" {
		t.Errorf("ListArtifacts page 2 returned diff (-want +got):\n%s", diff)
	}
}

func TestListArtifacts_FilterFields(t *testing.T) {
	ctx := context.Background()
	_, essClient := emulator.Setup(ctx, t)
	storage, err := NewStorage(essClient)
	if err != nil {
		t.Fatalf("NewStorage failed: %v", err)
	}

	// Create parent experiment
	createAndStoreExperiment(ctx, t, storage, blankExperimentProto(1))

	// Create some artifacts
	arts := []*essmsgpb.Artifact{
		blankArtifactProto(1, 1),
		blankArtifactProto(1, 2),
		blankArtifactProto(1, 3),
	}
	arts[0].Url.DisplayName = "name-1"
	arts[0].Tags = []string{"tag1"}
	arts[0].Notes = "notes-1"
	arts[0].MimeType = "mime-1"
	arts[1].Url.DisplayName = "name-2"
	arts[1].Tags = []string{"tag2"}
	arts[1].Notes = "notes-2"
	arts[1].MimeType = "mime-2"
	arts[2].Url.DisplayName = "name-1"
	arts[2].Tags = []string{"tag1", "tag3"}
	arts[2].Notes = "notes-3"
	arts[2].MimeType = "mime-3"

	for _, art := range arts {
		createAndStoreArtifact(ctx, t, storage, art)
	}

	testCases := []struct {
		name   string
		filter string
		want   []int64 // aids of expected artifacts
	}{
		{"artifact_id", "artifact_id=1", []int64{1}},
		{"display_name", `display_name="name-1"`, []int64{1, 3}},
		{"tags", `tags:"tag1"`, []int64{1, 3}},
		{"notes", `notes="notes-3"`, []int64{3}},
		{"mime_type", `mime_type="mime-2"`, []int64{2}},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			gotArtifacts, _, err := storage.ListArtifacts(ctx, "", 10, "", tc.filter, "artifact_id", 0, false)
			if err != nil {
				t.Fatalf("ListArtifacts failed: %v", err)
			}
			var gotAIDs []int64
			for _, art := range gotArtifacts {
				aid, _ := validator.ValidateArtifactName(art.GetName())
				gotAIDs = append(gotAIDs, aid)
			}
			if diff := cmp.Diff(tc.want, gotAIDs); diff != "" {
				t.Errorf("ListArtifacts with filter %q returned diff (-want +got):\n%s", tc.filter, diff)
			}
		})
	}
}

func TestListArtifacts_OrderByFields(t *testing.T) {
	ctx := context.Background()
	_, essClient := emulator.Setup(ctx, t)
	storage, err := NewStorage(essClient)
	if err != nil {
		t.Fatalf("NewStorage failed: %v", err)
	}

	// Create parent experiment
	createAndStoreExperiment(ctx, t, storage, blankExperimentProto(1))

	// Create some artifacts
	aids := []int64{3, 1, 2}
	for _, aid := range aids {
		art := blankArtifactProto(1, aid)
		createAndStoreArtifact(ctx, t, storage, art)
	}

	testCases := []struct {
		name    string
		orderBy string
		want    []int64 // aids of expected artifacts in order
	}{
		{"artifact_id asc", "artifact_id", []int64{1, 2, 3}},
		{"artifact_id desc", "artifact_id desc", []int64{3, 2, 1}},
		{"created asc", "created", []int64{3, 1, 2}},
		{"created desc", "created desc", []int64{2, 1, 3}},
		{"updated asc", "updated", []int64{3, 1, 2}},
		{"updated desc", "updated desc", []int64{2, 1, 3}},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			gotArtifacts, _, err := storage.ListArtifacts(ctx, "", 10, "", "", tc.orderBy, 0, false)
			if err != nil {
				t.Fatalf("ListArtifacts failed: %v", err)
			}
			var gotAIDs []int64
			for _, art := range gotArtifacts {
				aid, _ := validator.ValidateArtifactName(art.GetName())
				gotAIDs = append(gotAIDs, aid)
			}
			if diff := cmp.Diff(tc.want, gotAIDs); diff != "" {
				t.Errorf("ListArtifacts with orderBy %q returned diff (-want +got):\n%s", tc.orderBy, diff)
			}
		})
	}
}

func TestExtractArtifactCursor(t *testing.T) {
	now := time.Now()
	nowProto := timestamppb.New(now)

	testCases := []struct {
		name     string
		artifact *essmsgpb.Artifact
		want     map[string]any
		wantErr  bool
	}{
		{
			name: "valid_artifact_work_unit_parent",
			artifact: &essmsgpb.Artifact{
				Name:       "artifacts/456",
				Parent:     "experiments/123/workUnits/1",
				CreateTime: nowProto,
				UpdateTime: nowProto,
			},
			want: map[string]any{
				"xid":          int64(123),
				"work_unit_id": int64(1),
				"artifact_id":  int64(456),
				"created":      now,
				"updated":      now,
			},
			wantErr: false,
		},
		{
			name: "valid_artifact_experiment_parent",
			artifact: &essmsgpb.Artifact{
				Name:       "artifacts/456",
				Parent:     "experiments/123",
				CreateTime: nowProto,
				UpdateTime: nowProto,
			},
			want: map[string]any{
				"xid":          int64(123),
				"work_unit_id": int64(0),
				"artifact_id":  int64(456),
				"created":      now,
				"updated":      now,
			},
			wantErr: false,
		},
		{
			name: "valid_artifact_no_timestamps",
			artifact: &essmsgpb.Artifact{
				Name:   "artifacts/456",
				Parent: "experiments/123",
			},
			want: map[string]any{
				"xid":          int64(123),
				"work_unit_id": int64(0),
				"artifact_id":  int64(456),
			},
			wantErr: false,
		},
		{
			name: "invalid_artifact_name",
			artifact: &essmsgpb.Artifact{
				Name:   "invalid/456",
				Parent: "experiments/123",
			},
			want:    nil,
			wantErr: true,
		},
		{
			name: "invalid_artifact_parent",
			artifact: &essmsgpb.Artifact{
				Name:   "artifacts/456",
				Parent: "invalid/123",
			},
			want:    nil,
			wantErr: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := extractArtifactCursor(tc.artifact)
			if (err != nil) != tc.wantErr {
				t.Errorf("extractArtifactCursor(%v) returned error %v, wantErr=%t", tc.artifact, err, tc.wantErr)
			}
			if diff := cmp.Diff(tc.want, got); diff != "" {
				t.Errorf("extractArtifactCursor(%v) returned diff (-want +got):\n%s", tc.artifact, diff)
			}
		})
	}
}
