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

package statushistory

import (
	"context"
	"crypto/rand"
	"math/big"
	"strconv"
	"strings"
	"time"

	"golang/cloud_google_com/go/spanner/v/v1/spanner"
	"google.golang.org/api/iterator"
	essmsgpb "xmanager_cloud/experiment_state_server/proto/messages_go_proto"
	"xmanager_cloud/logging/logging"
	spannerdata "xmanager_cloud/storage/spanner/data/spannerdata"
)

// QueueVelocityResult contains velocity metrics retrieved from Spanner.
type QueueVelocityResult struct {
	AdmittedJobsCount int64
	ResourceUnits     int64
	ResourceType      string
	ResourceUnitsMap  map[string]int64
}

// PendingQueueMetricsResult contains pending metrics retrieved from Spanner.
type PendingQueueMetricsResult struct {
	PendingJobs             int64
	PendingResourceRequests int64
	ResourceType            string
	ResourceUnitsMap        map[string]int64
}

// ExtractAllResourcesFromExecutable extracts all compute resource types and unit counts from an Executable.
func ExtractAllResourcesFromExecutable(exec *essmsgpb.Executable) map[string]int64 {
	resources := make(map[string]int64)
	if exec == nil || exec.GetJobset() == nil {
		return resources
	}
	for _, j := range exec.GetJobset().GetJobs() {
		if j == nil || j.GetSpec() == nil || j.GetSpec().GetResources() == nil {
			continue
		}
		r := j.GetSpec().GetResources()
		if cpu := r.GetCpuResources(); cpu != nil {
			if cpu.GetCpuMillicores() > 0 {
				resources["CPU"] += cpu.GetCpuMillicores()
			}
		}
		if gpu := r.GetGpuResources(); gpu != nil {
			if gpu.GetCount() > 0 && gpu.GetType() != "" {
				resources[gpu.GetType()] += gpu.GetCount()
			}
		}
		if tpu := r.GetTpuResources(); tpu != nil {
			res := tpu.GetType()
			if res == "" {
				res = "TPU"
			}
			chips := parseTotalChips(tpu.GetTopology())
			if chips > 0 {
				resources[res] += chips
			}
		}
	}
	return resources
}

func parseTotalChips(topology string) int64 {
	if topology == "" {
		return 1
	}
	cleaned := strings.TrimSpace(strings.ToLower(topology))
	dims := strings.Split(cleaned, "x")
	totalChips := int64(1)
	for _, d := range dims {
		v, err := strconv.ParseInt(d, 10, 64)
		if err != nil || v <= 0 {
			return 0
		}
		totalChips *= v
	}
	return totalChips
}

// SaveWorkUnitStatusHistoryIfChanged buffers Spanner mutations for all requested resources if the WorkUnit state transitioned.
func SaveWorkUnitStatusHistoryIfChanged(ctx context.Context, xid, wid int64, eid *int64, queueName string, currentState, newState *essmsgpb.WorkUnitStatus_WorkUnitState, resourceMap map[string]int64, tx *spanner.ReadWriteTransaction) {
	if newState == nil {
		return
	}
	if currentState != nil && *currentState == *newState {
		return
	}

	if len(resourceMap) == 0 {
		return
	}

	now := time.Now()
	for resType, units := range resourceMap {
		if resType == "" || units <= 0 {
			continue
		}

		historyID, err := generateHistoryID()
		if err != nil {
			logging.Error(ctx, "Failed to generate HistoryId for work unit status history", "error", err)
			continue
		}

		mutation := spannerdata.WorkUnitStatusHistoryToInsertMutation(xid, wid, historyID, eid, queueName, currentState, newState, resType, units, now)
		tx.BufferWrite([]*spanner.Mutation{mutation})
	}
}

// Repository handles Spanner operations for status history and velocity metrics.
type Repository struct {
	client *spanner.Client
}

// NewRepository initializes a new statushistory Repository.
func NewRepository(client *spanner.Client) *Repository {
	return &Repository{client: client}
}

// FetchBatchQueueVelocity queries the WorkUnitStatusHistory table for multiple queues in a single Spanner query.
func (r *Repository) FetchBatchQueueVelocity(ctx context.Context, queueNames []string, window time.Duration) (map[string]QueueVelocityResult, error) {
	return FetchBatchQueueVelocity(ctx, r.client, queueNames, window)
}

// FetchBatchPendingQueueMetrics queries the WorkUnitStatusHistory table for pending work units across multiple queues.
func (r *Repository) FetchBatchPendingQueueMetrics(ctx context.Context, queueNames []string) (map[string]PendingQueueMetricsResult, error) {
	return FetchBatchPendingQueueMetrics(ctx, r.client, queueNames)
}

// FetchBatchQueueVelocity queries the WorkUnitStatusHistory table for multiple queues in a single Spanner query.
//
//nolint:dupl
func FetchBatchQueueVelocity(ctx context.Context, client *spanner.Client, queueNames []string, window time.Duration) (map[string]QueueVelocityResult, error) {
	results := make(map[string]QueueVelocityResult)
	if len(queueNames) == 0 {
		return results, nil
	}

	cutoff := time.Now().Add(-window)
	stmt := spanner.Statement{
		SQL: `WITH Admitted AS (
		        SELECT QueueName, BitReversedXid, WorkUnitId, ResourceType, ResourceUnits
		        FROM WorkUnitStatusHistory@{FORCE_INDEX=WorkUnitStatusHistoryByQueueAndTransitionTime}
		        WHERE QueueName IN UNNEST(@queueNames)
		          AND TransitionTime >= @cutoffTime
		          AND NewState = 1
		      ),
		      QueueTotals AS (
		        SELECT QueueName, COUNT(DISTINCT CONCAT(CAST(BitReversedXid AS STRING), '-', CAST(WorkUnitId AS STRING))) AS TotalAdmittedJobs
		        FROM Admitted
		        GROUP BY QueueName
		      )
		      SELECT a.QueueName,
		             qt.TotalAdmittedJobs AS AdmittedCount,
		             IFNULL(SUM(a.ResourceUnits), 0) AS TotalUnits,
		             a.ResourceType AS ResType
		      FROM Admitted a
		      JOIN QueueTotals qt ON a.QueueName = qt.QueueName
		      GROUP BY a.QueueName, qt.TotalAdmittedJobs, a.ResourceType`,
		Params: map[string]interface{}{
			"queueNames": queueNames,
			"cutoffTime": cutoff,
		},
	}

	if client == nil {
		return results, nil
	}

	iter := client.Single().Query(ctx, stmt)
	defer iter.Stop()

	for {
		row, err := iter.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			return nil, err
		}

		m, err := parseQueueMetricRow(row, "AdmittedCount")
		if err != nil {
			return nil, err
		}

		curr, exists := results[m.queueName]
		if !exists {
			curr = QueueVelocityResult{
				AdmittedJobsCount: m.count,
				ResourceUnits:     m.totalUnits,
				ResourceType:      m.resType,
				ResourceUnitsMap:  make(map[string]int64),
			}
		}
		if m.count > curr.AdmittedJobsCount {
			curr.AdmittedJobsCount = m.count
		}
		if m.resType != "" {
			curr.ResourceUnitsMap[m.resType] = m.totalUnits
		}
		if curr.ResourceType == "" || curr.ResourceType == "CPU" || (m.resType != "" && m.resType != "CPU" && (curr.ResourceType == "CPU" || m.resType < curr.ResourceType)) {
			curr.ResourceType = m.resType
			curr.ResourceUnits = m.totalUnits
		}
		results[m.queueName] = curr
	}

	return results, nil
}

// FetchBatchPendingQueueMetrics queries the WorkUnitStatusHistory table for pending work units across multiple queues.
//
//nolint:dupl
func FetchBatchPendingQueueMetrics(ctx context.Context, client *spanner.Client, queueNames []string) (map[string]PendingQueueMetricsResult, error) {
	results := make(map[string]PendingQueueMetricsResult)
	if len(queueNames) == 0 {
		return results, nil
	}

	stmt := spanner.Statement{
		SQL: `WITH MaxHistory AS (
		        SELECT BitReversedXid, WorkUnitId, MAX(TransitionTime) AS MaxTransitionTime
		        FROM WorkUnitStatusHistory@{FORCE_INDEX=WorkUnitStatusHistoryByQueueAndTransitionTime}
		        WHERE QueueName IN UNNEST(@queueNames)
		        GROUP BY BitReversedXid, WorkUnitId
		      ),
		      LatestPending AS (
		        SELECT h.QueueName, h.BitReversedXid, h.WorkUnitId, h.ResourceType, h.ResourceUnits
		        FROM WorkUnitStatusHistory@{FORCE_INDEX=WorkUnitStatusHistoryByQueueAndTransitionTime} h
		        JOIN MaxHistory m
		          ON h.BitReversedXid = m.BitReversedXid
		         AND h.WorkUnitId = m.WorkUnitId
		         AND h.TransitionTime = m.MaxTransitionTime
		        WHERE h.NewState IN (1, 2)
		      ),
		      QueueTotals AS (
		        SELECT QueueName, COUNT(DISTINCT CONCAT(CAST(BitReversedXid AS STRING), '-', CAST(WorkUnitId AS STRING))) AS TotalPendingJobs
		        FROM LatestPending
		        GROUP BY QueueName
		      )
		      SELECT lp.QueueName,
		             qt.TotalPendingJobs AS PendingJobs,
		             IFNULL(SUM(lp.ResourceUnits), 0) AS TotalUnits,
		             lp.ResourceType AS ResType
		      FROM LatestPending lp
		      JOIN QueueTotals qt ON lp.QueueName = qt.QueueName
		      GROUP BY lp.QueueName, qt.TotalPendingJobs, lp.ResourceType`,
		Params: map[string]interface{}{
			"queueNames": queueNames,
		},
	}

	if client == nil {
		return results, nil
	}

	iter := client.Single().Query(ctx, stmt)
	defer iter.Stop()

	for {
		row, err := iter.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			return nil, err
		}

		m, err := parseQueueMetricRow(row, "PendingJobs")
		if err != nil {
			return nil, err
		}

		curr, exists := results[m.queueName]
		if !exists {
			curr = PendingQueueMetricsResult{
				PendingJobs:             m.count,
				PendingResourceRequests: m.totalUnits,
				ResourceType:            m.resType,
				ResourceUnitsMap:        make(map[string]int64),
			}
		}
		if m.count > curr.PendingJobs {
			curr.PendingJobs = m.count
		}
		if m.resType != "" {
			curr.ResourceUnitsMap[m.resType] = m.totalUnits
		}
		if curr.ResourceType == "" || curr.ResourceType == "CPU" || (m.resType != "" && m.resType != "CPU" && (curr.ResourceType == "CPU" || m.resType < curr.ResourceType)) {
			curr.ResourceType = m.resType
			curr.PendingResourceRequests = m.totalUnits
		}
		results[m.queueName] = curr
	}

	return results, nil
}

type rawQueueMetricRow struct {
	queueName  string
	count      int64
	totalUnits int64
	resType    string
}

func parseQueueMetricRow(row *spanner.Row, countCol string) (*rawQueueMetricRow, error) {
	var r rawQueueMetricRow
	if err := row.ColumnByName("QueueName", &r.queueName); err != nil {
		return nil, err
	}
	if err := row.ColumnByName(countCol, &r.count); err != nil {
		return nil, err
	}
	if err := row.ColumnByName("TotalUnits", &r.totalUnits); err != nil {
		return nil, err
	}
	if err := row.ColumnByName("ResType", &r.resType); err != nil {
		return nil, err
	}
	return &r, nil
}

func generateHistoryID() (int64, error) {
	nBig, err := rand.Int(rand.Reader, big.NewInt(1<<63-1))
	if err != nil {
		return 0, err
	}
	return nBig.Int64(), nil
}
