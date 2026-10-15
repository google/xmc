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

	lropb "cloud.google.com/go/longrunning/autogen/longrunningpb"
	"google.golang.org/protobuf/proto"
	essmsgpb "xmanager_cloud/api/protos/experiment"
	msgpb "xmanager_cloud/api/protos/xid"
	"xmanager_cloud/internal/grpcerr"
)

// MockXidTxRepository is an in-memory mock implementation of XidTxRepository for unit testing.
type MockXidTxRepository struct {
	Entries      map[int64]*msgpb.XidEntry
	FetchErr     error
	UpdateErr    error
	UpdatedEntry *msgpb.XidEntry
	UpdateCalls  int
}

// FetchXidEntry retrieves an XidEntry from the mock repository.
func (m *MockXidTxRepository) FetchXidEntry(_ context.Context, xid int64) (*msgpb.XidEntry, error) {
	if m.FetchErr != nil {
		return nil, m.FetchErr
	}
	if m.Entries != nil {
		entry, ok := m.Entries[xid]
		if !ok {
			return nil, grpcerr.ErrNotFound
		}
		return proto.Clone(entry).(*msgpb.XidEntry), nil
	}
	return &msgpb.XidEntry{
		Name: proto.String("xids/1"),
		Acls: &msgpb.XidAcls{
			Owners: []*msgpb.XidAcls_Role{{Principal: &msgpb.XidAcls_Role_User{User: "test@example.com"}}},
		},
	}, nil
}

// UpdateXidEntry persists an XidEntry in the mock repository.
func (m *MockXidTxRepository) UpdateXidEntry(_ context.Context, _ int64, entry *msgpb.XidEntry) error {
	m.UpdateCalls++
	if m.UpdateErr != nil {
		return m.UpdateErr
	}
	m.UpdatedEntry = proto.Clone(entry).(*msgpb.XidEntry)
	return nil
}

// MockStorage is a mock implementation of Storage for testing.
type MockStorage struct {
	Storage
	CreateErr error
	FetchErr  error
	ListErr   error

	ListXidEntriesResp          []*msgpb.XidEntry
	ListXidEntriesNextPageToken string
	XidTxRepository             XidTxRepository

	CreateArtifactErr  error
	Artifact           *essmsgpb.Artifact
	FetchArtifactErr   error
	ListArtifactsResp  []*essmsgpb.Artifact
	ListArtifactsErr   error
	UpdateArtifactErr  error
	UpdateArtifactResp *essmsgpb.Artifact
	DeleteArtifactErr  error

	FetchExperimentErr                error
	Experiment                        *essmsgpb.Experiment
	FetchExperimentStatusErr          error
	ExperimentStatus                  *essmsgpb.ExperimentStatus
	FetchWorkUnitErr                  error
	WorkUnit                          *essmsgpb.WorkUnit
	FetchCurrentWorkUnitExecutionErr  error
	WorkUnitExecution                 *essmsgpb.WorkUnitExecution
	CreateExperimentErr               error
	CreateWorkUnitErr                 error
	AddWorkUnitOperationErr           error
	LROOp                             *lropb.Operation
	UpdateCurrentWorkUnitExecutionErr error
	UpdatedWorkUnitExecutions         map[int64]*essmsgpb.WorkUnitExecution
	ListWorkUnitsResp                 []*essmsgpb.WorkUnit
	ListWorkUnitsErr                  error
	ListWorkUnitExecutionsErr         error
	ListWorkUnitExecutionsResp        []*essmsgpb.WorkUnitExecution
	ListExperimentsResp               []*essmsgpb.Experiment
	NextPageToken                     string
	ListExperimentsErr                error
	UpdateExperimentErr               error
	UpdateWorkUnitErr                 error
	UpdateWorkUnitRespOriginal        *essmsgpb.WorkUnit
	UpdateWorkUnitRespReturned        *essmsgpb.WorkUnit
	UpdateWorkUnitOperationErr        error
	UpdateWorkUnitOperationCalls      int
	GotOperationUpdate                *OperationUpdate
	ListStatusMessagesResp            []*essmsgpb.StatusMessage
	ListStatusMessagesErr             error

	RunInTransactionErr error
	// RunInTransactionAttempts is how many times RunInTransaction invokes fn, simulating
	// Spanner re-running the closure after an abort. Values below 2 invoke fn once.
	RunInTransactionAttempts int
	// UpdatedExperiment records the last experiment passed to UpdateExperimentStatus.
	UpdatedExperiment *essmsgpb.Experiment

	PauseStateRepository PauseStateRepository
	PausedTaskRepository PausedTaskRepository
	// Transaction optionally overrides Transaction.XIDs, Transaction.PauseState, and
	// Transaction.PausedTasks passed to RunInTransaction callbacks; other fields are ignored.
	Transaction *Transaction
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

// ListXidEntries mocks XID entry listing.
func (s *MockStorage) ListXidEntries(ctx context.Context, pageSize int32, pageToken, filter, orderBy string, staleness time.Duration) ([]*msgpb.XidEntry, string, error) {
	return s.ListXidEntriesResp, s.ListXidEntriesNextPageToken, s.ListErr
}

// FetchExperiment mocks experiment fetching.
func (s *MockStorage) FetchExperiment(ctx context.Context, xid int64) (*essmsgpb.Experiment, error) {
	return proto.Clone(s.Experiment).(*essmsgpb.Experiment), s.FetchExperimentErr
}

// FetchExperimentStatus mocks experiment status fetching.
func (s *MockStorage) FetchExperimentStatus(ctx context.Context, xid int64) (*essmsgpb.ExperimentStatus, error) {
	return s.ExperimentStatus, s.FetchExperimentStatusErr
}

// FetchWorkUnit mocks work unit fetching. Like real storage, it returns a fresh copy on every call.
func (s *MockStorage) FetchWorkUnit(ctx context.Context, xid int64, wid int64) (*essmsgpb.WorkUnit, error) {
	return proto.Clone(s.WorkUnit).(*essmsgpb.WorkUnit), s.FetchWorkUnitErr
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

// FetchUpdatingWorkUnits mocks fetching a batch of work units for update within a transaction.
func (s *MockStorage) FetchUpdatingWorkUnits(ctx context.Context, xid int64, wids []int64) (map[int64]*essmsgpb.WorkUnit, error) {
	if s.FetchWorkUnitErr != nil {
		return nil, s.FetchWorkUnitErr
	}
	if s.WorkUnit == nil {
		return nil, nil
	}
	res := make(map[int64]*essmsgpb.WorkUnit, len(wids))
	for _, wid := range wids {
		res[wid] = proto.Clone(s.WorkUnit).(*essmsgpb.WorkUnit)
	}
	return res, nil
}

// ListWorkUnitMessagesBySeverity mocks listing work unit status messages by severity within a transaction.
func (s *MockStorage) ListWorkUnitMessagesBySeverity(ctx context.Context, xid int64, severity essmsgpb.StructuredMessage_Severity) ([]*essmsgpb.StructuredMessage, error) {
	return nil, nil
}

// RunInTransaction mocks running a function within a transaction context.
func (s *MockStorage) RunInTransaction(ctx context.Context, fn func(ctx context.Context, tx *Transaction) error) error {
	if s.RunInTransactionErr != nil {
		return s.RunInTransactionErr
	}
	pauseStateTx, _ := s.PauseStateRepository.(PauseStateTxRepository)
	pausedTaskTx, _ := s.PausedTaskRepository.(PausedTaskTxRepository)
	xidsTx := s.XidTxRepository
	if xidsTx == nil {
		xidsTx = &MockXidTxRepository{FetchErr: s.FetchErr}
	}
	tx := &Transaction{
		WorkUnits:      s,
		Experiments:    s,
		XIDs:           xidsTx,
		StatusMessages: s,
		StatusHistory:  s,
		PauseState:     pauseStateTx,
		PausedTasks:    pausedTaskTx,
	}
	if s.Transaction != nil {
		if s.Transaction.XIDs != nil {
			tx.XIDs = s.Transaction.XIDs
		}
		if s.Transaction.PauseState != nil {
			tx.PauseState = s.Transaction.PauseState
		}
		if s.Transaction.PausedTasks != nil {
			tx.PausedTasks = s.Transaction.PausedTasks
		}
	}
	for attempt := 1; attempt < s.RunInTransactionAttempts; attempt++ {
		if err := fn(ctx, tx); err != nil {
			return err
		}
	}
	return fn(ctx, tx)
}

// PauseState returns the configured pause state repository (nil if unset).
func (s *MockStorage) PauseState() PauseStateRepository {
	return s.PauseStateRepository
}

// PausedTasks returns the configured paused task repository (nil if unset).
func (s *MockStorage) PausedTasks() PausedTaskRepository {
	return s.PausedTaskRepository
}

// SaveWorkUnitExecution mocks saving a work unit execution.
func (s *MockStorage) SaveWorkUnitExecution(ctx context.Context, execution *essmsgpb.WorkUnitExecution) error {
	return nil
}

// UpdateCurrentWorkUnitExecution mocks updating the current work unit execution.
func (s *MockStorage) UpdateCurrentWorkUnitExecution(ctx context.Context, xid, wid, eid int64, execution *essmsgpb.WorkUnitExecution) error {
	if s.UpdateCurrentWorkUnitExecutionErr != nil {
		return s.UpdateCurrentWorkUnitExecutionErr
	}
	if s.UpdatedWorkUnitExecutions == nil {
		s.UpdatedWorkUnitExecutions = make(map[int64]*essmsgpb.WorkUnitExecution)
	}
	s.UpdatedWorkUnitExecutions[wid] = proto.Clone(execution).(*essmsgpb.WorkUnitExecution)
	return nil
}

// SaveWorkUnitStatusHistoryIfChanged mocks saving work unit status history if changed.
func (s *MockStorage) SaveWorkUnitStatusHistoryIfChanged(ctx context.Context, xid, wid int64, eid *int64, queueName string, prevState, newState *essmsgpb.WorkUnitStatus_WorkUnitState, resources map[string]int64, timestamp time.Time) error {
	return nil
}

// SaveStatusMessageHistoryIfChanged mocks saving status message history if changed.
func (s *MockStorage) SaveStatusMessageHistoryIfChanged(ctx context.Context, xid int64, wid, eid *int64, oldMsg, newMsg *essmsgpb.StructuredMessage) error {
	return nil
}

// UpdateExperimentStatus mocks updating experiment status and records the experiment.
func (s *MockStorage) UpdateExperimentStatus(ctx context.Context, exp *essmsgpb.Experiment) error {
	s.UpdatedExperiment = exp
	return nil
}

// ListWorkUnits mocks listing work units. Like real storage, it returns fresh copies on every call.
func (s *MockStorage) ListWorkUnits(ctx context.Context, parent string, pageSize int32, pageToken string, filter string, orderBy string, staleness time.Duration) ([]*essmsgpb.WorkUnit, string, error) {
	if s.ListWorkUnitsResp == nil {
		return nil, s.NextPageToken, s.ListWorkUnitsErr
	}
	workUnits := make([]*essmsgpb.WorkUnit, len(s.ListWorkUnitsResp))
	for i, wu := range s.ListWorkUnitsResp {
		workUnits[i] = proto.Clone(wu).(*essmsgpb.WorkUnit)
	}
	return workUnits, s.NextPageToken, s.ListWorkUnitsErr
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
