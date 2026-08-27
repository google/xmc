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
// The validator program is a script to test redisclient functionality against a real Redis instance.
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"strconv"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
	"go/google"
	"go/log"
	"xmanager_cloud/task_queue_redis/redisclient"
)

const (
	// Here you need to put your Redis address.
	redisAddr = "localhost:16379"

	// Here you need to put your Redis password.
	redisPassword = "YOUR_REDIS_PASSWORD"

	maxFailedAttempts = 5

	streamName              = "test-stream"
	groupName               = "test-group"
	dlqStreamName           = "test-dlq"
	processTimeout          = 1 * time.Second
	abandonedMessageTimeout = 2 * time.Second
	readBatchSize           = 1
)

type StreamStats struct {
	PendingCount int
	StreamLength int64
	DLQLength    int64
}

type testCase struct {
	name              string
	testFunc          func(context.Context, *redisclient.ConsumerClient, redisclient.Publisher) (StreamStats, error)
	expectedPending   int
	expectedStreamLen int64
	expectedDlqLen    int64
}

func main() {
	google.Init()
	ctx := context.Background()
	os.Setenv("REDIS_PASSWORD", redisPassword)
	redisConfig := redisclient.Config{
		Addr: redisAddr,
	}
	consumer := redisclient.NewConsumer(redisConfig, streamName, groupName, dlqStreamName, maxFailedAttempts, processTimeout, abandonedMessageTimeout, readBatchSize)
	consumer.SetTaskHandler(func(ctx context.Context, workerID string, data string) redisclient.TaskResult {
		return redisclient.TaskResult{WasRunSuccessful: true, ShouldBeRetried: false, Error: nil}
	})

	publisher := redisclient.NewPublisher(redisConfig, streamName)

	if err := consumer.Client.Ping(ctx).Err(); err != nil {
		log.ErrorContextf(ctx, "ERROR: Could not connect to Redis at %s: %v", redisAddr, err)
		os.Exit(1)
	}
	log.InfoContextf(ctx, "Connected to Redis at %s", redisAddr)

	tests := []testCase{
		{"TestSuccess", testSuccess, 0, 1, 0},
		{"TestPending", testPending, 1, 3, 0},
		{"TestRetryAndMoveToDLQ", testRetryAndMoveToDLQ, 0, maxFailedAttempts + 2, 2},
		{"TestCleanup", testCleanup, 0, maxFailedAttempts + 1, 1},
		{"TestLoadBalancing", testLoadBalancing, 0, 15, 0},
		{"TestTimeout", testTimeout, 0, maxFailedAttempts + 1, 1},
		{"TestBatchRead", testBatchRead, 0, 5, 0},
	}

	allPassed := true
	for _, tc := range tests {
		log.InfoContextf(ctx, "--- Running test: %s ---", tc.name)
		if err := setupRedis(ctx, consumer); err != nil {
			log.ErrorContextf(ctx, "Failed to setup Redis: %v", err)
			allPassed = false
			continue
		}

		stats, err := tc.testFunc(ctx, consumer, publisher)
		if err != nil {
			log.ErrorContextf(ctx, "--- FAIL: %s returned error: %v ---", tc.name, err)
			allPassed = false
			continue
		}

		success := true
		log.InfoContextf(ctx, "Validating tests results for %s", tc.name)
		if stats.PendingCount != tc.expectedPending {
			log.ErrorContextf(ctx, "Failure: expected %d pending messages, got %d", tc.expectedPending, stats.PendingCount)
			success = false
		}
		if stats.StreamLength != tc.expectedStreamLen {
			log.ErrorContextf(ctx, "Failure: expected %d stream len, got %d", tc.expectedStreamLen, stats.StreamLength)
			success = false
		}
		if stats.DLQLength != tc.expectedDlqLen {
			log.ErrorContextf(ctx, "Failure: expected %d DLQ stream len, got %d", tc.expectedDlqLen, stats.DLQLength)
			success = false
		}

		if !success {
			log.ErrorContextf(ctx, "--- FAIL: %s ---", tc.name)
			allPassed = false
		} else {
			log.InfoContextf(ctx, "--- PASS: %s ---", tc.name)
		}
	}

	if allPassed {
		log.InfoContextf(ctx, "All validation tests passed!")
	} else {
		log.ErrorContextf(ctx, "SOME VALIDATION TESTS FAILED.")
		os.Exit(1)
	}
}

// setupRedis resets Redis state for a test run.
func setupRedis(ctx context.Context, consumer *redisclient.ConsumerClient) error {
	log.InfoContextf(ctx, "Resetting Redis state (FLUSHALL, creating group)...")

	consumer.Reset(ctx)
	consumer.InitializeClient(ctx)
	consumer.DeleteStream(ctx, dlqStreamName)

	// Ensure DLQ stream doesn't exist from previous failed run if FlushAll failed
	consumer.Client.Del(ctx, dlqStreamName)
	return nil
}

type workerHandle struct {
	cancel context.CancelFunc
	wg     *sync.WaitGroup
}

// runWorkerInBackground starts the given worker function (e.g., StartWorker or RunCleanup)
// in a goroutine and returns a handle to manage its lifecycle.
func runWorkerInBackground(ctx context.Context, workerID string, workerFunc func(context.Context, string)) workerHandle {
	wctx, cancel := context.WithCancel(ctx)
	wg := &sync.WaitGroup{}
	wg.Add(1)
	go func() {
		defer wg.Done()
		workerFunc(wctx, workerID)
	}()
	return workerHandle{cancel, wg}
}

func testSuccess(ctx context.Context, consumer *redisclient.ConsumerClient, publisher redisclient.Publisher) (StreamStats, error) {
	log.InfoContextf(ctx, "Testing successful message processing...")
	// Setup: Mock function to succeed.
	consumer.SetTaskHandler(func(ctx context.Context, workerID, data string) redisclient.TaskResult {
		return redisclient.TaskResult{WasRunSuccessful: true, ShouldBeRetried: false, Error: nil}
	})

	// Publisher: Start the cleanup goroutine
	cleanupHandle := runWorkerInBackground(ctx, "cleanup-1", consumer.RunCleanup)

	// Subscribers: Start worker goroutine.
	workerHandle := runWorkerInBackground(ctx, "worker-1", consumer.StartWorker)

	// Publisher: Add a message to the stream.
	log.InfoContextf(ctx, "Publishing message 'ok'...")
	publisher.AddMessage(ctx, "ok", "0")

	// Assertion: Wait for message to be ACKed.
	log.InfoContextf(ctx, "Waiting for message to be ACKed...")
	time.Sleep(5 * time.Second)

	pending, _ := consumer.FetchPendingCount(ctx)
	streamLen, _ := consumer.FetchStreamLen(ctx, streamName)
	dlqLen, _ := consumer.FetchStreamLen(ctx, dlqStreamName)

	// Cleanup: Stop workers.
	workerHandle.cancel()
	cleanupHandle.cancel()
	workerHandle.wg.Wait()
	cleanupHandle.wg.Wait()
	return StreamStats{PendingCount: pending, StreamLength: streamLen, DLQLength: dlqLen}, nil
}

func testPending(ctx context.Context, consumer *redisclient.ConsumerClient, publisher redisclient.Publisher) (StreamStats, error) {
	log.InfoContextf(ctx, "Testing pending message count during processing...")
	consumer.ProcessTimeout = 10 * time.Second
	consumer.AbandonedMessageTimeout = 10 * time.Second
	defer func() { consumer.ProcessTimeout = processTimeout }()
	defer func() { consumer.AbandonedMessageTimeout = abandonedMessageTimeout }()

	consumer.SetTaskHandler(func(ctx context.Context, workerID, data string) redisclient.TaskResult {
		time.Sleep(8 * time.Second)
		return redisclient.TaskResult{WasRunSuccessful: true, ShouldBeRetried: false, Error: nil}
	})

	cleanupHandle := runWorkerInBackground(ctx, "cleanup-1", consumer.RunCleanup)
	workerHandle := runWorkerInBackground(ctx, "worker-1", consumer.StartWorker)

	publisher.AddMessage(ctx, "msg1", "0")
	publisher.AddMessage(ctx, "msg2", "0")
	publisher.AddMessage(ctx, "msg3", "0")

	time.Sleep(5 * time.Second) // Wait for worker to pick one message

	pending, _ := consumer.FetchPendingCount(ctx)
	streamLen, _ := consumer.FetchStreamLen(ctx, streamName)
	dlqLen, _ := consumer.FetchStreamLen(ctx, dlqStreamName)

	workerHandle.cancel()
	cleanupHandle.cancel()
	workerHandle.wg.Wait()
	cleanupHandle.wg.Wait()
	return StreamStats{PendingCount: pending, StreamLength: streamLen, DLQLength: dlqLen}, nil
}

func testRetryAndMoveToDLQ(ctx context.Context, consumer *redisclient.ConsumerClient, publisher redisclient.Publisher) (StreamStats, error) {
	log.InfoContextf(ctx, "Testing message retry on function error if retryable...")
	// Setup: Mock function to fail.
	consumer.SetTaskHandler(func(ctx context.Context, workerID, data string) redisclient.TaskResult {
		if data == "bad-no-retry" {
			return redisclient.TaskResult{WasRunSuccessful: false, ShouldBeRetried: false, Error: errors.New("non-retryable error")}
		}
		return redisclient.TaskResult{WasRunSuccessful: false, ShouldBeRetried: true, Error: errors.New("error")}
	})

	// Publisher: Start the cleanup goroutine
	cleanupHandle := runWorkerInBackground(ctx, "cleanup-1", consumer.RunCleanup)

	// Subscribers: Start worker and cleanup goroutines.
	workerHandle := runWorkerInBackground(ctx, "worker-1", consumer.StartWorker)

	// Publisher: Add a message that will cause an error.
	log.InfoContextf(ctx, "Publishing message 'bad-with-retry' that will be retried...")
	publisher.AddMessage(ctx, "bad-with-retry", "0")
	// Publisher: Add a message that will cause a non-retryable error.
	log.InfoContextf(ctx, "Publishing message 'bad-no-retry' that will not be retried...")
	publisher.AddMessage(ctx, "bad-no-retry", "0")

	log.InfoContextf(ctx, "Waiting for message to be re-queued for retry...")
	time.Sleep(5 * time.Second)

	pending, _ := consumer.FetchPendingCount(ctx)
	streamLen, _ := consumer.FetchStreamLen(ctx, streamName)
	dlqLen, _ := consumer.FetchStreamLen(ctx, dlqStreamName)

	dlqMessages, _ := consumer.Client.XRange(ctx, dlqStreamName, "-", "+").Result()
	noRetryMsgFound := false
	badMsgFound := false
	var validationErrors []error
	for _, msg := range dlqMessages {
		failedAttemptsStr, ok := msg.Values["failedAttempts"].(string)
		if !ok {
			validationErrors = append(validationErrors, fmt.Errorf("message in DLQ has unexpected type for failedAttempts: got %T, want string", msg.Values["failedAttempts"]))
			continue
		}
		failedAttempts, err := strconv.Atoi(failedAttemptsStr)
		if err != nil {
			validationErrors = append(validationErrors, fmt.Errorf("message in DLQ has invalid failedAttempts: %s", failedAttemptsStr))
			continue
		}

		if msg.Values["data"] == "bad-no-retry" {
			noRetryMsgFound = true
			if failedAttempts != 1 {
				validationErrors = append(validationErrors, fmt.Errorf("'bad-no-retry' message in DLQ has %d failed attempts, want 1", failedAttempts))
			} else {
				log.InfoContextf(ctx, "'bad-no-retry' message found in DLQ with 1 failed attempt, as expected.")
			}
		}
		if msg.Values["data"] == "bad-with-retry" {
			badMsgFound = true
			if failedAttempts != maxFailedAttempts+1 {
				validationErrors = append(validationErrors, fmt.Errorf("'bad-with-retry' message in DLQ has %d failed attempts, want %d", failedAttempts, maxFailedAttempts+1))
			} else {
				log.InfoContextf(ctx, "'bad-with-retry' message found in DLQ with %d failed attempts, as expected.", maxFailedAttempts+1)
			}
		}
	}
	if !noRetryMsgFound {
		validationErrors = append(validationErrors, errors.New("'bad-no-retry' message not found in DLQ"))
	}
	if !badMsgFound {
		validationErrors = append(validationErrors, errors.New("'bad' message not found in DLQ"))
	}

	// Cleanup: Stop workers.
	workerHandle.cancel()
	cleanupHandle.cancel()
	workerHandle.wg.Wait()
	cleanupHandle.wg.Wait()

	stats := StreamStats{PendingCount: pending, StreamLength: streamLen, DLQLength: dlqLen}
	if len(validationErrors) > 0 {
		return stats, errors.Join(validationErrors...)
	}
	return stats, nil
}

func testCleanup(ctx context.Context, consumer *redisclient.ConsumerClient, publisher redisclient.Publisher) (StreamStats, error) {
	log.InfoContextf(ctx, "Testing cleanup of abandoned messages...")
	// Setup: Mock function to fail, so we can see it gets re-processed and retried.
	consumer.SetTaskHandler(func(ctx context.Context, workerID, data string) redisclient.TaskResult {
		return redisclient.TaskResult{WasRunSuccessful: false, ShouldBeRetried: true, Error: errors.New("error")}
	})

	// Publisher simulation: Add and consume a message by 'abandoner' to make it pending.
	log.InfoContextf(ctx, "Publishing message 'abandon' and leaving it pending...")
	publisher.AddMessage(ctx, "abandon", "0")
	_, err := consumer.Client.XReadGroup(ctx, &redis.XReadGroupArgs{
		Group:    groupName,
		Consumer: "abandoner",
		Streams:  []string{streamName, ">"},
		Count:    1,
	}).Result()
	if err != nil {
		log.ErrorContextf(ctx, "XReadGroup for abandoner failed: %v", err)
		return StreamStats{PendingCount: -1}, fmt.Errorf("XReadGroup for abandoner failed: %v", err)
	}

	log.InfoContextf(ctx, "Message abandoned, starting worker and cleanup worker...")
	// Publisher: Start the cleanup goroutine
	cleanupHandle := runWorkerInBackground(ctx, "cleanup-1", consumer.RunCleanup)

	// Subscribers: Start worker goroutine.
	workerHandle := runWorkerInBackground(ctx, "worker-1", consumer.StartWorker)

	// Assertion: Cleanup should run XAutoClaim, find message idle for > MinIdle,
	// process it via ProcessMessage, which will see an error, ACK the original,
	// and add a new message to the stream with failedAttempts=1.
	// We expect 1 new message for the group to process. Pending should be 0.
	log.InfoContextf(ctx, "Waiting for cleanup worker to claim and re-queue message...")
	time.Sleep(5 * time.Second)

	pending, _ := consumer.FetchPendingCount(ctx)
	streamLen, _ := consumer.FetchStreamLen(ctx, streamName)
	dlqLen, _ := consumer.FetchStreamLen(ctx, dlqStreamName)

	// Cleanup: Stop workers.
	workerHandle.cancel()
	cleanupHandle.cancel()
	workerHandle.wg.Wait()
	cleanupHandle.wg.Wait()
	return StreamStats{PendingCount: pending, StreamLength: streamLen, DLQLength: dlqLen}, nil
}

func testLoadBalancing(ctx context.Context, consumer *redisclient.ConsumerClient, publisher redisclient.Publisher) (StreamStats, error) {
	messageCount := 15
	log.InfoContextf(ctx, "Testing load balancing with 3 workers and %d messages...", messageCount)
	var mu sync.Mutex
	processedCount := make(map[string]int)

	consumer.SetTaskHandler(func(ctx context.Context, workerID, data string) redisclient.TaskResult {
		time.Sleep(200 * time.Millisecond) // Sleep to distribute work
		mu.Lock()
		processedCount[data]++
		mu.Unlock()
		return redisclient.TaskResult{WasRunSuccessful: true, ShouldBeRetried: false, Error: nil}
	})
	// Publisher: Start the cleanup goroutine
	cleanupHandle := runWorkerInBackground(ctx, "cleanup-1", consumer.RunCleanup)

	// Subscribers: Start 3 workers and 1 cleanup.
	worker1 := runWorkerInBackground(ctx, "worker-lb-1", consumer.StartWorker)
	worker2 := runWorkerInBackground(ctx, "worker-lb-2", consumer.StartWorker)
	worker3 := runWorkerInBackground(ctx, "worker-lb-3", consumer.StartWorker)

	// Publisher: Add messageCount messages to the stream.
	log.InfoContextf(ctx, "Publishing %d messages...", messageCount)
	for i := 0; i < messageCount; i++ {
		publisher.AddMessage(ctx, strconv.Itoa(i), "0")
	}

	// Assertion: Wait for messages to be processed.
	// 15 messages * 200ms / 3 workers = 1s.
	// Let's wait 10 seconds to be sure all are processed and ACKed.
	log.InfoContextf(ctx, "Waiting for messages to be processed...")
	time.Sleep(10 * time.Second)

	pending, _ := consumer.FetchPendingCount(ctx)
	streamLen, _ := consumer.FetchStreamLen(ctx, streamName)
	dlqLen, _ := consumer.FetchStreamLen(ctx, dlqStreamName)

	// Cleanup: Stop workers.
	worker1.cancel()
	worker2.cancel()
	worker3.cancel()
	cleanupHandle.cancel()
	worker1.wg.Wait()
	worker2.wg.Wait()
	worker3.wg.Wait()
	cleanupHandle.wg.Wait()

	// Validate processed counts
	var validationErrors []error
	mu.Lock()
	if len(processedCount) != messageCount {
		validationErrors = append(validationErrors, fmt.Errorf("expected %d processed messages, got %d", messageCount, len(processedCount)))
	}
	for i := 0; i < messageCount; i++ {
		count := processedCount[strconv.Itoa(i)]
		if count != 1 {
			validationErrors = append(validationErrors, fmt.Errorf("message %d processed %d times, expected 1", i, count))
		}
	}
	mu.Unlock()

	stats := StreamStats{PendingCount: pending, StreamLength: streamLen, DLQLength: dlqLen}
	if len(validationErrors) > 0 {
		return stats, errors.Join(validationErrors...)
	}

	return stats, nil
}

func testTimeout(ctx context.Context, consumer *redisclient.ConsumerClient, publisher redisclient.Publisher) (StreamStats, error) {
	log.InfoContextf(ctx, "Testing message timeout...")
	// Setup: Mock function to timeout.
	consumer.SetTaskHandler(func(ctx context.Context, workerID, data string) redisclient.TaskResult {
		time.Sleep(processTimeout + 500*time.Millisecond)
		return redisclient.TaskResult{WasRunSuccessful: true, ShouldBeRetried: false, Error: nil}
	})

	// Publisher: Start the cleanup goroutine
	cleanupHandle := runWorkerInBackground(ctx, "cleanup-1", consumer.RunCleanup)

	// Subscribers: Start worker and cleanup goroutines.
	workerHandle := runWorkerInBackground(ctx, "worker-1", consumer.StartWorker)

	// Publisher: Add a message that will cause a timeout.
	log.InfoContextf(ctx, "Publishing message 'timeout'...")
	publisher.AddMessage(ctx, "timeout", "0")

	// Assertion: Wait for worker to timeout, ACK original message, and add a new one for retry.
	// This will repeat until maxFailedAttempts is reached, and message is moved to DLQ.
	log.InfoContextf(ctx, "Waiting for message to be moved to DLQ after timeouts...")
	time.Sleep(10 * time.Second) // need more time for 5 timeouts of 1s each + redis communication.

	pending, _ := consumer.FetchPendingCount(ctx)
	streamLen, _ := consumer.FetchStreamLen(ctx, streamName)
	dlqLen, _ := consumer.FetchStreamLen(ctx, dlqStreamName)

	// Cleanup: Stop workers.
	workerHandle.cancel()
	cleanupHandle.cancel()
	workerHandle.wg.Wait()
	cleanupHandle.wg.Wait()
	return StreamStats{PendingCount: pending, StreamLength: streamLen, DLQLength: dlqLen}, nil
}

func testBatchRead(ctx context.Context, consumer *redisclient.ConsumerClient, publisher redisclient.Publisher) (StreamStats, error) {
	log.InfoContextf(ctx, "Testing batching with batchSize=10...")
	numberOfMessages := 5
	consumer.ReadBatchSize = 10
	consumer.AbandonedMessageTimeout = 10 * time.Second
	defer func() { consumer.ReadBatchSize = readBatchSize }()
	defer func() { consumer.AbandonedMessageTimeout = abandonedMessageTimeout }()

	var mu sync.Mutex
	processedBy := make(map[string]string) // map message data to workerId

	consumer.SetTaskHandler(func(ctx context.Context, workerID, data string) redisclient.TaskResult {
		time.Sleep(500 * time.Millisecond)
		mu.Lock()
		processedBy[data] = workerID
		mu.Unlock()
		return redisclient.TaskResult{WasRunSuccessful: true, ShouldBeRetried: false, Error: nil}
	})

	// Publisher: Add 5 messages.
	log.InfoContextf(ctx, "Publishing %d messages...", numberOfMessages)
	for i := 0; i < numberOfMessages; i++ {
		publisher.AddMessage(ctx, strconv.Itoa(i), "0")
	}

	// Publisher: Start the cleanup goroutine
	cleanupHandle := runWorkerInBackground(ctx, "cleanup-1", consumer.RunCleanup)
	// Subscribers: Start 3 workers.
	w1 := runWorkerInBackground(ctx, "worker-batch-1", consumer.StartWorker)
	w2 := runWorkerInBackground(ctx, "worker-batch-2", consumer.StartWorker)
	w3 := runWorkerInBackground(ctx, "worker-batch-3", consumer.StartWorker)

	log.InfoContextf(ctx, "Waiting for messages to be processed...")
	time.Sleep(10 * time.Second)

	// Cleanup: Stop workers.
	w1.cancel()
	w2.cancel()
	w3.cancel()
	cleanupHandle.cancel()
	w1.wg.Wait()
	w2.wg.Wait()
	w3.wg.Wait()
	cleanupHandle.wg.Wait()

	// Validation for testBatchRead
	var validationErrors []error
	mu.Lock()
	consumers := make(map[string]bool)
	for i := 0; i < numberOfMessages; i++ {
		workerID, ok := processedBy[strconv.Itoa(i)]
		if !ok {
			validationErrors = append(validationErrors, fmt.Errorf("message %d not processed", i))
		} else {
			consumers[workerID] = true
		}
	}
	if len(consumers) != 1 {
		validationErrors = append(validationErrors, fmt.Errorf("messages processed by %d workers, expected 1", len(consumers)))
	}
	mu.Unlock()

	pending, _ := consumer.FetchPendingCount(ctx)
	streamLen, _ := consumer.FetchStreamLen(ctx, streamName)
	dlqLen, _ := consumer.FetchStreamLen(ctx, dlqStreamName)

	// Cleanup: Stop workers.
	w1.cancel()
	w2.cancel()
	w3.cancel()
	cleanupHandle.cancel()
	w1.wg.Wait()
	w2.wg.Wait()
	w3.wg.Wait()
	cleanupHandle.wg.Wait()

	stats := StreamStats{PendingCount: pending, StreamLength: streamLen, DLQLength: dlqLen}
	if len(validationErrors) > 0 {
		return stats, errors.Join(validationErrors...)
	}

	return stats, nil
}
