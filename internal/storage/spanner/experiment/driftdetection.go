// Copyright 2026 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package experiment

import (
	"context"
	"fmt"
	"strings"

	"golang/cloud_google_com/go/spanner/v/v1/spanner"
	"google.golang.org/api/iterator"
	"google.golang.org/protobuf/types/known/timestamppb"
	essmsgpb "xmanager_cloud/experiment_state_server/proto/messages_go_proto"
	"xmanager_cloud/logging/logging"
	"xmanager_cloud/storage/spanner/utils/spannerutils"
)

const (
	selectExperimentConsistencySQL = `SELECT IsExperimentConsistent
FROM ExperimentConsistencyInfo
WHERE BitReversedXid = @bitReversedXid`

	selectDriftedComponentsSQL = `SELECT c.Name, s.LockedGeneration, c.ObservedGeneration, c.State, s.ComponentId
FROM ExperimentComponentSnapshots s
LEFT JOIN@{JOIN_METHOD=APPLY_JOIN} Components c ON s.ComponentId = c.ComponentId
WHERE s.BitReversedXid = @bitReversedXid
  AND (c.ComponentId IS NULL OR c.State = @deletedState OR (c.ObservedGeneration IS NOT NULL AND s.LockedGeneration != c.ObservedGeneration))
ORDER BY c.Name ASC`

	componentStateDeleted = 5
	maxDriftDetails       = 10
)

// evaluateExperimentDrift checks whether an active experiment's component snapshots have diverged
// from live component catalog generations. If drift is detected, a warning notice is attached to the status.
func (r *Repository) evaluateExperimentDrift(ctx context.Context, txn SpannerTransaction, xid int64, expStatus *essmsgpb.ExperimentStatus) error {
	if expStatus == nil || expStatus.GetActiveState() != essmsgpb.ExperimentStatus_ACTIVE {
		return nil
	}

	isConsistent, err := checkConsistency(ctx, txn, xid)
	if err != nil {
		return fmt.Errorf("failed to evaluate consistency: %w", err)
	}
	if isConsistent {
		return nil
	}

	diffs, err := fetchDriftDetails(ctx, txn, xid)
	if err != nil {
		return fmt.Errorf("failed to fetch drift details: %w", err)
	}

	driftMsg := formatDriftWarningMessage(diffs)
	attachDriftWarningMessage(expStatus, driftMsg)
	logging.Info(ctx, "Attached component drift warning notice", "xid", xid, "warning", driftMsg.GetUserFacingText())

	return nil
}

// checkConsistency queries the ExperimentConsistencyInfo Spanner view using primary key BitReversedXid.
func checkConsistency(ctx context.Context, txn SpannerTransaction, xid int64) (bool, error) {
	stmt := spanner.Statement{
		SQL: selectExperimentConsistencySQL,
		Params: map[string]any{
			"bitReversedXid": spannerutils.ReverseBits(xid),
		},
	}
	iter := txn.Query(ctx, stmt)
	defer iter.Stop()

	row, err := iter.Next()
	if err == iterator.Done {
		// Experiment is inactive or not found in active view; treat as consistent.
		return true, nil
	}
	if err != nil {
		return false, fmt.Errorf("querying consistency view failed for xid %d: %w", xid, err)
	}

	var isConsistent bool
	if err := row.ColumnByName("IsExperimentConsistent", &isConsistent); err != nil {
		return false, fmt.Errorf("failed reading column 'IsExperimentConsistent': %w", err)
	}
	return isConsistent, nil
}

// fetchDriftDetails queries drifted component generation comparisons for an experiment.
func fetchDriftDetails(ctx context.Context, txn SpannerTransaction, xid int64) ([]string, error) {
	stmt := spanner.Statement{
		SQL: selectDriftedComponentsSQL,
		Params: map[string]any{
			"bitReversedXid": spannerutils.ReverseBits(xid),
			"deletedState":   componentStateDeleted,
		},
	}
	iter := txn.Query(ctx, stmt)
	defer iter.Stop()

	var diffs []string
	for {
		row, err := iter.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("reading drifted component row failed: %w", err)
		}
		if len(diffs) >= maxDriftDetails {
			diffs = append(diffs, "...")
			break
		}

		diff, err := formatComponentDiff(row)
		if err != nil {
			return nil, err
		}
		diffs = append(diffs, diff)
	}
	return diffs, nil
}

// formatComponentDiff parses a single drifted component row and formats its description.
func formatComponentDiff(row *spanner.Row) (string, error) {
	var name spanner.NullString
	var lockedGen int64
	var observedGen spanner.NullInt64
	var state spanner.NullInt64
	var componentID int64

	if err := row.ColumnByName("Name", &name); err != nil {
		return "", fmt.Errorf("failed reading column 'Name': %w", err)
	}
	if err := row.ColumnByName("LockedGeneration", &lockedGen); err != nil {
		return "", fmt.Errorf("failed reading column 'LockedGeneration': %w", err)
	}
	if err := row.ColumnByName("ObservedGeneration", &observedGen); err != nil {
		return "", fmt.Errorf("failed reading column 'ObservedGeneration': %w", err)
	}
	if err := row.ColumnByName("State", &state); err != nil {
		return "", fmt.Errorf("failed reading column 'State': %w", err)
	}
	if err := row.ColumnByName("ComponentId", &componentID); err != nil {
		return "", fmt.Errorf("failed reading column 'ComponentId': %w", err)
	}

	if !name.Valid {
		return fmt.Sprintf("Component ID %d (missing from catalog, snapshot generation %d)", componentID, lockedGen), nil
	}
	if state.Valid && state.Int64 == componentStateDeleted {
		return fmt.Sprintf("%s (deleted from catalog, snapshot generation %d)", name.StringVal, lockedGen), nil
	}
	return fmt.Sprintf("%s (snapshot generation %d != live observed generation %d)", name.StringVal, lockedGen, observedGen.Int64), nil
}

// formatDriftWarningMessage constructs a StructuredMessage with WARNING severity.
func formatDriftWarningMessage(diffs []string) *essmsgpb.StructuredMessage {
	warningText := "Component drift detected: live component generations diverge from snapshots"
	if len(diffs) > 0 {
		warningText = fmt.Sprintf("%s: %s", warningText, strings.Join(diffs, ", "))
	}
	return &essmsgpb.StructuredMessage{
		Severity:       essmsgpb.StructuredMessage_WARNING,
		Source:         essmsgpb.StructuredMessage_XMANAGER_BACKEND,
		UserFacingText: warningText,
		CreateTime:     timestamppb.Now(),
	}
}

// attachDriftWarningMessage safely sets or merges the drift notice into expStatus.
func attachDriftWarningMessage(expStatus *essmsgpb.ExperimentStatus, driftMsg *essmsgpb.StructuredMessage) {
	currentMsg := expStatus.GetStructuredStatusMessage()
	if currentMsg == nil || currentMsg.GetSeverity() < essmsgpb.StructuredMessage_WARNING {
		expStatus.StructuredStatusMessage = driftMsg
		return
	}
	if currentMsg.GetSeverity() != essmsgpb.StructuredMessage_WARNING || strings.Contains(currentMsg.GetUserFacingText(), "Component drift detected") {
		return
	}
	if currentMsg.GetUserFacingText() == "" {
		currentMsg.UserFacingText = driftMsg.GetUserFacingText()
		return
	}
	currentMsg.UserFacingText = fmt.Sprintf("%s; %s", currentMsg.GetUserFacingText(), driftMsg.GetUserFacingText())
}
