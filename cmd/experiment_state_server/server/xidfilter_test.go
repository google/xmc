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

import (
	"fmt"
	"testing"

	"xmanager_cloud/internal/storage/data"
)

func TestXidLookupFilter(t *testing.T) {
	xids := []int64{1, 42}
	tests := []struct {
		name    string
		backend string
		want    string
	}{
		{
			name:    "spanner by default uses bit_reversed_xid",
			backend: "",
			want:    fmt.Sprintf("bit_reversed_xid=%d OR bit_reversed_xid=%d", data.ReverseBits(1), data.ReverseBits(42)),
		},
		{
			name:    "spanner explicitly uses bit_reversed_xid",
			backend: "spanner",
			want:    fmt.Sprintf("bit_reversed_xid=%d OR bit_reversed_xid=%d", data.ReverseBits(1), data.ReverseBits(42)),
		},
		{
			name:    "postgres uses xid",
			backend: "postgres",
			want:    "xid=1 OR xid=42",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("DB_BACKEND", tt.backend)
			if got := xidLookupFilter(xids); got != tt.want {
				t.Errorf("xidLookupFilter(%v) = %q, want %q", xids, got, tt.want)
			}
		})
	}
}

func TestXidLookupFilter_Empty(t *testing.T) {
	if got := xidLookupFilter(nil); got != "" {
		t.Errorf("xidLookupFilter(nil) = %q, want empty", got)
	}
}
