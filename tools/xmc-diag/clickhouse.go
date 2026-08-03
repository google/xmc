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
	"fmt"
	"strings"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
)

// RealClickHouseQuerier wraps driver.Conn.
type RealClickHouseQuerier struct {
	conn driver.Conn
}

func (r *RealClickHouseQuerier) Ping(ctx context.Context) error {
	return r.conn.Ping(ctx)
}

func (r *RealClickHouseQuerier) SelectVersion(ctx context.Context) (string, error) {
	var ver string
	err := r.conn.QueryRow(ctx, "SELECT version()").Scan(&ver)
	return ver, err
}

func (r *RealClickHouseQuerier) CheckTablesExist(ctx context.Context, tables []string) ([]string, error) {
	var missing []string
	for _, tbl := range tables {
		var count uint64
		err := r.conn.QueryRow(ctx, "SELECT count() FROM system.tables WHERE database = currentDatabase() AND name = ?", tbl).Scan(&count)
		if err != nil {
			return nil, fmt.Errorf("failed checking table %q: %w", tbl, err)
		}
		if count == 0 {
			missing = append(missing, tbl)
		}
	}
	return missing, nil
}

func (r *RealClickHouseQuerier) MeasureLatency(ctx context.Context) (time.Duration, error) {
	start := time.Now()
	var dummy int
	err := r.conn.QueryRow(ctx, "SELECT 1").Scan(&dummy)
	return time.Since(start), err
}

// CheckClickHouse performs ClickHouse diagnostics using querier.
func CheckClickHouse(ctx context.Context, querier ClickHouseQuerier, skipSmoke bool) ClickHouseReport {
	if querier == nil {
		return ClickHouseReport{
			Status: StatusFailed,
			Reason: "ClickHouse client uninitialized or connection disabled",
			Error:  "clickhouse querier not initialized",
		}
	}

	if err := querier.Ping(ctx); err != nil {
		return ClickHouseReport{
			Status: StatusFailed,
			Reason: "Failed to ping ClickHouse endpoint",
			Error:  fmt.Sprintf("ping failed: %v", err),
		}
	}

	ver, err := querier.SelectVersion(ctx)
	if err != nil {
		return ClickHouseReport{
			Status:    StatusFailed,
			Connected: true,
			Reason:    "Failed to execute version query",
			Error:     fmt.Sprintf("version check failed: %v", err),
		}
	}

	requiredTables := []string{"xmc_metrics", "xmc_metrics_queue", "xmc_metrics_mv"}
	missing, err := querier.CheckTablesExist(ctx, requiredTables)
	if err != nil {
		return ClickHouseReport{
			Status:    StatusFailed,
			Connected: true,
			Version:   ver,
			Reason:    "Failed querying system.tables schema",
			Error:     fmt.Sprintf("schema check failed: %v", err),
		}
	}

	var statusReasons []string
	status := StatusHealthy
	if len(missing) > 0 {
		status = StatusDegraded
		statusReasons = append(statusReasons, fmt.Sprintf("missing required schema tables: %s", strings.Join(missing, ", ")))
	}

	var latencyStr string
	if !skipSmoke {
		lat, err := querier.MeasureLatency(ctx)
		if err != nil {
			status = StatusDegraded
			statusReasons = append(statusReasons, fmt.Sprintf("query smoke test (SELECT 1) failed: %v", err))
		} else {
			latencyStr = lat.String()
		}
	}

	var reason string
	if len(statusReasons) > 0 {
		reason = strings.Join(statusReasons, "; ")
	}

	return ClickHouseReport{
		Status:        status,
		Connected:     true,
		Version:       ver,
		MissingTables: missing,
		QueryLatency:  latencyStr,
		Reason:        reason,
	}
}
