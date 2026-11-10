// Copyright 2026 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package component

import (
	"context"
	"fmt"

	"cloud.google.com/go/spanner"

	componentspb "xmanager_cloud/api/protos/components/v1alpha1"
	"xmanager_cloud/internal/grpcerr"
	spannerdata "xmanager_cloud/internal/storage/spanner/data"
	"xmanager_cloud/pkg/components/store"
)

// InsertComponent buffers mutations to insert a new component and its dependency rows.
func (r *TxRepository) InsertComponent(_ context.Context, rec *store.ComponentRecord, depIDs map[string]int64) error {
	if r.tx == nil {
		return fmt.Errorf("cannot insert component without a read-write transaction: %w", grpcerr.ErrFailedPrecondition)
	}
	mutations, err := spannerdata.ComponentInsertMutations(rec, depIDs)
	if err != nil {
		return err
	}
	return r.tx.BufferWrite(mutations)
}

// UpdateComponentIntent buffers mutations to update an existing component's intent and replace its dependencies.
func (r *TxRepository) UpdateComponentIntent(_ context.Context, rec *store.ComponentRecord, depIDs map[string]int64) error {
	if r.tx == nil {
		return fmt.Errorf("cannot update component intent without a read-write transaction: %w", grpcerr.ErrFailedPrecondition)
	}
	mutations, err := spannerdata.ComponentIntentUpdateMutations(rec, depIDs)
	if err != nil {
		return err
	}
	return r.tx.BufferWrite(mutations)
}

// UpdateComponentState buffers an update of a component's state and access status payload.
func (r *TxRepository) UpdateComponentState(_ context.Context, componentID int64, state componentspb.ComponentState, access *componentspb.ComponentAccessStatus) error {
	if r.tx == nil {
		return fmt.Errorf("cannot update component state without a read-write transaction: %w", grpcerr.ErrFailedPrecondition)
	}
	m, err := spannerdata.ComponentStateUpdateMutation(componentID, state, access)
	if err != nil {
		return err
	}
	return r.tx.BufferWrite([]*spanner.Mutation{m})
}

// UpdateComponentObservation buffers an update of rec's observed generation, access status, and optional state.
func (r *TxRepository) UpdateComponentObservation(_ context.Context, rec *store.ComponentRecord, updateState bool) error {
	if r.tx == nil {
		return fmt.Errorf("cannot update component observation without a read-write transaction: %w", grpcerr.ErrFailedPrecondition)
	}
	m, err := spannerdata.ComponentObservationUpdateMutation(rec, updateState)
	if err != nil {
		return err
	}
	return r.tx.BufferWrite([]*spanner.Mutation{m})
}

// SoftDeleteComponent buffers an update marking rec as DELETED and optionally updating its owner columns.
func (r *TxRepository) SoftDeleteComponent(_ context.Context, rec *store.ComponentRecord, owner *store.SourceOwner) error {
	if r.tx == nil {
		return fmt.Errorf("cannot soft-delete component without a read-write transaction: %w", grpcerr.ErrFailedPrecondition)
	}
	m, err := spannerdata.ComponentSoftDeleteMutation(rec, owner)
	if err != nil {
		return err
	}
	return r.tx.BufferWrite([]*spanner.Mutation{m})
}

// WriteComponentOwner buffers an update to componentID's source owner columns.
func (r *TxRepository) WriteComponentOwner(_ context.Context, componentID int64, owner *store.SourceOwner) error {
	if r.tx == nil {
		return fmt.Errorf("cannot write component owner without a read-write transaction: %w", grpcerr.ErrFailedPrecondition)
	}
	m := spannerdata.ComponentOwnerUpdateMutation(componentID, owner)
	if m == nil {
		return nil
	}
	return r.tx.BufferWrite([]*spanner.Mutation{m})
}

// InsertExperimentComponentSnapshot buffers a mutation pinning rec at rec.Generation to experiment xid.
func (r *TxRepository) InsertExperimentComponentSnapshot(_ context.Context, xid int64, rec *store.ComponentRecord) error {
	if r.tx == nil {
		return fmt.Errorf("cannot insert experiment component snapshot without a read-write transaction: %w", grpcerr.ErrFailedPrecondition)
	}
	m, err := spannerdata.ExperimentComponentSnapshotInsertMutation(xid, rec)
	if err != nil {
		return err
	}
	return r.tx.BufferWrite([]*spanner.Mutation{m})
}
