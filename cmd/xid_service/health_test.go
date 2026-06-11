// Copyright 2025 Google LLC
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

	"golang/grpc/health/health"
	healthpb "grpc_proto/health_go_proto"
	"xmanager_cloud/storage/spanner/emulator/emulator"
)

func TestUpdateHealthStatus(t *testing.T) {
	ctx := context.Background()

	t.Run("SERVING when spanner ok", func(t *testing.T) {
		xidClient, _, _ := emulator.SetupWithEmulator(ctx, t)
		healthServer := health.NewServer()

		updateHealthStatus(ctx, xidClient, healthServer)

		resp, err := healthServer.Check(ctx, &healthpb.HealthCheckRequest{Service: xidServiceHealthCheckName})
		if err != nil {
			t.Fatalf("healthServer.Check(%q) failed: %v", xidServiceHealthCheckName, err)
		}
		if resp.Status != healthpb.HealthCheckResponse_SERVING {
			t.Errorf("healthServer.Check(%q).Status = %v, want %v", xidServiceHealthCheckName, resp.Status, healthpb.HealthCheckResponse_SERVING)
		}
	})

	t.Run("NOT_SERVING when spanner down", func(t *testing.T) {
		xidClient, _, _ := emulator.SetupWithEmulator(ctx, t)
		xidClient.Close()
		healthServer := health.NewServer()

		updateHealthStatus(ctx, xidClient, healthServer)

		resp, err := healthServer.Check(ctx, &healthpb.HealthCheckRequest{Service: xidServiceHealthCheckName})
		if err != nil {
			t.Fatalf("healthServer.Check(%q) failed: %v", xidServiceHealthCheckName, err)
		}
		if resp.Status != healthpb.HealthCheckResponse_NOT_SERVING {
			t.Errorf("healthServer.Check(%q).Status = %v, want %v", xidServiceHealthCheckName, resp.Status, healthpb.HealthCheckResponse_NOT_SERVING)
		}
	})
}
