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

// Package testing provides thread-safe in-memory test doubles for the storage layer.
package testing

import (
	"context"
	"fmt"
	"sync"
	"time"

	"golang/protobuf/v2/proto/proto"
	longrunningpb "longrunning/operations_go_proto"
	"task/go/status"
	essmsgpb "xmanager_cloud/experiment_state_server/proto/messages_go_proto"
	"xmanager_cloud/internal/storage"
	"xmanager_cloud/validator/validator"
	xidmsgpb "xmanager_cloud/xid_service/proto/messages_go_proto"
)

var _ storage.Storage = (*FakeStorage)(nil)

// FakeStorage provides a thread-safe, in-memory implementation of storage.Storage for unit tests.
type FakeStorage struct {
	mu sync.RWMutex

	experiments    map[int64]*essmsgpb.Experiment
	workUnits      map[string]*essmsgpb.WorkUnit            // Key: "experiments/{xid}/workUnits/{wid}"
	executions     map[string]*essmsgpb.WorkUnitExecution   // Key: "experiments/{xid}/workUnits/{wid}/executions/{eid}"
	artifacts      map[int64]*essmsgpb.Artifact             // Key: AID
	xidEntries     map[int64]*xidmsgpb.XidEntry             // Key: XID
	operations     map[string]*longrunningpb.Operation      // Key: "experiments/{xid}/workUnits/{wid}/operations/{oid}"
	statusMessages map[string][]*essmsgpb.StructuredMessage // Key: parent name

	nextWID map[int64]int64
	nextAID int64
	nextXID int64
}

// NewFakeStorage creates and initializes a new FakeStorage instance.
func NewFakeStorage() *FakeStorage {
	return &FakeStorage{
		experiments:    make(map[int64]*essmsgpb.Experiment),
		workUnits:      make(map[string]*essmsgpb.WorkUnit),
		executions:     make(map[string]*essmsgpb.WorkUnitExecution),
		artifacts:      make(map[int64]*essmsgpb.Artifact),
		xidEntries:     make(map[int64]*xidmsgpb.XidEntry),
		operations:     make(map[string]*longrunningpb.Operation),
		statusMessages: make(map[string][]*essmsgpb.StructuredMessage),
		nextWID:        make(map[int64]int64),
		nextAID:        1,
		nextXID:        1000,
	}
}

// -----------------------
// Experiment Methods
// -----------------------

func (f *FakeStorage) CreateExperiment(ctx context.Context, exp *essmsgpb.Experiment) (*essmsgpb.Experiment, error) {
	if exp == nil {
		return nil, fmt.Errorf("experiment must not be nil")
	}
	xid, err := validator.ValidateExperimentName(exp.GetName())
	if err != nil {
		return nil, fmt.Errorf("invalid experiment name: %w", err)
	}

	f.mu.Lock()
	defer f.mu.Unlock()

	if _, exists := f.experiments[xid]; exists {
		return nil, fmt.Errorf("experiment %d already exists: %w", xid, status.ErrAlreadyExists)
	}

	cloned := proto.Clone(exp).(*essmsgpb.Experiment)
	f.experiments[xid] = cloned
	return proto.Clone(cloned).(*essmsgpb.Experiment), nil
}

func (f *FakeStorage) FetchExperiment(ctx context.Context, xid int64) (*essmsgpb.Experiment, error) {
	f.mu.RLock()
	defer f.mu.RUnlock()

	exp, exists := f.experiments[xid]
	if !exists {
		return nil, fmt.Errorf("experiment %d not found: %w", xid, status.ErrNotFound)
	}
	return proto.Clone(exp).(*essmsgpb.Experiment), nil
}

func (f *FakeStorage) FetchExperimentStatus(ctx context.Context, xid int64) (*essmsgpb.ExperimentStatus, error) {
	exp, err := f.FetchExperiment(ctx, xid)
	if err != nil {
		return nil, err
	}
	if exp.GetStatus() == nil {
		return nil, fmt.Errorf("experiment %d status is nil", xid)
	}
	return proto.Clone(exp.GetStatus()).(*essmsgpb.ExperimentStatus), nil
}

func (f *FakeStorage) ListExperiments(ctx context.Context, pageSize int32, pageToken, filter, orderBy string, staleness time.Duration) ([]*essmsgpb.Experiment, string, error) {
	f.mu.RLock()
	defer f.mu.RUnlock()

	var result []*essmsgpb.Experiment
	for _, exp := range f.experiments {
		result = append(result, proto.Clone(exp).(*essmsgpb.Experiment))
	}
	return result, "", nil
}

func (f *FakeStorage) UpdateExperiment(ctx context.Context, update *storage.ExperimentUpdate) (*essmsgpb.Experiment, *essmsgpb.Experiment, error) {
	if update == nil || update.Experiment == nil {
		return nil, nil, fmt.Errorf("update and update.Experiment must not be nil")
	}

	f.mu.Lock()
	defer f.mu.Unlock()

	original, exists := f.experiments[update.XID]
	if !exists {
		return nil, nil, fmt.Errorf("experiment %d not found: %w", update.XID, status.ErrNotFound)
	}

	originalClone := proto.Clone(original).(*essmsgpb.Experiment)
	updatedClone := proto.Clone(update.Experiment).(*essmsgpb.Experiment)
	f.experiments[update.XID] = updatedClone

	return originalClone, proto.Clone(updatedClone).(*essmsgpb.Experiment), nil
}

func (f *FakeStorage) UpdateExperimentLaunchState(ctx context.Context, xid int64, launchState essmsgpb.ExperimentStatus_LaunchState) (*essmsgpb.Experiment, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	exp, exists := f.experiments[xid]
	if !exists {
		return nil, fmt.Errorf("experiment %d not found: %w", xid, status.ErrNotFound)
	}

	if exp.Status == nil {
		exp.Status = &essmsgpb.ExperimentStatus{}
	}
	exp.Status.LaunchState = launchState
	return proto.Clone(exp).(*essmsgpb.Experiment), nil
}

// -----------------------
// WorkUnit Methods
// -----------------------

func (f *FakeStorage) CreateWorkUnit(ctx context.Context, wu *essmsgpb.WorkUnit) (*essmsgpb.WorkUnit, error) {
	if wu == nil {
		return nil, fmt.Errorf("work unit must not be nil")
	}
	xid, wid, err := validator.ValidateWorkUnitNameAllowZeroWID(wu.GetName())
	if err != nil {
		return nil, fmt.Errorf("invalid work unit name: %w", err)
	}

	f.mu.Lock()
	defer f.mu.Unlock()

	if wid == 0 {
		f.nextWID[xid]++
		wid = f.nextWID[xid]
		wu.Name = fmt.Sprintf("experiments/%d/workUnits/%d", xid, wid)
		if wu.CurrentWorkUnitExecution != nil {
			wu.CurrentWorkUnitExecution.Name = fmt.Sprintf("experiments/%d/workUnits/%d/executions/1", xid, wid)
			if wu.CurrentWorkUnitExecution.Status != nil && wu.CurrentWorkUnitExecution.Status.SystemMessage != nil {
				wu.CurrentWorkUnitExecution.Status.SystemMessage.Parent = wu.Name
			}
		}
	}

	cloned := proto.Clone(wu).(*essmsgpb.WorkUnit)
	f.workUnits[wu.GetName()] = cloned

	if wu.CurrentWorkUnitExecution != nil {
		execCloned := proto.Clone(wu.CurrentWorkUnitExecution).(*essmsgpb.WorkUnitExecution)
		f.executions[execCloned.GetName()] = execCloned
	}

	return proto.Clone(cloned).(*essmsgpb.WorkUnit), nil
}

func (f *FakeStorage) FetchWorkUnit(ctx context.Context, xid int64, wid int64) (*essmsgpb.WorkUnit, error) {
	f.mu.RLock()
	defer f.mu.RUnlock()

	key := fmt.Sprintf("experiments/%d/workUnits/%d", xid, wid)
	wu, exists := f.workUnits[key]
	if !exists {
		return nil, fmt.Errorf("work unit %d for experiment %d not found: %w", wid, xid, status.ErrNotFound)
	}
	return proto.Clone(wu).(*essmsgpb.WorkUnit), nil
}

func (f *FakeStorage) ListWorkUnits(ctx context.Context, parent string, pageSize int32, pageToken, filter, orderBy string, staleness time.Duration) ([]*essmsgpb.WorkUnit, string, error) {
	f.mu.RLock()
	defer f.mu.RUnlock()

	var result []*essmsgpb.WorkUnit
	for _, wu := range f.workUnits {
		result = append(result, proto.Clone(wu).(*essmsgpb.WorkUnit))
	}
	return result, "", nil
}

func (f *FakeStorage) UpdateWorkUnit(ctx context.Context, update *storage.WorkUnitUpdate) (*essmsgpb.WorkUnit, *essmsgpb.WorkUnit, error) {
	if update == nil || update.WorkUnit == nil {
		return nil, nil, fmt.Errorf("update and update.WorkUnit must not be nil")
	}

	f.mu.Lock()
	defer f.mu.Unlock()

	key := fmt.Sprintf("experiments/%d/workUnits/%d", update.XID, update.WID)
	original, exists := f.workUnits[key]
	if !exists {
		return nil, nil, fmt.Errorf("work unit %d for experiment %d not found: %w", update.WID, update.XID, status.ErrNotFound)
	}

	originalClone := proto.Clone(original).(*essmsgpb.WorkUnit)
	updatedClone := proto.Clone(update.WorkUnit).(*essmsgpb.WorkUnit)
	f.workUnits[key] = updatedClone

	return originalClone, proto.Clone(updatedClone).(*essmsgpb.WorkUnit), nil
}

func (f *FakeStorage) FetchCurrentWorkUnitExecution(ctx context.Context, xid int64, wid int64) (*essmsgpb.WorkUnitExecution, error) {
	wu, err := f.FetchWorkUnit(ctx, xid, wid)
	if err != nil {
		return nil, err
	}
	if wu.GetCurrentWorkUnitExecution() == nil {
		return nil, fmt.Errorf("current work unit execution is nil for work unit %d", wid)
	}
	return proto.Clone(wu.GetCurrentWorkUnitExecution()).(*essmsgpb.WorkUnitExecution), nil
}

func (f *FakeStorage) ListWorkUnitExecutions(ctx context.Context, xid int64, wid int64) ([]*essmsgpb.WorkUnitExecution, error) {
	f.mu.RLock()
	defer f.mu.RUnlock()

	prefix := fmt.Sprintf("experiments/%d/workUnits/%d/executions/", xid, wid)
	var result []*essmsgpb.WorkUnitExecution
	for name, exec := range f.executions {
		if len(name) >= len(prefix) && name[:len(prefix)] == prefix {
			result = append(result, proto.Clone(exec).(*essmsgpb.WorkUnitExecution))
		}
	}
	return result, nil
}

func (f *FakeStorage) UpdateWorkUnitExecutions(ctx context.Context, xid int64, updates []*storage.WorkUnitExecutionUpdate) (map[int64]*essmsgpb.WorkUnitExecution, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	result := make(map[int64]*essmsgpb.WorkUnitExecution)
	for _, u := range updates {
		key := fmt.Sprintf("experiments/%d/workUnits/%d", xid, u.WID)
		wu, exists := f.workUnits[key]
		if !exists {
			return nil, fmt.Errorf("work unit %d for experiment %d not found: %w", u.WID, xid, status.ErrNotFound)
		}

		execClone := proto.Clone(u.WorkUnitExecution).(*essmsgpb.WorkUnitExecution)
		wu.CurrentWorkUnitExecution = execClone
		f.executions[execClone.GetName()] = execClone
		result[u.WID] = proto.Clone(execClone).(*essmsgpb.WorkUnitExecution)
	}
	return result, nil
}

func (f *FakeStorage) RestartWorkUnitExecutions(ctx context.Context, requester string, xid int64, wids []int64) (map[int64]*essmsgpb.WorkUnitExecution, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	result := make(map[int64]*essmsgpb.WorkUnitExecution)
	for _, wid := range wids {
		key := fmt.Sprintf("experiments/%d/workUnits/%d", xid, wid)
		wu, exists := f.workUnits[key]
		if !exists {
			return nil, fmt.Errorf("work unit %d for experiment %d not found: %w", wid, xid, status.ErrNotFound)
		}

		currentExec := wu.GetCurrentWorkUnitExecution()
		if currentExec != nil {
			currentExec.State = essmsgpb.WorkUnitExecution_INACTIVE
		}

		newEID := int64(2)
		if currentExec != nil {
			_, _, eid, err := validator.ValidateWorkUnitExecutionName(currentExec.GetName())
			if err == nil && eid > 0 {
				newEID = eid + 1
			}
		}

		newExec := &essmsgpb.WorkUnitExecution{
			Name:  fmt.Sprintf("experiments/%d/workUnits/%d/executions/%d", xid, wid, newEID),
			State: essmsgpb.WorkUnitExecution_ACTIVE,
			Status: &essmsgpb.WorkUnitStatus{
				State: essmsgpb.WorkUnitStatus_RESTARTING,
				SystemMessage: &essmsgpb.StructuredMessage{
					UserFacingText: fmt.Sprintf("Work unit is being restarted by %s", requester),
					Severity:       essmsgpb.StructuredMessage_INFO,
					Source:         essmsgpb.StructuredMessage_XMANAGER_BACKEND,
					Parent:         key,
				},
			},
		}
		wu.CurrentWorkUnitExecution = newExec
		f.executions[newExec.GetName()] = newExec
		result[wid] = proto.Clone(newExec).(*essmsgpb.WorkUnitExecution)
	}
	return result, nil
}

// -----------------------
// StatusMessage Methods
// -----------------------

func (f *FakeStorage) ListStatusMessages(ctx context.Context, parent string, pageSize int32, pageToken, filter, orderBy string, staleness time.Duration) ([]*essmsgpb.StatusMessage, string, error) {
	f.mu.RLock()
	defer f.mu.RUnlock()

	msgs := f.statusMessages[parent]
	var result []*essmsgpb.StatusMessage
	for _, m := range msgs {
		result = append(result, &essmsgpb.StatusMessage{
			Content: proto.Clone(m).(*essmsgpb.StructuredMessage),
		})
	}
	return result, "", nil
}

// SaveStatusMessage is a helper for fake storage test assertions.
func (f *FakeStorage) SaveStatusMessage(parent string, msg *essmsgpb.StructuredMessage) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.statusMessages[parent] = append(f.statusMessages[parent], proto.Clone(msg).(*essmsgpb.StructuredMessage))
}

// -----------------------
// Artifact Methods
// -----------------------

func (f *FakeStorage) CreateArtifact(ctx context.Context, artifact *essmsgpb.Artifact) (*essmsgpb.Artifact, error) {
	if artifact == nil {
		return nil, fmt.Errorf("artifact must not be nil")
	}

	f.mu.Lock()
	defer f.mu.Unlock()

	aid := f.nextAID
	f.nextAID++
	if artifact.GetName() == "" {
		artifact.Name = fmt.Sprintf("artifacts/%d", aid)
	}

	cloned := proto.Clone(artifact).(*essmsgpb.Artifact)
	f.artifacts[aid] = cloned
	return proto.Clone(cloned).(*essmsgpb.Artifact), nil
}

func (f *FakeStorage) FetchArtifact(ctx context.Context, aid int64) (*essmsgpb.Artifact, error) {
	f.mu.RLock()
	defer f.mu.RUnlock()

	art, exists := f.artifacts[aid]
	if !exists {
		return nil, fmt.Errorf("artifact %d not found: %w", aid, status.ErrNotFound)
	}
	return proto.Clone(art).(*essmsgpb.Artifact), nil
}

func (f *FakeStorage) ListArtifacts(ctx context.Context, parent string, pageSize int32, pageToken, filter, orderBy string, staleness time.Duration) ([]*essmsgpb.Artifact, string, error) {
	f.mu.RLock()
	defer f.mu.RUnlock()

	var result []*essmsgpb.Artifact
	for _, art := range f.artifacts {
		if parent == "" || art.GetParent() == parent {
			result = append(result, proto.Clone(art).(*essmsgpb.Artifact))
		}
	}
	return result, "", nil
}

func (f *FakeStorage) UpdateArtifact(ctx context.Context, update *storage.ArtifactUpdate) (*essmsgpb.Artifact, *essmsgpb.Artifact, error) {
	if update == nil || update.Artifact == nil {
		return nil, nil, fmt.Errorf("update and update.Artifact must not be nil")
	}

	f.mu.Lock()
	defer f.mu.Unlock()

	original, exists := f.artifacts[update.AID]
	if !exists {
		return nil, nil, fmt.Errorf("artifact %d not found: %w", update.AID, status.ErrNotFound)
	}

	originalClone := proto.Clone(original).(*essmsgpb.Artifact)
	updatedClone := proto.Clone(update.Artifact).(*essmsgpb.Artifact)
	f.artifacts[update.AID] = updatedClone

	return originalClone, proto.Clone(updatedClone).(*essmsgpb.Artifact), nil
}

func (f *FakeStorage) DeleteArtifact(ctx context.Context, aid int64) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	if _, exists := f.artifacts[aid]; !exists {
		return fmt.Errorf("artifact %d not found: %w", aid, status.ErrNotFound)
	}
	delete(f.artifacts, aid)
	return nil
}

// -----------------------
// Operation Methods
// -----------------------

func (f *FakeStorage) AddWorkUnitOperation(ctx context.Context, xid int64, wid int64, operationMetadata *essmsgpb.OperationMetadata) (*longrunningpb.Operation, error) {
	if operationMetadata == nil {
		return nil, fmt.Errorf("operationMetadata must not be nil")
	}

	f.mu.Lock()
	defer f.mu.Unlock()

	oid := fmt.Sprintf("op-%d", time.Now().UnixNano())
	opName := fmt.Sprintf("experiments/%d/workUnits/%d/operations/%s", xid, wid, oid)

	op := &longrunningpb.Operation{
		Name: opName,
		Done: false,
	}

	f.operations[opName] = op
	return proto.Clone(op).(*longrunningpb.Operation), nil
}

func (f *FakeStorage) FetchWorkUnitOperation(ctx context.Context, xid int64, wid int64, operationID string) (*longrunningpb.Operation, error) {
	f.mu.RLock()
	defer f.mu.RUnlock()

	opName := fmt.Sprintf("experiments/%d/workUnits/%d/operations/%s", xid, wid, operationID)
	op, exists := f.operations[opName]
	if !exists {
		return nil, fmt.Errorf("operation %s not found: %w", operationID, status.ErrNotFound)
	}
	return proto.Clone(op).(*longrunningpb.Operation), nil
}

func (f *FakeStorage) UpdateWorkUnitOperation(ctx context.Context, update *storage.OperationUpdate) (*longrunningpb.Operation, error) {
	if update == nil || update.Operation == nil {
		return nil, fmt.Errorf("update must not be nil")
	}

	f.mu.Lock()
	defer f.mu.Unlock()

	opName := fmt.Sprintf("experiments/%d/workUnits/%d/operations/%s", update.XID, update.WID, update.OID)
	f.operations[opName] = proto.Clone(update.Operation).(*longrunningpb.Operation)
	return proto.Clone(update.Operation).(*longrunningpb.Operation), nil
}

// -----------------------
// XidEntry Methods
// -----------------------

func (f *FakeStorage) CreateXidEntry(ctx context.Context, entry *xidmsgpb.XidEntry) (*xidmsgpb.XidEntry, error) {
	if entry == nil {
		return nil, fmt.Errorf("entry must not be nil")
	}

	f.mu.Lock()
	defer f.mu.Unlock()

	var xid int64
	var err error
	if entry.GetName() == "" || entry.GetName() == "xids/0" {
		f.nextXID++
		xid = f.nextXID
		entry.Name = proto.String(validator.ConstructXIDName(xid))
	} else {
		xid, err = validator.ValidateXIDName(entry.GetName())
		if err != nil {
			return nil, fmt.Errorf("invalid XID name: %w", err)
		}
	}

	if _, exists := f.xidEntries[xid]; exists {
		return nil, fmt.Errorf("XID %d already exists: %w", xid, status.ErrAlreadyExists)
	}

	cloned := proto.Clone(entry).(*xidmsgpb.XidEntry)
	f.xidEntries[xid] = cloned
	return proto.Clone(cloned).(*xidmsgpb.XidEntry), nil
}

func (f *FakeStorage) FetchXidEntry(ctx context.Context, xid int64) (*xidmsgpb.XidEntry, error) {
	f.mu.RLock()
	defer f.mu.RUnlock()

	entry, exists := f.xidEntries[xid]
	if !exists {
		return nil, fmt.Errorf("XID %d not found: %w", xid, status.ErrNotFound)
	}
	return proto.Clone(entry).(*xidmsgpb.XidEntry), nil
}

func (f *FakeStorage) ListXidEntries(ctx context.Context, pageSize int32, pageToken, filter, orderBy string, staleness time.Duration) ([]*xidmsgpb.XidEntry, string, error) {
	f.mu.RLock()
	defer f.mu.RUnlock()

	var result []*xidmsgpb.XidEntry
	for _, entry := range f.xidEntries {
		result = append(result, proto.Clone(entry).(*xidmsgpb.XidEntry))
	}
	return result, "", nil
}

func (f *FakeStorage) UpdateXidEntry(ctx context.Context, update *storage.XidEntryUpdate) (*xidmsgpb.XidEntry, error) {
	if update == nil || update.XidEntry == nil {
		return nil, fmt.Errorf("update and update.XidEntry must not be nil")
	}

	f.mu.Lock()
	defer f.mu.Unlock()

	original, exists := f.xidEntries[update.XID]
	if !exists {
		return nil, fmt.Errorf("XID %d not found: %w", update.XID, status.ErrNotFound)
	}

	updatedClone := proto.Clone(update.XidEntry).(*xidmsgpb.XidEntry)
	f.xidEntries[update.XID] = updatedClone
	_ = original

	return proto.Clone(updatedClone).(*xidmsgpb.XidEntry), nil
}
