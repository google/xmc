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
}

// ExtractAllResourcesFromExecutable extracts all compute resource types and unit counts from an Executable.
func ExtractAllResourcesFromExecutable(exec *essmsgpb.Executable) map[string]int64 {
	resources := make(map[string]int64)
	if exec == nil || exec.GetJobset() == nil {
		resources["CPU"] = 1
		return resources
	}
	for _, j := range exec.GetJobset().GetJobs() {
		if j == nil || j.GetSpec() == nil || j.GetSpec().GetResources() == nil {
			continue
		}
		r := j.GetSpec().GetResources()
		if cpu := r.GetCpuResources(); cpu != nil {
			cpus := cpu.GetCpuMillicores() / 1000
			if cpus == 0 {
				cpus = 1
			}
			resources["CPU"] += cpus
		}
		if gpu := r.GetGpuResources(); gpu != nil && gpu.GetCount() > 0 {
			res := gpu.GetType()
			if res == "" {
				res = "GPU H100"
			}
			resources[res] += gpu.GetCount()
		}
		if r.GetTpuResources() != nil {
			resources["TPU"] += 1
		}
	}
	if len(resources) == 0 {
		resources["CPU"] = 1
	}
	return resources
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
		resourceMap = map[string]int64{"CPU": 1}
	}

	now := time.Now()
	for resType, units := range resourceMap {
		if resType == "" {
			resType = "CPU"
		}
		if units <= 0 {
			units = 1
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

// FetchQueueVelocity queries the WorkUnitStatusHistory table in Spanner for transitions in the sliding window.
func (r *Repository) FetchQueueVelocity(ctx context.Context, queueName string, window time.Duration) (QueueVelocityResult, error) {
	return FetchQueueVelocity(ctx, r.client, queueName, window)
}

// FetchBatchQueueVelocity queries the WorkUnitStatusHistory table for multiple queues in a single Spanner query.
func (r *Repository) FetchBatchQueueVelocity(ctx context.Context, queueNames []string, window time.Duration) (map[string]QueueVelocityResult, error) {
	return FetchBatchQueueVelocity(ctx, r.client, queueNames, window)
}

// FetchQueueVelocity queries the WorkUnitStatusHistory table in Spanner for transitions to SCHEDULING/STARTING/RUNNING in the sliding window.
func FetchQueueVelocity(ctx context.Context, client *spanner.Client, queueName string, window time.Duration) (QueueVelocityResult, error) {
	cutoff := time.Now().Add(-window)
	stmt := spanner.Statement{
		SQL: `SELECT COUNT(1) AS AdmittedCount, IFNULL(SUM(ResourceUnits), 0) AS TotalUnits, IFNULL(ANY_VALUE(ResourceType), "CPU") AS ResType
		      FROM WorkUnitStatusHistory@{FORCE_INDEX=WorkUnitStatusHistoryByQueueAndTransitionTime}
		      WHERE QueueName = @queueName
		        AND TransitionTime >= @cutoffTime
		        AND NewState IN (1, 2, 3, 4)`, // 1: INITIALIZING, 2: SCHEDULING, 3: STARTING, 4: RUNNING
		Params: map[string]interface{}{
			"queueName":  queueName,
			"cutoffTime": cutoff,
		},
	}

	var res QueueVelocityResult
	if client == nil {
		return res, nil
	}

	iter := client.Single().Query(ctx, stmt)
	defer iter.Stop()

	row, err := iter.Next()
	if err != nil {
		if err == iterator.Done {
			return res, nil
		}
		return res, err
	}

	var admittedCount, totalUnits int64
	var resType string
	if err := row.ColumnByName("AdmittedCount", &admittedCount); err != nil {
		return res, err
	}
	if err := row.ColumnByName("TotalUnits", &totalUnits); err != nil {
		return res, err
	}
	if err := row.ColumnByName("ResType", &resType); err != nil {
		return res, err
	}

	res.AdmittedJobsCount = admittedCount
	res.ResourceUnits = totalUnits
	res.ResourceType = resType
	return res, nil
}

// FetchBatchQueueVelocity queries the WorkUnitStatusHistory table for multiple queues in a single Spanner query.
func FetchBatchQueueVelocity(ctx context.Context, client *spanner.Client, queueNames []string, window time.Duration) (map[string]QueueVelocityResult, error) {
	results := make(map[string]QueueVelocityResult)
	if len(queueNames) == 0 {
		return results, nil
	}

	cutoff := time.Now().Add(-window)
	stmt := spanner.Statement{
		SQL: `SELECT QueueName, COUNT(1) AS AdmittedCount, IFNULL(SUM(ResourceUnits), 0) AS TotalUnits, IFNULL(ANY_VALUE(ResourceType), "CPU") AS ResType
		      FROM WorkUnitStatusHistory@{FORCE_INDEX=WorkUnitStatusHistoryByQueueAndTransitionTime}
		      WHERE QueueName IN UNNEST(@queueNames)
		        AND TransitionTime >= @cutoffTime
		        AND NewState IN (1, 2, 3, 4)
		      GROUP BY QueueName`,
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

		var qName, resType string
		var admittedCount, totalUnits int64
		if err := row.ColumnByName("QueueName", &qName); err != nil {
			return nil, err
		}
		if err := row.ColumnByName("AdmittedCount", &admittedCount); err != nil {
			return nil, err
		}
		if err := row.ColumnByName("TotalUnits", &totalUnits); err != nil {
			return nil, err
		}
		if err := row.ColumnByName("ResType", &resType); err != nil {
			return nil, err
		}

		results[qName] = QueueVelocityResult{
			AdmittedJobsCount: admittedCount,
			ResourceUnits:     totalUnits,
			ResourceType:      resType,
		}
	}

	return results, nil
}

func generateHistoryID() (int64, error) {
	nBig, err := rand.Int(rand.Reader, big.NewInt(1<<63-1))
	if err != nil {
		return 0, err
	}
	return nBig.Int64(), nil
}
