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

package artifact

import (
	"context"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	timestamppb "google.golang.org/protobuf/types/known/timestamppb"
	pb "xmanager_cloud/api/protos/experiment"
	"xmanager_cloud/internal/storage"
)

func TestNewRepository_BindsPool(t *testing.T) {
	pool := &pgxpool.Pool{}
	repo := NewRepository(pool)
	if repo.db != pool {
		t.Errorf("NewRepository(pool).db = %v, want %p", repo.db, pool)
	}
	if repo.pool != pool {
		t.Errorf("NewRepository(pool).pool = %p, want %p", repo.pool, pool)
	}
}

func TestNewRepository_NilPool(t *testing.T) {
	repo := NewRepository(nil)
	if repo.db != nil {
		t.Errorf("NewRepository(nil).db = %v, want nil", repo.db)
	}

	// Read-modify-write operations need a pool to start their transaction and must fail cleanly.
	_, err := repo.UpdateArtifact(context.Background(), &storage.ArtifactUpdate{AID: 1, Artifact: &pb.Artifact{}})
	if got := status.Code(err); got != codes.FailedPrecondition {
		t.Errorf("UpdateArtifact() without pool code = %v, want %v (err: %v)", got, codes.FailedPrecondition, err)
	}
}

func TestExtractArtifactCursor(t *testing.T) {
	now := time.Now()
	nowProto := timestamppb.New(now)

	testCases := []struct {
		name     string
		artifact *pb.Artifact
		want     map[string]any
		wantErr  bool
	}{
		{
			name: "valid_artifact_work_unit_parent",
			artifact: &pb.Artifact{
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
				"display_name": "",
				"priority":     int64(0),
			},
		},
		{
			name: "valid_artifact_with_url_and_priority",
			artifact: &pb.Artifact{
				Name:       "artifacts/456",
				Parent:     "experiments/123/workUnits/1",
				CreateTime: nowProto,
				UpdateTime: nowProto,
				Url: &pb.Url{
					DisplayName: "test-display",
					Priority:    10,
				},
				Priority: 5,
			},
			want: map[string]any{
				"xid":          int64(123),
				"work_unit_id": int64(1),
				"artifact_id":  int64(456),
				"created":      now,
				"updated":      now,
				"display_name": "test-display",
				"priority":     int64(5),
			},
		},
		{
			name: "url_priority_used_when_priority_unset",
			artifact: &pb.Artifact{
				Name:   "artifacts/456",
				Parent: "experiments/123",
				Url:    &pb.Url{Priority: 10},
			},
			want: map[string]any{
				"xid":          int64(123),
				"work_unit_id": nil,
				"artifact_id":  int64(456),
				"display_name": "",
				"priority":     int64(10),
			},
		},
		{
			name: "valid_artifact_experiment_parent",
			artifact: &pb.Artifact{
				Name:       "artifacts/456",
				Parent:     "experiments/123",
				CreateTime: nowProto,
				UpdateTime: nowProto,
			},
			want: map[string]any{
				"xid":          int64(123),
				"work_unit_id": nil,
				"artifact_id":  int64(456),
				"created":      now,
				"updated":      now,
				"display_name": "",
				"priority":     int64(0),
			},
		},
		{
			name: "valid_artifact_no_timestamps",
			artifact: &pb.Artifact{
				Name:   "artifacts/456",
				Parent: "experiments/123",
			},
			want: map[string]any{
				"xid":          int64(123),
				"work_unit_id": nil,
				"artifact_id":  int64(456),
				"display_name": "",
				"priority":     int64(0),
			},
		},
		{
			name: "invalid_artifact_name",
			artifact: &pb.Artifact{
				Name:   "invalid/456",
				Parent: "experiments/123",
			},
			wantErr: true,
		},
		{
			name: "invalid_artifact_parent",
			artifact: &pb.Artifact{
				Name:   "artifacts/456",
				Parent: "invalid/123",
			},
			wantErr: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := ExtractArtifactCursor(tc.artifact)
			if (err != nil) != tc.wantErr {
				t.Errorf("ExtractArtifactCursor(%v) returned error %v, wantErr=%t", tc.artifact, err, tc.wantErr)
			}
			if diff := cmp.Diff(tc.want, got); diff != "" {
				t.Errorf("ExtractArtifactCursor(%v) returned diff (-want +got):\n%s", tc.artifact, diff)
			}
		})
	}
}

func TestBuildParentFilter(t *testing.T) {
	tests := []struct {
		name    string
		parent  string
		filter  string
		want    string
		wantErr bool
	}{
		{name: "no_parent_keeps_filter", parent: "", filter: `notes="x"`, want: `notes="x"`},
		{name: "experiment_parent", parent: "experiments/123", want: "xid = 123"},
		{name: "work_unit_parent", parent: "experiments/123/workUnits/4", want: "xid = 123 AND work_unit_id = 4"},
		{name: "experiment_parent_with_filter", parent: "experiments/123", filter: `notes="x" OR tags:"y"`, want: `xid = 123 AND (notes="x" OR tags:"y")`},
		{name: "work_unit_parent_with_filter", parent: "experiments/123/workUnits/4", filter: `notes="x"`, want: `xid = 123 AND work_unit_id = 4 AND (notes="x")`},
		{name: "invalid_parent", parent: "artifacts/123", wantErr: true},
		{name: "zero_xid", parent: "experiments/0", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, _, err := buildParentFilter(context.Background(), tt.parent, tt.filter)
			if (err != nil) != tt.wantErr {
				t.Fatalf("buildParentFilter(%q, %q) error = %v, wantErr %t", tt.parent, tt.filter, err, tt.wantErr)
			}
			if tt.wantErr && status.Code(err) != codes.InvalidArgument {
				t.Errorf("buildParentFilter(%q, %q) code = %v, want %v", tt.parent, tt.filter, status.Code(err), codes.InvalidArgument)
			}
			if got != tt.want {
				t.Errorf("buildParentFilter(%q, %q) = %q, want %q", tt.parent, tt.filter, got, tt.want)
			}
		})
	}
}

func TestPayloadType(t *testing.T) {
	tests := []struct {
		name    string
		payload *pb.ArtifactPayload
		want    string
	}{
		{name: "nil_payload", payload: nil, want: ""},
		{name: "unset_oneof", payload: &pb.ArtifactPayload{}, want: ""},
		{name: "managed_xprof", payload: &pb.ArtifactPayload{Type: &pb.ArtifactPayload_ManagedXprof{ManagedXprof: &pb.ManagedXprofArtifact{}}}, want: "managed_xprof"},
		{name: "code_source", payload: &pb.ArtifactPayload{Type: &pb.ArtifactPayload_CodeSource{CodeSource: &pb.CodeSourceArtifact{}}}, want: "code_source"},
		{name: "text", payload: &pb.ArtifactPayload{Type: &pb.ArtifactPayload_Text{Text: &pb.TextArtifact{}}}, want: "text"},
		{name: "url", payload: &pb.ArtifactPayload{Type: &pb.ArtifactPayload_Url{Url: &pb.UrlArtifact{}}}, want: "url"},
		{name: "chart", payload: &pb.ArtifactPayload{Type: &pb.ArtifactPayload_Chart{Chart: &pb.ChartArtifact{}}}, want: "chart"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := payloadType(tt.payload); got != tt.want {
				t.Errorf("payloadType() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestMarshalNullableProto(t *testing.T) {
	tests := []struct {
		name string
		msg  *pb.ArtifactPayload
		// wantNil expects SQL NULL; otherwise wantEmpty distinguishes a present empty message
		// from an encoded one.
		wantNil   bool
		wantEmpty bool
	}{
		{name: "nil_message_is_NULL", msg: nil, wantNil: true},
		{name: "empty_message_is_present", msg: &pb.ArtifactPayload{}, wantEmpty: true},
		{name: "populated_message_is_encoded", msg: &pb.ArtifactPayload{Type: &pb.ArtifactPayload_Text{Text: &pb.TextArtifact{Title: "t"}}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := marshalNullableProto(tt.msg)
			if err != nil {
				t.Fatalf("marshalNullableProto() unexpected error: %v", err)
			}
			if (got == nil) != tt.wantNil {
				t.Fatalf("marshalNullableProto() = %v, want nil: %t", got, tt.wantNil)
			}
			if !tt.wantNil && (len(got) == 0) != tt.wantEmpty {
				t.Errorf("marshalNullableProto() = %v, want empty: %t", got, tt.wantEmpty)
			}
		})
	}
}

func TestMutableColumnArgs_DerivedColumns(t *testing.T) {
	tests := []struct {
		name                  string
		artifact              *pb.Artifact
		wantPriority          int64
		wantTags              []string
		wantPayloadType       string
		wantSearchableContent string
		wantAdditionalInfoNil bool
		wantPayloadNil        bool
	}{
		{
			name:                  "defaults",
			artifact:              &pb.Artifact{},
			wantTags:              []string{},
			wantAdditionalInfoNil: true,
			wantPayloadNil:        true,
		},
		{
			name:         "priority_falls_back_to_url_priority",
			artifact:     &pb.Artifact{Url: &pb.Url{Priority: 7}},
			wantPriority: 7,
			wantTags:     []string{},
			// A nil payload and additional info stay NULL.
			wantAdditionalInfoNil: true,
			wantPayloadNil:        true,
		},
		{
			name:                  "top_level_priority_wins",
			artifact:              &pb.Artifact{Priority: 3, Url: &pb.Url{Priority: 7}},
			wantPriority:          3,
			wantTags:              []string{},
			wantAdditionalInfoNil: true,
			wantPayloadNil:        true,
		},
		{
			name: "payload_derives_type_and_searchable_content",
			artifact: &pb.Artifact{
				Tags:           []string{"a", "b"},
				AdditionalInfo: &pb.Artifact_AdditionalInfo{Title: "info"},
				Payload: &pb.ArtifactPayload{Type: &pb.ArtifactPayload_ManagedXprof{ManagedXprof: &pb.ManagedXprofArtifact{
					DisplayName: "xprof-display",
					RunGroup:    "xprof-run-group",
				}}},
			},
			wantTags:              []string{"a", "b"},
			wantPayloadType:       "managed_xprof",
			wantSearchableContent: "xprof-display xprof-run-group",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			args, err := mutableColumnArgs(tt.artifact)
			if err != nil {
				t.Fatalf("mutableColumnArgs() unexpected error: %v", err)
			}
			if len(args) != 13 {
				t.Fatalf("mutableColumnArgs() returned %d args, want 13", len(args))
			}
			if got := args[3]; got != tt.wantPriority {
				t.Errorf("Priority arg = %v, want %v", got, tt.wantPriority)
			}
			if diff := cmp.Diff(tt.wantTags, args[4]); diff != "" {
				t.Errorf("Tags arg mismatch (-want +got):\n%s", diff)
			}
			if got := args[9]; (got.([]byte) == nil) != tt.wantAdditionalInfoNil {
				t.Errorf("AdditionalInfo arg = %v, want nil: %t", got, tt.wantAdditionalInfoNil)
			}
			if got := args[10]; (got.([]byte) == nil) != tt.wantPayloadNil {
				t.Errorf("Payload arg = %v, want nil: %t", got, tt.wantPayloadNil)
			}
			if got := args[11]; got != tt.wantPayloadType {
				t.Errorf("PayloadType arg = %v, want %q", got, tt.wantPayloadType)
			}
			if got := args[12]; got != tt.wantSearchableContent {
				t.Errorf("SearchableContent arg = %v, want %q", got, tt.wantSearchableContent)
			}
		})
	}
}
