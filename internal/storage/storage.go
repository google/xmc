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

// Storage defines the interface for all data persistence operations.
type Storage interface {
	// -----------------------
	// Experiment Methods
	// -----------------------

	// CreateExperiment creates a new experiment.
	CreateExperiment(ctx context.Context, exp *essmsgpb.Experiment) (*essmsgpb.Experiment, error)

	// FetchExperiment retrieves an experiment by its unique ID.
	FetchExperiment(ctx context.Context, xid int64) (*essmsgpb.Experiment, error)

	// FetchExperimentStatus retrieves only the status of an experiment by its unique ID.
	FetchExperimentStatus(ctx context.Context, xid int64) (*essmsgpb.ExperimentStatus, error)

	// ListExperiments retrieves a list of experiments matching the given filter. Staleness should
	// be set to 0 for strong reads, and up to 15 seconds for stale reads.
	// If pageToken is empty, defaultToCursorPagination determines the pagination type. Otherwise,
	// the pagination type is determined by the format of the non-empty pageToken.
	ListExperiments(ctx context.Context, pageSize int32, pageToken, filter, orderBy string, staleness time.Duration, defaultToCursorPagination bool) ([]*essmsgpb.Experiment, string, error)

	// UpdateExperiment updates an experiment with the given update mask. It returns both the
	// original and updated experiments, so that the caller can choose whether to perform
	// additional actions based on the original experiment state.
	UpdateExperiment(ctx context.Context, update *ExperimentUpdate) (*essmsgpb.Experiment, *essmsgpb.Experiment, error)

	// UpdateExperimentLaunchState updates the launch state of an experiment.
	UpdateExperimentLaunchState(ctx context.Context, xid int64, launchState essmsgpb.ExperimentStatus_LaunchState) (*essmsgpb.Experiment, error)

	// -----------------------
	// WorkUnit Methods
	// -----------------------

	// CreateWorkUnit creates a new work unit as a sub-collection of an experiment.
	CreateWorkUnit(ctx context.Context, wu *essmsgpb.WorkUnit) (*essmsgpb.WorkUnit, error)

	// FetchWorkUnit retrieves a work unit by its unique ID.
	FetchWorkUnit(ctx context.Context, xid int64, wid int64) (*essmsgpb.WorkUnit, error)

	// ListWorkUnits retrieves a list of work units matching the given filter. Staleness should
	// be set to 0 for strong reads, and up to 15 seconds for stale reads.
	// If pageToken is empty, defaultToCursorPagination determines the pagination type. Otherwise,
	// the pagination type is determined by the format of the non-empty pageToken.
	ListWorkUnits(ctx context.Context, parent string, pageSize int32, pageToken, filter, orderBy string, staleness time.Duration, defaultToCursorPagination bool) ([]*essmsgpb.WorkUnit, string, error)

	// UpdateWorkUnit updates a work unit with the given update mask. It returns both the
	// original and updated work units, so that the caller can choose whether to perform
	// additional actions based on the original work unit state.
	UpdateWorkUnit(ctx context.Context, update *WorkUnitUpdate) (*essmsgpb.WorkUnit, *essmsgpb.WorkUnit, error)

	// -----------------------
	// WorkUnitExecution Methods
	// -----------------------

	// FetchCurrentWorkUnitExecution retrieves the current work unit execution.
	FetchCurrentWorkUnitExecution(ctx context.Context, xid int64, wid int64) (*essmsgpb.WorkUnitExecution, error)

	// ListWorkUnitExecutions retrieves the historical work unit executions for a given work unit.
	ListWorkUnitExecutions(ctx context.Context, xid int64, wid int64) ([]*essmsgpb.WorkUnitExecution, error)

	// UpdateWorkUnitExecutions updates the status of a work unit. It returns the canonical
	// executions, either the updated executions or the original executions if no update was
	// done.
	UpdateWorkUnitExecutions(ctx context.Context, xid int64, updates []*WorkUnitExecutionUpdate) (map[int64]*essmsgpb.WorkUnitExecution, error)

	// RestartWorkUnitExecutions archives the active work unit execution for a given work unit,
	// and generates a new current work unit execution in the given state.
	RestartWorkUnitExecutions(ctx context.Context, requester string, xid int64, wids []int64) (map[int64]*essmsgpb.WorkUnitExecution, error)

	// -----------------------
	// StatusMessage Methods
	// -----------------------

	// ListStatusMessages retrieves a list of status messages for an experiment or work unit. Staleness should
	// be set to 0 for strong reads, and up to 15 seconds for stale reads.
	// If pageToken is empty, defaultToCursorPagination determines the pagination type. Otherwise,
	// the pagination type is determined by the format of the non-empty pageToken.
	ListStatusMessages(ctx context.Context, parent string, pageSize int32, pageToken, filter, orderBy string, staleness time.Duration) ([]*essmsgpb.StatusMessage, string, error)

	// -----------------------
	// Artifact Methods
	// -----------------------

	// CreateArtifact creates a new artifact.
	CreateArtifact(ctx context.Context, artifact *essmsgpb.Artifact) (*essmsgpb.Artifact, error)

	// FetchArtifact retrieves an artifact by its unique ID.
	FetchArtifact(ctx context.Context, aid int64) (*essmsgpb.Artifact, error)

	// ListArtifacts retrieves a list of artifacts matching the given filter. Staleness should be set
	// to 0 for strong reads, and up to 15 seconds for stale reads.
	// If pageToken is empty, defaultToCursorPagination determines the pagination type. Otherwise,
	// the pagination type is determined by the format of the non-empty pageToken.
	ListArtifacts(ctx context.Context, parent string, pageSize int32, pageToken, filter, orderBy string, staleness time.Duration, defaultToCursorPagination bool) ([]*essmsgpb.Artifact, string, error)

	// UpdateArtifact updates an artifact with the given update mask.
	UpdateArtifact(ctx context.Context, update *ArtifactUpdate) (originalArtifact *essmsgpb.Artifact, updatedArtifact *essmsgpb.Artifact, err error)

	// DeleteArtifact deletes an artifact by its unique ID.
	DeleteArtifact(ctx context.Context, aid int64) error

	// -----------------------
	// Operation Methods
	// -----------------------

	// AddWorkUnitOperation adds an operation to a work unit.
	AddWorkUnitOperation(ctx context.Context, xid int64, wid int64, operationMetadata *essmsgpb.OperationMetadata) (*longrunningpb.Operation, error)

	// FetchWorkUnitOperation retrieves an operation by its unique ID.
	FetchWorkUnitOperation(ctx context.Context, xid int64, wid int64, operationID string) (*longrunningpb.Operation, error)

	// UpdateWorkUnitOperation updates the status of an operation.
	UpdateWorkUnitOperation(ctx context.Context, operationUpdate *OperationUpdate) (*longrunningpb.Operation, error)

	// -----------------------
	// XidEntry Methods
	// -----------------------

	// CreateXidEntry creates a new XidEntry.
	CreateXidEntry(ctx context.Context, entry *xidmsgpb.XidEntry) (*xidmsgpb.XidEntry, error)

	// FetchXidEntry retrieves an XidEntry by its unique ID.
	FetchXidEntry(ctx context.Context, xid int64) (*xidmsgpb.XidEntry, error)

	// ListXidEntries retrieves a paginated and filtered list of XidEntries. Staleness should
	// be set to 0 for strong reads, and up to 15 seconds for stale reads.
	// If pageToken is empty, defaultToCursorPagination determines the pagination type. Otherwise,
	// the pagination type is determined by the format of the non-empty pageToken.
	ListXidEntries(ctx context.Context, pageSize int32, pageToken, filter, orderBy string, staleness time.Duration, defaultToCursorPagination bool) ([]*xidmsgpb.XidEntry, string, error)

	// UpdateXidEntry updates an XidEntry with the given update mask.
	UpdateXidEntry(ctx context.Context, update *XidEntryUpdate) (*xidmsgpb.XidEntry, error)
}
