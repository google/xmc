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

package component

import (
	"context"
	"fmt"
	"time"

	"cloud.google.com/go/spanner"
	"google.golang.org/api/iterator"
	"google.golang.org/protobuf/proto"

	componentspb "xmanager_cloud/api/protos/components/v1alpha1"
	"xmanager_cloud/pkg/components/store"
)

const (
	selectComponentByActiveNameSQL = `SELECT ` + ComponentColumnsSQL + `
FROM Components
WHERE ActiveName = @name
LIMIT 1`

	selectComponentByIDSQL = `SELECT ` + ComponentColumnsSQL + `
FROM Components
WHERE ComponentId = @id AND State != 5
LIMIT 1`

	selectAllActiveComponentsSQL = `SELECT ` + ComponentColumnsSQL + `
FROM Components
WHERE ActiveName IS NOT NULL
ORDER BY Name ASC`

	// ComponentColumnsSQL lists the Components columns that RowToComponentRecord reads. It is
	// exported only while the SpannerStore intent fast path still builds its own query.
	ComponentColumnsSQL = `ComponentId, Name, ComponentClass, ConfigProto, AccessPayload, State, Generation, ObservedGeneration, CreateTime, UpdateTime, SourceUid, SourceGeneration, SourceNamespace, SourceClusterUid, SourceEpoch, SourceLastSeenTime`

	selectDependenciesByComponentIDSQL = `SELECT dep.Name
FROM ComponentDependencies cd
JOIN Components dep ON cd.DependencyComponentId = dep.ComponentId
WHERE cd.ComponentId = @id AND dep.ActiveName IS NOT NULL
ORDER BY dep.Name ASC`

	selectAllActiveDependenciesSQL = `SELECT cd.ComponentId, dep.Name
FROM ComponentDependencies cd
JOIN Components dep ON cd.DependencyComponentId = dep.ComponentId
JOIN Components parent ON cd.ComponentId = parent.ComponentId
WHERE parent.ActiveName IS NOT NULL AND dep.ActiveName IS NOT NULL
ORDER BY cd.ComponentId, dep.Name ASC`
)

// GetActiveComponent returns the active record of canonicalName with its active dependencies.
func (r *reader) GetActiveComponent(ctx context.Context, canonicalName string) (*store.ComponentRecord, error) {
	stmt := spanner.Statement{
		SQL: selectComponentByActiveNameSQL,
		Params: map[string]any{
			"name": canonicalName,
		},
	}
	subject := fmt.Sprintf("component %q", canonicalName)
	return r.queryComponentWithDeps(ctx, stmt, subject, subject)
}

// GetComponentByID returns the non-deleted record componentID with its active dependencies.
func (r *reader) GetComponentByID(ctx context.Context, componentID int64) (*store.ComponentRecord, error) {
	stmt := spanner.Statement{
		SQL: selectComponentByIDSQL,
		Params: map[string]any{
			"id": componentID,
		},
	}
	return r.queryComponentWithDeps(ctx, stmt, fmt.Sprintf("component by ID %d", componentID), fmt.Sprintf("component ID %d", componentID))
}

// queryComponentWithDeps returns the first component row of stmt with its active dependencies,
// or store.ErrComponentNotFound. querySubject and depsSubject name the component in the errors
// of the component and dependency queries.
func (r *reader) queryComponentWithDeps(ctx context.Context, stmt spanner.Statement, querySubject, depsSubject string) (*store.ComponentRecord, error) {
	iter := r.q.Query(ctx, stmt)
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

	deps, err := r.queryDependencies(ctx, rec.ComponentID)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch dependencies for %s: %w", depsSubject, err)
	}
	rec.Dependencies = deps
	rec.Etag = store.GenerateEtag(rec)

	return rec, nil
}

// queryDependencies returns the names of the active dependencies of componentID sorted by name.
func (r *reader) queryDependencies(ctx context.Context, componentID int64) ([]string, error) {
	depStmt := spanner.Statement{
		SQL: selectDependenciesByComponentIDSQL,
		Params: map[string]any{
			"id": componentID,
		},
	}
	depIter := r.q.Query(ctx, depStmt)
	defer depIter.Stop()

	return fetchDependencies(depIter)
}

// ListActiveComponents returns every active record, with its active dependencies, by name.
func (r *reader) ListActiveComponents(ctx context.Context) (map[string]*store.ComponentRecord, error) {
	compIter := r.q.Query(ctx, spanner.Statement{SQL: selectAllActiveComponentsSQL})
	defer compIter.Stop()

	componentsByName := make(map[string]*store.ComponentRecord)
	componentsByID := make(map[int64]*store.ComponentRecord)

	if err := compIter.Do(func(row *spanner.Row) error {
		rec, err := RowToComponentRecord(row)
		if err != nil {
			return err
		}
		componentsByName[rec.Name] = rec
		componentsByID[rec.ComponentID] = rec
		return nil
	}); err != nil {
		return nil, fmt.Errorf("failed to list active components: %w", err)
	}

	depIter := r.q.Query(ctx, spanner.Statement{SQL: selectAllActiveDependenciesSQL})
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
	sourceLastSeen spanner.NullTime
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
	if err := row.ColumnByName("SourceLastSeenTime", &d.sourceLastSeen); err != nil {
		return nil, fmt.Errorf("failed to read SourceLastSeenTime: %w", err)
	}
	return &d, nil
}

// RowToComponentRecord decodes a row of ComponentColumnsSQL into a record. It is exported only
// while the SpannerStore intent fast path still decodes its own query.
func RowToComponentRecord(row *spanner.Row) (*store.ComponentRecord, error) {
	d, err := scanComponentRow(row)
	if err != nil {
		return nil, err
	}

	rec := &store.ComponentRecord{
		ComponentID:        d.componentID,
		Name:               d.name,
		State:              componentspb.ComponentState(d.state),
		Generation:         d.generation,
		CreateTime:         d.createTime,
		UpdateTime:         d.updateTime,
		SourceLastSeenTime: d.sourceLastSeen.Time, // Zero when NULL.
	}

	if d.observedGen.Valid {
		gen := d.observedGen.Int64
		rec.ObservedGeneration = &gen
	}
	if rec.Owner, err = scanSourceOwner(row); err != nil {
		return nil, err
	}

	if err := unmarshalConfigProto(d.configProto, rec); err != nil {
		return nil, fmt.Errorf("failed to unmarshal config proto: %w", err)
	}
	if err := unmarshalAccessStatus(d.accessPayload, rec); err != nil {
		return nil, fmt.Errorf("failed to unmarshal access status: %w", err)
	}

	if rec.State == componentspb.ComponentState_COMPONENT_STATE_DELETED {
		t := d.updateTime
		rec.DeleteTime = &t
	}

	rec.Etag = store.GenerateEtag(rec)
	return rec, nil
}

// scanSourceOwner reads the recorded owner of a component row, or nil when SourceClusterUid is NULL.
func scanSourceOwner(row *spanner.Row) (*store.SourceOwner, error) {
	var uid, namespace, clusterUID spanner.NullString
	var generation, epoch spanner.NullInt64
	for _, col := range [...]struct {
		name string
		dst  any
	}{ // An array, unlike a map, does not allocate per row.
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
