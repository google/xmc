// Copyright 2026 Google LLC
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

package shared

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"xmanager_cloud/internal/grpcerr"
)

func TestIsPermanentStorageError(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{name: "nil", err: nil, want: false},
		{name: "wrapped NotFound sentinel", err: fmt.Errorf("lookup: %w", grpcerr.ErrNotFound), want: true},
		{name: "raw InvalidArgument status", err: status.Error(codes.InvalidArgument, "bad"), want: true},
		{name: "FailedPrecondition sentinel", err: grpcerr.ErrFailedPrecondition, want: true},
		{name: "PermissionDenied sentinel", err: grpcerr.ErrPermissionDenied, want: true},
		{name: "Unavailable", err: grpcerr.ErrUnavailable, want: false},
		{name: "Aborted", err: status.Error(codes.Aborted, "retry"), want: false},
		{name: "Unknown status", err: status.Error(codes.Unknown, "?"), want: false},
		{name: "plain error", err: errors.New("boom"), want: false},
		{name: "context deadline", err: context.DeadlineExceeded, want: false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := IsPermanentStorageError(tc.err); got != tc.want {
				t.Errorf("IsPermanentStorageError(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}
