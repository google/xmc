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
	"crypto/rand"
	"encoding/binary"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"google.golang.org/protobuf/proto"

	componentspb "xmanager_cloud/api/protos/components/v1alpha1"
	"xmanager_cloud/internal/logging"
	"xmanager_cloud/internal/storage/postgres/pgdb"
	"xmanager_cloud/pkg/components/naming"
	"xmanager_cloud/pkg/components/store"
	"xmanager_cloud/pkg/components/store/dependency"
	"xmanager_cloud/pkg/components/store/ownership"
)

const (
	selectComponentWithActiveDependenciesSQL = `SELECT ` + componentColumnsSQL + `,
  ARRAY(SELECT dep.Name FROM ComponentDependencies cd JOIN Components dep ON cd.DependencyComponentId = dep.ComponentId WHERE cd.ComponentId = c.ComponentId AND dep.ActiveName IS NOT NULL ORDER BY dep.Name ASC) AS ActiveDependencies,
  ` + fenceColumnsSQL + `
FROM Components c WHERE c.ActiveName = $1 LIMIT 1`

	insertComponentSQL          = `INSERT INTO Components (ComponentId, Name, ComponentClass, ConfigProto, AccessPayload, State, Generation, CreateTime, UpdateTime) VALUES ($1, $2, NULLIF($3, ''), $4, $5, $6, $7, $8, $8)`
	updateComponentIntentSQL    = `UPDATE Components SET ComponentClass = NULLIF($2, ''), ConfigProto = $3, AccessPayload = $4, State = $5, Generation = $6, UpdateTime = $7 WHERE ComponentId = $1`
	deleteDependenciesByCompSQL = `DELETE FROM ComponentDependencies WHERE ComponentId = $1`
	insertDependencySQL         = `INSERT INTO ComponentDependencies (ComponentId, DependencyComponentId) VALUES ($1, $2)`
	updateComponentOwnerColsSQL = `UPDATE Components SET SourceUid = $2, SourceGeneration = $3, SourceNamespace = $4, SourceClusterUid = $5, SourceEpoch = $6, SourceLastSeenTime = $7 WHERE ComponentId = $1`
)

// SetComponentIntent applies or updates component intent within PostgreSQL. An unchanged intent
// whose dependencies are all registered and active is answered from a single-query read without a
// read-write transaction; anything else is decided by the serializable read-write transaction.
func (s *PostgresStore) SetComponentIntent(ctx context.Context, name string, spec *componentspb.ComponentSpec, labels map[string]string, desiredGen int64) (*store.ComponentRecord, error) {
	return s.setIntent(ctx, name, spec, labels, desiredGen, nil)
}

// SetSourcedComponentIntent applies an intent sent by src within PostgreSQL, with the same
// read-only fast path as SetComponentIntent.
func (s *PostgresStore) SetSourcedComponentIntent(ctx context.Context, name string, spec *componentspb.ComponentSpec, labels map[string]string, src store.RequestSource) (*store.ComponentRecord, error) {
	if err := ownership.ValidateRequestSource(src); err != nil {
		return nil, err
	}
	return s.setIntent(ctx, name, spec, labels, 0, &src)
}

func (s *PostgresStore) setIntent(ctx context.Context, name string, spec *componentspb.ComponentSpec, labels map[string]string, desiredGen int64, src *store.RequestSource) (*store.ComponentRecord, error) {
	canonicalName, err := store.ValidateIntentArgs(name, spec)
	if err != nil {
		return nil, err
	}
	deps := dependency.ExtractDependencies(spec, labels)
	rec, ok, err := s.readUnchangedIntent(ctx, canonicalName, spec, labels, deps, desiredGen, src)
	if err != nil {
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

func (s *PostgresStore) readUnchangedIntent(ctx context.Context, canonicalName string, spec *componentspb.ComponentSpec, labels map[string]string, deps []string, desiredGen int64, src *store.RequestSource) (*store.ComponentRecord, bool, error) {
	rec, fence, err := queryComponentWithActiveDependencies(ctx, s.pool, canonicalName)
	if err != nil {
		logFastPathReadError(ctx, canonicalName, err)
		return nil, false, nil
	}
	if err := ownership.CheckClusterFence(store.OperationSetComponentIntent, fence, src); err != nil {
		return nil, false, err
	}
	if plan, err := ownership.PlanIntent(canonicalName, rec, spec, labels, deps, desiredGen, src); err != nil || plan.Write != ownership.WriteNothing {
		return nil, false, nil
	}
	return store.CloneRecord(rec), true, nil
}

func logFastPathReadError(ctx context.Context, canonicalName string, err error) {
	switch {
	case errors.Is(err, store.ErrComponentNotFound):
	case ctx.Err() != nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded):
		logging.Debug(ctx, "Intent fast-path read canceled; falling back to read-write transaction", "name", canonicalName, "error", err)
	default:
		logging.Warn(ctx, "Intent fast-path read failed; falling back to read-write transaction", "name", canonicalName, "error", err)
	}
}

func queryComponentWithActiveDependencies(ctx context.Context, q pgdb.DBTX, canonicalName string) (*store.ComponentRecord, *store.ClusterIdentity, error) {
	var r componentRow
	var deps []string
	var fenceUID, fenceName pgtype.Text
	var fenceEpoch pgtype.Int8
	targets := append(r.scanTargets(), &deps, &fenceUID, &fenceName, &fenceEpoch)
	if err := q.QueryRow(ctx, selectComponentWithActiveDependenciesSQL, canonicalName).Scan(targets...); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, nil, store.ErrComponentNotFound
		}
		return nil, nil, fmt.Errorf("failed to query component %q: %w", canonicalName, err)
	}
	rec, err := r.toRecord()
	if err != nil {
		return nil, nil, err
	}
	if len(deps) > 0 {
		rec.Dependencies = deps
	}
	var fence *store.ClusterIdentity
	if fenceUID.Valid {
		fence = &store.ClusterIdentity{UID: fenceUID.String, Name: fenceName.String, Epoch: fenceEpoch.Int64}
	}
	return rec, fence, nil
}

func (s *PostgresStore) writeIntent(ctx context.Context, canonicalName string, spec *componentspb.ComponentSpec, labels map[string]string, deps []string, desiredGen int64, src *store.RequestSource) (*store.ComponentRecord, error) {
	var finalRec *store.ComponentRecord
	var plan ownership.IntentPlan
	commitTimestamp, err := s.runInTx(ctx, func(ctx context.Context, tx pgx.Tx, commitTime time.Time) error {
		var err error
		finalRec, plan, err = s.applyIntentTx(ctx, tx, canonicalName, spec, labels, deps, desiredGen, src, commitTime)
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

func (s *PostgresStore) applyIntentTx(ctx context.Context, tx pgx.Tx, canonicalName string, spec *componentspb.ComponentSpec, labels map[string]string, deps []string, desiredGen int64, src *store.RequestSource, commitTime time.Time) (*store.ComponentRecord, ownership.IntentPlan, error) {
	if err := checkFence(ctx, tx, store.OperationSetComponentIntent, src); err != nil {
		return nil, ownership.IntentPlan{}, err
	}
	catalog, err := s.loadActiveCatalog(ctx, tx)
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
		rec, err = updateExistingIntentTx(ctx, tx, existingRec, canonicalName, spec, labels, deps, plan.Generation, depIDs, commitTime)
	default:
		rec, err = createNewIntentTx(ctx, tx, canonicalName, spec, labels, deps, plan.Generation, depIDs, commitTime)
	}
	if err != nil {
		return nil, ownership.IntentPlan{}, err
	}
	return rec, plan, recordOwnerTx(ctx, tx, rec, src, commitTime)
}

func recordOwnerTx(ctx context.Context, tx pgx.Tx, rec *store.ComponentRecord, src *store.RequestSource, commitTime time.Time) error {
	if src == nil {
		return nil
	}
	return writeOwnerColumnsTx(ctx, tx, rec, store.OwnerOf(*src), commitTime)
}

func writeOwnerColumnsTx(ctx context.Context, tx pgx.Tx, rec *store.ComponentRecord, owner *store.SourceOwner, commitTime time.Time) error {
	if owner == nil {
		return nil
	}
	rec.Owner = owner
	_, err := tx.Exec(ctx, updateComponentOwnerColsSQL, rec.ComponentID, owner.UID, owner.Generation, owner.Namespace, owner.ClusterUID, owner.Epoch, commitTime)
	return err
}

func updateExistingIntentTx(ctx context.Context, tx pgx.Tx, existingRec *store.ComponentRecord, canonicalName string, spec *componentspb.ComponentSpec, labels map[string]string, deps []string, newGen int64, depIDs map[string]int64, commitTime time.Time) (*store.ComponentRecord, error) {
	p, err := marshalIntentPayloads(canonicalName, spec, labels, existingRec.Status, newGen)
	if err != nil {
		return nil, err
	}
	state := int64(componentspb.ComponentState_COMPONENT_STATE_PENDING_RECONCILIATION)
	if _, err := tx.Exec(ctx, updateComponentIntentSQL, existingRec.ComponentID, determineComponentClass(p.spec), p.configBytes, p.accessBytes, state, newGen, commitTime); err != nil {
		return nil, fmt.Errorf("failed to update component intent: %w", err)
	}
	if err := replaceDependenciesTx(ctx, tx, existingRec.ComponentID, deps, depIDs, true); err != nil {
		return nil, err
	}
	return &store.ComponentRecord{
		ComponentID:        existingRec.ComponentID,
		Name:               canonicalName,
		Spec:               p.spec,
		Status:             p.status,
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

func createNewIntentTx(ctx context.Context, tx pgx.Tx, canonicalName string, spec *componentspb.ComponentSpec, labels map[string]string, deps []string, gen int64, depIDs map[string]int64, commitTime time.Time) (*store.ComponentRecord, error) {
	id, err := generateRandomID()
	if err != nil {
		return nil, fmt.Errorf("failed to generate component ID: %w", err)
	}
	p, err := marshalIntentPayloads(canonicalName, spec, labels, nil, gen)
	if err != nil {
		return nil, err
	}
	state := int64(componentspb.ComponentState_COMPONENT_STATE_PENDING_RECONCILIATION)
	if _, err := tx.Exec(ctx, insertComponentSQL, id, canonicalName, determineComponentClass(p.spec), p.configBytes, p.accessBytes, state, gen, commitTime); err != nil {
		return nil, fmt.Errorf("failed to insert component: %w", err)
	}
	if err := replaceDependenciesTx(ctx, tx, id, deps, depIDs, false); err != nil {
		return nil, err
	}
	return &store.ComponentRecord{
		ComponentID:  id,
		Name:         canonicalName,
		Spec:         p.spec,
		Status:       p.status,
		State:        componentspb.ComponentState_COMPONENT_STATE_PENDING_RECONCILIATION,
		Generation:   gen,
		Dependencies: deps,
		Labels:       store.CopyMap(labels),
	}, nil
}

type intentPayloads struct {
	spec        *componentspb.ComponentSpec
	status      *componentspb.ComponentAccessStatus
	configBytes []byte
	accessBytes []byte
}

func marshalIntentPayloads(canonicalName string, spec *componentspb.ComponentSpec, labels map[string]string, existingStatus *componentspb.ComponentAccessStatus, gen int64) (intentPayloads, error) {
	specClone := proto.Clone(spec).(*componentspb.ComponentSpec)
	specClone.Generation = gen
	configBytes, err := proto.Marshal(&componentspb.Component{
		Name:   naming.FullComponentName(canonicalName),
		Spec:   specClone,
		Labels: store.CopyMap(labels),
	})
	if err != nil {
		return intentPayloads{}, fmt.Errorf("failed to marshal component proto: %w", err)
	}
	statusProto := &componentspb.ComponentAccessStatus{}
	if existingStatus != nil {
		statusProto = proto.Clone(existingStatus).(*componentspb.ComponentAccessStatus)
	}
	statusProto.State = componentspb.ComponentState_COMPONENT_STATE_PENDING_RECONCILIATION
	accessBytes, err := proto.Marshal(statusProto)
	if err != nil {
		return intentPayloads{}, fmt.Errorf("failed to marshal access status proto: %w", err)
	}
	return intentPayloads{spec: specClone, status: statusProto, configBytes: configBytes, accessBytes: accessBytes}, nil
}

func replaceDependenciesTx(ctx context.Context, tx pgx.Tx, componentID int64, deps []string, depIDs map[string]int64, isUpdate bool) error {
	if isUpdate {
		if _, err := tx.Exec(ctx, deleteDependenciesByCompSQL, componentID); err != nil {
			return fmt.Errorf("failed to delete dependencies: %w", err)
		}
	}
	for _, dep := range deps {
		if _, err := tx.Exec(ctx, insertDependencySQL, componentID, depIDs[dep]); err != nil {
			return fmt.Errorf("failed to insert dependency %q: %w", dep, err)
		}
	}
	return nil
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
