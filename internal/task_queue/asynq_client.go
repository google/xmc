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
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/hibiken/asynq"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
	"xmanager_cloud/logging/logging"

	essmsgpb "xmanager_cloud/experiment_state_server/proto/messages_go_proto"
	actuatortaskpb "xmanager_cloud/task_queue/proto/task_go_proto"
)

const (
	// TaskTypeWorkUnitActuation is the asynq task type for WorkUnit actuation requests.
	TaskTypeWorkUnitActuation = "task:actuate_workunit"

	// QueueCritical is the high-priority queue for deletion and cancellation tasks.
	QueueCritical = "critical"

	// QueueDefault is the standard priority queue for creation and update tasks.
	QueueDefault = "default"

	// QueueLow is the low-priority queue.
	QueueLow = "low"

	// DefaultMaxRetry is the maximum retry limit for actuation tasks.
	DefaultMaxRetry = 3

	// DefaultUniqueTTL is the deduplication window for identical WorkUnit actuation tasks.
	DefaultUniqueTTL = 5 * time.Minute
)

// TaskEnqueuer defines the underlying enqueue operations required from Asynq.
type TaskEnqueuer interface {
	EnqueueContext(ctx context.Context, task *asynq.Task, opts ...asynq.Option) (*asynq.TaskInfo, error)
	Close() error
	Ping() error
}

// AsynqClient enqueues WorkUnit actuation tasks to Redis via Asynq.
type AsynqClient struct {
	enqueuer TaskEnqueuer
}

// Ensure AsynqClient satisfies the contract.
var _ TaskEnqueuer = (*asynq.Client)(nil)

// NewAsynqClient creates an AsynqClient connected to the specified Redis address.
func NewAsynqClient(redisAddr string, redisPassword string) (*AsynqClient, error) {
	if redisAddr == "" {
		return nil, fmt.Errorf("redis address cannot be empty")
	}
	client := asynq.NewClient(asynq.RedisClientOpt{
		Addr:     redisAddr,
		Password: redisPassword,
	})
	return NewAsynqClientWithEnqueuer(client), nil
}

// NewAsynqClientWithEnqueuer creates an AsynqClient using an existing TaskEnqueuer.
func NewAsynqClientWithEnqueuer(enqueuer TaskEnqueuer) *AsynqClient {
	return &AsynqClient{enqueuer: enqueuer}
}

// EnqueueWorkUnitTask creates and enqueues an actuation task.
func (c *AsynqClient) EnqueueWorkUnitTask(ctx context.Context, target string, workUnit *essmsgpb.WorkUnit, action *actuatortaskpb.Action, operationName string) error {
	if c.enqueuer == nil {
		return fmt.Errorf("task enqueuer is nil")
	}
	if target == "" {
		return fmt.Errorf("target cannot be empty")
	}
	if action == nil {
		return fmt.Errorf("action cannot be nil")
	}

	payload, err := buildActuatorPayload(ctx, target, workUnit, action, operationName)
	if err != nil {
		return fmt.Errorf("failed to build actuator payload: %w", err)
	}

	payloadBytes, err := proto.Marshal(payload)
	if err != nil {
		return fmt.Errorf("failed to marshal actuator payload: %w", err)
	}

	queue := resolveQueue(action)
	task := asynq.NewTask(TaskTypeWorkUnitActuation, payloadBytes)
	opts := []asynq.Option{
		asynq.Queue(queue),
		asynq.MaxRetry(DefaultMaxRetry),
		asynq.Unique(DefaultUniqueTTL),
	}

	_, err = c.enqueuer.EnqueueContext(ctx, task, opts...)
	if err != nil {
		if errors.Is(err, asynq.ErrDuplicateTask) {
			logging.Info(ctx, "Actuation task deduplicated by asynq uniqueness window", "target", target, "queue", queue)
			return nil
		}
		return fmt.Errorf("failed to enqueue asynq task: %w", err)
	}
	return nil
}

// Close closes the underlying task enqueuer.
func (c *AsynqClient) Close() error {
	if c.enqueuer == nil {
		return nil
	}
	return c.enqueuer.Close()
}

// Ping checks the health of the task enqueuer connection.
func (c *AsynqClient) Ping(ctx context.Context) error {
	if c.enqueuer == nil {
		return fmt.Errorf("task enqueuer is nil")
	}
	return c.enqueuer.Ping()
}

// PingRedis provides backward compatibility with the existing client interface.
func (c *AsynqClient) PingRedis(ctx context.Context) error {
	return c.Ping(ctx)
}

func buildActuatorPayload(ctx context.Context, target string, workUnit *essmsgpb.WorkUnit, action *actuatortaskpb.Action, operationName string) (*actuatortaskpb.ActuatorPayload, error) {
	actionType := determineActionType(action)
	idempotencyKey := fmt.Sprintf("%s:%s", target, actionType)

	traceID, ok := logging.OperationID(ctx)
	if !ok {
		traceID = uuid.NewSHA1(uuid.NameSpaceOID, []byte(idempotencyKey)).String()
	}

	var createTime *timestamppb.Timestamp
	if workUnit != nil && workUnit.GetCreateTime() != nil {
		createTime = workUnit.GetCreateTime()
	}

	metadata := &actuatortaskpb.Metadata{
		IdempotencyKey: proto.String(idempotencyKey),
		OperationName:  proto.String(operationName),
		TraceId:        proto.String(traceID),
	}

	return &actuatortaskpb.ActuatorPayload{
		Kind:       actuatortaskpb.ActuatorPayload_WORK_UNIT_ACTUATION_REQUEST.Enum(),
		Target:     proto.String(target),
		WorkUnit:   workUnit,
		Action:     action,
		Metadata:   metadata,
		CreateTime: createTime,
	}, nil
}

func resolveQueue(action *actuatortaskpb.Action) string {
	if action.GetDelete() != nil {
		return QueueCritical
	}
	return QueueDefault
}

func determineActionType(action *actuatortaskpb.Action) string {
	switch action.GetAction().(type) {
	case *actuatortaskpb.Action_Create:
		return "CREATE"
	case *actuatortaskpb.Action_Delete:
		return "DELETE"
	case *actuatortaskpb.Action_Restart:
		return "RESTART"
	case *actuatortaskpb.Action_Update:
		return "UPDATE"
	default:
		return "UNKNOWN"
	}
}

// DefaultSignatureTTLSeconds is preserved for configuration compatibility.
const DefaultSignatureTTLSeconds = 3600

// New creates a new AsynqClient instance connecting to the specified Redis address.
func New(redisAddr string, password string) (*AsynqClient, error) {
	return NewAsynqClient(redisAddr, password)
}

// CreateAction creates an action for the Actuator to create a WorkUnit.
func CreateAction(experimentPriority essmsgpb.Priority, author string) *actuatortaskpb.Action {
	return &actuatortaskpb.Action{
		Action: &actuatortaskpb.Action_Create{
			Create: &actuatortaskpb.CreateAction{
				ExperimentPriority: experimentPriority.Enum(),
				Author:             proto.String(author),
			},
		},
	}
}

// DeleteAction creates an action for the Actuator to delete a WorkUnit.
func DeleteAction() *actuatortaskpb.Action {
	return &actuatortaskpb.Action{
		Action: &actuatortaskpb.Action_Delete{
			Delete: &actuatortaskpb.DeleteAction{},
		},
	}
}

// RestartAction creates an action for the Actuator to restart a WorkUnit.
func RestartAction(experimentPriority essmsgpb.Priority, author string) *actuatortaskpb.Action {
	return &actuatortaskpb.Action{
		Action: &actuatortaskpb.Action_Restart{
			Restart: &actuatortaskpb.RestartAction{
				ExperimentPriority: experimentPriority.Enum(),
				Author:             proto.String(author),
			},
		},
	}
}
