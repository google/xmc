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

// Package spanner provides a Cloud Spanner implementation of store.Store.
package spanner

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"maps"
	"sort"
	"time"

	"cloud.google.com/go/spanner"
	"google.golang.org/api/iterator"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	componentspb "xmanager_cloud/api/protos/components/v1alpha1"
	"xmanager_cloud/internal/logging"
	"xmanager_cloud/internal/storage/spanner/component"
	"xmanager_cloud/pkg/components/naming"
	"xmanager_cloud/pkg/components/semver"
	"xmanager_cloud/pkg/components/store"
	"xmanager_cloud/pkg/components/store/dependency"
	"xmanager_cloud/pkg/components/store/lifecycle"
	"xmanager_cloud/pkg/components/store/ownership"
)

// Ensure SpannerStore implements Store interface at compile time.
var _ store.Store = (*SpannerStore)(nil)

const (
	selectComponentWithActiveDependenciesSQL = `SELECT ` + component.ComponentColumnsSQL + `,
  ARRAY(
    SELECT dep.Name
    FROM ComponentDependencies cd
    JOIN Components dep ON cd.DependencyComponentId = dep.ComponentId
    WHERE cd.ComponentId = c.ComponentId AND dep.ActiveName IS NOT NULL
    ORDER BY dep.Name ASC
  ) AS ActiveDependencies,
  ` + fenceColumnsSQL + `
FROM Components c
WHERE c.ActiveName = @name
LIMIT 1`

	selectAllActiveComponentsSQL = `SELECT ` + component.ComponentColumnsSQL + `
FROM Components
WHERE ActiveName IS NOT NULL
ORDER BY Name ASC`

	selectAllActiveDependenciesSQL = `SELECT cd.ComponentId, dep.Name
FROM ComponentDependencies cd
JOIN Components dep ON cd.DependencyComponentId = dep.ComponentId
JOIN Components parent ON cd.ComponentId = parent.ComponentId
WHERE parent.ActiveName IS NOT NULL AND dep.ActiveName IS NOT NULL
ORDER BY cd.ComponentId, dep.Name ASC`

	selectSnapshotByComponentIDAndGenSQL = `SELECT ComponentId, LockedGeneration, ConfigProto, AccessPayload
FROM ExperimentComponentSnapshots
WHERE ComponentId = @id AND LockedGeneration = @gen
LIMIT 1`

	selectSnapshotByNameAndGenSQL = `SELECT s.ComponentId, s.LockedGeneration, s.ConfigProto, s.AccessPayload, c.CreateTime, c.UpdateTime
FROM ExperimentComponentSnapshots s
JOIN Components c ON s.ComponentId = c.ComponentId
WHERE c.Name = @name AND s.LockedGeneration = @gen
ORDER BY c.CreateTime DESC, c.ComponentId DESC
LIMIT 1`
)

type spannerQueryable interface {
	Query(ctx context.Context, statement spanner.Statement) *spanner.RowIterator
}

// SpannerStore provides a Cloud Spanner backed production implementation of Store.
type SpannerStore struct {
	client *spanner.Client
	// TODO: Replace repo and the Spanner-specific component helpers with storage.Storage and
	// RunInTransaction once the remaining Spanner reads and transactions move out of this store.
	repo *component.Repository
	// beforeIntentWrite, when non-nil, runs before SetComponentIntent falls back to its read-write
	// transaction. Tests use it to count fallbacks and to inject concurrent writes.
	beforeIntentWrite func()
}

// NewSpannerStore creates a new Spanner-backed component store.
func NewSpannerStore(client *spanner.Client) *SpannerStore {
	return &SpannerStore{client: client, repo: component.NewRepository(client)}
}

// SetComponentIntent applies or updates component intent within Spanner. An unchanged intent whose
// dependencies are all registered and active is answered from a strong read-only read without a
// read-write transaction; anything else, including every changed or new intent (which therefore
// pays the extra read), is decided by the read-write transaction.
func (s *SpannerStore) SetComponentIntent(ctx context.Context, name string, spec *componentspb.ComponentSpec, labels map[string]string, desiredGen int64) (*store.ComponentRecord, error) {
	return s.setIntent(ctx, name, spec, labels, desiredGen, nil)
}

// SetSourcedComponentIntent applies an intent sent by src within Spanner, with the same read-only
// fast path as SetComponentIntent. Recording a new owner, and every rejection except a fence
// rejection seen by the fast-path read, is decided by the read-write transaction.
func (s *SpannerStore) SetSourcedComponentIntent(ctx context.Context, name string, spec *componentspb.ComponentSpec, labels map[string]string, src store.RequestSource) (*store.ComponentRecord, error) {
	if err := ownership.ValidateRequestSource(src); err != nil {
		return nil, err
	}
	return s.setIntent(ctx, name, spec, labels, 0, &src)
}

// setIntent applies an intent ordered by desiredGen when src is nil and by src otherwise.
func (s *SpannerStore) setIntent(ctx context.Context, name string, spec *componentspb.ComponentSpec, labels map[string]string, desiredGen int64, src *store.RequestSource) (*store.ComponentRecord, error) {
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
func (s *SpannerStore) readUnchangedIntent(ctx context.Context, canonicalName string, spec *componentspb.ComponentSpec, labels map[string]string, deps []string, desiredGen int64, src *store.RequestSource) (*store.ComponentRecord, bool, error) {
	rec, fence, err := queryComponentWithActiveDependencies(ctx, s.client.Single(), canonicalName)
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

// queryComponentWithActiveDependencies runs one query, so it can use a single-use read-only
// transaction, and returns the active record of canonicalName with Dependencies set to its
// registered, non-deleted dependencies (nil when there are none, as on the read-write path),
// together with the fence holder in the same snapshot (nil before the first declaration).
func queryComponentWithActiveDependencies(ctx context.Context, queryable spannerQueryable, canonicalName string) (*store.ComponentRecord, *store.ClusterIdentity, error) {
	iter := queryable.Query(ctx, spanner.Statement{SQL: selectComponentWithActiveDependenciesSQL, Params: map[string]any{"name": canonicalName}})
	defer iter.Stop()

	row, err := iter.Next()
	if errors.Is(err, iterator.Done) {
		return nil, nil, store.ErrComponentNotFound
	}
	if err != nil {
		return nil, nil, fmt.Errorf("failed to query component %q: %w", canonicalName, err)
	}
	rec, err := component.RowToComponentRecord(row)
	if err != nil {
		return nil, nil, err
	}
	if err := row.ColumnByName("ActiveDependencies", &rec.Dependencies); err != nil {
		return nil, nil, fmt.Errorf("failed to read dependencies of component %q: %w", canonicalName, err)
	}
	if len(rec.Dependencies) == 0 {
		rec.Dependencies = nil
	}
	fence, err := scanFenceColumns(row)
	if err != nil {
		return nil, nil, err
	}
	return rec, fence, nil
}

// writeIntent applies the intent in a read-write transaction. It returns the existing record, with
// any newly recorded owner, when the intent writes neither the spec nor the generation.
func (s *SpannerStore) writeIntent(ctx context.Context, canonicalName string, spec *componentspb.ComponentSpec, labels map[string]string, deps []string, desiredGen int64, src *store.RequestSource) (*store.ComponentRecord, error) {
	var finalRec *store.ComponentRecord
	var plan ownership.IntentPlan

	commitTimestamp, err := s.client.ReadWriteTransaction(ctx, func(ctx context.Context, txn *spanner.ReadWriteTransaction) error {
		// Assign both results on every attempt because the transaction body may be retried.
		var err error
		finalRec, plan, err = s.applyIntentTx(ctx, txn, canonicalName, spec, labels, deps, desiredGen, src)
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
func (s *SpannerStore) applyIntentTx(
	ctx context.Context,
	txn *spanner.ReadWriteTransaction,
	canonicalName string,
	spec *componentspb.ComponentSpec,
	labels map[string]string,
	deps []string,
	desiredGen int64,
	src *store.RequestSource,
) (*store.ComponentRecord, ownership.IntentPlan, error) {
	if err := checkFence(ctx, txn, store.OperationSetComponentIntent, src); err != nil {
		return nil, ownership.IntentPlan{}, err
	}
	catalog, err := s.loadActiveCatalog(ctx, txn)
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
		rec, err = s.updateExistingIntentTx(txn, existingRec, canonicalName, spec, labels, deps, plan.Generation, depIDs)
	default:
		rec, err = s.createNewIntentTx(txn, canonicalName, spec, labels, deps, plan.Generation, depIDs)
	}
	if err != nil {
		return nil, ownership.IntentPlan{}, err
	}
	return rec, plan, recordOwnerTx(txn, rec, src)
}

// recordOwnerTx buffers a write of src as the owner of rec, with SourceLastSeenTime at the commit
// timestamp, and sets rec.Owner. A nil src keeps the recorded owner. SourceLastSeenTime is the
// commit time of the last request accepted from the recorded owner; it only feeds owner-staleness
// observability and never decides ownership.
func recordOwnerTx(txn *spanner.ReadWriteTransaction, rec *store.ComponentRecord, src *store.RequestSource) error {
	if src == nil {
		return nil
	}
	return writeOwnerColumnsTx(txn, rec, store.OwnerOf(*src))
}

func recordNonIntentOwnerTx(txn *spanner.ReadWriteTransaction, rec *store.ComponentRecord, src *store.RequestSource) error {
	if src == nil {
		return nil
	}
	return writeOwnerColumnsTx(txn, rec, ownership.NonIntentOwner(rec.Owner, src))
}

// writeOwnerColumnsTx buffers a write of owner into rec's source columns, with SourceLastSeenTime
// at the commit timestamp, and sets rec.Owner. A nil owner is a no-op that keeps the recorded owner.
func writeOwnerColumnsTx(txn *spanner.ReadWriteTransaction, rec *store.ComponentRecord, owner *store.SourceOwner) error {
	if owner == nil {
		return nil
	}
	rec.Owner = owner
	cols := ownerColumns(owner)
	cols["ComponentId"] = rec.ComponentID
	return txn.BufferWrite([]*spanner.Mutation{spanner.UpdateMap("Components", cols)})
}

// ownerColumns returns the Components owner columns for owner, with SourceLastSeenTime at the
// commit timestamp.
func ownerColumns(owner *store.SourceOwner) map[string]any {
	return map[string]any{
		"SourceUid": owner.UID, "SourceGeneration": owner.Generation,
		"SourceNamespace": owner.Namespace, "SourceClusterUid": owner.ClusterUID, "SourceEpoch": owner.Epoch,
		"SourceLastSeenTime": spanner.CommitTimestamp,
	}
}

func (s *SpannerStore) updateExistingIntentTx(
	txn *spanner.ReadWriteTransaction,
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

	compProto := &componentspb.Component{
		Name:   naming.FullComponentName(canonicalName),
		Spec:   specClone,
		Labels: store.CopyMap(labels),
	}
	configBytes, err := proto.Marshal(compProto)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal component proto: %w", err)
	}

	var statusProto *componentspb.ComponentAccessStatus
	if existingRec.Status != nil {
		statusProto = proto.Clone(existingRec.Status).(*componentspb.ComponentAccessStatus)
	} else {
		statusProto = &componentspb.ComponentAccessStatus{}
	}
	statusProto.State = componentspb.ComponentState_COMPONENT_STATE_PENDING_RECONCILIATION
	accessBytes, err := proto.Marshal(statusProto)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal access status proto: %w", err)
	}

	compClass := determineComponentClass(specClone)
	updateMap := map[string]any{
		"ComponentId":    existingRec.ComponentID,
		"ComponentClass": spanner.NullString{StringVal: compClass, Valid: compClass != ""},
		"ConfigProto":    configBytes,
		"AccessPayload":  accessBytes,
		"State":          int64(componentspb.ComponentState_COMPONENT_STATE_PENDING_RECONCILIATION),
		"Generation":     newGen,
		"UpdateTime":     spanner.CommitTimestamp,
	}

	mutations := []*spanner.Mutation{
		spanner.UpdateMap("Components", updateMap),
		spanner.Delete("ComponentDependencies", spanner.KeyRange{
			Start: spanner.Key{existingRec.ComponentID},
			End:   spanner.Key{existingRec.ComponentID},
			Kind:  spanner.ClosedClosed,
		}),
	}

	for _, dep := range deps {
		mutations = append(mutations, spanner.Insert(
			"ComponentDependencies",
			[]string{"ComponentId", "DependencyComponentId"},
			[]any{existingRec.ComponentID, depIDs[dep]},
		))
	}

	if err := txn.BufferWrite(mutations); err != nil {
		return nil, err
	}

	return &store.ComponentRecord{
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
	}, nil
}

func (s *SpannerStore) createNewIntentTx(
	txn *spanner.ReadWriteTransaction,
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

	compProto := &componentspb.Component{
		Name:   naming.FullComponentName(canonicalName),
		Spec:   specClone,
		Labels: store.CopyMap(labels),
	}
	configBytes, err := proto.Marshal(compProto)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal component proto: %w", err)
	}

	statusProto := &componentspb.ComponentAccessStatus{
		State: componentspb.ComponentState_COMPONENT_STATE_PENDING_RECONCILIATION,
	}
	accessBytes, err := proto.Marshal(statusProto)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal access status proto: %w", err)
	}

	compClass := determineComponentClass(specClone)
	insertCols := []string{"ComponentId", "Name", "ComponentClass", "ConfigProto", "AccessPayload", "State", "Generation", "CreateTime", "UpdateTime"}
	insertVals := []any{id, canonicalName, spanner.NullString{StringVal: compClass, Valid: compClass != ""}, configBytes, accessBytes, int64(componentspb.ComponentState_COMPONENT_STATE_PENDING_RECONCILIATION), gen, spanner.CommitTimestamp, spanner.CommitTimestamp}

	mutations := []*spanner.Mutation{spanner.Insert("Components", insertCols, insertVals)}
	for _, dep := range deps {
		mutations = append(mutations, spanner.Insert(
			"ComponentDependencies",
			[]string{"ComponentId", "DependencyComponentId"},
			[]any{id, depIDs[dep]},
		))
	}

	if err := txn.BufferWrite(mutations); err != nil {
		return nil, err
	}

	return &store.ComponentRecord{
		ComponentID:  id,
		Name:         canonicalName,
		Spec:         specClone,
		Status:       statusProto,
		State:        componentspb.ComponentState_COMPONENT_STATE_PENDING_RECONCILIATION,
		Generation:   gen,
		Dependencies: deps,
		Labels:       store.CopyMap(labels),
	}, nil
}

// GetComponent retrieves a component record by canonical or full resource name.
func (s *SpannerStore) GetComponent(ctx context.Context, name string) (*store.ComponentRecord, error) {
	canonicalName, err := store.ValidateResourceName(name)
	if err != nil {
		return nil, err
	}

	rec, err := s.repo.GetActiveComponent(ctx, canonicalName)
	if err != nil {
		return nil, err
	}

	return store.CloneRecord(rec), nil
}

// GetComponentByID retrieves a component record by its unique database identifier.
func (s *SpannerStore) GetComponentByID(ctx context.Context, componentID int64) (*store.ComponentRecord, error) {
	if componentID <= 0 {
		return nil, status.Error(codes.InvalidArgument, "invalid component ID")
	}

	rec, err := s.repo.GetComponentByID(ctx, componentID)
	if err != nil {
		return nil, err
	}

	return store.CloneRecord(rec), nil
}

// ListComponents returns all non-deleted components in the catalog sorted by name.
func (s *SpannerStore) ListComponents(ctx context.Context) ([]*store.ComponentRecord, error) {
	txn := s.client.ReadOnlyTransaction()
	defer txn.Close()

	catalog, err := s.loadActiveCatalog(ctx, txn)
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
func (s *SpannerStore) PrepareDeactivation(ctx context.Context, name string, requestCascade bool, src ...store.RequestSource) (*store.PrepareDeactivationResult, error) {
	canonicalName, reqSrc, err := ownership.ValidateSourcedResourceArgs(name, src)
	if err != nil {
		return nil, err
	}

	var result *store.PrepareDeactivationResult
	var prevOwner *store.SourceOwner

	_, err = s.client.ReadWriteTransaction(ctx, func(ctx context.Context, txn *spanner.ReadWriteTransaction) error {
		catalog, targetRec, err := s.lookupCatalogTargetTx(ctx, txn, store.OperationPrepareDeactivation, canonicalName, reqSrc)
		if err != nil {
			return err
		}

		res, err := lifecycle.EvaluateDeactivationSafety(catalog, canonicalName, requestCascade, lifecycle.MinDrainTimeout)
		if err != nil {
			return err
		}
		result = res

		toTransition := collectDeactivationTargets(targetRec, catalog, res.CascadedDeactivations, requestCascade)
		mutations, err := buildDisablingMutations(toTransition, time.Now().UTC())
		if err != nil {
			return err
		}

		if len(mutations) > 0 {
			if err := txn.BufferWrite(mutations); err != nil {
				return err
			}
		}
		prevOwner = store.CloneSourceOwner(targetRec.Owner)
		return recordNonIntentOwnerTx(txn, targetRec, reqSrc)
	})

	if err != nil {
		return nil, ownership.RecordSourceConflictError(err)
	}
	ownership.RecordOwnerAdoption(prevOwner, reqSrc, false)
	return result, nil
}

// lookupCatalogTargetTx checks the cluster fence, loads the active catalog, finds canonicalName,
// and checks source ownership for operation.
func (s *SpannerStore) lookupCatalogTargetTx(ctx context.Context, txn *spanner.ReadWriteTransaction, operation, canonicalName string, src *store.RequestSource) (map[string]*store.ComponentRecord, *store.ComponentRecord, error) {
	if err := checkFence(ctx, txn, operation, src); err != nil {
		return nil, nil, err
	}
	catalog, err := s.loadActiveCatalog(ctx, txn)
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

func buildDisablingMutations(toTransition []*store.ComponentRecord, now time.Time) ([]*spanner.Mutation, error) {
	var mutations []*spanner.Mutation
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

		accessBytes, err := proto.Marshal(statusProto)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal access status proto: %w", err)
		}

		mutations = append(mutations, spanner.UpdateMap("Components", map[string]any{
			"ComponentId":   rec.ComponentID,
			"State":         int64(componentspb.ComponentState_COMPONENT_STATE_DISABLING),
			"AccessPayload": accessBytes,
			"UpdateTime":    spanner.CommitTimestamp,
		}))
	}
	return mutations, nil
}

// CompleteDeactivation marks a component in DISABLING state as DISABLED.
func (s *SpannerStore) CompleteDeactivation(ctx context.Context, name string, force bool, src ...store.RequestSource) error {
	canonicalName, reqSrc, err := ownership.ValidateSourcedResourceArgs(name, src)
	if err != nil {
		return err
	}

	var prevOwner *store.SourceOwner
	_, err = s.client.ReadWriteTransaction(ctx, func(ctx context.Context, txn *spanner.ReadWriteTransaction) error {
		catalog, rec, err := s.lookupCatalogTargetTx(ctx, txn, store.OperationCompleteDeactivation, canonicalName, reqSrc)
		if err != nil {
			return err
		}

		if rec.State == componentspb.ComponentState_COMPONENT_STATE_DISABLED {
			prevOwner = store.CloneSourceOwner(rec.Owner)
			return recordNonIntentOwnerTx(txn, rec, reqSrc)
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

		accessBytes, err := proto.Marshal(statusProto)
		if err != nil {
			return fmt.Errorf("failed to marshal access status proto: %w", err)
		}

		mutation := spanner.UpdateMap("Components", map[string]any{
			"ComponentId":   rec.ComponentID,
			"State":         int64(componentspb.ComponentState_COMPONENT_STATE_DISABLED),
			"AccessPayload": accessBytes,
			"UpdateTime":    spanner.CommitTimestamp,
		})
		if err := txn.BufferWrite([]*spanner.Mutation{mutation}); err != nil {
			return err
		}
		prevOwner = store.CloneSourceOwner(rec.Owner)
		return recordNonIntentOwnerTx(txn, rec, reqSrc)
	})

	if err != nil {
		return ownership.RecordSourceConflictError(err)
	}
	ownership.RecordOwnerAdoption(prevOwner, reqSrc, false)
	return nil
}

// UpdateObservedGeneration synchronizes actual component access, generation, and state.
func (s *SpannerStore) UpdateObservedGeneration(ctx context.Context, name string, observedGen int64, access *componentspb.ComponentAccessStatus, state componentspb.ComponentState, statusMsg string, src ...store.RequestSource) (*store.ComponentRecord, error) {
	canonicalName, reqSrc, err := ownership.ValidateUpdateObservedArgs(name, observedGen, src)
	if err != nil {
		return nil, err
	}

	var finalRec *store.ComponentRecord
	var unchanged bool
	var prevOwner *store.SourceOwner

	commitTimestamp, err := s.client.ReadWriteTransaction(ctx, func(ctx context.Context, txn *spanner.ReadWriteTransaction) error {
		unchanged = false
		if err := checkFence(ctx, txn, store.OperationUpdateObservedGeneration, reqSrc); err != nil {
			return err
		}
		rec, err := s.repo.WithTx(txn).GetActiveComponent(ctx, canonicalName)
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
			if err := writeObservationTx(txn, rec, observedGen, state); err != nil {
				return err
			}
		}
		prevOwner = store.CloneSourceOwner(rec.Owner)
		if err := recordNonIntentOwnerTx(txn, rec, reqSrc); err != nil {
			return err
		}

		finalRec = store.CloneRecord(rec)
		return nil
	})

	if err != nil {
		return nil, ownership.RecordSourceConflictError(err)
	}
	ownership.RecordOwnerAdoption(prevOwner, reqSrc, false)

	if finalRec == nil {
		return nil, nil
	}
	if reqSrc != nil {
		finalRec.SourceLastSeenTime = commitTimestamp
	}
	if !unchanged {
		finalRec.UpdateTime = commitTimestamp
		finalRec.Etag = store.GenerateEtag(finalRec)
	}

	return store.CloneRecord(finalRec), nil
}

// writeObservationTx buffers a write of rec's observed generation, access status and, unless
// state is UNSPECIFIED, state, with UpdateTime at the commit timestamp.
func writeObservationTx(txn *spanner.ReadWriteTransaction, rec *store.ComponentRecord, observedGen int64, state componentspb.ComponentState) error {
	var accessBytes []byte
	if rec.Status != nil {
		bytes, err := proto.Marshal(rec.Status)
		if err != nil {
			return fmt.Errorf("failed to marshal access status proto: %w", err)
		}
		accessBytes = bytes
	}

	updateMap := map[string]any{
		"ComponentId":        rec.ComponentID,
		"ObservedGeneration": observedGen,
		"AccessPayload":      accessBytes,
		"UpdateTime":         spanner.CommitTimestamp,
	}
	if state != componentspb.ComponentState_COMPONENT_STATE_UNSPECIFIED {
		updateMap["State"] = int64(state)
	}
	return txn.BufferWrite([]*spanner.Mutation{spanner.UpdateMap("Components", updateMap)})
}

// ExecuteDeletion deletes a component using the specified deletion policy.
func (s *SpannerStore) ExecuteDeletion(ctx context.Context, name string, policy componentspb.DeletionPolicy, force bool, src ...store.RequestSource) error {
	canonicalName, reqSrc, err := ownership.ValidateSourcedResourceArgs(name, src)
	if err != nil {
		return err
	}

	var prevOwner *store.SourceOwner
	_, err = s.client.ReadWriteTransaction(ctx, func(ctx context.Context, txn *spanner.ReadWriteTransaction) error {
		catalog, rec, err := s.lookupCatalogTargetTx(ctx, txn, store.OperationExecuteDeletion, canonicalName, reqSrc)
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
		mutations, err := buildDeletionMutations(toDelete, policy, time.Now().UTC(), owner)
		if err != nil {
			return err
		}
		return txn.BufferWrite(mutations)
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

// buildDeletionMutations returns one Components update per target. A non-nil targetOwner is merged
// into the mutation of toDelete[0] (the requested deletion target) so the row is written once.
func buildDeletionMutations(toDelete []*store.ComponentRecord, policy componentspb.DeletionPolicy, now time.Time, targetOwner *store.SourceOwner) ([]*spanner.Mutation, error) {
	mutations := make([]*spanner.Mutation, 0, len(toDelete))
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

		compProto := &componentspb.Component{
			Name:   naming.FullComponentName(target.Name),
			Spec:   target.Spec,
			Labels: store.CopyMap(target.Labels),
		}
		configBytes, err := proto.Marshal(compProto)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal component proto: %w", err)
		}

		accessBytes, err := proto.Marshal(target.Status)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal access status proto: %w", err)
		}

		cols := map[string]any{
			"ComponentId":   target.ComponentID,
			"ConfigProto":   configBytes,
			"AccessPayload": accessBytes,
			"State":         int64(componentspb.ComponentState_COMPONENT_STATE_DELETED),
			"UpdateTime":    spanner.CommitTimestamp,
		}
		if i == 0 && targetOwner != nil {
			maps.Copy(cols, ownerColumns(targetOwner))
		}
		mutations = append(mutations, spanner.UpdateMap("Components", cols))
	}
	return mutations, nil
}

// ResolveComponentSnapshot resolves or creates an immutable snapshot of a component at generation.
func (s *SpannerStore) ResolveComponentSnapshot(ctx context.Context, name string, generation int64) (*store.ComponentRecord, *componentspb.ResolvedComponentCoordinates, error) {
	canonicalName, err := store.ValidateResourceName(name)
	if err != nil {
		return nil, nil, err
	}
	if generation < 0 {
		return nil, nil, status.Error(codes.InvalidArgument, "generation cannot be negative")
	}

	txn := s.client.ReadOnlyTransaction()
	defer txn.Close()

	rec, err := s.repo.WithTx(txn).GetActiveComponent(ctx, canonicalName)
	if err != nil {
		if errors.Is(err, store.ErrComponentNotFound) && generation > 0 {
			return s.resolveHistoricalSnapshotByName(ctx, txn, canonicalName, generation)
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
		snapRec, coords, err := s.resolveHistoricalSnapshotByName(ctx, txn, canonicalName, targetGen)
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

	return s.resolveHistoricalSnapshot(ctx, txn, rec, targetGen, canonicalName)
}

// snapshotRowData holds the raw ExperimentComponentSnapshots column values read from a query row.
type snapshotRowData struct {
	componentID   int64
	lockedGen     int64
	configProto   []byte
	accessPayload []byte
}

// querySnapshotRow executes stmt and returns the first matching ExperimentComponentSnapshots row.
// It returns a NotFound status error when no snapshot exists for the requested generation.
func querySnapshotRow(ctx context.Context, queryable spannerQueryable, stmt spanner.Statement, canonicalName string, targetGen int64) (*spanner.Row, error) {
	iter := queryable.Query(ctx, stmt)
	defer iter.Stop()

	row, err := iter.Next()
	if err != nil {
		if errors.Is(err, iterator.Done) {
			return nil, status.Errorf(codes.NotFound, "historical snapshot for generation %d of component %s not found", targetGen, canonicalName)
		}
		return nil, fmt.Errorf("failed to query experiment component snapshots: %w", err)
	}
	return row, nil
}

// scanSnapshotRow reads the ExperimentComponentSnapshots columns shared by all snapshot queries.
func scanSnapshotRow(row *spanner.Row) (*snapshotRowData, error) {
	var d snapshotRowData
	if err := row.ColumnByName("ComponentId", &d.componentID); err != nil {
		return nil, fmt.Errorf("failed to read ComponentId: %w", err)
	}
	if err := row.ColumnByName("LockedGeneration", &d.lockedGen); err != nil {
		return nil, fmt.Errorf("failed to read LockedGeneration: %w", err)
	}
	if err := row.ColumnByName("ConfigProto", &d.configProto); err != nil {
		return nil, fmt.Errorf("failed to read ConfigProto: %w", err)
	}
	if err := row.ColumnByName("AccessPayload", &d.accessPayload); err != nil {
		return nil, fmt.Errorf("failed to read AccessPayload: %w", err)
	}
	return &d, nil
}

// snapshotRowToRecord hydrates a historical ComponentRecord and its coordinates from a scanned
// snapshot row. Timestamps are supplied by the caller because snapshots do not store their own;
// the record is built from scratch so no active component state leaks into the historical view.
func snapshotRowToRecord(d *snapshotRowData, canonicalName string, createTime, updateTime time.Time) (*store.ComponentRecord, *componentspb.ResolvedComponentCoordinates, error) {
	snapRec := &store.ComponentRecord{
		ComponentID: d.componentID,
		Name:        canonicalName,
		Generation:  d.lockedGen,
		CreateTime:  createTime,
		UpdateTime:  updateTime,
	}
	if err := component.UnmarshalConfigProto(d.configProto, snapRec); err != nil {
		return nil, nil, fmt.Errorf("failed to unmarshal historical config proto: %w", err)
	}
	if err := component.UnmarshalAccessStatus(d.accessPayload, snapRec); err != nil {
		return nil, nil, fmt.Errorf("failed to unmarshal historical access status: %w", err)
	}
	if snapRec.Status != nil {
		snapRec.State = snapRec.Status.State
		obsGen := snapRec.Status.ObservedGeneration
		snapRec.ObservedGeneration = &obsGen
	}
	snapRec.Dependencies = dependency.ExtractDependencies(snapRec.Spec, snapRec.Labels)
	snapRec.Etag = store.GenerateEtag(snapRec)

	var coords *componentspb.ResolvedComponentCoordinates
	if snapRec.Status != nil && snapRec.Status.Coordinates != nil {
		coords = store.CloneCoordinates(snapRec.Status.Coordinates)
	}
	return snapRec, coords, nil
}

// resolveHistoricalSnapshot resolves a snapshot of an active component by its ComponentId,
// inheriting the timestamps of the owning active record.
func (s *SpannerStore) resolveHistoricalSnapshot(ctx context.Context, queryable spannerQueryable, rec *store.ComponentRecord, targetGen int64, canonicalName string) (*store.ComponentRecord, *componentspb.ResolvedComponentCoordinates, error) {
	stmt := spanner.Statement{
		SQL: selectSnapshotByComponentIDAndGenSQL,
		Params: map[string]any{
			"id":  rec.ComponentID,
			"gen": targetGen,
		},
	}
	row, err := querySnapshotRow(ctx, queryable, stmt, canonicalName, targetGen)
	if err != nil {
		return nil, nil, err
	}
	d, err := scanSnapshotRow(row)
	if err != nil {
		return nil, nil, err
	}
	return snapshotRowToRecord(d, canonicalName, rec.CreateTime, rec.UpdateTime)
}

// resolveHistoricalSnapshotByName resolves a snapshot by component name, covering soft-deleted or
// recreated incarnations whose active record is unavailable. Timestamps come from the joined
// Components row of the incarnation that owns the snapshot.
func (s *SpannerStore) resolveHistoricalSnapshotByName(ctx context.Context, queryable spannerQueryable, canonicalName string, targetGen int64) (*store.ComponentRecord, *componentspb.ResolvedComponentCoordinates, error) {
	stmt := spanner.Statement{
		SQL: selectSnapshotByNameAndGenSQL,
		Params: map[string]any{
			"name": canonicalName,
			"gen":  targetGen,
		},
	}
	row, err := querySnapshotRow(ctx, queryable, stmt, canonicalName, targetGen)
	if err != nil {
		return nil, nil, err
	}
	d, err := scanSnapshotRow(row)
	if err != nil {
		return nil, nil, err
	}

	var createTime, updateTime time.Time
	if err := row.ColumnByName("CreateTime", &createTime); err != nil {
		return nil, nil, fmt.Errorf("failed to read CreateTime: %w", err)
	}
	if err := row.ColumnByName("UpdateTime", &updateTime); err != nil {
		return nil, nil, fmt.Errorf("failed to read UpdateTime: %w", err)
	}
	return snapshotRowToRecord(d, canonicalName, createTime, updateTime)
}

// ValidateComponentRequirement checks if a required component type and version exists and is active.
func (s *SpannerStore) ValidateComponentRequirement(ctx context.Context, name string, reqType componentspb.ComponentType, minVersion string) (*store.RequirementValidationResult, error) {
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
func (s *SpannerStore) GetPlatformHealth(ctx context.Context, filter string, pageSize int32, pageToken string) (*componentspb.GetPlatformHealthResponse, error) {
	return nil, status.Error(codes.Unimplemented, "method GetPlatformHealth not implemented in SpannerStore")
}

func (s *SpannerStore) loadActiveCatalog(ctx context.Context, queryable spannerQueryable) (map[string]*store.ComponentRecord, error) {
	compIter := queryable.Query(ctx, spanner.Statement{SQL: selectAllActiveComponentsSQL})
	defer compIter.Stop()

	componentsByName := make(map[string]*store.ComponentRecord)
	componentsByID := make(map[int64]*store.ComponentRecord)

	if err := compIter.Do(func(row *spanner.Row) error {
		rec, err := component.RowToComponentRecord(row)
		if err != nil {
			return err
		}
		componentsByName[rec.Name] = rec
		componentsByID[rec.ComponentID] = rec
		return nil
	}); err != nil {
		return nil, fmt.Errorf("failed to list active components: %w", err)
	}

	depIter := queryable.Query(ctx, spanner.Statement{SQL: selectAllActiveDependenciesSQL})
	defer depIter.Stop()

	if err := depIter.Do(func(row *spanner.Row) error {
		var (
			compID  int64
			depName string
		)
		if err := row.ColumnByName("ComponentId", &compID); err != nil {
			return err
		}
		if err := row.ColumnByName("Name", &depName); err != nil {
			return err
		}
		if rec, ok := componentsByID[compID]; ok {
			rec.Dependencies = append(rec.Dependencies, depName)
		}
		return nil
	}); err != nil {
		return nil, fmt.Errorf("failed to list component dependencies: %w", err)
	}

	for _, rec := range componentsByName {
		rec.Etag = store.GenerateEtag(rec)
	}

	return componentsByName, nil
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

func determineComponentClass(spec *componentspb.ComponentSpec) string {
	if spec == nil {
		return ""
	}
	if spec.GetType() == componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_3P_WRAPPER && spec.GetWrapperRef() != nil && spec.GetWrapperRef().GetCrdKind() != "" {
		return spec.GetWrapperRef().GetCrdKind()
	}
	if spec.GetType() != componentspb.ComponentType_COMPONENT_TYPE_UNSPECIFIED {
		return spec.GetType().String()
	}
	return ""
}
