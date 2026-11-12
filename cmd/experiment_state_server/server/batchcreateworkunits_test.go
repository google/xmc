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

package server

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	lropb "cloud.google.com/go/longrunning/autogen/longrunningpb"
	"github.com/google/go-cmp/cmp"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	msgpb "xmanager_cloud/api/protos/experiment"
	"xmanager_cloud/cmd/experiment_state_server/config"
	"xmanager_cloud/internal/experiment/experimenttest"
	"xmanager_cloud/internal/grpcerr"
	"xmanager_cloud/internal/prototest"
	"xmanager_cloud/internal/storage"
	tq "xmanager_cloud/internal/task_queue"
	"xmanager_cloud/internal/validator"
	"xmanager_cloud/internal/workunit"
)

const batchParent = "experiments/123"

// batchStorage counts transactions and records one operation per AddWorkUnitOperation call.
type batchStorage struct {
	*storage.MockStorage
	txCalls    int
	operations []*msgpb.OperationMetadata
	// operationErrs maps a work unit name to the error of its AddWorkUnitOperation call.
	operationErrs map[string]error
}

func (s *batchStorage) RunInTransaction(ctx context.Context, fn func(ctx context.Context, tx *storage.Transaction) error) error {
	s.txCalls++
	return s.MockStorage.RunInTransaction(ctx, fn)
}

func (s *batchStorage) AddWorkUnitOperation(ctx context.Context, xid, wid int64, metadata *msgpb.OperationMetadata) (*lropb.Operation, error) {
	s.operations = append(s.operations, metadata)
	if err := s.operationErrs[metadata.GetTarget()]; err != nil {
		return nil, err
	}
	return &lropb.Operation{Name: validator.ConstructOperationName(xid, wid, fmt.Sprintf("%08d-0000-0000-0000-000000000000", wid))}, nil
}

// newBatchStorage returns storage holding the STUDY experiment 123, whose next work unit ID is 4,
// and an hpo component whose studyControllerServiceAccount is serviceAccount.
func newBatchStorage(serviceAccount string) *batchStorage {
	return &batchStorage{MockStorage: &storage.MockStorage{
		Experiment: &msgpb.Experiment{
			Name: batchParent, ExperimentType: msgpb.Experiment_STUDY, Priority: msgpb.Priority_HIGH,
			Status:     &msgpb.ExperimentStatus{},
			TypeConfig: &msgpb.Experiment_StudyConfig{StudyConfig: experimenttest.StudyConfig()},
		},
		NextWorkUnitID:            4,
		ComponentRepository:       &storage.MockComponentRepository{Records: experimenttest.HPOCatalog(map[string]string{"studyControllerServiceAccount": serviceAccount})},
		StatusHistoryTxRepository: &storage.MockStatusHistoryTxRepository{},
	}}
}

// batchRequest returns a request for n valid trials, mutated by fn.
func batchRequest(n int, fn func(req *msgpb.BatchCreateWorkUnitsRequest)) *msgpb.BatchCreateWorkUnitsRequest {
	req := &msgpb.BatchCreateWorkUnitsRequest{Parent: batchParent}
	for i := range n {
		req.Requests = append(req.Requests, &msgpb.CreateWorkUnitRequest{WorkUnit: &msgpb.WorkUnit{
			WorkUnitType: msgpb.WorkUnit_STANDARD,
			Executable:   testExecutable,
			Parameters:   experimenttest.TrialParameters(),
			TrialId:      fmt.Sprintf("trial-%d", i+1),
		}})
	}
	return prototest.Modify(req, fn)
}

func newBatchServer(t *testing.T, st storage.Storage, actuator *tq.FakeClient, opts ...testServerOpt) *Server {
	t.Helper()
	return newTestServer(t, append([]testServerOpt{
		withConfig(&config.Config{DefaultExecutionEnvironment: "gke"}),
		withStorage(st),
		withWorkUnitActuator(actuator),
	}, opts...)...)
}

func TestBatchCreateWorkUnits_CreatesTrialsInOneTransactionAndOneEnqueue(t *testing.T) {
	// The configured principal differs in case from the authenticated test@example.com.
	st := newBatchStorage("TEST@example.com")
	actuator := &tq.FakeClient{}
	server := newBatchServer(t, st, actuator)

	resp, err := server.BatchCreateWorkUnits(context.Background(), batchRequest(3, nil))
	if err != nil {
		t.Fatalf("BatchCreateWorkUnits() failed: %v", err)
	}

	var gotNames, gotTrials []string
	for _, wu := range resp.GetWorkUnits() {
		gotNames = append(gotNames, wu.GetName())
		gotTrials = append(gotTrials, wu.GetTrialId())
	}
	wantNames := []string{"experiments/123/workUnits/4", "experiments/123/workUnits/5", "experiments/123/workUnits/6"}
	if diff := cmp.Diff(wantNames, gotNames); diff != "" {
		t.Errorf("created work unit names mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff([]string{"trial-1", "trial-2", "trial-3"}, gotTrials); diff != "" {
		t.Errorf("created trial IDs mismatch (-want +got):\n%s", diff)
	}
	if st.txCalls != 1 || st.UpdatedExperiment == nil || len(st.InsertedWorkUnits) != 3 {
		t.Errorf("transactions = %d, experiment updated = %v, inserted = %d; want 1, true, 3", st.txCalls, st.UpdatedExperiment != nil, len(st.InsertedWorkUnits))
	}
	var gotOps []string
	for _, op := range st.operations {
		gotOps = append(gotOps, fmt.Sprintf("%s %v %s", op.GetTarget(), op.GetActionType(), op.GetOriginalRequester()))
	}
	wantOps := []string{
		"experiments/123/workUnits/4 CREATE test@example.com",
		"experiments/123/workUnits/5 CREATE test@example.com",
		"experiments/123/workUnits/6 CREATE test@example.com",
	}
	if diff := cmp.Diff(wantOps, gotOps); diff != "" {
		t.Errorf("operations mismatch (-want +got):\n%s", diff)
	}
	if actuator.EnqueueBatchCalls != 1 || len(actuator.EnqueuedTasks) != 3 {
		t.Fatalf("enqueue batch calls = %d, tasks = %d; want 1, 3", actuator.EnqueueBatchCalls, len(actuator.EnqueuedTasks))
	}
	for i, task := range actuator.EnqueuedTasks {
		wid := int64(4 + i)
		wantTarget := validator.ConstructWorkUnitExecutionName(123, wid, 1)
		if task.Target != wantTarget || !strings.Contains(task.OperationName, fmt.Sprintf("workUnits/%d/operations/", wid)) ||
			task.Action.GetCreate().GetExperimentPriority() != msgpb.Priority_HIGH || task.Action.GetCreate().GetAuthor() != "test@example.com" {
			t.Errorf("task %d = %+v, want target %s with its operation and a HIGH priority create action", i, task, wantTarget)
		}
	}
}

func TestBatchCreateWorkUnits_Rejections(t *testing.T) {
	tests := []struct {
		name       string
		storage    func() *batchStorage
		req        *msgpb.BatchCreateWorkUnitsRequest
		opts       []testServerOpt
		wantCode   codes.Code
		wantErrSub string
	}{
		{name: "unauthenticated", opts: []testServerOpt{withAuthorizer(&mockAuthenticator{err: grpcerr.ErrUnauthenticated})}, wantCode: codes.Unauthenticated},
		{name: "invalid parent", req: batchRequest(1, func(r *msgpb.BatchCreateWorkUnitsRequest) { r.Parent = "experiments/x" }), wantCode: codes.InvalidArgument},
		{name: "no requests", req: batchRequest(0, nil), wantCode: codes.InvalidArgument},
		{name: "experiment not found", storage: func() *batchStorage {
			st := newBatchStorage("test@example.com")
			st.FetchExperimentErr = status.Error(codes.NotFound, "missing")
			return st
		}, wantCode: codes.NotFound},
		{name: "experiment absent", storage: func() *batchStorage {
			st := newBatchStorage("test@example.com")
			st.Experiment = nil
			return st
		}, wantCode: codes.NotFound},
		{name: "experiment read failure", storage: func() *batchStorage {
			st := newBatchStorage("test@example.com")
			st.FetchExperimentErr = errors.New("spanner down")
			return st
		}, wantCode: codes.Internal},
		{name: "standard experiment", storage: func() *batchStorage {
			st := newBatchStorage("test@example.com")
			st.Experiment.ExperimentType = msgpb.Experiment_STANDARD
			return st
		}, wantCode: codes.Unimplemented},
		{name: "caller is not the study controller", storage: func() *batchStorage { return newBatchStorage("controller@p.iam.gserviceaccount.com") }, wantCode: codes.PermissionDenied},
		{name: "other caller with an invalid payload", storage: func() *batchStorage { return newBatchStorage("controller@p.iam.gserviceaccount.com") }, req: batchRequest(0, nil), wantCode: codes.PermissionDenied},
		{name: "other caller with a missing experiment", storage: func() *batchStorage {
			st := newBatchStorage("controller@p.iam.gserviceaccount.com")
			st.Experiment = nil
			return st
		}, wantCode: codes.PermissionDenied},
		{name: "other caller with a standard experiment", storage: func() *batchStorage {
			st := newBatchStorage("controller@p.iam.gserviceaccount.com")
			st.Experiment.ExperimentType = msgpb.Experiment_STANDARD
			return st
		}, wantCode: codes.PermissionDenied},
		{name: "no study controller configured", storage: func() *batchStorage { return newBatchStorage("") }, wantCode: codes.PermissionDenied},
		{name: "hpo component absent", storage: func() *batchStorage {
			st := newBatchStorage("test@example.com")
			st.ComponentRepository = &storage.MockComponentRepository{}
			return st
		}, wantCode: codes.PermissionDenied},
		{name: "component catalog failure", storage: func() *batchStorage {
			st := newBatchStorage("test@example.com")
			st.ComponentRepository = &storage.MockComponentRepository{GetActiveComponentErr: errors.New("catalog down")}
			return st
		}, wantCode: codes.Internal},
		{name: "parameters do not match the study", req: batchRequest(2, func(r *msgpb.BatchCreateWorkUnitsRequest) {
			r.Requests[1].WorkUnit.Parameters.Fields["momentum"] = r.Requests[1].WorkUnit.Parameters.Fields["dropout"]
		}), wantCode: codes.InvalidArgument, wantErrSub: "requests[1]"},
		{name: "missing trial ID", req: batchRequest(1, func(r *msgpb.BatchCreateWorkUnitsRequest) { r.Requests[0].WorkUnit.TrialId = "" }), wantCode: codes.InvalidArgument},
		{name: "unknown execution environment", opts: []testServerOpt{withConfig(&config.Config{DefaultExecutionEnvironment: "unknown"})}, wantCode: codes.Internal, wantErrSub: "requests[0]"},
		{name: "storage failure", storage: func() *batchStorage {
			st := newBatchStorage("test@example.com")
			st.InsertWorkUnitsErr = errors.New("insert failed")
			return st
		}, wantCode: codes.Internal},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			st := newBatchStorage("test@example.com")
			if tt.storage != nil {
				st = tt.storage()
			}
			actuator := &tq.FakeClient{}
			server := newBatchServer(t, st, actuator, tt.opts...)
			req := tt.req
			if req == nil {
				req = batchRequest(1, nil)
			}

			resp, err := server.BatchCreateWorkUnits(context.Background(), req)

			if status.Code(err) != tt.wantCode || resp != nil {
				t.Fatalf("BatchCreateWorkUnits() = (%v, %v), want code %v", resp, err, tt.wantCode)
			}
			if !strings.Contains(err.Error(), tt.wantErrSub) {
				t.Errorf("BatchCreateWorkUnits() = %v, want it to contain %q", err, tt.wantErrSub)
			}
			if len(st.InsertedWorkUnits) != 0 || actuator.EnqueueBatchCalls != 0 {
				t.Errorf("rejected request inserted %d work units and made %d enqueue calls, want none", len(st.InsertedWorkUnits), actuator.EnqueueBatchCalls)
			}
		})
	}
}

func TestBatchCreateWorkUnits_ErrorsHideAuthorizationDetails(t *testing.T) {
	detailed := fmt.Errorf("component \"hpo\" has no valid studyControllerServiceAccount parameter: %w", workunit.ErrNotStudyController)
	if err := batchCreateError(detailed); err != workunit.ErrNotStudyController {
		t.Errorf("batchCreateError(%v) = %v, want exactly ErrNotStudyController", detailed, err)
	}
	unsupported := fmt.Errorf("experiment 123: %w", workunit.ErrBatchCreateRequiresStudy)
	if err := batchCreateError(unsupported); status.Code(err) != codes.Unimplemented {
		t.Errorf("batchCreateError(%v) code = %v, want Unimplemented", unsupported, status.Code(err))
	}
}

func TestBatchCreateWorkUnits_PartialSchedulingFailure(t *testing.T) {
	tests := []struct {
		name        string
		mutate      func(st *batchStorage, actuator *tq.FakeClient)
		wantTasks   int
		wantFailed  string
		wantStopped int
	}{
		{
			name: "enqueue fails for one work unit",
			mutate: func(_ *batchStorage, actuator *tq.FakeClient) {
				actuator.EnqueueBatchErrs = []error{nil, errors.New("redis down"), nil}
			},
			wantTasks: 3, wantFailed: "1 of 3 work units", wantStopped: 1,
		},
		{
			name: "operation fails for one work unit",
			mutate: func(st *batchStorage, _ *tq.FakeClient) {
				st.operationErrs = map[string]error{"experiments/123/workUnits/6": errors.New("operation write failed")}
			},
			wantTasks: 2, wantFailed: "experiments/123/workUnits/6", wantStopped: 0,
		},
		{
			name: "created work unit name cannot be parsed",
			mutate: func(st *batchStorage, _ *tq.FakeClient) {
				st.NextWorkUnitID = -5
			},
			wantTasks: 0, wantFailed: "3 of 3 work units", wantStopped: 0,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			st := newBatchStorage("test@example.com")
			actuator := &tq.FakeClient{}
			tt.mutate(st, actuator)
			server := newBatchServer(t, st, actuator)

			_, err := server.BatchCreateWorkUnits(context.Background(), batchRequest(3, nil))

			if !errors.Is(err, grpcerr.ErrInternal) || !strings.Contains(err.Error(), tt.wantFailed) {
				t.Errorf("BatchCreateWorkUnits() = %v, want Internal naming %q", err, tt.wantFailed)
			}
			if actuator.EnqueueBatchCalls != 1 || len(actuator.EnqueuedTasks) != tt.wantTasks {
				t.Errorf("enqueue batch calls = %d, tasks = %d; want 1, %d", actuator.EnqueueBatchCalls, len(actuator.EnqueuedTasks), tt.wantTasks)
			}
			if st.UpdateWorkUnitOperationCalls != tt.wantStopped {
				t.Errorf("stopped operations = %d, want %d", st.UpdateWorkUnitOperationCalls, tt.wantStopped)
			}
		})
	}
}
