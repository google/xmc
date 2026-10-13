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

package storage

import (
	"errors"
	"fmt"
	"testing"

	"github.com/google/go-cmp/cmp"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"xmanager_cloud/internal/grpcerr"
)

func TestWorkUnitNotFoundError(t *testing.T) {
	base := &WorkUnitNotFoundError{XID: 7, WIDs: []int64{2, 5}}
	tests := []struct {
		name string
		err  error
	}{
		{name: "direct", err: base},
		{name: "wrapped with %w", err: fmt.Errorf("transaction failed: %w", base)},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var got *WorkUnitNotFoundError
			if !errors.As(tc.err, &got) {
				t.Fatalf("errors.As(%v, *WorkUnitNotFoundError) = false, want true", tc.err)
			}
			if diff := cmp.Diff(base, got); diff != "" {
				t.Errorf("errors.As target mismatch (-want +got):\n%s", diff)
			}
			if !errors.Is(tc.err, grpcerr.ErrNotFound) {
				t.Errorf("errors.Is(%v, grpcerr.ErrNotFound) = false, want true", tc.err)
			}
			if got := status.Code(tc.err); got != codes.NotFound {
				t.Errorf("status.Code(%v) = %v, want %v", tc.err, got, codes.NotFound)
			}
		})
	}
	if got, want := base.Error(), "work units [2 5] of experiment 7 not found"; got != want {
		t.Errorf("Error() = %q, want %q", got, want)
	}
}
