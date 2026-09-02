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

package client

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/hibiken/asynq"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/testing/protocmp"
	"google.golang.org/protobuf/types/known/timestamppb"

	essmsgpb "xmanager_cloud/experiment_state_server/proto/messages_go_proto"
	actuatortaskpb "xmanager_cloud/task_queue/proto/task_go_proto"
)

type mockTaskEnqueuer struct {
	lastTask   *asynq.Task
	lastOpts   []asynq.Option
	enqueueErr error
	closeErr   error
	pingErr    error
}

func (m *mockTaskEnqueuer) EnqueueContext(ctx context.Context, task *asynq.Task, opts ...asynq.Option) (*asynq.TaskInfo, error) {
	m.lastTask = task
	m.lastOpts = opts
	if m.enqueueErr != nil {
		return nil, m.enqueueErr
	}
	return &asynq.TaskInfo{ID: "mock-task-id", Queue: "mock-queue"}, nil
}

func (m *mockTaskEnqueuer) Close() error {
	return m.closeErr
}

func (m *mockTaskEnqueuer) Ping() error {
	return m.pingErr
}

func TestNewAsynqClient_EmptyAddr(t *testing.T) {
	_, err := NewAsynqClient("", "")
	if err == nil {
		t.Fatal("expected error on empty redis address, got nil")
	}
}

func TestEnqueueWorkUnitTask_Validation(t *testing.T) {
	ctx := context.Background()
	target := "experiments/1/workUnits/2/executions/3"
	action := CreateAction(essmsgpb.Priority_NORMAL, "tester")

	tests := []struct {
		name      string
		client    *AsynqClient
		target    string
		action    *actuatortaskpb.Action
		wantError bool
	}{
		{
			name:      "nil enqueuer",
			client:    &AsynqClient{enqueuer: nil},
			target:    target,
			action:    action,
			wantError: true,
		},
		{
			name:      "empty target",
			client:    NewAsynqClientWithEnqueuer(&mockTaskEnqueuer{}),
			target:    "",
			action:    action,
			wantError: true,
		},
		{
			name:      "nil action",
			client:    NewAsynqClientWithEnqueuer(&mockTaskEnqueuer{}),
			target:    target,
			action:    nil,
			wantError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.client.EnqueueWorkUnitTask(ctx, tt.target, nil, tt.action, "op")
			if (err != nil) != tt.wantError {
				t.Fatalf("EnqueueWorkUnitTask() error = %v, wantError = %v", err, tt.wantError)
			}
		})
	}
}

func TestEnqueueWorkUnitTask_CreateRoutesToDefaultQueue(t *testing.T) {
	ctx := context.Background()
	mock := &mockTaskEnqueuer{}
	client := NewAsynqClientWithEnqueuer(mock)

	fixedTime := timestamppb.New(time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC))
	workUnit := &essmsgpb.WorkUnit{
		Name:       "experiments/100/workUnits/200",
		CreateTime: fixedTime,
	}
	action := CreateAction(essmsgpb.Priority_NORMAL, "author@xmc.local")
	target := "experiments/100/workUnits/200/executions/300"
	operationName := "test-create-op"

	if err := client.EnqueueWorkUnitTask(ctx, target, workUnit, action, operationName); err != nil {
		t.Fatalf("EnqueueWorkUnitTask() unexpected error: %v", err)
	}

	if mock.lastTask == nil {
		t.Fatal("expected task to be enqueued, got nil")
	}
	if got := mock.lastTask.Type(); got != TaskTypeWorkUnitActuation {
		t.Errorf("expected task type %q, got %q", TaskTypeWorkUnitActuation, got)
	}

	var payload actuatortaskpb.ActuatorPayload
	if err := proto.Unmarshal(mock.lastTask.Payload(), &payload); err != nil {
		t.Fatalf("failed to unmarshal payload: %v", err)
	}

	if payload.GetTarget() != target {
		t.Errorf("target mismatch: got %q, want %q", payload.GetTarget(), target)
	}
	if payload.GetMetadata().GetOperationName() != operationName {
		t.Errorf("operation name mismatch: got %q, want %q", payload.GetMetadata().GetOperationName(), operationName)
	}
	if payload.GetMetadata().GetIdempotencyKey() != target+":CREATE" {
		t.Errorf("idempotency key mismatch: got %q, want %q", payload.GetMetadata().GetIdempotencyKey(), target+":CREATE")
	}
	if diff := cmp.Diff(payload.GetAction(), action, protocmp.Transform()); diff != "" {
		t.Errorf("action mismatch (-got +want):\n%s", diff)
	}
}

func TestEnqueueWorkUnitTask_DeleteRoutesToCriticalQueue(t *testing.T) {
	ctx := context.Background()
	mock := &mockTaskEnqueuer{}
	client := NewAsynqClientWithEnqueuer(mock)

	action := DeleteAction()
	target := "experiments/100/workUnits/200/executions/300"

	if err := client.EnqueueWorkUnitTask(ctx, target, nil, action, "op-delete"); err != nil {
		t.Fatalf("EnqueueWorkUnitTask() unexpected error: %v", err)
	}

	if mock.lastTask == nil {
		t.Fatal("expected task to be enqueued, got nil")
	}

	var payload actuatortaskpb.ActuatorPayload
	if err := proto.Unmarshal(mock.lastTask.Payload(), &payload); err != nil {
		t.Fatalf("failed to unmarshal payload: %v", err)
	}
	if payload.GetMetadata().GetIdempotencyKey() != target+":DELETE" {
		t.Errorf("idempotency key mismatch: got %q, want %q", payload.GetMetadata().GetIdempotencyKey(), target+":DELETE")
	}
}

func TestEnqueueWorkUnitTask_DeduplicationHandledIdempotently(t *testing.T) {
	ctx := context.Background()
	mock := &mockTaskEnqueuer{
		enqueueErr: asynq.ErrDuplicateTask,
	}
	client := NewAsynqClientWithEnqueuer(mock)

	action := DeleteAction()
	target := "experiments/100/workUnits/200/executions/300"

	err := client.EnqueueWorkUnitTask(ctx, target, nil, action, "op-dup")
	if err != nil {
		t.Fatalf("expected nil error on duplicate task (idempotent), got: %v", err)
	}
}

func TestEnqueueWorkUnitTask_EnqueuerErrorPropagated(t *testing.T) {
	ctx := context.Background()
	expectedErr := errors.New("redis connection refused")
	mock := &mockTaskEnqueuer{
		enqueueErr: expectedErr,
	}
	client := NewAsynqClientWithEnqueuer(mock)

	action := DeleteAction()
	target := "experiments/100/workUnits/200/executions/300"

	err := client.EnqueueWorkUnitTask(ctx, target, nil, action, "op-err")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !errors.Is(err, expectedErr) {
		t.Fatalf("expected error to wrap %v, got: %v", expectedErr, err)
	}
}

func TestCloseAndPing(t *testing.T) {
	ctx := context.Background()
	mock := &mockTaskEnqueuer{
		closeErr: errors.New("close error"),
		pingErr:  errors.New("ping error"),
	}
	client := NewAsynqClientWithEnqueuer(mock)

	if err := client.Close(); err == nil || err.Error() != "close error" {
		t.Errorf("expected close error, got: %v", err)
	}
	if err := client.Ping(ctx); err == nil || err.Error() != "ping error" {
		t.Errorf("expected ping error, got: %v", err)
	}
	if err := client.PingRedis(ctx); err == nil || err.Error() != "ping error" {
		t.Errorf("expected ping error on PingRedis, got: %v", err)
	}
}
