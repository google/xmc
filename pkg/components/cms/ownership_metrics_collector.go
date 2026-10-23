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

package cms

import (
	"context"
	"time"

	"xmanager_cloud/internal/logging"
	"xmanager_cloud/pkg/components/store"
	"xmanager_cloud/pkg/components/store/ownership"
)

// OwnershipMetricsInterval is how often RunOwnershipMetricsCollector refreshes the ownership gauges.
const OwnershipMetricsInterval = 30 * time.Second

// CollectOwnershipMetrics reads the stored ownership state at now and sets the ownership gauges.
// On error the gauges keep their previous values.
func CollectOwnershipMetrics(ctx context.Context, reader store.OwnershipGaugeReader, now time.Time) error {
	g, err := reader.ReadOwnershipGauges(ctx, now)
	if err != nil {
		return err
	}
	ownership.RecordOwnershipGauges(g)
	return nil
}

// RunOwnershipMetricsCollector refreshes the ownership gauges from reader immediately and then every
// interval until ctx is done. Each read is bounded by interval, so a hung read logs a warning
// instead of parking the loop. The gauges are computed from the store rather than from this
// replica's requests, so they keep reporting after the active cluster disappears; each replica
// refreshes on its own schedule, so replicas can differ for up to one interval.
func RunOwnershipMetricsCollector(ctx context.Context, reader store.OwnershipGaugeReader, interval time.Duration) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		if err := collectWithTimeout(ctx, reader, interval); err != nil && ctx.Err() == nil {
			logging.Warn(ctx, "Failed to refresh CMS ownership gauges; keeping the previous values", "error", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// collectWithTimeout runs CollectOwnershipMetrics at the current time with a read deadline of timeout.
func collectWithTimeout(ctx context.Context, reader store.OwnershipGaugeReader, timeout time.Duration) error {
	readCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	return CollectOwnershipMetrics(readCtx, reader, time.Now().UTC())
}
