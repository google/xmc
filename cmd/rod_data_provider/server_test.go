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
	"xmanager_cloud/cmd/rod_data_provider/cache"
)

func TestGetFittingQueues(t *testing.T) {
	ctx := context.Background()
	c := cache.NewKueueCache()

	cfg := &Config{
		Port:     "8080",
		GRPCPort: "50051",
	}
	srv := newServer(cfg, c)

	req := &spb.GetFittingQueuesRequest{}
	resp, err := srv.GetFittingQueues(ctx, req)
	if err != nil {
		t.Fatalf("GetFittingQueues failed: %v", err)
	}

	if len(resp.Queues) == 0 {
		t.Errorf("expected non-empty queues in response")
	}
}
