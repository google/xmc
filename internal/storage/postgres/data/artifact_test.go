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
	"google.golang.org/protobuf/testing/protocmp"
	timestamppb "google.golang.org/protobuf/types/known/timestamppb"
	essmsgpb "xmanager_cloud/api/protos/experiment"
	"xmanager_cloud/internal/prototest"
)

var testArtifactTime = time.Date(2026, time.January, 1, 0, 0, 0, 0, time.UTC)

func TestArtifactProtoAndRowRoundTrip(t *testing.T) {
	baseArtifact := &essmsgpb.Artifact{
		Name: "artifacts/789", Parent: "experiments/123/workUnits/456",
		Url:      &essmsgpb.Url{Url: "http://test.com", Icon: "test-icon", DisplayName: "test-display-name", Priority: 100},
		Priority: 100, Tags: []string{"tag1", "tag2"}, Notes: "Test notes",
		LifecyclePhase: essmsgpb.Artifact_INPUT, DataType: essmsgpb.Artifact_URL, MimeType: "text/plain",
		CreateTime: timestamppb.New(testArtifactTime), UpdateTime: timestamppb.New(testArtifactTime),
	}

	tests := []struct {
		name              string
		art               *essmsgpb.Artifact
		wantWorkUnitValid bool
		wantPayloadType   string
		wantSearchable    string
	}{
		{name: "work unit parent without optional protos", art: baseArtifact, wantWorkUnitValid: true},
		{
			name: "experiment parent with additional info",
			art: prototest.Modify(baseArtifact, func(a *essmsgpb.Artifact) {
				a.Parent = "experiments/123"
				a.AdditionalInfo = &essmsgpb.Artifact_AdditionalInfo{
					Title: "Launch Command", Content: &essmsgpb.Artifact_AdditionalInfo_CodeBlock{CodeBlock: "python train.py"},
				}
			}),
			wantWorkUnitValid: false,
		},
		{
			name: "managed xprof payload",
			art: prototest.Modify(baseArtifact, func(a *essmsgpb.Artifact) {
				a.Payload = &essmsgpb.ArtifactPayload{Type: &essmsgpb.ArtifactPayload_ManagedXprof{ManagedXprof: &essmsgpb.ManagedXprofArtifact{DisplayName: "tb-display-name", RunGroup: "tb-run-group"}}}
			}),
			wantWorkUnitValid: true, wantPayloadType: "managed_xprof", wantSearchable: "tb-display-name tb-run-group",
		},
		{
			name: "code source payload",
			art: prototest.Modify(baseArtifact, func(a *essmsgpb.Artifact) {
				a.Payload = &essmsgpb.ArtifactPayload{Type: &essmsgpb.ArtifactPayload_CodeSource{CodeSource: &essmsgpb.CodeSourceArtifact{Title: "my-title", Description: "my-description", CodeBlock: "my-code-block", CodeUrl: "http://code.url"}}}
			}),
			wantWorkUnitValid: true, wantPayloadType: "code_source", wantSearchable: "my-title my-description my-code-block http://code.url",
		},
		{
			name: "text payload",
			art: prototest.Modify(baseArtifact, func(a *essmsgpb.Artifact) {
				a.Payload = &essmsgpb.ArtifactPayload{Type: &essmsgpb.ArtifactPayload_Text{Text: &essmsgpb.TextArtifact{Title: "my-title", Text: "my-text"}}}
			}),
			wantWorkUnitValid: true, wantPayloadType: "text", wantSearchable: "my-title my-text",
		},
		{
			name: "url payload",
			art: prototest.Modify(baseArtifact, func(a *essmsgpb.Artifact) {
				a.Payload = &essmsgpb.ArtifactPayload{Type: &essmsgpb.ArtifactPayload_Url{Url: &essmsgpb.UrlArtifact{Url: "http://example.com/artifact", DisplayName: "Example Link"}}}
			}),
			wantWorkUnitValid: true, wantPayloadType: "url", wantSearchable: "http://example.com/artifact Example Link",
		},
		{
			name: "chart payload",
			art: prototest.Modify(baseArtifact, func(a *essmsgpb.Artifact) {
				a.Payload = &essmsgpb.ArtifactPayload{Type: &essmsgpb.ArtifactPayload_Chart{Chart: &essmsgpb.ChartArtifact{Title: "Test Dashboard", MetricNames: []string{"accuracy", "loss"}}}}
			}),
			wantWorkUnitValid: true, wantPayloadType: "chart", wantSearchable: "Test Dashboard accuracy loss",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			row, err := ArtifactProtoToRow(tc.art)
			if err != nil {
				t.Fatalf("ArtifactProtoToRow() failed: %v", err)
			}
			if row.WorkUnitID.Valid != tc.wantWorkUnitValid || row.PayloadType != tc.wantPayloadType || row.SearchableContent != tc.wantSearchable {
				t.Errorf("row derived fields = (valid=%v, type=%q, search=%q), want (%v, %q, %q)", row.WorkUnitID.Valid, row.PayloadType, row.SearchableContent, tc.wantWorkUnitValid, tc.wantPayloadType, tc.wantSearchable)
			}
			if args := row.GetInsertArgs(); len(args) != 18 || args["createtime"] != testArtifactTime {
				t.Errorf("row.GetInsertArgs() = %v, want 18 entries with matching createtime", args)
			}
			if args := row.GetUpdateArgs(); len(args) != 16 || args["artifactid"] != row.ArtifactID {
				t.Errorf("row.GetUpdateArgs() = %v, want 16 entries with matching artifactid", args)
			}
			got, err := ArtifactRowToProto(row)
			if err != nil {
				t.Fatalf("ArtifactRowToProto() failed: %v", err)
			}
			if diff := cmp.Diff(tc.art, got, protocmp.Transform()); diff != "" {
				t.Errorf("round-trip Artifact mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestArtifactRowToProto_PreservesEmptyNonNilProtoBytes(t *testing.T) {
	got, err := ArtifactRowToProto(ArtifactRow{
		Xid: 123, ArtifactID: 789, AdditionalInfo: []byte{}, Payload: []byte{},
		CreateTime: testArtifactTime, UpdateTime: testArtifactTime,
	})
	if err != nil {
		t.Fatalf("ArtifactRowToProto() failed: %v", err)
	}
	if got.GetAdditionalInfo() == nil || got.GetPayload() == nil {
		t.Errorf("empty non-nil proto bytes unmarshaled to (info=%v, payload=%v), want non-nil empty structs", got.GetAdditionalInfo(), got.GetPayload())
	}
}

func TestArtifactConversionErrors(t *testing.T) {
	corrupt := []byte{0xff, 0xff, 0xff}
	for i, r := range []ArtifactRow{{Xid: 1, ArtifactID: 1, AdditionalInfo: corrupt}, {Xid: 1, ArtifactID: 1, Payload: corrupt}} {
		if _, err := ArtifactRowToProto(r); err == nil {
			t.Errorf("ArtifactRowToProto(badRows[%d]) = nil, want error", i)
		}
	}
	for i, art := range []*essmsgpb.Artifact{{Name: "invalid", Parent: "experiments/1"}, {Name: "artifacts/1", Parent: "invalid/1"}} {
		if _, err := ArtifactProtoToRow(art); err == nil {
			t.Errorf("ArtifactProtoToRow(badProtos[%d]) = nil, want error", i)
		}
	}
}
