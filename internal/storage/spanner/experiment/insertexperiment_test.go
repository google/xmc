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
	"testing"
	"time"

	gcspanner "cloud.google.com/go/spanner"
	"github.com/google/go-cmp/cmp"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/testing/protocmp"
	fmpb "google.golang.org/protobuf/types/known/fieldmaskpb"
	timestamppb "google.golang.org/protobuf/types/known/timestamppb"
	essmsgpb "xmanager_cloud/api/protos/experiment"
	"xmanager_cloud/internal/experiment/experimenttest"
	"xmanager_cloud/internal/grpcerr"
	"xmanager_cloud/internal/storage"
	"xmanager_cloud/internal/storage/spanner"
	"xmanager_cloud/internal/storage/spanner/emulator"
)

func TestInsertExperiment(t *testing.T) {
	ctx := context.Background()
	_, essClient := emulator.Setup(ctx, t)
	storageClient, _ := spanner.NewStorage(essClient)

	now := timestamppb.New(time.Now())
	exp := &essmsgpb.Experiment{
		Name:       "experiments/789",
		Title:      "insert-title",
		Creator:    "test-creator",
		Urls:       []*essmsgpb.Url{},
		Tags:       []string{},
		Resources:  []*essmsgpb.ResourceSet{},
		CreateTime: now,
		UpdateTime: now,
		Status: &essmsgpb.ExperimentStatus{
			ActiveState:               essmsgpb.ExperimentStatus_ACTIVE,
			UpdateTime:                now,
			LastChildStatusUpdateTime: now,
		},
	}

	err := storageClient.RunInTransaction(ctx, func(ctx context.Context, tx *storage.Transaction) error {
		return tx.Experiments.InsertExperiment(ctx, exp)
	})
	if err != nil {
		t.Fatalf("InsertExperiment failed: %v", err)
	}

	fetched, err := storageClient.FetchExperiment(ctx, 789)
	if err != nil {
		t.Fatalf("FetchExperiment failed: %v", err)
	}
	if fetched.GetName() != exp.GetName() {
		t.Errorf("FetchExperiment got name %q, want %q", fetched.GetName(), exp.GetName())
	}
}

func TestInsertExperiment_Nil(t *testing.T) {
	ctx := context.Background()
	_, essClient := emulator.Setup(ctx, t)
	storageClient, _ := spanner.NewStorage(essClient)

	err := storageClient.RunInTransaction(ctx, func(ctx context.Context, tx *storage.Transaction) error {
		return tx.Experiments.InsertExperiment(ctx, nil)
	})
	if err == nil {
		t.Errorf("InsertExperiment(nil) succeeded, want error")
	}
}

func TestInsertExperiment_ExperimentTypeRoundTripAndImmutable(t *testing.T) {
	ctx := context.Background()
	_, essClient := emulator.Setup(ctx, t)
	storageClient, _ := spanner.NewStorage(essClient)

	// experiments/200 is a legacy row written without the ExperimentType column (NULL).
	now := time.Now()
	legacy := gcspanner.Insert("Experiments",
		[]string{"Xid", "Title", "Creator", "Priority", "Urls", "Tags", "Notes", "Resources", "Status", "CreateTime", "UpdateTime"},
		[]any{int64(200), "legacy", "test@example.com", int64(1), []*essmsgpb.Url{}, []string{}, "", []*essmsgpb.ResourceSet{}, &essmsgpb.ExperimentStatus{}, now, now})
	if _, err := essClient.Apply(ctx, []*gcspanner.Mutation{legacy}); err != nil {
		t.Fatalf("Apply(legacy experiment) failed: %v", err)
	}
	for xid, expType := range map[int64]essmsgpb.Experiment_ExperimentType{201: essmsgpb.Experiment_STANDARD, 202: essmsgpb.Experiment_STUDY} {
		exp := spanner.BlankExperimentProto(xid)
		exp.ExperimentType = expType
		exp.CreateTime, exp.UpdateTime = timestamppb.New(now), timestamppb.New(now)
		// Insert through the repository: experiment.CreateExperiment would also require a STUDY's components.
		if err := storageClient.RunInTransaction(ctx, func(ctx context.Context, tx *storage.Transaction) error {
			return tx.Experiments.InsertExperiment(ctx, exp)
		}); err != nil {
			t.Fatalf("InsertExperiment(%d) failed: %v", xid, err)
		}
	}

	for xid, want := range map[int64]essmsgpb.Experiment_ExperimentType{200: essmsgpb.Experiment_STANDARD, 201: essmsgpb.Experiment_STANDARD, 202: essmsgpb.Experiment_STUDY} {
		got, err := storageClient.FetchExperiment(ctx, xid)
		if err != nil {
			t.Fatalf("FetchExperiment(%d) failed: %v", xid, err)
		}
		if got.GetExperimentType() != want {
			t.Errorf("FetchExperiment(%d).ExperimentType = %v, want %v", xid, got.GetExperimentType(), want)
		}
	}

	for filter, want := range map[string][]string{
		`experiment_type = "STANDARD"`: {"experiments/200", "experiments/201"},
		`experiment_type != "STUDY"`:   {"experiments/200", "experiments/201"},
		`experiment_type = "STUDY"`:    {"experiments/202"},
	} {
		gotList, _, err := storageClient.ListExperiments(ctx, 10, "", filter, "xid", 0)
		if err != nil {
			t.Fatalf("ListExperiments(%s) failed: %v", filter, err)
		}
		var gotNames []string
		for _, exp := range gotList {
			gotNames = append(gotNames, exp.GetName())
		}
		if diff := cmp.Diff(want, gotNames); diff != "" {
			t.Errorf("ListExperiments(%s) names diff (-want +got):\n%s", filter, diff)
		}
	}

	studyXID := int64(202)
	_, err := storageClient.UpdateExperiment(ctx, &storage.ExperimentUpdate{
		XID:        studyXID,
		Experiment: &essmsgpb.Experiment{ExperimentType: essmsgpb.Experiment_STANDARD},
		UpdateMask: &fmpb.FieldMask{Paths: []string{"experiment_type"}},
	})
	if !errors.Is(err, grpcerr.ErrInvalidArgument) {
		t.Errorf("UpdateExperiment(experiment_type) error = %v, want %v", err, grpcerr.ErrInvalidArgument)
	}

	if _, err := storageClient.UpdateExperiment(ctx, &storage.ExperimentUpdate{
		XID:        studyXID,
		Experiment: &essmsgpb.Experiment{Title: "renamed", ExperimentType: essmsgpb.Experiment_STANDARD},
		UpdateMask: &fmpb.FieldMask{Paths: []string{"title"}},
	}); err != nil {
		t.Fatalf("UpdateExperiment(title) failed: %v", err)
	}
	got, err := storageClient.FetchExperiment(ctx, studyXID)
	if err != nil {
		t.Fatalf("FetchExperiment(%d) failed: %v", studyXID, err)
	}
	if got.GetExperimentType() != essmsgpb.Experiment_STUDY {
		t.Errorf("experiment_type after UpdateExperiment(title) = %v, want %v", got.GetExperimentType(), essmsgpb.Experiment_STUDY)
	}
}

func TestInsertExperiment_StudyConfigRoundTripAndImmutable(t *testing.T) {
	ctx := context.Background()
	_, essClient := emulator.Setup(ctx, t)
	storageClient, _ := spanner.NewStorage(essClient)

	// experiments/300 is a legacy row written without the StudyConfig column (NULL).
	now := time.Now()
	legacy := gcspanner.Insert("Experiments",
		[]string{"Xid", "Title", "Creator", "Priority", "Urls", "Tags", "Notes", "Resources", "Status", "CreateTime", "UpdateTime"},
		[]any{int64(300), "legacy", "test@example.com", int64(1), []*essmsgpb.Url{}, []string{}, "", []*essmsgpb.ResourceSet{}, &essmsgpb.ExperimentStatus{}, now, now})
	if _, err := essClient.Apply(ctx, []*gcspanner.Mutation{legacy}); err != nil {
		t.Fatalf("Apply(legacy experiment) failed: %v", err)
	}
	studyXID := int64(301)
	study := spanner.BlankExperimentProto(studyXID)
	study.ExperimentType = essmsgpb.Experiment_STUDY
	study.TypeConfig = &essmsgpb.Experiment_StudyConfig{StudyConfig: experimenttest.StudyConfig()}
	study.CreateTime, study.UpdateTime = timestamppb.New(now), timestamppb.New(now)
	// Insert through the repository: experiment.CreateExperiment would also require the study's components.
	if err := storageClient.RunInTransaction(ctx, func(ctx context.Context, tx *storage.Transaction) error {
		return tx.Experiments.InsertExperiment(ctx, study)
	}); err != nil {
		t.Fatalf("InsertExperiment(%d) failed: %v", studyXID, err)
	}

	for xid, want := range map[int64]*essmsgpb.StudyConfig{300: nil, studyXID: study.GetStudyConfig()} {
		got, err := storageClient.FetchExperiment(ctx, xid)
		if err != nil {
			t.Fatalf("FetchExperiment(%d) failed: %v", xid, err)
		}
		if diff := cmp.Diff(want, got.GetStudyConfig(), protocmp.Transform()); diff != "" {
			t.Errorf("FetchExperiment(%d).StudyConfig diff (-want +got):\n%s", xid, diff)
		}
	}
	gotList, _, err := storageClient.ListExperiments(ctx, 10, "", "", "xid", 0)
	if err != nil {
		t.Fatalf("ListExperiments() failed: %v", err)
	}
	var gotStudyConfigs []*essmsgpb.StudyConfig
	for _, exp := range gotList {
		gotStudyConfigs = append(gotStudyConfigs, exp.GetStudyConfig())
	}
	if diff := cmp.Diff([]*essmsgpb.StudyConfig{nil, study.GetStudyConfig()}, gotStudyConfigs, protocmp.Transform()); diff != "" {
		t.Errorf("ListExperiments() study configs diff (-want +got):\n%s", diff)
	}
	// study_config is selectable but holds an opaque proto, so it cannot be filtered on.
	if _, _, err := storageClient.ListExperiments(ctx, 10, "", `study_config = "x"`, "xid", 0); status.Code(err) != codes.InvalidArgument {
		t.Errorf("ListExperiments(study_config filter) error = %v, want code %v", err, codes.InvalidArgument)
	}

	_, err = storageClient.UpdateExperiment(ctx, &storage.ExperimentUpdate{
		XID:        studyXID,
		Experiment: &essmsgpb.Experiment{TypeConfig: &essmsgpb.Experiment_StudyConfig{StudyConfig: &essmsgpb.StudyConfig{}}},
		UpdateMask: &fmpb.FieldMask{Paths: []string{"study_config"}},
	})
	if !errors.Is(err, grpcerr.ErrInvalidArgument) {
		t.Errorf("UpdateExperiment(study_config) error = %v, want %v", err, grpcerr.ErrInvalidArgument)
	}
	res, err := storageClient.UpdateExperiment(ctx, &storage.ExperimentUpdate{
		XID:        studyXID,
		Experiment: &essmsgpb.Experiment{Title: "renamed"},
		UpdateMask: &fmpb.FieldMask{Paths: []string{"title"}},
	})
	if err != nil {
		t.Fatalf("UpdateExperiment(title) failed: %v", err)
	}
	got, err := storageClient.FetchExperiment(ctx, studyXID)
	if err != nil {
		t.Fatalf("FetchExperiment(%d) failed: %v", studyXID, err)
	}
	for desc, exp := range map[string]*essmsgpb.Experiment{"returned": res.Updated, "stored": got} {
		if diff := cmp.Diff(study.GetStudyConfig(), exp.GetStudyConfig(), protocmp.Transform()); diff != "" {
			t.Errorf("study_config after UpdateExperiment(title) (%s) diff (-want +got):\n%s", desc, diff)
		}
	}
}
