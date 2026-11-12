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
	"errors"
	"fmt"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	componentspb "xmanager_cloud/api/protos/components/v1alpha1"
	essmsgpb "xmanager_cloud/api/protos/experiment"
	"xmanager_cloud/internal/experiment"
	"xmanager_cloud/internal/experiment/experimenttest"
	"xmanager_cloud/internal/storage"
	"xmanager_cloud/pkg/components/store"
	dbstoretest "xmanager_cloud/pkg/components/store/dbstore/testing"
)

const (
	experimentalComponentsPrefix = "uses experimental components: "
	// seededMaxTrialBudget is the maxTrialBudget parameter of the seeded hpo component.
	seededMaxTrialBudget = 5
)

// catalogRecord returns an ENABLED record at generation 1 that has observed that generation.
func catalogRecord(id int64, name string, componentType componentspb.ComponentType, stability componentspb.ComponentStability, deps ...string) *store.ComponentRecord {
	return &store.ComponentRecord{
		ComponentID:        id,
		Name:               name,
		Spec:               &componentspb.ComponentSpec{Type: componentType, Version: "v1.0.0", Stability: stability, Dependencies: deps},
		State:              componentspb.ComponentState_COMPONENT_STATE_ENABLED,
		Generation:         1,
		ObservedGeneration: proto.Int64(1),
		Dependencies:       deps,
	}
}

// seedHPOCatalog writes ready EXPERIMENTAL hpo and vizier components and a ready STABLE clickhouse
// component, on which hpo depends, through the transaction-scoped component repository. hpo limits
// study trial budgets to seededMaxTrialBudget.
func seedHPOCatalog(ctx context.Context, t *testing.T, st storage.Storage) {
	t.Helper()
	hpo := catalogRecord(103, "hpo", componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_1P_COMPONENT, componentspb.ComponentStability_COMPONENT_STABILITY_EXPERIMENTAL, "clickhouse", "vizier")
	hpo.Spec.Parameters = map[string]string{"maxTrialBudget": strconv.Itoa(seededMaxTrialBudget)}
	recs := []*store.ComponentRecord{
		catalogRecord(101, "clickhouse", componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_3P_WRAPPER, componentspb.ComponentStability_COMPONENT_STABILITY_STABLE),
		catalogRecord(102, "vizier", componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_3P_WRAPPER, componentspb.ComponentStability_COMPONENT_STABILITY_EXPERIMENTAL),
		hpo,
	}
	depIDs := map[string]int64{"clickhouse": 101, "vizier": 102}
	for _, rec := range recs {
		_, err := st.RunInSerializableTransaction(ctx, func(ctx context.Context, tx *storage.Transaction) error {
			return tx.Components.InsertComponent(ctx, rec, depIDs)
		})
		if err != nil {
			t.Fatalf("InsertComponent(%q) failed: %v", rec.Name, err)
		}
		_, err = st.RunInSerializableTransaction(ctx, func(ctx context.Context, tx *storage.Transaction) error {
			return tx.Components.UpdateComponentObservation(ctx, rec, false)
		})
		if err != nil {
			t.Fatalf("UpdateComponentObservation(%q) failed: %v", rec.Name, err)
		}
	}
}

func newStudy(xid int64) *essmsgpb.Experiment {
	return &essmsgpb.Experiment{
		Name:           fmt.Sprintf("experiments/%d", xid),
		Title:          "study",
		Creator:        "test-creator",
		Tags:           []string{},
		Urls:           []*essmsgpb.Url{},
		Resources:      []*essmsgpb.ResourceSet{},
		Status:         &essmsgpb.ExperimentStatus{ActiveState: essmsgpb.ExperimentStatus_ACTIVE},
		ExperimentType: essmsgpb.Experiment_STUDY,
	}
}

// experimentalWarningNames returns the sorted component names in the experimental components
// warning of text, or nil when text has no such warning.
func experimentalWarningNames(text string) []string {
	_, names, ok := strings.Cut(text, experimentalComponentsPrefix)
	if !ok {
		return nil
	}
	names, _, _ = strings.Cut(names, ";")
	got := strings.Split(names, ", ")
	slices.Sort(got)
	return got
}

func TestCreateExperiment_StudyPinsComponentsOnEveryBackend(t *testing.T) {
	for _, backend := range dbstoretest.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			ctx := t.Context()
			st, _ := backend.Open(ctx, t)
			seedHPOCatalog(ctx, t, st)

			const xid = 501
			if _, err := experiment.CreateExperiment(ctx, st, newStudy(xid)); err != nil {
				t.Fatalf("CreateExperiment(STUDY) failed: %v", err)
			}
			assertPinned(ctx, t, st, xid)
			assertExperimentalWarning(ctx, t, st, xid)

			// vizier stops being ready while hpo stays ENABLED: the in-transaction check rejects the
			// study and rolls back its experiment row.
			_, err := st.RunInSerializableTransaction(ctx, func(ctx context.Context, tx *storage.Transaction) error {
				return tx.Components.UpdateComponentState(ctx, 102, componentspb.ComponentState_COMPONENT_STATE_PENDING_RECONCILIATION, nil)
			})
			if err != nil {
				t.Fatalf("UpdateComponentState(vizier) failed: %v", err)
			}
			const rejectedXid = 502
			_, err = experiment.CreateExperiment(ctx, st, newStudy(rejectedXid))
			if !errors.Is(err, experiment.ErrComponentRequirementNotMet) || status.Code(err) != codes.FailedPrecondition {
				t.Fatalf("CreateExperiment(STUDY, pending vizier) error = %v, want %v", err, experiment.ErrComponentRequirementNotMet)
			}
			if _, err := st.FetchExperiment(ctx, rejectedXid); status.Code(err) != codes.NotFound {
				t.Errorf("FetchExperiment(%d) error = %v, want NotFound", rejectedXid, err)
			}
		})
	}
}

// assertPinned checks that hpo and its transitive dependencies are pinned to xid at generation 1.
func assertPinned(ctx context.Context, t *testing.T, st storage.Storage, xid int64) {
	t.Helper()
	snaps, err := st.FetchExperimentSnapshotComponents(ctx, xid)
	if err != nil {
		t.Fatalf("FetchExperimentSnapshotComponents(%d) failed: %v", xid, err)
	}
	var got []string
	for _, snap := range snaps {
		if snap.LockedGeneration != 1 {
			t.Errorf("component %d locked generation = %d, want 1", snap.ComponentID, snap.LockedGeneration)
		}
		got = append(got, *snap.Name)
	}
	slices.Sort(got)
	if diff := cmp.Diff([]string{"clickhouse", "hpo", "vizier"}, got); diff != "" {
		t.Errorf("pinned components mismatch (-want +got):\n%s", diff)
	}
}

// assertExperimentalWarning checks that the drift evaluator warns about the EXPERIMENTAL pinned
// components of xid, and not about drift.
func assertExperimentalWarning(ctx context.Context, t *testing.T, st storage.Storage, xid int64) {
	t.Helper()
	expStatus := &essmsgpb.ExperimentStatus{ActiveState: essmsgpb.ExperimentStatus_ACTIVE}
	if err := experiment.NewDriftEvaluator(st).Evaluate(ctx, xid, expStatus); err != nil {
		t.Fatalf("Evaluate(%d) failed: %v", xid, err)
	}
	msg := expStatus.GetStructuredStatusMessage()
	if msg.GetSeverity() != essmsgpb.StructuredMessage_WARNING {
		t.Errorf("status message severity = %v, want WARNING", msg.GetSeverity())
	}
	if got := strings.Count(msg.GetUserFacingText(), experimentalComponentsPrefix); got != 1 {
		t.Errorf("status message %q has %d experimental components warnings, want exactly 1", msg.GetUserFacingText(), got)
	}
	if diff := cmp.Diff([]string{"hpo", "vizier"}, experimentalWarningNames(msg.GetUserFacingText())); diff != "" {
		t.Errorf("experimental components in warning %q mismatch (-want +got):\n%s", msg.GetUserFacingText(), diff)
	}
	if strings.Contains(msg.GetUserFacingText(), "drift") {
		t.Errorf("status message %q reports drift, want none", msg.GetUserFacingText())
	}
}

func TestCreateExperiment_StudyTrialBudgetIsClampedOnEveryBackend(t *testing.T) {
	const clampWarning = "max_trial_budget 10 exceeds the hpo component limit; clamped to 5"
	for _, backend := range dbstoretest.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			ctx := t.Context()
			st, _ := backend.Open(ctx, t)
			seedHPOCatalog(ctx, t, st)
			const xid = 601
			study := newStudy(xid)
			study.TypeConfig = &essmsgpb.Experiment_StudyConfig{StudyConfig: experimenttest.StudyConfig()} // max_trial_budget 10.
			if _, err := experiment.CreateExperiment(ctx, st, study); err != nil {
				t.Fatalf("CreateExperiment(STUDY) failed: %v", err)
			}

			got, err := st.FetchExperiment(ctx, xid)
			if err != nil {
				t.Fatalf("FetchExperiment(%d) failed: %v", xid, err)
			}
			if budget := got.GetStudyConfig().GetMaxTrialBudget(); budget != seededMaxTrialBudget {
				t.Errorf("stored max_trial_budget = %d, want %d", budget, seededMaxTrialBudget)
			}
			msg := got.GetStatus().GetStructuredStatusMessage()
			if msg.GetSeverity() != essmsgpb.StructuredMessage_WARNING || msg.GetUserFacingText() != clampWarning {
				t.Errorf("stored status message = %v, want WARNING %q", msg, clampWarning)
			}
			assertRecordedStatusMessages(ctx, t, st, xid, []string{clampWarning})

			// The read-time experimental components warning is merged into the stored warning.
			if err := experiment.NewDriftEvaluator(st).Evaluate(ctx, xid, got.GetStatus()); err != nil {
				t.Fatalf("Evaluate(%d) failed: %v", xid, err)
			}
			text := got.GetStatus().GetStructuredStatusMessage().GetUserFacingText()
			if strings.Count(text, clampWarning) != 1 || strings.Count(text, experimentalComponentsPrefix) != 1 {
				t.Errorf("merged status message %q, want the clamp and experimental components warnings once each", text)
			}
		})
	}
}

// assertRecordedStatusMessages checks the texts of the status message history of experiment xid.
func assertRecordedStatusMessages(ctx context.Context, t *testing.T, st storage.Storage, xid int64, want []string) {
	t.Helper()
	msgs, _, err := st.ListStatusMessages(ctx, fmt.Sprintf("experiments/%d", xid), 10, "", "", "", 0)
	if err != nil {
		t.Fatalf("ListStatusMessages(%d) failed: %v", xid, err)
	}
	var got []string
	for _, msg := range msgs {
		got = append(got, msg.GetContent().GetUserFacingText())
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("status message history mismatch (-want +got):\n%s", diff)
	}
}
