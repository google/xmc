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

	"github.com/google/go-cmp/cmp"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/testing/protocmp"
	spb "xmanager_cloud/api/protos/rod"
)

func TestBatchGetQueueSecondaryData_MockHandler(t *testing.T) {
	ctx := context.Background()
	srv := &server{}

	req := &spb.BatchGetQueueSecondaryDataRequest{
		QueueNames: []string{"cq-gpu", "cq-cpu"},
		Range:      spb.RangePreset_RANGE_PRESET_LAST_HOUR,
	}

	want := &spb.BatchGetQueueSecondaryDataResponse{
		QueuePredictability: map[string]*spb.ClusterQueueSecondaryMetrics{
			"cq-gpu": {
				QueuePredictability: []*spb.QueuePredictability{
					{
						VelocityRange: spb.RangePreset_RANGE_PRESET_LAST_HOUR,
						JobVelocity:   12.5,
						ResourceVelocity: &spb.ResourceVelocity{
							ResourceType: "GPU H100",
							Velocity:     25.0,
						},
						PendingJobs: 3,
						PendingResourceRequests: &spb.Resource{
							ResourceType: "GPU H100",
							Count:        6,
						},
					},
				},
			},
			"cq-cpu": {
				QueuePredictability: []*spb.QueuePredictability{
					{
						VelocityRange: spb.RangePreset_RANGE_PRESET_LAST_HOUR,
						JobVelocity:   12.5,
						ResourceVelocity: &spb.ResourceVelocity{
							ResourceType: "CPU",
							Velocity:     25.0,
						},
						PendingJobs: 3,
						PendingResourceRequests: &spb.Resource{
							ResourceType: "CPU",
							Count:        6,
						},
					},
				},
			},
		},
	}

	got, err := srv.BatchGetQueueSecondaryData(ctx, req)
	if err != nil {
		t.Fatalf("BatchGetQueueSecondaryData failed: %v", err)
	}

	if diff := cmp.Diff(want, got, protocmp.Transform()); diff != "" {
		t.Errorf("BatchGetQueueSecondaryData mismatch (-want +got):\n%s", diff)
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
