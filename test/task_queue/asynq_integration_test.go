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

package taskqueue_test

import (
	"context"
	"flag"
	"fmt"
	"net"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/hibiken/asynq"
	"github.com/redis/go-redis/v9"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
	apierrors "k8s.io/apimachinery/pkg/api/errors"

	"xmanager_cloud/cmd/actuator/workunittaskhandler"
	tqclient "xmanager_cloud/task_queue/client"

	essmsgpb "xmanager_cloud/experiment_state_server/proto/messages_go_proto"
	actuatortaskpb "xmanager_cloud/task_queue/proto/task_go_proto"
)

var sharedRedisAddr string

func cleanRedis(t *testing.T, ctx context.Context) {
	t.Helper()
	rdb := redis.NewClient(&redis.Options{Addr: sharedRedisAddr})
	defer rdb.Close()
	if err := rdb.FlushDB(ctx).Err(); err != nil {
		t.Fatalf("failed to flush redis: %v", err)
	}
}

func TestMain(m *testing.M) {
	flag.Parse()
	if testing.Short() {
		os.Exit(m.Run())
	}
	os.Exit(runTests(m))
}

func runTests(m *testing.M) int {
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()

	redisC, addr, err := startRedisContainer(ctx)
	if err != nil {
		fmt.Printf("failed to start Redis testcontainer: %v\n", err)
		return 1
	}
	defer func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cleanupCancel()
		_ = redisC.Terminate(cleanupCtx)
	}()

	sharedRedisAddr = addr
	return m.Run()
}

func startRedisContainer(ctx context.Context) (testcontainers.Container, string, error) {
	req := testcontainers.ContainerRequest{
		Image:        "redis:7-alpine",
		ExposedPorts: []string{"6379/tcp"},
		WaitingFor:   wait.ForLog("Ready to accept connections").WithStartupTimeout(60 * time.Second),
	}
	c, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: req,
		Started:          true,
	})
	if err != nil {
		return nil, "", fmt.Errorf("failed to start redis container: %w", err)
	}

	host, err := c.Host(ctx)
	if err != nil {
		_ = c.Terminate(ctx)
		return nil, "", fmt.Errorf("failed to get container host: %w", err)
	}

	port, err := c.MappedPort(ctx, "6379/tcp")
	if err != nil {
		_ = c.Terminate(ctx)
		return nil, "", fmt.Errorf("failed to get mapped port: %w", err)
	}

	addr := net.JoinHostPort(host, port.Port())
	return c, addr, nil
}

type integrationK8sClient struct {
	mu           sync.Mutex
	lastPayload  *actuatortaskpb.ActuatorPayload
	createCalls  int32
	deleteCalls  int32
	createFn     func(ctx context.Context, payload *actuatortaskpb.ActuatorPayload) error
	deleteFn     func(ctx context.Context, payload *actuatortaskpb.ActuatorPayload) error
	createHookCh chan struct{}
}

func (k *integrationK8sClient) CreateWorkUnit(ctx context.Context, payload *actuatortaskpb.ActuatorPayload) error {
	atomic.AddInt32(&k.createCalls, 1)
	k.mu.Lock()
	k.lastPayload = payload
	fn := k.createFn
	k.mu.Unlock()

	if k.createHookCh != nil {
		select {
		case k.createHookCh <- struct{}{}:
		default:
		}
	}

	if fn != nil {
		return fn(ctx, payload)
	}
	return nil
}

func (k *integrationK8sClient) DeleteWorkUnit(ctx context.Context, payload *actuatortaskpb.ActuatorPayload) error {
	atomic.AddInt32(&k.deleteCalls, 1)
	k.mu.Lock()
	k.lastPayload = payload
	fn := k.deleteFn
	k.mu.Unlock()

	if fn != nil {
		return fn(ctx, payload)
	}
	return nil
}

func (k *integrationK8sClient) RestartWorkUnit(ctx context.Context, payload *actuatortaskpb.ActuatorPayload) error {
	return nil
}

func (k *integrationK8sClient) UpdateWorkUnit(ctx context.Context, payload *actuatortaskpb.ActuatorPayload) error {
	return nil
}

// 1. Happy Path: Task enqueued by client, processed by server, triggers mock K8s client.
func TestAsynqIntegration_HappyPath(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cleanRedis(t, ctx)

	client, err := tqclient.NewAsynqClient(sharedRedisAddr, "")
	if err != nil {
		t.Fatalf("failed to create asynq client: %v", err)
	}
	defer client.Close()

	if err := client.Ping(ctx); err != nil {
		t.Fatalf("redis ping failed: %v", err)
	}

	k8sClient := &integrationK8sClient{
		createHookCh: make(chan struct{}, 1),
	}
	handler, err := workunittaskhandler.NewAsynqHandler(workunittaskhandler.AsynqHandlerParams{
		K8sClient: k8sClient,
		ProjectID: "integration-test-proj",
	})
	if err != nil {
		t.Fatalf("failed to create asynq handler: %v", err)
	}

	server := asynq.NewServer(
		asynq.RedisClientOpt{Addr: sharedRedisAddr},
		asynq.Config{
			Concurrency: 5,
			Queues:      map[string]int{"critical": 6, "default": 3},
		},
	)
	if err := server.Start(handler); err != nil {
		t.Fatalf("failed to start asynq server: %v", err)
	}
	defer server.Shutdown()

	target := "experiments/101/workUnits/1/executions/1"
	workUnit := &essmsgpb.WorkUnit{
		Name:       "experiments/101/workUnits/1",
		CreateTime: timestamppb.Now(),
	}
	action := tqclient.CreateAction(essmsgpb.Priority_NORMAL, "user@test.local")
	opName := "experiments/101/workUnits/1/operations/00000000-0000-0000-0000-000000000001"

	if err := client.EnqueueWorkUnitTask(ctx, target, workUnit, action, opName); err != nil {
		t.Fatalf("EnqueueWorkUnitTask failed: %v", err)
	}

	select {
	case <-k8sClient.createHookCh:
		// Success: worker processed task
	case <-time.After(10 * time.Second):
		t.Fatal("timed out waiting for task to be processed by actuator worker")
	}

	k8sClient.mu.Lock()
	defer k8sClient.mu.Unlock()
	if k8sClient.lastPayload == nil {
		t.Fatal("expected lastPayload to be non-nil")
	}
	if k8sClient.lastPayload.GetTarget() != target {
		t.Errorf("got target %q, want %q", k8sClient.lastPayload.GetTarget(), target)
	}
}

// 2. Exponential Backoff: Transient K8s failure triggers automatic retry in Asynq.
func TestAsynqIntegration_ExponentialBackoff(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cleanRedis(t, ctx)

	client, err := tqclient.NewAsynqClient(sharedRedisAddr, "")
	if err != nil {
		t.Fatalf("failed to create asynq client: %v", err)
	}
	defer client.Close()

	attemptCh := make(chan struct{}, 5)
	k8sClient := &integrationK8sClient{
		createFn: func(ctx context.Context, payload *actuatortaskpb.ActuatorPayload) error {
			attemptCh <- struct{}{}
			return apierrors.NewTooManyRequests("rate limited by k8s api", 1)
		},
	}

	handler, err := workunittaskhandler.NewAsynqHandler(workunittaskhandler.AsynqHandlerParams{
		K8sClient: k8sClient,
		ProjectID: "integration-test-proj",
	})
	if err != nil {
		t.Fatalf("failed to create asynq handler: %v", err)
	}

	server := asynq.NewServer(
		asynq.RedisClientOpt{Addr: sharedRedisAddr},
		asynq.Config{
			Concurrency: 2,
			Queues:      map[string]int{"default": 1},
			RetryDelayFunc: func(n int, e error, t *asynq.Task) time.Duration {
				// Fast backoff for testing: 1s, 2s, 4s
				return time.Duration(1<<n) * time.Second
			},
		},
	)
	if err := server.Start(handler); err != nil {
		t.Fatalf("failed to start asynq server: %v", err)
	}
	defer server.Shutdown()

	target := "experiments/102/workUnits/2/executions/1"
	workUnit := &essmsgpb.WorkUnit{
		Name:       "experiments/102/workUnits/2",
		CreateTime: timestamppb.Now(),
	}
	action := tqclient.CreateAction(essmsgpb.Priority_NORMAL, "user@test.local")
	opName := "experiments/102/workUnits/2/operations/00000000-0000-0000-0000-000000000002"

	if err := client.EnqueueWorkUnitTask(ctx, target, workUnit, action, opName); err != nil {
		t.Fatalf("EnqueueWorkUnitTask failed: %v", err)
	}

	// Wait for the first attempt
	select {
	case <-attemptCh:
	case <-time.After(10 * time.Second):
		t.Fatal("timed out waiting for first attempt")
	}

	// Wait for the retry attempt after exponential backoff
	select {
	case <-attemptCh:
	case <-time.After(10 * time.Second):
		t.Fatal("timed out waiting for automatic retry with backoff")
	}

	calls := atomic.LoadInt32(&k8sClient.createCalls)
	if calls < 2 {
		t.Errorf("expected at least 2 attempts due to backoff retries, got %d", calls)
	}
}

// 3. Context Cancellation: Worker handler receives ctx.Done() without leaking goroutines.
func TestAsynqIntegration_ContextCancellation(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cleanRedis(t, ctx)

	client, err := tqclient.NewAsynqClient(sharedRedisAddr, "")
	if err != nil {
		t.Fatalf("failed to create asynq client: %v", err)
	}
	defer client.Close()

	ctxCanceledCh := make(chan struct{}, 1)
	k8sClient := &integrationK8sClient{
		createFn: func(taskCtx context.Context, payload *actuatortaskpb.ActuatorPayload) error {
			<-taskCtx.Done()
			ctxCanceledCh <- struct{}{}
			return taskCtx.Err()
		},
	}

	handler, err := workunittaskhandler.NewAsynqHandler(workunittaskhandler.AsynqHandlerParams{
		K8sClient: k8sClient,
		ProjectID: "integration-test-proj",
	})
	if err != nil {
		t.Fatalf("failed to create asynq handler: %v", err)
	}

	// Server configured with short task timeout to test active context cancellation
	server := asynq.NewServer(
		asynq.RedisClientOpt{Addr: sharedRedisAddr},
		asynq.Config{
			Concurrency: 1,
			Queues:      map[string]int{"default": 1},
		},
	)

	// Create custom task with 1s timeout to test timeout cancellation
	payload := &actuatortaskpb.ActuatorPayload{
		Kind:   actuatortaskpb.ActuatorPayload_WORK_UNIT_ACTUATION_REQUEST.Enum(),
		Target: proto.String("experiments/103/workUnits/3/executions/1"),
		Action: tqclient.CreateAction(essmsgpb.Priority_NORMAL, "user@test.local"),
		Metadata: &actuatortaskpb.Metadata{
			IdempotencyKey: proto.String("cancel-1"),
			OperationName:  proto.String("experiments/103/workUnits/3/operations/00000000-0000-0000-0000-000000000003"),
		},
	}
	payloadBytes, _ := proto.Marshal(payload)
	task := asynq.NewTask(tqclient.TaskTypeWorkUnitActuation, payloadBytes, asynq.Timeout(1*time.Second))

	asynqClient := asynq.NewClient(asynq.RedisClientOpt{Addr: sharedRedisAddr})
	defer asynqClient.Close()

	if _, err := asynqClient.EnqueueContext(ctx, task); err != nil {
		t.Fatalf("failed to enqueue task: %v", err)
	}

	if err := server.Start(handler); err != nil {
		t.Fatalf("failed to start asynq server: %v", err)
	}
	defer server.Shutdown()

	select {
	case <-ctxCanceledCh:
		// Success: worker unblocked and received ctx.Done()
	case <-time.After(10 * time.Second):
		t.Fatal("timed out waiting for worker context cancellation")
	}
}

// 4. Deduplication: Rapid identical enqueue calls within uniqueness window result in only one queued task.
func TestAsynqIntegration_Deduplication(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cleanRedis(t, ctx)

	client, err := tqclient.NewAsynqClient(sharedRedisAddr, "")
	if err != nil {
		t.Fatalf("failed to create asynq client: %v", err)
	}
	defer client.Close()

	target := "experiments/104/workUnits/4/executions/1"
	fixedTime := timestamppb.New(time.Date(2026, 2, 1, 12, 0, 0, 0, time.UTC))
	workUnit := &essmsgpb.WorkUnit{
		Name:       "experiments/104/workUnits/4",
		CreateTime: fixedTime,
	}
	action := tqclient.CreateAction(essmsgpb.Priority_NORMAL, "user@test.local")
	opName := "experiments/104/workUnits/4/operations/00000000-0000-0000-0000-000000000004"

	// First enqueue
	if err := client.EnqueueWorkUnitTask(ctx, target, workUnit, action, opName); err != nil {
		t.Fatalf("first enqueue failed: %v", err)
	}

	// Immediate second enqueue with identical parameters
	if err := client.EnqueueWorkUnitTask(ctx, target, workUnit, action, opName); err != nil {
		t.Fatalf("second enqueue failed: %v", err)
	}

	inspector := asynq.NewInspector(asynq.RedisClientOpt{Addr: sharedRedisAddr})
	defer inspector.Close()

	pending, err := inspector.ListPendingTasks(tqclient.QueueDefault)
	if err != nil {
		t.Fatalf("ListPendingTasks failed: %v", err)
	}

	var matching int
	for _, task := range pending {
		var p actuatortaskpb.ActuatorPayload
		if err := proto.Unmarshal(task.Payload, &p); err == nil {
			if p.GetTarget() == target {
				matching++
			}
		}
	}

	if matching != 1 {
		t.Fatalf("expected exactly 1 task enqueued due to deduplication, got %d", matching)
	}
}

// 5. Dead-Letter Archival: Non-retryable errors move the task to Archived state immediately.
func TestAsynqIntegration_DeadLetterArchival(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cleanRedis(t, ctx)

	client, err := tqclient.NewAsynqClient(sharedRedisAddr, "")
	if err != nil {
		t.Fatalf("failed to create asynq client: %v", err)
	}
	defer client.Close()

	target := "experiments/105/workUnits/5/executions/1"
	processedCh := make(chan struct{}, 1)
	nonRetryableErr := apierrors.NewBadRequest("invalid jobset specification schema")
	k8sClient := &integrationK8sClient{
		createFn: func(ctx context.Context, payload *actuatortaskpb.ActuatorPayload) error {
			if payload.GetTarget() == target {
				defer func() { processedCh <- struct{}{} }()
			}
			return nonRetryableErr
		},
	}

	handler, err := workunittaskhandler.NewAsynqHandler(workunittaskhandler.AsynqHandlerParams{
		K8sClient: k8sClient,
		ProjectID: "integration-test-proj",
	})
	if err != nil {
		t.Fatalf("failed to create asynq handler: %v", err)
	}

	server := asynq.NewServer(
		asynq.RedisClientOpt{Addr: sharedRedisAddr},
		asynq.Config{
			Concurrency: 1,
			Queues:      map[string]int{"default": 1},
		},
	)
	if err := server.Start(handler); err != nil {
		t.Fatalf("failed to start asynq server: %v", err)
	}
	defer server.Shutdown()

	workUnit := &essmsgpb.WorkUnit{
		Name:       "experiments/105/workUnits/5",
		CreateTime: timestamppb.Now(),
	}
	action := tqclient.CreateAction(essmsgpb.Priority_NORMAL, "user@test.local")
	opName := "experiments/105/workUnits/5/operations/00000000-0000-0000-0000-000000000005"

	if err := client.EnqueueWorkUnitTask(ctx, target, workUnit, action, opName); err != nil {
		t.Fatalf("EnqueueWorkUnitTask failed: %v", err)
	}

	select {
	case <-processedCh:
	case <-time.After(10 * time.Second):
		t.Fatal("timed out waiting for task processing")
	}

	// Verify task was immediately moved to Archived state (dead-letter)
	inspector := asynq.NewInspector(asynq.RedisClientOpt{Addr: sharedRedisAddr})
	defer inspector.Close()

	var foundArchived bool
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		archived, err := inspector.ListArchivedTasks(tqclient.QueueDefault)
		if err == nil {
			for _, task := range archived {
				var p actuatortaskpb.ActuatorPayload
				if err := proto.Unmarshal(task.Payload, &p); err == nil {
					if p.GetTarget() == target {
						foundArchived = true
						break
					}
				}
			}
		}
		if foundArchived {
			break
		}
		time.Sleep(100 * time.Millisecond)
	}

	if !foundArchived {
		t.Fatalf("expected task for target %q to be in Archived (DLQ) state, but was not found in archived tasks", target)
	}
}
