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

package workunit

import (
	"context"
	"fmt"
	"time"

	"google.golang.org/protobuf/proto"
	essmsgpb "xmanager_cloud/api/protos/experiment"
	"xmanager_cloud/internal/common/resourceaccumulator"
	"xmanager_cloud/internal/storage"
	"xmanager_cloud/internal/validator"
)

// WithDefaultParent returns msg with Parent set to the work unit resource name (when wid is
// non-zero) or the experiment resource name if Parent is empty. The input message is not mutated.
func WithDefaultParent(xid int64, wid *int64, msg *essmsgpb.StructuredMessage) *essmsgpb.StructuredMessage {
	if msg == nil || msg.GetParent() != "" || xid <= 0 {
		return msg
	}
	cloned := proto.Clone(msg).(*essmsgpb.StructuredMessage)
	if wid != nil && *wid != 0 {
		cloned.Parent = validator.ConstructWorkUnitName(xid, *wid)
		return cloned
	}
	cloned.Parent = validator.ConstructExperimentName(xid)
	return cloned
}

// StatusMessageChanged reports whether newMsg should be recorded as a new status message compared
// to oldMsg, ignoring differences in CreateTime.
func StatusMessageChanged(oldMsg, newMsg *essmsgpb.StructuredMessage) bool {
	if newMsg == nil {
		return false
	}
	if oldMsg == nil {
		return true
	}
	if proto.Equal(oldMsg, newMsg) {
		return false
	}

	oldClone := proto.Clone(oldMsg).(*essmsgpb.StructuredMessage)
	newClone := proto.Clone(newMsg).(*essmsgpb.StructuredMessage)
	oldClone.CreateTime = nil
	newClone.CreateTime = nil

	return !proto.Equal(oldClone, newClone)
}

// PrepareStatusMessageHistory fills in default Parent resource names on oldMsg and newMsg and
// reports whether newMsg is a change that should be recorded in the status message history.
func PrepareStatusMessageHistory(xid int64, wid *int64, oldMsg, newMsg *essmsgpb.StructuredMessage) (*essmsgpb.StructuredMessage, *essmsgpb.StructuredMessage, bool) {
	preparedOld := WithDefaultParent(xid, wid, oldMsg)
	preparedNew := WithDefaultParent(xid, wid, newMsg)
	if !StatusMessageChanged(preparedOld, preparedNew) {
		return nil, nil, false
	}
	return preparedOld, preparedNew, true
}

// RecordStatusMessageHistory prepares oldMsg and newMsg with default parents and saves the status
// message history entry via repo if the message changed.
func RecordStatusMessageHistory(ctx context.Context, repo storage.StatusMessageTxRepository, xid int64, wid, eid *int64, oldMsg, newMsg *essmsgpb.StructuredMessage) error {
	if repo == nil {
		return nil
	}
	prevMsg, nextMsg, changed := PrepareStatusMessageHistory(xid, wid, oldMsg, newMsg)
	if !changed {
		return nil
	}
	return repo.SaveStatusMessageHistory(ctx, xid, wid, eid, prevMsg, nextMsg)
}

// StateChanged reports whether newState is a state transition from currentState.
func StateChanged(currentState, newState *essmsgpb.WorkUnitStatus_WorkUnitState) bool {
	if newState == nil {
		return false
	}
	return currentState == nil || *currentState != *newState
}

// PrepareStatusHistory reports whether a work unit status history entry should be recorded for the
// transition from currentState to newState. It returns the resources to record, defaulting to a
// single empty resource ({"": 0}) so the transition is recorded even without resources, and the
// entry timestamp, defaulting to time.Now() when timestamp is zero.
func PrepareStatusHistory(currentState, newState *essmsgpb.WorkUnitStatus_WorkUnitState, resources map[string]int64, timestamp time.Time) (map[string]int64, time.Time, bool) {
	if !StateChanged(currentState, newState) {
		return nil, time.Time{}, false
	}
	if len(resources) == 0 {
		resources = map[string]int64{"": 0}
	}
	if timestamp.IsZero() {
		timestamp = time.Now()
	}
	return resources, timestamp, true
}

// RecordExecutionHistory records system and user status messages and work unit status history
// for a work unit execution transition within the provided transaction.
func RecordExecutionHistory(ctx context.Context, tx *storage.Transaction, xid, wid, eid int64, executable *essmsgpb.Executable, oldExec, newExec *essmsgpb.WorkUnitExecution) error {
	if tx == nil || newExec == nil {
		return nil
	}
	if err := recordStatusMessages(ctx, tx.StatusMessages, xid, wid, eid, oldExec.GetStatus(), newExec.GetStatus()); err != nil {
		return err
	}
	return recordStatusHistory(ctx, tx.StatusHistory, xid, wid, eid, executable, oldExec.GetStatus(), newExec.GetStatus())
}

func recordStatusMessages(ctx context.Context, repo storage.StatusMessageTxRepository, xid, wid, eid int64, oldStatus, newStatus *essmsgpb.WorkUnitStatus) error {
	if repo == nil {
		return nil
	}
	if err := RecordStatusMessageHistory(ctx, repo, xid, &wid, &eid, oldStatus.GetSystemMessage(), newStatus.GetSystemMessage()); err != nil {
		return fmt.Errorf("failed to save system status message history: %w", err)
	}
	if err := RecordStatusMessageHistory(ctx, repo, xid, &wid, &eid, oldStatus.GetUserMessage(), newStatus.GetUserMessage()); err != nil {
		return fmt.Errorf("failed to save user status message history: %w", err)
	}
	return nil
}

func recordStatusHistory(ctx context.Context, repo storage.StatusHistoryTxRepository, xid, wid, eid int64, executable *essmsgpb.Executable, oldStatus, newStatus *essmsgpb.WorkUnitStatus) error {
	if repo == nil {
		return nil
	}
	oldState := workUnitState(oldStatus)
	newState := workUnitState(newStatus)
	if !StateChanged(oldState, newState) {
		return nil
	}
	resources, timestamp, _ := PrepareStatusHistory(oldState, newState, resourceaccumulator.ExtractExecutableResources(executable), statusUpdateTime(newStatus))
	queueName := executable.GetSchedulingConstraints().GetQueueName()
	if err := repo.SaveWorkUnitStatusHistory(ctx, xid, wid, &eid, queueName, oldState, newState, resources, timestamp); err != nil {
		return fmt.Errorf("failed to save work unit status history: %w", err)
	}
	return nil
}

func workUnitState(status *essmsgpb.WorkUnitStatus) *essmsgpb.WorkUnitStatus_WorkUnitState {
	if status == nil {
		return nil
	}
	return status.GetState().Enum()
}

// statusUpdateTime returns the server-set status update time used to timestamp status history
// entries, or the zero time if it is unset.
func statusUpdateTime(status *essmsgpb.WorkUnitStatus) time.Time {
	if status.GetUpdateTime() == nil {
		return time.Time{}
	}
	return status.GetUpdateTime().AsTime()
}
