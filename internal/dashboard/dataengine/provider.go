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
	"io"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/sync/singleflight"
	dpb "xmanager_cloud/api/protos/dashboard"
	"xmanager_cloud/internal/logging"
)

// defaultRetryInterval bounds how often a Provider redials the metrics store.
//
// Readiness probes and chart requests both drive reconnection, so unbounded retries would turn a
// struggling ClickHouse into a dial flood from every dashboard service pod at once.
const defaultRetryInterval = 15 * time.Second

// defaultCloseGracePeriod bounds how long a superseded DataEngine remains open after a config swap
// so in-flight lock-free chart queries on the previous connection pool can finish cleanly.
const defaultCloseGracePeriod = time.Minute

// dialKey names the shared connection attempt. A Provider dials one destination, so every caller
// waiting for an engine waits on the same key.
const (
	dialKey    = "dataengine"
	rebuildKey = "dataengine-rebuild"
)

var errProviderClosed = errors.New("data engine provider is closed")

// Connector opens the ClickHouse connection a DataEngine runs its queries through.
type Connector func(ctx context.Context) (ClickHouseConn, error)

// Resolver resolves the active ClickHouse configuration fingerprint and a Connector bound to that configuration.
type Resolver func(ctx context.Context) (fingerprint string, connect Connector, err error)

// EngineBuilder opens and initializes a backend-agnostic DataEngine instance.
type EngineBuilder func(ctx context.Context) (*DataEngine, error)

// EngineResolver resolves the active metrics store configuration fingerprint and an EngineBuilder bound to it.
type EngineResolver func(ctx context.Context) (fingerprint string, build EngineBuilder, err error)

// Provider hands out a DataEngine, building one on first use and rebuilding it after a failure.
//
// ClickHouse comes up on its own schedule and can be restarted underneath a running pod. Building
// the engine once at startup left the process permanently without one in both cases, so callers
// ask the Provider instead and it retries, at a bounded rate, until a connection succeeds.
type Provider struct {
	buildEngine      EngineBuilder
	resolveEngine    EngineResolver
	retryInterval    time.Duration
	closeGracePeriod time.Duration

	// engine is read on every call and that read must never block. A reader lock would not do:
	// it queues behind an in-flight dial, which is precisely the wait Engine promises callers it
	// will not make them take.
	engine atomic.Pointer[DataEngine]

	// group collapses concurrent dials and dynamic rebuilds into shared attempts.
	group singleflight.Group

	// mu guards the bookkeeping the retry interval is computed from and the active config fingerprint.
	// Only the goroutine running the shared attempt touches these, so the lock is uncontended; it is
	// kept because relying on singleflight's internal ordering for that guarantee would be an easy
	// thing to break later.
	mu                 sync.Mutex
	closed             bool
	fingerprint        string
	lastErr            error
	lastAttempt        time.Time
	lastRebuildFP      string
	lastRebuildErr     error
	lastRebuildAttempt time.Time
}

// ProviderOption configures Provider settings.
type ProviderOption func(*Provider)

// WithRetryInterval overrides how long a Provider waits between connection attempts.
func WithRetryInterval(interval time.Duration) ProviderOption {
	return func(p *Provider) {
		p.retryInterval = interval
	}
}

// WithCloseGracePeriod overrides how long a superseded engine waits before closing its connection.
func WithCloseGracePeriod(gracePeriod time.Duration) ProviderOption {
	return func(p *Provider) {
		p.closeGracePeriod = gracePeriod
	}
}

// NewProvider creates a Provider that builds engines over the connections connect returns.
func NewProvider(connect Connector, opts ...ProviderOption) *Provider {
	if connect == nil {
		panic("connect must not be nil")
	}
	return NewEngineProvider(connectorEngineBuilder(connect), opts...)
}

// NewEngineProvider creates a Provider that builds engines via buildEngine on demand.
func NewEngineProvider(buildEngine EngineBuilder, opts ...ProviderOption) *Provider {
	if buildEngine == nil {
		panic("buildEngine must not be nil")
	}
	provider := &Provider{
		buildEngine:      buildEngine,
		retryInterval:    defaultRetryInterval,
		closeGracePeriod: defaultCloseGracePeriod,
	}
	for _, opt := range opts {
		opt(provider)
	}
	return provider
}

// NewDynamicProvider creates a Provider that resolves configuration on initial dial and swaps the
// cached engine on readiness Ping whenever the resolved configuration fingerprint changes.
func NewDynamicProvider(resolve Resolver, opts ...ProviderOption) *Provider {
	if resolve == nil {
		panic("resolve must not be nil")
	}
	return NewDynamicEngineProvider(resolverEngineResolver(resolve), opts...)
}

// NewDynamicEngineProvider creates a Provider that resolves backend-agnostic configuration on initial
// dial and swaps the cached engine on readiness Ping whenever the resolved fingerprint changes.
func NewDynamicEngineProvider(resolveEngine EngineResolver, opts ...ProviderOption) *Provider {
	if resolveEngine == nil {
		panic("resolveEngine must not be nil")
	}
	provider := &Provider{
		resolveEngine:    resolveEngine,
		retryInterval:    defaultRetryInterval,
		closeGracePeriod: defaultCloseGracePeriod,
	}
	for _, opt := range opts {
		opt(provider)
	}
	return provider
}

func connectorEngineBuilder(connect Connector) EngineBuilder {
	return func(ctx context.Context) (*DataEngine, error) {
		conn, err := connect(ctx)
		if err != nil {
			return nil, fmt.Errorf("failed to connect to clickhouse: %w", err)
		}
		if conn == nil {
			return nil, fmt.Errorf("failed to connect to clickhouse: connection is nil")
		}
		engine, err := New(conn)
		if err != nil {
			closeConn(conn)
			return nil, fmt.Errorf("failed to initialize data engine: %w", err)
		}
		return engine, nil
	}
}

func resolverEngineResolver(resolve Resolver) EngineResolver {
	return func(ctx context.Context) (string, EngineBuilder, error) {
		fp, connect, err := resolve(ctx)
		if err != nil {
			return "", nil, err
		}
		if connect == nil {
			return fp, nil, nil
		}
		return fp, connectorEngineBuilder(connect), nil
	}
}

// Engine returns the data engine, building it when the Provider does not hold one yet.
func (p *Provider) Engine(ctx context.Context) (*DataEngine, error) {
	// Once an engine exists there is nothing left to build, so the steady state never coordinates
	// at all -- an atomic load and nothing more.
	if engine := p.engine.Load(); engine != nil {
		return engine, nil
	}

	// Concurrent callers share one dial instead of racing it, and all of them receive its result.
	// Failing the losers outright would surface as an error on a user's chart request even when
	// the attempt it was waiting behind went on to succeed a moment later.
	shared := p.group.DoChan(dialKey, func() (any, error) { return p.build(ctx) })

	select {
	case <-ctx.Done():
		// Only this caller gives up; the shared dial runs on for whoever is still waiting.
		// Readiness probes run on a short deadline and must not be held open by a slow ClickHouse.
		return nil, ctx.Err()
	case result := <-shared:
		if result.Err != nil {
			return nil, result.Err
		}
		return result.Val.(*DataEngine), nil
	}
}

// Ping reports whether the Provider can currently serve chart data.
//
// Holding an engine is not the same as ClickHouse being reachable: the engine is built once and
// cached, so after ClickHouse goes away every subsequent Engine call still succeeds. Readiness has
// to ask the connection itself, which is what this does. It deliberately does not discard the
// cached engine on failure -- the driver's connection is a pool that redials on its own, so the
// engine stays valid and recovers without a rebuild, and tearing it down here would fail the
// queries already running on it.
func (p *Provider) Ping(ctx context.Context) error {
	if p.engine.Load() == nil {
		engine, err := p.Engine(ctx)
		if err != nil {
			return err
		}
		return engine.Ping(ctx)
	}
	if p.resolveEngine != nil {
		return p.pingDynamic(ctx)
	}
	return p.pingActive(ctx, errProviderClosed)
}

func (p *Provider) pingDynamic(ctx context.Context) error {
	fp, buildEngine, err := p.resolveEngine(ctx)
	if err != nil {
		logging.Warn(ctx, "Failed to resolve data engine configuration during readiness check, keeping active engine", "error", err)
		return p.pingActive(ctx, fmt.Errorf("failed to resolve data engine configuration: %w", err))
	}
	if buildEngine == nil {
		logging.Warn(ctx, "Resolved nil engine builder during readiness check, keeping active engine")
		return p.pingActive(ctx, fmt.Errorf("failed to resolve data engine configuration: engine builder must not be nil"))
	}
	if !p.hasFingerprint(fp) {
		if err := p.rebuildIfChanged(ctx, fp, buildEngine); err != nil {
			logging.Warn(ctx, "Failed to rebuild data engine after configuration change, keeping active engine", "error", err)
			return p.pingActive(ctx, err)
		}
	}
	return p.pingActive(ctx, errProviderClosed)
}

func (p *Provider) pingActive(ctx context.Context, candidateErr error) error {
	if current := p.engine.Load(); current != nil {
		return current.Ping(ctx)
	}
	return candidateErr
}

func (p *Provider) rebuildIfChanged(ctx context.Context, fingerprint string, buildEngine EngineBuilder) error {
	shared := p.group.DoChan(rebuildKey, func() (any, error) {
		return nil, p.rebuildOnce(ctx, fingerprint, buildEngine)
	})
	select {
	case <-ctx.Done():
		return ctx.Err()
	case result := <-shared:
		return result.Err
	}
}

func (p *Provider) rebuildOnce(ctx context.Context, fingerprint string, buildEngine EngineBuilder) error {
	due, cachedErr := p.beginRebuildAttempt(fingerprint)
	if !due {
		return cachedErr
	}
	if err := p.rebuild(ctx, fingerprint, buildEngine); err != nil {
		return p.recordRebuildAttempt(err)
	}
	return nil
}

func (p *Provider) rebuild(ctx context.Context, fingerprint string, buildEngine EngineBuilder) error {
	dialCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), p.dialTimeout())
	defer cancel()

	engine, err := buildEngine(dialCtx)
	if err != nil {
		return err
	}
	if engine == nil {
		return fmt.Errorf("engine builder returned nil engine")
	}
	if err := engine.Ping(dialCtx); err != nil {
		closeEngine(engine)
		return fmt.Errorf("failed to ping data engine: %w", err)
	}
	old, stored := p.storeEngine(engine, fingerprint)
	if !stored {
		closeEngine(engine)
		return errProviderClosed
	}
	p.scheduleCloseEngine(old)
	return nil
}

// attemptDue reports whether enough time has passed since the last attempt to make another.
//
// Callers must hold mu.
func (p *Provider) attemptDue() bool {
	return p.lastAttempt.IsZero() || time.Since(p.lastAttempt) >= p.retryInterval
}

// build dials the metrics store and caches either the resulting engine or the reason there is none.
//
// It runs as the shared singleflight attempt, so at most one instance is ever in flight.
func (p *Provider) build(ctx context.Context) (*DataEngine, error) {
	// An attempt may have completed between the caller's load and this one starting.
	if engine := p.engine.Load(); engine != nil {
		return engine, nil
	}

	if due, lastErr := p.beginAttempt(); !due {
		return nil, lastErr
	}

	// Detached from the caller's cancellation and bounded on its own: this attempt is shared, so
	// letting whichever request happened to start it cancel the dial would abort it for every
	// other waiter -- and a readiness probe's short deadline alone would then be enough to keep
	// the engine from ever being built. The bound matches the retry interval so that at most one
	// attempt is outstanding per interval.
	dialCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), p.dialTimeout())
	defer cancel()

	engine, fp, err := p.dialEngine(dialCtx)
	if err != nil {
		return nil, p.recordAttempt(err)
	}
	if engine == nil {
		return nil, p.recordAttempt(fmt.Errorf("engine builder returned nil engine"))
	}

	if _, stored := p.storeEngine(engine, fp); !stored {
		closeEngine(engine)
		return nil, errProviderClosed
	}
	return engine, nil
}

func (p *Provider) dialEngine(ctx context.Context) (*DataEngine, string, error) {
	if p.resolveEngine == nil {
		engine, err := p.buildEngine(ctx)
		return engine, "", err
	}
	fp, buildEngine, err := p.resolveEngine(ctx)
	if err != nil {
		return nil, "", fmt.Errorf("failed to resolve data engine: %w", err)
	}
	if buildEngine == nil {
		return nil, "", fmt.Errorf("resolved engine builder must not be nil")
	}
	engine, err := buildEngine(ctx)
	return engine, fp, err
}

// beginAttempt reports whether a fresh attempt is due, and if not, why the last one failed.
func (p *Provider) beginAttempt() (bool, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return false, errProviderClosed
	}
	if !p.attemptDue() {
		return false, p.lastErr
	}
	p.lastAttempt = time.Now()
	return true, nil
}

// recordAttempt remembers how an attempt ended and passes its error back for the caller to return.
func (p *Provider) recordAttempt(err error) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.lastErr = err
	return err
}

func (p *Provider) beginRebuildAttempt(fingerprint string) (bool, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed {
		return false, errProviderClosed
	}
	if p.fingerprint == fingerprint {
		return false, nil
	}
	if p.lastRebuildFP == fingerprint && !p.lastRebuildAttempt.IsZero() && time.Since(p.lastRebuildAttempt) < p.retryInterval {
		return false, p.lastRebuildErr
	}
	p.lastRebuildFP = fingerprint
	p.lastRebuildAttempt = time.Now()
	return true, nil
}

func (p *Provider) recordRebuildAttempt(err error) error {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.lastRebuildErr = err
	return err
}

func (p *Provider) hasFingerprint(fingerprint string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.fingerprint == fingerprint
}

func (p *Provider) storeEngine(engine *DataEngine, fingerprint string) (*DataEngine, bool) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.closed && engine != nil {
		return nil, false
	}
	p.fingerprint = fingerprint
	p.lastErr = nil
	p.lastRebuildFP = ""
	p.lastRebuildErr = nil
	p.lastRebuildAttempt = time.Time{}
	return p.engine.Swap(engine), true
}

func (p *Provider) scheduleCloseEngine(engine *DataEngine) {
	if engine == nil {
		return
	}
	if p.closeGracePeriod <= 0 {
		closeEngine(engine)
		return
	}
	time.AfterFunc(p.closeGracePeriod, func() {
		closeEngine(engine)
	})
}

func closeConn(conn ClickHouseConn) {
	if closer, ok := conn.(io.Closer); ok {
		_ = closer.Close()
	}
}

func closeEngine(engine *DataEngine) {
	if engine != nil {
		_ = engine.Close()
	}
}

// Close releases any cached DataEngine held by the Provider.
func (p *Provider) Close() error {
	if p == nil {
		return nil
	}
	p.mu.Lock()
	p.closed = true
	p.fingerprint = ""
	p.lastErr = nil
	p.lastRebuildFP = ""
	p.lastRebuildErr = nil
	p.lastRebuildAttempt = time.Time{}
	old := p.engine.Swap(nil)
	p.mu.Unlock()
	if old == nil {
		return nil
	}
	return old.Close()
}

// dialTimeout bounds a single connection attempt.
//
// WithRetryInterval(0) is how tests ask for "retry immediately", which is not a request for a
// zero-length dial, so the default stands in for it.
func (p *Provider) dialTimeout() time.Duration {
	if p.retryInterval <= 0 {
		return defaultRetryInterval
	}
	return p.retryInterval
}

// FetchChartData serves the request from the current engine, building one first when needed.
//
// Implementing the same contract as DataEngine lets the dashboard server hold the Provider
// directly, so a request arriving after ClickHouse recovers is served rather than rejected.
func (p *Provider) FetchChartData(ctx context.Context, chart *dpb.Chart, req *dpb.GetChartDataRequest) (*dpb.ChartData, error) {
	engine, err := p.Engine(ctx)
	if err != nil {
		return nil, err
	}
	return engine.FetchChartData(ctx, chart, req)
}
