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

// Package component provides the Cloud Spanner implementation for persisting CMS component catalog records.
package component

import (
	"context"
	"errors"
	"fmt"
	"time"

	"cloud.google.com/go/spanner"
	"google.golang.org/api/iterator"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	componentspb "xmanager_cloud/api/protos/components/v1alpha1"
	"xmanager_cloud/internal/grpcerr"
	"xmanager_cloud/internal/storage"
	"xmanager_cloud/internal/storage/data"
	"xmanager_cloud/pkg/components/store"
)

const (
	// ComponentColumnsSQL lists the Components columns that RowToComponentRecord reads.
	ComponentColumnsSQL                = `ComponentId, Name, ComponentClass, ConfigProto, AccessPayload, State, Generation, ObservedGeneration, CreateTime, UpdateTime, SourceUid, SourceGeneration, SourceNamespace, SourceClusterUid, SourceEpoch, SourceLastSeenTime`
	selectComponentByActiveNameSQL     = `SELECT ` + ComponentColumnsSQL + ` FROM Components WHERE ActiveName = @name LIMIT 1`
	selectComponentByIDSQL             = `SELECT ` + ComponentColumnsSQL + ` FROM Components WHERE ComponentId = @id AND State != 5 LIMIT 1`
	selectAllActiveComponentsSQL       = `SELECT ` + ComponentColumnsSQL + ` FROM Components WHERE ActiveName IS NOT NULL ORDER BY Name ASC`
	selectDependenciesByComponentIDSQL = `SELECT dep.Name FROM ComponentDependencies cd JOIN Components dep ON cd.DependencyComponentId = dep.ComponentId WHERE cd.ComponentId = @id AND dep.ActiveName IS NOT NULL ORDER BY dep.Name ASC`
	selectAllActiveDependenciesSQL     = `SELECT cd.ComponentId, dep.Name FROM ComponentDependencies cd JOIN Components dep ON cd.DependencyComponentId = dep.ComponentId JOIN Components parent ON cd.ComponentId = parent.ComponentId WHERE parent.ActiveName IS NOT NULL AND dep.ActiveName IS NOT NULL ORDER BY cd.ComponentId, dep.Name ASC`
	fenceTable                         = "ComponentOwnershipFence"
	fenceID                            = int64(1)
	// takeOverOwnersSQL moves every active owned component to the declaring cluster and clears its
	// owner UID so that the cluster's next intent adopts it (R2). SourceLastSeenTime is set to the
	// commit time, which starts the adoption grace period of the owner_stale gauge.
	takeOverOwnersSQL = `UPDATE Components SET SourceClusterUid = @clusterUid, SourceEpoch = @epoch, SourceUid = NULL, SourceLastSeenTime = PENDING_COMMIT_TIMESTAMP() WHERE ActiveName IS NOT NULL AND SourceClusterUid IS NOT NULL`

	// ownershipGaugesSQL reads the fence and counts active owned components whose last-seen
	// timestamp is at or before @cutoff in one snapshot. Records reassigned by a takeover count
	// only once the adoption grace period that starts at the takeover commit has ended.
	ownershipGaugesSQL = `SELECT
  (SELECT f.Epoch FROM ComponentOwnershipFence f WHERE f.FenceId = 1) AS FenceEpoch,
  (SELECT f.LastSeenTime FROM ComponentOwnershipFence f WHERE f.FenceId = 1) AS FenceLastSeenTime,
  (SELECT COUNT(*) FROM Components WHERE ActiveName IS NOT NULL AND SourceClusterUid IS NOT NULL AND SourceLastSeenTime <= @cutoff) AS StaleOwners`

	// fenceColumnsSQL adds the fence holder to a query as scalar subqueries, NULL before the first
	// declaration, so that one single-use read sees the fence and the component in one snapshot.
	fenceColumnsSQL = `(SELECT f.ActiveClusterUid FROM ComponentOwnershipFence f WHERE f.FenceId = 1) AS FenceClusterUid,
  (SELECT f.ActiveClusterName FROM ComponentOwnershipFence f WHERE f.FenceId = 1) AS FenceClusterName,
  (SELECT f.Epoch FROM ComponentOwnershipFence f WHERE f.FenceId = 1) AS FenceEpoch`

	selectComponentWithActiveDependenciesSQL = `SELECT ` + ComponentColumnsSQL + `,
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

var (
	_ storage.ComponentRepository   = (*Repository)(nil)
	_ storage.ComponentTxRepository = (*TxRepository)(nil)
)

// Queryable is implemented by Spanner single-use reads, read-only transactions, and read-write transactions.
type Queryable interface {
	Query(ctx context.Context, statement spanner.Statement) *spanner.RowIterator
}

// Repository reads component catalog records from Cloud Spanner.
type Repository struct {
	client *spanner.Client
}

// TxRepository reads and writes component catalog records within a bound Spanner transaction.
type TxRepository struct {
	q  Queryable
	tx *spanner.ReadWriteTransaction
}

// NewRepository creates a Spanner-backed component Repository.
func NewRepository(client *spanner.Client) *Repository {
	return &Repository{client: client}
}

// WithTx returns a repository bound to the given Spanner read-only or read-write transaction.
func (r *Repository) WithTx(q Queryable) *TxRepository {
	tx, _ := q.(*spanner.ReadWriteTransaction)
	return &TxRepository{q: q, tx: tx}
}

// WithReadOnlySnapshot runs fn against a consistent read-only snapshot.
func (r *Repository) WithReadOnlySnapshot(ctx context.Context, fn func(ctx context.Context, repo storage.ComponentTxRepository) error) error {
	if fn == nil {
		return fmt.Errorf("callback function must not be nil: %w", grpcerr.ErrInvalidArgument)
	}
	ro, err := r.readOnlyTransaction()
	if err != nil {
		return err
	}
	defer ro.Close()
	return fn(ctx, r.WithTx(ro))
}

// GetActiveComponent retrieves a non-deleted component record by canonical name, populated with active dependencies.
func (r *Repository) GetActiveComponent(ctx context.Context, canonicalName string) (*store.ComponentRecord, error) {
	ro, err := r.readOnlyTransaction()
	if err != nil {
		return nil, err
	}
	defer ro.Close()
	return getActiveComponent(ctx, ro, canonicalName)
}

// GetComponentByID retrieves a non-deleted component record by its unique database identifier, populated with active dependencies.
func (r *Repository) GetComponentByID(ctx context.Context, componentID int64) (*store.ComponentRecord, error) {
	ro, err := r.readOnlyTransaction()
	if err != nil {
		return nil, err
	}
	defer ro.Close()
	return getComponentByID(ctx, ro, componentID)
}

// ListActiveComponents retrieves all non-deleted components keyed by canonical name, populated with active dependencies.
func (r *Repository) ListActiveComponents(ctx context.Context) (map[string]*store.ComponentRecord, error) {
	ro, err := r.readOnlyTransaction()
	if err != nil {
		return nil, err
	}
	defer ro.Close()
	return listActiveComponents(ctx, ro)
}

// GetComponentWithFence runs one query, so it can use a single-use read, and returns the active
// record of canonicalName with Dependencies set to its registered, non-deleted dependencies (nil
// when there are none, as on the read-write path), together with the fence holder in the same
// snapshot (nil before the first declaration). The fence comes from scalar subqueries
// (fenceColumnsSQL), so the component and the fence are read in one snapshot.
func (r *Repository) GetComponentWithFence(ctx context.Context, canonicalName string) (*store.ComponentRecord, *store.ClusterIdentity, error) {
	q, err := r.single()
	if err != nil {
		return nil, nil, err
	}
	iter := q.Query(ctx, spanner.Statement{SQL: selectComponentWithActiveDependenciesSQL, Params: map[string]any{"name": canonicalName}})
	defer iter.Stop()
	row, err := iter.Next()
	if errors.Is(err, iterator.Done) {
		return nil, nil, store.ErrComponentNotFound
	}
	if err != nil {
		return nil, nil, fmt.Errorf("failed to query component %q: %w", canonicalName, err)
	}
	rec, err := RowToComponentRecord(row)
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

// ReadOwnershipGauges reads the ownership fence and counts active components whose owner last-seen
// timestamp is at or before cutoff in one snapshot. Records reassigned by a takeover count only once
// the adoption grace period that starts at the takeover commit has ended.
func (r *Repository) ReadOwnershipGauges(ctx context.Context, cutoff time.Time) (store.OwnershipGauges, error) {
	q, err := r.single()
	if err != nil {
		return store.OwnershipGauges{}, err
	}
	iter := q.Query(ctx, spanner.Statement{SQL: ownershipGaugesSQL, Params: map[string]any{"cutoff": cutoff}})
	defer iter.Stop()
	row, err := iter.Next()
	if err != nil {
		return store.OwnershipGauges{}, fmt.Errorf("failed to read the ownership gauges: %w", err)
	}
	var epoch spanner.NullInt64
	var lastSeen spanner.NullTime
	var g store.OwnershipGauges
	if err := row.Columns(&epoch, &lastSeen, &g.StaleOwners); err != nil {
		return store.OwnershipGauges{}, fmt.Errorf("failed to decode the ownership gauges: %w", err)
	}
	g.FenceDeclared, g.FenceEpoch, g.FenceLastSeenTime = epoch.Valid, epoch.Int64, lastSeen.Time
	return g, nil
}

// GetActiveComponent retrieves a non-deleted component record by canonical name within the transaction.
func (r *TxRepository) GetActiveComponent(ctx context.Context, canonicalName string) (*store.ComponentRecord, error) {
	return getActiveComponent(ctx, r.q, canonicalName)
}

// GetComponentByID retrieves a non-deleted component record by its unique database identifier within the transaction.
func (r *TxRepository) GetComponentByID(ctx context.Context, componentID int64) (*store.ComponentRecord, error) {
	return getComponentByID(ctx, r.q, componentID)
}

// ListActiveComponents retrieves all non-deleted components keyed by canonical name within the transaction.
func (r *TxRepository) ListActiveComponents(ctx context.Context) (map[string]*store.ComponentRecord, error) {
	return listActiveComponents(ctx, r.q)
}

// GetSnapshotByID resolves a historical component snapshot by ComponentId and generation within the transaction.
func (r *TxRepository) GetSnapshotByID(ctx context.Context, componentID, generation int64, canonicalName string, createTime, updateTime time.Time) (*store.ComponentRecord, *componentspb.ResolvedComponentCoordinates, error) {
	stmt := spanner.Statement{SQL: selectSnapshotByComponentIDAndGenSQL, Params: map[string]any{"id": componentID, "gen": generation}}
	row, err := querySnapshotRow(ctx, r.q, stmt, canonicalName, generation)
	if err != nil {
		return nil, nil, err
	}
	return decodeSnapshotRecord(row, canonicalName, createTime, updateTime)
}

// GetSnapshotByName resolves a historical component snapshot by canonical name and generation within the transaction.
func (r *TxRepository) GetSnapshotByName(ctx context.Context, canonicalName string, generation int64) (*store.ComponentRecord, *componentspb.ResolvedComponentCoordinates, error) {
	stmt := spanner.Statement{SQL: selectSnapshotByNameAndGenSQL, Params: map[string]any{"name": canonicalName, "gen": generation}}
	row, err := querySnapshotRow(ctx, r.q, stmt, canonicalName, generation)
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
	return decodeSnapshotRecord(row, canonicalName, createTime, updateTime)
}

// ReadFence returns the current ownership fence holder within the transaction, or nil before the first declaration.
func (r *TxRepository) ReadFence(ctx context.Context) (*store.ClusterIdentity, error) {
	if r.tx == nil {
		return nil, fmt.Errorf("cannot read the ownership fence without a read-write transaction: %w", grpcerr.ErrFailedPrecondition)
	}
	row, err := r.tx.ReadRow(ctx, fenceTable, spanner.Key{fenceID}, []string{"ActiveClusterUid", "ActiveClusterName", "Epoch"})
	if err != nil {
		if spanner.ErrCode(err) == codes.NotFound {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to read the ownership fence: %w", err)
	}
	var fence store.ClusterIdentity
	if err := row.Columns(&fence.UID, &fence.Name, &fence.Epoch); err != nil {
		return nil, fmt.Errorf("failed to decode the ownership fence: %w", err)
	}
	return &fence, nil
}

// RefreshFence updates LastSeenTime to the commit time and, when newName is non-nil, ActiveClusterName,
// taking no write lock on Epoch or ActiveClusterUid.
func (r *TxRepository) RefreshFence(_ context.Context, newName *string) error {
	if r.tx == nil {
		return fmt.Errorf("cannot refresh the ownership fence without a read-write transaction: %w", grpcerr.ErrFailedPrecondition)
	}
	cols := map[string]any{"FenceId": fenceID, "LastSeenTime": spanner.CommitTimestamp}
	if newName != nil {
		cols["ActiveClusterName"] = *newName
	}
	return r.tx.BufferWrite([]*spanner.Mutation{spanner.UpdateMap(fenceTable, cols)})
}

// ReplaceFence writes cluster as the fence holder with DeclaredTime and LastSeenTime at commit time.
func (r *TxRepository) ReplaceFence(_ context.Context, cluster store.ClusterIdentity) error {
	if r.tx == nil {
		return fmt.Errorf("cannot replace the ownership fence without a read-write transaction: %w", grpcerr.ErrFailedPrecondition)
	}
	return r.tx.BufferWrite([]*spanner.Mutation{spanner.InsertOrUpdateMap(fenceTable, map[string]any{
		"FenceId": fenceID, "ActiveClusterUid": cluster.UID, "ActiveClusterName": cluster.Name,
		"Epoch": cluster.Epoch, "DeclaredTime": spanner.CommitTimestamp, "LastSeenTime": spanner.CommitTimestamp,
	})})
}

// ReassignOwnedComponents moves every active owned component to the declaring cluster and clears its owner UID
// so that the cluster's next intent adopts it (R2). SourceLastSeenTime is set to the commit time, which starts
// the adoption grace period of the owner_stale gauge.
func (r *TxRepository) ReassignOwnedComponents(ctx context.Context, clusterUID string, epoch int64) (int64, error) {
	if r.tx == nil {
		return 0, fmt.Errorf("cannot reassign component owners without a read-write transaction: %w", grpcerr.ErrFailedPrecondition)
	}
	stmt := spanner.Statement{SQL: takeOverOwnersSQL, Params: map[string]any{"clusterUid": clusterUID, "epoch": epoch}}
	count, err := r.tx.Update(ctx, stmt)
	if err != nil {
		return 0, fmt.Errorf("failed to reassign component owners: %w", err)
	}
	return count, nil
}

// readOnlyTransaction returns a new read-only transaction of r.client, so that all queries of one
// read come from one snapshot. The caller must close it.
func (r *Repository) readOnlyTransaction() (*spanner.ReadOnlyTransaction, error) {
	if r.client == nil {
		return nil, fmt.Errorf("spanner client must not be nil: %w", grpcerr.ErrFailedPrecondition)
	}
	return r.client.ReadOnlyTransaction(), nil
}

// single returns a single-use read-only transaction of r.client.
func (r *Repository) single() (*spanner.ReadOnlyTransaction, error) {
	if r.client == nil {
		return nil, fmt.Errorf("spanner client must not be nil: %w", grpcerr.ErrFailedPrecondition)
	}
	return r.client.Single(), nil
}

func getActiveComponent(ctx context.Context, q Queryable, canonicalName string) (*store.ComponentRecord, error) {
	stmt := spanner.Statement{SQL: selectComponentByActiveNameSQL, Params: map[string]any{"name": canonicalName}}
	subject := fmt.Sprintf("component %q", canonicalName)
	return queryComponentWithDeps(ctx, q, stmt, subject, subject)
}

func getComponentByID(ctx context.Context, q Queryable, componentID int64) (*store.ComponentRecord, error) {
	stmt := spanner.Statement{SQL: selectComponentByIDSQL, Params: map[string]any{"id": componentID}}
	return queryComponentWithDeps(ctx, q, stmt, fmt.Sprintf("component by ID %d", componentID), fmt.Sprintf("component ID %d", componentID))
}

// listActiveComponents reads all non-deleted components keyed by canonical name and their active dependencies through q.
func listActiveComponents(ctx context.Context, q Queryable) (map[string]*store.ComponentRecord, error) {
	if q == nil {
		return nil, fmt.Errorf("cannot list active components without a transaction: %w", grpcerr.ErrFailedPrecondition)
	}
	componentsByID, err := scanActiveComponents(ctx, q)
	if err != nil {
		return nil, fmt.Errorf("failed to list active components: %w", err)
	}
	if err := attachActiveDependencies(ctx, q, componentsByID); err != nil {
		return nil, fmt.Errorf("failed to list component dependencies: %w", err)
	}
	componentsByName := make(map[string]*store.ComponentRecord, len(componentsByID))
	for _, rec := range componentsByID {
		rec.Etag = store.GenerateEtag(rec)
		componentsByName[rec.Name] = rec
	}
	return componentsByName, nil
}

// scanActiveComponents reads all non-deleted components keyed by component ID, without their dependencies.
func scanActiveComponents(ctx context.Context, q Queryable) (map[int64]*store.ComponentRecord, error) {
	iter := q.Query(ctx, spanner.Statement{SQL: selectAllActiveComponentsSQL})
	defer iter.Stop()
	componentsByID := make(map[int64]*store.ComponentRecord)
	err := iter.Do(func(row *spanner.Row) error {
		rec, err := RowToComponentRecord(row)
		if err != nil {
			return err
		}
		componentsByID[rec.ComponentID] = rec
		return nil
	})
	return componentsByID, err
}

// attachActiveDependencies appends the names of the active dependencies of every component in
// componentsByID, sorted by name.
func attachActiveDependencies(ctx context.Context, q Queryable, componentsByID map[int64]*store.ComponentRecord) error {
	iter := q.Query(ctx, spanner.Statement{SQL: selectAllActiveDependenciesSQL})
	defer iter.Stop()
	return iter.Do(func(row *spanner.Row) error {
		var compID int64
		var depName string
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
	})
}

// querySnapshotRow returns the first snapshot row matched by stmt, or a NotFound status error when
// no snapshot exists for targetGen.
func querySnapshotRow(ctx context.Context, q Queryable, stmt spanner.Statement, canonicalName string, targetGen int64) (*spanner.Row, error) {
	if q == nil {
		return nil, fmt.Errorf("cannot query snapshot of component %q without a transaction: %w", canonicalName, grpcerr.ErrFailedPrecondition)
	}
	iter := q.Query(ctx, stmt)
	defer iter.Stop()
	row, err := iter.Next()
	if errors.Is(err, iterator.Done) {
		return nil, status.Errorf(codes.NotFound, "historical snapshot for generation %d of component %s not found", targetGen, canonicalName)
	}
	if err != nil {
		return nil, fmt.Errorf("failed to query experiment component snapshots: %w", err)
	}
	return row, nil
}

// decodeSnapshotRecord hydrates a historical record and its coordinates from a snapshot row.
// Timestamps are supplied by the caller because snapshots do not store their own; the record is
// built from scratch so no active component state leaks into the historical view.
func decodeSnapshotRecord(row *spanner.Row, canonicalName string, createTime, updateTime time.Time) (*store.ComponentRecord, *componentspb.ResolvedComponentCoordinates, error) {
	snapRec := &store.ComponentRecord{Name: canonicalName, CreateTime: createTime, UpdateTime: updateTime}
	var configProto, accessPayload []byte
	for _, col := range [...]struct {
		name string
		dst  any
	}{
		{"ComponentId", &snapRec.ComponentID}, {"LockedGeneration", &snapRec.Generation},
		{"ConfigProto", &configProto}, {"AccessPayload", &accessPayload},
	} {
		if err := row.ColumnByName(col.name, col.dst); err != nil {
			return nil, nil, fmt.Errorf("failed to read %s: %w", col.name, err)
		}
	}
	if err := data.UnmarshalConfigProto(configProto, snapRec); err != nil {
		return nil, nil, fmt.Errorf("failed to unmarshal historical config proto: %w", err)
	}
	if err := data.UnmarshalAccessStatus(accessPayload, snapRec); err != nil {
		return nil, nil, fmt.Errorf("failed to unmarshal historical access status: %w", err)
	}
	if snapRec.Status != nil {
		snapRec.State = snapRec.Status.State
		obsGen := snapRec.Status.ObservedGeneration
		snapRec.ObservedGeneration = &obsGen
	}
	snapRec.Etag = store.GenerateEtag(snapRec)
	var coords *componentspb.ResolvedComponentCoordinates
	if snapRec.Status != nil && snapRec.Status.Coordinates != nil {
		coords = store.CloneCoordinates(snapRec.Status.Coordinates)
	}
	return snapRec, coords, nil
}

// scanFenceColumns returns the fence holder from the columns that fenceColumnsSQL adds to a
// query, or nil before the first declaration (when the scalar subqueries are NULL).
func scanFenceColumns(row *spanner.Row) (*store.ClusterIdentity, error) {
	var uid, name spanner.NullString
	var epoch spanner.NullInt64
	if err := row.ColumnByName("FenceClusterUid", &uid); err != nil {
		return nil, fmt.Errorf("failed to decode the ownership fence column FenceClusterUid: %w", err)
	}
	if err := row.ColumnByName("FenceClusterName", &name); err != nil {
		return nil, fmt.Errorf("failed to decode the ownership fence column FenceClusterName: %w", err)
	}
	if err := row.ColumnByName("FenceEpoch", &epoch); err != nil {
		return nil, fmt.Errorf("failed to decode the ownership fence column FenceEpoch: %w", err)
	}
	if !uid.Valid {
		return nil, nil
	}
	return &store.ClusterIdentity{UID: uid.StringVal, Name: name.StringVal, Epoch: epoch.Int64}, nil
}

// queryComponentWithDeps reads the first component matched by stmt and its active dependencies through q.
func queryComponentWithDeps(ctx context.Context, q Queryable, stmt spanner.Statement, querySubject, depsSubject string) (*store.ComponentRecord, error) {
	if q == nil {
		return nil, fmt.Errorf("cannot query %s without a transaction: %w", querySubject, grpcerr.ErrFailedPrecondition)
	}
	iter := q.Query(ctx, stmt)
	defer iter.Stop()
	row, err := iter.Next()
	if err != nil {
		if err == iterator.Done {
			return nil, store.ErrComponentNotFound
		}
		return nil, fmt.Errorf("failed to query %s: %w", querySubject, err)
	}
	rec, err := RowToComponentRecord(row)
	if err != nil {
		return nil, err
	}
	if rec.Dependencies, err = fetchDependencies(ctx, q, rec.ComponentID); err != nil {
		return nil, fmt.Errorf("failed to fetch dependencies for %s: %w", depsSubject, err)
	}
	rec.Etag = store.GenerateEtag(rec)
	return rec, nil
}

// fetchDependencies returns the names of the registered, non-deleted dependencies of componentID, sorted by name.
func fetchDependencies(ctx context.Context, q Queryable, componentID int64) ([]string, error) {
	iter := q.Query(ctx, spanner.Statement{SQL: selectDependenciesByComponentIDSQL, Params: map[string]any{"id": componentID}})
	defer iter.Stop()
	var deps []string
	err := iter.Do(func(row *spanner.Row) error {
		var name string
		if err := row.Column(0, &name); err != nil {
			return err
		}
		deps = append(deps, name)
		return nil
	})
	return deps, err
}

// RowToComponentRecord decodes a Spanner Components row matching ComponentColumnsSQL into a ComponentRecord.
func RowToComponentRecord(row *spanner.Row) (*store.ComponentRecord, error) {
	rec := &store.ComponentRecord{}
	var class spanner.NullString
	var state int64
	var observedGen spanner.NullInt64
	var configProto, accessPayload []byte
	var sourceLastSeen spanner.NullTime
	for _, col := range [...]struct {
		name string
		dst  any
	}{
		{"ComponentId", &rec.ComponentID}, {"Name", &rec.Name}, {"ComponentClass", &class},
		{"ConfigProto", &configProto}, {"AccessPayload", &accessPayload}, {"State", &state},
		{"Generation", &rec.Generation}, {"ObservedGeneration", &observedGen}, {"CreateTime", &rec.CreateTime},
		{"UpdateTime", &rec.UpdateTime}, {"SourceLastSeenTime", &sourceLastSeen},
	} {
		if err := row.ColumnByName(col.name, col.dst); err != nil {
			return nil, fmt.Errorf("failed to read %s: %w", col.name, err)
		}
	}
	rec.State, rec.SourceLastSeenTime = componentspb.ComponentState(state), sourceLastSeen.Time
	if observedGen.Valid {
		rec.ObservedGeneration = &observedGen.Int64
	}
	var err error
	if rec.Owner, err = scanSourceOwner(row); err != nil {
		return nil, err
	}
	if err := decodeComponentPayloads(configProto, accessPayload, rec); err != nil {
		return nil, err
	}
	data.FinalizeComponentRecord(rec)
	return rec, nil
}

// scanSourceOwner reads the recorded owner of a Components row, or nil when SourceClusterUid is NULL.
func scanSourceOwner(row *spanner.Row) (*store.SourceOwner, error) {
	var uid, namespace, clusterUID spanner.NullString
	var generation, epoch spanner.NullInt64
	for _, col := range [...]struct {
		name string
		dst  any
	}{
		{"SourceUid", &uid}, {"SourceGeneration", &generation}, {"SourceNamespace", &namespace}, {"SourceClusterUid", &clusterUID}, {"SourceEpoch", &epoch},
	} {
		if err := row.ColumnByName(col.name, col.dst); err != nil {
			return nil, fmt.Errorf("failed to read %s: %w", col.name, err)
		}
	}
	if !clusterUID.Valid {
		return nil, nil
	}
	return &store.SourceOwner{UID: uid.StringVal, Generation: generation.Int64, Namespace: namespace.StringVal, ClusterUID: clusterUID.StringVal, Epoch: epoch.Int64}, nil
}

// decodeComponentPayloads decodes the stored ConfigProto of a component into rec.Spec and rec.Labels,
// and its AccessPayload into rec.Status.
func decodeComponentPayloads(configProto, accessPayload []byte, rec *store.ComponentRecord) error {
	if err := data.UnmarshalConfigProto(configProto, rec); err != nil {
		return fmt.Errorf("failed to unmarshal config proto: %w", err)
	}
	if err := data.UnmarshalAccessStatus(accessPayload, rec); err != nil {
		return fmt.Errorf("failed to unmarshal access status: %w", err)
	}
	return nil
}
