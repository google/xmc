// Copyright 2026 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//      http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package grpcerr

import (
	"errors"
	"fmt"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestSentinelErrors(t *testing.T) {
	tests := []struct {
		name     string
		sentinel error
		wantCode codes.Code
	}{
		{name: "InvalidArgument", sentinel: ErrInvalidArgument, wantCode: codes.InvalidArgument},
		{name: "NotFound", sentinel: ErrNotFound, wantCode: codes.NotFound},
		{name: "AlreadyExists", sentinel: ErrAlreadyExists, wantCode: codes.AlreadyExists},
		{name: "PermissionDenied", sentinel: ErrPermissionDenied, wantCode: codes.PermissionDenied},
		{name: "Internal", sentinel: ErrInternal, wantCode: codes.Internal},
		{name: "Unimplemented", sentinel: ErrUnimplemented, wantCode: codes.Unimplemented},
		{name: "FailedPrecondition", sentinel: ErrFailedPrecondition, wantCode: codes.FailedPrecondition},
		{name: "Aborted", sentinel: ErrAborted, wantCode: codes.Aborted},
		{name: "Unavailable", sentinel: ErrUnavailable, wantCode: codes.Unavailable},
		{name: "DeadlineExceeded", sentinel: ErrDeadlineExceeded, wantCode: codes.DeadlineExceeded},
		{name: "Unauthenticated", sentinel: ErrUnauthenticated, wantCode: codes.Unauthenticated},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := status.Code(tt.sentinel); got != tt.wantCode {
				t.Errorf("status.Code(%v) = %v, want %v", tt.sentinel, got, tt.wantCode)
			}
			wrapped := fmt.Errorf("context: %w", tt.sentinel)
			if !errors.Is(wrapped, tt.sentinel) {
				t.Errorf("errors.Is(wrapped, %v) = false, want true", tt.sentinel)
			}
		})
	}
}
