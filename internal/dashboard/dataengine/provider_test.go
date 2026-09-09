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
	"sync"
	"testing"
	"time"

	dpb "xmanager_cloud/dashboard_service/proto/api_go_proto"
)

// countingConnector records how many times it ran, so tests can assert on retry behavior.
type countingConnector struct {
	mu      sync.Mutex
	calls   int
	results []func() (ClickHouseConn, error)
}

// connect returns the result scripted for this call, repeating the last one once they run out.
func (c *countingConnector) connect(ctx context.Context) (ClickHouseConn, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	result := c.results[min(c.calls, len(c.results)-1)]
	c.calls++
	return result()
}

// callCount reports how many times the connector ran.
func (c *countingConnector) callCount() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.calls
}

func succeeds() (ClickHouseConn, error) { return &mockClickHouseConn{}, nil }

func fails() (ClickHouseConn, error) { return nil, errors.New("clickhouse is down") }

func TestNewProviderPanicsWithoutConnector(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Error("NewProvider(nil) returned normally, want a panic")
		}
	}()

	NewProvider(nil)
}

func TestProviderEngineCachesTheEngine(t *testing.T) {
	connector := &countingConnector{results: []func() (ClickHouseConn, error){succeeds}}
	// Zero retry interval so a second attempt would be allowed: only caching can prevent it.
	provider := NewProvider(connector.connect, WithRetryInterval(0))

	first, err := provider.Engine(context.Background())
	if err != nil {
		t.Fatalf("Engine() returned error: %v", err)
	}
	second, err := provider.Engine(context.Background())
	if err != nil {
		t.Fatalf("Engine() returned error on second call: %v", err)
	}

	if first != second {
		t.Errorf("Engine() returned %p then %p, want the same engine", first, second)
	}
	if got := connector.callCount(); got != 1 {
		t.Errorf("connector ran %d times, want 1", got)
	}
}

func TestProviderEngineReportsConnectionFailure(t *testing.T) {
	connector := &countingConnector{results: []func() (ClickHouseConn, error){fails}}
	provider := NewProvider(connector.connect)

	engine, err := provider.Engine(context.Background())

	if err == nil {
		t.Fatalf("Engine() = %v, want an error", engine)
	}
	if engine != nil {
		t.Errorf("Engine() returned engine %p alongside an error, want nil", engine)
	}
}

func TestProviderEngineReportsEngineConstructionFailure(t *testing.T) {
	// A connector that reports success while handing back no connection must not yield an engine.
	nilConn := func() (ClickHouseConn, error) { return nil, nil }
	provider := NewProvider((&countingConnector{results: []func() (ClickHouseConn, error){nilConn}}).connect)

	if engine, err := provider.Engine(context.Background()); err == nil {
		t.Fatalf("Engine() = %v, want an error", engine)
	}
}

func TestProviderEngineRateLimitsAttempts(t *testing.T) {
	connector := &countingConnector{results: []func() (ClickHouseConn, error){fails}}
	provider := NewProvider(connector.connect, WithRetryInterval(time.Hour))

	firstErr := mustFail(t, provider)
	secondErr := mustFail(t, provider)

	if got := connector.callCount(); got != 1 {
		t.Errorf("connector ran %d times within the retry interval, want 1", got)
	}
	if firstErr != secondErr {
		t.Errorf("second Engine() reported %v, want the remembered %v", secondErr, firstErr)
	}
}

func TestProviderEngineRecoversAfterRetryInterval(t *testing.T) {
	connector := &countingConnector{results: []func() (ClickHouseConn, error){fails, succeeds}}
	// A zero interval makes the next attempt immediately due, without sleeping in the test.
	provider := NewProvider(connector.connect, WithRetryInterval(0))

	mustFail(t, provider)
	engine, err := provider.Engine(context.Background())

	if err != nil {
		t.Fatalf("Engine() returned error after ClickHouse recovered: %v", err)
	}
	if engine == nil {
		t.Error("Engine() returned no engine after ClickHouse recovered")
	}
}

func TestProviderEngineSharesAnInFlightAttempt(t *testing.T) {
	dialing, release := make(chan struct{}), make(chan struct{})
	blocked := func() (ClickHouseConn, error) {
		close(dialing)
		<-release
		return succeeds()
	}
	connector := &countingConnector{results: []func() (ClickHouseConn, error){blocked}}
	provider := NewProvider(connector.connect)

	done := make(chan struct{})
	go func() {
		defer close(done)
		provider.Engine(context.Background())
	}()
	<-dialing

	// Callers arriving mid-dial wait on the attempt already running rather than being turned
	// away, so each sees the engine that attempt produced instead of an error about a competing
	// one.
	errs := make(chan error, 8)
	var wg sync.WaitGroup
	for range cap(errs) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := provider.Engine(context.Background())
			errs <- err
		}()
	}
	close(release)
	wg.Wait()
	close(errs)
	<-done

	for err := range errs {
		if err != nil {
			t.Fatalf("Engine() during an in-flight dial = %v, want no error", err)
		}
	}
	if got := connector.callCount(); got != 1 {
		t.Errorf("connector ran %d times for one shared attempt, want 1", got)
	}
}

func TestProviderEngineStopsWaitingWhenTheCallerGivesUp(t *testing.T) {
	dialing, release := make(chan struct{}), make(chan struct{})
	blocked := func() (ClickHouseConn, error) {
		close(dialing)
		<-release
		return succeeds()
	}
	provider := NewProvider((&countingConnector{results: []func() (ClickHouseConn, error){blocked}}).connect)

	done := make(chan struct{})
	go func() {
		defer close(done)
		provider.Engine(context.Background())
	}()
	<-dialing

	// A readiness probe arriving mid-dial must come back on its own deadline rather than be held
	// open for however long ClickHouse takes to answer.
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := provider.Engine(ctx)
	close(release)
	<-done

	if !errors.Is(err, context.Canceled) {
		t.Errorf("Engine() with a cancelled context = %v, want %v", err, context.Canceled)
	}
}

func TestProviderEngineOutlivesTheCancelledCallerThatStartedIt(t *testing.T) {
	dialing, release := make(chan struct{}), make(chan struct{})
	// Sampled once the initiating caller has already given up, so a cancelled dial context here
	// would mean its cancellation had propagated into the shared attempt.
	var errDuringDial error
	connector := func(ctx context.Context) (ClickHouseConn, error) {
		close(dialing)
		<-release
		errDuringDial = ctx.Err()
		return succeeds()
	}
	provider := NewProvider(connector)

	ctx, cancel := context.WithCancel(context.Background())
	initiator := make(chan error, 1)
	go func() {
		_, err := provider.Engine(ctx)
		initiator <- err
	}()
	<-dialing
	cancel()
	if err := <-initiator; !errors.Is(err, context.Canceled) {
		t.Fatalf("the initiating Engine() = %v, want %v", err, context.Canceled)
	}

	// The attempt is shared, so the caller that happened to start it cannot be allowed to abort
	// it for everyone else still waiting.
	close(release)
	engine, err := provider.Engine(context.Background())

	if err != nil {
		t.Fatalf("Engine() after the initiator gave up = %v, want no error", err)
	}
	if engine == nil {
		t.Fatal("Engine() returned no engine after the initiator gave up")
	}
	if errDuringDial != nil {
		t.Errorf("dial context reported %v while the initiator was cancelled, want nil", errDuringDial)
	}
}

func TestProviderPingChecksTheConnection(t *testing.T) {
	pingErr := errors.New("clickhouse is unreachable")
	conn := &mockClickHouseConn{pingFn: func(context.Context) error { return pingErr }}
	provider := NewProvider(func(context.Context) (ClickHouseConn, error) { return conn, nil })

	// A cached engine is not evidence that ClickHouse still answers, which is the whole reason
	// readiness pings rather than merely asking whether an engine exists.
	if _, err := provider.Engine(context.Background()); err != nil {
		t.Fatalf("Engine() returned error: %v", err)
	}
	err := provider.Ping(context.Background())

	if !errors.Is(err, pingErr) {
		t.Errorf("Ping() = %v, want it to wrap %v", err, pingErr)
	}
}

func TestProviderPingSucceedsWhenClickHouseAnswers(t *testing.T) {
	provider := NewProvider((&countingConnector{results: []func() (ClickHouseConn, error){succeeds}}).connect)

	if err := provider.Ping(context.Background()); err != nil {
		t.Errorf("Ping() = %v, want no error", err)
	}
}

func TestProviderPingReportsAnUnbuildableEngine(t *testing.T) {
	provider := NewProvider((&countingConnector{results: []func() (ClickHouseConn, error){fails}}).connect)

	if err := provider.Ping(context.Background()); err == nil {
		t.Error("Ping() = nil, want an error when the engine cannot be built")
	}
}

func TestProviderEngineBuildsOnceUnderConcurrency(t *testing.T) {
	connector := &countingConnector{results: []func() (ClickHouseConn, error){succeeds}}
	provider := NewProvider(connector.connect, WithRetryInterval(0))

	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			provider.Engine(context.Background())
		}()
	}
	wg.Wait()

	if got := connector.callCount(); got != 1 {
		t.Errorf("connector ran %d times across concurrent callers, want 1", got)
	}
}

func TestProviderEngineServesConcurrentCallersOnceBuilt(t *testing.T) {
	provider := NewProvider((&countingConnector{results: []func() (ClickHouseConn, error){succeeds}}).connect)
	if _, err := provider.Engine(context.Background()); err != nil {
		t.Fatalf("Engine() failed to build: %v", err)
	}

	// Coordinating in the steady state would make callers wait on each other even though
	// ClickHouse is up and the engine is already cached.
	errs := make(chan error, 32)
	var wg sync.WaitGroup
	for range cap(errs) {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := provider.Engine(context.Background())
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)

	for err := range errs {
		if err != nil {
			t.Fatalf("Engine() on a built provider = %v, want no error", err)
		}
	}
}

func TestProviderFetchChartDataUsesTheEngine(t *testing.T) {
	provider := NewProvider((&countingConnector{results: []func() (ClickHouseConn, error){succeeds}}).connect)
	chart := &dpb.Chart{Name: "dashboards/1/charts/2"}

	data, err := provider.FetchChartData(context.Background(), chart, nil)

	if err != nil {
		t.Fatalf("FetchChartData() returned error: %v", err)
	}
	if data.GetName() != chart.GetName() {
		t.Errorf("FetchChartData() returned data for %q, want %q", data.GetName(), chart.GetName())
	}
}

func TestProviderFetchChartDataReportsMissingEngine(t *testing.T) {
	provider := NewProvider((&countingConnector{results: []func() (ClickHouseConn, error){fails}}).connect)

	data, err := provider.FetchChartData(context.Background(), &dpb.Chart{}, nil)

	if err == nil {
		t.Fatalf("FetchChartData() = %v, want an error", data)
	}
}

// mustFail asserts the provider cannot produce an engine and returns the reason it gave.
func mustFail(t *testing.T, provider *Provider) error {
	t.Helper()
	engine, err := provider.Engine(context.Background())
	if err == nil {
		t.Fatalf("Engine() = %v, want an error", engine)
	}
	return err
}
