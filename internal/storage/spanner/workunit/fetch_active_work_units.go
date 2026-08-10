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

package workunit

import (
	"context"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"

	essmsgpb "xmanager_cloud/experiment_state_server/proto/messages_go_proto"
	"xmanager_cloud/validator/validator"
)

// FetchActiveWorkUnitsByQueues queries Spanner for active and pending work units assigned to the given queues.
// TODO: Implement Spanner query to fetch active work units for given queues.
//
//nolint:revive // standard repository pattern returning (results, pageToken, err)
func (r *Repository) FetchActiveWorkUnitsByQueues(ctx context.Context, queueNames []string, pageSize int32, pageToken string) ([]*essmsgpb.WorkUnit, string, error) {
	return r.FetchActiveWorkUnitsByQueuesAt(ctx, queueNames, pageSize, pageToken, time.Now())
}

// FetchActiveWorkUnitsByQueuesAt queries Spanner using deterministic reference time now.
// TODO: Implement Spanner query to fetch active work units for given queues.
//
//nolint:revive // standard repository pattern returning (results, pageToken, err)
func (r *Repository) FetchActiveWorkUnitsByQueuesAt(ctx context.Context, queueNames []string, pageSize int32, pageToken string, now time.Time) ([]*essmsgpb.WorkUnit, string, error) {
	if len(queueNames) == 0 {
		return []*essmsgpb.WorkUnit{}, "", nil
	}

	queueName := queueNames[0]

	// Returns mocked WorkUnits for development and UI prototyping.
	mockedWorkUnits := []*essmsgpb.WorkUnit{
		{
			Name:       validator.ConstructWorkUnitName(100, 1),
			Priority:   essmsgpb.Priority(100),
			CreateTime: timestamppb.New(now.Add(-10 * time.Minute)),
			Notes:      "test@example.com",
			Executable: &essmsgpb.Executable{
				SchedulingConstraints: &essmsgpb.SchedulingConstraints{
					QueueName: queueName,
				},
			},
			CurrentWorkUnitExecution: &essmsgpb.WorkUnitExecution{
				Status: &essmsgpb.WorkUnitStatus{
					State: essmsgpb.WorkUnitStatus_SCHEDULING,
					SchedulingInformation: &essmsgpb.StructuredMessage{
						AuxiliaryInfo: &essmsgpb.AuxiliaryInfo{
							Detail: &essmsgpb.AuxiliaryInfo_QueueInfo{
								QueueInfo: &essmsgpb.QueueInfo{
									PositionInQueue: 1,
								},
							},
						},
					},
				},
			},
		},
		{
			Name:       validator.ConstructWorkUnitName(100, 2),
			Priority:   essmsgpb.Priority(50),
			CreateTime: timestamppb.New(now.Add(-5 * time.Minute)),
			Notes:      "dev@example.com",
			Executable: &essmsgpb.Executable{
				SchedulingConstraints: &essmsgpb.SchedulingConstraints{
					QueueName: queueName,
				},
			},
			CurrentWorkUnitExecution: &essmsgpb.WorkUnitExecution{
				Status: &essmsgpb.WorkUnitStatus{
					State: essmsgpb.WorkUnitStatus_RUNNING,
					SchedulingInformation: &essmsgpb.StructuredMessage{
						AuxiliaryInfo: &essmsgpb.AuxiliaryInfo{
							Detail: &essmsgpb.AuxiliaryInfo_QueueInfo{
								QueueInfo: &essmsgpb.QueueInfo{
									PositionInQueue: -1,
								},
							},
						},
					},
				},
			},
		},
	}

	return mockedWorkUnits, "", nil
}
