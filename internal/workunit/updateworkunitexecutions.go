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
	"slices"
	"time"

	"github.com/kylelemons/godebug/diff"
	"golang/protobuf/v2/proto/proto"
	"google.golang.org/protobuf/encoding/prototext"
	timestamppb "google.golang.org/protobuf/types/known/timestamppb"
	fieldmaskpb "protobuf/field_mask_go_proto"
	"task/go/status"
	essmsgpb "xmanager_cloud/experiment_state_server/proto/messages_go_proto"
	"xmanager_cloud/internal/common/resourceaccumulator"
	"xmanager_cloud/internal/storage"
	"xmanager_cloud/internal/storage/data"
	"xmanager_cloud/logging/logging"
	"xmanager_cloud/storage/state/state"
	"xmanager_cloud/validator/validator"
)

// Updater applies transactional updates to work unit executions.
type Updater struct {
	txRunner storage.TransactionRunner
}

// NewUpdater creates an Updater backed by txRunner.
func NewUpdater(txRunner storage.TransactionRunner) *Updater {
	return &Updater{txRunner: txRunner}
}

// UpdateWorkUnitExecutions updates the executions of a work unit.
func (u *Updater) UpdateWorkUnitExecutions(ctx context.Context, xid int64, updates []*storage.WorkUnitExecutionUpdate) (map[int64]*essmsgpb.WorkUnitExecution, error) {
	return UpdateWorkUnitExecutions(ctx, u.txRunner, xid, updates)
}

// UpdateWorkUnitExecutions updates the executions of a work unit. It returns the canonical
// executions, either the updated executions or the original executions if no update was done.
func UpdateWorkUnitExecutions(ctx context.Context, txRunner storage.TransactionRunner, xid int64, updates []*storage.WorkUnitExecutionUpdate) (map[int64]*essmsgpb.WorkUnitExecution, error) {
	ctx = logging.WithExperimentName(ctx, validator.ConstructExperimentName(xid))
	logging.Info(ctx, "Update work unit executions", "updates", updates)

	if len(updates) == 0 {
		return nil, nil
	}

	wids := make([]int64, len(updates))
	for i, update := range updates {
		wids[i] = update.WID
	}

	for _, update := range updates {
		ctx := logging.WithWorkUnitExecutionName(ctx, validator.ConstructWorkUnitExecutionName(xid, update.WID, update.EID))
		logging.Info(ctx, "Process work unit execution update", "update", update.WorkUnitExecution)
	}

	var updatedExecutions map[int64]*essmsgpb.WorkUnitExecution
	var exp *essmsgpb.Experiment
	err := txRunner.RunInTransaction(ctx, func(ctx context.Context, tx *storage.Transaction) error {
		var err error
		updatedExecutions, exp, err = updateWorkUnitExecutionsInTxn(ctx, tx, xid, wids, updates)
		return err
	})
	if err != nil {
		return nil, err
	}

	logging.Info(ctx, "Updated work unit executions", "updatedExecutions", updatedExecutions)
	logging.Info(ctx, "Updated Experiment", "exp", exp)

	return updatedExecutions, nil
}

// UpdateWorkUnitExecutionsInTx updates the executions of a work unit using the bound read-write
// transaction. It applies the same merge, state transition guard, status history and experiment
// status denormalization as UpdateWorkUnitExecutions, but the writes only take effect
// when the caller's transaction commits.
func UpdateWorkUnitExecutionsInTx(ctx context.Context, tx *storage.Transaction, xid int64, updates []*storage.WorkUnitExecutionUpdate) (map[int64]*essmsgpb.WorkUnitExecution, error) {
	ctx = logging.WithExperimentName(ctx, validator.ConstructExperimentName(xid))
	if len(updates) == 0 {
		return nil, nil
	}

	wids := make([]int64, len(updates))
	for i, update := range updates {
		wids[i] = update.WID
	}

	updatedExecutions, _, err := updateWorkUnitExecutionsInTxn(ctx, tx, xid, wids, updates)
	return updatedExecutions, err
}

// updateWorkUnitExecutionsInTxn applies the updates within tx and returns the canonical
// executions and the experiment read within the transaction.
func updateWorkUnitExecutionsInTxn(ctx context.Context, tx *storage.Transaction, xid int64, wids []int64, updates []*storage.WorkUnitExecutionUpdate) (map[int64]*essmsgpb.WorkUnitExecution, *essmsgpb.Experiment, error) {
	// 1. Read all WorkUnits from storage in a single batch. This also locks the rows for the
	// duration of the transaction using the `FOR UPDATE` hint.
	workUnits, err := tx.WorkUnits.FetchUpdatingWorkUnits(ctx, xid, wids)
	if err != nil {
		return nil, nil, err
	}

	// 1.1. Process the rows and build a map of WorkUnitExecutions keyed by WorkUnitId.
	updatedExecutions := make(map[int64]*essmsgpb.WorkUnitExecution)
	queueNames := make(map[int64]string)
	executables := make(map[int64]*essmsgpb.Executable)
	originalExecutions := make(map[int64]*essmsgpb.WorkUnitExecution)
	for wid, wu := range workUnits {
		execution := wu.GetCurrentWorkUnitExecution()
		updatedExecutions[wid] = execution
		originalExecutions[wid] = proto.Clone(execution).(*essmsgpb.WorkUnitExecution)
		queueNames[wid] = wu.GetExecutable().GetSchedulingConstraints().GetQueueName()
		executables[wid] = wu.GetExecutable()
	}

	// 2. Process each update. The updates should be idempotent to the processing order, but if we
	// have duplicates, we don't want to end up with a mutation ordering dependency issue. So we
	// process them all and then update the database in one go.
	var executionDeltas []*state.WorkUnitStateDelta
	for _, update := range updates {
		currentWorkUnitExecution := updatedExecutions[update.WID]
		if currentWorkUnitExecution == nil {
			return nil, nil, fmt.Errorf("work unit execution %d not found: %w", update.WID, status.ErrNotFound)
		}

		ctx := logging.WithWorkUnitExecutionName(ctx, currentWorkUnitExecution.GetName())

		_, _, curEID, err := validator.ValidateWorkUnitExecutionName(currentWorkUnitExecution.GetName())
		if err != nil {
			return nil, nil, fmt.Errorf("failed to validate work unit execution name %q: %w", currentWorkUnitExecution.GetName(), err)
		}

		// If the execution ID doesn't match, we can skip this update. This is fine, it just means
		// that the execution was updated in between the request and our transaction.
		if curEID != update.EID {
			continue
		}

		statusVal := update.WorkUnitExecution.Status
		if statusVal != nil {
			// Set the parent field in the structured status message. This is not strictly required, but
			// it makes the caller's job easier since they don't need to construct the parent resource
			// name, and it's easier for clients to consume these messages if they're inherently labelled
			// with their parent.
			parent := validator.ConstructWorkUnitName(xid, update.WID)
			setParentIfNonNil(statusVal.SystemMessage, parent)
			setParentIfNonNil(statusVal.UserMessage, parent)
		}

		updatedExecution, err := updateWorkUnitExecution(ctx, updatedExecutions[update.WID], update.WorkUnitExecution, update.UpdateMask, update.ForceStatusMessageUpdate, update.DeferToExistingMessage)
		if err != nil {
			return nil, nil, fmt.Errorf("failed to update work unit execution: %w", err)
		}

		// If the update time is the same, no update was done, so we can skip the update.
		if updatedExecution.UpdateTime.AsTime().Equal(updatedExecutions[update.WID].UpdateTime.AsTime()) {
			continue
		}

		executionDelta := &state.WorkUnitStateDelta{
			CurrentState:   &updatedExecutions[update.WID].Status.State,
			NewState:       &updatedExecution.Status.State,
			CurrentMessage: state.GetBestMessageFromStatus(updatedExecutions[update.WID].GetStatus()),
			NewMessage:     state.GetBestMessageFromStatus(updatedExecution.GetStatus()),
		}
		executionDeltas = append(executionDeltas, executionDelta)

		if err := saveUpdateHistory(ctx, tx, xid, update.WID, update.EID, queueNames[update.WID], executables[update.WID], updatedExecutions[update.WID], updatedExecution); err != nil {
			return nil, nil, err
		}

		updatedExecutions[update.WID] = updatedExecution
	}

	if err := persistUpdatedExecutions(ctx, tx, xid, originalExecutions, updatedExecutions); err != nil {
		return nil, nil, err
	}

	exp, err := updateExperimentForDeltas(ctx, tx, xid, executionDeltas, updatedExecutions)
	if err != nil {
		return nil, nil, err
	}

	return updatedExecutions, exp, nil
}

func saveUpdateHistory(ctx context.Context, tx *storage.Transaction, xid, wid, eid int64, qName string, exec *essmsgpb.Executable, currentExecution, updatedExecution *essmsgpb.WorkUnitExecution) error {
	if err := tx.StatusMessages.SaveStatusMessageHistoryIfChanged(ctx, xid, &wid, &eid, currentExecution.GetStatus().GetSystemMessage(), updatedExecution.GetStatus().GetSystemMessage()); err != nil {
		return fmt.Errorf("failed to save system status message history: %w", err)
	}
	if err := tx.StatusMessages.SaveStatusMessageHistoryIfChanged(ctx, xid, &wid, &eid, currentExecution.GetStatus().GetUserMessage(), updatedExecution.GetStatus().GetUserMessage()); err != nil {
		return fmt.Errorf("failed to save user status message history: %w", err)
	}

	acc := resourceaccumulator.New()
	acc.AccumulateExecutable(exec)
	resMap := acc.IntTotals()
	var prevState *essmsgpb.WorkUnitStatus_WorkUnitState
	if currentExecution.GetStatus() != nil {
		prevState = currentExecution.Status.State.Enum()
	}
	var newState *essmsgpb.WorkUnitStatus_WorkUnitState
	var stateTime time.Time
	if updatedExecution.GetStatus() != nil {
		newState = updatedExecution.Status.State.Enum()
		if updatedExecution.Status.GetUpdateTime() != nil {
			stateTime = updatedExecution.Status.GetUpdateTime().AsTime()
		}
	}
	if err := tx.StatusHistory.SaveWorkUnitStatusHistoryIfChanged(ctx, xid, wid, &eid, qName, prevState, newState, resMap, stateTime); err != nil {
		return fmt.Errorf("failed to save work unit status history: %w", err)
	}
	return nil
}

func persistUpdatedExecutions(ctx context.Context, tx *storage.Transaction, xid int64, originalExecutions, updatedExecutions map[int64]*essmsgpb.WorkUnitExecution) error {
	// 2.1. Update the database with the processed updates.
	for wid, execution := range updatedExecutions {
		_, _, eid, err := validator.ValidateWorkUnitExecutionName(execution.GetName())
		if err != nil {
			return fmt.Errorf("failed to validate work unit execution name %q: %w", execution.GetName(), err)
		}
		if eid == 0 {
			return fmt.Errorf("invalid eid 0 from work unit execution name %q: %w", execution.GetName(), status.ErrInternal)
		}

		ctx := logging.WithWorkUnitExecutionName(ctx, execution.GetName())
		protoDiff := diff.Diff(prototext.Format(originalExecutions[wid]), prototext.Format(execution))

		if err := tx.WorkUnits.UpdateCurrentWorkUnitExecution(ctx, xid, wid, eid, execution); err != nil {
			return fmt.Errorf("failed to update current work unit execution: %w", err)
		}
		logging.Info(ctx, fmt.Sprintf("Updated work unit execution: %s", protoDiff), "execution", execution)
	}
	return nil
}

func updateExperimentForDeltas(ctx context.Context, tx *storage.Transaction, xid int64, executionDeltas []*state.WorkUnitStateDelta, updatedExecutions map[int64]*essmsgpb.WorkUnitExecution) (*essmsgpb.Experiment, error) {
	if tx.Experiments == nil {
		return nil, fmt.Errorf("experiment repository is not initialized")
	}
	// 3. Read the Experiment row.
	exp, err := tx.Experiments.FetchExperiment(ctx, xid)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch experiment: %w", err)
	}
	if exp == nil {
		return nil, fmt.Errorf("experiment %d not found", xid)
	}
	if exp.Status == nil {
		exp.Status = &essmsgpb.ExperimentStatus{}
	}

	// 3.1. Clone the experiment status so we can compare it to the updated status later.
	currentExpStatus := proto.Clone(exp.Status).(*essmsgpb.ExperimentStatus)

	// 4. Update the denormalized status fields in the Experiment row.
	updatedExp, err := state.DenormalizeWorkUnitStatus(ctx, exp, executionDeltas)
	if err != nil {
		return nil, fmt.Errorf("failed to update experiment status: %w", err)
	}

	// 4.1. Determine the new experiment status message.
	newExpMsg, err := findExperimentStatusMessage(ctx, xid, tx, updatedExp, updatedExecutions)
	if err != nil {
		return nil, fmt.Errorf("failed to find experiment status message: %w", err)
	}
	updatedExp.Status.StructuredStatusMessage = newExpMsg

	// 5. Update the Experiment row if it has changed.
	if !proto.Equal(currentExpStatus, updatedExp.Status) {
		logging.Info(ctx, "Update experiment status", "updatedExp", updatedExp.Status)
		// Update the update time and state update time if the execution or status or state has changed.
		now := time.Now()
		exp.UpdateTime = timestamppb.New(now)

		if currentExpStatus.ActiveState == essmsgpb.ExperimentStatus_ACTIVE && updatedExp.Status.ActiveState != essmsgpb.ExperimentStatus_ACTIVE {
			exp.CompleteTime = timestamppb.New(now)
			logging.Info(ctx, "Marking experiment as complete", "completeTime", exp.CompleteTime)
		}

		if err := tx.Experiments.UpdateExperimentStatus(ctx, updatedExp); err != nil {
			logging.Error(ctx, "Failed to create mutation for experiment status update", "error", err)
			return nil, fmt.Errorf("failed to create mutation for experiment status update: %w", err)
		}

		protoDiff := diff.Diff(prototext.Format(currentExpStatus), prototext.Format(updatedExp.Status))
		logging.Info(ctx, fmt.Sprintf("Updated experiment status: %s", protoDiff), "updatedExpStatus", updatedExp.Status)
	}

	return exp, nil
}

func updateWorkUnitExecution(ctx context.Context, currentExecution *essmsgpb.WorkUnitExecution, updatedExecution *essmsgpb.WorkUnitExecution, mask *fieldmaskpb.FieldMask, forceStatusMessageUpdate bool, deferToExistingMessage bool) (*essmsgpb.WorkUnitExecution, error) {
	ctx = logging.WithWorkUnitExecutionName(ctx, currentExecution.GetName())
	logging.Debug(ctx, fmt.Sprintf("updateWorkUnitExecution - mask: %v", mask), "currentExecution", currentExecution, "updatedExecution", updatedExecution, "mask", mask, "forceStatusMessageUpdate", forceStatusMessageUpdate, "deferToExistingMessage", deferToExistingMessage)

	// Merge the updated execution with the current execution.
	newExecution, err := data.MergeWorkUnitExecution(ctx, currentExecution, updatedExecution, mask, forceStatusMessageUpdate, deferToExistingMessage)
	if err != nil {
		return nil, err
	}

	now := time.Now()
	if !proto.Equal(currentExecution, newExecution) {
		newExecution.UpdateTime = timestamppb.New(now)
	}
	if !proto.Equal(currentExecution.Status, newExecution.Status) {
		newExecution.Status.UpdateTime = timestamppb.New(now)
	}
	if !slices.Contains(state.WorkUnitTerminalStates, currentExecution.Status.State) && slices.Contains(state.WorkUnitTerminalStates, newExecution.Status.State) {
		newExecution.CompleteTime = timestamppb.New(now)
	}

	return newExecution, nil
}

func findExperimentStatusMessage(ctx context.Context, xid int64, tx *storage.Transaction, updatedExp *essmsgpb.Experiment, updatedWUs map[int64]*essmsgpb.WorkUnitExecution) (*essmsgpb.StructuredMessage, error) {
	// These are the severities we consider when propagating a work unit status message up to the
	// experiment.
	severities := []essmsgpb.StructuredMessage_Severity{
		essmsgpb.StructuredMessage_FATAL,
		essmsgpb.StructuredMessage_ERROR,
		essmsgpb.StructuredMessage_WARNING,
	}

	// If the source work unit for the current experiment message is not part of this update batch
	// (`!isCurrentMsgUpdated`), we can optimize by avoiding a full database query for that message.
	currentMsg := updatedExp.Status.StructuredStatusMessage
	isCurrentMsgUpdated, err := checkIsCurrentMsgUpdated(currentMsg, updatedWUs)
	if err != nil {
		return nil, err
	}

	// Build a map of the current severity counts.
	severityCounts := make(map[essmsgpb.StructuredMessage_Severity]int64)
	for _, severityCount := range updatedExp.Status.MessageSeverityCounts {
		severityCounts[severityCount.GetSeverity()] = severityCount.GetCount()
	}

	for _, sev := range severities {
		// If there are no messages of this severity in the work units (as indicated by severityCounts)
		// and the current message is not of this severity (or is being updated), then we can skip this
		// severity.
		hasCurrentMsg := currentMsg != nil && !isCurrentMsgUpdated && currentMsg.GetSeverity() == sev
		if severityCounts[sev] == 0 && !hasCurrentMsg {
			continue
		}

		var candidates []*essmsgpb.StructuredMessage

		// If the current status message is not being updated, we just need to check if any of the
		// updated work units have a better message.
		if !isCurrentMsgUpdated {
			if hasCurrentMsg {
				candidates = append(candidates, currentMsg)
			}
		} else if count := severityCounts[sev]; count > 0 {
			var err error
			candidates, err = collectUnmodifiedMessagesForSeverity(ctx, xid, sev, tx, updatedWUs)
			if err != nil {
				return nil, err
			}
		}

		candidates = appendUpdatedMessagesForSeverity(candidates, sev, updatedWUs)

		// Find the best status message from the candidates and return it.
		if msg := state.FindBestStatusMessage(candidates); msg != nil {
			return msg, nil
		}
	}

	return nil, nil
}

func appendUpdatedMessagesForSeverity(candidates []*essmsgpb.StructuredMessage, sev essmsgpb.StructuredMessage_Severity, updatedWUs map[int64]*essmsgpb.WorkUnitExecution) []*essmsgpb.StructuredMessage {
	// Add the updated messages to the candidates list. They haven't been committed yet, so they
	// won't be returned by the query.
	for _, wu := range updatedWUs {
		msg := state.GetBestMessageFromStatus(wu.GetStatus())
		if msg == nil || msg.GetSeverity() != sev {
			continue
		}
		candidates = append(candidates, msg)
	}
	return candidates
}

func setParentIfNonNil(msg *essmsgpb.StructuredMessage, parent string) {
	if msg != nil {
		msg.Parent = parent
	}
}

func checkIsCurrentMsgUpdated(currentMsg *essmsgpb.StructuredMessage, updatedWUs map[int64]*essmsgpb.WorkUnitExecution) (bool, error) {
	if currentMsg == nil {
		return false, nil
	}
	resourceType := validator.GetResourceType(currentMsg.Parent)
	if resourceType == validator.WorkUnitResource {
		_, wid, err := validator.ValidateWorkUnitName(currentMsg.Parent)
		if err != nil {
			return false, fmt.Errorf("failed to validate work unit name from message parent %q: %w", currentMsg.Parent, err)
		}
		_, isUpdated := updatedWUs[wid]
		return isUpdated, nil
	}
	if resourceType != validator.ExperimentResource {
		return false, fmt.Errorf("current message parent %q has invalid resource type %v: %w", currentMsg.Parent, resourceType, status.ErrInternal)
	}
	return false, nil
}

func collectUnmodifiedMessagesForSeverity(ctx context.Context, xid int64, sev essmsgpb.StructuredMessage_Severity, tx *storage.Transaction, updatedWUs map[int64]*essmsgpb.WorkUnitExecution) ([]*essmsgpb.StructuredMessage, error) {
	messages, err := tx.WorkUnits.ListWorkUnitMessagesBySeverity(ctx, xid, sev)
	if err != nil {
		return nil, err
	}
	var candidates []*essmsgpb.StructuredMessage
	for _, msg := range messages {
		if msg == nil {
			continue
		}
		_, wid, err := validator.ValidateWorkUnitName(msg.GetParent())
		if err != nil {
			return nil, fmt.Errorf("failed to validate work unit name from message parent %q: %w", msg.GetParent(), err)
		}
		if _, ok := updatedWUs[wid]; ok {
			continue
		}
		candidates = append(candidates, msg)
	}
	return candidates, nil
}
