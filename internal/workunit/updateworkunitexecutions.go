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

// Package workunit orchestrates work unit execution updates across storage repositories.
package workunit

import (
	"context"
	"fmt"
	"slices"
	"time"

	"github.com/kylelemons/godebug/diff"
	"google.golang.org/protobuf/encoding/prototext"
	"google.golang.org/protobuf/proto"
	fieldmaskpb "google.golang.org/protobuf/types/known/fieldmaskpb"
	timestamppb "google.golang.org/protobuf/types/known/timestamppb"
	essmsgpb "xmanager_cloud/experiment_state_server/proto/messages_go_proto"
	"xmanager_cloud/internal/common/resourceaccumulator"
	"xmanager_cloud/internal/grpcerr"
	"xmanager_cloud/internal/logging"
	"xmanager_cloud/internal/storage"
	"xmanager_cloud/internal/storage/data"
	"xmanager_cloud/storage/state/state"
	"xmanager_cloud/validator/validator"
)

// Updater orchestrates work unit execution updates across storage repositories.
type Updater struct {
	txRunner storage.TransactionRunner
}

// NewUpdater creates a new Updater backed by the given TransactionRunner.
func NewUpdater(txRunner storage.TransactionRunner) *Updater {
	return &Updater{txRunner: txRunner}
}

// UpdateWorkUnitExecutions updates the executions of work units within a read-write transaction.
// It returns the canonical executions, either the updated executions or the original executions if
// no update was done.
func (u *Updater) UpdateWorkUnitExecutions(ctx context.Context, xid int64, updates []*storage.WorkUnitExecutionUpdate) (map[int64]*essmsgpb.WorkUnitExecution, error) {
	ctx = logging.WithExperimentName(ctx, validator.ConstructExperimentName(xid))
	logging.Info(ctx, "Update work unit executions", "updates", updates)

	if len(updates) == 0 {
		return nil, nil
	}

	for _, update := range updates {
		ctx := logging.WithWorkUnitExecutionName(ctx, validator.ConstructWorkUnitExecutionName(xid, update.WID, update.EID))
		logging.Info(ctx, "Process work unit execution update", "update", update.WorkUnitExecution)
	}

	var updatedExecutions map[int64]*essmsgpb.WorkUnitExecution
	err := u.txRunner.RunInTransaction(ctx, func(ctx context.Context, tx *storage.Transaction) error {
		var err error
		updatedExecutions, err = UpdateWorkUnitExecutionsInTx(ctx, tx, xid, updates)
		return err
	})
	if err != nil {
		return nil, err
	}

	logging.Info(ctx, "Updated work unit executions", "updatedExecutions", updatedExecutions)
	return updatedExecutions, nil
}

// UpdateWorkUnitExecutionsInTx applies work unit execution updates within an existing storage
// transaction, including merge logic, state transition guards, status history, and experiment
// status denormalization.
func UpdateWorkUnitExecutionsInTx(ctx context.Context, tx *storage.Transaction, xid int64, updates []*storage.WorkUnitExecutionUpdate) (map[int64]*essmsgpb.WorkUnitExecution, error) {
	ctx = logging.WithExperimentName(ctx, validator.ConstructExperimentName(xid))
	if len(updates) == 0 {
		return nil, nil
	}

	workUnits, updatedExecutions, err := fetchExecutionsForUpdate(ctx, tx, xid, updates)
	if err != nil {
		return nil, err
	}

	executionDeltas, err := processExecutionUpdates(ctx, tx, xid, workUnits, updatedExecutions, updates)
	if err != nil {
		return nil, err
	}

	if err := persistUpdatedExecutions(ctx, tx, xid, workUnits, updatedExecutions); err != nil {
		return nil, err
	}

	if err := updateExperimentFromExecutions(ctx, tx, xid, executionDeltas, updatedExecutions); err != nil {
		return nil, err
	}

	return updatedExecutions, nil
}

func fetchExecutionsForUpdate(
	ctx context.Context,
	tx *storage.Transaction,
	xid int64,
	updates []*storage.WorkUnitExecutionUpdate,
) (map[int64]*essmsgpb.WorkUnit, map[int64]*essmsgpb.WorkUnitExecution, error) {
	wids := make([]int64, len(updates))
	for i, update := range updates {
		wids[i] = update.WID
	}

	workUnits, err := tx.WorkUnits.FetchUpdatingWorkUnits(ctx, xid, wids)
	if err != nil {
		return nil, nil, err
	}

	updatedExecutions := make(map[int64]*essmsgpb.WorkUnitExecution, len(workUnits))
	for wid, wu := range workUnits {
		exec := wu.GetCurrentWorkUnitExecution()
		if exec == nil {
			continue
		}
		if exec.Status == nil {
			exec = proto.Clone(exec).(*essmsgpb.WorkUnitExecution)
			exec.Status = &essmsgpb.WorkUnitStatus{}
			wu.CurrentWorkUnitExecution = exec
		}
		updatedExecutions[wid] = exec
	}

	return workUnits, updatedExecutions, nil
}

func processExecutionUpdates(
	ctx context.Context,
	tx *storage.Transaction,
	xid int64,
	workUnits map[int64]*essmsgpb.WorkUnit,
	updatedExecutions map[int64]*essmsgpb.WorkUnitExecution,
	updates []*storage.WorkUnitExecutionUpdate,
) ([]*state.WorkUnitStateDelta, error) {
	var executionDeltas []*state.WorkUnitStateDelta
	for _, update := range updates {
		delta, err := applySingleExecutionUpdate(ctx, tx, xid, workUnits[update.WID], updatedExecutions, update)
		if err != nil {
			return nil, err
		}
		if delta != nil {
			executionDeltas = append(executionDeltas, delta)
		}
	}
	return executionDeltas, nil
}

func applySingleExecutionUpdate(
	ctx context.Context,
	tx *storage.Transaction,
	xid int64,
	wu *essmsgpb.WorkUnit,
	updatedExecutions map[int64]*essmsgpb.WorkUnitExecution,
	update *storage.WorkUnitExecutionUpdate,
) (*state.WorkUnitStateDelta, error) {
	currentExecution := updatedExecutions[update.WID]
	if currentExecution == nil {
		return nil, fmt.Errorf("work unit execution %d not found: %w", update.WID, grpcerr.ErrNotFound)
	}

	ctx = logging.WithWorkUnitExecutionName(ctx, currentExecution.GetName())
	_, _, curEID, err := validator.ValidateWorkUnitExecutionName(currentExecution.GetName())
	if err != nil {
		return nil, fmt.Errorf("failed to validate work unit execution name %q: %w", currentExecution.GetName(), err)
	}
	if curEID != update.EID {
		return nil, nil
	}

	setStatusMessageParents(xid, update)
	updatedExecution, err := updateWorkUnitExecution(ctx, currentExecution, update.WorkUnitExecution, update.UpdateMask, update.ForceStatusMessageUpdate, update.DeferToExistingMessage)
	if err != nil {
		return nil, fmt.Errorf("failed to update work unit execution: %w", err)
	}
	if updatedExecution.UpdateTime.AsTime().Equal(currentExecution.UpdateTime.AsTime()) {
		return nil, nil
	}

	if err := recordExecutionHistory(ctx, tx, xid, update, wu.GetExecutable(), currentExecution, updatedExecution); err != nil {
		return nil, err
	}

	delta := &state.WorkUnitStateDelta{
		CurrentState:   &currentExecution.Status.State,
		NewState:       &updatedExecution.Status.State,
		CurrentMessage: state.GetBestMessageFromStatus(currentExecution.GetStatus()),
		NewMessage:     state.GetBestMessageFromStatus(updatedExecution.GetStatus()),
	}
	updatedExecutions[update.WID] = updatedExecution
	return delta, nil
}

func recordExecutionHistory(
	ctx context.Context,
	tx *storage.Transaction,
	xid int64,
	update *storage.WorkUnitExecutionUpdate,
	executable *essmsgpb.Executable,
	prevExec, newExec *essmsgpb.WorkUnitExecution,
) error {
	if err := tx.StatusMessages.SaveStatusMessageHistoryIfChanged(ctx, xid, &update.WID, &update.EID, prevExec.GetStatus().GetSystemMessage(), newExec.GetStatus().GetSystemMessage()); err != nil {
		return fmt.Errorf("failed to save system status message history: %w", err)
	}
	if err := tx.StatusMessages.SaveStatusMessageHistoryIfChanged(ctx, xid, &update.WID, &update.EID, prevExec.GetStatus().GetUserMessage(), newExec.GetStatus().GetUserMessage()); err != nil {
		return fmt.Errorf("failed to save user status message history: %w", err)
	}

	qName := executable.GetSchedulingConstraints().GetQueueName()
	acc := resourceaccumulator.New()
	acc.AccumulateExecutable(executable)
	resMap := acc.IntTotals()

	var prevState *essmsgpb.WorkUnitStatus_WorkUnitState
	if prevExec.GetStatus() != nil {
		prevState = prevExec.Status.State.Enum()
	}
	var newState *essmsgpb.WorkUnitStatus_WorkUnitState
	var stateTime time.Time
	if newExec.GetStatus() != nil {
		newState = newExec.Status.State.Enum()
		if newExec.Status.GetUpdateTime() != nil {
			stateTime = newExec.Status.GetUpdateTime().AsTime()
		}
	}
	if err := tx.StatusHistory.SaveWorkUnitStatusHistoryIfChanged(ctx, xid, update.WID, &update.EID, qName, prevState, newState, resMap, stateTime); err != nil {
		return fmt.Errorf("failed to save work unit status history: %w", err)
	}
	return nil
}

func persistUpdatedExecutions(
	ctx context.Context,
	tx *storage.Transaction,
	xid int64,
	workUnits map[int64]*essmsgpb.WorkUnit,
	updatedExecutions map[int64]*essmsgpb.WorkUnitExecution,
) error {
	for wid, execution := range updatedExecutions {
		_, _, eid, err := validator.ValidateWorkUnitExecutionName(execution.GetName())
		if err != nil {
			return fmt.Errorf("failed to validate work unit execution name %q: %w", execution.GetName(), err)
		}
		if eid == 0 {
			return fmt.Errorf("invalid eid 0 from work unit execution name %q: %w", execution.GetName(), grpcerr.ErrInternal)
		}

		ctx := logging.WithWorkUnitExecutionName(ctx, execution.GetName())
		protoDiff := diff.Diff(prototext.Format(workUnits[wid].GetCurrentWorkUnitExecution()), prototext.Format(execution))
		if err := tx.WorkUnits.UpdateCurrentWorkUnitExecution(ctx, xid, wid, eid, execution); err != nil {
			return fmt.Errorf("failed to update current work unit execution: %w", err)
		}
		logging.Info(ctx, fmt.Sprintf("Updated work unit execution: %s", protoDiff), "execution", execution)
	}
	return nil
}

func updateExperimentFromExecutions(
	ctx context.Context,
	tx *storage.Transaction,
	xid int64,
	executionDeltas []*state.WorkUnitStateDelta,
	updatedExecutions map[int64]*essmsgpb.WorkUnitExecution,
) error {
	exp, err := tx.Experiments.FetchExperiment(ctx, xid)
	if err != nil {
		return fmt.Errorf("failed to fetch experiment: %w", err)
	}
	if exp == nil {
		return fmt.Errorf("experiment %d not found", xid)
	}
	if exp.Status == nil {
		exp.Status = &essmsgpb.ExperimentStatus{}
	}

	currentExpStatus := proto.Clone(exp.Status).(*essmsgpb.ExperimentStatus)
	updatedExp, err := state.DenormalizeWorkUnitStatus(ctx, exp, executionDeltas)
	if err != nil {
		return fmt.Errorf("failed to update experiment status: %w", err)
	}

	newExpMsg, err := findExperimentStatusMessage(ctx, xid, tx.WorkUnits, updatedExp, updatedExecutions)
	if err != nil {
		return fmt.Errorf("failed to find experiment status message: %w", err)
	}
	updatedExp.Status.StructuredStatusMessage = newExpMsg

	if !proto.Equal(currentExpStatus, updatedExp.Status) {
		if err := saveUpdatedExperimentStatus(ctx, tx, currentExpStatus, updatedExp); err != nil {
			return err
		}
	}
	logging.Info(ctx, "Updated Experiment", "exp", updatedExp)
	return nil
}

func saveUpdatedExperimentStatus(
	ctx context.Context,
	tx *storage.Transaction,
	currentExpStatus *essmsgpb.ExperimentStatus,
	updatedExp *essmsgpb.Experiment,
) error {
	logging.Info(ctx, "Update experiment status", "updatedExp", updatedExp.Status)
	now := time.Now()
	updatedExp.UpdateTime = timestamppb.New(now)

	if currentExpStatus.GetActiveState() == essmsgpb.ExperimentStatus_ACTIVE && updatedExp.Status.GetActiveState() != essmsgpb.ExperimentStatus_ACTIVE {
		updatedExp.CompleteTime = timestamppb.New(now)
		logging.Info(ctx, "Marking experiment as complete", "completeTime", updatedExp.CompleteTime)
	}

	protoDiff := diff.Diff(prototext.Format(currentExpStatus), prototext.Format(updatedExp.Status))
	logging.Info(ctx, fmt.Sprintf("Updated experiment status: %s", protoDiff), "updatedExpStatus", updatedExp.Status)

	if err := tx.Experiments.UpdateExperimentStatus(ctx, updatedExp); err != nil {
		return fmt.Errorf("failed to update experiment status: %w", err)
	}
	return nil
}

func updateWorkUnitExecution(ctx context.Context, currentExecution *essmsgpb.WorkUnitExecution, updatedExecution *essmsgpb.WorkUnitExecution, mask *fieldmaskpb.FieldMask, forceStatusMessageUpdate bool, deferToExistingMessage bool) (*essmsgpb.WorkUnitExecution, error) {
	ctx = logging.WithWorkUnitExecutionName(ctx, currentExecution.GetName())
	logging.Debug(ctx, fmt.Sprintf("updateWorkUnitExecution - mask: %v", mask), "currentExecution", currentExecution, "updatedExecution", updatedExecution, "mask", mask, "forceStatusMessageUpdate", forceStatusMessageUpdate, "deferToExistingMessage", deferToExistingMessage)

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

func findExperimentStatusMessage(ctx context.Context, xid int64, workUnits storage.WorkUnitTxRepository, updatedExp *essmsgpb.Experiment, updatedWUs map[int64]*essmsgpb.WorkUnitExecution) (*essmsgpb.StructuredMessage, error) {
	severities := []essmsgpb.StructuredMessage_Severity{
		essmsgpb.StructuredMessage_FATAL,
		essmsgpb.StructuredMessage_ERROR,
		essmsgpb.StructuredMessage_WARNING,
	}

	currentMsg := updatedExp.Status.StructuredStatusMessage
	isCurrentMsgUpdated, err := checkIsCurrentMsgUpdated(currentMsg, updatedWUs)
	if err != nil {
		return nil, err
	}

	severityCounts := make(map[essmsgpb.StructuredMessage_Severity]int64)
	for _, severityCount := range updatedExp.Status.MessageSeverityCounts {
		severityCounts[severityCount.GetSeverity()] = severityCount.GetCount()
	}

	for _, sev := range severities {
		hasCurrentMsg := currentMsg != nil && !isCurrentMsgUpdated && currentMsg.GetSeverity() == sev
		if severityCounts[sev] == 0 && !hasCurrentMsg {
			continue
		}

		candidates, err := candidatesForSeverity(ctx, xid, sev, workUnits, currentMsg, isCurrentMsgUpdated, updatedWUs)
		if err != nil {
			return nil, err
		}
		if msg := state.FindBestStatusMessage(candidates); msg != nil {
			return msg, nil
		}
	}

	return nil, nil
}

func candidatesForSeverity(
	ctx context.Context,
	xid int64,
	sev essmsgpb.StructuredMessage_Severity,
	workUnits storage.WorkUnitTxRepository,
	currentMsg *essmsgpb.StructuredMessage,
	isCurrentMsgUpdated bool,
	updatedWUs map[int64]*essmsgpb.WorkUnitExecution,
) ([]*essmsgpb.StructuredMessage, error) {
	var candidates []*essmsgpb.StructuredMessage
	if !isCurrentMsgUpdated {
		if currentMsg.GetSeverity() == sev {
			candidates = append(candidates, currentMsg)
		}
	} else {
		var err error
		candidates, err = collectUnmodifiedMessagesForSeverity(ctx, xid, sev, workUnits, updatedWUs)
		if err != nil {
			return nil, err
		}
	}

	for _, wu := range updatedWUs {
		msg := state.GetBestMessageFromStatus(wu.GetStatus())
		if msg == nil || msg.GetSeverity() != sev {
			continue
		}
		candidates = append(candidates, msg)
	}
	return candidates, nil
}

func setStatusMessageParents(xid int64, update *storage.WorkUnitExecutionUpdate) {
	statusVal := update.WorkUnitExecution.GetStatus()
	if statusVal == nil {
		return
	}
	parent := validator.ConstructWorkUnitName(xid, update.WID)
	setParentIfNonNil(statusVal.SystemMessage, parent)
	setParentIfNonNil(statusVal.UserMessage, parent)
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
		return false, fmt.Errorf("current message parent %q has invalid resource type %v: %w", currentMsg.Parent, resourceType, grpcerr.ErrInternal)
	}
	return false, nil
}

func collectUnmodifiedMessagesForSeverity(ctx context.Context, xid int64, sev essmsgpb.StructuredMessage_Severity, workUnits storage.WorkUnitTxRepository, updatedWUs map[int64]*essmsgpb.WorkUnitExecution) ([]*essmsgpb.StructuredMessage, error) {
	messages, err := workUnits.ListWorkUnitMessagesBySeverity(ctx, xid, sev)
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
