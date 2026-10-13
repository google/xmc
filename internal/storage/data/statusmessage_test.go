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

package data

import (
	"testing"
	"time"

	timestamppb "google.golang.org/protobuf/types/known/timestamppb"
)

func TestStatusMessageCreateTime(t *testing.T) {
	now := time.Date(2024, time.January, 5, 0, 0, 0, 0, time.UTC)
	set := time.Date(2024, time.January, 1, 0, 0, 0, 0, time.UTC)
	testCases := []struct {
		name       string
		createTime *timestamppb.Timestamp
		want       time.Time
	}{
		{name: "unset defaults to now", createTime: nil, want: now},
		{name: "set is preserved", createTime: timestamppb.New(set), want: set},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			if got := StatusMessageCreateTime(tc.createTime, now); !got.Equal(tc.want) {
				t.Errorf("StatusMessageCreateTime() = %v, want %v", got, tc.want)
			}
		})
	}
}
