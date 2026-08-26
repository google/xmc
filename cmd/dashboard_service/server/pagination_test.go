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

package server

import "testing"

func TestClampPageSize(t *testing.T) {
	const (
		defaultPageSize int32 = 10
		maxPageSize     int32 = 100
	)

	testCases := []struct {
		name     string
		pageSize int32
		want     int32
	}{
		{name: "unset falls back to the default", pageSize: 0, want: defaultPageSize},
		{name: "negative falls back to the default", pageSize: -1, want: defaultPageSize},
		{name: "below the cap is preserved", pageSize: 25, want: 25},
		{name: "at the cap is preserved", pageSize: maxPageSize, want: maxPageSize},
		{name: "above the cap is clamped", pageSize: 5000, want: maxPageSize},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			if got := clampPageSize(tc.pageSize, defaultPageSize, maxPageSize); got != tc.want {
				t.Errorf("clampPageSize(%d, %d, %d) = %d, want %d", tc.pageSize, defaultPageSize, maxPageSize, got, tc.want)
			}
		})
	}
}
