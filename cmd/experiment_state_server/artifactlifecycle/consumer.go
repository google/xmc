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

package artifactlifecycle

import (
	"context"
	"fmt"
	"os"
	"sync"
	"time"

	"xmanager_cloud/internal/kubernetes/common/constants"
	"xmanager_cloud/internal/logging"
	"xmanager_cloud/internal/task_queue_redis/redisclient"
)

const (
	defaultDrainTimeout = 5 * time.Second
)

// ConsumerConfig defines the configuration required to initialize the artifact lifecycle stream consumer.
type ConsumerConfig struct {
	RedisAddr string
	Client    redisclient.Consumer
}

// Consumer coordinates background consumption of the artifact lifecycle event stream.
type Consumer struct {
	client       redisclient.Consumer
	cancel       context.CancelFunc
	wg           sync.WaitGroup
	once         sync.Once
	drainTimeout time.Duration
}

// NewRedisConsumer creates the Redis consumer client for the artifact lifecycle stream.
func NewRedisConsumer(redisAddr string) (redisclient.Consumer, error) {
	if redisAddr == "" {
		return nil, fmt.Errorf("redis address cannot be empty")
	}
	return redisclient.NewConsumer(
		redisclient.Config{
			Addr: redisAddr,
		},
		constants.RedisArtifactLifecycleStream,
		constants.RedisESSArtifactLifecycleGroup,
		constants.RedisDLQ,
		constants.RedisMaxRetries,
		constants.RedisProcessTimeout,
		constants.RedisAbandonedMessageTimeout,
		constants.RedisReadBatchSize,
	), nil
}

// StartConsumer creates and starts the artifact lifecycle stream consumer and its cleanup worker.
func StartConsumer(ctx context.Context, cfg ConsumerConfig, worker *Worker) (*Consumer, error) {
	if worker == nil {
		return nil, fmt.Errorf("worker cannot be nil")
	}

	client := cfg.Client
	var err error
	if client == nil {
		client, err = NewRedisConsumer(cfg.RedisAddr)
		if err != nil {
			return nil, err
		}
	}

	client.InitializeClient(ctx)
	client.SetTaskHandler(worker.HandleTask)

	workerID := essWorkerID(constants.ESSArtifactLifecycleWorkerID)
	cleanupWorkerID := essWorkerID(constants.ESSArtifactLifecycleCleanupWorkerID)

	consumerCtx, cancel := context.WithCancel(ctx)
	c := &Consumer{
		client:       client,
		cancel:       cancel,
		drainTimeout: defaultDrainTimeout,
	}

	c.wg.Add(1)
	go func() {
		defer c.wg.Done()
		client.StartWorker(consumerCtx, workerID)
	}()

	c.wg.Add(1)
	go func() {
		defer c.wg.Done()
		client.RunCleanup(consumerCtx, cleanupWorkerID)
	}()

	logging.Info(ctx, "Started Redis artifact lifecycle consumer and workers",
		"stream", constants.RedisArtifactLifecycleStream,
		"group", constants.RedisESSArtifactLifecycleGroup,
		"worker_id", workerID,
		"cleanup_worker_id", cleanupWorkerID,
	)

	return c, nil
}

// Close signals consumer workers to stop, waits for them with a bounded drain timeout,
// and closes the underlying Redis client connection.
func (c *Consumer) Close() error {
	if c == nil {
		return nil
	}

	var err error
	c.once.Do(func() {
		if c.cancel != nil {
			c.cancel()
		}

		timeout := c.drainTimeout
		if timeout <= 0 {
			timeout = defaultDrainTimeout
		}

		done := make(chan struct{})
		go func() {
			c.wg.Wait()
			close(done)
		}()

		select {
		case <-done:
		case <-time.After(timeout):
			logging.Warn(context.Background(), "Artifact lifecycle consumer workers did not finish draining within timeout; forcing close",
				"timeout", timeout)
		}

		if c.client != nil {
			err = c.client.Close(context.Background())
		}
	})
	return err
}

func essWorkerID(prefix string) string {
	hostname, err := os.Hostname()
	if err != nil || hostname == "" {
		hostname = "unknown"
	}
	return fmt.Sprintf("%s-%s", prefix, hostname)
}
