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

// Package postgres provides a PostgreSQL implementation of store.Store.
package postgres

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgtype"
	"github.com/jackc/pgx/v5/pgxpool"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"

	componentspb "xmanager_cloud/api/protos/components/v1alpha1"
	"xmanager_cloud/internal/grpcerr"
	"xmanager_cloud/internal/storage/postgres/pgdb"
	"xmanager_cloud/pkg/components/semver"
	"xmanager_cloud/pkg/components/store"
	"xmanager_cloud/pkg/components/store/lifecycle"
)

// Ensure PostgresStore implements Store interface at compile time.
var _ store.Store = (*PostgresStore)(nil)

const (
	componentColumnsSQL = `ComponentId, Name, ConfigProto, AccessPayload, State, Generation, ObservedGeneration, CreateTime, UpdateTime, SourceUid, SourceGeneration, SourceNamespace, SourceClusterUid, SourceEpoch, SourceLastSeenTime`

	selectComponentByActiveNameSQL     = `SELECT ` + componentColumnsSQL + ` FROM Components WHERE ActiveName = $1 LIMIT 1`
	selectComponentByIDSQL             = `SELECT ` + componentColumnsSQL + ` FROM Components WHERE ComponentId = $1 AND State != 5 LIMIT 1`
	selectAllActiveComponentsSQL       = `SELECT ` + componentColumnsSQL + ` FROM Components WHERE ActiveName IS NOT NULL ORDER BY Name ASC`
	selectDependenciesByComponentIDSQL = `SELECT dep.Name FROM ComponentDependencies cd JOIN Components dep ON cd.DependencyComponentId = dep.ComponentId WHERE cd.ComponentId = $1 AND dep.ActiveName IS NOT NULL ORDER BY dep.Name ASC`
	selectAllActiveDependenciesSQL     = `SELECT cd.ComponentId, dep.Name FROM ComponentDependencies cd JOIN Components dep ON cd.DependencyComponentId = dep.ComponentId JOIN Components parent ON cd.ComponentId = parent.ComponentId WHERE parent.ActiveName IS NOT NULL AND dep.ActiveName IS NOT NULL ORDER BY cd.ComponentId, dep.Name ASC`
)

// PostgresStore provides a PostgreSQL-backed production implementation of store.Store.
type PostgresStore struct {
	pool              *pgxpool.Pool
	beforeIntentWrite func()
}

// NewPostgresStore creates a new PostgreSQL-backed component store.
func NewPostgresStore(pool *pgxpool.Pool) (*PostgresStore, error) {
	if pool == nil {
		return nil, pgdb.WrapStatusError("postgres pool must not be nil", grpcerr.ErrInvalidArgument, nil)
	}
	return &PostgresStore{
		pool: pool,
	}, nil
}

// GetComponent retrieves a component record by canonical or full resource name.
func (s *PostgresStore) GetComponent(ctx context.Context, name string) (*store.ComponentRecord, error) {
	canonicalName, err := store.ValidateResourceName(name)
	if err != nil {
		return nil, err
	}
	var rec *store.ComponentRecord
	err = s.withReadOnlyTx(ctx, func(tx pgx.Tx) error {
		var err error
		rec, err = queryComponentWithDeps(ctx, tx, canonicalName)
		return err
	})
	if err != nil {
		return nil, err
	}
	return store.CloneRecord(rec), nil
}

// GetComponentByID retrieves a component record by its unique database identifier.
func (s *PostgresStore) GetComponentByID(ctx context.Context, componentID int64) (*store.ComponentRecord, error) {
	if componentID <= 0 {
		return nil, status.Error(codes.InvalidArgument, "invalid component ID")
	}
	var rec *store.ComponentRecord
	err := s.withReadOnlyTx(ctx, func(tx pgx.Tx) error {
		var err error
		rec, err = querySingleComponentWithDeps(ctx, tx, selectComponentByIDSQL, componentID)
		return err
	})
	if err != nil {
		return nil, err
	}
	return store.CloneRecord(rec), nil
}

// ListComponents returns all non-deleted components in the catalog sorted by name.
func (s *PostgresStore) ListComponents(ctx context.Context) ([]*store.ComponentRecord, error) {
	var catalog map[string]*store.ComponentRecord
	err := s.withReadOnlyTx(ctx, func(tx pgx.Tx) error {
		var err error
		catalog, err = s.loadActiveCatalog(ctx, tx)
		return err
	})
	if err != nil {
		return nil, err
	}
	results := make([]*store.ComponentRecord, 0, len(catalog))
	for _, rec := range catalog {
		if !store.IsDeletedRecord(rec) {
			results = append(results, store.CloneRecord(rec))
		}
	}
	sort.Slice(results, func(i, j int) bool { return results[i].Name < results[j].Name })
	return results, nil
}

// ValidateComponentRequirement checks if a required component type and version exists and is active.
func (s *PostgresStore) ValidateComponentRequirement(ctx context.Context, name string, reqType componentspb.ComponentType, minVersion string) (*store.RequirementValidationResult, error) {
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
func (s *PostgresStore) GetPlatformHealth(ctx context.Context, filter string, pageSize int32, pageToken string) (*componentspb.GetPlatformHealthResponse, error) {
	return nil, status.Error(codes.Unimplemented, "postgres component store: GetPlatformHealth is not implemented")
}

func (s *PostgresStore) withReadOnlyTx(ctx context.Context, fn func(tx pgx.Tx) error) error {
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return fmt.Errorf("failed to begin read-only transaction: %w", err)
	}
	defer func() {
		rollbackCtx, cancel := context.WithTimeout(context.Background(), txRollbackTimeout)
		defer cancel()
		_ = tx.Rollback(rollbackCtx)
	}()
	return fn(tx)
}

func queryComponentWithDeps(ctx context.Context, q pgdb.DBTX, canonicalName string) (*store.ComponentRecord, error) {
	return querySingleComponentWithDeps(ctx, q, selectComponentByActiveNameSQL, canonicalName)
}

func querySingleComponentWithDeps(ctx context.Context, q pgdb.DBTX, querySQL string, arg any) (*store.ComponentRecord, error) {
	rec, err := scanComponentRecord(q.QueryRow(ctx, querySQL, arg))
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, store.ErrComponentNotFound
		}
		return nil, fmt.Errorf("failed to query component (%v): %w", arg, err)
	}
	deps, err := fetchDependencies(ctx, q, rec.ComponentID)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch dependencies for component %q: %w", rec.Name, err)
	}
	rec.Dependencies = deps
	rec.Etag = store.GenerateEtag(rec)
	return rec, nil
}

func (s *PostgresStore) loadActiveCatalog(ctx context.Context, q pgdb.DBTX) (map[string]*store.ComponentRecord, error) {
	componentsByName, componentsByID, err := loadCatalogComponents(ctx, q)
	if err != nil {
		return nil, err
	}
	if err := loadCatalogDependencies(ctx, q, componentsByID); err != nil {
		return nil, err
	}
	for _, rec := range componentsByName {
		rec.Etag = store.GenerateEtag(rec)
	}
	return componentsByName, nil
}

func loadCatalogComponents(ctx context.Context, q pgdb.DBTX) (map[string]*store.ComponentRecord, map[int64]*store.ComponentRecord, error) {
	rows, err := q.Query(ctx, selectAllActiveComponentsSQL)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to query active components: %w", err)
	}
	defer rows.Close()
	byName := make(map[string]*store.ComponentRecord)
	byID := make(map[int64]*store.ComponentRecord)
	for rows.Next() {
		rec, err := scanComponentRecord(rows)
		if err != nil {
			return nil, nil, err
		}
		byName[rec.Name] = rec
		byID[rec.ComponentID] = rec
	}
	if err := rows.Err(); err != nil {
		return nil, nil, fmt.Errorf("failed to list active components: %w", err)
	}
	return byName, byID, nil
}

func loadCatalogDependencies(ctx context.Context, q pgdb.DBTX, byID map[int64]*store.ComponentRecord) error {
	rows, err := q.Query(ctx, selectAllActiveDependenciesSQL)
	if err != nil {
		return fmt.Errorf("failed to query component dependencies: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var compID int64
		var depName string
		if err := rows.Scan(&compID, &depName); err != nil {
			return fmt.Errorf("failed to scan component dependency: %w", err)
		}
		if rec, ok := byID[compID]; ok {
			rec.Dependencies = append(rec.Dependencies, depName)
		}
	}
	return rows.Err()
}

type componentRow struct {
	componentID      int64
	name             string
	configProto      []byte
	accessPayload    []byte
	state            int64
	generation       int64
	observedGen      pgtype.Int8
	createTime       time.Time
	updateTime       time.Time
	sourceUID        pgtype.Text
	sourceGeneration pgtype.Int8
	sourceNamespace  pgtype.Text
	sourceClusterUID pgtype.Text
	sourceEpoch      pgtype.Int8
	sourceLastSeen   pgtype.Timestamptz
}

func (r *componentRow) scanTargets() []any {
	return []any{
		&r.componentID, &r.name, &r.configProto, &r.accessPayload, &r.state,
		&r.generation, &r.observedGen, &r.createTime, &r.updateTime,
		&r.sourceUID, &r.sourceGeneration, &r.sourceNamespace, &r.sourceClusterUID,
		&r.sourceEpoch, &r.sourceLastSeen,
	}
}

func (r *componentRow) toRecord() (*store.ComponentRecord, error) {
	rec := &store.ComponentRecord{
		ComponentID: r.componentID,
		Name:        r.name,
		State:       componentspb.ComponentState(r.state),
		Generation:  r.generation,
		CreateTime:  r.createTime.UTC(),
		UpdateTime:  r.updateTime.UTC(),
	}
	if r.sourceLastSeen.Valid {
		rec.SourceLastSeenTime = r.sourceLastSeen.Time.UTC()
	}
	if r.observedGen.Valid {
		gen := r.observedGen.Int64
		rec.ObservedGeneration = &gen
	}
	if r.sourceClusterUID.Valid {
		rec.Owner = &store.SourceOwner{
			UID: r.sourceUID.String, Generation: r.sourceGeneration.Int64,
			Namespace: r.sourceNamespace.String, ClusterUID: r.sourceClusterUID.String, Epoch: r.sourceEpoch.Int64,
		}
	}
	if err := unmarshalConfigProto(r.configProto, rec); err != nil {
		return nil, fmt.Errorf("failed to unmarshal config proto: %w", err)
	}
	if err := unmarshalAccessStatus(r.accessPayload, rec); err != nil {
		return nil, fmt.Errorf("failed to unmarshal access status: %w", err)
	}
	if rec.State == componentspb.ComponentState_COMPONENT_STATE_DELETED {
		t := rec.UpdateTime
		rec.DeleteTime = &t
	}
	rec.Etag = store.GenerateEtag(rec)
	return rec, nil
}

func scanComponentRecord(row pgx.Row) (*store.ComponentRecord, error) {
	var r componentRow
	if err := row.Scan(r.scanTargets()...); err != nil {
		return nil, err
	}
	return r.toRecord()
}

func unmarshalConfigProto(configBytes []byte, rec *store.ComponentRecord) error {
	if len(configBytes) == 0 {
		return nil
	}
	var comp componentspb.Component
	if err := proto.Unmarshal(configBytes, &comp); err == nil && (comp.Spec != nil || len(comp.Labels) > 0) {
		rec.Spec = comp.Spec
		if len(comp.Labels) > 0 {
			rec.Labels = store.CopyMap(comp.Labels)
		}
		return nil
	}
	var spec componentspb.ComponentSpec
	if err := proto.Unmarshal(configBytes, &spec); err != nil {
		return err
	}
	rec.Spec = &spec
	return nil
}

func unmarshalAccessStatus(accessBytes []byte, rec *store.ComponentRecord) error {
	if len(accessBytes) == 0 {
		rec.Status = nil
		return nil
	}
	var statusProto componentspb.ComponentAccessStatus
	if err := proto.Unmarshal(accessBytes, &statusProto); err != nil {
		return err
	}
	rec.Status = &statusProto
	return nil
}

func fetchDependencies(ctx context.Context, q pgdb.DBTX, componentID int64) ([]string, error) {
	rows, err := q.Query(ctx, selectDependenciesByComponentIDSQL, componentID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var deps []string
	for rows.Next() {
		var depName string
		if err := rows.Scan(&depName); err != nil {
			return nil, err
		}
		deps = append(deps, depName)
	}
	return deps, rows.Err()
}
