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

package worker

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/redis/go-redis/v9"
	"xmanager_cloud/internal/kubernetes/common/constants"
	"xmanager_cloud/internal/logging"
	"xmanager_cloud/internal/redislock"
	"xmanager_cloud/internal/task_queue_redis/redisclient"
)

type fakeLease struct {
	mu                        sync.Mutex
	extendFn                  func(ctx context.Context, call int) error
	releaseFn                 func(ctx context.Context) error
	extendCalls, releaseCalls int
	extendAfterRelease        bool
	releaseCtxErr             error
	releaseHasDeadline        bool
}

func (l *fakeLease) Extend(ctx context.Context) error {
	l.mu.Lock()
	l.extendCalls++
	call, fn := l.extendCalls, l.extendFn
	l.extendAfterRelease = l.extendAfterRelease || l.releaseCalls > 0
	l.mu.Unlock()
	if fn != nil {
		return fn(ctx, call)
	}
	return nil
}

func (l *fakeLease) Release(ctx context.Context) error {
	l.mu.Lock()
	l.releaseCalls++
	l.releaseCtxErr = ctx.Err()
	_, l.releaseHasDeadline = ctx.Deadline()
	fn := l.releaseFn
	l.mu.Unlock()
	if fn != nil {
		return fn(ctx)
	}
	return nil
}

type fakeLocker struct {
	mu      sync.Mutex
	lease   Lease
	err     error
	lastKey string
	calls   int
}

func (f *fakeLocker) Acquire(_ context.Context, key string) (Lease, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	f.lastKey = key
	if f.err != nil {
		return nil, f.err
	}
	return f.lease, nil
}

type stubLockClient struct {
	redisclient.Client
	redis.Scripter
	setErr error
}

func (s *stubLockClient) SetArgs(_ context.Context, _ string, _ any, _ redis.SetArgs) *redis.StatusCmd {
	if s.setErr != nil {
		return redis.NewStatusResult("", s.setErr)
	}
	return redis.NewStatusResult("OK", nil)
}

func (s *stubLockClient) EvalSha(_ context.Context, _ string, _ []string, _ ...any) *redis.Cmd {
	cmd := redis.NewCmd(context.Background())
	cmd.SetVal(int64(1))
	return cmd
}

func snapshotOutcomes() map[string]float64 {
	outcomes := []string{outcomeReconciled, outcomeContended, outcomeRetryError, outcomeInvalidPayload}
	snap := make(map[string]float64, len(outcomes))
	for _, o := range outcomes {
		snap[o] = testutil.ToFloat64(reconcileTicksTotal.WithLabelValues(o))
	}
	return snap
}

func diffOutcomes(before map[string]float64) map[string]float64 {
	deltas := make(map[string]float64)
	for k, v := range snapshotOutcomes() {
		if delta := v - before[k]; delta != 0 {
			deltas[k] = delta
		}
	}
	return deltas
}

func newLogContext(buf *bytes.Buffer) context.Context {
	return logging.WithLogger(context.Background(), slog.New(slog.NewTextHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
}

func TestNewHandler_ValidationAndRedisWiring(t *testing.T) {
	valid := &fakeLocker{lease: &fakeLease{}}
	tests := []struct {
		name    string
		params  HandlerParams
		wantErr bool
	}{
		{name: "nil locker", params: HandlerParams{}, wantErr: true},
		{name: "negative renew", params: HandlerParams{Locker: valid, RenewInterval: -time.Second}, wantErr: true},
		{name: "negative reconcile timeout", params: HandlerParams{Locker: valid, ReconcileTimeout: -time.Second}, wantErr: true},
		{name: "negative release timeout", params: HandlerParams{Locker: valid, ReleaseTimeout: -time.Second}, wantErr: true},
		{name: "reconcile timeout at process timeout", params: HandlerParams{Locker: valid, ReconcileTimeout: constants.RedisProcessTimeout}, wantErr: true},
		{name: "valid defaults", params: HandlerParams{Locker: valid}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := NewHandler(tc.params)
			if (err != nil) != tc.wantErr || (!tc.wantErr && got == nil) {
				t.Fatalf("NewHandler(%+v) = (%v, %v), wantErr %v", tc.params, got, err, tc.wantErr)
			}
		})
	}

	var nonLockConsumer redisclient.ConsumerClient
	typedNilConsumer := NewRedisConsumer("127.0.0.1:6379")
	_ = typedNilConsumer.Close(context.Background())
	typedNilConsumer.Client = (*redis.Client)(nil)
	for _, bad := range []*redisclient.ConsumerClient{nil, &nonLockConsumer, typedNilConsumer} {
		if _, err := NewRedisHandler(bad); err == nil {
			t.Fatalf("NewRedisHandler(%+v) error = nil, want error", bad)
		}
	}

	okConsumer := NewRedisConsumer("127.0.0.1:6379")
	_ = okConsumer.Close(context.Background())
	okConsumer.Client = &stubLockClient{}
	h, err := NewRedisHandler(okConsumer)
	if err != nil || !h.HandleTask(context.Background(), "w", `{"experiment":"experiments/55"}`).WasRunSuccessful {
		t.Fatalf("NewRedisHandler(ok) err = %v or HandleTask failed", err)
	}
	okConsumer.Client = &stubLockClient{setErr: redis.Nil}
	hContended, err := NewRedisHandler(okConsumer)
	if err != nil || !hContended.HandleTask(context.Background(), "w", `{"experiment":"experiments/55"}`).WasRunSuccessful {
		t.Fatalf("NewRedisHandler(contended) err = %v or HandleTask failed", err)
	}
}

func TestHandleTask_OutcomesAndLockAcquisition(t *testing.T) {
	acquireErr, reconcileErr := errors.New("redis connection refused"), errors.New("vizier unavailable")
	const validTick = `{"experiment":"experiments/123"}`
	const wantKey = "xmc:study-reconcile-lock:123"

	tests := []struct {
		name                            string
		data                            string
		acquireErr, recErr, releaseErr  error
		wantSuccess, wantRetry, wantRec bool
		wantErrIs                       error
		wantAcquireCalls                int
		wantKey                         string
		wantMetrics                     map[string]float64
		wantLogs                        []string
	}{
		{name: "fast reconcile", data: validTick, wantSuccess: true, wantAcquireCalls: 1, wantKey: wantKey, wantRec: true,
			wantMetrics: map[string]float64{outcomeReconciled: 1}, wantLogs: []string{"Processed study reconcile tick", "worker_id=worker-1", "xid=123"}},
		{name: "contended tick acked", data: validTick, acquireErr: redislock.ErrNotAcquired, wantSuccess: true, wantAcquireCalls: 1, wantKey: wantKey,
			wantMetrics: map[string]float64{outcomeContended: 1}, wantLogs: []string{"Skipped contended study reconcile tick", "xid=123"}},
		{name: "acquire error retried", data: validTick, acquireErr: acquireErr, wantRetry: true, wantErrIs: acquireErr, wantAcquireCalls: 1, wantKey: wantKey,
			wantMetrics: map[string]float64{outcomeRetryError: 1}, wantLogs: []string{"Failed to acquire study reconcile lock", "xid=123"}},
		{name: "reconcile error retried", data: validTick, recErr: reconcileErr, wantRetry: true, wantErrIs: reconcileErr, wantAcquireCalls: 1, wantKey: wantKey, wantRec: true,
			wantMetrics: map[string]float64{outcomeRetryError: 1}, wantLogs: []string{"Failed to process study reconcile tick", "xid=123"}},
		{name: "release lost lease logs warn", data: validTick, releaseErr: redislock.ErrLeaseLost, wantSuccess: true, wantAcquireCalls: 1, wantKey: wantKey, wantRec: true,
			wantMetrics: map[string]float64{outcomeReconciled: 1}, wantLogs: []string{"Study reconcile lock lease was already lost before release"}},
		{name: "release transport error logs warn", data: validTick, releaseErr: acquireErr, wantSuccess: true, wantAcquireCalls: 1, wantKey: wantKey, wantRec: true,
			wantMetrics: map[string]float64{outcomeReconciled: 1}, wantLogs: []string{"Failed to release study reconcile lock"}},
		{name: "empty payload", data: "", wantMetrics: map[string]float64{outcomeInvalidPayload: 1}, wantLogs: []string{"Dropping invalid study reconcile tick", "payload_bytes=0"}},
		{name: "malformed json", data: "not json", wantMetrics: map[string]float64{outcomeInvalidPayload: 1}, wantLogs: []string{"Dropping invalid study reconcile tick", "payload_bytes=8"}},
		{name: "multibyte invalid xid", data: `{"experiment":"experiments/１２３"}`, wantMetrics: map[string]float64{outcomeInvalidPayload: 1}, wantLogs: []string{"Dropping invalid study reconcile tick"}},
		{name: "wrong resource", data: `{"experiment":"xids/123"}`, wantMetrics: map[string]float64{outcomeInvalidPayload: 1}, wantLogs: []string{"Dropping invalid study reconcile tick"}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			lease := &fakeLease{releaseFn: func(context.Context) error { return tc.releaseErr }}
			locker := &fakeLocker{lease: lease, err: tc.acquireErr}
			var reconciled bool
			h, _ := NewHandler(HandlerParams{
				Locker: locker,
				Reconcile: func(_ context.Context, xid int64) error {
					reconciled = xid == 123
					return tc.recErr
				},
			})

			var logs bytes.Buffer
			before := snapshotOutcomes()
			got := h.HandleTask(newLogContext(&logs), "worker-1", tc.data)

			if got.WasRunSuccessful != tc.wantSuccess || got.ShouldBeRetried != tc.wantRetry {
				t.Errorf("HandleTask() = %+v, want success=%v retry=%v", got, tc.wantSuccess, tc.wantRetry)
			}
			if tc.wantErrIs != nil && !errors.Is(got.Error, tc.wantErrIs) {
				t.Errorf("error = %v, want %v", got.Error, tc.wantErrIs)
			}
			if locker.calls != tc.wantAcquireCalls || locker.lastKey != tc.wantKey || reconciled != tc.wantRec {
				t.Errorf("calls=%d key=%q rec=%v, want %d %q %v", locker.calls, locker.lastKey, reconciled, tc.wantAcquireCalls, tc.wantKey, tc.wantRec)
			}
			if tc.wantRec && (lease.releaseCalls != 1 || !lease.releaseHasDeadline) {
				t.Errorf("releaseCalls=%d hasDeadline=%v, want 1 bounded Release", lease.releaseCalls, lease.releaseHasDeadline)
			}
			if diff := cmp.Diff(tc.wantMetrics, diffOutcomes(before)); diff != "" {
				t.Errorf("metric deltas mismatch (-want +got):\n%s", diff)
			}
			for _, wantLog := range tc.wantLogs {
				if !strings.Contains(logs.String(), wantLog) {
					t.Errorf("logs = %q, want %q", logs.String(), wantLog)
				}
			}
		})
	}
}

func TestHandleTask_MultiRenewAndTeardownRace(t *testing.T) {
	thirdExtend := make(chan struct{})
	lease := &fakeLease{extendFn: func(_ context.Context, call int) error {
		if call == 3 {
			close(thirdExtend)
		}
		return nil
	}}
	h, _ := NewHandler(HandlerParams{
		Locker: &fakeLocker{lease: lease}, RenewInterval: 5 * time.Millisecond,
		Reconcile: func(ctx context.Context, _ int64) error {
			select {
			case <-thirdExtend:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		},
	})
	got := h.HandleTask(context.Background(), "worker-1", `{"experiment":"experiments/9"}`)
	if !got.WasRunSuccessful || lease.extendCalls < 3 || lease.extendAfterRelease || lease.releaseCalls != 1 {
		t.Fatalf("got=%+v extend=%d afterRel=%v release=%d", got, lease.extendCalls, lease.extendAfterRelease, lease.releaseCalls)
	}

	// In-flight Extend cancelled by normal reconcile completion must not fail the tick or log renewal warnings.
	extendStarted := make(chan struct{})
	raceLease := &fakeLease{extendFn: func(ctx context.Context, call int) error {
		if call == 1 {
			close(extendStarted)
			<-ctx.Done()
			return ctx.Err()
		}
		return nil
	}}
	hRace, _ := NewHandler(HandlerParams{
		Locker: &fakeLocker{lease: raceLease}, RenewInterval: 5 * time.Millisecond,
		Reconcile: func(context.Context, int64) error { <-extendStarted; return nil },
	})
	var raceLogs bytes.Buffer
	if got := hRace.HandleTask(newLogContext(&raceLogs), "worker-1", `{"experiment":"experiments/13"}`); !got.WasRunSuccessful || raceLease.releaseCalls != 1 {
		t.Fatalf("in-flight Extend teardown race got=%+v releaseCalls=%d, want success and 1", got, raceLease.releaseCalls)
	}
	if strings.Contains(raceLogs.String(), "Failed to extend study reconcile lock lease") || strings.Contains(raceLogs.String(), "Lost study reconcile lock lease") {
		t.Fatalf("in-flight Extend teardown race logs = %q, want zero renewal warnings", raceLogs.String())
	}

	// Genuine ErrLeaseLost racing with reconcile returning nil must NOT be swallowed by teardown.
	lostStarted := make(chan struct{})
	lostLease := &fakeLease{extendFn: func(ctx context.Context, call int) error {
		if call == 1 {
			close(lostStarted)
			<-ctx.Done()
			return redislock.ErrLeaseLost
		}
		return nil
	}}
	hLostRace, _ := NewHandler(HandlerParams{
		Locker: &fakeLocker{lease: lostLease}, RenewInterval: 5 * time.Millisecond,
		Reconcile: func(context.Context, int64) error { <-lostStarted; return nil },
	})
	gotLost := hLostRace.HandleTask(context.Background(), "worker-1", `{"experiment":"experiments/14"}`)
	if gotLost.WasRunSuccessful || !gotLost.ShouldBeRetried || !errors.Is(gotLost.Error, redislock.ErrLeaseLost) || lostLease.releaseCalls != 0 {
		t.Fatalf("in-flight ErrLeaseLost teardown race got=%+v releaseCalls=%d, want retryable ErrLeaseLost and 0 releases", gotLost, lostLease.releaseCalls)
	}
}

func TestHandleTask_RenewalDeadlineAndShutdownTransitions(t *testing.T) {
	transportErr := errors.New("broken pipe")
	tests := []struct {
		name             string
		extendFn         func(context.Context, int) error
		renewInterval    time.Duration
		reconcileTimeout time.Duration
		ignoreCancel     bool
		cancelParent     bool
		cancelOnExtend   bool
		wantExtendCalls  int
		wantReleaseCalls int
		wantErrIs        error
		wantLog          string
	}{
		{name: "lease lost cancels reconcile", extendFn: func(context.Context, int) error { return redislock.ErrLeaseLost },
			wantReleaseCalls: 0, wantErrIs: redislock.ErrLeaseLost, wantLog: "Lost study reconcile lock lease during renewal"},
		{name: "lease lost fails tick even if reconcile returns nil", extendFn: func(context.Context, int) error { return redislock.ErrLeaseLost },
			ignoreCancel: true, wantReleaseCalls: 0, wantErrIs: redislock.ErrLeaseLost, wantLog: "Lost study reconcile lock lease during renewal"},
		{name: "transport error cancels reconcile and releases", extendFn: func(context.Context, int) error { return transportErr },
			wantReleaseCalls: 1, wantErrIs: transportErr, wantLog: "Failed to extend study reconcile lock lease"},
		{name: "hung extend aborts at renew deadline", extendFn: func(ctx context.Context, _ int) error { <-ctx.Done(); return ctx.Err() },
			reconcileTimeout: 5 * time.Second, wantReleaseCalls: 1, wantErrIs: context.DeadlineExceeded, wantLog: "Failed to extend study reconcile lock lease"},
		{name: "reconcile timeout deadline", renewInterval: time.Second, reconcileTimeout: 15 * time.Millisecond,
			wantReleaseCalls: 1, wantErrIs: context.DeadlineExceeded},
		{name: "reconcile timeout while extend in flight suppresses renewal warning",
			extendFn:      func(ctx context.Context, _ int) error { <-ctx.Done(); return ctx.Err() },
			renewInterval: 80 * time.Millisecond, reconcileTimeout: 150 * time.Millisecond,
			wantExtendCalls: 1, wantReleaseCalls: 1, wantErrIs: context.DeadlineExceeded},
		{name: "reconcile timeout exact boundary when reconcile returns nil", renewInterval: time.Second, reconcileTimeout: 15 * time.Millisecond,
			ignoreCancel: true, wantReleaseCalls: 1, wantErrIs: context.DeadlineExceeded},
		{name: "parent shutdown cancels reconcile and releases on detached ctx", renewInterval: time.Second,
			cancelParent: true, wantReleaseCalls: 1, wantErrIs: context.Canceled},
		{name: "parent shutdown while extend in flight suppresses renewal warning",
			extendFn:         func(ctx context.Context, _ int) error { <-ctx.Done(); return ctx.Err() },
			reconcileTimeout: 5 * time.Second, cancelOnExtend: true,
			wantExtendCalls: 1, wantReleaseCalls: 1, wantErrIs: context.Canceled},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			parentCtx, cancelParent := context.WithCancel(context.Background())
			defer cancelParent()
			started := make(chan struct{})
			extendStarted := make(chan struct{})
			if tc.cancelParent {
				go func() { <-started; cancelParent() }()
			}
			if tc.cancelOnExtend {
				go func() { <-extendStarted; cancelParent() }()
			}
			renewInterval := tc.renewInterval
			if renewInterval == 0 {
				renewInterval = 5 * time.Millisecond
			}
			extendFn := tc.extendFn
			if tc.cancelOnExtend {
				extendFn = func(ctx context.Context, call int) error {
					if call == 1 {
						close(extendStarted)
					}
					return tc.extendFn(ctx, call)
				}
			}
			lease := &fakeLease{extendFn: extendFn}
			h, _ := NewHandler(HandlerParams{
				Locker: &fakeLocker{lease: lease}, RenewInterval: renewInterval, ReconcileTimeout: tc.reconcileTimeout,
				Reconcile: func(ctx context.Context, _ int64) error {
					close(started)
					<-ctx.Done()
					if tc.ignoreCancel {
						return nil
					}
					return ctx.Err()
				},
			})

			var logs bytes.Buffer
			ctx := logging.WithLogger(parentCtx, slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug})))
			before := snapshotOutcomes()
			got := h.HandleTask(ctx, "worker-1", `{"experiment":"experiments/14"}`)

			if got.WasRunSuccessful || !got.ShouldBeRetried || !errors.Is(got.Error, tc.wantErrIs) {
				t.Fatalf("got = %+v, want retryable %v", got, tc.wantErrIs)
			}
			if tc.wantExtendCalls > 0 && lease.extendCalls != tc.wantExtendCalls {
				t.Errorf("extendCalls=%d, want %d", lease.extendCalls, tc.wantExtendCalls)
			}
			if lease.releaseCalls != tc.wantReleaseCalls || (tc.wantReleaseCalls == 1 && (lease.releaseCtxErr != nil || !lease.releaseHasDeadline)) {
				t.Errorf("releaseCalls=%d ctxErr=%v hasDeadline=%v", lease.releaseCalls, lease.releaseCtxErr, lease.releaseHasDeadline)
			}
			if diff := cmp.Diff(map[string]float64{outcomeRetryError: 1}, diffOutcomes(before)); diff != "" {
				t.Errorf("metric deltas mismatch (-want +got):\n%s", diff)
			}
			if tc.wantLog != "" && !strings.Contains(logs.String(), tc.wantLog) {
				t.Errorf("logs = %q, want %q", logs.String(), tc.wantLog)
			}
			if tc.wantLog == "" && (strings.Contains(logs.String(), "Failed to extend study reconcile lock lease") || strings.Contains(logs.String(), "Lost study reconcile lock lease")) {
				t.Errorf("logs = %q, want zero lease renewal warnings", logs.String())
			}
		})
	}
}
