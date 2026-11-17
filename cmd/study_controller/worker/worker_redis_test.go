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
	"context"
	"errors"
	"fmt"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/redis/go-redis/v9"
	"xmanager_cloud/internal/kubernetes/common/constants"
	"xmanager_cloud/internal/redislock"
	"xmanager_cloud/internal/redistest"
)

var testRedisAddr string

func TestMain(m *testing.M) { os.Exit(redistest.RunMain(m, &testRedisAddr)) }

func startTestReplica(ctx context.Context, t *testing.T, workerName string, leaseTTL, renewInterval time.Duration, reconcile ReconcileFunc) {
	t.Helper()
	consumerClient := NewRedisConsumer(testRedisAddr)
	locker, err := redislock.New(consumerClient.Client.(redislock.Client), leaseTTL)
	if err != nil {
		t.Fatalf("redislock.New() error = %v", err)
	}
	handler, err := NewHandler(HandlerParams{Locker: redisLocker{locker: locker}, Reconcile: reconcile, RenewInterval: renewInterval})
	if err != nil {
		t.Fatalf("NewHandler() error = %v", err)
	}
	loopCtx, cancel := context.WithCancel(ctx)
	c := &Consumer{client: consumerClient, cancel: cancel, drainTimeout: defaultDrainTimeout}
	consumerClient.SetTaskHandler(handler.HandleTask)
	c.runLoop(func() { consumerClient.StartWorker(loopCtx, workerName) })
	t.Cleanup(func() { _ = c.Close(context.Background()) })
}

func publishTick(t *testing.T, client *redis.Client, xid int64) {
	t.Helper()
	err := client.XAdd(context.Background(), &redis.XAddArgs{
		Stream: constants.RedisStudyReconcileStream,
		Values: map[string]any{"data": fmt.Sprintf(`{"experiment":"experiments/%d"}`, xid), "failedAttempts": "0"},
	}).Err()
	if err != nil {
		t.Fatalf("XAdd(%d) error = %v", xid, err)
	}
}

func waitForCondition(t *testing.T, desc string, cond func() bool) {
	t.Helper()
	for deadline := time.Now().Add(10 * time.Second); time.Now().Before(deadline); time.Sleep(15 * time.Millisecond) {
		if cond() {
			return
		}
	}
	t.Fatalf("timed out waiting for %s", desc)
}

func TestConsumerRedis_MultiReplicaContentionAcksDuplicateWithoutOverlap(t *testing.T) {
	ctx := context.Background()
	admin := redis.NewClient(&redis.Options{Addr: testRedisAddr, ContextTimeoutEnabled: true})
	defer admin.Close()
	if err := admin.FlushAll(ctx).Err(); err != nil {
		t.Fatalf("FlushAll() error = %v", err)
	}

	const xid = int64(777)
	const otherXID = int64(778)
	reconcileEntered, releaseReconcile := make(chan struct{}), make(chan struct{})
	var inFlight, reconcileCalls, otherCalls atomic.Int32
	var overlap atomic.Bool
	reconcile := func(ctx context.Context, gotXID int64) error {
		if gotXID == otherXID {
			otherCalls.Add(1)
			return nil
		}
		if gotXID != xid {
			t.Errorf("reconcile xid = %d, want %d", gotXID, xid)
		}
		if inFlight.Add(1) != 1 {
			overlap.Store(true)
		}
		defer inFlight.Add(-1)
		if reconcileCalls.Add(1) == 1 {
			close(reconcileEntered)
		}
		select {
		case <-releaseReconcile:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	}

	contendedBefore := testutil.ToFloat64(reconcileTicksTotal.WithLabelValues(outcomeContended))
	reconciledBefore := testutil.ToFloat64(reconcileTicksTotal.WithLabelValues(outcomeReconciled))
	startTestReplica(ctx, t, "study-controller-worker-replica-a", 5*time.Second, time.Second, reconcile)
	startTestReplica(ctx, t, "study-controller-worker-replica-b", 5*time.Second, time.Second, reconcile)

	waitForCondition(t, "consumer group creation", func() bool {
		groups, err := admin.XInfoGroups(ctx, constants.RedisStudyReconcileStream).Result()
		return err == nil && len(groups) == 1
	})

	publishTick(t, admin, xid)
	<-reconcileEntered

	// While xid=777 holds its lock, a different study (xid=778) reconciles without blocking.
	publishTick(t, admin, otherXID)
	waitForCondition(t, "distinct study reconciled concurrently", func() bool {
		return otherCalls.Load() == 1
	})

	publishTick(t, admin, xid)
	waitForCondition(t, "contended duplicate tick acknowledged", func() bool {
		return testutil.ToFloat64(reconcileTicksTotal.WithLabelValues(outcomeContended))-contendedBefore >= 1
	})

	close(releaseReconcile)
	waitForCondition(t, "both distinct studies reconciled and PEL drained", func() bool {
		pending, err := admin.XPending(ctx, constants.RedisStudyReconcileStream, constants.RedisStudyControllerGroup).Result()
		delta := testutil.ToFloat64(reconcileTicksTotal.WithLabelValues(outcomeReconciled)) - reconciledBefore
		return err == nil && pending.Count == 0 && delta == 2
	})

	if overlap.Load() || reconcileCalls.Load() != 1 {
		t.Fatalf("overlap=%v reconcileCalls=%d, want false and 1", overlap.Load(), reconcileCalls.Load())
	}
	if dlqLen, err := admin.XLen(ctx, constants.RedisStudyReconcileDLQ).Result(); err != nil || dlqLen != 0 {
		t.Errorf("DLQ len = %d, err = %v; want 0", dlqLen, err)
	}
	if exists, err := admin.Exists(ctx, studyLockKey(xid)).Result(); err != nil || exists != 0 {
		t.Errorf("lock key Exists = %d, err = %v; want 0", exists, err)
	}
}

func TestConsumerRedis_LongReconcileExtendsLeaseAndCrashedHolderExpires(t *testing.T) {
	ctx := context.Background()
	admin := redis.NewClient(&redis.Options{Addr: testRedisAddr, ContextTimeoutEnabled: true})
	defer admin.Close()
	if err := admin.FlushAll(ctx).Err(); err != nil {
		t.Fatalf("FlushAll() error = %v", err)
	}

	const xid = int64(888)
	probeLocker, _ := redislock.New(admin, 120*time.Millisecond)
	if _, err := probeLocker.Acquire(ctx, studyLockKey(xid)); err != nil {
		t.Fatalf("probeLocker.Acquire() error = %v", err)
	}
	time.Sleep(180 * time.Millisecond)

	pastInitialTTL, releaseHold := make(chan struct{}), make(chan struct{})
	var done atomic.Bool
	startTestReplica(ctx, t, "study-controller-worker-long", 120*time.Millisecond, 40*time.Millisecond, func(ctx context.Context, _ int64) error {
		time.Sleep(160 * time.Millisecond)
		close(pastInitialTTL)
		select {
		case <-releaseHold:
		case <-ctx.Done():
		}
		done.Store(ctx.Err() == nil)
		return ctx.Err()
	})

	publishTick(t, admin, xid)
	<-pastInitialTTL
	if _, err := probeLocker.Acquire(ctx, studyLockKey(xid)); !errors.Is(err, redislock.ErrNotAcquired) {
		close(releaseHold)
		t.Fatalf("probeLocker.Acquire() past initial TTL = %v, want ErrNotAcquired", err)
	}
	close(releaseHold)
	waitForCondition(t, "long reconcile completion and PEL drain", func() bool {
		pending, err := admin.XPending(ctx, constants.RedisStudyReconcileStream, constants.RedisStudyControllerGroup).Result()
		return err == nil && pending.Count == 0 && done.Load()
	})
}
