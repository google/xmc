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

	"golang/cloud_google_com/go/spanner/v/v1/spanner"
	"golang/godebug/diff/diff"
	"golang/protobuf/v2/encoding/prototext/prototext"
	"golang/protobuf/v2/proto/proto"
	"googlesql/safesql/go/safesql"
	fieldmaskpb "protobuf/field_mask_go_proto"
	timestamppb "protobuf/timestamp_go_proto"
	"task/go/status"
	essmsgpb "xmanager_cloud/experiment_state_server/proto/messages_go_proto"
	"xmanager_cloud/internal/storage/spanner/statusmessage"
	"xmanager_cloud/logging/logging"
	"xmanager_cloud/storage/data/data"
	"xmanager_cloud/storage/spanner/data/spannerdata"
	"xmanager_cloud/storage/spanner/utils/spannerutils"
	"xmanager_cloud/storage/state/state"
	"xmanager_cloud/storage/storage"
	"xmanager_cloud/validator/validator"
)

var getUpdatingCurrentWorkUnitExecutionSQL = safesql.New(`
	SELECT
		Xid,
		WorkUnitId,
		ExecutionId,
		ExecutionCreateTime AS CreateTime,
		ExecutionUpdateTime AS UpdateTime,
		ExecutionCompleteTime AS CompleteTime,
		ExecutionMetadata,
		WorkUnitStatus
	FROM
		WorkUnits
	WHERE
		BitReversedXid = @bitReversedXid AND WorkUnitId IN UNNEST(@workUnitIds)
	FOR UPDATE;
`)

// UpdateWorkUnitExecutions updates the executions of a work unit.
// TODO(b/517479143): Move work unit status update decision logic and status propagation outside the repository layer into a domain service layer.
// UpdateWorkUnitExecutions updates the executions of a work unit. It returns the canonical
// executions, either the updated executions or the original executions if no update was done.
func (r *Repository) UpdateWorkUnitExecutions(ctx context.Context, xid int64, updates []*storage.WorkUnitExecutionUpdate) (map[int64]*essmsgpb.WorkUnitExecution, error) {
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
	exp := &essmsgpb.Experiment{}
	_, err := r.client.ReadWriteTransaction(ctx, func(ctx context.Context, txn *spanner.ReadWriteTransaction) error {
		// 1. Read all WorkUnits from storage in a single batch. This also locks the rows for the
		// duration of the transaction using the `FOR UPDATE` hint.
		stmt := spanner.NewStatement(getUpdatingCurrentWorkUnitExecutionSQL.String())
		stmt.Params["bitReversedXid"] = spannerutils.ReverseBits(xid)
		stmt.Params["workUnitIds"] = wids

		iter := txn.Query(ctx, stmt)
		defer iter.Stop()

		// 1.1. Process the rows and build a map of WorkUnitExecutions keyed by WorkUnitId.
		updatedExecutions = make(map[int64]*essmsgpb.WorkUnitExecution)
		if err := iter.Do(func(row *spanner.Row) error {
			workUnitExecution, err := spannerdata.WorkUnitExecutionRowToProto(row)
			if err != nil {
				return fmt.Errorf("failed to convert row to WorkUnitExecution proto: %w", err)
			}

			_, wid, _, err := validator.ValidateWorkUnitExecutionName(workUnitExecution.GetName())
			if err != nil {
				return fmt.Errorf("failed to validate WorkUnitExecution name %q: %w", workUnitExecution.GetName(), err)
			}

			updatedExecutions[wid] = workUnitExecution
			return nil
		}); err != nil {
			return err
		}

		originalExecutions := make(map[int64]*essmsgpb.WorkUnitExecution)
		for wid, execution := range updatedExecutions {
			originalExecutions[wid] = proto.Clone(execution).(*essmsgpb.WorkUnitExecution)
		}

		// 2. Process each update. The updates should be idempotent to the processing order, but if we
		// have duplicates, we don't want to end up with a mutation ordering dependency issue. So we
		// process them all and then update the database in one go.
		var executionDeltas []*state.WorkUnitStateDelta
		for _, update := range updates {
			currentWorkUnitExecution := updatedExecutions[update.WID]
			if currentWorkUnitExecution == nil {
				return fmt.Errorf("work unit execution %d not found: %w", update.WID, status.ErrNotFound)
			}

			ctx := logging.WithWorkUnitExecutionName(ctx, currentWorkUnitExecution.GetName())

			_, _, curEID, err := validator.ValidateWorkUnitExecutionName(currentWorkUnitExecution.GetName())
			if err != nil {
				return fmt.Errorf("failed to validate work unit execution name %q: %w", currentWorkUnitExecution.GetName(), err)
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
				return fmt.Errorf("failed to update work unit execution: %w", err)
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

			statusmessage.SaveStatusMessageHistoryIfChanged(ctx, r.statusMessageHistoryEnabled, xid, &update.WID, &update.EID, updatedExecutions[update.WID].GetStatus().GetSystemMessage(), updatedExecution.GetStatus().GetSystemMessage(), txn)
			statusmessage.SaveStatusMessageHistoryIfChanged(ctx, r.statusMessageHistoryEnabled, xid, &update.WID, &update.EID, updatedExecutions[update.WID].GetStatus().GetUserMessage(), updatedExecution.GetStatus().GetUserMessage(), txn)

			updatedExecutions[update.WID] = updatedExecution
		}

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

			txn.BufferWrite([]*spanner.Mutation{
				spannerdata.CurrentWorkUnitExecutionProtoToUpdateMutation(xid, wid, eid, execution),
			})
			logging.Info(ctx, fmt.Sprintf("Updated work unit execution: %s", protoDiff), "execution", execution)
		}

		if r.experimentRepo == nil {
			return fmt.Errorf("experiment repository is not initialized")
		}
		// 3. Read the Experiment row.
		var err error
		exp, err = r.experimentRepo.FetchExperimentTransaction(ctx, xid, txn)
		if err != nil {
			return fmt.Errorf("failed to fetch experiment: %w", err)
		}
		if exp == nil {
			return fmt.Errorf("experiment %d not found", xid)
		}

		// 3.1. Clone the experiment status so we can compare it to the updated status later.
		currentExpStatus := proto.Clone(exp.Status).(*essmsgpb.ExperimentStatus)

		// 4. Update the denormalized status fields in the Experiment row.
		updatedExp, err := state.DenormalizeWorkUnitStatus(ctx, exp, executionDeltas)
		if err != nil {
			return fmt.Errorf("failed to update experiment status: %w", err)
		}

		// 4.1. Determine the new experiment status message.
		newExpMsg, err := r.findExperimentStatusMessage(ctx, xid, txn, updatedExp, updatedExecutions)
		if err != nil {
			return fmt.Errorf("failed to find experiment status message: %w", err)
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

			expStatusMutation, err := spannerdata.ExperimentProtoToStatusUpdateMutation(updatedExp)
			if err != nil {
				logging.Error(ctx, "Failed to create mutation for experiment status update", "error", err)
				return fmt.Errorf("failed to create mutation for experiment status update: %w", err)
			}

			protoDiff := diff.Diff(prototext.Format(currentExpStatus), prototext.Format(updatedExp.Status))
			logging.Info(ctx, fmt.Sprintf("Updated experiment status: %s", protoDiff), "updatedExpStatus", updatedExp.Status)

			txn.BufferWrite([]*spanner.Mutation{expStatusMutation})
		}

		return nil
	})
	if err != nil {
		return nil, err
	}

	logging.Info(ctx, "Updated work unit executions", "updatedExecutions", updatedExecutions)
	logging.Info(ctx, "Updated Experiment", "exp", exp)

	return updatedExecutions, nil
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

func (r *Repository) findExperimentStatusMessage(ctx context.Context, xid int64, txn spannerTransaction, updatedExp *essmsgpb.Experiment, updatedWUs map[int64]*essmsgpb.WorkUnitExecution) (*essmsgpb.StructuredMessage, error) {
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
	isCurrentMsgUpdated := false
	if currentMsg != nil {
		resourceType := validator.GetResourceType(currentMsg.Parent)

		// If the current message is a work unit message, check if it's being updated. If it's an
		// experiment message, it's always being updated, so we don't need to check.
		if resourceType == validator.WorkUnitResource {
			_, wid, err := validator.ValidateWorkUnitName(currentMsg.Parent)
			if err != nil {
				return nil, fmt.Errorf("failed to validate work unit name from message parent %q: %w", currentMsg.Parent, err)
			}
			_, isCurrentMsgUpdated = updatedWUs[wid]
		} else if resourceType != validator.ExperimentResource {
			return nil, fmt.Errorf("current message parent %q has invalid resource type %v: %w", currentMsg.Parent, resourceType, status.ErrInternal)
		}
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
		} else {
			// If the current message is being updated, and there are existing messages of this severity,
			// we need to check the entire database for messages with this severity.
			if count := severityCounts[sev]; count > 0 {
				messages, err := r.ListWorkUnitMessagesBySeverity(ctx, xid, sev, txn)
				if err != nil {
					return nil, err
				}

				for _, msg := range messages {
					if msg == nil {
						continue
					}
					_, wid, err := validator.ValidateWorkUnitName(msg.GetParent())
					if err != nil {
						return nil, fmt.Errorf("failed to validate work unit name from message parent %q: %w", msg.GetParent(), err)
					}

					// If the work unit has been updated, don't include its message in the candidates. We need to
					// add the updated message to the candidates list below, because they haven't been updated
					// yet.
					if _, ok := updatedWUs[wid]; ok {
						continue
					}
					candidates = append(candidates, msg)
				}
			}
		}

		// Add the updated messages to the candidates list. They haven't been committed yet, so they
		// won't be returned by the query.
		for _, wu := range updatedWUs {
			msg := state.GetBestMessageFromStatus(wu.GetStatus())
			if msg == nil || msg.GetSeverity() != sev {
				continue
			}
			candidates = append(candidates, msg)
		}

		// Find the best status message from the candidates and return it.
		if msg := state.FindBestStatusMessage(candidates); msg != nil {
			return msg, nil
		}
	}

	return nil, nil
}

func setParentIfNonNil(msg *essmsgpb.StructuredMessage, parent string) {
	if msg != nil {
		msg.Parent = parent
	}
}
