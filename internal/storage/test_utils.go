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
package storage

import (
	"context"
	"time"

	"golang/protobuf/v2/proto/proto"
	lropb "longrunning/operations_go_proto"
	essmsgpb "xmanager_cloud/experiment_state_server/proto/messages_go_proto"
	msgpb "xmanager_cloud/xid_service/proto/messages_go_proto"
)

// MockStorage is a mock implementation of Storage for testing.
type MockStorage struct {
	Storage
	CreateErr error
	FetchErr  error
	UpdateErr error
	ListErr   error

	ListXidEntriesResp          []*msgpb.XidEntry
	ListXidEntriesNextPageToken string

	CreateArtifactErr  error
	Artifact           *essmsgpb.Artifact
	FetchArtifactErr   error
	ListArtifactsResp  []*essmsgpb.Artifact
	ListArtifactsErr   error
	UpdateArtifactErr  error
	UpdateArtifactResp *essmsgpb.Artifact
	DeleteArtifactErr  error

	FetchExperimentErr               error
	Experiment                       *essmsgpb.Experiment
	FetchExperimentStatusErr         error
	ExperimentStatus                 *essmsgpb.ExperimentStatus
	FetchWorkUnitErr                 error
	WorkUnit                         *essmsgpb.WorkUnit
	FetchCurrentWorkUnitExecutionErr error
	WorkUnitExecution                *essmsgpb.WorkUnitExecution
	CreateExperimentErr              error
	CreateWorkUnitErr                error
	AddWorkUnitOperationErr          error
	LROOp                            *lropb.Operation
	UpdateWorkUnitExecutionsErr      error
	RestartWorkUnitExecutionsResp    map[int64]*essmsgpb.WorkUnitExecution
	RestartWorkUnitExecutionsErr     error
	ListWorkUnitsResp                []*essmsgpb.WorkUnit
	ListWorkUnitsErr                 error
	UpdateExperimentLaunchStateErr   error
	ListWorkUnitExecutionsErr        error
	ListWorkUnitExecutionsResp       []*essmsgpb.WorkUnitExecution
	ListExperimentsResp              []*essmsgpb.Experiment
	NextPageToken                    string
	ListExperimentsErr               error
	UpdateExperimentErr              error
	UpdateWorkUnitErr                error
	UpdateWorkUnitRespOriginal       *essmsgpb.WorkUnit
	UpdateWorkUnitRespReturned       *essmsgpb.WorkUnit
	UpdateWorkUnitOperationErr       error
	UpdateWorkUnitOperationCalls     int
	GotOperationUpdate               *OperationUpdate

	ListStatusMessagesResp []*essmsgpb.StatusMessage
	ListStatusMessagesErr  error
}

// CreateXidEntry mocks XID entry creation.
func (s *MockStorage) CreateXidEntry(ctx context.Context, xidEntry *msgpb.XidEntry) (*msgpb.XidEntry, error) {
	return nil, s.CreateErr
}

// FetchXidEntry mocks XID entry fetching.
func (s *MockStorage) FetchXidEntry(ctx context.Context, xid int64) (*msgpb.XidEntry, error) {
	if s.FetchErr == nil {
		return &msgpb.XidEntry{Name: proto.String("xids/1"), Acls: &msgpb.XidAcls{
			Owners: []*msgpb.XidAcls_Role{{Principal: &msgpb.XidAcls_Role_User{User: "test@example.com"}}},
		}}, nil
	}
	return nil, s.FetchErr
}

// UpdateXidEntry mocks XID entry updating.
func (s *MockStorage) UpdateXidEntry(ctx context.Context, update *XidEntryUpdate) (*msgpb.XidEntry, error) {
	return nil, s.UpdateErr
}

// ListXidEntries mocks XID entry listing.
func (s *MockStorage) ListXidEntries(ctx context.Context, pageSize int32, pageToken, filter, orderBy string, staleness time.Duration) ([]*msgpb.XidEntry, string, error) {
	return s.ListXidEntriesResp, s.ListXidEntriesNextPageToken, s.ListErr
}

// FetchExperiment mocks experiment fetching.
func (s *MockStorage) FetchExperiment(ctx context.Context, xid int64) (*essmsgpb.Experiment, error) {
	return s.Experiment, s.FetchExperimentErr
}

// FetchExperimentStatus mocks experiment status fetching.
func (s *MockStorage) FetchExperimentStatus(ctx context.Context, xid int64) (*essmsgpb.ExperimentStatus, error) {
	return s.ExperimentStatus, s.FetchExperimentStatusErr
}

// FetchWorkUnit mocks work unit fetching.
func (s *MockStorage) FetchWorkUnit(ctx context.Context, xid int64, wid int64) (*essmsgpb.WorkUnit, error) {
	return s.WorkUnit, s.FetchWorkUnitErr
}

// FetchCurrentWorkUnitExecution mocks current work unit execution fetching.
func (s *MockStorage) FetchCurrentWorkUnitExecution(ctx context.Context, xid int64, wid int64) (*essmsgpb.WorkUnitExecution, error) {
	return s.WorkUnitExecution, s.FetchCurrentWorkUnitExecutionErr
}

// CreateArtifact mocks artifact creation.
func (s *MockStorage) CreateArtifact(ctx context.Context, artifact *essmsgpb.Artifact) (*essmsgpb.Artifact, error) {
	if s.CreateArtifactErr != nil {
		return nil, s.CreateArtifactErr
	}
	return s.Artifact, nil
}

// FetchArtifact mocks artifact fetching.
func (s *MockStorage) FetchArtifact(ctx context.Context, aid int64) (*essmsgpb.Artifact, error) {
	return s.Artifact, s.FetchArtifactErr
}

// ListArtifacts mocks artifact listing.
func (s *MockStorage) ListArtifacts(ctx context.Context, parent string, pageSize int32, pageToken string, filter string, orderBy string, staleness time.Duration) ([]*essmsgpb.Artifact, string, error) {
	return s.ListArtifactsResp, s.NextPageToken, s.ListArtifactsErr
}

// UpdateArtifact mocks artifact updating.
func (s *MockStorage) UpdateArtifact(ctx context.Context, update *ArtifactUpdate) (UpdatedArtifacts, error) {
	if s.UpdateArtifactErr != nil {
		return UpdatedArtifacts{}, s.UpdateArtifactErr
	}
	if s.Artifact != nil {
		return UpdatedArtifacts{Original: s.Artifact, Updated: s.Artifact}, nil
	}
	return UpdatedArtifacts{Original: update.Artifact, Updated: update.Artifact}, nil
}

// DeleteArtifact mocks artifact deletion.
func (s *MockStorage) DeleteArtifact(ctx context.Context, aid int64) error {
	return s.DeleteArtifactErr
}

// CreateExperiment mocks experiment creation.
func (s *MockStorage) CreateExperiment(ctx context.Context, exp *essmsgpb.Experiment) (*essmsgpb.Experiment, error) {
	if s.CreateExperimentErr != nil {
		return nil, s.CreateExperimentErr
	}
	return s.Experiment, nil
}

// CreateWorkUnit mocks work unit creation.
func (s *MockStorage) CreateWorkUnit(ctx context.Context, wu *essmsgpb.WorkUnit) (*essmsgpb.WorkUnit, error) {
	if s.CreateWorkUnitErr != nil {
		return nil, s.CreateWorkUnitErr
	}
	return wu, nil
}

// AddWorkUnitOperation mocks adding a work unit operation.
func (s *MockStorage) AddWorkUnitOperation(ctx context.Context, xid int64, wid int64, metadata *essmsgpb.OperationMetadata) (*lropb.Operation, error) {
	if s.AddWorkUnitOperationErr != nil {
		return nil, s.AddWorkUnitOperationErr
	}
	return s.LROOp, nil
}

// UpdateWorkUnitExecutions mocks updating work unit executions.
func (s *MockStorage) UpdateWorkUnitExecutions(ctx context.Context, xid int64, updates []*WorkUnitExecutionUpdate) (map[int64]*essmsgpb.WorkUnitExecution, error) {
	return nil, s.UpdateWorkUnitExecutionsErr
}

// RestartWorkUnitExecutions mocks restarting work unit executions.
func (s *MockStorage) RestartWorkUnitExecutions(ctx context.Context, requester string, xid int64, wids []int64) (map[int64]*essmsgpb.WorkUnitExecution, error) {
	return s.RestartWorkUnitExecutionsResp, s.RestartWorkUnitExecutionsErr
}

// ListWorkUnits mocks listing work units.
func (s *MockStorage) ListWorkUnits(ctx context.Context, parent string, pageSize int32, pageToken string, filter string, orderBy string, staleness time.Duration) ([]*essmsgpb.WorkUnit, string, error) {
	return s.ListWorkUnitsResp, s.NextPageToken, s.ListWorkUnitsErr
}

// UpdateExperimentLaunchState mocks updating experiment launch state.
func (s *MockStorage) UpdateExperimentLaunchState(ctx context.Context, xid int64, launchState essmsgpb.ExperimentStatus_LaunchState) (*essmsgpb.Experiment, error) {
	if s.UpdateExperimentLaunchStateErr != nil {
		return nil, s.UpdateExperimentLaunchStateErr
	}
	return s.Experiment, nil
}

// ListWorkUnitExecutions mocks listing work unit executions.
func (s *MockStorage) ListWorkUnitExecutions(ctx context.Context, xid int64, wid int64) ([]*essmsgpb.WorkUnitExecution, error) {
	return s.ListWorkUnitExecutionsResp, s.ListWorkUnitExecutionsErr
}

// ListExperiments mocks listing experiments.
func (s *MockStorage) ListExperiments(ctx context.Context, pageSize int32, pageToken string, filter string, orderBy string, staleness time.Duration) ([]*essmsgpb.Experiment, string, error) {
	return s.ListExperimentsResp, s.NextPageToken, s.ListExperimentsErr
}

// UpdateExperiment mocks updating an experiment.
func (s *MockStorage) UpdateExperiment(ctx context.Context, update *ExperimentUpdate) (UpdatedExperiments, error) {
	if s.UpdateExperimentErr != nil {
		return UpdatedExperiments{}, s.UpdateExperimentErr
	}
	if s.Experiment != nil {
		return UpdatedExperiments{Original: s.Experiment, Updated: s.Experiment}, nil
	}
	return UpdatedExperiments{Original: update.Experiment, Updated: update.Experiment}, nil
}

// UpdateWorkUnit mocks updating a work unit.
func (s *MockStorage) UpdateWorkUnit(ctx context.Context, update *WorkUnitUpdate) (UpdatedWorkUnits, error) {
	if s.UpdateWorkUnitErr != nil {
		return UpdatedWorkUnits{}, s.UpdateWorkUnitErr
	}
	if s.UpdateWorkUnitRespOriginal != nil && s.UpdateWorkUnitRespReturned != nil {
		return UpdatedWorkUnits{Original: s.UpdateWorkUnitRespOriginal, Updated: s.UpdateWorkUnitRespReturned}, nil
	}
	if s.WorkUnit != nil {
		return UpdatedWorkUnits{Original: s.WorkUnit, Updated: s.WorkUnit}, nil
	}
	return UpdatedWorkUnits{Original: update.WorkUnit, Updated: update.WorkUnit}, nil
}

// UpdateWorkUnitOperation mocks updating a work unit operation.
func (s *MockStorage) UpdateWorkUnitOperation(ctx context.Context, update *OperationUpdate) (*lropb.Operation, error) {
	s.UpdateWorkUnitOperationCalls++
	s.GotOperationUpdate = update
	if s.UpdateWorkUnitOperationErr != nil {
		return nil, s.UpdateWorkUnitOperationErr
	}
	return update.Operation, nil
}

// ListStatusMessages mocks listing status messages.
func (s *MockStorage) ListStatusMessages(ctx context.Context, parent string, pageSize int32, pageToken, filter, orderBy string, staleness time.Duration) ([]*essmsgpb.StatusMessage, string, error) {
	return s.ListStatusMessagesResp, s.NextPageToken, s.ListStatusMessagesErr
}
