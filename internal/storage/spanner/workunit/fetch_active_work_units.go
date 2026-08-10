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

// ActiveWorkUnitsResult encapsulates the result of querying active work units.
type ActiveWorkUnitsResult struct {
	WorkUnits     []*essmsgpb.WorkUnit
	NextPageToken string
}

// FetchActiveWorkUnitsByQueues queries Spanner for active and pending work units assigned to the given queues.
// TODO: Implement Spanner query to fetch active work units for given queues.
func (r *Repository) FetchActiveWorkUnitsByQueues(ctx context.Context, queueNames []string, pageSize int32, pageToken string) (*ActiveWorkUnitsResult, error) {
	return r.FetchActiveWorkUnitsByQueuesAt(ctx, queueNames, pageSize, pageToken, time.Now())
}

// FetchActiveWorkUnitsByQueuesAt queries Spanner using deterministic reference time now.
// TODO: Implement Spanner query to fetch active work units for given queues.
func (r *Repository) FetchActiveWorkUnitsByQueuesAt(ctx context.Context, queueNames []string, pageSize int32, pageToken string, now time.Time) (*ActiveWorkUnitsResult, error) {
	if len(queueNames) == 0 {
		return &ActiveWorkUnitsResult{
			WorkUnits: []*essmsgpb.WorkUnit{},
		}, nil
	}

	queueName := queueNames[0]

	// Returns mocked WorkUnits for development and UI prototyping.
	mockedWorkUnits := []*essmsgpb.WorkUnit{
		{
			Name:       validator.ConstructWorkUnitName(100, 1),
			Priority:   essmsgpb.Priority(100),
			CreateTime: timestamppb.New(now.Add(-10 * time.Minute)),
			Notes:      "test@example.com",
			Executable: buildMockExecutable(queueName, 8000, 34359738368, "NVIDIA-H100-80GB", 4, "", ""),
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
			Executable: buildMockExecutable(queueName, 16000, 68719476736, "", 0, "v4-16", "2x2x4"),
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

	return &ActiveWorkUnitsResult{
		WorkUnits: mockedWorkUnits,
	}, nil
}

func buildMockExecutable(queueName string, cpuMillicores, memoryBytes int64, gpuType string, gpuCount int64, tpuType, tpuTopology string) *essmsgpb.Executable {
	jobs := []*essmsgpb.KubernetesJob{
		{
			Replicas: 1,
			Spec: &essmsgpb.ExecutableSpec{
				Resources: &essmsgpb.ResourceSet{
					Resources: &essmsgpb.ResourceSet_CpuResources{
						CpuResources: &essmsgpb.CpuResources{
							CpuMillicores: cpuMillicores,
							MemoryBytes:   memoryBytes,
						},
					},
				},
			},
		},
	}
	if gpuType != "" || gpuCount > 0 {
		jobs = append(jobs, &essmsgpb.KubernetesJob{
			Replicas: 1,
			Spec: &essmsgpb.ExecutableSpec{
				Resources: &essmsgpb.ResourceSet{
					Resources: &essmsgpb.ResourceSet_GpuResources{
						GpuResources: &essmsgpb.GpuResources{
							Type:  gpuType,
							Count: gpuCount,
						},
					},
				},
			},
		})
	}
	if tpuType != "" || tpuTopology != "" {
		jobs = append(jobs, &essmsgpb.KubernetesJob{
			Replicas: 1,
			Spec: &essmsgpb.ExecutableSpec{
				Resources: &essmsgpb.ResourceSet{
					Resources: &essmsgpb.ResourceSet_TpuResources{
						TpuResources: &essmsgpb.TpuResources{
							Type:     tpuType,
							Topology: tpuTopology,
						},
					},
				},
			},
		})
	}
	return &essmsgpb.Executable{
		SchedulingConstraints: &essmsgpb.SchedulingConstraints{
			QueueName: queueName,
		},
		Type: &essmsgpb.Executable_Jobset{
			Jobset: &essmsgpb.JobSetExecutable{
				Jobs: jobs,
			},
		},
	}
}
