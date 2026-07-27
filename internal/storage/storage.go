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

// ExperimentStore defines storage operations for experiments.
type ExperimentStore interface {
	CreateExperiment(ctx context.Context, exp *essmsgpb.Experiment) (*essmsgpb.Experiment, error)
	FetchExperiment(ctx context.Context, xid int64) (*essmsgpb.Experiment, error)
	FetchExperimentStatus(ctx context.Context, xid int64) (*essmsgpb.ExperimentStatus, error)
	ListExperiments(ctx context.Context, pageSize int32, pageToken, filter, orderBy string, staleness time.Duration) ([]*essmsgpb.Experiment, string, error)
	UpdateExperiment(ctx context.Context, update *ExperimentUpdate) (*essmsgpb.Experiment, *essmsgpb.Experiment, error)
	UpdateExperimentLaunchState(ctx context.Context, xid int64, launchState essmsgpb.ExperimentStatus_LaunchState) (*essmsgpb.Experiment, error)
}

// WorkUnitStore defines storage operations for work units and work unit executions.
type WorkUnitStore interface {
	CreateWorkUnit(ctx context.Context, wu *essmsgpb.WorkUnit) (*essmsgpb.WorkUnit, error)
	FetchWorkUnit(ctx context.Context, xid int64, wid int64) (*essmsgpb.WorkUnit, error)
	ListWorkUnits(ctx context.Context, parent string, pageSize int32, pageToken, filter, orderBy string, staleness time.Duration) ([]*essmsgpb.WorkUnit, string, error)
	UpdateWorkUnit(ctx context.Context, update *WorkUnitUpdate) (*essmsgpb.WorkUnit, *essmsgpb.WorkUnit, error)
	FetchCurrentWorkUnitExecution(ctx context.Context, xid int64, wid int64) (*essmsgpb.WorkUnitExecution, error)
	ListWorkUnitExecutions(ctx context.Context, xid int64, wid int64) ([]*essmsgpb.WorkUnitExecution, error)
	UpdateWorkUnitExecutions(ctx context.Context, xid int64, updates []*WorkUnitExecutionUpdate) (map[int64]*essmsgpb.WorkUnitExecution, error)
	RestartWorkUnitExecutions(ctx context.Context, requester string, xid int64, wids []int64) (map[int64]*essmsgpb.WorkUnitExecution, error)
}

// StatusMessageStore defines storage operations for status messages.
type StatusMessageStore interface {
	ListStatusMessages(ctx context.Context, parent string, pageSize int32, pageToken, filter, orderBy string, staleness time.Duration) ([]*essmsgpb.StatusMessage, string, error)
}

// ArtifactStore defines storage operations for artifacts.
type ArtifactStore interface {
	CreateArtifact(ctx context.Context, artifact *essmsgpb.Artifact) (*essmsgpb.Artifact, error)
	FetchArtifact(ctx context.Context, aid int64) (*essmsgpb.Artifact, error)
	ListArtifacts(ctx context.Context, parent string, pageSize int32, pageToken, filter, orderBy string, staleness time.Duration) ([]*essmsgpb.Artifact, string, error)
	UpdateArtifact(ctx context.Context, update *ArtifactUpdate) (originalArtifact *essmsgpb.Artifact, updatedArtifact *essmsgpb.Artifact, err error)
	DeleteArtifact(ctx context.Context, aid int64) error
}

// OperationStore defines storage operations for operations.
type OperationStore interface {
	AddWorkUnitOperation(ctx context.Context, xid int64, wid int64, operationMetadata *essmsgpb.OperationMetadata) (*longrunningpb.Operation, error)
	FetchWorkUnitOperation(ctx context.Context, xid int64, wid int64, operationID string) (*longrunningpb.Operation, error)
	UpdateWorkUnitOperation(ctx context.Context, operationUpdate *OperationUpdate) (*longrunningpb.Operation, error)
}

// XidEntryStore defines storage operations for XID entries.
type XidEntryStore interface {
	CreateXidEntry(ctx context.Context, entry *xidmsgpb.XidEntry) (*xidmsgpb.XidEntry, error)
	FetchXidEntry(ctx context.Context, xid int64) (*xidmsgpb.XidEntry, error)
	ListXidEntries(ctx context.Context, pageSize int32, pageToken, filter, orderBy string, staleness time.Duration) ([]*xidmsgpb.XidEntry, string, error)
	UpdateXidEntry(ctx context.Context, update *XidEntryUpdate) (*xidmsgpb.XidEntry, error)
}

// Storage defines the unified interface for all data persistence operations.
type Storage interface {
	ExperimentStore
	WorkUnitStore
	StatusMessageStore
	ArtifactStore
	OperationStore
	XidEntryStore
}
