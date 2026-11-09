// Copyright 2025 Google LLC
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
	"errors"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/testing/protocmp"
	essmsgpb "xmanager_cloud/api/protos/experiment"
	"xmanager_cloud/internal/experiment"
	"xmanager_cloud/internal/experiment/experimenttest"
	"xmanager_cloud/internal/grpcerr"
	"xmanager_cloud/internal/storage"
	"xmanager_cloud/internal/storage/spanner"
	"xmanager_cloud/internal/storage/spanner/emulator"
)

func TestCreateExperiment(t *testing.T) {
	ctx := context.Background()
	_, essClient := emulator.Setup(ctx, t)
	storage, _ := spanner.NewStorage(essClient)

	exp := &essmsgpb.Experiment{
		Name:    "experiments/123",
		Title:   "test-title",
		Creator: "test-creator",
		Notes:   "test-notes",
		Tags:    []string{"tag1", "tag2"},
		Urls: []*essmsgpb.Url{
			{
				Url: "http://test.com",
			},
		},
		Resources: []*essmsgpb.ResourceSet{},
		Status: &essmsgpb.ExperimentStatus{
			ActiveState: essmsgpb.ExperimentStatus_ACTIVE,
		},
	}

	createdExp, err := experiment.CreateExperiment(ctx, storage, exp)
	if err != nil {
		t.Fatalf("CreateExperiment failed: %v", err)
	}

	if createdExp.GetName() != exp.GetName() {
		t.Errorf("CreateExperiment returned experiment with wrong name: got %q, want %q", createdExp.GetName(), exp.GetName())
	}
	if createdExp.GetCreateTime() == nil {
		t.Errorf("CreateExperiment returned experiment with nil CreateTime")
	}
	if createdExp.GetUpdateTime() == nil {
		t.Errorf("CreateExperiment returned experiment with nil UpdateTime")
	}
	if createdExp.GetStatus().GetLastChildStatusUpdateTime() == nil {
		t.Errorf("CreateExperiment returned experiment with nil LastChildStatusUpdateTime in Status")
	}
	if createdExp.GetStatus().GetUpdateTime() == nil {
		t.Errorf("CreateExperiment returned experiment with nil UpdateTime in Status")
	}
	if createdExp.GetCreator() != exp.GetCreator() {
		t.Errorf("CreateExperiment returned experiment with wrong creator: got %q, want %q", createdExp.GetCreator(), exp.GetCreator())
	}
}

func TestCreateExperiment_InvalidName(t *testing.T) {
	ctx := context.Background()
	_, essClient := emulator.Setup(ctx, t)
	storage, _ := spanner.NewStorage(essClient)

	exp := &essmsgpb.Experiment{
		Name: "invalid-name",
	}

	_, err := experiment.CreateExperiment(ctx, storage, exp)
	if err == nil {
		t.Errorf("CreateExperiment with invalid name succeeded, want error")
	}
}

func TestCreateExperiment_NilExperiment(t *testing.T) {
	ctx := context.Background()
	_, essClient := emulator.Setup(ctx, t)
	storage, _ := spanner.NewStorage(essClient)

	_, err := experiment.CreateExperiment(ctx, storage, nil)
	if err == nil {
		t.Errorf("CreateExperiment(nil) succeeded, want error")
	}
}

func TestCreateExperiment_InitializesStatusIfNil(t *testing.T) {
	ctx := context.Background()
	_, essClient := emulator.Setup(ctx, t)
	storage, _ := spanner.NewStorage(essClient)

	exp := &essmsgpb.Experiment{
		Name:      "experiments/456",
		Title:     "test-title",
		Creator:   "test-creator",
		Tags:      []string{},
		Urls:      []*essmsgpb.Url{},
		Resources: []*essmsgpb.ResourceSet{},
		Status:    nil,
	}

	createdExp, err := experiment.CreateExperiment(ctx, storage, exp)
	if err != nil {
		t.Fatalf("CreateExperiment failed: %v", err)
	}
	if createdExp.GetStatus() == nil {
		t.Fatalf("CreateExperiment did not initialize nil status")
	}
	if createdExp.GetStatus().GetUpdateTime() == nil {
		t.Errorf("CreateExperiment returned experiment with nil Status.UpdateTime")
	}
}

func TestCreateExperiment_WithStructuredStatusMessage(t *testing.T) {
	ctx := context.Background()
	_, essClient := emulator.Setup(ctx, t)
	storage, _ := spanner.NewStorage(essClient)

	exp := &essmsgpb.Experiment{
		Name:      "experiments/789",
		Title:     "test-title",
		Creator:   "test-creator",
		Tags:      []string{},
		Urls:      []*essmsgpb.Url{},
		Resources: []*essmsgpb.ResourceSet{},
		Status: &essmsgpb.ExperimentStatus{
			ActiveState: essmsgpb.ExperimentStatus_ACTIVE,
			StructuredStatusMessage: &essmsgpb.StructuredMessage{
				Severity:       essmsgpb.StructuredMessage_INFO,
				UserFacingText: "initial status message",
				Parent:         "experiments/789",
			},
		},
	}

	createdExp, err := experiment.CreateExperiment(ctx, storage, exp)
	if err != nil {
		t.Fatalf("CreateExperiment failed: %v", err)
	}

	if got := createdExp.GetStatus().GetStructuredStatusMessage().GetUserFacingText(); got != "initial status message" {
		t.Errorf("CreateExperiment returned message %q, want %q", got, "initial status message")
	}

	messages, _, err := storage.ListStatusMessages(ctx, "experiments/789", 10, "", "", "", 0)
	if err != nil {
		t.Fatalf("ListStatusMessages failed: %v", err)
	}
	if len(messages) != 1 {
		t.Fatalf("ListStatusMessages returned %d messages, want 1", len(messages))
	}
	if got := messages[0].GetContent().GetUserFacingText(); got != "initial status message" {
		t.Errorf("ListStatusMessages returned message text %q, want %q", got, "initial status message")
	}
}

func TestCreateExperiment_SaveStatusMessageHistoryError(t *testing.T) {
	ctx := context.Background()
	boomErr := errors.New("boom")
	mockStorage := &storage.MockStorage{
		SaveStatusMessageHistoryErr: boomErr,
	}

	exp := &essmsgpb.Experiment{
		Name: "experiments/789",
		Status: &essmsgpb.ExperimentStatus{
			StructuredStatusMessage: &essmsgpb.StructuredMessage{
				Severity:       essmsgpb.StructuredMessage_INFO,
				UserFacingText: "initial status message",
			},
		},
	}

	_, err := experiment.CreateExperiment(ctx, mockStorage, exp)
	if !errors.Is(err, boomErr) {
		t.Fatalf("CreateExperiment() error = %v, want %v", err, boomErr)
	}
}

// studyRequest returns a study whose max_trial_budget is budget.
func studyRequest(budget int32) *essmsgpb.Experiment {
	cfg := experimenttest.StudyConfig()
	cfg.MaxTrialBudget = budget
	return &essmsgpb.Experiment{
		Name:           "experiments/321",
		ExperimentType: essmsgpb.Experiment_STUDY,
		StudyConfig:    cfg,
		Status:         &essmsgpb.ExperimentStatus{ActiveState: essmsgpb.ExperimentStatus_ACTIVE},
	}
}

// catalogWithTrialBudget returns a ready hpo catalog whose hpo maxTrialBudget parameter is limit.
func catalogWithTrialBudget(limit string) *storage.MockComponentRepository {
	return &storage.MockComponentRepository{Records: experimenttest.HPOCatalog(map[string]string{"maxTrialBudget": limit})}
}

func TestCreateExperiment_ClampsStudyTrialBudget(t *testing.T) {
	tests := []struct {
		name        string
		limit       string
		budget      int32
		wantBudget  int32
		wantWarning string
	}{
		{name: "above limit", limit: "5", budget: 10, wantBudget: 5, wantWarning: "max_trial_budget 10 exceeds the hpo component limit; clamped to 5"},
		{name: "at limit", limit: "10", budget: 10, wantBudget: 10},
		{name: "malformed limit falls back to the default", limit: "lots", budget: 2000, wantBudget: 1000,
			wantWarning: "max_trial_budget 2000 exceeds the hpo component limit; clamped to 1000"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			history := &storage.MockStatusMessageTxRepository{}
			catalog := catalogWithTrialBudget(tc.limit)
			st := &storage.MockStorage{ComponentRepository: catalog, StatusMessageTxRepository: history}

			got, err := experiment.CreateExperiment(context.Background(), st, studyRequest(tc.budget))
			if err != nil {
				t.Fatalf("CreateExperiment() failed: %v", err)
			}

			for desc, exp := range map[string]*essmsgpb.Experiment{"returned": got, "inserted": st.Experiment} {
				if budget := exp.GetStudyConfig().GetMaxTrialBudget(); budget != tc.wantBudget {
					t.Errorf("%s max_trial_budget = %d, want %d", desc, budget, tc.wantBudget)
				}
				if text := exp.GetStatus().GetStructuredStatusMessage().GetUserFacingText(); text != tc.wantWarning {
					t.Errorf("%s status message = %q, want %q", desc, text, tc.wantWarning)
				}
			}
			if pinned := len(catalog.Snapshots[321]); pinned != 3 {
				t.Errorf("CreateExperiment() pinned %d components, want 3", pinned)
			}
			var recorded []string
			for _, call := range history.Calls {
				recorded = append(recorded, call.NewMsg.GetUserFacingText())
			}
			var wantRecorded []string
			if tc.wantWarning != "" {
				wantRecorded = []string{tc.wantWarning}
			}
			if diff := cmp.Diff(wantRecorded, recorded); diff != "" {
				t.Errorf("recorded status message history mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// retryingRunner runs a serializable transaction once per catalog, as a backend retries an aborted
// attempt, and keeps the result of the last attempt.
type retryingRunner struct {
	*storage.MockStorage
	catalogs []*storage.MockComponentRepository
}

func (r *retryingRunner) RunInSerializableTransaction(ctx context.Context, fn func(ctx context.Context, tx *storage.Transaction) error) (time.Time, error) {
	var commitTime time.Time
	for _, catalog := range r.catalogs {
		r.MockStorage.ComponentRepository = catalog
		var err error
		if commitTime, err = r.MockStorage.RunInSerializableTransaction(ctx, fn); err != nil {
			return time.Time{}, err
		}
	}
	return commitTime, nil
}

func TestCreateExperiment_RetriedTransactionClampsFromTheRequest(t *testing.T) {
	runner := &retryingRunner{
		MockStorage: &storage.MockStorage{},
		catalogs:    []*storage.MockComponentRepository{catalogWithTrialBudget("5"), catalogWithTrialBudget("8")},
	}
	req := studyRequest(10)
	req.Status.StructuredStatusMessage = &essmsgpb.StructuredMessage{Severity: essmsgpb.StructuredMessage_INFO, UserFacingText: "launched"}

	got, err := experiment.CreateExperiment(context.Background(), runner, req)
	if err != nil {
		t.Fatalf("CreateExperiment() failed: %v", err)
	}

	if budget := got.GetStudyConfig().GetMaxTrialBudget(); budget != 8 {
		t.Errorf("max_trial_budget = %d, want 8 from the last attempt", budget)
	}
	msg := got.GetStatus().GetStructuredStatusMessage()
	want := "launched; max_trial_budget 10 exceeds the hpo component limit; clamped to 8"
	if msg.GetUserFacingText() != want || msg.GetSeverity() != essmsgpb.StructuredMessage_WARNING {
		t.Errorf("status message = %v, want WARNING %q", msg, want)
	}
}

func TestCreateExperiment_FailedTransactionRestoresStatusAndStudyConfig(t *testing.T) {
	st := &storage.MockStorage{ComponentRepository: catalogWithTrialBudget("5"), InsertExperimentErr: grpcerr.ErrAborted}
	req := studyRequest(10)
	wantStatus, wantStudyConfig := proto.Clone(req.GetStatus()), proto.Clone(req.GetStudyConfig())

	if _, err := experiment.CreateExperiment(context.Background(), st, req); !errors.Is(err, grpcerr.ErrAborted) {
		t.Fatalf("CreateExperiment() error = %v, want %v", err, grpcerr.ErrAborted)
	}
	if diff := cmp.Diff(wantStatus, req.GetStatus(), protocmp.Transform()); diff != "" {
		t.Errorf("CreateExperiment() changed the requested status (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff(wantStudyConfig, req.GetStudyConfig(), protocmp.Transform()); diff != "" {
		t.Errorf("CreateExperiment() changed the requested study config (-want +got):\n%s", diff)
	}
}
