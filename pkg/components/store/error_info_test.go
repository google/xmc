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
	"errors"
	"testing"

	"github.com/google/go-cmp/cmp"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/testing/protocmp"
)

func TestErrorWithInfo(t *testing.T) {
	err := ErrorWithInfo(codes.FailedPrecondition, ReasonDependencyNotRegistered, map[string]string{"dependency": "redpanda"}, "dependency missing")

	st := status.Convert(err)
	if st.Code() != codes.FailedPrecondition || st.Message() != "dependency missing" {
		t.Errorf("status = %v %q, want %v %q", st.Code(), st.Message(), codes.FailedPrecondition, "dependency missing")
	}
	want := []any{&errdetails.ErrorInfo{Reason: ReasonDependencyNotRegistered, Domain: "components.xmc.dev", Metadata: map[string]string{"dependency": "redpanda"}}}
	if diff := cmp.Diff(want, st.Details(), protocmp.Transform()); diff != "" {
		t.Errorf("details mismatch (-want +got):\n%s", diff)
	}
}

func TestErrorInfoReason(t *testing.T) {
	foreign, err := status.New(codes.FailedPrecondition, "foreign").WithDetails(&errdetails.ErrorInfo{Reason: ReasonClusterFenced, Domain: "example.com"})
	if err != nil {
		t.Fatalf("WithDetails() error = %v", err)
	}
	tests := []struct {
		name, want string
		err        error
	}{
		{name: "non-status error", err: errors.New("boom"), want: ""},
		{name: "ErrorInfo in a foreign domain", err: foreign.Err(), want: ""},
		{name: "store rejection", err: ErrorWithInfo(codes.FailedPrecondition, ReasonClusterFenced, nil, "fenced"), want: ReasonClusterFenced},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ErrorInfoReason(tt.err); got != tt.want {
				t.Errorf("ErrorInfoReason(%v) = %q, want %q", tt.err, got, tt.want)
			}
		})
	}
}
