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

package clickhouse

import (
	"testing"
	"time"
)

func TestMicroOrTime_Scan(t *testing.T) {
	tests := []struct {
		name  string
		input any
		want  time.Time
	}{
		{
			name:  "Time",
			input: time.Unix(1700000000, 0),
			want:  time.Unix(1700000000, 0),
		},
		{
			name:  "Int64Micro",
			input: int64(1700000000000000),
			want:  time.Unix(1700000000, 0),
		},
		{
			name:  "Uint64Micro",
			input: uint64(1700000000000000),
			want:  time.Unix(1700000000, 0),
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var m MicroOrTime
			if err := m.Scan(tc.input); err != nil {
				t.Fatalf("unexpected Scan error: %v", err)
			}
			if !m.Time.Equal(tc.want) {
				t.Errorf("got %v, want %v", m.Time, tc.want)
			}
			if protoTS := m.ToProtoTimestamp(); protoTS == nil || protoTS.Seconds != 1700000000 {
				t.Errorf("ToProtoTimestamp() invalid: %v", protoTS)
			}
		})
	}
}

func TestMicroOrTime_ScanError(t *testing.T) {
	var m MicroOrTime
	if err := m.Scan("invalid-type"); err == nil {
		t.Errorf("expected error when scanning string into MicroOrTime")
	}
}
