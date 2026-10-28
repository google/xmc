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
	"cmp"
	"math/rand/v2"
	"slices"
	"time"

	essmsgpb "xmanager_cloud/api/protos/experiment"
	"xmanager_cloud/internal/storage"
)

// StatusTransition is one SaveWorkUnitStatusHistory call used to seed a backend. Offset is relative
// to the seeding time and must be a whole number of minutes so both backends store it exactly.
type StatusTransition struct {
	XID       int64
	WID       int64
	Queue     string
	Offset    time.Duration
	NewState  essmsgpb.WorkUnitStatus_WorkUnitState
	Resources map[string]int64
}

// historyRow is one stored WorkUnitStatusHistory row, as produced by seeding a StatusTransition.
type historyRow struct {
	xid, wid     int64
	queue        string
	createTime   time.Time
	newState     essmsgpb.WorkUnitStatus_WorkUnitState
	resourceType string
	units        int64
}

type workUnitKey struct{ xid, wid int64 }

type queueResourceKey struct{ queue, resourceType string }

// historyRows expands transitions into the rows a backend stores: one row per resource type.
func historyRows(transitions []StatusTransition, base time.Time) []historyRow {
	var rows []historyRow
	for _, tr := range transitions {
		for resType, units := range tr.Resources {
			rows = append(rows, historyRow{
				xid: tr.XID, wid: tr.WID, queue: tr.Queue, createTime: base.Add(tr.Offset),
				newState: tr.NewState, resourceType: resType, units: units,
			})
		}
	}
	return rows
}

// expectedQueueVelocity is the reference semantics of FetchBatchQueueVelocity: rows in the requested
// queues at or after cutoff that entered STARTING or RUNNING, counted per queue by distinct work
// unit and summed per (queue, resource type).
func expectedQueueVelocity(rows []historyRow, queues []string, cutoff time.Time) []storage.QueueMetricRow {
	var admitted []historyRow
	for _, r := range rows {
		isAdmitted := r.newState == essmsgpb.WorkUnitStatus_STARTING || r.newState == essmsgpb.WorkUnitStatus_RUNNING
		if slices.Contains(queues, r.queue) && !r.createTime.Before(cutoff) && isAdmitted {
			admitted = append(admitted, r)
		}
	}
	return aggregateQueueMetrics(admitted)
}

// expectedPendingQueueMetrics is the reference semantics of FetchBatchPendingQueueMetrics: for each
// work unit with history in the requested queues, the rows at its latest CreateTime (taken over the
// requested queues only, then matched across all queues, mirroring the SQL join) that are SCHEDULING.
func expectedPendingQueueMetrics(rows []historyRow, queues []string) []storage.QueueMetricRow {
	latest := make(map[workUnitKey]time.Time)
	for _, r := range rows {
		k := workUnitKey{r.xid, r.wid}
		if slices.Contains(queues, r.queue) && r.createTime.After(latest[k]) {
			latest[k] = r.createTime
		}
	}
	var pending []historyRow
	for _, r := range rows {
		t, ok := latest[workUnitKey{r.xid, r.wid}]
		if ok && r.createTime.Equal(t) && r.newState == essmsgpb.WorkUnitStatus_SCHEDULING {
			pending = append(pending, r)
		}
	}
	return aggregateQueueMetrics(pending)
}

// aggregateQueueMetrics emits one row per (queue, resource type) with the queue's distinct work unit
// count and the summed units, sorted by queue then resource type. It returns nil for no rows.
func aggregateQueueMetrics(rows []historyRow) []storage.QueueMetricRow {
	jobs := make(map[string]map[workUnitKey]bool)
	units := make(map[queueResourceKey]int64)
	for _, r := range rows {
		if jobs[r.queue] == nil {
			jobs[r.queue] = make(map[workUnitKey]bool)
		}
		jobs[r.queue][workUnitKey{r.xid, r.wid}] = true
		units[queueResourceKey{r.queue, r.resourceType}] += r.units
	}
	var out []storage.QueueMetricRow
	for k, total := range units {
		out = append(out, storage.QueueMetricRow{QueueName: k.queue, Count: int64(len(jobs[k.queue])), TotalUnits: total, ResourceType: k.resourceType})
	}
	slices.SortFunc(out, compareQueueMetricRows)
	return out
}

func compareQueueMetricRows(a, b storage.QueueMetricRow) int {
	return cmp.Or(cmp.Compare(a.QueueName, b.QueueName), cmp.Compare(a.ResourceType, b.ResourceType))
}

// randomOffsetsMinutes keeps every row at least 5 minutes away from the 60-minute velocity window
// boundary, so the few milliseconds between seeding and querying cannot move a row across it. The
// small set also makes CreateTime ties within one work unit common.
var randomOffsetsMinutes = []int{-5, -10, -20, -30, -45, -55, -65, -90, -120}

var randomStates = []essmsgpb.WorkUnitStatus_WorkUnitState{
	essmsgpb.WorkUnitStatus_INITIALIZING,
	essmsgpb.WorkUnitStatus_SCHEDULING,
	essmsgpb.WorkUnitStatus_STARTING,
	essmsgpb.WorkUnitStatus_RUNNING,
}

// randomStatusHistory deterministically generates n transitions for seed over a small domain of
// experiments, work units, queues ("q3" is never queried), states, and resource types, so that
// ties, queue moves, and multi-resource rows occur frequently.
func randomStatusHistory(seed uint64, n int) []StatusTransition {
	rng := rand.New(rand.NewPCG(seed, seed^0x9e3779b97f4a7c15))
	queues := []string{"q1", "q2", "q3"}
	resourceTypes := []string{"", "CPU", "GPU"}
	transitions := make([]StatusTransition, 0, n)
	for range n {
		resources := make(map[string]int64)
		for range 1 + rng.IntN(2) {
			resources[resourceTypes[rng.IntN(len(resourceTypes))]] = rng.Int64N(17)
		}
		transitions = append(transitions, StatusTransition{
			XID:       1 + rng.Int64N(2),
			WID:       1 + rng.Int64N(8),
			Queue:     queues[rng.IntN(len(queues))],
			Offset:    time.Duration(randomOffsetsMinutes[rng.IntN(len(randomOffsetsMinutes))]) * time.Minute,
			NewState:  randomStates[rng.IntN(len(randomStates))],
			Resources: resources,
		})
	}
	return transitions
}
