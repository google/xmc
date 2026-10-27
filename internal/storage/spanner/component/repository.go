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
	"fmt"

	"cloud.google.com/go/spanner"
	"google.golang.org/api/iterator"
	"google.golang.org/protobuf/proto"

	componentspb "xmanager_cloud/api/protos/components/v1alpha1"
	"xmanager_cloud/internal/grpcerr"
	"xmanager_cloud/internal/storage"
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
)

var (
	_ storage.ComponentRepository   = (*Repository)(nil)
	_ storage.ComponentTxRepository = (*TxRepository)(nil)
)

// Queryable is implemented by Spanner single-use reads, read-only transactions, and read-write transactions.
type Queryable interface {
	Query(ctx context.Context, statement spanner.Statement) *spanner.RowIterator
}

// Repository reads component catalog records from Cloud Spanner, each read from its own read-only transaction.
type Repository struct {
	client *spanner.Client
}

// TxRepository reads component catalog records within a bound Spanner transaction.
type TxRepository struct {
	q Queryable
}

// NewRepository creates a Spanner-backed component Repository.
func NewRepository(client *spanner.Client) *Repository {
	return &Repository{client: client}
}

// WithTx returns a repository bound to the given Spanner read-only or read-write transaction.
func (r *Repository) WithTx(q Queryable) *TxRepository {
	return &TxRepository{q: q}
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

// readOnlyTransaction returns a new read-only transaction of r.client, so that all queries of one
// read come from one snapshot. The caller must close it.
func (r *Repository) readOnlyTransaction() (*spanner.ReadOnlyTransaction, error) {
	if r.client == nil {
		return nil, fmt.Errorf("spanner client must not be nil: %w", grpcerr.ErrFailedPrecondition)
	}
	return r.client.ReadOnlyTransaction(), nil
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
	finalizeComponentRecord(rec)
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
	if err := UnmarshalConfigProto(configProto, rec); err != nil {
		return fmt.Errorf("failed to unmarshal config proto: %w", err)
	}
	if err := UnmarshalAccessStatus(accessPayload, rec); err != nil {
		return fmt.Errorf("failed to unmarshal access status: %w", err)
	}
	return nil
}

// finalizeComponentRecord completes a decoded record: a DELETED record gets its UpdateTime as
// DeleteTime, and Etag is derived.
func finalizeComponentRecord(rec *store.ComponentRecord) {
	if rec.State == componentspb.ComponentState_COMPONENT_STATE_DELETED {
		t := rec.UpdateTime
		rec.DeleteTime = &t
	}
	rec.Etag = store.GenerateEtag(rec)
}

// UnmarshalConfigProto decodes a serialized Component or ComponentSpec proto into rec.Spec and rec.Labels.
func UnmarshalConfigProto(configBytes []byte, rec *store.ComponentRecord) error {
	if len(configBytes) == 0 {
		return nil
	}
	var comp componentspb.Component
	if err := proto.Unmarshal(configBytes, &comp); err == nil && (comp.Spec != nil || len(comp.Labels) > 0) {
		rec.Spec, rec.Labels = comp.Spec, store.CopyMap(comp.Labels)
		return nil
	}
	rec.Spec = &componentspb.ComponentSpec{}
	return proto.Unmarshal(configBytes, rec.Spec)
}

// UnmarshalAccessStatus decodes a serialized ComponentAccessStatus proto into rec.Status.
func UnmarshalAccessStatus(accessBytes []byte, rec *store.ComponentRecord) error {
	if len(accessBytes) == 0 {
		rec.Status = nil
		return nil
	}
	rec.Status = &componentspb.ComponentAccessStatus{}
	return proto.Unmarshal(accessBytes, rec.Status)
}
