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

// Package dbstore provides a storage.Storage-backed implementation of store.Store.
package dbstore

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"sort"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	componentspb "xmanager_cloud/api/protos/components/v1alpha1"
	"xmanager_cloud/internal/logging"
	"xmanager_cloud/internal/storage"
	"xmanager_cloud/pkg/components/semver"
	"xmanager_cloud/pkg/components/store"
	"xmanager_cloud/pkg/components/store/dependency"
	"xmanager_cloud/pkg/components/store/lifecycle"
	"xmanager_cloud/pkg/components/store/ownership"
)

// Ensure Store implements the store.Store interface at compile time.
var _ store.Store = (*Store)(nil)

// Store provides a storage.Storage-backed production implementation of store.Store.
type Store struct {
	storage storage.Storage
	// beforeIntentWrite, when non-nil, runs before SetComponentIntent falls back to its read-write
	// transaction. Tests use it to count fallbacks and to inject concurrent writes.
	beforeIntentWrite func()
}

// New creates a component store over st. Every operation goes through st's
// ComponentRepository and TransactionRunner (including RunInTransactionWithCommitTime), so st must
// implement them; today only the Spanner backend does, and other backends return codes.Unimplemented.
func New(st storage.Storage) (*Store, error) {
	if st == nil {
		return nil, errors.New("dbstore: storage.Storage must not be nil")
	}
	return &Store{storage: st}, nil
}

// SetComponentIntent applies or updates component intent within storage. An unchanged intent whose
// dependencies are all registered and active is answered from a strong read-only read without a
// read-write transaction; anything else, including every changed or new intent (which therefore
// pays the extra read), is decided by the read-write transaction.
func (s *Store) SetComponentIntent(ctx context.Context, name string, spec *componentspb.ComponentSpec, labels map[string]string, desiredGen int64) (*store.ComponentRecord, error) {
	return s.setIntent(ctx, name, spec, labels, desiredGen, nil)
}

// SetSourcedComponentIntent applies an intent sent by src within storage, with the same read-only
// fast path as SetComponentIntent. Recording a new owner, and every rejection except a fence
// rejection seen by the fast-path read, is decided by the read-write transaction.
func (s *Store) SetSourcedComponentIntent(ctx context.Context, name string, spec *componentspb.ComponentSpec, labels map[string]string, src store.RequestSource) (*store.ComponentRecord, error) {
	if err := ownership.ValidateRequestSource(src); err != nil {
		return nil, err
	}
	return s.setIntent(ctx, name, spec, labels, 0, &src)
}

// setIntent applies an intent ordered by desiredGen when src is nil and by src otherwise.
func (s *Store) setIntent(ctx context.Context, name string, spec *componentspb.ComponentSpec, labels map[string]string, desiredGen int64, src *store.RequestSource) (*store.ComponentRecord, error) {
	canonicalName, err := store.ValidateIntentArgs(name, spec)
	if err != nil {
		return nil, err
	}

	deps := dependency.ExtractDependencies(spec, labels)
	rec, ok, err := s.readUnchangedIntent(ctx, canonicalName, spec, labels, deps, desiredGen, src)
	if err != nil {
		// The fast path returns only fence rejections, which count as source conflicts.
		return nil, ownership.RecordSourceConflictError(err)
	}
	if ok {
		return rec, nil
	}
	if s.beforeIntentWrite != nil {
		s.beforeIntentWrite()
	}
	return s.writeIntent(ctx, canonicalName, spec, labels, deps, desiredGen, src)
}

// readUnchangedIntent reads the active record of canonicalName, its active dependencies and the
// ownership fence with a single strong read. It returns the record and true only when the intent
// writes nothing at that read. A fence rejection is returned as the error: the strong read sees
// the latest committed fence, so it is authoritative and fenced clusters replaying intents cost no
// read-write transaction. On any other outcome, including read errors and other rejections, it
// returns false so that the read-write transaction decides authoritatively.
func (s *Store) readUnchangedIntent(ctx context.Context, canonicalName string, spec *componentspb.ComponentSpec, labels map[string]string, deps []string, desiredGen int64, src *store.RequestSource) (*store.ComponentRecord, bool, error) {
	rec, fence, err := s.storage.Components().GetComponentWithFence(ctx, canonicalName)
	if err != nil {
		logFastPathReadError(ctx, canonicalName, err)
		return nil, false, nil
	}
	if err := ownership.CheckClusterFence(store.OperationSetComponentIntent, fence, src); err != nil {
		return nil, false, err
	}
	// The read returns only registered, non-deleted dependencies, so a deregistered dependency
	// makes the intent's dependencies differ and the plan a write; the read-write fallback then
	// rejects it authoritatively.
	if plan, err := ownership.PlanIntent(canonicalName, rec, spec, labels, deps, desiredGen, src); err != nil || plan.Write != ownership.WriteNothing {
		return nil, false, nil
	}
	return store.CloneRecord(rec), true, nil
}

// logFastPathReadError logs a failed fast-path read before the read-write fallback. A missing
// component is expected for new intents and is not logged; cancellation is logged at Debug.
func logFastPathReadError(ctx context.Context, canonicalName string, err error) {
	switch {
	case errors.Is(err, store.ErrComponentNotFound):
	case ctx.Err() != nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded):
		logging.Debug(ctx, "Intent fast-path read canceled; falling back to read-write transaction", "name", canonicalName, "error", err)
	default:
		logging.Warn(ctx, "Intent fast-path read failed; falling back to read-write transaction", "name", canonicalName, "error", err)
	}
}

// writeIntent applies the intent in a read-write transaction. It returns the existing record, with
// any newly recorded owner, when the intent writes neither the spec nor the generation.
func (s *Store) writeIntent(ctx context.Context, canonicalName string, spec *componentspb.ComponentSpec, labels map[string]string, deps []string, desiredGen int64, src *store.RequestSource) (*store.ComponentRecord, error) {
	var finalRec *store.ComponentRecord
	var plan ownership.IntentPlan

	commitTimestamp, err := s.storage.RunInTransactionWithCommitTime(ctx, func(ctx context.Context, tx *storage.Transaction) error {
		// Assign both results on every attempt because the transaction body may be retried.
		var err error
		finalRec, plan, err = s.applyIntentTx(ctx, tx.Components, canonicalName, spec, labels, deps, desiredGen, src)
		return err
	})
	if err != nil {
		return nil, ownership.RecordSourceConflictError(err)
	}
	if plan.Write == ownership.WriteSpec {
		if finalRec.CreateTime.IsZero() {
			finalRec.CreateTime = commitTimestamp
		}
		finalRec.UpdateTime = commitTimestamp
		finalRec.Etag = store.GenerateEtag(finalRec)
	}
	if src != nil && plan.Write != ownership.WriteNothing {
		finalRec.SourceLastSeenTime = commitTimestamp
	}
	if plan.Write != ownership.WriteNothing {
		ownership.RecordOwnerAdoption(plan.PrevOwner, src, plan.Created)
	}
	return store.CloneRecord(finalRec), nil
}

// applyIntentTx validates the intent against the active catalog, buffers the mutations that
// create or update the component and record src as its owner, and returns the resulting record
// with the applied plan. A created component is reported as a WriteSpec plan.
func (s *Store) applyIntentTx(
	ctx context.Context,
	txRepo storage.ComponentTxRepository,
	canonicalName string,
	spec *componentspb.ComponentSpec,
	labels map[string]string,
	deps []string,
	desiredGen int64,
	src *store.RequestSource,
) (*store.ComponentRecord, ownership.IntentPlan, error) {
	if err := s.checkFence(ctx, txRepo, store.OperationSetComponentIntent, src); err != nil {
		return nil, ownership.IntentPlan{}, err
	}
	catalog, err := txRepo.ListActiveComponents(ctx)
	if err != nil {
		return nil, ownership.IntentPlan{}, err
	}
	existingRec, plan, err := ownership.PlanLiveIntent(catalog, canonicalName, spec, labels, deps, desiredGen, src)
	if err != nil {
		return nil, ownership.IntentPlan{}, err
	}
	depIDs, err := dependency.ValidateIntentDependencies(catalog, canonicalName, deps)
	if err != nil {
		return nil, ownership.IntentPlan{}, err
	}

	var rec *store.ComponentRecord
	switch {
	case plan.Write == ownership.WriteNothing:
		return existingRec, plan, nil
	case plan.Write == ownership.WriteOwnerOnly:
		rec = store.CloneRecord(existingRec)
	case existingRec != nil:
		rec, err = s.updateExistingIntentTx(ctx, txRepo, existingRec, canonicalName, spec, labels, deps, plan.Generation, depIDs)
	default:
		rec, err = s.createNewIntentTx(ctx, txRepo, canonicalName, spec, labels, deps, plan.Generation, depIDs)
	}
	if err != nil {
		return nil, ownership.IntentPlan{}, err
	}
	return rec, plan, recordOwnerTx(ctx, txRepo, rec, src)
}

// recordOwnerTx buffers a write of src as the owner of rec, with SourceLastSeenTime at the commit
// timestamp, and sets rec.Owner. A nil src keeps the recorded owner. SourceLastSeenTime is the
// commit time of the last request accepted from the recorded owner; it only feeds owner-staleness
// observability and never decides ownership.
func recordOwnerTx(ctx context.Context, txRepo storage.ComponentTxRepository, rec *store.ComponentRecord, src *store.RequestSource) error {
	if src == nil {
		return nil
	}
	return writeOwnerColumnsTx(ctx, txRepo, rec, store.OwnerOf(*src))
}

func recordNonIntentOwnerTx(ctx context.Context, txRepo storage.ComponentTxRepository, rec *store.ComponentRecord, src *store.RequestSource) error {
	if src == nil {
		return nil
	}
	return writeOwnerColumnsTx(ctx, txRepo, rec, ownership.NonIntentOwner(rec.Owner, src))
}

// writeOwnerColumnsTx buffers a write of owner into rec's source columns, with SourceLastSeenTime
// at the commit timestamp, and sets rec.Owner. A nil owner is a no-op that keeps the recorded owner.
func writeOwnerColumnsTx(ctx context.Context, txRepo storage.ComponentTxRepository, rec *store.ComponentRecord, owner *store.SourceOwner) error {
	if owner == nil {
		return nil
	}
	rec.Owner = owner
	return txRepo.WriteComponentOwner(ctx, rec.ComponentID, owner)
}

func (s *Store) updateExistingIntentTx(
	ctx context.Context,
	txRepo storage.ComponentTxRepository,
	existingRec *store.ComponentRecord,
	canonicalName string,
	spec *componentspb.ComponentSpec,
	labels map[string]string,
	deps []string,
	newGen int64,
	depIDs map[string]int64,
) (*store.ComponentRecord, error) {
	specClone := proto.Clone(spec).(*componentspb.ComponentSpec)
	specClone.Generation = newGen

	var statusProto *componentspb.ComponentAccessStatus
	if existingRec.Status != nil {
		statusProto = proto.Clone(existingRec.Status).(*componentspb.ComponentAccessStatus)
	} else {
		statusProto = &componentspb.ComponentAccessStatus{}
	}
	statusProto.State = componentspb.ComponentState_COMPONENT_STATE_PENDING_RECONCILIATION

	rec := &store.ComponentRecord{
		ComponentID:        existingRec.ComponentID,
		Name:               canonicalName,
		Spec:               specClone,
		Status:             statusProto,
		State:              componentspb.ComponentState_COMPONENT_STATE_PENDING_RECONCILIATION,
		Generation:         newGen,
		ObservedGeneration: existingRec.ObservedGeneration,
		Dependencies:       deps,
		CreateTime:         existingRec.CreateTime,
		Labels:             store.CopyMap(labels),
		Owner:              store.CloneSourceOwner(existingRec.Owner),
		SourceLastSeenTime: existingRec.SourceLastSeenTime,
	}
	if err := txRepo.UpdateComponentIntent(ctx, rec, depIDs); err != nil {
		return nil, err
	}
	return rec, nil
}

func (s *Store) createNewIntentTx(
	ctx context.Context,
	txRepo storage.ComponentTxRepository,
	canonicalName string,
	spec *componentspb.ComponentSpec,
	labels map[string]string,
	deps []string,
	gen int64,
	depIDs map[string]int64,
) (*store.ComponentRecord, error) {
	id, err := generateRandomID()
	if err != nil {
		return nil, fmt.Errorf("failed to generate component ID: %w", err)
	}

	specClone := proto.Clone(spec).(*componentspb.ComponentSpec)
	specClone.Generation = gen

	statusProto := &componentspb.ComponentAccessStatus{
		State: componentspb.ComponentState_COMPONENT_STATE_PENDING_RECONCILIATION,
	}

	rec := &store.ComponentRecord{
		ComponentID:  id,
		Name:         canonicalName,
		Spec:         specClone,
		Status:       statusProto,
		State:        componentspb.ComponentState_COMPONENT_STATE_PENDING_RECONCILIATION,
		Generation:   gen,
		Dependencies: deps,
		Labels:       store.CopyMap(labels),
	}
	if err := txRepo.InsertComponent(ctx, rec, depIDs); err != nil {
		return nil, err
	}
	return rec, nil
}

// GetComponent retrieves a component record by canonical or full resource name.
func (s *Store) GetComponent(ctx context.Context, name string) (*store.ComponentRecord, error) {
	canonicalName, err := store.ValidateResourceName(name)
	if err != nil {
		return nil, err
	}

	rec, err := s.storage.Components().GetActiveComponent(ctx, canonicalName)
	if err != nil {
		return nil, err
	}

	return store.CloneRecord(rec), nil
}

// GetComponentByID retrieves a component record by its unique database identifier.
func (s *Store) GetComponentByID(ctx context.Context, componentID int64) (*store.ComponentRecord, error) {
	if componentID <= 0 {
		return nil, status.Error(codes.InvalidArgument, "invalid component ID")
	}

	rec, err := s.storage.Components().GetComponentByID(ctx, componentID)
	if err != nil {
		return nil, err
	}

	return store.CloneRecord(rec), nil
}

// ListComponents returns all non-deleted components in the catalog sorted by name.
func (s *Store) ListComponents(ctx context.Context) ([]*store.ComponentRecord, error) {
	catalog, err := s.storage.Components().ListActiveComponents(ctx)
	if err != nil {
		return nil, err
	}

	results := make([]*store.ComponentRecord, 0, len(catalog))
	for _, rec := range catalog {
		if !store.IsDeletedRecord(rec) {
			results = append(results, store.CloneRecord(rec))
		}
	}

	sort.Slice(results, func(i, j int) bool {
		return results[i].Name < results[j].Name
	})

	return results, nil
}

// PrepareDeactivation checks deactivation safety and transitions target (and cascaded dependents) to DISABLING.
func (s *Store) PrepareDeactivation(ctx context.Context, name string, requestCascade bool, src ...store.RequestSource) (*store.PrepareDeactivationResult, error) {
	canonicalName, reqSrc, err := ownership.ValidateSourcedResourceArgs(name, src)
	if err != nil {
		return nil, err
	}

	var result *store.PrepareDeactivationResult
	var prevOwner *store.SourceOwner

	err = s.storage.RunInTransaction(ctx, func(ctx context.Context, tx *storage.Transaction) error {
		catalog, targetRec, err := s.lookupCatalogTargetTx(ctx, tx.Components, store.OperationPrepareDeactivation, canonicalName, reqSrc)
		if err != nil {
			return err
		}

		res, err := lifecycle.EvaluateDeactivationSafety(catalog, canonicalName, requestCascade, lifecycle.MinDrainTimeout)
		if err != nil {
			return err
		}
		result = res

		toTransition := collectDeactivationTargets(targetRec, catalog, res.CascadedDeactivations, requestCascade)
		if err := transitionToDisablingTx(ctx, tx.Components, toTransition, time.Now().UTC()); err != nil {
			return err
		}
		prevOwner = store.CloneSourceOwner(targetRec.Owner)
		return recordNonIntentOwnerTx(ctx, tx.Components, targetRec, reqSrc)
	})

	if err != nil {
		return nil, ownership.RecordSourceConflictError(err)
	}
	ownership.RecordOwnerAdoption(prevOwner, reqSrc, false)
	return result, nil
}

// lookupCatalogTargetTx checks the cluster fence, loads the active catalog, finds canonicalName,
// and checks source ownership for operation.
func (s *Store) lookupCatalogTargetTx(ctx context.Context, txRepo storage.ComponentTxRepository, operation, canonicalName string, src *store.RequestSource) (map[string]*store.ComponentRecord, *store.ComponentRecord, error) {
	if err := s.checkFence(ctx, txRepo, operation, src); err != nil {
		return nil, nil, err
	}
	catalog, err := txRepo.ListActiveComponents(ctx)
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

func transitionToDisablingTx(ctx context.Context, txRepo storage.ComponentTxRepository, toTransition []*store.ComponentRecord, now time.Time) error {
	for _, rec := range toTransition {
		if rec.State == componentspb.ComponentState_COMPONENT_STATE_DISABLING || rec.State == componentspb.ComponentState_COMPONENT_STATE_DISABLED {
			continue
		}

		statusProto := rec.Status
		if statusProto == nil {
			statusProto = &componentspb.ComponentAccessStatus{}
		} else {
			statusProto = proto.Clone(statusProto).(*componentspb.ComponentAccessStatus)
		}
		statusProto.State = componentspb.ComponentState_COMPONENT_STATE_DISABLING
		statusProto.LastTransitionTime = timestamppb.New(now)

		if err := txRepo.UpdateComponentState(ctx, rec.ComponentID, componentspb.ComponentState_COMPONENT_STATE_DISABLING, statusProto); err != nil {
			return err
		}
	}
	return nil
}

// CompleteDeactivation marks a component in DISABLING state as DISABLED.
func (s *Store) CompleteDeactivation(ctx context.Context, name string, force bool, src ...store.RequestSource) error {
	canonicalName, reqSrc, err := ownership.ValidateSourcedResourceArgs(name, src)
	if err != nil {
		return err
	}

	var prevOwner *store.SourceOwner
	err = s.storage.RunInTransaction(ctx, func(ctx context.Context, tx *storage.Transaction) error {
		catalog, rec, err := s.lookupCatalogTargetTx(ctx, tx.Components, store.OperationCompleteDeactivation, canonicalName, reqSrc)
		if err != nil {
			return err
		}

		if rec.State == componentspb.ComponentState_COMPONENT_STATE_DISABLED {
			prevOwner = store.CloneSourceOwner(rec.Owner)
			return recordNonIntentOwnerTx(ctx, tx.Components, rec, reqSrc)
		}
		if rec.State != componentspb.ComponentState_COMPONENT_STATE_DISABLING {
			return store.ErrInvalidStateTransition
		}

		if !force {
			activeDependents, _ := lifecycle.FindActiveDependents(catalog, canonicalName)
			if len(activeDependents) > 0 {
				return status.Errorf(codes.FailedPrecondition, "cannot complete deactivation of component %s: active dependents remaining: %v", canonicalName, activeDependents)
			}
		}

		now := time.Now().UTC()
		statusProto := rec.Status
		if statusProto == nil {
			statusProto = &componentspb.ComponentAccessStatus{}
		} else {
			statusProto = proto.Clone(statusProto).(*componentspb.ComponentAccessStatus)
		}
		statusProto.State = componentspb.ComponentState_COMPONENT_STATE_DISABLED
		statusProto.LastTransitionTime = timestamppb.New(now)

		if err := tx.Components.UpdateComponentState(ctx, rec.ComponentID, componentspb.ComponentState_COMPONENT_STATE_DISABLED, statusProto); err != nil {
			return err
		}
		prevOwner = store.CloneSourceOwner(rec.Owner)
		return recordNonIntentOwnerTx(ctx, tx.Components, rec, reqSrc)
	})

	if err != nil {
		return ownership.RecordSourceConflictError(err)
	}
	ownership.RecordOwnerAdoption(prevOwner, reqSrc, false)
	return nil
}

// UpdateObservedGeneration synchronizes actual component access, generation, and state.
func (s *Store) UpdateObservedGeneration(ctx context.Context, name string, observedGen int64, access *componentspb.ComponentAccessStatus, state componentspb.ComponentState, statusMsg string, src ...store.RequestSource) (*store.ComponentRecord, error) {
	canonicalName, reqSrc, err := ownership.ValidateUpdateObservedArgs(name, observedGen, src)
	if err != nil {
		return nil, err
	}

	var finalRec *store.ComponentRecord
	var unchanged bool
	var prevOwner *store.SourceOwner

	commitTimestamp, err := s.storage.RunInTransactionWithCommitTime(ctx, func(ctx context.Context, tx *storage.Transaction) error {
		unchanged = false
		if err := s.checkFence(ctx, tx.Components, store.OperationUpdateObservedGeneration, reqSrc); err != nil {
			return err
		}
		rec, err := tx.Components.GetActiveComponent(ctx, canonicalName)
		if err != nil {
			return err
		}
		if err := ownership.CheckSourceMutation(store.OperationUpdateObservedGeneration, canonicalName, rec.Owner, reqSrc); err != nil {
			return err
		}

		// An unchanged heartbeat writes only the owner columns, refreshing SourceLastSeenTime of a
		// sourced call, and keeps UpdateTime.
		unchanged = !store.ApplyObservation(rec, observedGen, access, state, statusMsg)
		if !unchanged {
			if err := tx.Components.UpdateComponentObservation(ctx, rec, state != componentspb.ComponentState_COMPONENT_STATE_UNSPECIFIED); err != nil {
				return err
			}
		}
		prevOwner = store.CloneSourceOwner(rec.Owner)
		if err := recordNonIntentOwnerTx(ctx, tx.Components, rec, reqSrc); err != nil {
			return err
		}

		finalRec = store.CloneRecord(rec)
		return nil
	})

	if err != nil {
		return nil, ownership.RecordSourceConflictError(err)
	}
	ownership.RecordOwnerAdoption(prevOwner, reqSrc, false)

	if reqSrc != nil {
		finalRec.SourceLastSeenTime = commitTimestamp
	}
	if !unchanged {
		finalRec.UpdateTime = commitTimestamp
		finalRec.Etag = store.GenerateEtag(finalRec)
	}
	return store.CloneRecord(finalRec), nil
}

// ExecuteDeletion deletes a component using the specified deletion policy.
func (s *Store) ExecuteDeletion(ctx context.Context, name string, policy componentspb.DeletionPolicy, force bool, src ...store.RequestSource) error {
	canonicalName, reqSrc, err := ownership.ValidateSourcedResourceArgs(name, src)
	if err != nil {
		return err
	}

	var prevOwner *store.SourceOwner
	err = s.storage.RunInTransaction(ctx, func(ctx context.Context, tx *storage.Transaction) error {
		catalog, rec, err := s.lookupCatalogTargetTx(ctx, tx.Components, store.OperationExecuteDeletion, canonicalName, reqSrc)
		if err != nil {
			return err
		}

		if err := lifecycle.ValidateDeletionSafety(catalog, canonicalName, policy, force); err != nil {
			return err
		}

		prevOwner = store.CloneSourceOwner(rec.Owner)
		var owner *store.SourceOwner
		if reqSrc != nil {
			owner = ownership.DeletionOwner(rec.Owner, *reqSrc)
			rec.Owner = owner
		}
		toDelete := collectDeletionTargets(rec, catalog, canonicalName, policy)
		return softDeleteTargetsTx(ctx, tx.Components, toDelete, policy, time.Now().UTC(), owner)
	})

	if err != nil {
		return ownership.RecordSourceConflictError(err)
	}
	ownership.RecordOwnerAdoption(prevOwner, reqSrc, false)
	return nil
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

// softDeleteTargetsTx marks each target DELETED. A non-nil targetOwner is written with
// toDelete[0] (the requested deletion target) so the row is written once.
func softDeleteTargetsTx(ctx context.Context, txRepo storage.ComponentTxRepository, toDelete []*store.ComponentRecord, policy componentspb.DeletionPolicy, now time.Time, targetOwner *store.SourceOwner) error {
	for i, target := range toDelete {
		target.State = componentspb.ComponentState_COMPONENT_STATE_DELETED
		if target.Status == nil {
			target.Status = &componentspb.ComponentAccessStatus{}
		}
		target.Status.State = componentspb.ComponentState_COMPONENT_STATE_DELETED
		target.Status.LastTransitionTime = timestamppb.New(now)

		if policy != componentspb.DeletionPolicy_DELETION_POLICY_UNSPECIFIED && target.Spec != nil {
			target.Spec.DeletionPolicy = policy
		}

		var owner *store.SourceOwner
		if i == 0 {
			owner = targetOwner
		}
		if err := txRepo.SoftDeleteComponent(ctx, target, owner); err != nil {
			return err
		}
	}
	return nil
}

// ResolveComponentSnapshot resolves or creates an immutable snapshot of a component at generation.
func (s *Store) ResolveComponentSnapshot(ctx context.Context, name string, generation int64) (*store.ComponentRecord, *componentspb.ResolvedComponentCoordinates, error) {
	canonicalName, err := store.ValidateResourceName(name)
	if err != nil {
		return nil, nil, err
	}
	if generation < 0 {
		return nil, nil, status.Error(codes.InvalidArgument, "generation cannot be negative")
	}

	var outRec *store.ComponentRecord
	var outCoords *componentspb.ResolvedComponentCoordinates
	err = s.storage.Components().WithReadOnlySnapshot(ctx, func(ctx context.Context, repo storage.ComponentTxRepository) error {
		var err error
		outRec, outCoords, err = resolveSnapshotInRepo(ctx, repo, canonicalName, generation)
		return err
	})
	return outRec, outCoords, err
}

func resolveSnapshotInRepo(ctx context.Context, repo storage.ComponentTxRepository, canonicalName string, generation int64) (*store.ComponentRecord, *componentspb.ResolvedComponentCoordinates, error) {
	rec, err := repo.GetActiveComponent(ctx, canonicalName)
	if err != nil {
		if errors.Is(err, store.ErrComponentNotFound) && generation > 0 {
			return withSnapshotDependencies(repo.GetSnapshotByName(ctx, canonicalName, generation))
		}
		return nil, nil, err
	}

	targetGen := generation
	if targetGen == 0 {
		targetGen = rec.Generation
	}

	if targetGen > rec.Generation {
		// If the active component is at a lower generation than requested (e.g. name was reused
		// after soft-deleting an older incarnation that reached a higher generation), attempt
		// resolving historically by name before returning NotFound.
		snapRec, coords, err := withSnapshotDependencies(repo.GetSnapshotByName(ctx, canonicalName, targetGen))
		if err == nil {
			return snapRec, coords, nil
		}
		if status.Code(err) != codes.NotFound {
			return nil, nil, err
		}
		return nil, nil, status.Errorf(codes.NotFound, "snapshot for generation %d of component %s not found (current generation is %d)", targetGen, canonicalName, rec.Generation)
	}

	if targetGen == rec.Generation {
		var coords *componentspb.ResolvedComponentCoordinates
		if rec.Status != nil && rec.Status.Coordinates != nil {
			coords = store.CloneCoordinates(rec.Status.Coordinates)
		}
		return store.CloneRecord(rec), coords, nil
	}

	return withSnapshotDependencies(repo.GetSnapshotByID(ctx, rec.ComponentID, targetGen, canonicalName, rec.CreateTime, rec.UpdateTime))
}

// withSnapshotDependencies derives the dependencies of a historical snapshot record from its spec
// and labels, because snapshots do not store dependency edges.
func withSnapshotDependencies(rec *store.ComponentRecord, coords *componentspb.ResolvedComponentCoordinates, err error) (*store.ComponentRecord, *componentspb.ResolvedComponentCoordinates, error) {
	if err != nil {
		return nil, nil, err
	}
	rec.Dependencies = dependency.ExtractDependencies(rec.Spec, rec.Labels)
	return rec, coords, nil
}

// ValidateComponentRequirement checks if a required component type and version exists and is active.
func (s *Store) ValidateComponentRequirement(ctx context.Context, name string, reqType componentspb.ComponentType, minVersion string) (*store.RequirementValidationResult, error) {
	canonicalName, err := store.ValidateResourceName(name)
	if err != nil {
		return nil, err
	}
	if minVersion != "" {
		if _, err := semver.Parse(minVersion); err != nil {
			return nil, status.Errorf(codes.InvalidArgument, "invalid minimum version %q: %v", minVersion, err)
		}
	}

	rec, err := s.GetComponent(ctx, canonicalName)
	if err != nil {
		if status.Code(err) == codes.NotFound {
			return &store.RequirementValidationResult{
				IsSatisfied:             false,
				SatisfyingComponentName: canonicalName,
				Reason:                  fmt.Sprintf("component %q not found", canonicalName),
			}, nil
		}
		return nil, err
	}

	return lifecycle.EvaluateComponentRequirement(rec, canonicalName, reqType, minVersion)
}

// GetPlatformHealth retrieves overall system component health metrics and status.
func (s *Store) GetPlatformHealth(ctx context.Context, filter string, pageSize int32, pageToken string) (*componentspb.GetPlatformHealthResponse, error) {
	return nil, status.Error(codes.Unimplemented, "method GetPlatformHealth not implemented in Store")
}

func generateRandomID() (int64, error) {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return 0, err
	}
	id := int64(binary.BigEndian.Uint64(b[:]) & 0x7FFFFFFFFFFFFFFF)
	if id == 0 {
		id = 1
	}
	return id, nil
}
