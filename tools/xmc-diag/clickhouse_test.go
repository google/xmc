// Copyright 2026 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//      http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package main

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestCheckClickHouse_Healthy(t *testing.T) {
	ctx := context.Background()
	mock := &MockClickHouseQuerier{
		version: "24.3.1",
		latency: 12 * time.Millisecond,
	}

	rep := CheckClickHouse(ctx, mock, false)
	if rep.Status != StatusHealthy {
		t.Errorf("Status = %q, want %q", rep.Status, StatusHealthy)
	}
	if !rep.Connected {
		t.Errorf("Connected = false, want true")
	}
	if rep.Version != "24.3.1" {
		t.Errorf("Version = %q, want 24.3.1", rep.Version)
	}
	if len(rep.MissingTables) > 0 {
		t.Errorf("MissingTables = %v, want empty", rep.MissingTables)
	}
}

func TestCheckClickHouse_MissingTables_DegradedReason(t *testing.T) {
	ctx := context.Background()
	mock := &MockClickHouseQuerier{
		version:       "24.3.1",
		missingTables: []string{"xmc_metrics_mv"},
		latency:       10 * time.Millisecond,
	}

	rep := CheckClickHouse(ctx, mock, false)
	if rep.Status != StatusDegraded {
		t.Errorf("Status = %q, want %q", rep.Status, StatusDegraded)
	}
	if len(rep.MissingTables) != 1 || rep.MissingTables[0] != "xmc_metrics_mv" {
		t.Errorf("MissingTables = %v, want [xmc_metrics_mv]", rep.MissingTables)
	}
	if !strings.Contains(rep.Reason, "missing required schema tables: xmc_metrics_mv") {
		t.Errorf("Reason = %q, expected missing schema tables explanation", rep.Reason)
	}
}

func TestCheckClickHouse_PingFailed(t *testing.T) {
	ctx := context.Background()
	mock := &MockClickHouseQuerier{
		pingErr: errors.New("connection refused"),
	}

	rep := CheckClickHouse(ctx, mock, false)
	if rep.Status != StatusFailed {
		t.Errorf("Status = %q, want %q", rep.Status, StatusFailed)
	}
	if rep.Connected {
		t.Errorf("Connected = true, want false")
	}
	if !strings.Contains(rep.Reason, "Failed to ping ClickHouse endpoint") {
		t.Errorf("Reason = %q, expected ping failure explanation", rep.Reason)
	}
}
