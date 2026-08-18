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
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	spb "xmanager_cloud/api/protos/rod"
)

func TestBatchGetQueueSecondaryData_MockHandler(t *testing.T) {
	ctx := context.Background()
	srv := &server{}

	req := &spb.BatchGetQueueSecondaryDataRequest{
		QueueNames: []string{"cq-gpu", "cq-cpu"},
		Range:      spb.RangePreset_RANGE_PRESET_LAST_HOUR,
	}

	resp, err := srv.BatchGetQueueSecondaryData(ctx, req)
	if err != nil {
		t.Fatalf("BatchGetQueueSecondaryData failed: %v", err)
	}

	gpuData, ok := resp.QueuePredictability["cq-gpu"]
	if !ok {
		t.Fatalf("expected result for cq-gpu in QueuePredictability map")
	}

	p := gpuData.QueuePredictability[0]
	if p.JobVelocity != 12.5 {
		t.Errorf("JobVelocity = %f, want 12.5", p.JobVelocity)
	}
	if p.ResourceVelocity.ResourceType != "GPU H100" {
		t.Errorf("ResourceVelocity.ResourceType = %q, want GPU H100", p.ResourceVelocity.ResourceType)
	}
	if p.PendingJobs != 3 {
		t.Errorf("PendingJobs = %d, want 3", p.PendingJobs)
	}

	cpuData, ok := resp.QueuePredictability["cq-cpu"]
	if !ok {
		t.Fatalf("expected result for cq-cpu in QueuePredictability map")
	}

	pCpu := cpuData.QueuePredictability[0]
	if pCpu.ResourceVelocity.ResourceType != "CPU" {
		t.Errorf("ResourceVelocity.ResourceType = %q, want CPU", pCpu.ResourceVelocity.ResourceType)
	}
}

func TestBatchGetQueueSecondaryData_NilRequest(t *testing.T) {
	srv := &server{}
	_, err := srv.BatchGetQueueSecondaryData(context.Background(), nil)
	if err == nil {
		t.Fatal("expected error for nil request")
	}
	st, _ := status.FromError(err)
	if st.Code() != codes.InvalidArgument {
		t.Errorf("expected InvalidArgument code, got: %v", st.Code())
	}
}
