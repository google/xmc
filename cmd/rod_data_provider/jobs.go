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

package main

import (
	"context"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	spb "xmanager_cloud/api/protos/rod"
	essmsgpb "xmanager_cloud/experiment_state_server/proto/messages_go_proto"
	"xmanager_cloud/internal/storage/spanner/workunit"
	"xmanager_cloud/logging/logging"
	"xmanager_cloud/validator/validator"
)

// GetRunningAndPendingJobs handles fetching active and pending jobs for a cluster queue.
func (s *server) GetRunningAndPendingJobs(ctx context.Context, req *spb.GetRunningAndPendingJobsRequest) (*spb.GetRunningAndPendingJobsResponse, error) {
	if err := validateJobsRequest(ctx, req, s.workUnitStore); err != nil {
		return nil, err
	}

	// TODO: Fetch all LocalQueues connected to a given ClusterQueue
	targetQueues := []string{"gpu-queue"}

	result, err := s.workUnitStore.FetchActiveWorkUnitsByQueues(ctx, targetQueues, req.GetPageSize(), req.GetPageToken())
	if err != nil {
		logging.Error(ctx, "failed to fetch active work units from storage", "cluster_queue_name", req.GetQueueName(), "target_queues", targetQueues, "error", err)
		return nil, status.Error(codes.Internal, "failed to fetch active jobs")
	}

	workUnits, nextPageToken := extractWorkUnitsResult(result)
	jobs := mapWorkUnitsToDashboardJobs(ctx, workUnits, time.Now())

	// TODO: Set TotalQueueSize to the total count across all pages once pagination is implemented.
	return &spb.GetRunningAndPendingJobsResponse{
		Jobs:           jobs,
		NextPageToken:  nextPageToken,
		TotalQueueSize: int64(len(workUnits)),
	}, nil
}

func validateJobsRequest(ctx context.Context, req *spb.GetRunningAndPendingJobsRequest, store WorkUnitFetcher) error {
	if err := ctx.Err(); err != nil {
		return status.FromContextError(err).Err()
	}
	if req == nil || req.GetQueueName() == "" {
		return status.Error(codes.InvalidArgument, "queue_name is required")
	}
	if req.GetPageSize() < 0 {
		return status.Error(codes.InvalidArgument, "page_size cannot be negative")
	}
	if store == nil {
		return status.Error(codes.Unavailable, "work unit store is uninitialized")
	}
	return nil
}

func extractWorkUnitsResult(result *workunit.ActiveWorkUnitsResult) ([]*essmsgpb.WorkUnit, string) {
	if result == nil {
		return nil, ""
	}
	return result.WorkUnits, result.NextPageToken
}

func mapWorkUnitsToDashboardJobs(ctx context.Context, workUnits []*essmsgpb.WorkUnit, now time.Time) []*spb.DashboardJob {
	var jobs []*spb.DashboardJob
	for _, wu := range workUnits {
		if job := mapWorkUnitToDashboardJob(ctx, wu, now); job != nil {
			jobs = append(jobs, job)
		}
	}
	return jobs
}

// mapWorkUnitToDashboardJob transforms an essmsgpb.WorkUnit domain entity into a spb.DashboardJob presentation DTO.
func mapWorkUnitToDashboardJob(ctx context.Context, wu *essmsgpb.WorkUnit, now time.Time) *spb.DashboardJob {
	if wu == nil {
		return nil
	}
	xid, wid, err := validator.ValidateWorkUnitName(wu.GetName())
	if err != nil {
		logging.Warn(ctx, "invalid work unit name", "name", wu.GetName(), "error", err)
		return nil
	}
	return &spb.DashboardJob{
		Xid:                xid,
		WorkUnitId:         wid,
		User:               wu.GetNotes(),
		Priority:           int64(wu.GetPriority()),
		Status:             extractWorkUnitState(wu),
		TimeInQueueSeconds: calculateQueueDuration(wu, now),
		RequestedResources: extractRequestedResources(wu),
		PositionInQueue:    extractQueuePosition(wu),
		CreateTime:         wu.GetCreateTime(),
		Tags:               wu.GetTags(),
	}
}

func extractWorkUnitState(wu *essmsgpb.WorkUnit) essmsgpb.WorkUnitStatus_WorkUnitState {
	if exec := wu.GetCurrentWorkUnitExecution(); exec != nil && exec.GetStatus() != nil {
		return exec.GetStatus().GetState()
	}
	return essmsgpb.WorkUnitStatus_WORK_UNIT_STATE_UNSPECIFIED
}

func extractQueuePosition(wu *essmsgpb.WorkUnit) int64 {
	exec := wu.GetCurrentWorkUnitExecution()
	if exec == nil || exec.GetStatus() == nil || exec.GetStatus().GetSchedulingInformation() == nil {
		return -1
	}
	aux := exec.GetStatus().GetSchedulingInformation().GetAuxiliaryInfo()
	if aux == nil || aux.GetQueueInfo() == nil {
		return -1
	}
	return int64(aux.GetQueueInfo().GetPositionInQueue())
}

func calculateQueueDuration(wu *essmsgpb.WorkUnit, now time.Time) int64 {
	createTime := wu.GetCreateTime()
	if createTime == nil || !createTime.IsValid() {
		return 0
	}
	diff := int64(now.Sub(createTime.AsTime()).Seconds())
	if diff > 0 {
		return diff
	}
	return 0
}

type resourceAccumulator struct {
	totals map[string]float64
	order  []string
}

func newResourceAccumulator() *resourceAccumulator {
	return &resourceAccumulator{
		totals: make(map[string]float64),
	}
}

func (a *resourceAccumulator) add(resType string, count float64) {
	if a == nil || count <= 0 || resType == "" {
		return
	}
	if _, exists := a.totals[resType]; !exists {
		a.order = append(a.order, resType)
	}
	a.totals[resType] += count
}

func (a *resourceAccumulator) addCpu(cpu *essmsgpb.CpuResources) {
	if cpu == nil {
		return
	}
	if cpu.GetCpuMillicores() > 0 {
		a.add("cpu", float64(cpu.GetCpuMillicores())/1000.0)
	}
	if cpu.GetMemoryBytes() > 0 {
		a.add("memory", float64(cpu.GetMemoryBytes()))
	}
}

func (a *resourceAccumulator) addGpu(gpu *essmsgpb.GpuResources) {
	if gpu == nil {
		return
	}
	resType := gpu.GetType()
	if resType == "" {
		resType = "GPU"
	}
	a.add(resType, float64(gpu.GetCount()))
}

func (a *resourceAccumulator) addTpu(tpu *essmsgpb.TpuResources) {
	if tpu == nil {
		return
	}
	resType := tpu.GetType()
	if resType == "" {
		resType = "TPU"
	}
	a.add(resType, 1)
}

func (a *resourceAccumulator) toResources() []*spb.Resource {
	if a == nil || len(a.order) == 0 {
		return nil
	}
	resources := make([]*spb.Resource, 0, len(a.order))
	for _, resType := range a.order {
		resources = append(resources, &spb.Resource{
			ResourceType: resType,
			Count:        a.totals[resType],
		})
	}
	return resources
}

func extractRequestedResources(wu *essmsgpb.WorkUnit) []*spb.Resource {
	if wu == nil || wu.GetExecutable() == nil || wu.GetExecutable().GetJobset() == nil {
		return nil
	}
	acc := newResourceAccumulator()
	for _, job := range wu.GetExecutable().GetJobset().GetJobs() {
		accumulateJobResources(acc, job)
	}
	return acc.toResources()
}

func accumulateJobResources(acc *resourceAccumulator, job *essmsgpb.KubernetesJob) {
	if acc == nil || job == nil || job.GetSpec() == nil || job.GetSpec().GetResources() == nil {
		return
	}
	switch r := job.GetSpec().GetResources().GetResources().(type) {
	case *essmsgpb.ResourceSet_CpuResources:
		acc.addCpu(r.CpuResources)
	case *essmsgpb.ResourceSet_GpuResources:
		acc.addGpu(r.GpuResources)
	case *essmsgpb.ResourceSet_TpuResources:
		acc.addTpu(r.TpuResources)
	}
}
