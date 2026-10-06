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

package jobset

import (
	"context"
	"time"

	"golang/prometheus/client/prometheus/prometheus"
	"xmanager_cloud/kubernetes/reconciler/hypercomputeclient/hypercomputeclient"
	"xmanager_cloud/kubernetes/reconciler/shared/shared"
)

// mlRunLatencyBuckets covers the latency range of a remote control-plane RPC
// (tens of milliseconds to several seconds), so the histogram resolves both the
// fast happy path and slow/timing-out calls without wasting buckets.
var mlRunLatencyBuckets = []float64{0.01, 0.025, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10}

var (
	// mlRunRequestsTotal and mlRunRequestDuration are the RED (Rate/Errors/
	// Duration) signals for the MLRun control plane, split by operation so a
	// regression can be attributed to a specific call.
	mlRunRequestsTotal = shared.MetricsFactory.NewCounterVec(prometheus.CounterOpts{
		Name: "jobset_reconciler_mlrun_requests_total",
		Help: "Total MLRun control-plane calls made by the reconciler, by operation and result.",
	}, []string{"operation", "result"})

	mlRunRequestDuration = shared.MetricsFactory.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "jobset_reconciler_mlrun_request_duration_seconds",
		Help:    "Latency of MLRun control-plane calls in seconds, by operation.",
		Buckets: mlRunLatencyBuckets,
	}, []string{"operation"})
)

// MLRun operation label values. Kept as constants so the decorator and any test
// reference identical strings (a typo would silently create a new time series).
const (
	mlRunOpCreate = "create"
	mlRunOpGet    = "get"
	mlRunOpUpdate = "update"
)

// MLRun result label values.
const (
	mlRunResultSuccess       = "success"
	mlRunResultError         = "error"
	mlRunResultAlreadyExists = "already_exists"
)

// instrumentedMLRunCreator decorates a MLRunCreator with RED metrics. It is a
// pure pass-through: it changes no business behavior, it only observes each call.
// Instrumentation lives here (rather than inside ensureMLRunCreated /
// upsertExistingMLRun) so the reconciler's control flow stays untouched and the
// measurement logic is testable in isolation.
type instrumentedMLRunCreator struct {
	inner MLRunCreator
}

// NewInstrumentedMLRunCreator wraps c so every control-plane call records
// traffic, errors, and latency. It returns nil when c is nil so the reconciler's
// existing r.MLRunClient == nil guard (which disables MLRun creation) is
// preserved rather than defeated by a non-nil interface wrapping a nil client.
func NewInstrumentedMLRunCreator(c MLRunCreator) MLRunCreator {
	if c == nil {
		return nil
	}
	return &instrumentedMLRunCreator{inner: c}
}

// observeMLRun records the latency and outcome of a single MLRun call.
func observeMLRun(operation string, start time.Time, err error) {
	mlRunRequestDuration.WithLabelValues(operation).Observe(time.Since(start).Seconds())
	mlRunRequestsTotal.WithLabelValues(operation, mlRunResultLabel(err)).Inc()
}

// mlRunResultLabel classifies an MLRun call outcome for the result label.
// AlreadyExists is deliberately kept distinct from error: it is the expected,
// idempotent outcome of the create-conflict/upsert path, so folding it into
// "error" would inflate the observed error rate.
func mlRunResultLabel(err error) string {
	switch {
	case err == nil:
		return mlRunResultSuccess
	case isMLRunAlreadyExistsError(err):
		return mlRunResultAlreadyExists
	default:
		return mlRunResultError
	}
}

func (m *instrumentedMLRunCreator) CreateMLRun(ctx context.Context, name, displayName, runPhase string, opts *hypercomputeclient.CreateMLRunOptions) (map[string]any, error) {
	start := time.Now()
	res, err := m.inner.CreateMLRun(ctx, name, displayName, runPhase, opts)
	observeMLRun(mlRunOpCreate, start, err)
	return res, err
}

func (m *instrumentedMLRunCreator) GetMLRun(ctx context.Context, name string) (map[string]any, error) {
	start := time.Now()
	res, err := m.inner.GetMLRun(ctx, name)
	observeMLRun(mlRunOpGet, start, err)
	return res, err
}

func (m *instrumentedMLRunCreator) UpdateMLRun(ctx context.Context, name string, opts *hypercomputeclient.UpdateMLRunOptions) (map[string]any, error) {
	start := time.Now()
	res, err := m.inner.UpdateMLRun(ctx, name, opts)
	observeMLRun(mlRunOpUpdate, start, err)
	return res, err
}

// Compile-time assertion that the decorator satisfies the interface it wraps.
var _ MLRunCreator = (*instrumentedMLRunCreator)(nil)
