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

package spanner

import (
	"context"
	"time"

	"xmanager_cloud/internal/storage"
	"xmanager_cloud/pkg/components/store"
	"xmanager_cloud/pkg/components/store/ownership"
)

// DeclareClusterEpoch declares cluster the writer of sourced intents within Spanner, reading the
// fence, reassigning owners on a takeover, and writing the fence in one read-write transaction.
func (s *SpannerStore) DeclareClusterEpoch(ctx context.Context, cluster store.ClusterIdentity) (*store.DeclarationResult, error) {
	if err := ownership.ValidateClusterIdentity(cluster); err != nil {
		return nil, err
	}
	var res *store.DeclarationResult
	err := s.storage.RunInTransaction(ctx, func(ctx context.Context, tx *storage.Transaction) error {
		var err error
		res, err = s.declareClusterEpochTx(ctx, tx.Components, cluster)
		return err
	})
	if err != nil {
		return nil, ownership.RecordSourceConflictError(err)
	}
	return res, nil
}

var _ store.OwnershipGaugeReader = (*SpannerStore)(nil)

// ReadOwnershipGauges reads the fence and counts the stale owners in Spanner at now with one
// single-use read, so the fence and the count come from one snapshot.
func (s *SpannerStore) ReadOwnershipGauges(ctx context.Context, now time.Time) (store.OwnershipGauges, error) {
	return s.storage.Components().ReadOwnershipGauges(ctx, now.Add(-ownership.OwnerStaleThreshold))
}

// declareClusterEpochTx applies a declaration by cluster within txRepo.
func (s *SpannerStore) declareClusterEpochTx(ctx context.Context, txRepo storage.ComponentTxRepository, cluster store.ClusterIdentity) (*store.DeclarationResult, error) {
	fence, err := txRepo.ReadFence(ctx)
	if err != nil {
		return nil, err
	}
	outcome, err := ownership.PlanDeclaration(fence, cluster)
	if err != nil {
		return nil, err
	}
	res := &store.DeclarationResult{Outcome: outcome, Active: cluster}
	if outcome == store.DeclarationRefreshed {
		var newName *string
		if fence.Name != cluster.Name {
			newName = &cluster.Name
		}
		return res, txRepo.RefreshFence(ctx, newName)
	}
	if outcome == store.DeclarationTakeover {
		if res.RecordsReassigned, err = txRepo.ReassignOwnedComponents(ctx, cluster.UID, cluster.Epoch); err != nil {
			return nil, err
		}
	}
	return res, txRepo.ReplaceFence(ctx, cluster)
}

// checkFence reads the fence within txRepo and returns the rejection of src by CheckClusterFence.
// Unsourced requests (nil src) skip the read.
func (s *SpannerStore) checkFence(ctx context.Context, txRepo storage.ComponentTxRepository, operation string, src *store.RequestSource) error {
	if src == nil {
		return nil
	}
	fence, err := txRepo.ReadFence(ctx)
	if err != nil {
		return err
	}
	return ownership.CheckClusterFence(operation, fence, src)
}
