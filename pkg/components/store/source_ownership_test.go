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
var baseRequestSource = RequestSource{UID: "uid-a", Generation: 3, Namespace: "xmc", ClusterUID: "cluster-1", Epoch: 1}

// modifiedSource returns baseRequestSource changed by mutate.
func modifiedSource(mutate func(*RequestSource)) RequestSource {
	src := baseRequestSource
	mutate(&src)
	return src
}

// reasonOf returns the ErrorInfo reason of err, or "" when err carries none in ErrorInfoDomain.
func reasonOf(err error) string {
	for _, d := range status.Convert(err).Details() {
		if info, ok := d.(*errdetails.ErrorInfo); ok && info.GetDomain() == ErrorInfoDomain {
			return info.GetReason()
		}
	}
	return ""
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
		{name: "empty cluster uid", src: modifiedSource(func(s *RequestSource) { s.ClusterUID = "" }), wantErr: true},
		{name: "zero epoch", src: modifiedSource(func(s *RequestSource) { s.Epoch = 0 }), wantErr: true},
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

func TestSourceOwnerRelations(t *testing.T) {
	owner := ownerOf(baseRequestSource)
	tests := []struct {
		name            string
		owner           *SourceOwner
		src             RequestSource
		wantRecords     bool
		wantOwnerObject bool
	}{
		{name: "nil owner", src: baseRequestSource},
		{name: "same source", owner: owner, src: baseRequestSource, wantRecords: true, wantOwnerObject: true},
		{name: "owner object at a newer generation", owner: owner, src: modifiedSource(func(s *RequestSource) { s.Generation = 4 }), wantOwnerObject: true},
		{name: "owner object at a new epoch", owner: owner, src: modifiedSource(func(s *RequestSource) { s.Epoch = 2 }), wantOwnerObject: true},
		{name: "another object", owner: owner, src: modifiedSource(func(s *RequestSource) { s.UID = "uid-b" })},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.owner.records(tt.src); got != tt.wantRecords {
				t.Errorf("records() = %t, want %t", got, tt.wantRecords)
			}
			if got := tt.owner.isOwnerObject(tt.src); got != tt.wantOwnerObject {
				t.Errorf("isOwnerObject() = %t, want %t", got, tt.wantOwnerObject)
			}
		})
	}
}

func TestStaleGenerationError(t *testing.T) {
	st := status.Convert(staleGenerationError("SetComponentIntent", "redpanda", ownerOf(baseRequestSource), modifiedSource(func(s *RequestSource) { s.Generation = 1 })))

	if st.Code() != codes.FailedPrecondition {
		t.Errorf("code = %v, want %v", st.Code(), codes.FailedPrecondition)
	}
	want := []any{&errdetails.ErrorInfo{
		Reason: "STALE_GENERATION",
		Domain: ErrorInfoDomain,
		Metadata: map[string]string{
			"operation": "SetComponentIntent", "owner_namespace": "xmc", "owner_uid": "uid-a", "owner_cluster_uid": "cluster-1", "owner_generation": "3",
		},
	}}
	if diff := cmp.Diff(want, st.Details(), protocmp.Transform()); diff != "" {
		t.Errorf("details mismatch (-want +got):\n%s", diff)
	}
}
