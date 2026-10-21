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
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/testing/protocmp"
)

// baseRequestSource is a valid source that test cases modify.
var baseRequestSource = RequestSource{UID: "uid-a", Generation: 3, Namespace: "xmc", ClusterUID: "11111111-1111-4111-8111-111111111111", Epoch: 1}

// modifiedSource returns baseRequestSource changed by mutate.
func modifiedSource(mutate func(*RequestSource)) RequestSource {
	src := baseRequestSource
	mutate(&src)
	return src
}

func TestValidateRequestSource(t *testing.T) {
	tests := []struct {
		name    string
		src     RequestSource
		wantErr bool
	}{
		{name: "valid source", src: baseRequestSource},
		{name: "empty uid", src: modifiedSource(func(s *RequestSource) { s.UID = "" }), wantErr: true},
		{name: "uid wider than the column", src: modifiedSource(func(s *RequestSource) { s.UID = strings.Repeat("u", 65) }), wantErr: true},
		{name: "zero generation", src: modifiedSource(func(s *RequestSource) { s.Generation = 0 }), wantErr: true},
		{name: "empty namespace", src: modifiedSource(func(s *RequestSource) { s.Namespace = "" }), wantErr: true},
		{name: "namespace wider than the column", src: modifiedSource(func(s *RequestSource) { s.Namespace = strings.Repeat("n", 64) }), wantErr: true},
		{name: "namespace that is not a DNS-1123 label", src: modifiedSource(func(s *RequestSource) { s.Namespace = "XMC_ns" }), wantErr: true},
		{name: "empty cluster uid", src: modifiedSource(func(s *RequestSource) { s.ClusterUID = "" }), wantErr: true},
		{name: "cluster uid that is not a UUID", src: modifiedSource(func(s *RequestSource) { s.ClusterUID = "cluster-1" }), wantErr: true},
		{name: "cluster uid that is an uppercase UUID", src: modifiedSource(func(s *RequestSource) { s.ClusterUID = "5A6B7C8D-1E2F-4A3B-8C4D-5E6F7A8B9C01" }), wantErr: true},
		{name: "cluster uid that is a braced UUID", src: modifiedSource(func(s *RequestSource) { s.ClusterUID = "{5a6b7c8d-1e2f-4a3b-8c4d-5e6f7a8b9c01}" }), wantErr: true},
		{name: "zero epoch", src: modifiedSource(func(s *RequestSource) { s.Epoch = 0 }), wantErr: true},
		{name: "valid cluster name", src: modifiedSource(func(s *RequestSource) { s.ClusterName = "gke-1" })},
		{name: "cluster name that is not a DNS-1123 label", src: modifiedSource(func(s *RequestSource) { s.ClusterName = "GKE_1" }), wantErr: true},
		{name: "cluster name wider than a label", src: modifiedSource(func(s *RequestSource) { s.ClusterName = strings.Repeat("c", 64) }), wantErr: true},
		{name: "valid CAS guard", src: modifiedSource(func(s *RequestSource) { s.ExpectedOwnerUID = "uid-b" })},
		{name: "CAS guard wider than the column", src: modifiedSource(func(s *RequestSource) { s.ExpectedOwnerUID = strings.Repeat("u", 65) }), wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateRequestSource(tt.src)
			if gotErr := err != nil; gotErr != tt.wantErr {
				t.Fatalf("ValidateRequestSource() error = %v, want error %t", err, tt.wantErr)
			}
			if tt.wantErr && status.Code(err) != codes.InvalidArgument {
				t.Errorf("ValidateRequestSource() code = %v, want %v", status.Code(err), codes.InvalidArgument)
			}
		})
	}
}

func TestOptionalRequestSource(t *testing.T) {
	invalid := modifiedSource(func(s *RequestSource) { s.UID = "" })
	tests := []struct {
		name     string
		srcs     []RequestSource
		want     *RequestSource
		wantCode codes.Code
	}{
		{name: "no source", srcs: nil, want: nil},
		{name: "one valid source", srcs: []RequestSource{baseRequestSource}, want: &baseRequestSource},
		{name: "one invalid source", srcs: []RequestSource{invalid}, wantCode: codes.InvalidArgument},
		{name: "more than one source", srcs: []RequestSource{baseRequestSource, baseRequestSource}, wantCode: codes.InvalidArgument},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := OptionalRequestSource(tt.srcs)
			if status.Code(err) != tt.wantCode {
				t.Fatalf("OptionalRequestSource() error = %v, want code %v", err, tt.wantCode)
			}
			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("OptionalRequestSource() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestSourceOwnerRelations(t *testing.T) {
	owner := OwnerOf(baseRequestSource)
	tests := []struct {
		name                string
		owner               *SourceOwner
		src                 RequestSource
		wantRecords         bool
		wantOlderGeneration bool
	}{
		{name: "nil owner", src: modifiedSource(func(s *RequestSource) { s.Generation = 1 })},
		{name: "same source", owner: owner, src: baseRequestSource, wantRecords: true},
		{name: "same source with a CAS guard", owner: owner, src: modifiedSource(func(s *RequestSource) { s.ExpectedOwnerUID = "uid-z" }), wantRecords: true},
		{name: "owner object at an older generation", owner: owner, src: modifiedSource(func(s *RequestSource) { s.Generation = 1 }), wantOlderGeneration: true},
		{name: "owner object at a newer generation", owner: owner, src: modifiedSource(func(s *RequestSource) { s.Generation = 4 })},
		{name: "owner object at a new epoch", owner: owner, src: modifiedSource(func(s *RequestSource) { s.Epoch = 2 })},
		{name: "another object at an older generation", owner: owner, src: modifiedSource(func(s *RequestSource) { s.UID, s.Generation = "uid-b", 1 })},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.owner.Records(tt.src); got != tt.wantRecords {
				t.Errorf("Records() = %t, want %t", got, tt.wantRecords)
			}
			if got := tt.owner.IsOlderGeneration(tt.src); got != tt.wantOlderGeneration {
				t.Errorf("IsOlderGeneration() = %t, want %t", got, tt.wantOlderGeneration)
			}
		})
	}
}

func TestStaleGenerationError(t *testing.T) {
	st := status.Convert(StaleGenerationError("redpanda", OwnerOf(baseRequestSource), modifiedSource(func(s *RequestSource) { s.Generation = 1 })))

	if st.Code() != codes.FailedPrecondition {
		t.Errorf("code = %v, want %v", st.Code(), codes.FailedPrecondition)
	}
	want := []any{&errdetails.ErrorInfo{
		Reason: "STALE_GENERATION",
		Domain: ErrorInfoDomain,
		Metadata: map[string]string{
			"operation": "SetComponentIntent", "owner_namespace": "xmc", "owner_uid": "uid-a", "owner_cluster_uid": baseRequestSource.ClusterUID, "owner_generation": "3",
		},
	}}
	if diff := cmp.Diff(want, st.Details(), protocmp.Transform()); diff != "" {
		t.Errorf("details mismatch (-want +got):\n%s", diff)
	}
}

func TestCheckSourceOwnership(t *testing.T) {
	owner := OwnerOf(baseRequestSource)
	tests := []struct {
		name       string
		owner      *SourceOwner
		src        RequestSource
		wantReason string
	}{
		{name: "no recorded owner is adopted (R2)", src: modifiedSource(func(s *RequestSource) { s.Namespace = "other" })},
		{name: "owner without a UID after a takeover is adopted in its namespace and cluster (R2)", owner: &SourceOwner{Namespace: "xmc", ClusterUID: "cluster-2", Epoch: 2}, src: modifiedSource(func(s *RequestSource) { s.UID, s.ClusterUID = "uid-b", "cluster-2" })},
		{name: "owner without a UID after a takeover rejects the fenced-out cluster (R6)", owner: &SourceOwner{Namespace: "xmc", ClusterUID: "cluster-2", Epoch: 2}, src: modifiedSource(func(s *RequestSource) { s.UID = "uid-b" }), wantReason: "SOURCE_INCARNATION_MISMATCH"},
		{name: "owner object writes (R3)", owner: owner, src: modifiedSource(func(s *RequestSource) { s.Generation = 1 })},
		{name: "another namespace conflicts (R4)", owner: owner, src: modifiedSource(func(s *RequestSource) { s.Namespace = "other" }), wantReason: "SOURCE_NAMESPACE_CONFLICT"},
		{
			name:       "another namespace conflicts even with a CAS guard (R4)",
			owner:      owner,
			src:        modifiedSource(func(s *RequestSource) { s.Namespace, s.UID, s.ExpectedOwnerUID = "other", "uid-b", "uid-a" }),
			wantReason: "SOURCE_NAMESPACE_CONFLICT",
		},
		{name: "another incarnation naming the owner adopts (R5)", owner: owner, src: modifiedSource(func(s *RequestSource) { s.UID, s.ExpectedOwnerUID = "uid-b", "uid-a" })},
		{name: "another incarnation without a CAS guard mismatches (R6)", owner: owner, src: modifiedSource(func(s *RequestSource) { s.UID = "uid-b" }), wantReason: "SOURCE_INCARNATION_MISMATCH"},
		{
			name:       "another incarnation naming another owner mismatches (R6)",
			owner:      owner,
			src:        modifiedSource(func(s *RequestSource) { s.UID, s.ExpectedOwnerUID = "uid-b", "uid-z" }),
			wantReason: "SOURCE_INCARNATION_MISMATCH",
		},
		{
			name:  "another cluster naming the owner mismatches (R6)",
			owner: owner,
			src: modifiedSource(func(s *RequestSource) {
				s.UID, s.ExpectedOwnerUID, s.ClusterUID = "uid-b", "uid-a", "22222222-2222-4222-8222-222222222222"
			}),
			wantReason: "SOURCE_INCARNATION_MISMATCH",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := CheckSourceOwnership("SetComponentIntent", "redpanda", tt.owner, tt.src)
			if tt.wantReason == "" {
				if err != nil {
					t.Fatalf("CheckSourceOwnership() error = %v, want nil", err)
				}
				return
			}
			st := status.Convert(err)
			want := []any{&errdetails.ErrorInfo{
				Reason:   tt.wantReason,
				Domain:   ErrorInfoDomain,
				Metadata: map[string]string{"operation": "SetComponentIntent", "owner_namespace": "xmc", "owner_uid": tt.owner.UID, "owner_cluster_uid": tt.owner.ClusterUID},
			}}
			if st.Code() != codes.FailedPrecondition {
				t.Errorf("CheckSourceOwnership() code = %v, want %v", st.Code(), codes.FailedPrecondition)
			}
			if diff := cmp.Diff(want, st.Details(), protocmp.Transform()); diff != "" {
				t.Errorf("CheckSourceOwnership() details mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestCheckSourceCreation(t *testing.T) {
	if err := CheckSourceCreation("SetComponentIntent", "redpanda", baseRequestSource); err != nil {
		t.Errorf("CheckSourceCreation() without a CAS guard error = %v, want nil (R1 creates)", err)
	}

	casAdopter := modifiedSource(func(s *RequestSource) { s.UID, s.ExpectedOwnerUID = "uid-b", "uid-a" })
	st := status.Convert(CheckSourceCreation("SetComponentIntent", "redpanda", casAdopter))
	want := []any{&errdetails.ErrorInfo{
		Reason:   "SOURCE_INCARNATION_MISMATCH",
		Domain:   ErrorInfoDomain,
		Metadata: map[string]string{"operation": "SetComponentIntent", "owner_namespace": "", "owner_uid": "", "owner_cluster_uid": ""},
	}}
	if st.Code() != codes.FailedPrecondition {
		t.Errorf("CheckSourceCreation() with a CAS guard code = %v, want %v", st.Code(), codes.FailedPrecondition)
	}
	if diff := cmp.Diff(want, st.Details(), protocmp.Transform()); diff != "" {
		t.Errorf("CheckSourceCreation() with a CAS guard details mismatch (-want +got):\n%s", diff)
	}
}
