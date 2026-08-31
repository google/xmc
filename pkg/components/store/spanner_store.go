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

package store

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"fmt"
	"sort"
	"time"

	"cloud.google.com/go/spanner"
	"google.golang.org/api/iterator"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	componentspb "xmanager_cloud/api/protos/components/v1alpha1"
)

// Ensure SpannerStore implements Store interface at compile time.
var _ Store = (*SpannerStore)(nil)

const (
	selectComponentByActiveNameSQL = `SELECT ComponentId, Name, ComponentClass, ConfigProto, AccessPayload, State, Generation, ObservedGeneration, CreateTime, UpdateTime
FROM Components
WHERE ActiveName = @name
LIMIT 1`

	selectComponentByIDSQL = `SELECT ComponentId, Name, ComponentClass, ConfigProto, AccessPayload, State, Generation, ObservedGeneration, CreateTime, UpdateTime
FROM Components
WHERE ComponentId = @id AND State != 5
LIMIT 1`

	selectAllActiveComponentsSQL = `SELECT ComponentId, Name, ComponentClass, ConfigProto, AccessPayload, State, Generation, ObservedGeneration, CreateTime, UpdateTime
FROM Components
WHERE ActiveName IS NOT NULL
ORDER BY Name ASC`

	selectDependenciesByComponentIDSQL = `SELECT dep.Name
FROM ComponentDependencies cd
JOIN Components dep ON cd.DependencyComponentId = dep.ComponentId
WHERE cd.ComponentId = @id
ORDER BY dep.Name ASC`

	selectAllActiveDependenciesSQL = `SELECT cd.ComponentId, dep.Name
FROM ComponentDependencies cd
JOIN Components dep ON cd.DependencyComponentId = dep.ComponentId
JOIN Components parent ON cd.ComponentId = parent.ComponentId
WHERE parent.ActiveName IS NOT NULL
ORDER BY cd.ComponentId, dep.Name ASC`

	selectSnapshotByComponentIDAndGenSQL = `SELECT ComponentId, LockedGeneration, ConfigProto, AccessPayload
FROM ExperimentComponentSnapshots
WHERE ComponentId = @id AND LockedGeneration = @gen
LIMIT 1`
)

type spannerQueryable interface {
	Query(ctx context.Context, statement spanner.Statement) *spanner.RowIterator
}

// SpannerStore provides a Cloud Spanner backed production implementation of Store.
type SpannerStore struct {
	client *spanner.Client
}

// NewSpannerStore creates a new Spanner-backed component store.
func NewSpannerStore(client *spanner.Client) *SpannerStore {
	return &SpannerStore{
		client: client,
	}
}

// SetComponentIntent applies or updates component intent within Spanner.
func (s *SpannerStore) SetComponentIntent(ctx context.Context, name string, spec *componentspb.ComponentSpec, labels map[string]string, desiredGen int64) (*ComponentRecord, error) {
	canonicalName, err := validateIntentArgs(name, spec)
	if err != nil {
		return nil, err
	}

	deps := ExtractDependencies(spec, labels)
	var finalRec *ComponentRecord

	commitTimestamp, err := s.client.ReadWriteTransaction(ctx, func(ctx context.Context, txn *spanner.ReadWriteTransaction) error {
		catalog, err := s.loadActiveCatalog(ctx, txn)
		if err != nil {
			return err
		}

		if err := ValidateProspectiveDependenciesFromMap(catalog, canonicalName, deps); err != nil {
			return err
		}

		depIDs := make(map[string]int64, len(deps))
		for _, dep := range deps {
			depRec, ok := catalog[dep]
			if !ok || isDeletedRecord(depRec) {
				return ErrDependencyNotFound
			}
			depIDs[dep] = depRec.ComponentID
		}

		existingRec, exists := catalog[canonicalName]
		if exists && !isDeletedRecord(existingRec) {
			rec, err := s.updateExistingIntentTx(txn, existingRec, canonicalName, spec, labels, deps, desiredGen, depIDs)
			if err != nil {
				return err
			}
			finalRec = rec
			return nil
		}

		rec, err := s.createNewIntentTx(txn, canonicalName, spec, labels, deps, desiredGen, depIDs)
		if err != nil {
			return err
		}
		finalRec = rec
		return nil
	})

	if err != nil {
		return nil, err
	}

	if finalRec != nil {
		if finalRec.CreateTime.IsZero() {
			finalRec.CreateTime = commitTimestamp
		}
		finalRec.UpdateTime = commitTimestamp
		finalRec.Etag = generateEtag(finalRec)
	}

	return cloneRecord(finalRec), nil
}

func (s *SpannerStore) updateExistingIntentTx(
	txn *spanner.ReadWriteTransaction,
	existingRec *ComponentRecord,
	canonicalName string,
	spec *componentspb.ComponentSpec,
	labels map[string]string,
	deps []string,
	desiredGen int64,
	depIDs map[string]int64,
) (*ComponentRecord, error) {
	newGen := existingRec.Generation + 1
	if desiredGen > existingRec.Generation {
		newGen = desiredGen
	}

	specClone := proto.Clone(spec).(*componentspb.ComponentSpec)
	specClone.Generation = newGen

	compProto := &componentspb.Component{
		Name:   FullComponentName(canonicalName),
		Spec:   specClone,
		Labels: copyMap(labels),
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

	return &ComponentRecord{
		ComponentID:        existingRec.ComponentID,
		Name:               canonicalName,
		Spec:               specClone,
		Status:             statusProto,
		State:              componentspb.ComponentState_COMPONENT_STATE_PENDING_RECONCILIATION,
		Generation:         newGen,
		ObservedGeneration: existingRec.ObservedGeneration,
		Dependencies:       deps,
		CreateTime:         existingRec.CreateTime,
		Labels:             copyMap(labels),
	}, nil
}

func (s *SpannerStore) createNewIntentTx(
	txn *spanner.ReadWriteTransaction,
	canonicalName string,
	spec *componentspb.ComponentSpec,
	labels map[string]string,
	deps []string,
	desiredGen int64,
	depIDs map[string]int64,
) (*ComponentRecord, error) {
	id, err := generateRandomID()
	if err != nil {
		return nil, fmt.Errorf("failed to generate component ID: %w", err)
	}

	gen := int64(1)
	if desiredGen > 1 {
		gen = desiredGen
	}

	specClone := proto.Clone(spec).(*componentspb.ComponentSpec)
	specClone.Generation = gen

	compProto := &componentspb.Component{
		Name:   FullComponentName(canonicalName),
		Spec:   specClone,
		Labels: copyMap(labels),
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

	return &ComponentRecord{
		ComponentID:  id,
		Name:         canonicalName,
		Spec:         specClone,
		Status:       statusProto,
		State:        componentspb.ComponentState_COMPONENT_STATE_PENDING_RECONCILIATION,
		Generation:   gen,
		Dependencies: deps,
		Labels:       copyMap(labels),
	}, nil
}

// GetComponent retrieves a component record by canonical or full resource name.
func (s *SpannerStore) GetComponent(ctx context.Context, name string) (*ComponentRecord, error) {
	canonicalName, err := validateResourceName(name)
	if err != nil {
		return nil, err
	}

	txn := s.client.ReadOnlyTransaction()
	defer txn.Close()

	stmt := spanner.Statement{
		SQL: selectComponentByActiveNameSQL,
		Params: map[string]any{
			"name": canonicalName,
		},
	}

	iter := txn.Query(ctx, stmt)
	defer iter.Stop()

	row, err := iter.Next()
	if err != nil {
		if err == iterator.Done {
			return nil, ErrComponentNotFound
		}
		return nil, fmt.Errorf("failed to query component %q: %w", canonicalName, err)
	}

	rec, err := rowToComponentRecord(row)
	if err != nil {
		return nil, err
	}

	depStmt := spanner.Statement{
		SQL: selectDependenciesByComponentIDSQL,
		Params: map[string]any{
			"id": rec.ComponentID,
		},
	}
	depIter := txn.Query(ctx, depStmt)
	defer depIter.Stop()

	deps, err := fetchDependencies(depIter)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch dependencies for component %q: %w", canonicalName, err)
	}
	rec.Dependencies = deps
	rec.Etag = generateEtag(rec)

	return cloneRecord(rec), nil
}

// GetComponentByID retrieves a component record by its unique database identifier.
func (s *SpannerStore) GetComponentByID(ctx context.Context, componentID int64) (*ComponentRecord, error) {
	if componentID <= 0 {
		return nil, status.Error(codes.InvalidArgument, "invalid component ID")
	}

	txn := s.client.ReadOnlyTransaction()
	defer txn.Close()

	stmt := spanner.Statement{
		SQL: selectComponentByIDSQL,
		Params: map[string]any{
			"id": componentID,
		},
	}

	iter := txn.Query(ctx, stmt)
	defer iter.Stop()

	row, err := iter.Next()
	if err != nil {
		if err == iterator.Done {
			return nil, ErrComponentNotFound
		}
		return nil, fmt.Errorf("failed to query component by ID %d: %w", componentID, err)
	}

	rec, err := rowToComponentRecord(row)
	if err != nil {
		return nil, err
	}

	depStmt := spanner.Statement{
		SQL: selectDependenciesByComponentIDSQL,
		Params: map[string]any{
			"id": rec.ComponentID,
		},
	}
	depIter := txn.Query(ctx, depStmt)
	defer depIter.Stop()

	deps, err := fetchDependencies(depIter)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch dependencies for component ID %d: %w", componentID, err)
	}
	rec.Dependencies = deps
	rec.Etag = generateEtag(rec)

	return cloneRecord(rec), nil
}

// ListComponents returns all non-deleted components in the catalog sorted by name.
func (s *SpannerStore) ListComponents(ctx context.Context) ([]*ComponentRecord, error) {
	txn := s.client.ReadOnlyTransaction()
	defer txn.Close()

	catalog, err := s.loadActiveCatalog(ctx, txn)
	if err != nil {
		return nil, err
	}

	results := make([]*ComponentRecord, 0, len(catalog))
	for _, rec := range catalog {
		if !isDeletedRecord(rec) {
			results = append(results, cloneRecord(rec))
		}
	}

	sort.Slice(results, func(i, j int) bool {
		return results[i].Name < results[j].Name
	})

	return results, nil
}

// PrepareDeactivation checks deactivation safety and transitions target (and cascaded dependents) to DISABLING.
func (s *SpannerStore) PrepareDeactivation(ctx context.Context, name string, requestCascade bool) (*PrepareDeactivationResult, error) {
	canonicalName, err := validateResourceName(name)
	if err != nil {
		return nil, err
	}

	var result *PrepareDeactivationResult

	_, err = s.client.ReadWriteTransaction(ctx, func(ctx context.Context, txn *spanner.ReadWriteTransaction) error {
		catalog, err := s.loadActiveCatalog(ctx, txn)
		if err != nil {
			return err
		}

		targetRec, ok := catalog[canonicalName]
		if !ok || targetRec == nil || isDeletedRecord(targetRec) {
			return ErrComponentNotFound
		}

		res, err := EvaluateDeactivationSafety(catalog, canonicalName, requestCascade, minDrainTimeout)
		if err != nil {
			if status.Code(err) == codes.FailedPrecondition && res != nil && len(res.BlockingDependents) > 0 {
				return status.Errorf(codes.FailedPrecondition, "component %s has active dependents: %v", canonicalName, res.BlockingDependents)
			}
			return err
		}
		result = res

		toTransition := collectDeactivationTargets(targetRec, catalog, res.CascadedDeactivations, requestCascade)
		mutations, err := buildDisablingMutations(toTransition, time.Now().UTC())
		if err != nil {
			return err
		}

		if len(mutations) > 0 {
			return txn.BufferWrite(mutations)
		}
		return nil
	})

	if err != nil {
		return nil, err
	}
	return result, nil
}

func collectDeactivationTargets(targetRec *ComponentRecord, catalog map[string]*ComponentRecord, cascaded []string, requestCascade bool) []*ComponentRecord {
	targets := []*ComponentRecord{targetRec}
	if !requestCascade {
		return targets
	}
	for _, depName := range cascaded {
		if depRec, ok := catalog[depName]; ok && depRec != nil && !isDeletedRecord(depRec) {
			targets = append(targets, depRec)
		}
	}
	return targets
}

func buildDisablingMutations(toTransition []*ComponentRecord, now time.Time) ([]*spanner.Mutation, error) {
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
func (s *SpannerStore) CompleteDeactivation(ctx context.Context, name string, force bool) error {
	canonicalName, err := validateResourceName(name)
	if err != nil {
		return err
	}

	_, err = s.client.ReadWriteTransaction(ctx, func(ctx context.Context, txn *spanner.ReadWriteTransaction) error {
		catalog, err := s.loadActiveCatalog(ctx, txn)
		if err != nil {
			return err
		}

		rec, ok := catalog[canonicalName]
		if !ok || rec == nil || isDeletedRecord(rec) {
			return ErrComponentNotFound
		}

		if rec.State == componentspb.ComponentState_COMPONENT_STATE_DISABLED {
			return nil
		}
		if rec.State != componentspb.ComponentState_COMPONENT_STATE_DISABLING {
			return ErrInvalidStateTransition
		}

		if !force {
			activeDependents, _ := findActiveDependents(catalog, canonicalName)
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
		return txn.BufferWrite([]*spanner.Mutation{mutation})
	})

	return err
}

// UpdateObservedGeneration synchronizes actual component access, generation, and state.
func (s *SpannerStore) UpdateObservedGeneration(ctx context.Context, name string, observedGen int64, access *componentspb.ComponentAccessStatus, state componentspb.ComponentState, statusMsg string) (*ComponentRecord, error) {
	canonicalName, err := validateUpdateObservedArgs(name, observedGen)
	if err != nil {
		return nil, err
	}

	var finalRec *ComponentRecord

	commitTimestamp, err := s.client.ReadWriteTransaction(ctx, func(ctx context.Context, txn *spanner.ReadWriteTransaction) error {
		catalog, err := s.loadActiveCatalog(ctx, txn)
		if err != nil {
			return err
		}

		rec, ok := catalog[canonicalName]
		if !ok || rec == nil || isDeletedRecord(rec) {
			return ErrComponentNotFound
		}

		rec.ObservedGeneration = &observedGen
		if access != nil {
			rec.Status = proto.Clone(access).(*componentspb.ComponentAccessStatus)
		}
		if state != componentspb.ComponentState_COMPONENT_STATE_UNSPECIFIED {
			rec.State = state
		}
		applyStatusUpdates(rec, observedGen, state, statusMsg)

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

		if err := txn.BufferWrite([]*spanner.Mutation{spanner.UpdateMap("Components", updateMap)}); err != nil {
			return err
		}

		finalRec = cloneRecord(rec)
		return nil
	})

	if err != nil {
		return nil, err
	}

	if finalRec != nil {
		finalRec.UpdateTime = commitTimestamp
		finalRec.Etag = generateEtag(finalRec)
	}

	return cloneRecord(finalRec), nil
}

// ExecuteDeletion deletes a component using the specified deletion policy.
func (s *SpannerStore) ExecuteDeletion(ctx context.Context, name string, policy componentspb.DeletionPolicy, force bool) error {
	canonicalName, err := validateResourceName(name)
	if err != nil {
		return err
	}

	_, err = s.client.ReadWriteTransaction(ctx, func(ctx context.Context, txn *spanner.ReadWriteTransaction) error {
		catalog, err := s.loadActiveCatalog(ctx, txn)
		if err != nil {
			return err
		}

		rec, ok := catalog[canonicalName]
		if !ok || rec == nil || isDeletedRecord(rec) {
			return ErrComponentNotFound
		}

		if err := ValidateDeletionSafety(catalog, canonicalName, policy, force); err != nil {
			return err
		}

		toDelete := collectDeletionTargets(rec, catalog, canonicalName, policy)
		mutations, err := buildDeletionMutations(toDelete, policy, time.Now().UTC())
		if err != nil {
			return err
		}

		return txn.BufferWrite(mutations)
	})

	return err
}

func collectDeletionTargets(rec *ComponentRecord, catalog map[string]*ComponentRecord, canonicalName string, policy componentspb.DeletionPolicy) []*ComponentRecord {
	toDelete := []*ComponentRecord{rec}
	if policy == componentspb.DeletionPolicy_DELETION_POLICY_DELETE || policy == componentspb.DeletionPolicy_DELETION_POLICY_UNSPECIFIED {
		allDependents, _ := findAllDependents(catalog, canonicalName)
		for _, depName := range allDependents {
			if depRec, ok := catalog[depName]; ok && depRec != nil && !isDeletedRecord(depRec) {
				toDelete = append(toDelete, depRec)
			}
		}
	}
	return toDelete
}

func buildDeletionMutations(toDelete []*ComponentRecord, policy componentspb.DeletionPolicy, now time.Time) ([]*spanner.Mutation, error) {
	var mutations []*spanner.Mutation
	for _, target := range toDelete {
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
			Name:   FullComponentName(target.Name),
			Spec:   target.Spec,
			Labels: copyMap(target.Labels),
		}
		configBytes, err := proto.Marshal(compProto)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal component proto: %w", err)
		}

		accessBytes, err := proto.Marshal(target.Status)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal access status proto: %w", err)
		}

		mutations = append(mutations, spanner.UpdateMap("Components", map[string]any{
			"ComponentId":   target.ComponentID,
			"ConfigProto":   configBytes,
			"AccessPayload": accessBytes,
			"State":         int64(componentspb.ComponentState_COMPONENT_STATE_DELETED),
			"UpdateTime":    spanner.CommitTimestamp,
		}))
	}
	return mutations, nil
}

// ResolveComponentSnapshot resolves or creates an immutable snapshot of a component at generation.
func (s *SpannerStore) ResolveComponentSnapshot(ctx context.Context, name string, generation int64) (*ComponentRecord, *componentspb.ResolvedComponentCoordinates, error) {
	canonicalName, err := validateResourceName(name)
	if err != nil {
		return nil, nil, err
	}
	if generation < 0 {
		return nil, nil, status.Error(codes.InvalidArgument, "generation cannot be negative")
	}

	rec, err := s.GetComponent(ctx, canonicalName)
	if err != nil {
		return nil, nil, err
	}

	targetGen := generation
	if targetGen == 0 {
		targetGen = rec.Generation
	}

	if targetGen > rec.Generation {
		return nil, nil, status.Errorf(codes.NotFound, "snapshot for generation %d of component %s not found (current generation is %d)", targetGen, canonicalName, rec.Generation)
	}

	if targetGen == rec.Generation {
		var coords *componentspb.ResolvedComponentCoordinates
		if rec.Status != nil && rec.Status.Coordinates != nil {
			coords = cloneCoordinates(rec.Status.Coordinates)
		}
		return cloneRecord(rec), coords, nil
	}

	return s.resolveHistoricalSnapshot(ctx, rec, targetGen, canonicalName)
}

func (s *SpannerStore) resolveHistoricalSnapshot(ctx context.Context, rec *ComponentRecord, targetGen int64, canonicalName string) (*ComponentRecord, *componentspb.ResolvedComponentCoordinates, error) {
	stmt := spanner.Statement{
		SQL: selectSnapshotByComponentIDAndGenSQL,
		Params: map[string]any{
			"id":  rec.ComponentID,
			"gen": targetGen,
		},
	}
	iter := s.client.Single().Query(ctx, stmt)
	defer iter.Stop()

	row, err := iter.Next()
	if err != nil {
		if err == iterator.Done {
			return nil, nil, status.Errorf(codes.NotFound, "historical snapshot for generation %d of component %s not found", targetGen, canonicalName)
		}
		return nil, nil, fmt.Errorf("failed to query experiment component snapshots: %w", err)
	}

	var (
		compID        int64
		lockedGen     int64
		configProto   []byte
		accessPayload []byte
	)
	if err := row.ColumnByName("ComponentId", &compID); err != nil {
		return nil, nil, err
	}
	if err := row.ColumnByName("LockedGeneration", &lockedGen); err != nil {
		return nil, nil, err
	}
	if err := row.ColumnByName("ConfigProto", &configProto); err != nil {
		return nil, nil, err
	}
	if err := row.ColumnByName("AccessPayload", &accessPayload); err != nil {
		return nil, nil, err
	}

	snapRec := cloneRecord(rec)
	snapRec.Generation = lockedGen
	unmarshalConfigProto(configProto, snapRec)

	var coords *componentspb.ResolvedComponentCoordinates
	if len(accessPayload) > 0 {
		var statusProto componentspb.ComponentAccessStatus
		if err := proto.Unmarshal(accessPayload, &statusProto); err == nil {
			snapRec.Status = &statusProto
			coords = cloneCoordinates(statusProto.Coordinates)
		}
	}

	return snapRec, coords, nil
}

// ValidateComponentRequirement checks if a required component type and version exists and is active.
func (s *SpannerStore) ValidateComponentRequirement(ctx context.Context, name string, reqType componentspb.ComponentType, minVersion string) (*RequirementValidationResult, error) {
	canonicalName, err := validateResourceName(name)
	if err != nil {
		return nil, err
	}
	if minVersion != "" {
		if _, err := Parse(minVersion); err != nil {
			return nil, status.Errorf(codes.InvalidArgument, "invalid minimum version %q: %v", minVersion, err)
		}
	}

	rec, err := s.GetComponent(ctx, canonicalName)
	if err != nil {
		if status.Code(err) == codes.NotFound {
			return &RequirementValidationResult{
				IsSatisfied:             false,
				SatisfyingComponentName: canonicalName,
				Reason:                  fmt.Sprintf("component %q not found", canonicalName),
			}, nil
		}
		return nil, err
	}

	return evaluateComponentRequirement(rec, canonicalName, reqType, minVersion)
}

// GetPlatformHealth retrieves overall system component health metrics and status.
func (s *SpannerStore) GetPlatformHealth(ctx context.Context, filter string, pageSize int32, pageToken string) (*componentspb.GetPlatformHealthResponse, error) {
	return nil, status.Error(codes.Unimplemented, "method GetPlatformHealth not implemented in SpannerStore")
}

func (s *SpannerStore) loadActiveCatalog(ctx context.Context, queryable spannerQueryable) (map[string]*ComponentRecord, error) {
	compIter := queryable.Query(ctx, spanner.Statement{SQL: selectAllActiveComponentsSQL})
	defer compIter.Stop()

	componentsByName := make(map[string]*ComponentRecord)
	componentsByID := make(map[int64]*ComponentRecord)

	if err := compIter.Do(func(row *spanner.Row) error {
		rec, err := rowToComponentRecord(row)
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
		rec.Etag = generateEtag(rec)
	}

	return componentsByName, nil
}

type componentRowData struct {
	componentID    int64
	name           string
	componentClass spanner.NullString
	configProto    []byte
	accessPayload  []byte
	state          int64
	generation     int64
	observedGen    spanner.NullInt64
	createTime     time.Time
	updateTime     time.Time
}

func scanComponentRow(row *spanner.Row) (*componentRowData, error) {
	var d componentRowData
	if err := row.ColumnByName("ComponentId", &d.componentID); err != nil {
		return nil, fmt.Errorf("failed to read ComponentId: %w", err)
	}
	if err := row.ColumnByName("Name", &d.name); err != nil {
		return nil, fmt.Errorf("failed to read Name: %w", err)
	}
	if err := row.ColumnByName("ComponentClass", &d.componentClass); err != nil {
		return nil, fmt.Errorf("failed to read ComponentClass: %w", err)
	}
	if err := row.ColumnByName("ConfigProto", &d.configProto); err != nil {
		return nil, fmt.Errorf("failed to read ConfigProto: %w", err)
	}
	if err := row.ColumnByName("AccessPayload", &d.accessPayload); err != nil {
		return nil, fmt.Errorf("failed to read AccessPayload: %w", err)
	}
	if err := row.ColumnByName("State", &d.state); err != nil {
		return nil, fmt.Errorf("failed to read State: %w", err)
	}
	if err := row.ColumnByName("Generation", &d.generation); err != nil {
		return nil, fmt.Errorf("failed to read Generation: %w", err)
	}
	if err := row.ColumnByName("ObservedGeneration", &d.observedGen); err != nil {
		return nil, fmt.Errorf("failed to read ObservedGeneration: %w", err)
	}
	if err := row.ColumnByName("CreateTime", &d.createTime); err != nil {
		return nil, fmt.Errorf("failed to read CreateTime: %w", err)
	}
	if err := row.ColumnByName("UpdateTime", &d.updateTime); err != nil {
		return nil, fmt.Errorf("failed to read UpdateTime: %w", err)
	}
	return &d, nil
}

func rowToComponentRecord(row *spanner.Row) (*ComponentRecord, error) {
	d, err := scanComponentRow(row)
	if err != nil {
		return nil, err
	}

	rec := &ComponentRecord{
		ComponentID: d.componentID,
		Name:        d.name,
		State:       componentspb.ComponentState(d.state),
		Generation:  d.generation,
		CreateTime:  d.createTime,
		UpdateTime:  d.updateTime,
	}

	if d.observedGen.Valid {
		gen := d.observedGen.Int64
		rec.ObservedGeneration = &gen
	}

	unmarshalConfigProto(d.configProto, rec)
	unmarshalAccessStatus(d.accessPayload, rec)

	if rec.State == componentspb.ComponentState_COMPONENT_STATE_DELETED {
		t := d.updateTime
		rec.DeleteTime = &t
	}

	rec.Etag = generateEtag(rec)
	return rec, nil
}

func unmarshalConfigProto(configBytes []byte, rec *ComponentRecord) {
	if len(configBytes) == 0 {
		return
	}
	var comp componentspb.Component
	if err := proto.Unmarshal(configBytes, &comp); err == nil && (comp.Spec != nil || len(comp.Labels) > 0) {
		rec.Spec = comp.Spec
		if len(comp.Labels) > 0 {
			rec.Labels = copyMap(comp.Labels)
		}
		return
	}
	var spec componentspb.ComponentSpec
	if err := proto.Unmarshal(configBytes, &spec); err == nil {
		rec.Spec = &spec
	}
}

func unmarshalAccessStatus(accessBytes []byte, rec *ComponentRecord) {
	if len(accessBytes) == 0 {
		return
	}
	var statusProto componentspb.ComponentAccessStatus
	if err := proto.Unmarshal(accessBytes, &statusProto); err == nil {
		rec.Status = &statusProto
	}
}

func fetchDependencies(iter *spanner.RowIterator) ([]string, error) {
	var deps []string
	err := iter.Do(func(row *spanner.Row) error {
		var depName string
		if err := row.Column(0, &depName); err != nil {
			return err
		}
		deps = append(deps, depName)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return deps, nil
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
