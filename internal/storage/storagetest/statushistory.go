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

// Package storagetest holds backend-agnostic conformance suites that every storage backend
// (Spanner and PostgreSQL) runs against its own test database, so both backends are held to one
// set of expected results instead of separately maintained copies of the same tests.
package storagetest

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	essmsgpb "xmanager_cloud/api/protos/experiment"
	"xmanager_cloud/internal/storage"
)

// StatusHistoryBackend is the part of storage.Storage exercised by RunStatusHistoryTests.
type StatusHistoryBackend interface {
	storage.TransactionRunner
	storage.StatusHistoryStore
}

// NewStatusHistoryBackend returns a fresh, empty backend for one subtest.
type NewStatusHistoryBackend func(t *testing.T) StatusHistoryBackend

const (
	cpu = "CPU"
	gpu = "GPU"
	// velocityWindow is the window used by every velocity case; see randomOffsetsMinutes.
	velocityWindow = time.Hour
	// oracleTransitions is the number of random transitions seeded per oracle seed.
	oracleTransitions = 40
)

// oracleSeeds are fixed so failures reproduce; add a seed to widen coverage.
var oracleSeeds = []uint64{1, 2, 3}

var sortQueueMetricRows = cmpopts.SortSlices(func(a, b storage.QueueMetricRow) bool { return compareQueueMetricRows(a, b) < 0 })

type metricsCase struct {
	name    string
	queues  []string
	history []StatusTransition
	want    []storage.QueueMetricRow
}

func transition(wid int64, queue string, offset time.Duration, state essmsgpb.WorkUnitStatus_WorkUnitState, resources map[string]int64) StatusTransition {
	return StatusTransition{XID: 1, WID: wid, Queue: queue, Offset: offset, NewState: state, Resources: resources}
}

// multiQueueAndMultiResourceCase seeds every transition in state (an admitted or pending state),
// including a multi-resource work unit, an empty resource type, and an unqueried queue "q3".
func multiQueueAndMultiResourceCase(state essmsgpb.WorkUnitStatus_WorkUnitState) metricsCase {
	return metricsCase{
		name:   "MultiQueueAndMultiResource",
		queues: []string{"q1", "q2"},
		history: []StatusTransition{
			transition(101, "q1", -10*time.Minute, state, map[string]int64{cpu: 10, gpu: 4}),
			transition(102, "q1", -8*time.Minute, state, map[string]int64{cpu: 20}),
			transition(201, "q2", -4*time.Minute, state, map[string]int64{"": 0}),
			transition(301, "q3", -4*time.Minute, state, map[string]int64{cpu: 99}),
		},
		want: []storage.QueueMetricRow{
			{QueueName: "q1", Count: 2, TotalUnits: 30, ResourceType: cpu},
			{QueueName: "q1", Count: 2, TotalUnits: 4, ResourceType: gpu},
			{QueueName: "q2", Count: 1, TotalUnits: 0, ResourceType: ""},
		},
	}
}

var velocityCases = []metricsCase{
	{
		name:   "OnlyStartingAndRunningAreAdmitted",
		queues: []string{"q1"},
		history: []StatusTransition{
			transition(101, "q1", -10*time.Minute, essmsgpb.WorkUnitStatus_STARTING, map[string]int64{cpu: 10}),
			transition(102, "q1", -5*time.Minute, essmsgpb.WorkUnitStatus_SCHEDULING, map[string]int64{cpu: 20}),
			transition(103, "q1", -5*time.Minute, essmsgpb.WorkUnitStatus_INITIALIZING, map[string]int64{cpu: 30}),
		},
		want: []storage.QueueMetricRow{{QueueName: "q1", Count: 1, TotalUnits: 10, ResourceType: cpu}},
	},
	{
		name:   "RowsBeforeWindowAreExcluded",
		queues: []string{"q1"},
		history: []StatusTransition{
			transition(101, "q1", -10*time.Minute, essmsgpb.WorkUnitStatus_STARTING, map[string]int64{cpu: 10}),
			transition(102, "q1", -90*time.Minute, essmsgpb.WorkUnitStatus_STARTING, map[string]int64{cpu: 20}),
		},
		want: []storage.QueueMetricRow{{QueueName: "q1", Count: 1, TotalUnits: 10, ResourceType: cpu}},
	},
	multiQueueAndMultiResourceCase(essmsgpb.WorkUnitStatus_RUNNING),
	{
		name:   "WorkUnitAdmittedTwiceIsCountedOnce",
		queues: []string{"q1"},
		history: []StatusTransition{
			transition(101, "q1", -20*time.Minute, essmsgpb.WorkUnitStatus_STARTING, map[string]int64{cpu: 4}),
			transition(101, "q1", -10*time.Minute, essmsgpb.WorkUnitStatus_RUNNING, map[string]int64{cpu: 4}),
		},
		want: []storage.QueueMetricRow{{QueueName: "q1", Count: 1, TotalUnits: 8, ResourceType: cpu}},
	},
	{
		name:    "NoQueues",
		queues:  []string{},
		history: []StatusTransition{transition(101, "q1", -10*time.Minute, essmsgpb.WorkUnitStatus_STARTING, map[string]int64{cpu: 10})},
		want:    nil,
	},
}

var pendingCases = []metricsCase{
	{
		name:   "OnlySchedulingIsPending",
		queues: []string{"q1"},
		history: []StatusTransition{
			transition(101, "q1", -10*time.Minute, essmsgpb.WorkUnitStatus_SCHEDULING, map[string]int64{cpu: 10}),
			transition(102, "q1", -5*time.Minute, essmsgpb.WorkUnitStatus_STARTING, map[string]int64{cpu: 20}),
		},
		want: []storage.QueueMetricRow{{QueueName: "q1", Count: 1, TotalUnits: 10, ResourceType: cpu}},
	},
	{
		name:   "OnlyLatestTransitionCounts",
		queues: []string{"q1"},
		history: []StatusTransition{
			transition(101, "q1", -20*time.Minute, essmsgpb.WorkUnitStatus_SCHEDULING, map[string]int64{cpu: 10}),
			transition(101, "q1", -10*time.Minute, essmsgpb.WorkUnitStatus_RUNNING, map[string]int64{cpu: 10}),
		},
		want: nil,
	},
	{
		name:   "TieAtLatestCreateTimeCountsSchedulingRow",
		queues: []string{"q1"},
		history: []StatusTransition{
			transition(101, "q1", -10*time.Minute, essmsgpb.WorkUnitStatus_SCHEDULING, map[string]int64{cpu: 6}),
			transition(101, "q1", -10*time.Minute, essmsgpb.WorkUnitStatus_RUNNING, map[string]int64{cpu: 6}),
		},
		want: []storage.QueueMetricRow{{QueueName: "q1", Count: 1, TotalUnits: 6, ResourceType: cpu}},
	},
	multiQueueAndMultiResourceCase(essmsgpb.WorkUnitStatus_SCHEDULING),
	{
		name:    "NoQueues",
		queues:  []string{},
		history: []StatusTransition{transition(101, "q1", -10*time.Minute, essmsgpb.WorkUnitStatus_SCHEDULING, map[string]int64{cpu: 10})},
		want:    nil,
	},
}

// RunStatusHistoryTests runs the StatusHistory conformance suite against backends from newBackend:
// hand-written cases with explicit expected rows, then randomized histories compared with the Go
// reference semantics in statushistory_oracle.go.
func RunStatusHistoryTests(t *testing.T, newBackend NewStatusHistoryBackend) {
	t.Run("QueueVelocity", func(t *testing.T) {
		for _, tc := range velocityCases {
			t.Run(tc.name, func(t *testing.T) {
				got := seedAndFetchVelocity(t, newBackend(t), tc.history, tc.queues)
				assertQueueMetrics(t, tc.want, got)
			})
		}
	})
	t.Run("PendingQueueMetrics", func(t *testing.T) {
		for _, tc := range pendingCases {
			t.Run(tc.name, func(t *testing.T) {
				got := seedAndFetchPending(t, newBackend(t), tc.history, tc.queues)
				assertQueueMetrics(t, tc.want, got)
			})
		}
	})
	t.Run("MatchesReferenceSemantics", func(t *testing.T) {
		for _, seed := range oracleSeeds {
			t.Run(fmt.Sprintf("seed=%d", seed), func(t *testing.T) {
				runOracleSeed(t, newBackend(t), seed)
			})
		}
	})
}

func runOracleSeed(t *testing.T, backend StatusHistoryBackend, seed uint64) {
	t.Helper()
	history := randomStatusHistory(seed, oracleTransitions)
	queues := []string{"q1", "q2"}
	base := seedHistory(t, backend, history)
	rows := historyRows(history, base)

	gotVelocity, err := backend.FetchBatchQueueVelocity(t.Context(), queues, velocityWindow)
	if err != nil {
		t.Fatalf("FetchBatchQueueVelocity() failed: %v", err)
	}
	assertQueueMetrics(t, expectedQueueVelocity(rows, queues, base.Add(-velocityWindow)), gotVelocity)

	gotPending, err := backend.FetchBatchPendingQueueMetrics(t.Context(), queues)
	if err != nil {
		t.Fatalf("FetchBatchPendingQueueMetrics() failed: %v", err)
	}
	assertQueueMetrics(t, expectedPendingQueueMetrics(rows, queues), gotPending)
}

func seedAndFetchVelocity(t *testing.T, backend StatusHistoryBackend, history []StatusTransition, queues []string) []storage.QueueMetricRow {
	t.Helper()
	seedHistory(t, backend, history)
	got, err := backend.FetchBatchQueueVelocity(t.Context(), queues, velocityWindow)
	if err != nil {
		t.Fatalf("FetchBatchQueueVelocity() failed: %v", err)
	}
	return got
}

func seedAndFetchPending(t *testing.T, backend StatusHistoryBackend, history []StatusTransition, queues []string) []storage.QueueMetricRow {
	t.Helper()
	seedHistory(t, backend, history)
	got, err := backend.FetchBatchPendingQueueMetrics(t.Context(), queues)
	if err != nil {
		t.Fatalf("FetchBatchPendingQueueMetrics() failed: %v", err)
	}
	return got
}

// seedHistory writes history through the backend's public transactional API and returns the base time
// that the transition offsets are relative to, truncated to whole seconds so both backends store
// timestamps exactly.
func seedHistory(t *testing.T, backend StatusHistoryBackend, history []StatusTransition) time.Time {
	t.Helper()
	base := time.Now().UTC().Truncate(time.Second)
	err := backend.RunInTransaction(t.Context(), func(ctx context.Context, tx *storage.Transaction) error {
		for _, tr := range history {
			state := tr.NewState
			if err := tx.StatusHistory.SaveWorkUnitStatusHistory(ctx, tr.XID, tr.WID, nil, tr.Queue, nil, &state, tr.Resources, base.Add(tr.Offset)); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("seeding status history failed: %v", err)
	}
	return base
}

func assertQueueMetrics(t *testing.T, want, got []storage.QueueMetricRow) {
	t.Helper()
	if diff := cmp.Diff(want, got, sortQueueMetricRows, cmpopts.EquateEmpty()); diff != "" {
		t.Errorf("QueueMetricRow mismatch (-want +got):\n%s", diff)
	}
}
