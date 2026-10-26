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
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
	dpb "xmanager_cloud/api/protos/dashboard"
	"xmanager_cloud/internal/logging"
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

type closableMockConn struct {
	mockClickHouseConn
	closed atomic.Bool
}

func (c *closableMockConn) Close() error {
	c.closed.Store(true)
	return nil
}

func newSummaryConn(metricName string, lastVal float64) *closableMockConn {
	sampleTime := time.Date(2026, 8, 11, 10, 0, 0, 0, time.UTC)
	conn := &closableMockConn{}
	conn.queryFn = func(ctx context.Context, query string, args ...any) (driver.Rows, error) {
		if conn.closed.Load() {
			return nil, errors.New("clickhouse connection is closed")
		}
		return &mockRows{data: [][]any{
			{metricName, lastVal, lastVal, lastVal, lastVal, int64(1), sampleTime},
		}}, nil
	}
	return conn
}

func summaryTestChart() *dpb.Chart {
	return &dpb.Chart{
		Name: "experiments/1/workUnits/1/charts/summary",
		Xid:  1,
		Wid:  1,
		Plots: []*dpb.PlotSpec{
			{
				PlotId: "kpi",
				Type:   dpb.PlotType_SUMMARY_BOX,
				Data:   &dpb.DataSpec{Metrics: []string{"loss"}},
			},
		},
	}
}

func TestNewDynamicProviderPanicsWithoutResolver(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Error("NewDynamicProvider(nil) returned normally, want a panic")
		}
	}()

	NewDynamicProvider(nil)
}

func TestDynamicProviderUnchangedFingerprintReusesEngine(t *testing.T) {
	var resolveCalls, dialCalls atomic.Int32
	conn := newSummaryConn("loss", 0.5)
	provider := NewDynamicProvider(func(ctx context.Context) (string, Connector, error) {
		resolveCalls.Add(1)
		return "fp-v1", func(ctx context.Context) (ClickHouseConn, error) {
			dialCalls.Add(1)
			return conn, nil
		}, nil
	}, WithRetryInterval(0))

	if err := provider.Ping(context.Background()); err != nil {
		t.Fatalf("initial Ping() error: %v", err)
	}
	firstEngine, err := provider.Engine(context.Background())
	if err != nil {
		t.Fatalf("Engine() error: %v", err)
	}

	// Chart queries must never call the resolver once an engine is cached.
	if _, err := provider.FetchChartData(context.Background(), summaryTestChart(), nil); err != nil {
		t.Fatalf("FetchChartData() error: %v", err)
	}
	if got := resolveCalls.Load(); got != 1 {
		t.Errorf("resolveCalls after FetchChartData = %d, want 1 (zero K8s resolution on query path)", got)
	}

	// Subsequent Ping with unchanged fingerprint re-checks fingerprint without re-dialing.
	if err := provider.Ping(context.Background()); err != nil {
		t.Fatalf("second Ping() error: %v", err)
	}
	secondEngine, _ := provider.Engine(context.Background())
	if firstEngine != secondEngine {
		t.Errorf("Engine() changed on unchanged fingerprint: %p != %p", firstEngine, secondEngine)
	}
	if got := dialCalls.Load(); got != 1 {
		t.Errorf("dialCalls = %d, want 1", got)
	}
	if conn.closed.Load() {
		t.Error("active connection was closed on unchanged fingerprint")
	}
}

func TestDynamicProviderChangedFingerprintSwapsEngineAndClosesOldConn(t *testing.T) {
	for _, gracePeriod := range []time.Duration{0, 15 * time.Millisecond} {
		t.Run(fmt.Sprintf("grace_%s", gracePeriod), func(t *testing.T) {
			connV1 := newSummaryConn("loss", 0.5)
			connV2 := newSummaryConn("loss", 0.1)

			var mu sync.Mutex
			currentFP := "fp-v1"
			currentConn := connV1
			var dialCount int

			provider := NewDynamicProvider(func(ctx context.Context) (string, Connector, error) {
				mu.Lock()
				fp, c := currentFP, currentConn
				mu.Unlock()
				return fp, func(ctx context.Context) (ClickHouseConn, error) {
					mu.Lock()
					dialCount++
					mu.Unlock()
					return c, nil
				}, nil
			}, WithRetryInterval(0), WithCloseGracePeriod(gracePeriod))

			if err := provider.Ping(context.Background()); err != nil {
				t.Fatalf("initial Ping() error: %v", err)
			}
			dataV1, err := provider.FetchChartData(context.Background(), summaryTestChart(), nil)
			if err != nil {
				t.Fatalf("FetchChartData() v1 error: %v", err)
			}
			if got := dataV1.GetPlotsData()[0].GetSummaryData().GetLatestValue(); got != 0.5 {
				t.Fatalf("v1 summary LatestValue = %v, want 0.5", got)
			}

			// Update configuration to v2. Before Ping runs, chart queries continue using the cached v1 engine.
			mu.Lock()
			currentFP = "fp-v2"
			currentConn = connV2
			mu.Unlock()

			prePingData, err := provider.FetchChartData(context.Background(), summaryTestChart(), nil)
			if err != nil {
				t.Fatalf("FetchChartData() before Ping error: %v", err)
			}
			if got := prePingData.GetPlotsData()[0].GetSummaryData().GetLatestValue(); got != 0.5 {
				t.Fatalf("pre-Ping summary LatestValue = %v, want 0.5 from cached engine", got)
			}

			// Ping detects fingerprint change, dials v2, swaps engine, and schedules closing v1 connection.
			if err := provider.Ping(context.Background()); err != nil {
				t.Fatalf("Ping() after fingerprint change error: %v", err)
			}
			deadline := time.Now().Add(200 * time.Millisecond)
			for !connV1.closed.Load() && time.Now().Before(deadline) {
				time.Sleep(2 * time.Millisecond)
			}
			if !connV1.closed.Load() {
				t.Error("connV1.closed = false, want true after engine swap and grace period")
			}
			if connV2.closed.Load() {
				t.Error("connV2.closed = true, want false for active connection")
			}

			dataV2, err := provider.FetchChartData(context.Background(), summaryTestChart(), nil)
			if err != nil {
				t.Fatalf("FetchChartData() v2 error: %v", err)
			}
			if got := dataV2.GetPlotsData()[0].GetSummaryData().GetLatestValue(); got != 0.1 {
				t.Errorf("v2 summary LatestValue = %v, want 0.1", got)
			}
		})
	}
}

func TestDynamicProviderPreservesExistingEngineOnRebuildFailure(t *testing.T) {
	unhealthyNewConn := newSummaryConn("loss", 0.9)
	unhealthyNewConn.pingFn = func(context.Context) error { return errors.New("new clickhouse ping failed") }

	tests := []struct {
		name           string
		nextResolve    Resolver
		checkAfterPing func(t *testing.T)
	}{
		{
			name: "resolver error on Ping",
			nextResolve: func(ctx context.Context) (string, Connector, error) {
				return "", nil, errors.New("k8s api timeout")
			},
		},
		{
			name: "nil connector returned by resolver on Ping",
			nextResolve: func(ctx context.Context) (string, Connector, error) {
				return "fp-v2", nil, nil
			},
		},
		{
			name: "dial failure on fingerprint change",
			nextResolve: func(ctx context.Context) (string, Connector, error) {
				return "fp-v2", func(context.Context) (ClickHouseConn, error) { return fails() }, nil
			},
		},
		{
			name: "nil connection on fingerprint change",
			nextResolve: func(ctx context.Context) (string, Connector, error) {
				return "fp-v2", func(context.Context) (ClickHouseConn, error) { return nil, nil }, nil
			},
		},
		{
			name: "ping failure on newly dialed connection",
			nextResolve: func(ctx context.Context) (string, Connector, error) {
				return "fp-v2", func(context.Context) (ClickHouseConn, error) { return unhealthyNewConn, nil }, nil
			},
			checkAfterPing: func(t *testing.T) {
				if !unhealthyNewConn.closed.Load() {
					t.Error("unhealthyNewConn.closed = false, want failed replacement connection closed")
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			connV1 := newSummaryConn("loss", 0.5)
			var mu sync.Mutex
			activeResolve := Resolver(func(ctx context.Context) (string, Connector, error) {
				return "fp-v1", func(context.Context) (ClickHouseConn, error) { return connV1, nil }, nil
			})
			provider := NewDynamicProvider(func(ctx context.Context) (string, Connector, error) {
				mu.Lock()
				r := activeResolve
				mu.Unlock()
				return r(ctx)
			}, WithRetryInterval(0))

			if err := provider.Ping(context.Background()); err != nil {
				t.Fatalf("initial Ping() error: %v", err)
			}

			mu.Lock()
			activeResolve = tt.nextResolve
			mu.Unlock()

			// While connV1 remains healthy, Ping() falls back to pinging connV1 so the pod stays ready.
			if err := provider.Ping(context.Background()); err != nil {
				t.Fatalf("Ping() returned error %v when active v1 engine is still healthy, want nil", err)
			}
			if connV1.closed.Load() {
				t.Error("connV1.closed = true, want existing engine connection kept open when rebuild fails")
			}
			if tt.checkAfterPing != nil {
				tt.checkAfterPing(t)
			}

			data, err := provider.FetchChartData(context.Background(), summaryTestChart(), nil)
			if err != nil {
				t.Fatalf("FetchChartData() failed after unsuccessful rebuild: %v", err)
			}
			if got := data.GetPlotsData()[0].GetSummaryData().GetLatestValue(); got != 0.5 {
				t.Errorf("summary LatestValue = %v, want 0.5 from preserved v1 engine", got)
			}

			// If the active v1 engine also becomes unreachable, Ping() surfaces the active engine's ping error.
			activeErr := errors.New("active v1 clickhouse went down")
			connV1.pingFn = func(context.Context) error { return activeErr }
			if err := provider.Ping(context.Background()); !errors.Is(err, activeErr) {
				t.Errorf("Ping() with unhealthy active engine = %v, want %v", err, activeErr)
			}
		})
	}
}

func TestDynamicProviderRebuildDetachesFromCallerCancellation(t *testing.T) {
	connV1 := newSummaryConn("loss", 0.5)
	connV2 := newSummaryConn("loss", 0.2)

	var (
		mu          sync.Mutex
		currentFP   = "fp-v1"
		dialStarted = make(chan struct{})
		releaseDial = make(chan struct{})
		dialErrCh   = make(chan error, 1)
	)

	provider := NewDynamicProvider(func(ctx context.Context) (string, Connector, error) {
		mu.Lock()
		fp := currentFP
		mu.Unlock()
		if fp == "fp-v1" {
			return fp, func(context.Context) (ClickHouseConn, error) { return connV1, nil }, nil
		}
		return fp, func(dialCtx context.Context) (ClickHouseConn, error) {
			close(dialStarted)
			<-releaseDial
			dialErrCh <- dialCtx.Err()
			return connV2, nil
		}, nil
	}, WithRetryInterval(0), WithCloseGracePeriod(0))

	if err := provider.Ping(context.Background()); err != nil {
		t.Fatalf("initial Ping() error: %v", err)
	}

	mu.Lock()
	currentFP = "fp-v2"
	mu.Unlock()

	probeCtx, cancelProbe := context.WithCancel(context.Background())
	pingDone := make(chan error, 1)
	go func() {
		pingDone <- provider.Ping(probeCtx)
	}()

	<-dialStarted
	cancelProbe()

	// Ping(probeCtx) must abort promptly upon caller cancellation without blocking on releaseDial.
	select {
	case <-pingDone:
	case <-time.After(time.Second):
		t.Fatal("Ping(probeCtx) remained blocked on slow rebuild after caller cancellation")
	}

	close(releaseDial)
	if errDuringDial := <-dialErrCh; errDuringDial != nil {
		t.Errorf("rebuild dial context err = %v after probe cancellation, want nil", errDuringDial)
	}
	deadline := time.Now().Add(time.Second)
	for {
		data, err := provider.FetchChartData(context.Background(), summaryTestChart(), nil)
		if err != nil {
			t.Fatalf("FetchChartData() after rebuild error: %v", err)
		}
		if data.GetPlotsData()[0].GetSummaryData().GetLatestValue() == 0.2 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("summary LatestValue after rebuild = %v, want 0.2 from connV2", data.GetPlotsData()[0].GetSummaryData().GetLatestValue())
		}
		time.Sleep(time.Millisecond)
	}
}

func TestDynamicProviderInitialDialFailures(t *testing.T) {
	tests := []struct {
		name    string
		resolve Resolver
	}{
		{
			name: "resolver returns error on initial build",
			resolve: func(ctx context.Context) (string, Connector, error) {
				return "", nil, errors.New("initial resolution failed")
			},
		},
		{
			name: "resolver returns nil connector on initial build",
			resolve: func(ctx context.Context) (string, Connector, error) {
				return "fp-v1", nil, nil
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			provider := NewDynamicProvider(tt.resolve, WithRetryInterval(0))
			if err := provider.Ping(context.Background()); err == nil {
				t.Fatal("Ping() = nil, want error on initial build failure")
			}
		})
	}
}

func TestDynamicProviderConcurrentPingAndFetchChartData(t *testing.T) {
	var step atomic.Int64
	provider := NewDynamicProvider(func(ctx context.Context) (string, Connector, error) {
		n := step.Add(1)
		fp := fmt.Sprintf("fp-%d", n%4)
		return fp, func(ctx context.Context) (ClickHouseConn, error) {
			if n%5 == 0 {
				return nil, errors.New("transient dial failure")
			}
			return newSummaryConn("loss", float64(n)), nil
		}, nil
	}, WithRetryInterval(0))

	// Seed initial healthy engine before concurrent stress.
	step.Store(1)
	if err := provider.Ping(context.Background()); err != nil {
		t.Fatalf("initial Ping() error: %v", err)
	}

	var wg sync.WaitGroup
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 25 {
				_ = provider.Ping(context.Background())
			}
		}()
	}
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 50 {
				if _, err := provider.FetchChartData(context.Background(), summaryTestChart(), nil); err != nil {
					t.Errorf("concurrent FetchChartData() error: %v", err)
					return
				}
			}
		}()
	}
	wg.Wait()
}

func TestEngineProviderAndCloseLifecycle(t *testing.T) {
	t.Parallel()
	for _, fn := range []func(){
		func() { NewEngineProvider(nil) },
		func() { NewDynamicEngineProvider(nil) },
	} {
		func() {
			defer func() {
				if recover() == nil {
					t.Error("expected panic on nil builder/resolver")
				}
			}()
			fn()
		}()
	}

	var nilProvider *Provider
	if err := nilProvider.Close(); err != nil {
		t.Errorf("nilProvider.Close() = %v, want nil", err)
	}
	nilBuildProvider := NewEngineProvider(func(context.Context) (*DataEngine, error) { return nil, nil })
	if _, err := nilBuildProvider.Engine(context.Background()); err == nil {
		t.Error("Engine() with nil-returning EngineBuilder succeeded, want error")
	}
	if err := nilBuildProvider.Close(); err != nil {
		t.Errorf("unbuilt Provider.Close() = %v, want nil", err)
	}

	conn := newSummaryConn("loss", 0.4)
	var logBuf bytes.Buffer
	logCtx := logging.WithLogger(context.Background(), slog.New(slog.NewTextHandler(&logBuf, nil)))
	step := atomic.Int32{}
	dyn := NewDynamicEngineProvider(func(context.Context) (string, EngineBuilder, error) {
		switch step.Add(1) {
		case 1:
			return "fp-1", func(context.Context) (*DataEngine, error) { return New(conn) }, nil
		case 2:
			return "", nil, errors.New("resolver unreachable")
		case 3:
			return "fp-2", nil, nil
		default:
			return "fp-3", func(context.Context) (*DataEngine, error) { return nil, nil }, nil
		}
	}, WithRetryInterval(0))
	for range 4 {
		if err := dyn.Ping(logCtx); err != nil {
			t.Fatalf("dyn.Ping() error: %v", err)
		}
	}
	logs := logBuf.String()
	for _, wantWarn := range []string{
		"Failed to resolve data engine configuration during readiness check, keeping active engine",
		"Resolved nil engine builder during readiness check, keeping active engine",
		"Failed to rebuild data engine after configuration change, keeping active engine",
	} {
		if !strings.Contains(logs, wantWarn) {
			t.Errorf("Ping() warning logs = %q, missing substring %q", logs, wantWarn)
		}
	}
	if err := dyn.Close(); err != nil || !conn.closed.Load() {
		t.Errorf("dyn.Close() err=%v, closed=%v; want nil, true", err, conn.closed.Load())
	}
}

func TestDynamicProviderRebuildRateLimitsAndDeduplicates(t *testing.T) {
	t.Parallel()
	connV1 := newSummaryConn("loss", 0.5)
	var (
		fp        atomic.Value
		dialCount atomic.Int32
		gate      = make(chan struct{})
	)
	fp.Store("fp-v1")

	provider := NewDynamicProvider(func(context.Context) (string, Connector, error) {
		cur := fp.Load().(string)
		if cur == "fp-v1" {
			return cur, func(context.Context) (ClickHouseConn, error) { return connV1, nil }, nil
		}
		return cur, func(context.Context) (ClickHouseConn, error) {
			dialCount.Add(1)
			<-gate
			return nil, errors.New("unreachable v2")
		}, nil
	}, WithRetryInterval(time.Hour))

	if err := provider.Ping(context.Background()); err != nil {
		t.Fatalf("initial Ping() error: %v", err)
	}
	fp.Store("fp-v2")

	var wg sync.WaitGroup
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = provider.Ping(context.Background())
		}()
	}
	for dialCount.Load() == 0 {
		time.Sleep(time.Millisecond)
	}
	close(gate)
	wg.Wait()

	// Subsequent Ping calls within retryInterval (1h) must be rate-limited without dialing again.
	for range 3 {
		if err := provider.Ping(context.Background()); err != nil {
			t.Fatalf("rate-limited Ping() with healthy v1 engine error: %v", err)
		}
	}
	if got := dialCount.Load(); got != 1 {
		t.Errorf("rebuild dialCount across concurrent + cooldown Pings = %d, want 1", got)
	}
}
