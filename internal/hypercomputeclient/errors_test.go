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

package hypercomputeclient

import (
	"errors"
	"fmt"
	"net/http"
	"testing"
)

func TestIsAlreadyExists(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{name: "nil", err: nil, want: false},
		{name: "other error", err: errors.New("deadline exceeded"), want: false},
		{name: "http 409 conflict", err: &APIError{StatusCode: http.StatusConflict, Body: "ALREADY_EXISTS"}, want: true},
		{name: "http other status", err: &APIError{StatusCode: http.StatusInternalServerError}, want: false},
		{name: "lro already exists", err: &OperationError{Code: CodeAlreadyExists}, want: true},
		{name: "lro other code", err: &OperationError{Code: 3}, want: false},
		{name: "wrapped http 409", err: fmt.Errorf("create failed: %w", &APIError{StatusCode: http.StatusConflict}), want: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := IsAlreadyExists(tc.err); got != tc.want {
				t.Errorf("IsAlreadyExists(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}
