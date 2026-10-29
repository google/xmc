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

package postgresdata

import (
	"testing"
	"time"
)

func TestMonotonicUpdateTime(t *testing.T) {
	base := time.Date(2026, time.January, 1, 12, 0, 0, 123000, time.UTC)
	tests := []struct {
		name string
		now  time.Time
		prev time.Time
		want time.Time
	}{
		{
			name: "now strictly after prev truncates to microsecond",
			now:  base.Add(5*time.Microsecond + 900*time.Nanosecond),
			prev: base,
			want: base.Add(5 * time.Microsecond),
		},
		{
			name: "now within same microsecond as prev advances by 1 microsecond",
			now:  base.Add(500 * time.Nanosecond),
			prev: base,
			want: base.Add(time.Microsecond),
		},
		{
			name: "now before prev advances prev by 1 microsecond",
			now:  base.Add(-time.Second),
			prev: base.Add(400 * time.Nanosecond),
			want: base.Add(time.Microsecond),
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := MonotonicUpdateTime(tc.now, tc.prev); !got.Equal(tc.want) || got.Location() != time.UTC {
				t.Errorf("MonotonicUpdateTime(%v, %v) = %v (%v), want %v (UTC)", tc.now, tc.prev, got, got.Location(), tc.want)
			}
		})
	}
}
