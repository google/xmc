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
	"context"
	"fmt"
	"slices"
	"sync"
	"testing"

	"github.com/google/go-cmp/cmp"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/testing/protocmp"
	essmsgpb "xmanager_cloud/api/protos/experiment"
	"xmanager_cloud/internal/experiment"
	"xmanager_cloud/internal/experiment/experimenttest"
	"xmanager_cloud/internal/storage"
	"xmanager_cloud/internal/validator"
	"xmanager_cloud/internal/workunit"
	dbstoretest "xmanager_cloud/pkg/components/store/dbstore/testing"
)

// seedStudyController writes a ready hpo component, without dependencies, whose
// studyControllerServiceAccount parameter is studyControllerGSA.
func seedStudyController(ctx context.Context, t *testing.T, st storage.Storage) {
	t.Helper()
	hpo := experimenttest.HPOCatalog(map[string]string{"studyControllerServiceAccount": studyControllerGSA})["hpo"]
	hpo.Spec.Dependencies = nil
	for _, write := range []func(ctx context.Context, tx *storage.Transaction) error{
		func(ctx context.Context, tx *storage.Transaction) error {
			return tx.Components.InsertComponent(ctx, hpo, nil)
		},
		func(ctx context.Context, tx *storage.Transaction) error {
			return tx.Components.UpdateComponentObservation(ctx, hpo, false)
		},
	} {
		if _, err := st.RunInSerializableTransaction(ctx, write); err != nil {
			t.Fatalf("seeding the hpo component failed: %v", err)
		}
	}
}

// createStudy creates the STUDY experiment xid.
func createStudy(ctx context.Context, t *testing.T, st storage.Storage, xid int64) {
	t.Helper()
	study := &essmsgpb.Experiment{
		Name: fmt.Sprintf("experiments/%d", xid), Title: "study", Creator: "test@example.com",
		Tags: []string{}, Urls: []*essmsgpb.Url{}, Resources: []*essmsgpb.ResourceSet{},
		Status:         &essmsgpb.ExperimentStatus{ActiveState: essmsgpb.ExperimentStatus_ACTIVE},
		ExperimentType: essmsgpb.Experiment_STUDY,
		TypeConfig:     &essmsgpb.Experiment_StudyConfig{StudyConfig: experimenttest.StudyConfig()},
	}
	if _, err := experiment.CreateExperiment(ctx, st, study); err != nil {
		t.Fatalf("CreateExperiment(STUDY) failed: %v", err)
	}
}

func TestBatchCreateWorkUnits_OnEveryBackend(t *testing.T) {
	for _, backend := range dbstoretest.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			ctx := t.Context()
			st, _ := backend.Open(ctx, t)
			seedStudyController(ctx, t, st)
			createStudy(ctx, t, st, batchXID)

			for _, batch := range [][]string{{"t1", "t2"}, {"t3", "t4", "t5"}} {
				wus := make([]*essmsgpb.WorkUnit, len(batch))
				for i, trialID := range batch {
					wus[i] = newTrialRequest(trialID)
				}
				if _, err := workunit.BatchCreateWorkUnits(ctx, st, batchXID, studyControllerGSA, wus); err != nil {
					t.Fatalf("BatchCreateWorkUnits(%v) failed: %v", batch, err)
				}
			}
			_, err := workunit.BatchCreateWorkUnits(ctx, st, batchXID, "alice@example.com", []*essmsgpb.WorkUnit{newTrialRequest("t6")})
			if status.Code(err) != codes.PermissionDenied {
				t.Errorf("BatchCreateWorkUnits(other caller) = %v, want PermissionDenied", err)
			}

			listed, _, err := st.ListWorkUnits(ctx, fmt.Sprintf("experiments/%d", batchXID), 10, "", "", "", 0)
			if err != nil {
				t.Fatalf("ListWorkUnits() failed: %v", err)
			}
			var got []*essmsgpb.WorkUnit
			for _, wu := range listed {
				got = append(got, &essmsgpb.WorkUnit{Name: wu.GetName(), Notes: wu.GetNotes()})
			}
			var want []*essmsgpb.WorkUnit
			for wid := int64(1); wid <= 5; wid++ {
				want = append(want, &essmsgpb.WorkUnit{Name: fmt.Sprintf("experiments/%d/workUnits/%d", batchXID, wid), Notes: fmt.Sprintf("t%d", wid)})
			}
			if diff := cmp.Diff(want, got, protocmp.Transform()); diff != "" {
				t.Errorf("stored trials mismatch (-want +got):\n%s", diff)
			}
			exp, err := st.FetchExperiment(ctx, batchXID)
			if err != nil {
				t.Fatalf("FetchExperiment() failed: %v", err)
			}
			wantCounts := []*essmsgpb.ExperimentStatus_StateCount{{State: essmsgpb.WorkUnitStatus_INITIALIZING, Count: 5}}
			if diff := cmp.Diff(wantCounts, exp.GetStatus().GetChildStateCounts(), protocmp.Transform()); diff != "" {
				t.Errorf("experiment child state counts mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// widOf returns the work unit ID of the work unit named name.
func widOf(t *testing.T, name string) int64 {
	t.Helper()
	_, wid, err := validator.ValidateWorkUnitName(name)
	if err != nil {
		t.Fatalf("ValidateWorkUnitName(%q) failed: %v", name, err)
	}
	return wid
}

func TestBatchCreateWorkUnits_ConcurrentBatchesGetDisjointContiguousIDs(t *testing.T) {
	const batches, perBatch = 4, 3
	for _, backend := range dbstoretest.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			ctx := t.Context()
			st, _ := backend.Open(ctx, t)
			seedStudyController(ctx, t, st)
			createStudy(ctx, t, st, batchXID)

			created := make([][]*essmsgpb.WorkUnit, batches)
			errs := make([]error, batches)
			var wg sync.WaitGroup
			for b := range batches {
				wg.Add(1)
				go func() {
					defer wg.Done()
					wus := make([]*essmsgpb.WorkUnit, perBatch)
					for i := range wus {
						wus[i] = newTrialRequest(fmt.Sprintf("b%d-t%d", b, i))
					}
					created[b], errs[b] = workunit.BatchCreateWorkUnits(ctx, st, batchXID, studyControllerGSA, wus)
				}()
			}
			wg.Wait()

			var all []int64
			for b := range batches {
				if errs[b] != nil {
					t.Fatalf("batch %d failed: %v", b, errs[b])
				}
				first := widOf(t, created[b][0].GetName())
				for i, wu := range created[b] {
					wid := widOf(t, wu.GetName())
					if wid != first+int64(i) {
						t.Errorf("batch %d work unit %d has ID %d, want contiguous ID %d", b, i, wid, first+int64(i))
					}
					all = append(all, wid)
				}
			}
			slices.Sort(all)
			want := make([]int64, batches*perBatch)
			for i := range want {
				want[i] = int64(i + 1)
			}
			if diff := cmp.Diff(want, all); diff != "" {
				t.Errorf("union of work unit IDs mismatch (-want +got):\n%s", diff)
			}
		})
	}
}
