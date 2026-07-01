// Copyright 2026 Google LLC
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

package spanner

import (
	"context"
	"errors"
	"fmt"
	"time"

	"golang/cloud_google_com/go/spanner/v/v1/spanner"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	timestamppb "protobuf/timestamp_go_proto"
	"google.golang.org/protobuf/proto"
	essmsgpb "xmanager_cloud/experiment_state_server/proto/messages_go_proto"
	client "xmanager_cloud/task_queue/client"
	"xmanager_cloud/storage/spanner/data/spannerdata"
	"xmanager_cloud/storage/storage"
	taskpb "xmanager_cloud/task_queue/proto/task_go_proto"
	"xmanager_cloud/validator/validator"
)

// UpdateSchedulingPause updates the global scheduling pause state in SystemSettings.
func (s *Storage) UpdateSchedulingPause(ctx context.Context, paused bool) error {
	mutation := spanner.InsertOrUpdate("SystemSettings", []string{"Id", "ActuatorSchedulingPaused"}, []any{int64(1), paused})
	_, err := s.client.Apply(ctx, []*spanner.Mutation{mutation})
	return err
}

// GetSchedulingPause retrieves the global scheduling pause state.
func (s *Storage) GetSchedulingPause(ctx context.Context) (bool, error) {
	row, err := s.client.Single().ReadRow(ctx, "SystemSettings", spanner.Key{int64(1)}, []string{"ActuatorSchedulingPaused"})
	if err != nil {
		if status.Code(err) == codes.NotFound || errors.Is(err, spanner.ErrRowNotFound) {
			return false, nil
		}
		return false, err
	}
	var paused bool
	if err := row.Column(0, &paused); err != nil {
		return false, err
	}
	return paused, nil
}

// BufferPausedTask writes a task payload to the PausedTasks table and updates the parent WorkUnit state to GLOBAL_PAUSE in a single transaction.
func (s *Storage) BufferPausedTask(ctx context.Context, task *storage.PausedTask, workUnitStateUpdate *storage.WorkUnitExecutionUpdate) error {
	_, err := s.client.ReadWriteTransaction(ctx, func(ctx context.Context, txn *spanner.ReadWriteTransaction) error {
		actionBytes, marshalErr := proto.Marshal(task.Action)
		if marshalErr != nil {
			return fmt.Errorf("failed to marshal action: %w", marshalErr)
		}

		// 1. Insert into PausedTasks
		taskMutation := spanner.Insert("PausedTasks",
			[]string{"BitReversedXid", "WorkUnitId", "TaskId", "Target", "Action", "OperationName", "TraceId", "CreateTime"},
			[]any{task.BitReversedXid, task.WorkUnitID, task.TaskID, task.Target, actionBytes, task.OperationName, task.TraceID, spanner.CommitTimestamp},
		)

		// 2. Query WorkUnit row to get details
		stmt := spanner.NewStatement(`
			SELECT ExecutionId, ExecutionCreateTime, ExecutionMetadata, WorkUnitStatus
			FROM WorkUnits
			WHERE BitReversedXid = @bitReversedXid AND WorkUnitId = @workUnitId
		`)
		stmt.Params["bitReversedXid"] = task.BitReversedXid
		stmt.Params["workUnitId"] = task.WorkUnitID

		iter := txn.Query(ctx, stmt)
		defer iter.Stop()

		var executionID int64
		var createTime spanner.NullTime
		var metadata *essmsgpb.WorkUnitExecutionMetadata
		var wuStatus *essmsgpb.WorkUnitStatus
		found := false

		err := iter.Do(func(r *spanner.Row) error {
			found = true
			if err := r.ColumnByName("ExecutionId", &executionID); err != nil {
				return err
			}
			if err := r.ColumnByName("ExecutionCreateTime", &createTime); err != nil {
				return err
			}
			if err := r.ColumnByName("ExecutionMetadata", &metadata); err != nil {
				return err
			}
			if err := r.ColumnByName("WorkUnitStatus", &wuStatus); err != nil {
				return err
			}
			return nil
		})
		if err != nil {
			return err
		}

		if !found {
			return fmt.Errorf("work unit with BitReversedXid=%d, WorkUnitId=%d not found", task.BitReversedXid, task.WorkUnitID)
		}

		// 3. Update the state to GLOBAL_PAUSE
		wuStatus.State = essmsgpb.WorkUnitStatus_GLOBAL_PAUSE
		now := time.Now()
		wuStatus.UpdateTime = timestamppb.New(now)
		wuStatus.StateUpdateTime = timestamppb.New(now)


		exec := &essmsgpb.WorkUnitExecution{
			Name:       validator.ConstructWorkUnitExecutionName(reverseBits(task.BitReversedXid), task.WorkUnitID, executionID),
			CreateTime: timestamppb.New(createTime.Time),
			UpdateTime: timestamppb.New(now),
			Status:     wuStatus,
			State:      essmsgpb.WorkUnitExecution_ACTIVE,
			Metadata:   metadata,
		}

		wuMutation := spannerdata.CurrentWorkUnitExecutionProtoToUpdateMutation(reverseBits(task.BitReversedXid), task.WorkUnitID, executionID, exec)

		txn.BufferWrite([]*spanner.Mutation{taskMutation, wuMutation})
		return nil
	})
	return err
}

// FetchPausedTasksBatch retrieves up to `limit` tasks from PausedTasks sorted by CreateTime.
func (s *Storage) FetchPausedTasksBatch(ctx context.Context, limit int) ([]*storage.PausedTask, error) {
	stmt := spanner.NewStatement(`
		SELECT BitReversedXid, WorkUnitId, TaskId, Target, Action, OperationName, TraceId, CreateTime
		FROM PausedTasks
		ORDER BY CreateTime ASC
		LIMIT @limit
	`)
	stmt.Params["limit"] = int64(limit)

	iter := s.client.Single().Query(ctx, stmt)
	defer iter.Stop()

	var tasks []*storage.PausedTask
	err := iter.Do(func(r *spanner.Row) error {
		task := &storage.PausedTask{}
		var createTime time.Time
		if err := r.ColumnByName("BitReversedXid", &task.BitReversedXid); err != nil {
			return err
		}
		if err := r.ColumnByName("WorkUnitId", &task.WorkUnitID); err != nil {
			return err
		}
		if err := r.ColumnByName("TaskId", &task.TaskID); err != nil {
			return err
		}
		if err := r.ColumnByName("Target", &task.Target); err != nil {
			return err
		}
		var actionBytes []byte
		if err := r.ColumnByName("Action", &actionBytes); err != nil {
			return err
		}
		task.Action = &taskpb.Action{}
		if err := proto.Unmarshal(actionBytes, task.Action); err != nil {
			return fmt.Errorf("failed to unmarshal action: %w", err)
		}
		if err := r.ColumnByName("OperationName", &task.OperationName); err != nil {
			return err
		}
		if err := r.ColumnByName("TraceId", &task.TraceID); err != nil {
			return err
		}
		if err := r.ColumnByName("CreateTime", &createTime); err != nil {
			return err
		}
		task.CreateTime = createTime
		tasks = append(tasks, task)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return tasks, nil
}

// DeletePausedTasks deletes a batch of tasks from PausedTasks.
func (s *Storage) DeletePausedTasks(ctx context.Context, keys []storage.PausedTaskKey) error {
	var mutations []*spanner.Mutation
	for _, k := range keys {
		mutations = append(mutations, spanner.Delete("PausedTasks", spanner.Key{k.BitReversedXid, k.WorkUnitID, k.TaskID}))
	}
	_, err := s.client.Apply(ctx, mutations)
	return err
}

// CompletePausedTask atomically deletes the task from PausedTasks and updates the parent WorkUnit state to targetState.
func (s *Storage) CompletePausedTask(ctx context.Context, key storage.PausedTaskKey, targetState essmsgpb.WorkUnitStatus_WorkUnitState) error {
	_, err := s.client.ReadWriteTransaction(ctx, func(ctx context.Context, txn *spanner.ReadWriteTransaction) error {
		// 1. Delete from PausedTasks
		deleteMutation := spanner.Delete("PausedTasks", spanner.Key{key.BitReversedXid, key.WorkUnitID, key.TaskID})

		// 2. Query parent WorkUnit to update its state
		stmt := spanner.NewStatement(`
			SELECT ExecutionId, ExecutionCreateTime, ExecutionMetadata, WorkUnitStatus
			FROM WorkUnits
			WHERE BitReversedXid = @bitReversedXid AND WorkUnitId = @workUnitId
		`)
		stmt.Params["bitReversedXid"] = key.BitReversedXid
		stmt.Params["workUnitId"] = key.WorkUnitID

		iter := txn.Query(ctx, stmt)
		defer iter.Stop()

		var executionID int64
		var createTime spanner.NullTime
		var metadata *essmsgpb.WorkUnitExecutionMetadata
		var wuStatus *essmsgpb.WorkUnitStatus
		found := false

		err := iter.Do(func(r *spanner.Row) error {
			found = true
			if err := r.ColumnByName("ExecutionId", &executionID); err != nil {
				return err
			}
			if err := r.ColumnByName("ExecutionCreateTime", &createTime); err != nil {
				return err
			}
			if err := r.ColumnByName("ExecutionMetadata", &metadata); err != nil {
				return err
			}
			if err := r.ColumnByName("WorkUnitStatus", &wuStatus); err != nil {
				return err
			}
			return nil
		})
		if err != nil {
			return err
		}

		if !found {
			// If parent WorkUnit is not found, we just perform the delete of the PausedTask and exit
			txn.BufferWrite([]*spanner.Mutation{deleteMutation})
			return nil
		}

		// 3. Update status state
		wuStatus.State = targetState
		now := time.Now()
		wuStatus.UpdateTime = timestamppb.New(now)
		wuStatus.StateUpdateTime = timestamppb.New(now)

		exec := &essmsgpb.WorkUnitExecution{
			Name:       validator.ConstructWorkUnitExecutionName(reverseBits(key.BitReversedXid), key.WorkUnitID, executionID),
			CreateTime: timestamppb.New(createTime.Time),
			UpdateTime: timestamppb.New(now),
			Status:     wuStatus,
			State:      essmsgpb.WorkUnitExecution_ACTIVE,
			Metadata:   metadata,
		}

		wuMutation := spannerdata.CurrentWorkUnitExecutionProtoToUpdateMutation(reverseBits(key.BitReversedXid), key.WorkUnitID, executionID, exec)

		txn.BufferWrite([]*spanner.Mutation{deleteMutation, wuMutation})
		return nil
	})
	return err
}

// ScanAndRecoverWorkUnits scans for stuck work units and pauses or schedules them based on the current pause state.
func (s *Storage) ScanAndRecoverWorkUnits(ctx context.Context, paused bool) error {
	if paused {
		// System is paused: find stuck scheduling/starting work units and pause them
		stmt := spanner.NewStatement(`
			SELECT BitReversedXid, WorkUnitId, ExecutionId, ExecutionCreateTime, ExecutionMetadata, WorkUnitStatus
			FROM WorkUnits
			WHERE WorkUnitStatus.state IN (1, 2, 3, 7) -- INITIALIZING, SCHEDULING, STARTING, RESTARTING
		`)
		iter := s.client.Single().Query(ctx, stmt)
		defer iter.Stop()

		var mutations []*spanner.Mutation
		now := time.Now()
		err := iter.Do(func(r *spanner.Row) error {
			var bitReversedXid int64
			var workUnitID int64
			var executionID int64
			var createTime spanner.NullTime
			var metadata *essmsgpb.WorkUnitExecutionMetadata
			var wuStatus *essmsgpb.WorkUnitStatus

			if err := r.ColumnByName("BitReversedXid", &bitReversedXid); err != nil {
				return err
			}
			if err := r.ColumnByName("WorkUnitId", &workUnitID); err != nil {
				return err
			}
			if err := r.ColumnByName("ExecutionId", &executionID); err != nil {
				return err
			}
			if err := r.ColumnByName("ExecutionCreateTime", &createTime); err != nil {
				return err
			}
			if err := r.ColumnByName("ExecutionMetadata", &metadata); err != nil {
				return err
			}
			if err := r.ColumnByName("WorkUnitStatus", &wuStatus); err != nil {
				return err
			}

			wuStatus.State = essmsgpb.WorkUnitStatus_GLOBAL_PAUSE
			wuStatus.UpdateTime = timestamppb.New(now)
			wuStatus.StateUpdateTime = timestamppb.New(now)

			exec := &essmsgpb.WorkUnitExecution{
				Name:       validator.ConstructWorkUnitExecutionName(reverseBits(bitReversedXid), workUnitID, executionID),
				CreateTime: timestamppb.New(createTime.Time),
				UpdateTime: timestamppb.New(now),
				Status:     wuStatus,
				State:      essmsgpb.WorkUnitExecution_ACTIVE,
				Metadata:   metadata,
			}

			mut := spannerdata.CurrentWorkUnitExecutionProtoToUpdateMutation(reverseBits(bitReversedXid), workUnitID, executionID, exec)
			mutations = append(mutations, mut)
			return nil
		})
		if err != nil {
			return err
		}

		if len(mutations) > 0 {
			_, err = s.client.Apply(ctx, mutations)
			return err
		}
		return nil
	} else {
		// System is unpaused: find work units in GLOBAL_PAUSE and buffer them in PausedTasks if not already buffered
		stmt := spanner.NewStatement(`
			SELECT BitReversedXid, WorkUnitId, ExecutionId, WorkUnitStatus
			FROM WorkUnits
			WHERE WorkUnitStatus.state = 12 -- GLOBAL_PAUSE
		`)
		iter := s.client.Single().Query(ctx, stmt)
		defer iter.Stop()

		type recoveryItem struct {
			bitReversedXid int64
			workUnitID     int64
			executionID    int64
			wuStatus       *essmsgpb.WorkUnitStatus
		}
		var items []recoveryItem
		err := iter.Do(func(r *spanner.Row) error {
			var item recoveryItem
			if err := r.ColumnByName("BitReversedXid", &item.bitReversedXid); err != nil {
				return err
			}
			if err := r.ColumnByName("WorkUnitId", &item.workUnitID); err != nil {
				return err
			}
			if err := r.ColumnByName("ExecutionId", &item.executionID); err != nil {
				return err
			}
			if err := r.ColumnByName("WorkUnitStatus", &item.wuStatus); err != nil {
				return err
			}
			items = append(items, item)
			return nil
		})
		if err != nil {
			return err
		}

		for _, item := range items {
			// Check if already in PausedTasks
			stmtCheck := spanner.NewStatement(`
				SELECT 1 FROM PausedTasks
				WHERE BitReversedXid = @bitReversedXid AND WorkUnitId = @workUnitId AND TaskId = @taskId
			`)
			taskId := validator.ConstructWorkUnitExecutionName(reverseBits(item.bitReversedXid), item.workUnitID, item.executionID)
			stmtCheck.Params["bitReversedXid"] = item.bitReversedXid
			stmtCheck.Params["workUnitId"] = item.workUnitID
			stmtCheck.Params["taskId"] = taskId

			iterCheck := s.client.Single().Query(ctx, stmtCheck)
			found := false
			errCheck := iterCheck.Do(func(r *spanner.Row) error {
				found = true
				return nil
			})
			iterCheck.Stop()
			if errCheck != nil {
				return errCheck
			}

			if !found {
				// We need to buffer it!
				var action *taskpb.Action
				xid := reverseBits(item.bitReversedXid)
				exp, err := s.FetchExperiment(ctx, xid)
				if err != nil {
					return err
				}
				if item.executionID == 1 {
					action = client.CreateAction(exp.GetPriority(), "system-recovery")
				} else {
					action = client.RestartAction(exp.GetPriority(), "system-recovery")
				}

				task := &storage.PausedTask{
					BitReversedXid: item.bitReversedXid,
					WorkUnitID:     item.workUnitID,
					TaskID:         taskId,
					Target:         taskId,
					Action:         action,
					OperationName:  fmt.Sprintf("recovery-operation-%d", time.Now().UnixNano()),
					TraceID:        "system-recovery-trace",
				}
				if item.executionID > 1 {
					task.Target = validator.ConstructWorkUnitExecutionName(xid, item.workUnitID, item.executionID-1)
				}

				err = s.BufferPausedTask(ctx, task, nil)
				if err != nil {
					return err
				}
			}
		}
		return nil
	}
}
