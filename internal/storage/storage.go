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

// Package storage defines the interface for all data persistence operations
// required by the Experiment State Server.
package storage

import (
	"context"
	"time"

	longrunningpb "longrunning/operations_go_proto"
	fmpb "protobuf/field_mask_go_proto"
	essmsgpb "xmanager_cloud/experiment_state_server/proto/messages_go_proto"
	xidmsgpb "xmanager_cloud/xid_service/proto/messages_go_proto"
)

// ExperimentUpdate represents an update to an experiment.
type ExperimentUpdate struct {
	XID        int64
	Experiment *essmsgpb.Experiment
	UpdateMask *fmpb.FieldMask
}

// OperationUpdate represents an update to an operation.
type OperationUpdate struct {
	XID        int64
	WID        int64
	OID        string
	Operation  *longrunningpb.Operation
	UpdateMask *fmpb.FieldMask
}

// WorkUnitUpdate represents an update to a work unit.
type WorkUnitUpdate struct {
	XID        int64
	WID        int64
	WorkUnit   *essmsgpb.WorkUnit
	UpdateMask *fmpb.FieldMask
}

// ArtifactUpdate represents an update to an artifact.
type ArtifactUpdate struct {
	AID        int64
	Artifact   *essmsgpb.Artifact
	UpdateMask *fmpb.FieldMask
}

// WorkUnitExecutionUpdate represents an update to a work unit status.
type WorkUnitExecutionUpdate struct {
	WID int64
	EID int64
	// ForceStatusMessageUpdate forces the status message to be updated even if the incoming
	// message has a lower severity than the current status message. This is used to update the
	// status message when the previous message is no longer relevant. E.G. if an error occurs
	// while the job is starting, but the job eventually starts successfully, the previous error
	// message should be cleared.
	// If true, DeferToExistingMessage is ignored.
	// It is a fault to set this field if the update mask does not include the status message.
	ForceStatusMessageUpdate bool
	// DeferToExistingMessage defers to the existing status message if it has the same severity
	// as the incoming message. For example, if we want to print an error message when a job
	// fails, but we want to keep any existing error message if one already exists, this can be
	// used.
	// It is a fault to set this field if the update mask does not include the status message.
	DeferToExistingMessage bool
	WorkUnitExecution      *essmsgpb.WorkUnitExecution
	UpdateMask             *fmpb.FieldMask
}

// XidEntryUpdate represents an update to an XidEntry.
type XidEntryUpdate struct {
	// XID is the unique ID of the XidEntry to update.
	XID int64
	// Creator is the email address of the user making the update request.
	Creator string
	// XidEntry is the XidEntry to update.
	XidEntry *xidmsgpb.XidEntry
	// UpdateMask is the field mask to use for the update.
	UpdateMask *fmpb.FieldMask
}

// -----------------------
// Experiment Methods
// -----------------------

// ExperimentStore defines storage operations for experiments.
type ExperimentStore interface {
	// CreateExperiment creates a new experiment.
	CreateExperiment(ctx context.Context, exp *essmsgpb.Experiment) (*essmsgpb.Experiment, error)

	// FetchExperiment retrieves an experiment by its unique ID.
	FetchExperiment(ctx context.Context, xid int64) (*essmsgpb.Experiment, error)

	// FetchExperimentStatus retrieves only the status of an experiment by its unique ID.
	FetchExperimentStatus(ctx context.Context, xid int64) (*essmsgpb.ExperimentStatus, error)

	// ListExperiments retrieves a list of experiments matching the given filter. Staleness should
	// be set to 0 for strong reads, and up to 15 seconds for stale reads.
	// It uses cursor pagination.
	ListExperiments(ctx context.Context, pageSize int32, pageToken, filter, orderBy string, staleness time.Duration) ([]*essmsgpb.Experiment, string, error)

	// UpdateExperiment updates an experiment with the given update mask. It returns both the
	// original and updated experiments, so that the caller can choose whether to perform
	// additional actions based on the original experiment state.
	UpdateExperiment(ctx context.Context, update *ExperimentUpdate) (UpdatedExperiments, error)

	// UpdateExperimentLaunchState updates the launch state of an experiment.
	UpdateExperimentLaunchState(ctx context.Context, xid int64, launchState essmsgpb.ExperimentStatus_LaunchState) (*essmsgpb.Experiment, error)
}

// UpdatedExperiments contains the original and updated experiment protos.
type UpdatedExperiments struct {
	Original *essmsgpb.Experiment
	Updated  *essmsgpb.Experiment
}

// -----------------------
// WorkUnit Methods
// -----------------------

// WorkUnitStore defines storage operations for work units and work unit executions.
type WorkUnitStore interface {
	// CreateWorkUnit creates a new work unit as a sub-collection of an experiment.
	CreateWorkUnit(ctx context.Context, wu *essmsgpb.WorkUnit) (*essmsgpb.WorkUnit, error)

	// FetchWorkUnit retrieves a work unit by its unique ID.
	FetchWorkUnit(ctx context.Context, xid int64, wid int64) (*essmsgpb.WorkUnit, error)

	// ListWorkUnits retrieves a list of work units matching the given filter. Staleness should
	// be set to 0 for strong reads, and up to 15 seconds for stale reads.
	// It uses cursor pagination.
	ListWorkUnits(ctx context.Context, parent string, pageSize int32, pageToken, filter, orderBy string, staleness time.Duration) ([]*essmsgpb.WorkUnit, string, error)

	// UpdateWorkUnit updates a work unit with the given update mask. It returns both the
	// original and updated work units, so that the caller can choose whether to perform
	// additional actions based on the original work unit state.
	UpdateWorkUnit(ctx context.Context, update *WorkUnitUpdate) (UpdatedWorkUnits, error)

	// -----------------------
	// WorkUnitExecution Methods
	// -----------------------

	// FetchCurrentWorkUnitExecution retrieves the current work unit execution.
	FetchCurrentWorkUnitExecution(ctx context.Context, xid int64, wid int64) (*essmsgpb.WorkUnitExecution, error)

	// ListWorkUnitExecutions retrieves the historical work unit executions for a given work unit.
	ListWorkUnitExecutions(ctx context.Context, xid int64, wid int64) ([]*essmsgpb.WorkUnitExecution, error)
}

// UpdatedWorkUnits contains the original and updated work unit protos.
type UpdatedWorkUnits struct {
	Original *essmsgpb.WorkUnit
	Updated  *essmsgpb.WorkUnit
}

// -----------------------
// StatusMessage Methods
// -----------------------

// StatusMessageStore defines storage operations for status messages.
type StatusMessageStore interface {
	// ListStatusMessages retrieves a list of status messages for an experiment or work unit. Staleness should
	// be set to 0 for strong reads, and up to 15 seconds for stale reads.
	// It uses cursor pagination.
	ListStatusMessages(ctx context.Context, parent string, pageSize int32, pageToken, filter, orderBy string, staleness time.Duration) ([]*essmsgpb.StatusMessage, string, error)
}

// -----------------------
// Artifact Methods
// -----------------------

// ArtifactStore defines storage operations for artifacts.
type ArtifactStore interface {
	// CreateArtifact creates a new artifact.
	CreateArtifact(ctx context.Context, artifact *essmsgpb.Artifact) (*essmsgpb.Artifact, error)

	// FetchArtifact retrieves an artifact by its unique ID.
	FetchArtifact(ctx context.Context, aid int64) (*essmsgpb.Artifact, error)

	// ListArtifacts retrieves a list of artifacts matching the given filter. Staleness should be set
	// to 0 for strong reads, and up to 15 seconds for stale reads.
	// It uses cursor pagination.
	ListArtifacts(ctx context.Context, parent string, pageSize int32, pageToken, filter, orderBy string, staleness time.Duration) ([]*essmsgpb.Artifact, string, error)

	// UpdateArtifact updates an artifact with the given update mask.
	UpdateArtifact(ctx context.Context, update *ArtifactUpdate) (UpdatedArtifacts, error)

	// DeleteArtifact deletes an artifact by its unique ID.
	DeleteArtifact(ctx context.Context, aid int64) error
}

// UpdatedArtifacts contains the original and updated artifact protos.
type UpdatedArtifacts struct {
	Original *essmsgpb.Artifact
	Updated  *essmsgpb.Artifact
}

// -----------------------
// Operation Methods
// -----------------------

// OperationStore defines storage operations for operations.
type OperationStore interface {
	// AddWorkUnitOperation adds an operation to a work unit.
	AddWorkUnitOperation(ctx context.Context, xid int64, wid int64, operationMetadata *essmsgpb.OperationMetadata) (*longrunningpb.Operation, error)

	// FetchWorkUnitOperation retrieves an operation by its unique ID.
	FetchWorkUnitOperation(ctx context.Context, xid int64, wid int64, operationID string) (*longrunningpb.Operation, error)

	// UpdateWorkUnitOperation updates the status of an operation.
	UpdateWorkUnitOperation(ctx context.Context, operationUpdate *OperationUpdate) (*longrunningpb.Operation, error)
}

// -----------------------
// XidEntry Methods
// -----------------------

// XidEntryStore defines storage operations for XID entries.
type XidEntryStore interface {
	// CreateXidEntry creates a new XidEntry.
	CreateXidEntry(ctx context.Context, entry *xidmsgpb.XidEntry) (*xidmsgpb.XidEntry, error)

	// FetchXidEntry retrieves an XidEntry by its unique ID.
	FetchXidEntry(ctx context.Context, xid int64) (*xidmsgpb.XidEntry, error)

	// ListXidEntries retrieves a paginated and filtered list of XidEntries. Staleness should
	// be set to 0 for strong reads, and up to 15 seconds for stale reads.
	// It uses cursor pagination.
	ListXidEntries(ctx context.Context, pageSize int32, pageToken, filter, orderBy string, staleness time.Duration) ([]*xidmsgpb.XidEntry, string, error)

	// UpdateXidEntry updates an XidEntry with the given update mask.
	UpdateXidEntry(ctx context.Context, update *XidEntryUpdate) (*xidmsgpb.XidEntry, error)
}

// -----------------------
// Pause Scheduling Methods
// -----------------------

// PauseStateRepository defines read operations for retrieving the scheduling pause state on the facade.
type PauseStateRepository interface {
	// Get retrieves the current global scheduling pause state.
	// If uninitialized, it returns a safe default with SchedulingPaused = false.
	Get(ctx context.Context) (*PauseSchedulingState, error)
}

// PauseStateTxRepository defines global pause scheduling state operations scoped to a read-write transaction.
type PauseStateTxRepository interface {
	// Get retrieves the current global scheduling pause state.
	// If uninitialized, it returns a safe default with SchedulingPaused = false.
	Get(ctx context.Context) (*PauseSchedulingState, error)
	// Set updates or initializes the global scheduling pause state.
	Set(ctx context.Context, state *PauseSchedulingState) error
}

// PausedTaskRepository defines operations for retrieving paused tasks on the storage facade.
type PausedTaskRepository interface {
	// ListPending retrieves pending paused tasks ordered by Priority DESC, CreateTime ASC.
	// A limit <= 0 returns all pending tasks.
	ListPending(ctx context.Context, limit int) ([]*PausedTask, error)
}

// PausedTaskTxRepository defines paused task operations scoped to a read-write transaction.
type PausedTaskTxRepository interface {
	// Create persists a new paused task.
	// Returns an error wrapping ErrPausedTaskAlreadyExists if (XID, WID, TaskID) already exists.
	Create(ctx context.Context, task *PausedTask) error
	// MarkProcessed records resolution details for a batch of paused tasks.
	// Returns an error wrapping ErrPausedTaskNotFound if any task is missing, and applies nothing.
	MarkProcessed(ctx context.Context, resolutions []PausedTaskResolution) error
}

// PauseSchedulingStore exposes repository accessors for global pause scheduling state
// and pending paused actuation tasks.
type PauseSchedulingStore interface {
	PauseState() PauseStateRepository
	PausedTasks() PausedTaskRepository
}

// WorkUnitTxRepository defines work unit operations scoped to a read-write transaction.
type WorkUnitTxRepository interface {
	// FetchWorkUnit retrieves a work unit by its unique ID within the transaction.
	FetchWorkUnit(ctx context.Context, xid, wid int64) (*essmsgpb.WorkUnit, error)
	// FetchUpdatingWorkUnits retrieves a batch of work units with their current execution and
	// executable locked for update within the transaction.
	FetchUpdatingWorkUnits(ctx context.Context, xid int64, wids []int64) (map[int64]*essmsgpb.WorkUnit, error)
	// ListWorkUnitMessagesBySeverity retrieves work unit status messages matching a given severity
	// level within the transaction.
	ListWorkUnitMessagesBySeverity(ctx context.Context, xid int64, severity essmsgpb.StructuredMessage_Severity) ([]*essmsgpb.StructuredMessage, error)
	// SaveWorkUnitExecution saves a work unit execution within the transaction.
	SaveWorkUnitExecution(ctx context.Context, execution *essmsgpb.WorkUnitExecution) error
	// UpdateCurrentWorkUnitExecution updates the current work unit execution within the transaction.
	UpdateCurrentWorkUnitExecution(ctx context.Context, xid, wid, eid int64, execution *essmsgpb.WorkUnitExecution) error
}

// ExperimentTxRepository defines experiment operations scoped to a read-write transaction.
type ExperimentTxRepository interface {
	// FetchExperiment retrieves an experiment by its unique ID within the transaction.
	FetchExperiment(ctx context.Context, xid int64) (*essmsgpb.Experiment, error)
	// UpdateExperimentStatus updates the experiment status within the transaction.
	UpdateExperimentStatus(ctx context.Context, exp *essmsgpb.Experiment) error
}

// StatusMessageTxRepository defines status message operations scoped to a read-write transaction.
type StatusMessageTxRepository interface {
	// SaveStatusMessageHistoryIfChanged records a status message history within the transaction if the message changed.
	SaveStatusMessageHistoryIfChanged(ctx context.Context, xid int64, wid, eid *int64, oldMsg, newMsg *essmsgpb.StructuredMessage) error
}

// StatusHistoryTxRepository defines status history operations scoped to a read-write transaction.
type StatusHistoryTxRepository interface {
	// SaveWorkUnitStatusHistoryIfChanged records a work unit status history within the transaction if the state changed.
	SaveWorkUnitStatusHistoryIfChanged(ctx context.Context, xid, wid int64, eid *int64, queueName string, prevState, newState *essmsgpb.WorkUnitStatus_WorkUnitState, resources map[string]int64, timestamp time.Time) error
}

// Transaction holds the domain repositories scoped to a single read-write transaction.
type Transaction struct {
	WorkUnits      WorkUnitTxRepository
	Experiments    ExperimentTxRepository
	StatusMessages StatusMessageTxRepository
	StatusHistory  StatusHistoryTxRepository
	PauseState     PauseStateTxRepository
	PausedTasks    PausedTaskTxRepository
}

// TransactionRunner executes an operation within a read-write transaction.
//
// Because the underlying storage engine may abort and re-run fn on lock contention,
// fn MUST be idempotent and re-initialize any local state or slices on each invocation.
// The provided *Transaction is valid only for the duration of fn and must not be retained.
type TransactionRunner interface {
	RunInTransaction(ctx context.Context, fn func(ctx context.Context, tx *Transaction) error) error
}

// Storage defines the interface for all data persistence operations.
type Storage interface {
	TransactionRunner
	ExperimentStore
	WorkUnitStore
	StatusMessageStore
	ArtifactStore
	OperationStore
	XidEntryStore
	PauseSchedulingStore
}
