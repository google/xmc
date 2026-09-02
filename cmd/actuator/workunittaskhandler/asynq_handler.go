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

// Package workunittaskhandler handles incoming actuation tasks.
package workunittaskhandler

import (
	"context"
	"errors"
	"fmt"
	"log/slog"

	"github.com/google/uuid"
	"github.com/hibiken/asynq"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"xmanager_cloud/logging/logging"
	actuatortaskpb "xmanager_cloud/task_queue/proto/task_go_proto"
)

const (
	// AsynqTaskTypeWorkUnitActuation is the task type for WorkUnit actuation tasks.
	AsynqTaskTypeWorkUnitActuation = "task:actuate_workunit"
)

// AsynqHandlerParams holds the dependencies required to instantiate an AsynqHandler.
type AsynqHandlerParams struct {
	K8sClient K8sClient
	ProjectID string
}

// AsynqHandler processes incoming WorkUnit actuation tasks from Asynq queues.
type AsynqHandler struct {
	k8sClient K8sClient
	projectID string
}

var _ asynq.Handler = (*AsynqHandler)(nil)

// NewAsynqHandler creates a new AsynqHandler instance.
func NewAsynqHandler(p AsynqHandlerParams) (*AsynqHandler, error) {
	if p.K8sClient == nil {
		return nil, fmt.Errorf("k8s client is required")
	}
	if p.ProjectID == "" {
		return nil, fmt.Errorf("project ID is required")
	}
	return &AsynqHandler{
		k8sClient: p.K8sClient,
		projectID: p.ProjectID,
	}, nil
}

// ProcessTask processes an incoming Asynq actuation task.
func (h *AsynqHandler) ProcessTask(ctx context.Context, task *asynq.Task) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	if task.Type() != AsynqTaskTypeWorkUnitActuation {
		logging.Warn(ctx, "Unsupported task type", "type", task.Type())
		return fmt.Errorf("%w: unsupported task type %q", asynq.SkipRetry, task.Type())
	}

	payload, err := parseTaskPayload(task.Payload())
	if err != nil {
		logging.Warn(ctx, "Failed to parse task payload", "error", err)
		ctMessagesFailed.WithLabelValues().Inc()
		return fmt.Errorf("%w: failed to parse actuator payload: %v", asynq.SkipRetry, err)
	}

	if payload.GetTarget() == "" || payload.GetAction() == nil {
		logging.Warn(ctx, "Invalid actuator payload: target and action are required")
		ctMessagesFailed.WithLabelValues().Inc()
		return fmt.Errorf("%w: invalid payload: target and action are required", asynq.SkipRetry)
	}

	ctx = setupLoggingContext(ctx, h.projectID, payload)

	if err := ctx.Err(); err != nil {
		return err
	}

	ctMessagesProcessed.WithLabelValues().Inc()

	if err := h.dispatchAction(ctx, payload); err != nil {
		return h.handleActuationError(ctx, payload.GetTarget(), err)
	}

	return nil
}

func parseTaskPayload(raw []byte) (*actuatortaskpb.ActuatorPayload, error) {
	var payload actuatortaskpb.ActuatorPayload
	if err := proto.Unmarshal(raw, &payload); err == nil {
		return &payload, nil
	}
	if err := protojson.Unmarshal(raw, &payload); err == nil {
		return &payload, nil
	}
	return nil, fmt.Errorf("payload is neither binary proto nor protojson")
}

func setupLoggingContext(ctx context.Context, projectID string, payload *actuatortaskpb.ActuatorPayload) context.Context {
	logger := logging.NewGCPLogger(slog.LevelInfo, projectID)
	ctx = logging.WithLogger(ctx, logger)
	ctx = logging.WithWorkUnitExecutionName(ctx, payload.GetTarget())

	traceID := payload.GetMetadata().GetTraceId()
	if traceID == "" {
		traceID = uuid.NewString()
	}
	ctx = logging.InjectOperation(ctx, traceID)

	operationName := payload.GetMetadata().GetOperationName()
	if operationName != "" {
		ctx = logging.WithOperationName(ctx, operationName)
	}
	return ctx
}

func (h *AsynqHandler) dispatchAction(ctx context.Context, payload *actuatortaskpb.ActuatorPayload) error {
	action := payload.GetAction()
	switch {
	case action.GetCreate() != nil:
		logging.Info(ctx, "Received create request for work unit")
		if err := h.k8sClient.CreateWorkUnit(ctx, payload); err != nil {
			if apierrors.IsAlreadyExists(err) {
				logging.Info(ctx, "JobSet already exists for target, treating as idempotent success", "target", payload.GetTarget())
				return nil
			}
			return err
		}
		workUnitsCreated.WithLabelValues().Inc()
		return nil
	case action.GetDelete() != nil:
		logging.Info(ctx, "Received delete request for work unit")
		if err := h.k8sClient.DeleteWorkUnit(ctx, payload); err != nil {
			if apierrors.IsNotFound(err) {
				logging.Info(ctx, "JobSet already deleted for target, treating as idempotent success", "target", payload.GetTarget())
				return nil
			}
			return err
		}
		return nil
	case action.GetRestart() != nil:
		logging.Info(ctx, "Received restart request for work unit")
		return h.k8sClient.RestartWorkUnit(ctx, payload)
	case action.GetUpdate() != nil:
		logging.Info(ctx, "Received update request for work unit")
		return h.k8sClient.UpdateWorkUnit(ctx, payload)
	default:
		logging.Warn(ctx, "Unsupported action type", "action", action)
		return apierrors.NewBadRequest("unsupported action type")
	}
}

func (h *AsynqHandler) handleActuationError(ctx context.Context, target string, err error) error {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		logging.Warn(ctx, "Actuation canceled by context timeout", "target", target, "error", err)
		return err
	}
	ctMessagesFailed.WithLabelValues().Inc()
	if isRetryableError(err) {
		logging.Warn(ctx, "Transient error processing actuation task, will retry", "target", target, "error", err)
		return err
	}
	logging.Warn(ctx, "Non-retryable error processing actuation task, skipping retry and archiving", "target", target, "error", err)
	return fmt.Errorf("%w: %v", asynq.SkipRetry, err)
}
