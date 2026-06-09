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

package status

import (
	"errors"
	"testing"
)

func TestCodeToError(t *testing.T) {
	testCases := []struct {
		code Code
		want error
	}{
		{InvalidArgument, ErrInvalidArgument},
		{NotFound, ErrNotFound},
		{AlreadyExists, ErrAlreadyExists},
		{PermissionDenied, ErrPermissionDenied},
		{Internal, ErrInternal},
		{Unimplemented, ErrUnimplemented},
		{FailedPrecondition, ErrFailedPrecondition},
		{Aborted, ErrAborted},
		{Unavailable, ErrUnavailable},
		{DeadlineExceeded, ErrDeadlineExceeded},
		{Unauthenticated, ErrUnauthenticated},
		{Unknown, ErrInternal}, // default fallback
		{DataLoss, ErrInternal}, // default fallback
	}

	for _, tc := range testCases {
		t.Run(tc.want.Error(), func(t *testing.T) {
			got := CodeToError(tc.code)
			if !errors.Is(got, tc.want) {
				t.Errorf("CodeToError(%v) = %v, want %v", tc.code, got, tc.want)
			}
		})
	}
}
