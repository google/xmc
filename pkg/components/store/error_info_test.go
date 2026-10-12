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
	"testing"

	"github.com/google/go-cmp/cmp"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/testing/protocmp"
)

func TestErrorWithInfo(t *testing.T) {
	err := errorWithInfo(codes.FailedPrecondition, ReasonDependencyNotRegistered, map[string]string{"dependency": "redpanda"}, "dependency missing")

	st := status.Convert(err)
	if st.Code() != codes.FailedPrecondition || st.Message() != "dependency missing" {
		t.Errorf("status = %v %q, want %v %q", st.Code(), st.Message(), codes.FailedPrecondition, "dependency missing")
	}
	want := []any{&errdetails.ErrorInfo{Reason: ReasonDependencyNotRegistered, Domain: "components.xmc.dev", Metadata: map[string]string{"dependency": "redpanda"}}}
	if diff := cmp.Diff(want, st.Details(), protocmp.Transform()); diff != "" {
		t.Errorf("details mismatch (-want +got):\n%s", diff)
	}
}
