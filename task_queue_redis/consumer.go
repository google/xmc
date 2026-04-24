// Copyright 2025 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//      http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.
package redisclient

import (
	"context"
	"strconv"
	"strings"
	"time"

	"go/log"
	"golang/github_com/go_redis/redis/v/v9/redis"
)

const (
	// DefaultBatchSize is the default batch size for reading messages from Redis. Defaults to 1 to
	// achieve maximum concurrency.
	DefaultBatchSize = 1
	// cleanupInterval is the interval at which the cleanup goroutine checks for abandoned messages.
	cleanupInterval = 500 * time.Millisecond
)

// NewConsumer creates a new redis client and configures client settings for consumption.
func NewConsumer(redisConfig Config, streamName, groupName, dlqStreamName string, maxRetries int, processTimeout, abandonedMessageTimeout time.Duration, readBatchSize int64) *ConsumerClient {
	if readBatchSize <= 0 {
		readBatchSize = DefaultBatchSize
	}
	options := getRedisOptions(redisConfig)
	return &ConsumerClient{
		baseManager: baseManager{
			Client:     redis.NewClient(options),
			StreamName: streamName,
		},
		GroupName:               groupName,
		DLQStreamName:           dlqStreamName,
		MaxRetries:              maxRetries,
		ProcessTimeout:          processTimeout,
		AbandonedMessageTimeout: abandonedMessageTimeout,
		TaskHandler:             defaultTaskHandler,
		ReadBatchSize:           readBatchSize,
	}
}

func defaultTaskHandler(ctx context.Context, workerID string, data string) TaskResult {
	return TaskResult{WasRunSuccessful: true, ShouldBeRetried: false, Error: nil}
}

// SetTaskHandler sets the task handler function for the client.
func (c *ConsumerClient) SetTaskHandler(taskHandler func(ctx context.Context, workerID string, data string) TaskResult) {
	if c.TaskHandler != nil {
		log.Warningf("Client.SetTaskHandler: overwriting existing task handler function")
	}
	c.TaskHandler = taskHandler
}

// InitializeClient creates stream and group for consumer.
func (c *ConsumerClient) InitializeClient(ctx context.Context) {
	err := c.Client.XGroupCreateMkStream(ctx, c.StreamName, c.GroupName, "0").Err()
	if err != nil && !strings.Contains(err.Error(), "BUSYGROUP") {
		log.ErrorContextf(ctx, "Failed to create stream and group: %v", err)
	}
}

// Reset flushes all data from Redis.
func (c *ConsumerClient) Reset(ctx context.Context) {
	if err := c.FlushAll(ctx).Err(); err != nil {
		log.ErrorContextf(ctx, "Failed to flush redis: %v", err)
	}
}

// DeleteStream deletes a stream from Redis.
func (c *ConsumerClient) DeleteStream(ctx context.Context, streamName string) {
	if err := c.Del(ctx, streamName).Err(); err != nil {
		log.ErrorContextf(ctx, "Failed to delete stream %s: %v", streamName, err)
	}
}

// AckMessage acknowledges a message in the Redis stream.
func (c *ConsumerClient) AckMessage(ctx context.Context, msgID string) {
	c.XAck(ctx, c.StreamName, c.GroupName, msgID)
}

func getFailedCount(msg redis.XMessage) int {
	var failedCount int
	if fs, ok := msg.Values["failedAttempts"].(string); ok {
		failedCount, _ = strconv.Atoi(fs)
	}
	return failedCount
}

func increaseFailedCount(ctx context.Context, msg redis.XMessage) redis.XMessage {
	failedCount := getFailedCount(msg)
	return redis.XMessage{
		ID: msg.ID,
		Values: map[string]any{
			"data":           msg.Values["data"],
			"failedAttempts": strconv.Itoa(failedCount + 1),
		},
	}
}

// StartWorker starts a worker that reads messages from a Redis stream and processes them.
// Meant to be called by the message consumer
func (c *ConsumerClient) StartWorker(ctx context.Context, workerID string) {
	c.InitializeClient(ctx)

	log.InfoContextf(ctx, "Worker %s started", workerID)
	for ctx.Err() == nil {
		entries, err := c.XReadGroup(ctx, &redis.XReadGroupArgs{
			Group:    c.GroupName,
			Consumer: workerID,
			Streams:  []string{c.StreamName, ">"},
			Count:    c.ReadBatchSize,
			Block:    2 * time.Second, // Block with timeout to avoid busy-looping
		}).Result()

		if err == redis.Nil { // redis.Nil is returned on timeout.
			continue
		}
		if err != nil {
			if ctx.Err() != nil {
				break
			}
			log.ErrorContextf(ctx, "Worker %s error reading stream: %v", workerID, err)
			time.Sleep(1 * time.Second) // back off on other errors
			continue
		}

		for _, entry := range entries {
			for _, msg := range entry.Messages {
				c.processMessage(ctx, msg, workerID)
			}
		}
	}
	log.InfoContextf(ctx, "Worker %s shutting down", workerID)
}

// processMessage processes a single message from the Redis stream.
func (c *ConsumerClient) processMessage(ctx context.Context, msg redis.XMessage, workerID string) {
	done := make(chan struct{})
	var result TaskResult

	failedCount := getFailedCount(msg)

	if failedCount > c.MaxRetries {
		c.handleCrashedTask(ctx, msg, "too_many_failed_attempts")
		return
	}

	// Run logic in goroutine
	go func() {
		result = c.TaskHandler(ctx, workerID, msg.Values["data"].(string))
		close(done)
	}()

	select {
	case <-done:
		if result.WasRunSuccessful {
			c.AckMessage(ctx, msg.ID)
		} else {
			msg = increaseFailedCount(ctx, msg)

			if result.ShouldBeRetried {
				c.handleCrashedTask(ctx, msg, "function_error_retryable")
			} else {
				c.moveToDLQ(ctx, msg, "non_retryable_error")
				c.AckMessage(ctx, msg.ID)
			}
		}

	case <-time.After(c.ProcessTimeout):
		msg = increaseFailedCount(ctx, msg)
		// Case: Local Timeout (Worker is alive but too slow)
		c.handleTimedOutTask(ctx, msg, "timeout")
	}
}

func (c *ConsumerClient) moveToDLQ(ctx context.Context, msg redis.XMessage, reason string) {
	log.WarningContextf(ctx, "🚨 Task %s moving to DLQ. Reason: %s", msg.ID, reason)
	c.XAdd(ctx, &redis.XAddArgs{
		Stream: c.DLQStreamName,
		MaxLen: DefaultMaxLen,
		Approx: true,
		Values: map[string]any{
			"original_id":    msg.ID,
			"data":           msg.Values["data"],
			"reason":         reason,
			"failedAttempts": strconv.Itoa(getFailedCount(msg)),
			"died_at":        time.Now().Format(time.RFC3339),
		},
	})
}

// For now we'll treat timed out tasks the same as crashed tasks.
func (c *ConsumerClient) handleTimedOutTask(ctx context.Context, msg redis.XMessage, reason string) {
	c.handleCrashedTask(ctx, msg, reason)
}

func (c *ConsumerClient) handleCrashedTask(ctx context.Context, msg redis.XMessage, reason string) {
	failedCount := getFailedCount(msg)
	if failedCount > c.MaxRetries {
		c.moveToDLQ(ctx, msg, reason)
	} else {
		// --- CASE: RETRY ---
		log.WarningContextf(ctx, "🔄 Task %s failed (%s). Retry attempt: %d", msg.ID, reason, failedCount)

		c.XAdd(ctx, &redis.XAddArgs{
			Stream: c.StreamName,
			MaxLen: DefaultMaxLen,
			Approx: true,
			Values: map[string]any{
				"data":           msg.Values["data"],
				"failedAttempts": strconv.Itoa(failedCount),
			},
		})
	}

	// Always ACK the old message to remove it from the Working/Pending state
	c.AckMessage(ctx, msg.ID)
}

// --- The Cleanup Goroutine ---

// RunCleanup runs a cleanup goroutine that claims abandoned messages and re-processes them.
// Meant to be called by the message consumer
func (c *ConsumerClient) RunCleanup(ctx context.Context, workerID string) {
	log.InfoContextf(ctx, "Cleanup worker %s started", workerID)
	ticker := time.NewTicker(cleanupInterval) // Tick frequently for responsiveness
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			log.InfoContextf(ctx, "Cleanup worker %s shutting down", workerID)
			return
		case <-ticker.C:
			claimed, _, err := c.XAutoClaim(ctx, &redis.XAutoClaimArgs{
				Stream:   c.StreamName,
				Group:    c.GroupName,
				MinIdle:  c.AbandonedMessageTimeout,
				Consumer: workerID,
				Start:    "0-0",
				Count:    10,
			}).Result()

			if err != nil && err != redis.Nil {
				log.ErrorContextf(ctx, "Cleanup worker %s XAutoClaim error: %v", workerID, err)
			} else if len(claimed) > 0 {
				log.InfoContextf(ctx, "Cleanup: Claimed %d abandoned tasks", len(claimed))
				for _, msg := range claimed {
					c.processMessage(ctx, msg, workerID)
				}
			}
		}
	}
}

// FetchPendingCount returns the number of pending messages in the stream - messages that were accepted
// by a worker but not yet acknowledged.
func (c *ConsumerClient) FetchPendingCount(ctx context.Context) (int, error) {
	p, err := c.XPendingExt(ctx, &redis.XPendingExtArgs{
		Stream: c.StreamName,
		Group:  c.GroupName,
		Start:  "-",
		End:    "+",
		Count:  10,
	}).Result()
	if err != nil {
		return -1, err
	}
	return len(p), nil
}

// FetchStreamLen returns the length of a stream.
func (c *ConsumerClient) FetchStreamLen(ctx context.Context, stream string) (int64, error) {
	l, err := c.XLen(ctx, stream).Result()
	if err != nil {
		return -1, err
	}
	return l, nil
}
