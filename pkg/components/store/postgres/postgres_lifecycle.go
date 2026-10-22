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

package postgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	componentspb "xmanager_cloud/api/protos/components/v1alpha1"
	"xmanager_cloud/internal/storage/postgres/pgdb"
	"xmanager_cloud/pkg/components/naming"
	"xmanager_cloud/pkg/components/store"
	"xmanager_cloud/pkg/components/store/dependency"
	"xmanager_cloud/pkg/components/store/lifecycle"
	"xmanager_cloud/pkg/components/store/ownership"
)

const (
	updateComponentStateSQL       = `UPDATE Components SET State = $2, AccessPayload = $3, UpdateTime = $4 WHERE ComponentId = $1`
	updateComponentObservationSQL = `UPDATE Components SET ObservedGeneration = $2, AccessPayload = $3, State = CASE WHEN $4 = 0 THEN State ELSE $4 END, UpdateTime = $5 WHERE ComponentId = $1`
	updateComponentDeletionSQL    = `UPDATE Components SET ConfigProto = $2, AccessPayload = $3, State = $4, UpdateTime = $5 WHERE ComponentId = $1`

	selectSnapshotByComponentIDAndGenSQL = `SELECT s.ComponentId, s.LockedGeneration, s.ConfigProto, s.AccessPayload, c.CreateTime, c.UpdateTime FROM ExperimentComponentSnapshots s JOIN Components c ON s.ComponentId = c.ComponentId WHERE s.ComponentId = $1 AND s.LockedGeneration = $2 LIMIT 1`
	selectSnapshotByNameAndGenSQL        = `SELECT s.ComponentId, s.LockedGeneration, s.ConfigProto, s.AccessPayload, c.CreateTime, c.UpdateTime FROM ExperimentComponentSnapshots s JOIN Components c ON s.ComponentId = c.ComponentId WHERE c.Name = $1 AND s.LockedGeneration = $2 ORDER BY c.CreateTime DESC, c.ComponentId DESC LIMIT 1`
)

// PrepareDeactivation checks deactivation safety and transitions target (and cascaded dependents) to DISABLING.
func (s *PostgresStore) PrepareDeactivation(ctx context.Context, name string, requestCascade bool, src ...store.RequestSource) (*store.PrepareDeactivationResult, error) {
	var result *store.PrepareDeactivationResult
	err := s.runCatalogTargetMutation(ctx, store.OperationPrepareDeactivation, name, src, func(ctx context.Context, tx pgx.Tx, catalog map[string]*store.ComponentRecord, targetRec *store.ComponentRecord, canonicalName string, reqSrc *store.RequestSource, commitTime time.Time) error {
		res, err := lifecycle.EvaluateDeactivationSafety(catalog, canonicalName, requestCascade, lifecycle.MinDrainTimeout)
		if err != nil {
			return err
		}
		result = res
		targets := collectDeactivationTargets(targetRec, catalog, res.CascadedDeactivations, requestCascade)
		if err := applyDisablingTransitionsTx(ctx, tx, targets, commitTime); err != nil {
			return err
		}
		return recordNonIntentOwnerTx(ctx, tx, targetRec, reqSrc, commitTime)
	})
	return result, err
}

// CompleteDeactivation marks a component in DISABLING state as DISABLED.
func (s *PostgresStore) CompleteDeactivation(ctx context.Context, name string, force bool, src ...store.RequestSource) error {
	return s.runCatalogTargetMutation(ctx, store.OperationCompleteDeactivation, name, src, func(ctx context.Context, tx pgx.Tx, catalog map[string]*store.ComponentRecord, rec *store.ComponentRecord, canonicalName string, reqSrc *store.RequestSource, commitTime time.Time) error {
		if rec.State == componentspb.ComponentState_COMPONENT_STATE_DISABLED {
			return recordNonIntentOwnerTx(ctx, tx, rec, reqSrc, commitTime)
		}
		if rec.State != componentspb.ComponentState_COMPONENT_STATE_DISABLING {
			return store.ErrInvalidStateTransition
		}
		if !force {
			if activeDeps, _ := lifecycle.FindActiveDependents(catalog, canonicalName); len(activeDeps) > 0 {
				return status.Errorf(codes.FailedPrecondition, "cannot complete deactivation of component %s: active dependents remaining: %v", canonicalName, activeDeps)
			}
		}
		if err := transitionComponentStateTx(ctx, tx, rec, componentspb.ComponentState_COMPONENT_STATE_DISABLED, commitTime); err != nil {
			return err
		}
		return recordNonIntentOwnerTx(ctx, tx, rec, reqSrc, commitTime)
	})
}

// UpdateObservedGeneration synchronizes actual component access, generation, and state.
func (s *PostgresStore) UpdateObservedGeneration(ctx context.Context, name string, observedGen int64, access *componentspb.ComponentAccessStatus, state componentspb.ComponentState, statusMsg string, src ...store.RequestSource) (*store.ComponentRecord, error) {
	canonicalName, reqSrc, err := ownership.ValidateUpdateObservedArgs(name, observedGen, src)
	if err != nil {
		return nil, err
	}
	var obsRes observationResult
	commitTimestamp, err := s.runInTx(ctx, func(ctx context.Context, tx pgx.Tx, commitTime time.Time) error {
		var err error
		obsRes, err = s.applyObservationTx(ctx, tx, canonicalName, observedGen, access, state, statusMsg, reqSrc, commitTime)
		return err
	})
	if err != nil {
		return nil, ownership.RecordSourceConflictError(err)
	}
	ownership.RecordOwnerAdoption(obsRes.prevOwner, reqSrc, false)
	if reqSrc != nil {
		obsRes.rec.SourceLastSeenTime = commitTimestamp
	}
	if !obsRes.unchanged {
		obsRes.rec.UpdateTime = commitTimestamp
		obsRes.rec.Etag = store.GenerateEtag(obsRes.rec)
	}
	return store.CloneRecord(obsRes.rec), nil
}

type observationResult struct {
	rec       *store.ComponentRecord
	prevOwner *store.SourceOwner
	unchanged bool
}

func (s *PostgresStore) applyObservationTx(ctx context.Context, tx pgx.Tx, canonicalName string, observedGen int64, access *componentspb.ComponentAccessStatus, state componentspb.ComponentState, statusMsg string, reqSrc *store.RequestSource, commitTime time.Time) (observationResult, error) {
	if err := checkFence(ctx, tx, store.OperationUpdateObservedGeneration, reqSrc); err != nil {
		return observationResult{}, err
	}
	rec, err := queryComponentWithDeps(ctx, tx, canonicalName)
	if err != nil {
		return observationResult{}, err
	}
	if err := ownership.CheckSourceMutation(store.OperationUpdateObservedGeneration, canonicalName, rec.Owner, reqSrc); err != nil {
		return observationResult{}, err
	}
	unchanged := !store.ApplyObservation(rec, observedGen, access, state, statusMsg)
	if !unchanged {
		if err := writeObservationTx(ctx, tx, rec, observedGen, state, commitTime); err != nil {
			return observationResult{}, err
		}
	}
	prevOwner := store.CloneSourceOwner(rec.Owner)
	if err := recordNonIntentOwnerTx(ctx, tx, rec, reqSrc, commitTime); err != nil {
		return observationResult{}, err
	}
	return observationResult{rec: store.CloneRecord(rec), prevOwner: prevOwner, unchanged: unchanged}, nil
}

func writeObservationTx(ctx context.Context, tx pgx.Tx, rec *store.ComponentRecord, observedGen int64, state componentspb.ComponentState, commitTime time.Time) error {
	var accessBytes []byte
	if rec.Status != nil {
		var err error
		if accessBytes, err = proto.Marshal(rec.Status); err != nil {
			return fmt.Errorf("failed to marshal access status proto: %w", err)
		}
	}
	_, err := tx.Exec(ctx, updateComponentObservationSQL, rec.ComponentID, observedGen, accessBytes, int64(state), commitTime)
	return err
}

// ExecuteDeletion deletes a component using the specified deletion policy.
func (s *PostgresStore) ExecuteDeletion(ctx context.Context, name string, policy componentspb.DeletionPolicy, force bool, src ...store.RequestSource) error {
	return s.runCatalogTargetMutation(ctx, store.OperationExecuteDeletion, name, src, func(ctx context.Context, tx pgx.Tx, catalog map[string]*store.ComponentRecord, rec *store.ComponentRecord, canonicalName string, reqSrc *store.RequestSource, commitTime time.Time) error {
		if err := lifecycle.ValidateDeletionSafety(catalog, canonicalName, policy, force); err != nil {
			return err
		}
		var owner *store.SourceOwner
		if reqSrc != nil {
			owner = ownership.DeletionOwner(rec.Owner, *reqSrc)
			rec.Owner = owner
		}
		toDelete := collectDeletionTargets(rec, catalog, canonicalName, policy)
		return applyDeletionMutationsTx(ctx, tx, toDelete, policy, commitTime, owner)
	})
}

// ResolveComponentSnapshot resolves or creates an immutable snapshot of a component at generation.
func (s *PostgresStore) ResolveComponentSnapshot(ctx context.Context, name string, generation int64) (*store.ComponentRecord, *componentspb.ResolvedComponentCoordinates, error) {
	canonicalName, err := store.ValidateResourceName(name)
	if err != nil {
		return nil, nil, err
	}
	if generation < 0 {
		return nil, nil, status.Error(codes.InvalidArgument, "generation cannot be negative")
	}
	var snapRec *store.ComponentRecord
	var coords *componentspb.ResolvedComponentCoordinates
	err = s.withReadOnlyTx(ctx, func(tx pgx.Tx) error {
		var err error
		snapRec, coords, err = s.resolveSnapshotInTx(ctx, tx, canonicalName, generation)
		return err
	})
	return snapRec, coords, err
}

func (s *PostgresStore) resolveSnapshotInTx(ctx context.Context, q pgdb.DBTX, canonicalName string, generation int64) (*store.ComponentRecord, *componentspb.ResolvedComponentCoordinates, error) {
	rec, err := queryComponentWithDeps(ctx, q, canonicalName)
	if err != nil {
		if errors.Is(err, store.ErrComponentNotFound) && generation > 0 {
			return queryHistoricalSnapshot(ctx, q, selectSnapshotByNameAndGenSQL, canonicalName, generation, canonicalName)
		}
		return nil, nil, err
	}
	targetGen := generation
	if targetGen == 0 {
		targetGen = rec.Generation
	}
	if targetGen > rec.Generation {
		if snapRec, coords, err := queryHistoricalSnapshot(ctx, q, selectSnapshotByNameAndGenSQL, canonicalName, targetGen, canonicalName); err == nil || status.Code(err) != codes.NotFound {
			return snapRec, coords, err
		}
		return nil, nil, status.Errorf(codes.NotFound, "snapshot for generation %d of component %s not found (current generation is %d)", targetGen, canonicalName, rec.Generation)
	}
	if targetGen == rec.Generation {
		var coords *componentspb.ResolvedComponentCoordinates
		if rec.Status != nil {
			coords = store.CloneCoordinates(rec.Status.Coordinates)
		}
		return store.CloneRecord(rec), coords, nil
	}
	return queryHistoricalSnapshot(ctx, q, selectSnapshotByComponentIDAndGenSQL, rec.ComponentID, targetGen, canonicalName)
}

func (s *PostgresStore) runCatalogTargetMutation(ctx context.Context, operation, name string, src []store.RequestSource, fn func(ctx context.Context, tx pgx.Tx, catalog map[string]*store.ComponentRecord, rec *store.ComponentRecord, canonicalName string, reqSrc *store.RequestSource, commitTime time.Time) error) error {
	canonicalName, reqSrc, err := ownership.ValidateSourcedResourceArgs(name, src)
	if err != nil {
		return err
	}
	var prevOwner *store.SourceOwner
	_, err = s.runInTx(ctx, func(ctx context.Context, tx pgx.Tx, commitTime time.Time) error {
		catalog, rec, err := s.lookupCatalogTargetTx(ctx, tx, operation, canonicalName, reqSrc)
		if err != nil {
			return err
		}
		prevOwner = store.CloneSourceOwner(rec.Owner)
		return fn(ctx, tx, catalog, rec, canonicalName, reqSrc, commitTime)
	})
	if err != nil {
		return ownership.RecordSourceConflictError(err)
	}
	ownership.RecordOwnerAdoption(prevOwner, reqSrc, false)
	return nil
}

func (s *PostgresStore) lookupCatalogTargetTx(ctx context.Context, tx pgx.Tx, operation, canonicalName string, src *store.RequestSource) (map[string]*store.ComponentRecord, *store.ComponentRecord, error) {
	if err := checkFence(ctx, tx, operation, src); err != nil {
		return nil, nil, err
	}
	catalog, err := s.loadActiveCatalog(ctx, tx)
	if err != nil {
		return nil, nil, err
	}
	rec, ok := catalog[canonicalName]
	if !ok || rec == nil || store.IsDeletedRecord(rec) {
		return nil, nil, store.ErrComponentNotFound
	}
	if err := ownership.CheckSourceMutation(operation, canonicalName, rec.Owner, src); err != nil {
		return nil, nil, err
	}
	return catalog, rec, nil
}

func recordNonIntentOwnerTx(ctx context.Context, tx pgx.Tx, rec *store.ComponentRecord, src *store.RequestSource, commitTime time.Time) error {
	if src == nil {
		return nil
	}
	return writeOwnerColumnsTx(ctx, tx, rec, ownership.NonIntentOwner(rec.Owner, src), commitTime)
}

func collectDeactivationTargets(targetRec *store.ComponentRecord, catalog map[string]*store.ComponentRecord, cascaded []string, requestCascade bool) []*store.ComponentRecord {
	targets := []*store.ComponentRecord{targetRec}
	if !requestCascade {
		return targets
	}
	for _, depName := range cascaded {
		if depRec, ok := catalog[depName]; ok && depRec != nil && !store.IsDeletedRecord(depRec) {
			targets = append(targets, depRec)
		}
	}
	return targets
}

func applyDisablingTransitionsTx(ctx context.Context, tx pgx.Tx, targets []*store.ComponentRecord, commitTime time.Time) error {
	for _, rec := range targets {
		if rec.State == componentspb.ComponentState_COMPONENT_STATE_DISABLING || rec.State == componentspb.ComponentState_COMPONENT_STATE_DISABLED {
			continue
		}
		if err := transitionComponentStateTx(ctx, tx, rec, componentspb.ComponentState_COMPONENT_STATE_DISABLING, commitTime); err != nil {
			return err
		}
	}
	return nil
}

func transitionComponentStateTx(ctx context.Context, tx pgx.Tx, rec *store.ComponentRecord, newState componentspb.ComponentState, commitTime time.Time) error {
	statusProto := &componentspb.ComponentAccessStatus{}
	if rec.Status != nil {
		statusProto = proto.Clone(rec.Status).(*componentspb.ComponentAccessStatus)
	}
	statusProto.State = newState
	statusProto.LastTransitionTime = timestamppb.New(commitTime)
	accessBytes, err := proto.Marshal(statusProto)
	if err != nil {
		return fmt.Errorf("failed to marshal access status proto: %w", err)
	}
	_, err = tx.Exec(ctx, updateComponentStateSQL, rec.ComponentID, int64(newState), accessBytes, commitTime)
	return err
}

func collectDeletionTargets(rec *store.ComponentRecord, catalog map[string]*store.ComponentRecord, canonicalName string, policy componentspb.DeletionPolicy) []*store.ComponentRecord {
	toDelete := []*store.ComponentRecord{rec}
	if policy == componentspb.DeletionPolicy_DELETION_POLICY_DELETE || policy == componentspb.DeletionPolicy_DELETION_POLICY_UNSPECIFIED {
		allDependents, _ := lifecycle.FindAllDependents(catalog, canonicalName)
		for _, depName := range allDependents {
			if depRec, ok := catalog[depName]; ok && depRec != nil && !store.IsDeletedRecord(depRec) {
				toDelete = append(toDelete, depRec)
			}
		}
	}
	return toDelete
}

func applyDeletionMutationsTx(ctx context.Context, tx pgx.Tx, toDelete []*store.ComponentRecord, policy componentspb.DeletionPolicy, commitTime time.Time, targetOwner *store.SourceOwner) error {
	for i, target := range toDelete {
		if err := deleteSingleComponentTx(ctx, tx, target, policy, commitTime); err != nil {
			return err
		}
		if i == 0 && targetOwner != nil {
			if err := writeOwnerColumnsTx(ctx, tx, target, targetOwner, commitTime); err != nil {
				return err
			}
		}
	}
	return nil
}

func deleteSingleComponentTx(ctx context.Context, tx pgx.Tx, target *store.ComponentRecord, policy componentspb.DeletionPolicy, commitTime time.Time) error {
	target.State = componentspb.ComponentState_COMPONENT_STATE_DELETED
	if target.Status == nil {
		target.Status = &componentspb.ComponentAccessStatus{}
	}
	target.Status.State = componentspb.ComponentState_COMPONENT_STATE_DELETED
	target.Status.LastTransitionTime = timestamppb.New(commitTime)
	if policy != componentspb.DeletionPolicy_DELETION_POLICY_UNSPECIFIED && target.Spec != nil {
		target.Spec.DeletionPolicy = policy
	}
	configBytes, err := proto.Marshal(&componentspb.Component{
		Name:   naming.FullComponentName(target.Name),
		Spec:   target.Spec,
		Labels: store.CopyMap(target.Labels),
	})
	if err != nil {
		return fmt.Errorf("failed to marshal component proto: %w", err)
	}
	accessBytes, err := proto.Marshal(target.Status)
	if err != nil {
		return fmt.Errorf("failed to marshal access status proto: %w", err)
	}
	_, err = tx.Exec(ctx, updateComponentDeletionSQL, target.ComponentID, configBytes, accessBytes, int64(componentspb.ComponentState_COMPONENT_STATE_DELETED), commitTime)
	return err
}

func queryHistoricalSnapshot(ctx context.Context, q pgdb.DBTX, querySQL string, arg any, targetGen int64, canonicalName string) (*store.ComponentRecord, *componentspb.ResolvedComponentCoordinates, error) {
	var compID, lockedGen int64
	var configProto, accessPayload []byte
	var createTime, updateTime time.Time
	err := q.QueryRow(ctx, querySQL, arg, targetGen).Scan(&compID, &lockedGen, &configProto, &accessPayload, &createTime, &updateTime)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil, status.Errorf(codes.NotFound, "historical snapshot for generation %d of component %s not found", targetGen, canonicalName)
		}
		return nil, nil, fmt.Errorf("failed to query experiment component snapshots: %w", err)
	}
	return snapshotRowToRecord(compID, lockedGen, configProto, accessPayload, canonicalName, createTime.UTC(), updateTime.UTC())
}

func snapshotRowToRecord(compID, lockedGen int64, configProto, accessPayload []byte, canonicalName string, createTime, updateTime time.Time) (*store.ComponentRecord, *componentspb.ResolvedComponentCoordinates, error) {
	snapRec := &store.ComponentRecord{
		ComponentID: compID,
		Name:        canonicalName,
		Generation:  lockedGen,
		CreateTime:  createTime,
		UpdateTime:  updateTime,
	}
	if err := unmarshalConfigProto(configProto, snapRec); err != nil {
		return nil, nil, fmt.Errorf("failed to unmarshal historical config proto: %w", err)
	}
	if err := unmarshalAccessStatus(accessPayload, snapRec); err != nil {
		return nil, nil, fmt.Errorf("failed to unmarshal historical access status: %w", err)
	}
	var coords *componentspb.ResolvedComponentCoordinates
	if snapRec.Status != nil {
		snapRec.State = snapRec.Status.State
		obsGen := snapRec.Status.ObservedGeneration
		snapRec.ObservedGeneration = &obsGen
		coords = store.CloneCoordinates(snapRec.Status.Coordinates)
	}
	snapRec.Dependencies = dependency.ExtractDependencies(snapRec.Spec, snapRec.Labels)
	snapRec.Etag = store.GenerateEtag(snapRec)
	return snapRec, coords, nil
}
