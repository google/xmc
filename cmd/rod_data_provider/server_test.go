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

	spb "xmanager_cloud/api/protos/rod"
)

func TestGetFittingQueues(t *testing.T) {
	cfg := &Config{
		Port:     "8080",
		GRPCPort: "50051",
	}
	srv := newServer(cfg)

	tests := []struct {
		name      string
		req       *spb.GetFittingQueuesRequest
		wantCount int32
	}{
		{
			name: "Returns Default Mock Fitting Queues",
			req: &spb.GetFittingQueuesRequest{
				Requirements: []*spb.Resource{
					{ResourceType: "cpu", Count: 2},
				},
			},
			wantCount: 2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp, err := srv.GetFittingQueues(context.Background(), tt.req)
			if err != nil {
				t.Fatalf("GetFittingQueues failed: %v", err)
			}
			if len(resp.Queues) != int(tt.wantCount) {
				t.Errorf("Queues len = %d, want %d", len(resp.Queues), tt.wantCount)
			}
		})
	}
}
