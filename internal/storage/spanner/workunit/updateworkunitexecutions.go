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
		stmt := spanner.NewStatement(getUpdatingCurrentWorkUnitExecutionSQL.String())
		stmt.Params["bitReversedXid"] = spannerutils.ReverseBits(xid)
		stmt.Params["workUnitIds"] = wids

		iter := txn.Query(ctx, stmt)
		defer iter.Stop()

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

			if curEID != update.EID {
				continue
			}

			statusVal := update.WorkUnitExecution.Status
			if statusVal != nil {
				parent := validator.ConstructWorkUnitName(xid, update.WID)
				setParentIfNonNil(statusVal.SystemMessage, parent)
				setParentIfNonNil(statusVal.UserMessage, parent)
			}

			updatedExecution, err := updateWorkUnitExecution(ctx, updatedExecutions[update.WID], update.WorkUnitExecution, update.UpdateMask, update.ForceStatusMessageUpdate, update.DeferToExistingMessage)
			if err != nil {
				return fmt.Errorf("failed to update work unit execution: %w", err)
			}

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
		var err error
		exp, err = r.experimentRepo.FetchExperimentTransaction(ctx, xid, txn)
		if err != nil {
			return fmt.Errorf("failed to fetch experiment: %w", err)
		}
		if exp == nil {
			return fmt.Errorf("experiment %d not found", xid)
		}

		currentExpStatus := proto.Clone(exp.Status).(*essmsgpb.ExperimentStatus)

		updatedExp, err := state.DenormalizeWorkUnitStatus(ctx, exp, executionDeltas)
		if err != nil {
			return fmt.Errorf("failed to update experiment status: %w", err)
		}

		newExpMsg, err := r.findExperimentStatusMessage(ctx, xid, txn, updatedExp, updatedExecutions)
		if err != nil {
			return fmt.Errorf("failed to find experiment status message: %w", err)
		}
		updatedExp.Status.StructuredStatusMessage = newExpMsg

		if !proto.Equal(currentExpStatus, updatedExp.Status) {
			logging.Info(ctx, "Update experiment status", "updatedExp", updatedExp.Status)
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
	severities := []essmsgpb.StructuredMessage_Severity{
		essmsgpb.StructuredMessage_FATAL,
		essmsgpb.StructuredMessage_ERROR,
		essmsgpb.StructuredMessage_WARNING,
	}

	currentMsg := updatedExp.Status.StructuredStatusMessage
	isCurrentMsgUpdated := false
	if currentMsg != nil {
		resourceType := validator.GetResourceType(currentMsg.Parent)

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

	severityCounts := make(map[essmsgpb.StructuredMessage_Severity]int64)
	for _, severityCount := range updatedExp.Status.MessageSeverityCounts {
		severityCounts[severityCount.GetSeverity()] = severityCount.GetCount()
	}

	for _, sev := range severities {
		hasCurrentMsg := currentMsg != nil && !isCurrentMsgUpdated && currentMsg.GetSeverity() == sev
		if severityCounts[sev] == 0 && !hasCurrentMsg {
			continue
		}

		var candidates []*essmsgpb.StructuredMessage

		if !isCurrentMsgUpdated {
			if hasCurrentMsg {
				candidates = append(candidates, currentMsg)
			}
		} else {
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

					if _, ok := updatedWUs[wid]; ok {
						continue
					}
					candidates = append(candidates, msg)
				}
			}
		}

		for _, wu := range updatedWUs {
			msg := state.GetBestMessageFromStatus(wu.GetStatus())
			if msg == nil || msg.GetSeverity() != sev {
				continue
			}
			candidates = append(candidates, msg)
		}

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
