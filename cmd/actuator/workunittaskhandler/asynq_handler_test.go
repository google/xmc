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

package workunittaskhandler

import (
	"context"
	"errors"
	"testing"

	"github.com/hibiken/asynq"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime/schema"
	actuatortaskpb "xmanager_cloud/task_queue/proto/task_go_proto"
)

type recordingK8sClient struct {
	lastPayload *actuatortaskpb.ActuatorPayload
	createErr   error
	deleteErr   error
	restartErr  error
	updateErr   error
}

func (r *recordingK8sClient) CreateWorkUnit(ctx context.Context, payload *actuatortaskpb.ActuatorPayload) error {
	r.lastPayload = payload
	return r.createErr
}

func (r *recordingK8sClient) DeleteWorkUnit(ctx context.Context, payload *actuatortaskpb.ActuatorPayload) error {
	r.lastPayload = payload
	return r.deleteErr
}

func (r *recordingK8sClient) RestartWorkUnit(ctx context.Context, payload *actuatortaskpb.ActuatorPayload) error {
	r.lastPayload = payload
	return r.restartErr
}

func (r *recordingK8sClient) UpdateWorkUnit(ctx context.Context, payload *actuatortaskpb.ActuatorPayload) error {
	r.lastPayload = payload
	return r.updateErr
}

func makeValidPayload(action *actuatortaskpb.Action) *actuatortaskpb.ActuatorPayload {
	target := "experiments/1/workUnits/2/executions/3"
	return &actuatortaskpb.ActuatorPayload{
		Kind:   actuatortaskpb.ActuatorPayload_WORK_UNIT_ACTUATION_REQUEST.Enum(),
		Target: &target,
		Action: action,
		Metadata: &actuatortaskpb.Metadata{
			IdempotencyKey: proto.String("idemp-1"),
			OperationName:  proto.String("op-1"),
			TraceId:        proto.String("trace-1"),
		},
	}
}

func TestNewAsynqHandler_Validation(t *testing.T) {
	tests := []struct {
		name      string
		params    AsynqHandlerParams
		wantError bool
	}{
		{
			name: "nil k8s client",
			params: AsynqHandlerParams{
				K8sClient: nil,
				ProjectID: "test-proj",
			},
			wantError: true,
		},
		{
			name: "empty project ID",
			params: AsynqHandlerParams{
				K8sClient: &recordingK8sClient{},
				ProjectID: "",
			},
			wantError: true,
		},
		{
			name: "valid params",
			params: AsynqHandlerParams{
				K8sClient: &recordingK8sClient{},
				ProjectID: "test-proj",
			},
			wantError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := NewAsynqHandler(tt.params)
			if (err != nil) != tt.wantError {
				t.Fatalf("NewAsynqHandler() error = %v, wantError = %v", err, tt.wantError)
			}
		})
	}
}

func TestProcessTask_ContextCancelled(t *testing.T) {
	client := &recordingK8sClient{}
	handler, _ := NewAsynqHandler(AsynqHandlerParams{
		K8sClient: client,
		ProjectID: "test-proj",
	})

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	task := asynq.NewTask(AsynqTaskTypeWorkUnitActuation, []byte("ignored"))
	err := handler.ProcessTask(ctx, task)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got: %v", err)
	}
}

func TestProcessTask_UnsupportedTaskType(t *testing.T) {
	client := &recordingK8sClient{}
	handler, _ := NewAsynqHandler(AsynqHandlerParams{
		K8sClient: client,
		ProjectID: "test-proj",
	})

	task := asynq.NewTask("unsupported_type", []byte("foo"))
	err := handler.ProcessTask(context.Background(), task)
	if !errors.Is(err, asynq.SkipRetry) {
		t.Fatalf("expected asynq.SkipRetry, got: %v", err)
	}
}

func TestProcessTask_InvalidPayload(t *testing.T) {
	client := &recordingK8sClient{}
	handler, _ := NewAsynqHandler(AsynqHandlerParams{
		K8sClient: client,
		ProjectID: "test-proj",
	})

	task := asynq.NewTask(AsynqTaskTypeWorkUnitActuation, []byte("not a valid proto"))
	err := handler.ProcessTask(context.Background(), task)
	if !errors.Is(err, asynq.SkipRetry) {
		t.Fatalf("expected asynq.SkipRetry, got: %v", err)
	}
}

func TestProcessTask_MissingTargetOrAction(t *testing.T) {
	client := &recordingK8sClient{}
	handler, _ := NewAsynqHandler(AsynqHandlerParams{
		K8sClient: client,
		ProjectID: "test-proj",
	})

	// Missing target and action
	payload := &actuatortaskpb.ActuatorPayload{}
	bytes, _ := proto.Marshal(payload)
	task := asynq.NewTask(AsynqTaskTypeWorkUnitActuation, bytes)

	err := handler.ProcessTask(context.Background(), task)
	if !errors.Is(err, asynq.SkipRetry) {
		t.Fatalf("expected asynq.SkipRetry, got: %v", err)
	}
}

func TestProcessTask_CreateSuccess(t *testing.T) {
	client := &recordingK8sClient{}
	handler, _ := NewAsynqHandler(AsynqHandlerParams{
		K8sClient: client,
		ProjectID: "test-proj",
	})

	action := &actuatortaskpb.Action{
		Action: &actuatortaskpb.Action_Create{
			Create: &actuatortaskpb.CreateAction{Author: proto.String("tester")},
		},
	}
	payload := makeValidPayload(action)
	bytes, _ := proto.Marshal(payload)
	task := asynq.NewTask(AsynqTaskTypeWorkUnitActuation, bytes)

	if err := handler.ProcessTask(context.Background(), task); err != nil {
		t.Fatalf("ProcessTask() unexpected error: %v", err)
	}

	if client.lastPayload == nil {
		t.Fatal("expected CreateWorkUnit to be called, but lastPayload is nil")
	}
	if client.lastPayload.GetTarget() != payload.GetTarget() {
		t.Errorf("got target %q, want %q", client.lastPayload.GetTarget(), payload.GetTarget())
	}
}

func TestProcessTask_CreateAlreadyExistsIsIdempotent(t *testing.T) {
	client := &recordingK8sClient{
		createErr: apierrors.NewAlreadyExists(schema.GroupResource{Resource: "jobsets"}, "jobset-1"),
	}
	handler, _ := NewAsynqHandler(AsynqHandlerParams{
		K8sClient: client,
		ProjectID: "test-proj",
	})

	action := &actuatortaskpb.Action{
		Action: &actuatortaskpb.Action_Create{
			Create: &actuatortaskpb.CreateAction{},
		},
	}
	payload := makeValidPayload(action)
	bytes, _ := proto.Marshal(payload)
	task := asynq.NewTask(AsynqTaskTypeWorkUnitActuation, bytes)

	err := handler.ProcessTask(context.Background(), task)
	if err != nil {
		t.Fatalf("expected nil error on AlreadyExists (idempotent), got: %v", err)
	}
}

func TestProcessTask_DeleteNotFoundIsIdempotent(t *testing.T) {
	client := &recordingK8sClient{
		deleteErr: apierrors.NewNotFound(schema.GroupResource{Resource: "jobsets"}, "jobset-1"),
	}
	handler, _ := NewAsynqHandler(AsynqHandlerParams{
		K8sClient: client,
		ProjectID: "test-proj",
	})

	action := &actuatortaskpb.Action{
		Action: &actuatortaskpb.Action_Delete{
			Delete: &actuatortaskpb.DeleteAction{},
		},
	}
	payload := makeValidPayload(action)
	bytes, _ := proto.Marshal(payload)
	task := asynq.NewTask(AsynqTaskTypeWorkUnitActuation, bytes)

	err := handler.ProcessTask(context.Background(), task)
	if err != nil {
		t.Fatalf("expected nil error on NotFound for delete (idempotent), got: %v", err)
	}
}

func TestProcessTask_TransientErrorRetried(t *testing.T) {
	transientErr := apierrors.NewTooManyRequests("rate limited", 5)
	client := &recordingK8sClient{
		createErr: transientErr,
	}
	handler, _ := NewAsynqHandler(AsynqHandlerParams{
		K8sClient: client,
		ProjectID: "test-proj",
	})

	action := &actuatortaskpb.Action{
		Action: &actuatortaskpb.Action_Create{
			Create: &actuatortaskpb.CreateAction{},
		},
	}
	payload := makeValidPayload(action)
	bytes, _ := proto.Marshal(payload)
	task := asynq.NewTask(AsynqTaskTypeWorkUnitActuation, bytes)

	err := handler.ProcessTask(context.Background(), task)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if errors.Is(err, asynq.SkipRetry) {
		t.Fatal("transient error should NOT wrap asynq.SkipRetry")
	}
	if !errors.Is(err, transientErr) {
		t.Fatalf("expected error to wrap transient error, got: %v", err)
	}
}

func TestProcessTask_NonRetryableErrorSkipped(t *testing.T) {
	nonRetryableErr := apierrors.NewBadRequest("invalid spec")
	client := &recordingK8sClient{
		createErr: nonRetryableErr,
	}
	handler, _ := NewAsynqHandler(AsynqHandlerParams{
		K8sClient: client,
		ProjectID: "test-proj",
	})

	action := &actuatortaskpb.Action{
		Action: &actuatortaskpb.Action_Create{
			Create: &actuatortaskpb.CreateAction{},
		},
	}
	payload := makeValidPayload(action)
	bytes, _ := proto.Marshal(payload)
	task := asynq.NewTask(AsynqTaskTypeWorkUnitActuation, bytes)

	err := handler.ProcessTask(context.Background(), task)
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if !errors.Is(err, asynq.SkipRetry) {
		t.Fatal("non-retryable error MUST wrap asynq.SkipRetry")
	}
}

func TestProcessTask_ProtojsonFallback(t *testing.T) {
	client := &recordingK8sClient{}
	handler, _ := NewAsynqHandler(AsynqHandlerParams{
		K8sClient: client,
		ProjectID: "test-proj",
	})

	action := &actuatortaskpb.Action{
		Action: &actuatortaskpb.Action_Restart{
			Restart: &actuatortaskpb.RestartAction{},
		},
	}
	payload := makeValidPayload(action)
	jsonBytes, err := protojson.Marshal(payload)
	if err != nil {
		t.Fatalf("failed to marshal protojson: %v", err)
	}
	task := asynq.NewTask(AsynqTaskTypeWorkUnitActuation, jsonBytes)

	if err := handler.ProcessTask(context.Background(), task); err != nil {
		t.Fatalf("ProcessTask() with protojson failed: %v", err)
	}
	if client.lastPayload == nil {
		t.Fatal("expected RestartWorkUnit to be called, got nil")
	}
}
