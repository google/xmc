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

package experiment_test

import (
	"context"
	"strings"
	"testing"

	cloudspanner "golang/cloud_google_com/go/spanner/v/v1/spanner"
	essmsgpb "xmanager_cloud/experiment_state_server/proto/messages_go_proto"
	"xmanager_cloud/internal/storage/spanner"
	"xmanager_cloud/internal/storage/spanner/emulator"
	"xmanager_cloud/storage/spanner/utils/spannerutils"
	"xmanager_cloud/validator/validator"
)

func newTestStorageWithComponents(ctx context.Context, t *testing.T, essClient *cloudspanner.Client) *spanner.Storage {
	t.Helper()
	storage, err := spanner.NewStorageWithConfig(essClient, spanner.StorageConfig{EnableComponents: true})
	if err != nil {
		t.Fatalf("NewStorageWithConfig failed: %v", err)
	}
	return storage
}

func TestDriftDetection_ConsistentSnapshots(t *testing.T) {
	ctx := context.Background()
	_, essClient := emulator.Setup(ctx, t)
	storage := newTestStorageWithComponents(ctx, t, essClient)

	const xid = int64(1001)
	createdExp := spanner.CreateBlankExperiment(ctx, t, storage, xid)

	componentMutation := cloudspanner.InsertOrUpdate("Components",
		[]string{"ComponentId", "Name", "ComponentClass", "ConfigProto", "State", "Generation", "ObservedGeneration", "CreateTime", "UpdateTime"},
		[]any{int64(201), "clickhouse", "storage", []byte{}, int64(1), int64(1), int64(1), cloudspanner.CommitTimestamp, cloudspanner.CommitTimestamp},
	)
	snapshotMutation := cloudspanner.InsertOrUpdate("ExperimentComponentSnapshots",
		[]string{"BitReversedXid", "ComponentId", "LockedGeneration", "ConfigProto"},
		[]any{spannerutils.ReverseBits(xid), int64(201), int64(1), []byte{}},
	)
	if _, err := essClient.Apply(ctx, []*cloudspanner.Mutation{componentMutation, snapshotMutation}); err != nil {
		t.Fatalf("Apply mutations failed: %v", err)
	}

	parsedXID, err := validator.ValidateExperimentName(createdExp.GetName())
	if err != nil {
		t.Fatalf("ValidateExperimentName failed: %v", err)
	}

	gotExp, err := storage.FetchExperiment(ctx, parsedXID)
	if err != nil {
		t.Fatalf("FetchExperiment failed: %v", err)
	}
	if gotExp.GetStatus().GetStructuredStatusMessage() != nil {
		t.Errorf("Expected nil structured status message, got: %v", gotExp.GetStatus().GetStructuredStatusMessage())
	}

	gotStatus, err := storage.FetchExperimentStatus(ctx, parsedXID)
	if err != nil {
		t.Fatalf("FetchExperimentStatus failed: %v", err)
	}
	if gotStatus.GetStructuredStatusMessage() != nil {
		t.Errorf("Expected nil status message, got: %v", gotStatus.GetStructuredStatusMessage())
	}
}

func TestDriftDetection_GenerationDiverged(t *testing.T) {
	ctx := context.Background()
	_, essClient := emulator.Setup(ctx, t)
	storage := newTestStorageWithComponents(ctx, t, essClient)

	const xid = int64(1002)
	createdExp := spanner.CreateBlankExperiment(ctx, t, storage, xid)

	// Component is at generation 2 with observed generation 2, but experiment snapshot locked generation 1.
	componentMutation := cloudspanner.InsertOrUpdate("Components",
		[]string{"ComponentId", "Name", "ComponentClass", "ConfigProto", "State", "Generation", "ObservedGeneration", "CreateTime", "UpdateTime"},
		[]any{int64(202), "redpanda", "messaging", []byte{}, int64(1), int64(2), int64(2), cloudspanner.CommitTimestamp, cloudspanner.CommitTimestamp},
	)
	snapshotMutation := cloudspanner.InsertOrUpdate("ExperimentComponentSnapshots",
		[]string{"BitReversedXid", "ComponentId", "LockedGeneration", "ConfigProto"},
		[]any{spannerutils.ReverseBits(xid), int64(202), int64(1), []byte{}},
	)
	if _, err := essClient.Apply(ctx, []*cloudspanner.Mutation{componentMutation, snapshotMutation}); err != nil {
		t.Fatalf("Apply mutations failed: %v", err)
	}

	parsedXID, err := validator.ValidateExperimentName(createdExp.GetName())
	if err != nil {
		t.Fatalf("ValidateExperimentName failed: %v", err)
	}

	gotExp, err := storage.FetchExperiment(ctx, parsedXID)
	if err != nil {
		t.Fatalf("FetchExperiment failed: %v", err)
	}

	msg := gotExp.GetStatus().GetStructuredStatusMessage()
	if msg == nil {
		t.Fatalf("Expected non-nil structured status message for drifted experiment")
	}
	if got, want := msg.GetSeverity(), essmsgpb.StructuredMessage_WARNING; got != want {
		t.Errorf("Message severity = %v, want %v", got, want)
	}
	if got, want := msg.GetSource(), essmsgpb.StructuredMessage_XMANAGER_BACKEND; got != want {
		t.Errorf("Message source = %v, want %v", got, want)
	}
	expectedSubstring := "redpanda (snapshot generation 1 != live observed generation 2)"
	if !strings.Contains(msg.GetUserFacingText(), expectedSubstring) {
		t.Errorf("UserFacingText %q does not contain %q", msg.GetUserFacingText(), expectedSubstring)
	}

	gotStatus, err := storage.FetchExperimentStatus(ctx, parsedXID)
	if err != nil {
		t.Fatalf("FetchExperimentStatus failed: %v", err)
	}
	statusMsg := gotStatus.GetStructuredStatusMessage()
	if statusMsg == nil || !strings.Contains(statusMsg.GetUserFacingText(), expectedSubstring) {
		t.Errorf("FetchExperimentStatus message %v does not contain %q", statusMsg, expectedSubstring)
	}
}

func TestDriftDetection_ComponentSoftDeleted(t *testing.T) {
	ctx := context.Background()
	_, essClient := emulator.Setup(ctx, t)
	storage := newTestStorageWithComponents(ctx, t, essClient)

	const xid = int64(1003)
	createdExp := spanner.CreateBlankExperiment(ctx, t, storage, xid)

	// State = 5 indicates COMPONENT_STATE_DELETED.
	componentMutation := cloudspanner.InsertOrUpdate("Components",
		[]string{"ComponentId", "Name", "ComponentClass", "ConfigProto", "State", "Generation", "ObservedGeneration", "CreateTime", "UpdateTime"},
		[]any{int64(203), "clickhouse", "storage", []byte{}, int64(5), int64(2), int64(2), cloudspanner.CommitTimestamp, cloudspanner.CommitTimestamp},
	)
	snapshotMutation := cloudspanner.InsertOrUpdate("ExperimentComponentSnapshots",
		[]string{"BitReversedXid", "ComponentId", "LockedGeneration", "ConfigProto"},
		[]any{spannerutils.ReverseBits(xid), int64(203), int64(1), []byte{}},
	)
	if _, err := essClient.Apply(ctx, []*cloudspanner.Mutation{componentMutation, snapshotMutation}); err != nil {
		t.Fatalf("Apply mutations failed: %v", err)
	}

	parsedXID, err := validator.ValidateExperimentName(createdExp.GetName())
	if err != nil {
		t.Fatalf("ValidateExperimentName failed: %v", err)
	}

	gotExp, err := storage.FetchExperiment(ctx, parsedXID)
	if err != nil {
		t.Fatalf("FetchExperiment failed: %v", err)
	}

	msg := gotExp.GetStatus().GetStructuredStatusMessage()
	if msg == nil {
		t.Fatalf("Expected non-nil structured status message for soft-deleted component")
	}
	expectedSubstring := "clickhouse (deleted from catalog, snapshot generation 1)"
	if !strings.Contains(msg.GetUserFacingText(), expectedSubstring) {
		t.Errorf("UserFacingText %q does not contain %q", msg.GetUserFacingText(), expectedSubstring)
	}
}

func TestDriftDetection_TerminalExperimentFiltered(t *testing.T) {
	ctx := context.Background()
	_, essClient := emulator.Setup(ctx, t)
	storage := newTestStorageWithComponents(ctx, t, essClient)

	const xid = int64(1004)
	expProto := spanner.BlankExperimentProto(xid)
	expProto.Status.ActiveState = essmsgpb.ExperimentStatus_INACTIVE
	createdExp := spanner.CreateAndStoreExperiment(ctx, t, storage, expProto)

	// Live component diverged.
	componentMutation := cloudspanner.InsertOrUpdate("Components",
		[]string{"ComponentId", "Name", "ComponentClass", "ConfigProto", "State", "Generation", "ObservedGeneration", "CreateTime", "UpdateTime"},
		[]any{int64(204), "ingestion-gateway", "network", []byte{}, int64(1), int64(2), int64(2), cloudspanner.CommitTimestamp, cloudspanner.CommitTimestamp},
	)
	snapshotMutation := cloudspanner.InsertOrUpdate("ExperimentComponentSnapshots",
		[]string{"BitReversedXid", "ComponentId", "LockedGeneration", "ConfigProto"},
		[]any{spannerutils.ReverseBits(xid), int64(204), int64(1), []byte{}},
	)
	if _, err := essClient.Apply(ctx, []*cloudspanner.Mutation{componentMutation, snapshotMutation}); err != nil {
		t.Fatalf("Apply mutations failed: %v", err)
	}

	parsedXID, err := validator.ValidateExperimentName(createdExp.GetName())
	if err != nil {
		t.Fatalf("ValidateExperimentName failed: %v", err)
	}

	gotExp, err := storage.FetchExperiment(ctx, parsedXID)
	if err != nil {
		t.Fatalf("FetchExperiment failed: %v", err)
	}
	// Terminal experiments are filtered out by ActiveState = 1 in the consistency view.
	if gotExp.GetStatus().GetStructuredStatusMessage() != nil {
		t.Errorf("Expected nil structured status message for inactive experiment, got: %v", gotExp.GetStatus().GetStructuredStatusMessage())
	}
}

func TestDriftDetection_PendingReconciliationNotDrifted(t *testing.T) {
	ctx := context.Background()
	_, essClient := emulator.Setup(ctx, t)
	storage := newTestStorageWithComponents(ctx, t, essClient)

	const xid = int64(1005)
	createdExp := spanner.CreateBlankExperiment(ctx, t, storage, xid)

	// ObservedGeneration is NULL (pending initial GKE reconciliation).
	componentMutation := cloudspanner.InsertOrUpdate("Components",
		[]string{"ComponentId", "Name", "ComponentClass", "ConfigProto", "State", "Generation", "CreateTime", "UpdateTime"},
		[]any{int64(205), "pending-comp", "workload", []byte{}, int64(1), int64(1), cloudspanner.CommitTimestamp, cloudspanner.CommitTimestamp},
	)
	snapshotMutation := cloudspanner.InsertOrUpdate("ExperimentComponentSnapshots",
		[]string{"BitReversedXid", "ComponentId", "LockedGeneration", "ConfigProto"},
		[]any{spannerutils.ReverseBits(xid), int64(205), int64(1), []byte{}},
	)
	if _, err := essClient.Apply(ctx, []*cloudspanner.Mutation{componentMutation, snapshotMutation}); err != nil {
		t.Fatalf("Apply mutations failed: %v", err)
	}

	parsedXID, err := validator.ValidateExperimentName(createdExp.GetName())
	if err != nil {
		t.Fatalf("ValidateExperimentName failed: %v", err)
	}

	gotExp, err := storage.FetchExperiment(ctx, parsedXID)
	if err != nil {
		t.Fatalf("FetchExperiment failed: %v", err)
	}
	if gotExp.GetStatus().GetStructuredStatusMessage() != nil {
		t.Errorf("Expected nil structured status message during pending reconciliation, got: %v", gotExp.GetStatus().GetStructuredStatusMessage())
	}
}

func TestDriftDetection_NoSnapshots_Consistent(t *testing.T) {
	ctx := context.Background()
	_, essClient := emulator.Setup(ctx, t)
	storage := newTestStorageWithComponents(ctx, t, essClient)

	const xid = int64(1006)
	createdExp := spanner.CreateBlankExperiment(ctx, t, storage, xid)

	parsedXID, err := validator.ValidateExperimentName(createdExp.GetName())
	if err != nil {
		t.Fatalf("ValidateExperimentName failed: %v", err)
	}

	gotExp, err := storage.FetchExperiment(ctx, parsedXID)
	if err != nil {
		t.Fatalf("FetchExperiment failed: %v", err)
	}
	if gotExp.GetStatus().GetStructuredStatusMessage() != nil {
		t.Errorf("Expected nil structured status message for experiment with no snapshots, got: %v", gotExp.GetStatus().GetStructuredStatusMessage())
	}
}

func TestDriftDetection_MergeWithExistingLowerSeverityMessage(t *testing.T) {
	ctx := context.Background()
	_, essClient := emulator.Setup(ctx, t)
	storage := newTestStorageWithComponents(ctx, t, essClient)

	const xid = int64(1007)
	expProto := spanner.BlankExperimentProto(xid)
	expProto.Status.StructuredStatusMessage = &essmsgpb.StructuredMessage{
		Severity:       essmsgpb.StructuredMessage_INFO,
		Source:         essmsgpb.StructuredMessage_XMANAGER_BACKEND,
		UserFacingText: "All workers scheduled successfully",
	}
	createdExp := spanner.CreateAndStoreExperiment(ctx, t, storage, expProto)

	componentMutation := cloudspanner.InsertOrUpdate("Components",
		[]string{"ComponentId", "Name", "ComponentClass", "ConfigProto", "State", "Generation", "ObservedGeneration", "CreateTime", "UpdateTime"},
		[]any{int64(207), "ray-cluster", "compute", []byte{}, int64(1), int64(2), int64(2), cloudspanner.CommitTimestamp, cloudspanner.CommitTimestamp},
	)
	snapshotMutation := cloudspanner.InsertOrUpdate("ExperimentComponentSnapshots",
		[]string{"BitReversedXid", "ComponentId", "LockedGeneration", "ConfigProto"},
		[]any{spannerutils.ReverseBits(xid), int64(207), int64(1), []byte{}},
	)
	if _, err := essClient.Apply(ctx, []*cloudspanner.Mutation{componentMutation, snapshotMutation}); err != nil {
		t.Fatalf("Apply mutations failed: %v", err)
	}

	parsedXID, err := validator.ValidateExperimentName(createdExp.GetName())
	if err != nil {
		t.Fatalf("ValidateExperimentName failed: %v", err)
	}

	gotExp, err := storage.FetchExperiment(ctx, parsedXID)
	if err != nil {
		t.Fatalf("FetchExperiment failed: %v", err)
	}

	msg := gotExp.GetStatus().GetStructuredStatusMessage()
	if msg == nil {
		t.Fatalf("Expected non-nil structured status message")
	}
	if got, want := msg.GetSeverity(), essmsgpb.StructuredMessage_WARNING; got != want {
		t.Errorf("Severity = %v, want %v", got, want)
	}
	wantPrefix := "All workers scheduled successfully; Component drift detected"
	if !strings.HasPrefix(msg.GetUserFacingText(), wantPrefix) {
		t.Errorf("UserFacingText %q does not have prefix %q", msg.GetUserFacingText(), wantPrefix)
	}
}

func TestDriftDetection_PreserveHigherSeverityMessage(t *testing.T) {
	ctx := context.Background()
	_, essClient := emulator.Setup(ctx, t)
	storage := newTestStorageWithComponents(ctx, t, essClient)

	const xid = int64(1008)
	expProto := spanner.BlankExperimentProto(xid)
	expProto.Status.StructuredStatusMessage = &essmsgpb.StructuredMessage{
		Severity:       essmsgpb.StructuredMessage_ERROR,
		Source:         essmsgpb.StructuredMessage_XMANAGER_BACKEND,
		UserFacingText: "Fatal crash on worker 0",
	}
	createdExp := spanner.CreateAndStoreExperiment(ctx, t, storage, expProto)

	componentMutation := cloudspanner.InsertOrUpdate("Components",
		[]string{"ComponentId", "Name", "ComponentClass", "ConfigProto", "State", "Generation", "ObservedGeneration", "CreateTime", "UpdateTime"},
		[]any{int64(208), "storage-vol", "storage", []byte{}, int64(1), int64(2), int64(2), cloudspanner.CommitTimestamp, cloudspanner.CommitTimestamp},
	)
	snapshotMutation := cloudspanner.InsertOrUpdate("ExperimentComponentSnapshots",
		[]string{"BitReversedXid", "ComponentId", "LockedGeneration", "ConfigProto"},
		[]any{spannerutils.ReverseBits(xid), int64(208), int64(1), []byte{}},
	)
	if _, err := essClient.Apply(ctx, []*cloudspanner.Mutation{componentMutation, snapshotMutation}); err != nil {
		t.Fatalf("Apply mutations failed: %v", err)
	}

	parsedXID, err := validator.ValidateExperimentName(createdExp.GetName())
	if err != nil {
		t.Fatalf("ValidateExperimentName failed: %v", err)
	}

	gotExp, err := storage.FetchExperiment(ctx, parsedXID)
	if err != nil {
		t.Fatalf("FetchExperiment failed: %v", err)
	}

	msg := gotExp.GetStatus().GetStructuredStatusMessage()
	if msg == nil {
		t.Fatalf("Expected non-nil structured status message")
	}
	if got, want := msg.GetSeverity(), essmsgpb.StructuredMessage_ERROR; got != want {
		t.Errorf("Severity = %v, want %v", got, want)
	}
	if got, want := msg.GetUserFacingText(), "Fatal crash on worker 0"; got != want {
		t.Errorf("UserFacingText = %q, want %q", got, want)
	}
}

func TestDriftDetection_ComponentsDisabled_SkipsDriftCheck(t *testing.T) {
	ctx := context.Background()
	_, essClient := emulator.Setup(ctx, t)
	// Default NewStorage has EnableComponents = false.
	storage, err := spanner.NewStorage(essClient)
	if err != nil {
		t.Fatalf("NewStorage failed: %v", err)
	}

	const xid = int64(1099)
	createdExp := spanner.CreateBlankExperiment(ctx, t, storage, xid)

	// Live component is diverged.
	componentMutation := cloudspanner.InsertOrUpdate("Components",
		[]string{"ComponentId", "Name", "ComponentClass", "ConfigProto", "State", "Generation", "ObservedGeneration", "CreateTime", "UpdateTime"},
		[]any{int64(299), "diverged-comp", "storage", []byte{}, int64(1), int64(2), int64(2), cloudspanner.CommitTimestamp, cloudspanner.CommitTimestamp},
	)
	snapshotMutation := cloudspanner.InsertOrUpdate("ExperimentComponentSnapshots",
		[]string{"BitReversedXid", "ComponentId", "LockedGeneration", "ConfigProto"},
		[]any{spannerutils.ReverseBits(xid), int64(299), int64(1), []byte{}},
	)
	if _, err := essClient.Apply(ctx, []*cloudspanner.Mutation{componentMutation, snapshotMutation}); err != nil {
		t.Fatalf("Apply mutations failed: %v", err)
	}

	parsedXID, err := validator.ValidateExperimentName(createdExp.GetName())
	if err != nil {
		t.Fatalf("ValidateExperimentName failed: %v", err)
	}

	gotExp, err := storage.FetchExperiment(ctx, parsedXID)
	if err != nil {
		t.Fatalf("FetchExperiment failed: %v", err)
	}
	// Since components are disabled, drift check is skipped completely.
	if gotExp.GetStatus().GetStructuredStatusMessage() != nil {
		t.Errorf("Expected nil structured status message when EnableComponents is false, got: %v", gotExp.GetStatus().GetStructuredStatusMessage())
	}
}

func TestDriftDetection_CacheHitsWithinTTL(t *testing.T) {
	ctx := context.Background()
	_, essClient := emulator.Setup(ctx, t)
	storage := newTestStorageWithComponents(ctx, t, essClient)

	const xid = int64(1100)
	createdExp := spanner.CreateBlankExperiment(ctx, t, storage, xid)

	componentMutation := cloudspanner.InsertOrUpdate("Components",
		[]string{"ComponentId", "Name", "ComponentClass", "ConfigProto", "State", "Generation", "ObservedGeneration", "CreateTime", "UpdateTime"},
		[]any{int64(300), "cached-comp", "storage", []byte{}, int64(1), int64(2), int64(2), cloudspanner.CommitTimestamp, cloudspanner.CommitTimestamp},
	)
	snapshotMutation := cloudspanner.InsertOrUpdate("ExperimentComponentSnapshots",
		[]string{"BitReversedXid", "ComponentId", "LockedGeneration", "ConfigProto"},
		[]any{spannerutils.ReverseBits(xid), int64(300), int64(1), []byte{}},
	)
	if _, err := essClient.Apply(ctx, []*cloudspanner.Mutation{componentMutation, snapshotMutation}); err != nil {
		t.Fatalf("Apply mutations failed: %v", err)
	}

	parsedXID, err := validator.ValidateExperimentName(createdExp.GetName())
	if err != nil {
		t.Fatalf("ValidateExperimentName failed: %v", err)
	}

	// First call: populates the cache
	gotExp1, err := storage.FetchExperiment(ctx, parsedXID)
	if err != nil {
		t.Fatalf("FetchExperiment 1 failed: %v", err)
	}
	if gotExp1.GetStatus().GetStructuredStatusMessage() == nil {
		t.Fatalf("Expected non-nil structured status message on first fetch")
	}

	// Mutate component to match snapshot (observed = 1), so database would evaluate as consistent.
	resolveMutation := cloudspanner.InsertOrUpdate("Components",
		[]string{"ComponentId", "Name", "ComponentClass", "ConfigProto", "State", "Generation", "ObservedGeneration", "CreateTime", "UpdateTime"},
		[]any{int64(300), "cached-comp", "storage", []byte{}, int64(1), int64(1), int64(1), cloudspanner.CommitTimestamp, cloudspanner.CommitTimestamp},
	)
	if _, err := essClient.Apply(ctx, []*cloudspanner.Mutation{resolveMutation}); err != nil {
		t.Fatalf("Apply resolve mutation failed: %v", err)
	}

	// Second call: within 30s TTL window, hits in-memory cache and still returns cached drift warning
	gotExp2, err := storage.FetchExperiment(ctx, parsedXID)
	if err != nil {
		t.Fatalf("FetchExperiment 2 failed: %v", err)
	}
	if gotExp2.GetStatus().GetStructuredStatusMessage() == nil {
		t.Errorf("Expected cached drift warning to be returned from in-memory cache within TTL")
	}
}
