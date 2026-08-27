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

package dataengine

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	dpb "xmanager_cloud/dashboard_service/proto/api_go_proto"
	spb "xmanager_cloud/dashboard_service/proto/service_go_proto"
)

// defaultRetryInterval bounds how often a Provider redials ClickHouse.
//
// Readiness probes and chart requests both drive reconnection, so unbounded retries would turn a
// struggling ClickHouse into a dial flood from every dashboard service pod at once.
const defaultRetryInterval = 15 * time.Second

// errAttemptInProgress reports that another caller already holds the connection attempt.
var errAttemptInProgress = errors.New("a clickhouse connection attempt is already in progress")

// Connector opens the ClickHouse connection a DataEngine runs its queries through.
type Connector func(ctx context.Context) (ClickHouseConn, error)

// Provider hands out a DataEngine, building one on first use and rebuilding it after a failure.
//
// ClickHouse comes up on its own schedule and can be restarted underneath a running pod. Building
// the engine once at startup left the process permanently without one in both cases, so callers
// ask the Provider instead and it retries, at a bounded rate, until a connection succeeds.
type Provider struct {
	connect       Connector
	retryInterval time.Duration

	mu          sync.RWMutex
	engine      *DataEngine
	lastErr     error
	lastAttempt time.Time
}

// ProviderOption configures Provider settings.
type ProviderOption func(*Provider)

// WithRetryInterval overrides how long a Provider waits between connection attempts.
func WithRetryInterval(interval time.Duration) ProviderOption {
	return func(p *Provider) {
		p.retryInterval = interval
	}
}

// NewProvider creates a Provider that builds engines over the connections connect returns.
func NewProvider(connect Connector, opts ...ProviderOption) *Provider {
	if connect == nil {
		panic("connect must not be nil")
	}
	provider := &Provider{
		connect:       connect,
		retryInterval: defaultRetryInterval,
	}
	for _, opt := range opts {
		opt(provider)
	}
	return provider
}

// Engine returns the data engine, building it when the Provider does not hold one yet.
func (p *Provider) Engine(ctx context.Context) (*DataEngine, error) {
	// Once an engine exists there is nothing left to build, so the steady state is served under a
	// read lock. Taking the dial lock here instead would make two concurrent requests fail each
	// other with errAttemptInProgress long after ClickHouse came up.
	if engine := p.cachedEngine(); engine != nil {
		return engine, nil
	}

	if !p.mu.TryLock() {
		// Another caller is dialing. Readiness probes and chart requests are both frequent, and
		// queueing them behind that dial would spread one slow ClickHouse across all of them.
		return nil, errAttemptInProgress
	}
	defer p.mu.Unlock()

	if p.engine != nil {
		return p.engine, nil
	}
	if !p.attemptDue() {
		return nil, p.lastErr
	}
	return p.build(ctx)
}

// cachedEngine returns the engine the Provider already holds, or nil when it has yet to build one.
func (p *Provider) cachedEngine() *DataEngine {
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.engine
}

// attemptDue reports whether enough time has passed since the last attempt to make another.
func (p *Provider) attemptDue() bool {
	return p.lastAttempt.IsZero() || time.Since(p.lastAttempt) >= p.retryInterval
}

// build dials ClickHouse and caches either the resulting engine or the reason there is none.
func (p *Provider) build(ctx context.Context) (*DataEngine, error) {
	p.lastAttempt = time.Now()

	conn, err := p.connect(ctx)
	if err != nil {
		p.lastErr = fmt.Errorf("failed to connect to clickhouse: %w", err)
		return nil, p.lastErr
	}
	engine, err := New(conn)
	if err != nil {
		p.lastErr = fmt.Errorf("failed to initialize data engine: %w", err)
		return nil, p.lastErr
	}

	p.engine, p.lastErr = engine, nil
	return engine, nil
}

// FetchChartData serves the request from the current engine, building one first when needed.
//
// Implementing the same contract as DataEngine lets the dashboard server hold the Provider
// directly, so a request arriving after ClickHouse recovers is served rather than rejected.
func (p *Provider) FetchChartData(ctx context.Context, chart *dpb.Chart, req *spb.GetChartDataRequest) (*dpb.ChartData, error) {
	engine, err := p.Engine(ctx)
	if err != nil {
		return nil, err
	}
	return engine.FetchChartData(ctx, chart, req)
}
