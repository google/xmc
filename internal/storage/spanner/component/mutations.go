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
	return r.bufferMutations(spannerdata.ComponentInsertMutations(rec, depIDs))
}

// UpdateComponentIntent buffers mutations to update an existing component's intent and replace its dependencies.
func (r *TxRepository) UpdateComponentIntent(_ context.Context, rec *store.ComponentRecord, depIDs map[string]int64) error {
	return r.bufferMutations(spannerdata.ComponentIntentUpdateMutations(rec, depIDs))
}

// UpdateComponentState buffers an update of a component's state and access status payload.
func (r *TxRepository) UpdateComponentState(_ context.Context, componentID int64, state componentspb.ComponentState, access *componentspb.ComponentAccessStatus) error {
	return r.bufferMutation(spannerdata.ComponentStateUpdateMutation(componentID, state, access))
}

// UpdateComponentObservation buffers an update of rec's observed generation, access status, and optional state.
func (r *TxRepository) UpdateComponentObservation(_ context.Context, rec *store.ComponentRecord, updateState bool) error {
	return r.bufferMutation(spannerdata.ComponentObservationUpdateMutation(rec, updateState))
}

// SoftDeleteComponent buffers an update marking rec as DELETED and optionally updating its owner columns.
func (r *TxRepository) SoftDeleteComponent(_ context.Context, rec *store.ComponentRecord, owner *store.SourceOwner) error {
	return r.bufferMutation(spannerdata.ComponentSoftDeleteMutation(rec, owner))
}

// WriteComponentOwner buffers an update to componentID's source owner columns.
func (r *TxRepository) WriteComponentOwner(_ context.Context, componentID int64, owner *store.SourceOwner) error {
	return r.bufferMutation(spannerdata.ComponentOwnerUpdateMutation(componentID, owner), nil)
}

func (r *TxRepository) bufferMutation(m *spanner.Mutation, err error) error {
	if m == nil && err == nil {
		return r.bufferMutations(nil, nil)
	}
	return r.bufferMutations([]*spanner.Mutation{m}, err)
}

func (r *TxRepository) bufferMutations(mutations []*spanner.Mutation, err error) error {
	if r.tx == nil {
		return fmt.Errorf("cannot buffer component write without a read-write transaction: %w", grpcerr.ErrFailedPrecondition)
	}
	if err != nil || len(mutations) == 0 {
		return err
	}
	return r.tx.BufferWrite(mutations)
}
