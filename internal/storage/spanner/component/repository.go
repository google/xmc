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
	"xmanager_cloud/internal/storage"
	"xmanager_cloud/pkg/components/store"
)

const (
	// ComponentColumnsSQL lists the Components columns that RowToComponentRecord reads.
	ComponentColumnsSQL                = `ComponentId, Name, ComponentClass, ConfigProto, AccessPayload, State, Generation, ObservedGeneration, CreateTime, UpdateTime, SourceUid, SourceGeneration, SourceNamespace, SourceClusterUid, SourceEpoch, SourceLastSeenTime`
	selectComponentByActiveNameSQL     = `SELECT ` + ComponentColumnsSQL + ` FROM Components WHERE ActiveName = @name LIMIT 1`
	selectComponentByIDSQL             = `SELECT ` + ComponentColumnsSQL + ` FROM Components WHERE ComponentId = @id AND State != 5 LIMIT 1`
	selectDependenciesByComponentIDSQL = `SELECT dep.Name FROM ComponentDependencies cd JOIN Components dep ON cd.DependencyComponentId = dep.ComponentId WHERE cd.ComponentId = @id AND dep.ActiveName IS NOT NULL ORDER BY dep.Name ASC`
)

var _ storage.ComponentRepository = (*Repository)(nil)

// Queryable is implemented by Spanner single-use reads, read-only transactions, and read-write transactions.
type Queryable interface {
	Query(ctx context.Context, statement spanner.Statement) *spanner.RowIterator
}

// Repository persists component catalog records in Cloud Spanner.
type Repository struct {
	client *spanner.Client
	q      Queryable
}

// NewRepository creates a Spanner-backed component Repository.
func NewRepository(client *spanner.Client) *Repository {
	return &Repository{client: client}
}

// WithTx returns a repository bound to the given Spanner queryable or transaction.
func (r *Repository) WithTx(q Queryable) *Repository {
	return &Repository{q: q}
}

// GetActiveComponent retrieves a non-deleted component record by canonical name, populated with active dependencies.
func (r *Repository) GetActiveComponent(ctx context.Context, canonicalName string) (*store.ComponentRecord, error) {
	stmt := spanner.Statement{SQL: selectComponentByActiveNameSQL, Params: map[string]any{"name": canonicalName}}
	subject := fmt.Sprintf("component %q", canonicalName)
	return r.queryComponentWithDeps(ctx, stmt, subject, subject)
}

// GetComponentByID retrieves a non-deleted component record by its unique database identifier, populated with active dependencies.
func (r *Repository) GetComponentByID(ctx context.Context, componentID int64) (*store.ComponentRecord, error) {
	stmt := spanner.Statement{SQL: selectComponentByIDSQL, Params: map[string]any{"id": componentID}}
	return r.queryComponentWithDeps(ctx, stmt, fmt.Sprintf("component by ID %d", componentID), fmt.Sprintf("component ID %d", componentID))
}

func (r *Repository) queryComponentWithDeps(ctx context.Context, stmt spanner.Statement, querySubject, depsSubject string) (*store.ComponentRecord, error) {
	q := r.q
	if q == nil {
		ro := r.client.ReadOnlyTransaction()
		defer ro.Close()
		q = ro
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
	depIter := q.Query(ctx, spanner.Statement{SQL: selectDependenciesByComponentIDSQL, Params: map[string]any{"id": rec.ComponentID}})
	defer depIter.Stop()
	if err := depIter.Do(func(row *spanner.Row) error {
		var depName string
		if err := row.Column(0, &depName); err != nil {
			return err
		}
		rec.Dependencies = append(rec.Dependencies, depName)
		return nil
	}); err != nil {
		return nil, fmt.Errorf("failed to fetch dependencies for %s: %w", depsSubject, err)
	}
	rec.Etag = store.GenerateEtag(rec)
	return rec, nil
}

// RowToComponentRecord decodes a Spanner Components row matching ComponentColumnsSQL into a ComponentRecord.
func RowToComponentRecord(row *spanner.Row) (*store.ComponentRecord, error) {
	rec := &store.ComponentRecord{}
	var class, uid, namespace, clusterUID spanner.NullString
	var state int64
	var observedGen, generation, epoch spanner.NullInt64
	var configProto, accessPayload []byte
	var sourceLastSeen spanner.NullTime
	for _, col := range [...]struct {
		name string
		dst  any
	}{
		{"ComponentId", &rec.ComponentID}, {"Name", &rec.Name}, {"ComponentClass", &class},
		{"ConfigProto", &configProto}, {"AccessPayload", &accessPayload}, {"State", &state},
		{"Generation", &rec.Generation}, {"ObservedGeneration", &observedGen}, {"CreateTime", &rec.CreateTime},
		{"UpdateTime", &rec.UpdateTime}, {"SourceUid", &uid}, {"SourceGeneration", &generation},
		{"SourceNamespace", &namespace}, {"SourceClusterUid", &clusterUID}, {"SourceEpoch", &epoch},
		{"SourceLastSeenTime", &sourceLastSeen},
	} {
		if err := row.ColumnByName(col.name, col.dst); err != nil {
			return nil, fmt.Errorf("failed to read %s: %w", col.name, err)
		}
	}
	rec.State = componentspb.ComponentState(state)
	rec.SourceLastSeenTime = sourceLastSeen.Time
	if observedGen.Valid {
		rec.ObservedGeneration = &observedGen.Int64
	}
	if clusterUID.Valid {
		rec.Owner = &store.SourceOwner{UID: uid.StringVal, Generation: generation.Int64, Namespace: namespace.StringVal, ClusterUID: clusterUID.StringVal, Epoch: epoch.Int64}
	}
	if err := UnmarshalConfigProto(configProto, rec); err != nil {
		return nil, fmt.Errorf("failed to unmarshal config proto: %w", err)
	}
	if err := UnmarshalAccessStatus(accessPayload, rec); err != nil {
		return nil, fmt.Errorf("failed to unmarshal access status: %w", err)
	}
	if rec.State == componentspb.ComponentState_COMPONENT_STATE_DELETED {
		t := rec.UpdateTime
		rec.DeleteTime = &t
	}
	rec.Etag = store.GenerateEtag(rec)
	return rec, nil
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
