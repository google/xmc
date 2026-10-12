// Copyright 2025 Google LLC
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
package redisclient

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/google/xmc/internal/logging"
	"github.com/redis/go-redis/v9"
)

// claimKey is the context key under which processMessage stores the claim of the message being
// handled, so that ExtendClaim can extend it.
type claimKey struct{}

type claim struct {
	msgID      string
	workerID   string
	extendChan chan<- struct{}
}

func withClaim(ctx context.Context, c claim) context.Context {
	return context.WithValue(ctx, claimKey{}, c)
}

func claimFromContext(ctx context.Context) (claim, bool) {
	c, ok := ctx.Value(claimKey{}).(claim)
	return c, ok
}

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

// Close closes the Redis client.
func (c *ConsumerClient) Close(ctx context.Context) error {
	return c.Client.Close()
}

func defaultTaskHandler(ctx context.Context, workerID string, data string) TaskResult {
	return TaskResult{WasRunSuccessful: true, ShouldBeRetried: false, Error: nil}
}

// SetTaskHandler sets the task handler function for the client.
func (c *ConsumerClient) SetTaskHandler(taskHandler func(ctx context.Context, workerID string, data string) TaskResult) {
	if c.TaskHandler != nil {
		logging.Warn(context.Background(), "Client.SetTaskHandler: overwriting existing task handler function")
	}
	c.TaskHandler = taskHandler
}

// InitializeClient creates stream and group for consumer.
func (c *ConsumerClient) InitializeClient(ctx context.Context) {
	err := c.Client.XGroupCreateMkStream(ctx, c.StreamName, c.GroupName, "0").Err()
	if err != nil && !strings.Contains(err.Error(), "BUSYGROUP") {
		logging.Error(ctx, "Failed to create stream and group", "error", err)
	}
}

// Reset flushes all data from Redis.
func (c *ConsumerClient) Reset(ctx context.Context) {
	if err := c.FlushAll(ctx).Err(); err != nil {
		logging.Error(ctx, "Failed to flush redis", "error", err)
	}
}

// DeleteStream deletes a stream from Redis.
func (c *ConsumerClient) DeleteStream(ctx context.Context, streamName string) {
	if err := c.Del(ctx, streamName).Err(); err != nil {
		logging.Error(ctx, "Failed to delete stream", "stream", streamName, "error", err)
	}
}

// AckMessage acknowledges a message in the Redis stream.
func (c *ConsumerClient) AckMessage(ctx context.Context, msgID string) {
	c.XAck(ctx, c.StreamName, c.GroupName, msgID)
}

// ExtendClaim extends the message lease by resetting the message's idle timer in the Redis PEL
// and resetting the local execution timeout monitor.
func (c *ConsumerClient) ExtendClaim(ctx context.Context) error {
	cl, ok := claimFromContext(ctx)
	if !ok {
		return fmt.Errorf("no message claim found in context")
	}

	err := c.Client.XClaim(ctx, &redis.XClaimArgs{
		Stream:   c.StreamName,
		Group:    c.GroupName,
		Consumer: cl.workerID,
		MinIdle:  0,
		Messages: []string{cl.msgID},
	}).Err()
	if err != nil {
		return fmt.Errorf("failed to extend claim for message %s: %w", cl.msgID, err)
	}

	select {
	case cl.extendChan <- struct{}{}:
	default:
	}

	logging.Debug(ctx, "Extended message claim", "taskID", cl.msgID, "workerID", cl.workerID)
	return nil
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

	logging.Info(ctx, "Worker started", "workerID", workerID)
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
			logging.Error(ctx, "Worker error reading stream", "workerID", workerID, "error", err)
			time.Sleep(1 * time.Second) // back off on other errors
			continue
		}

		for _, entry := range entries {
			for _, msg := range entry.Messages {
				c.processMessage(ctx, msg, workerID)
			}
		}
	}
	logging.Info(ctx, "Worker shutting down", "workerID", workerID)
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

	extendChan := make(chan struct{}, 1)
	taskCtx := withClaim(ctx, claim{msgID: msg.ID, workerID: workerID, extendChan: extendChan})

	// Run logic in goroutine
	go func() {
		result = c.TaskHandler(taskCtx, workerID, msg.Values["data"].(string))
		close(done)
	}()

	timer := time.NewTimer(c.ProcessTimeout)
	defer timer.Stop()

	for {
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
			return

		case <-extendChan:
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			timer.Reset(c.ProcessTimeout)
			logging.Debug(ctx, "Reset task timeout after claim extension", "taskID", msg.ID, "timeout", c.ProcessTimeout)

		case <-timer.C:
			msg = increaseFailedCount(ctx, msg)
			// Case: Local Timeout (Worker is alive but too slow)
			c.handleTimedOutTask(ctx, msg, "timeout")
			return
		}
	}
}

func (c *ConsumerClient) moveToDLQ(ctx context.Context, msg redis.XMessage, reason string) {
	logging.Warn(ctx, "Task moving to DLQ", "taskID", msg.ID, "reason", reason)
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
		logging.Warn(ctx, "Task failed, retrying", "taskID", msg.ID, "reason", reason, "attempt", failedCount)

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
	logging.Info(ctx, "Cleanup worker started", "workerID", workerID)
	ticker := time.NewTicker(cleanupInterval) // Tick frequently for responsiveness
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			logging.Info(ctx, "Cleanup worker shutting down", "workerID", workerID)
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
				logging.Error(ctx, fmt.Sprintf("Cleanup worker %s XAutoClaim error: %v", workerID, err))
			} else if len(claimed) > 0 {
				logging.Info(ctx, fmt.Sprintf("Cleanup: Claimed %d abandoned tasks", len(claimed)))
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
