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

// Package component contains the PostgreSQL repository implementation for CMS component catalog records.
package component

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	componentspb "xmanager_cloud/api/protos/components/v1alpha1"
	"xmanager_cloud/internal/grpcerr"
	"xmanager_cloud/internal/safesql"
	"xmanager_cloud/internal/storage"
	"xmanager_cloud/internal/storage/data"
	"xmanager_cloud/internal/storage/postgres/pgdb"
	"xmanager_cloud/pkg/components/store"
)

// entityName names component catalog rows in translated storage errors.
const entityName = "component"

const (
	// componentColumnsSQL lists the Components columns that componentColumns scans.
	componentColumnsSQL = `c.ComponentId, c.Name, c.ConfigProto, c.AccessPayload, c.State, c.Generation, c.ObservedGeneration, c.CreateTime, c.UpdateTime, c.SourceUid, c.SourceGeneration, c.SourceNamespace, c.SourceClusterUid, c.SourceEpoch, c.SourceLastSeenTime`
	// activeDependenciesSQL lists the registered, non-deleted dependencies of c in byte order, as
	// Spanner sorts strings, so that one statement reads a record and its edges from one snapshot.
	activeDependenciesSQL = `ARRAY(SELECT dep.Name FROM ComponentDependencies cd JOIN Components dep ON cd.DependencyComponentId = dep.ComponentId WHERE cd.ComponentId = c.ComponentId AND dep.ActiveName IS NOT NULL ORDER BY dep.Name COLLATE "C")`
	selectComponentsSQL   = `SELECT ` + componentColumnsSQL + `, ` + activeDependenciesSQL
)

var (
	selectComponentByActiveNameSQL = safesql.New(selectComponentsSQL + ` FROM Components c WHERE c.ActiveName = $1`)
	selectComponentByIDSQL         = safesql.New(selectComponentsSQL + ` FROM Components c WHERE c.ComponentId = $1 AND c.State != 5`)
	selectActiveComponentsSQL      = safesql.New(selectComponentsSQL + ` FROM Components c WHERE c.ActiveName IS NOT NULL`)
	selectComponentWithFenceSQL    = safesql.New(selectComponentsSQL + `, f.ActiveClusterUid, f.ActiveClusterName, f.Epoch FROM Components c LEFT JOIN ComponentOwnershipFence f ON f.FenceId = 1 WHERE c.ActiveName = $1`)
	selectSnapshotByIDSQL          = safesql.New(`SELECT ComponentId, LockedGeneration, ConfigProto, AccessPayload FROM ExperimentComponentSnapshots WHERE ComponentId = $1 AND LockedGeneration = $2 LIMIT 1`)
	selectSnapshotByNameSQL        = safesql.New(`SELECT s.ComponentId, s.LockedGeneration, s.ConfigProto, s.AccessPayload, c.CreateTime, c.UpdateTime FROM ExperimentComponentSnapshots s JOIN Components c ON s.ComponentId = c.ComponentId WHERE c.Name = $1 AND s.LockedGeneration = $2 ORDER BY c.CreateTime DESC, c.ComponentId DESC LIMIT 1`)
	selectOwnershipGaugesSQL       = safesql.New(`SELECT (SELECT Epoch FROM ComponentOwnershipFence WHERE FenceId = 1), (SELECT LastSeenTime FROM ComponentOwnershipFence WHERE FenceId = 1), (SELECT COUNT(*) FROM Components WHERE ActiveName IS NOT NULL AND SourceClusterUid IS NOT NULL AND SourceLastSeenTime <= $1)`)
	selectFenceSQL                 = safesql.New(`SELECT ActiveClusterUid, ActiveClusterName, Epoch FROM ComponentOwnershipFence WHERE FenceId = 1`)
)

// Write statements. Commit-time columns use now(), which returns the transaction timestamp in
// PostgreSQL and matches the timestamp returned by postgres.Storage.RunInSerializableTransaction.
var (
	insertComponentSQL = safesql.New(`INSERT INTO Components (ComponentId, Name, ComponentClass, ConfigProto, AccessPayload, State, Generation, CreateTime, UpdateTime)
VALUES (@componentid, @name, @componentclass, @configproto, @accesspayload, @state, @generation, now(), now())`)
	updateComponentIntentSQL      = safesql.New(`UPDATE Components SET ComponentClass = @componentclass, ConfigProto = @configproto, AccessPayload = @accesspayload, State = @state, Generation = @generation, UpdateTime = now() WHERE ComponentId = @componentid`)
	deleteDependenciesSQL         = safesql.New(`DELETE FROM ComponentDependencies WHERE ComponentId = $1`)
	insertDependenciesSQL         = safesql.New(`INSERT INTO ComponentDependencies (ComponentId, DependencyComponentId) SELECT @componentid, unnest(@depids::BIGINT[])`)
	updateComponentStateSQL       = safesql.New(`UPDATE Components SET State = @state, AccessPayload = @accesspayload, UpdateTime = now() WHERE ComponentId = @componentid`)
	updateComponentObservationSQL = safesql.New(`UPDATE Components SET ObservedGeneration = @observedgeneration, AccessPayload = @accesspayload, State = COALESCE(@state::BIGINT, State), UpdateTime = now() WHERE ComponentId = @componentid`)
	softDeleteComponentSQL        = safesql.New(`UPDATE Components SET ConfigProto = @configproto, AccessPayload = @accesspayload, State = 5, UpdateTime = now() WHERE ComponentId = @componentid`)
	updateComponentOwnerSQL       = safesql.New(`UPDATE Components SET SourceUid = @sourceuid, SourceGeneration = @sourcegeneration, SourceNamespace = @sourcenamespace, SourceClusterUid = @sourceclusteruid, SourceEpoch = @sourceepoch, SourceLastSeenTime = now() WHERE ComponentId = @componentid`)
	reassignOwnedComponentsSQL    = safesql.New(`UPDATE Components SET SourceClusterUid = @sourceclusteruid, SourceEpoch = @sourceepoch, SourceUid = NULL, SourceLastSeenTime = now() WHERE ActiveName IS NOT NULL AND SourceClusterUid IS NOT NULL`)
	refreshFenceSQL               = safesql.New(`UPDATE ComponentOwnershipFence SET LastSeenTime = now(), ActiveClusterName = COALESCE(@activeclustername::TEXT, ActiveClusterName) WHERE FenceId = 1`)
	replaceFenceSQL               = safesql.New(`INSERT INTO ComponentOwnershipFence (FenceId, ActiveClusterUid, ActiveClusterName, Epoch, DeclaredTime, LastSeenTime)
VALUES (1, @activeclusteruid, @activeclustername, @epoch, now(), now())
ON CONFLICT (FenceId) DO UPDATE SET ActiveClusterUid = EXCLUDED.ActiveClusterUid, ActiveClusterName = EXCLUDED.ActiveClusterName, Epoch = EXCLUDED.Epoch,
  DeclaredTime = EXCLUDED.DeclaredTime, LastSeenTime = EXCLUDED.LastSeenTime`)
)

var (
	_ storage.ComponentRepository   = (*Repository)(nil)
	_ storage.ComponentTxRepository = (*TxRepository)(nil)
)

// reader implements the component catalog reads shared by Repository and TxRepository. Each read is
// a single statement, so it reads from one snapshot even outside a transaction.
type reader struct {
	db pgdb.DBTX
}

// Repository reads component catalog records from PostgreSQL through a connection pool.
type Repository struct {
	reader
	pool *pgxpool.Pool
}

// TxRepository reads and writes component catalog records within one transaction: a read-write
// transaction of RunInSerializableTransaction or a read-only snapshot of Repository.WithReadOnlySnapshot.
type TxRepository struct {
	reader
}

// NewRepository creates a new PostgreSQL Component repository instance bound to pool.
func NewRepository(pool *pgxpool.Pool) *Repository {
	return &Repository{reader: reader{db: pgdb.FromPool(pool)}, pool: pool}
}

// WithTx returns a repository bound to the transaction tx.
func (r *Repository) WithTx(tx pgdb.DBTX) *TxRepository {
	return &TxRepository{reader: reader{db: tx}}
}

// WithReadOnlySnapshot runs fn with a repository bound to a REPEATABLE READ READ ONLY transaction,
// so that all its reads come from one snapshot.
func (r *Repository) WithReadOnlySnapshot(ctx context.Context, fn func(ctx context.Context, repo storage.ComponentTxRepository) error) error {
	if fn == nil {
		return pgdb.WrapStatusError("transaction callback must not be nil", grpcerr.ErrInvalidArgument, nil)
	}
	return pgdb.RunInReadOnlyTx(ctx, r.pool, func(ctx context.Context, tx pgx.Tx) error {
		return fn(ctx, r.WithTx(tx))
	})
}

// GetActiveComponent implements storage.ComponentRepository.
func (r reader) GetActiveComponent(ctx context.Context, canonicalName string) (*store.ComponentRecord, error) {
	return r.queryComponent(ctx, selectComponentByActiveNameSQL, canonicalName)
}

// GetComponentByID implements storage.ComponentRepository.
func (r reader) GetComponentByID(ctx context.Context, componentID int64) (*store.ComponentRecord, error) {
	return r.queryComponent(ctx, selectComponentByIDSQL, componentID)
}

// ListActiveComponents implements storage.ComponentRepository.
func (r reader) ListActiveComponents(ctx context.Context) (map[string]*store.ComponentRecord, error) {
	if err := r.checkDB(); err != nil {
		return nil, err
	}
	rows, err := r.db.Query(ctx, selectActiveComponentsSQL.String())
	if err != nil {
		return nil, pgdb.TranslateError(err, entityName)
	}
	defer rows.Close()
	catalog := make(map[string]*store.ComponentRecord)
	for rows.Next() {
		var cols componentColumns
		if err := rows.Scan(cols.dest()...); err != nil {
			return nil, pgdb.TranslateError(err, entityName)
		}
		rec, err := cols.record()
		if err != nil {
			return nil, err
		}
		catalog[rec.Name] = rec
	}
	if err := rows.Err(); err != nil {
		return nil, pgdb.TranslateError(err, entityName)
	}
	return catalog, nil
}

// GetComponentWithFence implements storage.ComponentRepository with one statement that joins the
// fence row, so the record and the fence come from one snapshot.
func (r *Repository) GetComponentWithFence(ctx context.Context, canonicalName string) (*store.ComponentRecord, *store.ClusterIdentity, error) {
	if err := r.checkDB(); err != nil {
		return nil, nil, err
	}
	var cols componentColumns
	var fence fenceColumns
	if err := r.db.QueryRow(ctx, selectComponentWithFenceSQL.String(), canonicalName).Scan(append(cols.dest(), fence.dest()...)...); err != nil {
		return nil, nil, translateLookupError(err)
	}
	rec, err := cols.record()
	if err != nil {
		return nil, nil, err
	}
	return rec, fence.identity(), nil
}

// ReadOwnershipGauges implements storage.ComponentRepository with one statement of scalar subqueries.
func (r *Repository) ReadOwnershipGauges(ctx context.Context, cutoff time.Time) (store.OwnershipGauges, error) {
	if err := r.checkDB(); err != nil {
		return store.OwnershipGauges{}, err
	}
	var epoch pgtype.Int8
	var lastSeen pgtype.Timestamptz
	var g store.OwnershipGauges
	if err := r.db.QueryRow(ctx, selectOwnershipGaugesSQL.String(), cutoff).Scan(&epoch, &lastSeen, &g.StaleOwners); err != nil {
		return store.OwnershipGauges{}, pgdb.TranslateError(err, "ownership gauges")
	}
	g.FenceDeclared, g.FenceEpoch = epoch.Valid, epoch.Int64
	if lastSeen.Valid {
		g.FenceLastSeenTime = lastSeen.Time.UTC()
	}
	return g, nil
}

// GetSnapshotByID implements storage.ComponentTxRepository.
func (r *TxRepository) GetSnapshotByID(ctx context.Context, componentID, generation int64, canonicalName string, createTime, updateTime time.Time) (*store.ComponentRecord, *componentspb.ResolvedComponentCoordinates, error) {
	if err := r.checkDB(); err != nil {
		return nil, nil, err
	}
	var snap data.ComponentSnapshot
	err := r.db.QueryRow(ctx, selectSnapshotByIDSQL.String(), componentID, generation).Scan(&snap.ComponentID, &snap.LockedGeneration, &snap.ConfigProto, &snap.AccessPayload)
	if err != nil {
		return nil, nil, translateSnapshotError(err, canonicalName, generation)
	}
	return data.DecodeComponentSnapshot(snap, canonicalName, createTime.UTC(), updateTime.UTC())
}

// GetSnapshotByName implements storage.ComponentTxRepository.
func (r *TxRepository) GetSnapshotByName(ctx context.Context, canonicalName string, generation int64) (*store.ComponentRecord, *componentspb.ResolvedComponentCoordinates, error) {
	if err := r.checkDB(); err != nil {
		return nil, nil, err
	}
	var snap data.ComponentSnapshot
	var createTime, updateTime time.Time
	err := r.db.QueryRow(ctx, selectSnapshotByNameSQL.String(), canonicalName, generation).Scan(&snap.ComponentID, &snap.LockedGeneration, &snap.ConfigProto, &snap.AccessPayload, &createTime, &updateTime)
	if err != nil {
		return nil, nil, translateSnapshotError(err, canonicalName, generation)
	}
	return data.DecodeComponentSnapshot(snap, canonicalName, createTime.UTC(), updateTime.UTC())
}

// ReadFence returns the ownership fence holder, or nil before the first declaration. CMS
// transactions run at SERIALIZABLE isolation, so a concurrent change to the row, or its insertion,
// aborts one of the transactions instead of being lost.
func (r *TxRepository) ReadFence(ctx context.Context) (*store.ClusterIdentity, error) {
	if err := r.checkDB(); err != nil {
		return nil, err
	}
	var fence fenceColumns
	err := r.db.QueryRow(ctx, selectFenceSQL.String()).Scan(fence.dest()...)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, pgdb.TranslateError(err, "ownership fence")
	}
	return fence.identity(), nil
}

func (r reader) checkDB() error {
	if r.db == nil {
		return pgdb.WrapStatusError("postgres database handle must not be nil", grpcerr.ErrFailedPrecondition, nil)
	}
	return nil
}

// queryComponent returns the record that query, with its single argument arg, selects.
func (r reader) queryComponent(ctx context.Context, query safesql.TrustedSQLString, arg any) (*store.ComponentRecord, error) {
	if err := r.checkDB(); err != nil {
		return nil, err
	}
	var cols componentColumns
	if err := r.db.QueryRow(ctx, query.String(), arg).Scan(cols.dest()...); err != nil {
		return nil, translateLookupError(err)
	}
	return cols.record()
}

// translateLookupError returns store.ErrComponentNotFound itself for a missing component, as the
// storage.ComponentRepository contract requires, and translates any other error.
func translateLookupError(err error) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return store.ErrComponentNotFound
	}
	return pgdb.TranslateError(err, entityName)
}

// translateSnapshotError returns a NotFound status error for a missing snapshot and translates any
// other error.
func translateSnapshotError(err error, canonicalName string, generation int64) error {
	if errors.Is(err, pgx.ErrNoRows) {
		return status.Errorf(codes.NotFound, "historical snapshot for generation %d of component %s not found", generation, canonicalName)
	}
	return pgdb.TranslateError(err, "component snapshot")
}

// componentColumns receives one row of selectComponentsSQL.
type componentColumns struct {
	rec                           store.ComponentRecord
	state                         int64
	configProto, accessPayload    []byte
	observedGen, sourceGen, epoch pgtype.Int8
	sourceUID, namespace, cluster pgtype.Text
	createTime, updateTime        time.Time
	lastSeen                      pgtype.Timestamptz
}

// dest returns the scan destinations of the selectComponentsSQL columns, in order.
func (c *componentColumns) dest() []any {
	return []any{&c.rec.ComponentID, &c.rec.Name, &c.configProto, &c.accessPayload, &c.state, &c.rec.Generation, &c.observedGen,
		&c.createTime, &c.updateTime, &c.sourceUID, &c.sourceGen, &c.namespace, &c.cluster, &c.epoch, &c.lastSeen, &c.rec.Dependencies}
}

// record decodes the scanned columns into a finalized record with UTC timestamps. As in Spanner,
// the record has an owner only when SourceClusterUid is set, and NULL owner columns read as zero
// values.
func (c *componentColumns) record() (*store.ComponentRecord, error) {
	rec := c.rec
	rec.State = componentspb.ComponentState(c.state)
	rec.CreateTime, rec.UpdateTime = c.createTime.UTC(), c.updateTime.UTC()
	if c.lastSeen.Valid {
		rec.SourceLastSeenTime = c.lastSeen.Time.UTC()
	}
	if c.observedGen.Valid {
		observedGen := c.observedGen.Int64
		rec.ObservedGeneration = &observedGen
	}
	if c.cluster.Valid {
		rec.Owner = &store.SourceOwner{UID: c.sourceUID.String, Generation: c.sourceGen.Int64, Namespace: c.namespace.String, ClusterUID: c.cluster.String, Epoch: c.epoch.Int64}
	}
	if err := data.DecodeComponentPayloads(c.configProto, c.accessPayload, &rec); err != nil {
		return nil, err
	}
	data.FinalizeComponentRecord(&rec)
	return &rec, nil
}

// fenceColumns receives the ownership fence holder columns, which are NULL before the first declaration.
type fenceColumns struct {
	uid, name pgtype.Text
	epoch     pgtype.Int8
}

// dest returns the scan destinations of the holder UID, name and epoch columns, in order.
func (f *fenceColumns) dest() []any {
	return []any{&f.uid, &f.name, &f.epoch}
}

// identity returns the scanned fence holder, or nil when the fence row is absent.
func (f *fenceColumns) identity() *store.ClusterIdentity {
	if !f.uid.Valid {
		return nil
	}
	return &store.ClusterIdentity{UID: f.uid.String, Name: f.name.String, Epoch: f.epoch.Int64}
}

// RefreshFence updates LastSeenTime to the commit time and, when newName is non-nil,
// ActiveClusterName within the transaction.
func (r *TxRepository) RefreshFence(ctx context.Context, newName *string) error {
	if err := r.checkDB(); err != nil {
		return err
	}
	var name pgtype.Text
	if newName != nil {
		name = pgtype.Text{String: *newName, Valid: true}
	}
	return r.execUpdate(ctx, refreshFenceSQL, "ownership fence", pgx.StrictNamedArgs{"activeclustername": name})
}

// ReplaceFence writes cluster as the ownership fence holder with DeclaredTime and LastSeenTime at
// the transaction's commit time.
func (r *TxRepository) ReplaceFence(ctx context.Context, cluster store.ClusterIdentity) error {
	if err := r.checkDB(); err != nil {
		return err
	}
	return r.exec(ctx, replaceFenceSQL, "ownership fence", pgx.StrictNamedArgs{
		"activeclusteruid": cluster.UID, "activeclustername": cluster.Name, "epoch": cluster.Epoch,
	})
}

// ReassignOwnedComponents reassigns active owned components to the declaring cluster without an
// owner UID, leaving UpdateTime unchanged, and returns how many it reassigned.
func (r *TxRepository) ReassignOwnedComponents(ctx context.Context, clusterUID string, epoch int64) (int64, error) {
	if err := r.checkDB(); err != nil {
		return 0, err
	}
	tag, err := r.db.Exec(ctx, reassignOwnedComponentsSQL.String(), pgx.StrictNamedArgs{
		"sourceclusteruid": clusterUID, "sourceepoch": epoch,
	})
	if err != nil {
		return 0, pgdb.TranslateError(err, entityName)
	}
	return tag.RowsAffected(), nil
}

// InsertComponent inserts a new component record, created at the commit time, and then its
// dependency edges, whose foreign keys PostgreSQL checks immediately.
func (r *TxRepository) InsertComponent(ctx context.Context, rec *store.ComponentRecord, depIDs map[string]int64) error {
	configBytes, accessBytes, err := r.prepareRecordWrite(rec)
	if err != nil {
		return err
	}
	ids, err := resolveDependencyIDs(rec.Dependencies, depIDs)
	if err != nil {
		return err
	}
	if err := r.exec(ctx, insertComponentSQL, entityName, pgx.StrictNamedArgs{
		"componentid": rec.ComponentID, "name": rec.Name, "componentclass": componentClass(rec.Spec),
		"configproto": configBytes, "accesspayload": accessBytes, "state": int64(rec.State), "generation": rec.Generation,
	}); err != nil {
		return err
	}
	return r.insertDependencies(ctx, rec.ComponentID, ids)
}

// UpdateComponentIntent updates an existing component's intent and replaces its dependencies.
func (r *TxRepository) UpdateComponentIntent(ctx context.Context, rec *store.ComponentRecord, depIDs map[string]int64) error {
	configBytes, accessBytes, err := r.prepareRecordWrite(rec)
	if err != nil {
		return err
	}
	ids, err := resolveDependencyIDs(rec.Dependencies, depIDs)
	if err != nil {
		return err
	}
	if err := r.execUpdate(ctx, updateComponentIntentSQL, entityName, pgx.StrictNamedArgs{
		"componentid": rec.ComponentID, "componentclass": componentClass(rec.Spec),
		"configproto": configBytes, "accesspayload": accessBytes, "state": int64(rec.State), "generation": rec.Generation,
	}); err != nil {
		return err
	}
	if err := r.exec(ctx, deleteDependenciesSQL, entityName, rec.ComponentID); err != nil {
		return err
	}
	return r.insertDependencies(ctx, rec.ComponentID, ids)
}

// UpdateComponentState updates a component's state and access status payload.
func (r *TxRepository) UpdateComponentState(ctx context.Context, componentID int64, state componentspb.ComponentState, access *componentspb.ComponentAccessStatus) error {
	if err := r.checkDB(); err != nil {
		return err
	}
	accessBytes, err := data.MarshalAccessStatus(access)
	if err != nil {
		return err
	}
	return r.execUpdate(ctx, updateComponentStateSQL, entityName, pgx.StrictNamedArgs{
		"componentid": componentID, "state": int64(state), "accesspayload": accessBytes,
	})
}

// UpdateComponentObservation updates a component's observed generation, access status, and optional state.
func (r *TxRepository) UpdateComponentObservation(ctx context.Context, rec *store.ComponentRecord, updateState bool) error {
	if err := r.checkRecordWrite(rec); err != nil {
		return err
	}
	accessBytes, err := data.MarshalAccessStatus(rec.Status)
	if err != nil {
		return err
	}
	var observedGen pgtype.Int8
	if rec.ObservedGeneration != nil {
		observedGen = pgtype.Int8{Int64: *rec.ObservedGeneration, Valid: true}
	}
	state := pgtype.Int8{Int64: int64(rec.State), Valid: updateState}
	return r.execUpdate(ctx, updateComponentObservationSQL, entityName, pgx.StrictNamedArgs{
		"componentid": rec.ComponentID, "observedgeneration": observedGen, "accesspayload": accessBytes, "state": state,
	})
}

// SoftDeleteComponent marks a component as DELETED and optionally updates its owner columns.
func (r *TxRepository) SoftDeleteComponent(ctx context.Context, rec *store.ComponentRecord, owner *store.SourceOwner) error {
	configBytes, accessBytes, err := r.prepareRecordWrite(rec)
	if err != nil {
		return err
	}
	if err := r.execUpdate(ctx, softDeleteComponentSQL, entityName, pgx.StrictNamedArgs{
		"componentid": rec.ComponentID, "configproto": configBytes, "accesspayload": accessBytes,
	}); err != nil {
		return err
	}
	return r.WriteComponentOwner(ctx, rec.ComponentID, owner)
}

// WriteComponentOwner updates a component's source owner columns, with SourceLastSeenTime at the
// commit time, and leaves UpdateTime unchanged. A nil owner is a no-op.
func (r *TxRepository) WriteComponentOwner(ctx context.Context, componentID int64, owner *store.SourceOwner) error {
	if err := r.checkDB(); err != nil {
		return err
	}
	if owner == nil {
		return nil
	}
	return r.execUpdate(ctx, updateComponentOwnerSQL, entityName, pgx.StrictNamedArgs{
		"componentid": componentID, "sourceuid": owner.UID, "sourcegeneration": owner.Generation,
		"sourcenamespace": owner.Namespace, "sourceclusteruid": owner.ClusterUID, "sourceepoch": owner.Epoch,
	})
}

func (r *TxRepository) checkRecordWrite(rec *store.ComponentRecord) error {
	if err := r.checkDB(); err != nil {
		return err
	}
	if rec == nil {
		return pgdb.WrapStatusError("component record must not be nil", grpcerr.ErrInvalidArgument, nil)
	}
	return nil
}

func (r *TxRepository) prepareRecordWrite(rec *store.ComponentRecord) ([]byte, []byte, error) {
	if err := r.checkRecordWrite(rec); err != nil {
		return nil, nil, err
	}
	configBytes, accessBytes, err := data.MarshalComponentPayloads(rec.Name, rec.Spec, rec.Labels, rec.Status)
	if err != nil {
		return nil, nil, err
	}
	if configBytes == nil {
		configBytes = []byte{}
	}
	return configBytes, accessBytes, nil
}

func (r *TxRepository) insertDependencies(ctx context.Context, componentID int64, ids []int64) error {
	if len(ids) == 0 {
		return nil
	}
	return r.exec(ctx, insertDependenciesSQL, entityName, pgx.StrictNamedArgs{
		"componentid": componentID, "depids": ids,
	})
}

func resolveDependencyIDs(deps []string, depIDs map[string]int64) ([]int64, error) {
	if len(deps) == 0 {
		return nil, nil
	}
	ids := make([]int64, 0, len(deps))
	for _, dep := range deps {
		id, ok := depIDs[dep]
		if !ok {
			return nil, pgdb.WrapStatusError(fmt.Sprintf("missing component ID for dependency %q", dep), grpcerr.ErrFailedPrecondition, nil)
		}
		ids = append(ids, id)
	}
	return ids, nil
}

// exec runs a write statement within the transaction and translates its error, keeping
// serialization failures and deadlocks retryable.
func (r *TxRepository) exec(ctx context.Context, query safesql.TrustedSQLString, entity string, args ...any) error {
	_, err := r.db.Exec(ctx, query.String(), args...)
	return pgdb.TranslateError(err, entity)
}

// execUpdate runs a single-row UPDATE and returns NotFound when no row matched, matching Spanner's
// UpdateMap semantics.
func (r *TxRepository) execUpdate(ctx context.Context, query safesql.TrustedSQLString, entity string, args ...any) error {
	tag, err := r.db.Exec(ctx, query.String(), args...)
	if err != nil {
		return pgdb.TranslateError(err, entity)
	}
	if tag.RowsAffected() == 0 {
		return pgdb.TranslateError(pgx.ErrNoRows, entity)
	}
	return nil
}

// componentClass returns the stored ComponentClass of spec, NULL when it has none.
func componentClass(spec *componentspb.ComponentSpec) pgtype.Text {
	class := data.DetermineComponentClass(spec)
	return pgtype.Text{String: class, Valid: class != ""}
}
