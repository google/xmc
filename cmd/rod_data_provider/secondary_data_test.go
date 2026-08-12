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
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	spb "xmanager_cloud/api/protos/rod"
	"xmanager_cloud/internal/storage/spanner/statushistory"
)

type mockVelocityFetcher struct {
	result statushistory.QueueVelocityResult
	err    error
}

var _ QueueVelocityFetcher = (*mockVelocityFetcher)(nil)

func (m *mockVelocityFetcher) FetchQueueVelocity(ctx context.Context, queueName string, window time.Duration) (statushistory.QueueVelocityResult, error) {
	return m.result, m.err
}

func (m *mockVelocityFetcher) FetchBatchQueueVelocity(ctx context.Context, queueNames []string, window time.Duration) (map[string]statushistory.QueueVelocityResult, error) {
	res := make(map[string]statushistory.QueueVelocityResult)
	for _, qName := range queueNames {
		res[qName] = m.result
	}
	return res, m.err
}

func TestBatchGetQueueSecondaryData(t *testing.T) {
	ctx := context.Background()
	mockFetcher := &mockVelocityFetcher{
		result: statushistory.QueueVelocityResult{
			AdmittedJobsCount: 12,
			ResourceUnits:     96,
			ResourceType:      "nvidia-tesla-h100",
		},
	}

	srv := &server{
		velocityStore: mockFetcher,
	}

	req := &spb.BatchGetQueueSecondaryDataRequest{
		QueueNames: []string{"cq-gpu"},
		Range:      spb.RangePreset_LAST_HOUR,
	}

	resp, err := srv.BatchGetQueueSecondaryData(ctx, req)
	if err != nil {
		t.Fatalf("BatchGetQueueSecondaryData failed: %v", err)
	}

	queueData, ok := resp.QueuePredictability["cq-gpu"]
	if !ok {
		t.Fatalf("expected result for cq-gpu in QueuePredictability map")
	}

	if len(queueData.QueuePredictability) != 1 {
		t.Fatalf("expected 1 predictability entry, got %d", len(queueData.QueuePredictability))
	}

	p := queueData.QueuePredictability[0]
	if p.JobVelocity != 12.0 {
		t.Errorf("JobVelocity = %f, want 12.0", p.JobVelocity)
	}
	if p.ResourceVelocity.Velocity != 1152.0 { // 12 * 96
		t.Errorf("ResourceVelocity.Velocity = %f, want 1152.0", p.ResourceVelocity.Velocity)
	}
	if p.ResourceVelocity.ResourceType != "nvidia-tesla-h100" {
		t.Errorf("ResourceVelocity.ResourceType = %q, want 'nvidia-tesla-h100'", p.ResourceVelocity.ResourceType)
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
		t.Errorf("expected InvalidArgument, got %v", st.Code())
	}
}

func TestRangePresetToDuration(t *testing.T) {
	tests := []struct {
		preset spb.RangePreset
		want   time.Duration
	}{
		{spb.RangePreset_LAST_HOUR, 1 * time.Hour},
		{spb.RangePreset_LAST_DAY, 24 * time.Hour},
		{spb.RangePreset_LAST_WEEK, 168 * time.Hour},
		{spb.RangePreset_RANGE_PRESET_UNSPECIFIED, 1 * time.Hour},
	}

	for _, tt := range tests {
		t.Run(tt.preset.String(), func(t *testing.T) {
			got := rangePresetToDuration(tt.preset)
			if got != tt.want {
				t.Errorf("rangePresetToDuration(%v) = %v, want %v", tt.preset, got, tt.want)
			}
		})
	}
}
