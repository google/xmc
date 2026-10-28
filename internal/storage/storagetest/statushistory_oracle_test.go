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

package storagetest

import (
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
)

var oracleTestBase = time.Date(2026, 8, 20, 12, 0, 0, 0, time.UTC)

// The reference semantics must agree with the hand-written cases, so the randomized comparison in
// RunStatusHistoryTests cannot pass two backends against a wrong reference.
func TestExpectedQueueVelocity_MatchesCases(t *testing.T) {
	for _, tc := range velocityCases {
		t.Run(tc.name, func(t *testing.T) {
			got := expectedQueueVelocity(historyRows(tc.history, oracleTestBase), tc.queues, oracleTestBase.Add(-velocityWindow))
			assertQueueMetrics(t, tc.want, got)
		})
	}
}

func TestExpectedPendingQueueMetrics_MatchesCases(t *testing.T) {
	for _, tc := range pendingCases {
		t.Run(tc.name, func(t *testing.T) {
			got := expectedPendingQueueMetrics(historyRows(tc.history, oracleTestBase), tc.queues)
			assertQueueMetrics(t, tc.want, got)
		})
	}
}

func TestRandomStatusHistory_IsDeterministic(t *testing.T) {
	if diff := cmp.Diff(randomStatusHistory(7, 20), randomStatusHistory(7, 20)); diff != "" {
		t.Errorf("randomStatusHistory(7, 20) differs between calls (-first +second):\n%s", diff)
	}
}

// Every random offset must stay clear of the velocity window boundary, or a slow test run could
// move a row across the cutoff between seeding and querying.
func TestRandomOffsets_AvoidWindowBoundary(t *testing.T) {
	const margin = 5 * time.Minute
	for _, m := range randomOffsetsMinutes {
		offset := time.Duration(m) * time.Minute
		if d := (offset + velocityWindow).Abs(); d < margin {
			t.Errorf("offset %v is %v from the %v window boundary, want at least %v", offset, d, velocityWindow, margin)
		}
	}
}
