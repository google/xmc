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
	"errors"
	"time"

	longrunningpb "cloud.google.com/go/longrunning/autogen/longrunningpb"
	fmpb "google.golang.org/protobuf/types/known/fieldmaskpb"
	componentspb "xmanager_cloud/api/protos/components/v1alpha1"
	dpb "xmanager_cloud/api/protos/dashboard"
	essmsgpb "xmanager_cloud/api/protos/experiment"
	xidmsgpb "xmanager_cloud/api/protos/xid"
	"xmanager_cloud/pkg/auth/oauth2"
	"xmanager_cloud/pkg/components/store"
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

// ComponentStateDeleted is the stored component state value for soft-deleted catalog components.
const ComponentStateDeleted = 5

// ComponentDiff describes a single component whose live catalog entry diverges from the
// generation locked in an experiment's component snapshot.
type ComponentDiff struct {
	ComponentID int64
	// Name is nil when the component is missing from the catalog.
	Name             *string
	LockedGeneration int64
	// ObservedGeneration is nil when the live component has not been reconciled yet.
	ObservedGeneration *int64
	// State is nil when the component is missing from the catalog.
	State *int64
}

// ExperimentStore defines storage operations for experiments.
type ExperimentStore interface {
	// FetchExperiment retrieves an experiment by its unique ID.
	FetchExperiment(ctx context.Context, xid int64) (*essmsgpb.Experiment, error)

	// FetchExperimentStatus retrieves only the status of an experiment by its unique ID.
	FetchExperimentStatus(ctx context.Context, xid int64) (*essmsgpb.ExperimentStatus, error)

	// FetchDriftedComponents retrieves up to limit components for an active experiment, ordered by
	// name, that are missing or deleted from the catalog or whose observed generation differs from
	// the experiment's snapshot. It returns an empty slice for consistent, inactive, or snapshot-free
	// experiments. A limit <= 0 returns all drifted components.
	FetchDriftedComponents(ctx context.Context, xid int64, limit int) ([]ComponentDiff, error)

	// ListExperiments retrieves a list of experiments matching the given filter. Staleness should
	// be set to 0 for strong reads, and up to 15 seconds for stale reads.
	// It uses cursor pagination.
	ListExperiments(ctx context.Context, pageSize int32, pageToken, filter, orderBy string, staleness time.Duration) ([]*essmsgpb.Experiment, string, error)

	// UpdateExperiment updates an experiment with the given update mask. It returns both the
	// original and updated experiments, so that the caller can choose whether to perform
	// additional actions based on the original experiment state.
	UpdateExperiment(ctx context.Context, update *ExperimentUpdate) (UpdatedExperiments, error)
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

	// FetchActiveWorkUnitsByQueues retrieves active work units associated with the requested queue names.
	FetchActiveWorkUnitsByQueues(ctx context.Context, queueNames []string, pageSize int32, pageToken string) (*ActiveWorkUnitsResult, error)
}

// UpdatedWorkUnits contains the original and updated work unit protos.
type UpdatedWorkUnits struct {
	Original *essmsgpb.WorkUnit
	Updated  *essmsgpb.WorkUnit
}

// -----------------------
// Resource Observability Types
// -----------------------

var (
	// ErrInvalidPageToken is returned when the page token cannot be decoded or is invalid.
	ErrInvalidPageToken = errors.New("invalid page_token")
)

// WorkUnitWithAuthor wraps a WorkUnit proto with author metadata.
type WorkUnitWithAuthor struct {
	WorkUnit *essmsgpb.WorkUnit
	Author   string
}

// ActiveWorkUnitsResult holds the result of querying active work units.
type ActiveWorkUnitsResult struct {
	WorkUnits      []WorkUnitWithAuthor
	NextPageToken  string
	TotalQueueSize int64
}

// QueueMetricRow represents a single raw row returned by the velocity or pending queue metrics query.
type QueueMetricRow struct {
	QueueName    string
	Count        int64
	TotalUnits   int64
	ResourceType string
}

// StatusHistoryStore defines storage operations for work unit status history and queue metrics.
type StatusHistoryStore interface {
	FetchBatchQueueVelocity(ctx context.Context, queueNames []string, window time.Duration) ([]QueueMetricRow, error)
	FetchBatchPendingQueueMetrics(ctx context.Context, queueNames []string) ([]QueueMetricRow, error)
}

// -----------------------
// Dashboard Methods
// -----------------------

// DashboardStore defines the persistence contract for dashboards and charts.
type DashboardStore interface {
	CreateDashboard(ctx context.Context, dashboard *dpb.Dashboard, charts []*dpb.Chart) error
	GetDashboard(ctx context.Context, dashboardID string) (*dpb.Dashboard, error)
	ListDashboards(ctx context.Context, pageSize int32, pageToken, filter, orderBy string, staleness time.Duration) ([]*dpb.Dashboard, string, error)
	UpdateDashboard(ctx context.Context, dashboard *dpb.Dashboard, updateMask *fmpb.FieldMask) (*dpb.Dashboard, error)
	DeleteDashboard(ctx context.Context, dashboardID string) error
	CreateChart(ctx context.Context, chart *dpb.Chart) error
	GetChart(ctx context.Context, chartID string) (*dpb.Chart, error)
	ListCharts(ctx context.Context, parent string, pageSize int32, pageToken, filter, orderBy string, staleness time.Duration) ([]*dpb.Chart, string, error)
	UpdateChart(ctx context.Context, chart *dpb.Chart, updateMask *fmpb.FieldMask) (*dpb.Chart, error)
	DeleteChart(ctx context.Context, chartID string) error
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
	// ListPending retrieves pending paused tasks ordered by Priority DESC, CreateTime ASC,
	// with each returned task's WorkUnit populated with the current state of its parent WorkUnit.
	// A limit <= 0 returns all pending tasks.
	ListPending(ctx context.Context, limit int) ([]*PausedTask, error)
}

// PausedTaskTxRepository defines paused task operations scoped to a read-write transaction.
type PausedTaskTxRepository interface {
	// Create persists a new paused task. TaskID, Action, and a non-zero CreateTime must be provided.
	// Duplicate (XID, WID, TaskID) keys fail the transaction (in-line or at commit) with AlreadyExists.
	Create(ctx context.Context, task *PausedTask) error
	// MarkProcessed records resolution details for a batch of paused tasks. Each resolution must have a non-zero ProcessedTime.
	// Calling MarkProcessed on an already-processed task overwrites its resolution fields (last-write-wins).
	// Missing tasks fail the transaction (in-line or at commit) with NotFound, applying nothing.
	MarkProcessed(ctx context.Context, resolutions []PausedTaskResolution) error
}

// PauseSchedulingStore exposes repository accessors for global pause scheduling state
// and pending paused actuation tasks.
type PauseSchedulingStore interface {
	PauseState() PauseStateRepository
	PausedTasks() PausedTaskRepository
}

// CLILoginCodeRepository defines persistence operations for one-time CLI login codes.
type CLILoginCodeRepository = oauth2.CLICodeStore

// CLILoginCodeStore exposes the repository accessor for one-time CLI login codes.
type CLILoginCodeStore interface {
	CLILoginCodes() CLILoginCodeRepository
}

// ComponentRepository defines read operations for component catalog persistence on the storage facade.
// A read that finds no matching component returns store.ErrComponentNotFound itself, not another NotFound error.
type ComponentRepository interface {
	// GetActiveComponent returns the non-deleted record of canonicalName with its active dependencies sorted by name.
	GetActiveComponent(ctx context.Context, canonicalName string) (*store.ComponentRecord, error)
	// GetComponentByID returns the non-deleted record of componentID with its active dependencies sorted by name.
	GetComponentByID(ctx context.Context, componentID int64) (*store.ComponentRecord, error)
	// ListActiveComponents returns every non-deleted record keyed by canonical name, each with its active
	// dependencies, read from one consistent snapshot.
	ListActiveComponents(ctx context.Context) (map[string]*store.ComponentRecord, error)
	// GetComponentWithFence returns the active record of canonicalName and the ownership fence holder (nil before
	// the first declaration) from one snapshot.
	GetComponentWithFence(ctx context.Context, canonicalName string) (*store.ComponentRecord, *store.ClusterIdentity, error)
	// ReadOwnershipGauges reads the fence and counts active owned components last seen at or before cutoff in one snapshot.
	ReadOwnershipGauges(ctx context.Context, cutoff time.Time) (store.OwnershipGauges, error)
}

// ComponentTxRepository defines transactional component catalog operations. A read-only binding supports only
// catalog and snapshot reads; in a read-write transaction, reads hold what they return (including an absent fence
// row, e.g. via a seeded singleton row locked with FOR UPDATE in PostgreSQL) until commit.
type ComponentTxRepository interface {
	// GetActiveComponent returns the non-deleted record of canonicalName with its active dependencies sorted by name.
	GetActiveComponent(ctx context.Context, canonicalName string) (*store.ComponentRecord, error)
	// GetComponentByID returns the non-deleted record of componentID with its active dependencies sorted by name.
	GetComponentByID(ctx context.Context, componentID int64) (*store.ComponentRecord, error)
	// ListActiveComponents returns every non-deleted record keyed by canonical name with its active dependencies.
	ListActiveComponents(ctx context.Context) (map[string]*store.ComponentRecord, error)
	// GetSnapshotByID returns the snapshot of componentID at generation stamped with createTime and updateTime
	// (Dependencies nil), or codes.NotFound when missing.
	GetSnapshotByID(ctx context.Context, componentID, generation int64, canonicalName string, createTime, updateTime time.Time) (*store.ComponentRecord, *componentspb.ResolvedComponentCoordinates, error)
	// GetSnapshotByName returns the snapshot at generation of the newest component named canonicalName (including
	// deleted ones), stamped with that component's timestamps, or codes.NotFound when missing.
	GetSnapshotByName(ctx context.Context, canonicalName string, generation int64) (*store.ComponentRecord, *componentspb.ResolvedComponentCoordinates, error)
	// ReadFence returns the ownership fence holder, or nil before the first declaration.
	ReadFence(ctx context.Context) (*store.ClusterIdentity, error)
	// RefreshFence updates LastSeenTime to the commit time and, when newName is non-nil, ActiveClusterName.
	RefreshFence(ctx context.Context, newName *string) error
	// ReplaceFence writes cluster as the fence holder with DeclaredTime and LastSeenTime at commit time.
	ReplaceFence(ctx context.Context, cluster store.ClusterIdentity) error
	// ReassignOwnedComponents moves every active owned component to clusterUID at epoch with no owner UID,
	// sets SourceLastSeenTime to the commit time, leaves UpdateTime unchanged, and returns the count.
	ReassignOwnedComponents(ctx context.Context, clusterUID string, epoch int64) (int64, error)
}

// ComponentStore exposes the repository accessor for component catalog persistence.
type ComponentStore interface {
	Components() ComponentRepository
}

// WorkUnitTxRepository defines work unit operations scoped to a read-write transaction.
type WorkUnitTxRepository interface {
	// FetchNextWorkUnitID retrieves the next available work unit ID for an experiment within the transaction.
	FetchNextWorkUnitID(ctx context.Context, xid int64) (int64, error)
	// InsertWorkUnit inserts a new work unit and its initial execution within the transaction.
	InsertWorkUnit(ctx context.Context, wu *essmsgpb.WorkUnit) error
	// FetchWorkUnit retrieves a work unit by its unique ID within the transaction.
	FetchWorkUnit(ctx context.Context, xid, wid int64) (*essmsgpb.WorkUnit, error)
	// FetchUpdatingWorkUnits retrieves a batch of work units for update by their unique IDs within the transaction.
	FetchUpdatingWorkUnits(ctx context.Context, xid int64, wids []int64) (map[int64]*essmsgpb.WorkUnit, error)
	// ListWorkUnitMessagesBySeverity retrieves work unit status messages matching a given severity level within the transaction.
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
	// InsertExperiment buffers a mutation to insert a new experiment within the transaction.
	InsertExperiment(ctx context.Context, exp *essmsgpb.Experiment) error
	// UpdateExperimentStatus updates the experiment status within the transaction.
	UpdateExperimentStatus(ctx context.Context, exp *essmsgpb.Experiment) error
	// UpdateExperimentResources updates the experiment resources within the transaction.
	UpdateExperimentResources(ctx context.Context, exp *essmsgpb.Experiment) error
}

// XidTxRepository defines XID entry operations scoped to a read-write transaction.
type XidTxRepository interface {
	// FetchXidEntry retrieves an XidEntry by its unique ID within the transaction.
	FetchXidEntry(ctx context.Context, xid int64) (*xidmsgpb.XidEntry, error)
	// UpdateXidEntry persists an updated XidEntry within the transaction.
	UpdateXidEntry(ctx context.Context, xid int64, entry *xidmsgpb.XidEntry) error
}

// StatusMessageTxRepository defines status message operations scoped to a read-write transaction.
type StatusMessageTxRepository interface {
	// SaveStatusMessageHistory records a status message history entry within the transaction.
	SaveStatusMessageHistory(ctx context.Context, eid *int64, oldMsg, newMsg *essmsgpb.StructuredMessage) error
}

// StatusHistoryTxRepository defines status history operations scoped to a read-write transaction.
type StatusHistoryTxRepository interface {
	// SaveWorkUnitStatusHistory records work unit status history entries within the transaction.
	SaveWorkUnitStatusHistory(ctx context.Context, xid, wid int64, eid *int64, queueName string, prevState, newState *essmsgpb.WorkUnitStatus_WorkUnitState, resources map[string]int64, timestamp time.Time) error
}

// Transaction holds the domain repositories scoped to a single read-write transaction.
type Transaction struct {
	WorkUnits      WorkUnitTxRepository
	Experiments    ExperimentTxRepository
	XIDs           XidTxRepository
	StatusMessages StatusMessageTxRepository
	StatusHistory  StatusHistoryTxRepository
	PauseState     PauseStateTxRepository
	PausedTasks    PausedTaskTxRepository
	Components     ComponentTxRepository
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
	StatusHistoryStore
	DashboardStore
	ArtifactStore
	OperationStore
	XidEntryStore
	PauseSchedulingStore
	CLILoginCodeStore
	ComponentStore
}
