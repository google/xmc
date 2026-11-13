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

package workunit_test

import (
	"errors"
	"fmt"
	"testing"

	"github.com/google/go-cmp/cmp"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/testing/protocmp"
	essmsgpb "xmanager_cloud/api/protos/experiment"
	"xmanager_cloud/internal/storage"
	"xmanager_cloud/internal/workunit"
)

// newBatchStorage returns a mock storage holding the STUDY experiment batchXID whose next work unit
// ID is nextWID, and an hpo component naming studyControllerGSA.
func newBatchStorage(nextWID int64) *storage.MockStorage {
	return &storage.MockStorage{
		Experiment: &essmsgpb.Experiment{
			Name:           fmt.Sprintf("experiments/%d", batchXID),
			ExperimentType: essmsgpb.Experiment_STUDY,
			Status:         &essmsgpb.ExperimentStatus{},
		},
		NextWorkUnitID:            nextWID,
		ComponentRepository:       hpoCatalog(studyControllerGSA),
		StatusHistoryTxRepository: &storage.MockStatusHistoryTxRepository{},
	}
}

func TestBatchCreateWorkUnits_AssignsContiguousIDsInOneTransaction(t *testing.T) {
	st := newBatchStorage(7)
	// The transaction body must be safe to re-run when the storage retries it.
	st.RunInTransactionAttempts = 2
	wus := []*essmsgpb.WorkUnit{newTrialRequest("a"), newTrialRequest("b"), newTrialRequest("c")}
	originals := []*essmsgpb.WorkUnit{proto.Clone(wus[0]).(*essmsgpb.WorkUnit), proto.Clone(wus[1]).(*essmsgpb.WorkUnit), proto.Clone(wus[2]).(*essmsgpb.WorkUnit)}

	created, err := workunit.BatchCreateWorkUnits(t.Context(), st, batchXID, studyControllerGSA, wus)
	if err != nil {
		t.Fatalf("BatchCreateWorkUnits() failed: %v", err)
	}

	var gotNames, gotTrials []string
	for _, wu := range created {
		gotNames = append(gotNames, wu.GetName(), wu.GetCurrentWorkUnitExecution().GetName(), wu.GetCurrentWorkUnitExecution().GetStatus().GetSystemMessage().GetParent())
		gotTrials = append(gotTrials, wu.GetTrialId())
		if wu.GetCreateTime() == nil || !proto.Equal(wu.GetCreateTime(), wu.GetCurrentWorkUnitExecution().GetStatus().GetStateUpdateTime()) {
			t.Errorf("work unit %s timestamps not stamped: %v", wu.GetName(), wu)
		}
	}
	wantNames := []string{
		"experiments/42/workUnits/7", "experiments/42/workUnits/7/executions/1", "experiments/42/workUnits/7",
		"experiments/42/workUnits/8", "experiments/42/workUnits/8/executions/1", "experiments/42/workUnits/8",
		"experiments/42/workUnits/9", "experiments/42/workUnits/9/executions/1", "experiments/42/workUnits/9",
	}
	if diff := cmp.Diff(wantNames, gotNames); diff != "" {
		t.Errorf("created names mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff([]string{"a", "b", "c"}, gotTrials); diff != "" {
		t.Errorf("created trial IDs mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff(created, st.InsertedWorkUnits, protocmp.Transform()); diff != "" {
		t.Errorf("inserted work units mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff(originals, wus, protocmp.Transform()); diff != "" {
		t.Errorf("BatchCreateWorkUnits() modified its input (-want +got):\n%s", diff)
	}
	var historyWIDs []int64
	for _, call := range st.StatusHistoryTxRepository.(*storage.MockStatusHistoryTxRepository).Calls {
		historyWIDs = append(historyWIDs, call.WID)
	}
	// Two transaction attempts record the history of the same three work units twice.
	if diff := cmp.Diff([]int64{7, 8, 9, 7, 8, 9}, historyWIDs); diff != "" {
		t.Errorf("status history work unit IDs mismatch (-want +got):\n%s", diff)
	}
	wantCounts := []*essmsgpb.ExperimentStatus_StateCount{{State: essmsgpb.WorkUnitStatus_INITIALIZING, Count: 3}}
	if diff := cmp.Diff(wantCounts, st.UpdatedExperiment.GetStatus().GetChildStateCounts(), protocmp.Transform()); diff != "" {
		t.Errorf("experiment child state counts mismatch (-want +got):\n%s", diff)
	}
	if len(st.UpdatedExperiment.GetResources()) == 0 {
		t.Errorf("experiment resources were not updated: %v", st.UpdatedExperiment)
	}
}

func TestBatchCreateWorkUnits_Errors(t *testing.T) {
	errStorage := errors.New("storage failure")
	tests := []struct {
		name     string
		mutate   func(st *storage.MockStorage)
		caller   string
		wantCode codes.Code
		wantErr  error
	}{
		{name: "caller is not the study controller", caller: "alice@example.com", wantCode: codes.PermissionDenied, wantErr: workunit.ErrNotStudyController},
		{name: "standard experiment", mutate: func(st *storage.MockStorage) { st.Experiment.ExperimentType = essmsgpb.Experiment_STANDARD }, wantCode: codes.Unimplemented, wantErr: workunit.ErrBatchCreateRequiresStudy},
		{name: "experiment read failure", mutate: func(st *storage.MockStorage) { st.FetchExperimentErr = errStorage }, wantErr: errStorage},
		{name: "missing experiment", mutate: func(st *storage.MockStorage) { st.Experiment = nil }, wantCode: codes.Unknown},
		{name: "ID allocation failure", mutate: func(st *storage.MockStorage) { st.FetchNextWorkUnitIDErr = errStorage }, wantErr: errStorage},
		{name: "insert failure", mutate: func(st *storage.MockStorage) { st.InsertWorkUnitsErr = errStorage }, wantErr: errStorage},
		{name: "history failure", mutate: func(st *storage.MockStorage) {
			st.StatusHistoryTxRepository = &storage.MockStatusHistoryTxRepository{SaveErr: errStorage}
		}, wantErr: errStorage},
		{name: "experiment update failure", mutate: func(st *storage.MockStorage) { st.UpdateExperimentStatusErr = errStorage }, wantErr: errStorage},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			st := newBatchStorage(1)
			if tt.mutate != nil {
				tt.mutate(st)
			}
			caller := tt.caller
			if caller == "" {
				caller = studyControllerGSA
			}
			created, err := workunit.BatchCreateWorkUnits(t.Context(), st, batchXID, caller, []*essmsgpb.WorkUnit{newTrialRequest("a")})
			if err == nil || created != nil {
				t.Fatalf("BatchCreateWorkUnits() = (%v, %v), want an error", created, err)
			}
			if tt.wantErr != nil && !errors.Is(err, tt.wantErr) {
				t.Errorf("BatchCreateWorkUnits() = %v, want it to wrap %v", err, tt.wantErr)
			}
			if tt.wantCode != codes.OK && status.Code(err) != tt.wantCode {
				t.Errorf("BatchCreateWorkUnits() code = %v, want %v", status.Code(err), tt.wantCode)
			}
		})
	}
}
