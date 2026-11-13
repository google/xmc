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
	"sync"
	"time"

	"xmanager_cloud/internal/kubernetes/common/constants"
	"xmanager_cloud/internal/logging"
	"xmanager_cloud/internal/task_queue_redis/redisclient"
)

// defaultDrainTimeout bounds how long Close waits for the worker and cleanup loops. It covers the
// 2s XREADGROUP block of the worker loop plus redisclient.SettleTimeout, so a tick being settled
// when Close is called finishes before the client is closed.
const defaultDrainTimeout = redisclient.SettleTimeout + 3*time.Second

// NewRedisConsumer returns a Redis consumer of the study reconcile stream for the Redis server at
// addr. It reads the password from REDIS_PASSWORD and connects lazily. Context deadlines bound its
// socket I/O, so a readiness ping on a Redis server that never replies fails at the ping's
// deadline rather than after the client's longer read timeout.
func NewRedisConsumer(addr string) *redisclient.ConsumerClient {
	return redisclient.NewConsumer(
		redisclient.Config{Addr: addr, ContextTimeoutEnabled: true},
		constants.RedisStudyReconcileStream,
		constants.RedisStudyControllerGroup,
		constants.RedisStudyReconcileDLQ,
		constants.RedisMaxRetries,
		constants.RedisProcessTimeout,
		constants.RedisAbandonedMessageTimeout,
		constants.RedisReadBatchSize,
	)
}

// Consumer runs the worker and cleanup loops of a Redis consumer with handleTask as its handler.
type Consumer struct {
	client       redisclient.Consumer
	cancel       context.CancelFunc
	wg           sync.WaitGroup
	once         sync.Once
	drainTimeout time.Duration
}

// Start sets handleTask on client and runs its worker and cleanup loops until ctx is done or Close
// is called. The loops create the stream and consumer group themselves, at start-up and again
// whenever Redis reports them missing (NOGROUP), so Start never blocks on Redis and consumption
// resumes after Redis comes back without them.
func Start(ctx context.Context, client redisclient.Consumer) *Consumer {
	client.SetTaskHandler(handleTask)
	loopCtx, cancel := context.WithCancel(ctx)
	c := &Consumer{client: client, cancel: cancel, drainTimeout: defaultDrainTimeout}
	workerID := redisclient.HostConsumerName(constants.StudyControllerWorkerID)
	cleanupWorkerID := redisclient.HostConsumerName(constants.StudyControllerCleanupWorkerID)
	c.runLoop(func() { client.StartWorker(loopCtx, workerID) })
	c.runLoop(func() { client.RunCleanup(loopCtx, cleanupWorkerID) })
	logging.Info(ctx, "Started study reconcile consumer",
		"stream", constants.RedisStudyReconcileStream, "group", constants.RedisStudyControllerGroup,
		"worker_id", workerID, "cleanup_worker_id", cleanupWorkerID)
	return c
}

// runLoop runs loop in a goroutine tracked by c.wg.
func (c *Consumer) runLoop(loop func()) {
	c.wg.Add(1)
	go func() {
		defer c.wg.Done()
		loop()
	}()
}

// Close stops the loops and closes the Redis client. It waits for the loops to return, but at
// most the drain timeout (8s) and only until ctx is done; after that it logs a warning and closes
// the client anyway, leaving a stuck loop to fail on the closed client. A tick being handled when
// Close is called is still acknowledged or retried. Calls after the first return nil.
func (c *Consumer) Close(ctx context.Context) error {
	var err error
	c.once.Do(func() {
		c.cancel()
		c.waitForLoops(ctx)
		err = c.client.Close(ctx)
	})
	return err
}

// waitForLoops waits for the loops to return, the drain timeout or ctx, whichever comes first, and
// logs a warning unless the loops returned.
func (c *Consumer) waitForLoops(ctx context.Context) {
	done := make(chan struct{})
	go func() {
		c.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-ctx.Done():
		logging.Warn(ctx, "Stopped waiting for the study reconcile consumer loops", "error", ctx.Err())
	case <-time.After(c.drainTimeout):
		logging.Warn(ctx, "Study reconcile consumer loops did not stop within the drain timeout", "timeout", c.drainTimeout)
	}
}
