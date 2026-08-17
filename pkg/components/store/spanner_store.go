// Copyright 2026 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//      http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package store

import (
	"context"
	"fmt"
	"math/bits"
	"math/rand"
	"strings"
	"time"

	"cloud.google.com/go/spanner"
	"google.golang.org/api/iterator"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"

	componentspb "xmanager_cloud/api/protos/components/v1alpha1"
)

var _ Store = (*SpannerStore)(nil)

// SpannerStore implements Store using Google Cloud Spanner.
type SpannerStore struct {
	client *spanner.Client
}

// NewSpannerStore creates a new SpannerStore instance.
func NewSpannerStore(client *spanner.Client) *SpannerStore {
	return &SpannerStore{client: client}
}

// SetComponentIntent applies or updates component intent within a Spanner transaction.
func (s *SpannerStore) SetComponentIntent(ctx context.Context, name string, intent *componentspb.ComponentSpecProto, desiredGen int64) (*ComponentRecord, error) {
	if name == "" {
		return nil, status.Error(codes.InvalidArgument, "component name must not be empty")
	}
	if intent == nil {
		return nil, status.Error(codes.InvalidArgument, "intent must not be nil")
	}

	var result *ComponentRecord
	_, err := s.client.ReadWriteTransaction(ctx, func(ctx context.Context, txn *spanner.ReadWriteTransaction) error {
		existing, err := s.getComponentTxn(ctx, txn, name)
		if err != nil && status.Code(err) != codes.NotFound {
			return err
		}

		intentBytes, err := proto.Marshal(intent)
		if err != nil {
			return status.Errorf(codes.Internal, "failed to marshal intent proto: %v", err)
		}

		// Validate all declared dependencies exist and are not deleted when enabling.
		if intent.GetEnabled() {
			for _, depName := range intent.GetDependencies() {
				dep, err := s.getComponentTxn(ctx, txn, depName)
				if err != nil {
					return status.Errorf(codes.FailedPrecondition, "dependency %q not found: %v", depName, err)
				}
				if dep.State == componentspb.ComponentState_COMPONENT_STATE_DISABLED || dep.State == componentspb.ComponentState_COMPONENT_STATE_DISABLING || dep.State == componentspb.ComponentState_COMPONENT_STATE_DELETED {
					return status.Errorf(codes.FailedPrecondition, "dependency %q is disabled or deleted", depName)
				}
			}
		}

		now := time.Now().UTC()
		if existing == nil {
			// Insert new component record with random 64-bit ID.
			componentID := rand.Int63()
			if componentID <= 0 {
				componentID = 1
			}
			state := componentspb.ComponentState_COMPONENT_STATE_PENDING_RECONCILIATION
			if !intent.GetEnabled() {
				state = componentspb.ComponentState_COMPONENT_STATE_DISABLED
			}

			mutations := []*spanner.Mutation{
				spanner.Insert("Components",
					[]string{"ComponentId", "Name", "ConfigProto", "State", "Generation", "CreateTime", "UpdateTime"},
					[]interface{}{componentID, name, intentBytes, int64(state), desiredGen, spanner.CommitTimestamp, spanner.CommitTimestamp},
				),
			}

			for _, depName := range intent.GetDependencies() {
				dep, _ := s.getComponentTxn(ctx, txn, depName)
				if dep != nil {
					mutations = append(mutations, spanner.Insert("ComponentDependencies",
						[]string{"ComponentId", "DependencyComponentId"},
						[]interface{}{componentID, dep.ComponentID},
					))
				}
			}

			if err := txn.BufferWrite(mutations); err != nil {
				return status.Errorf(codes.Internal, "failed to buffer insert: %v", err)
			}

			result = &ComponentRecord{
				ComponentID: componentID,
				Name:        name,
				ConfigProto: intent,
				State:       state,
				Generation:  desiredGen,
				CreateTime:  now,
				UpdateTime:  now,
			}
			return nil
		}

		// Updating existing component.
		nextState := existing.State
		if !intent.GetEnabled() {
			if existing.State == componentspb.ComponentState_COMPONENT_STATE_ENABLED {
				// Check active dependents.
				dependents, err := s.getDependentComponentsTxn(ctx, txn, existing.ComponentID)
				if err != nil {
					return err
				}
				if len(dependents) > 0 && !intent.GetLifecycleControl().GetCascade() {
					return status.Errorf(codes.FailedPrecondition, "cannot disable %q: active dependent components exist (%s)", name, strings.Join(dependents, ", "))
				}
				nextState = componentspb.ComponentState_COMPONENT_STATE_DISABLING
			} else {
				nextState = componentspb.ComponentState_COMPONENT_STATE_DISABLED
			}
		} else if intent.GetEnabled() && (existing.State == componentspb.ComponentState_COMPONENT_STATE_DISABLED || existing.State == componentspb.ComponentState_COMPONENT_STATE_DISABLING) {
			nextState = componentspb.ComponentState_COMPONENT_STATE_PENDING_RECONCILIATION
		}

		mutations := []*spanner.Mutation{
			spanner.Update("Components",
				[]string{"ComponentId", "ConfigProto", "State", "Generation", "UpdateTime"},
				[]interface{}{existing.ComponentID, intentBytes, int64(nextState), desiredGen, spanner.CommitTimestamp},
			),
			// Clear old dependencies and write new ones.
			spanner.Delete("ComponentDependencies", spanner.KeyRange{
				Start: spanner.Key{existing.ComponentID},
				End:   spanner.Key{existing.ComponentID},
				Kind:  spanner.ClosedClosed,
			}),
		}

		for _, depName := range intent.GetDependencies() {
			dep, _ := s.getComponentTxn(ctx, txn, depName)
			if dep != nil {
				mutations = append(mutations, spanner.Insert("ComponentDependencies",
					[]string{"ComponentId", "DependencyComponentId"},
					[]interface{}{existing.ComponentID, dep.ComponentID},
				))
			}
		}

		if err := txn.BufferWrite(mutations); err != nil {
			return status.Errorf(codes.Internal, "failed to buffer update: %v", err)
		}

		result = &ComponentRecord{
			ComponentID:        existing.ComponentID,
			Name:               name,
			ConfigProto:        intent,
			AccessPayload:      existing.AccessPayload,
			State:              nextState,
			Generation:         desiredGen,
			ObservedGeneration: existing.ObservedGeneration,
			CreateTime:         existing.CreateTime,
			UpdateTime:         now,
		}
		return nil
	})

	if err != nil {
		return nil, err
	}
	return result, nil
}

func (s *SpannerStore) querySingleComponent(ctx context.Context, stmt spanner.Statement, notFoundMsg string) (*ComponentRecord, error) {
	iter := s.client.Single().Query(ctx, stmt)
	defer iter.Stop()

	row, err := iter.Next()
	if err != nil {
		if err == iterator.Done {
			return nil, status.Errorf(codes.NotFound, "%s", notFoundMsg)
		}
		return nil, status.Errorf(codes.Internal, "failed to query component: %v", err)
	}

	return parseComponentRow(row)
}

// GetComponent retrieves a component record by its name using the ActiveName index.
func (s *SpannerStore) GetComponent(ctx context.Context, name string) (*ComponentRecord, error) {
	stmt := spanner.Statement{
		SQL: `SELECT ComponentId, Name, ConfigProto, AccessPayload, State, Generation, ObservedGeneration, CreateTime, UpdateTime
		      FROM Components
		      WHERE ActiveName = @name
		      LIMIT 1`,
		Params: map[string]interface{}{"name": name},
	}
	return s.querySingleComponent(ctx, stmt, fmt.Sprintf("component %q not found", name))
}

// GetComponentByID retrieves a component record by its primary key.
func (s *SpannerStore) GetComponentByID(ctx context.Context, componentID int64) (*ComponentRecord, error) {
	stmt := spanner.Statement{
		SQL: `SELECT ComponentId, Name, ConfigProto, AccessPayload, State, Generation, ObservedGeneration, CreateTime, UpdateTime
		      FROM Components
		      WHERE ComponentId = @id
		      LIMIT 1`,
		Params: map[string]interface{}{"id": componentID},
	}
	return s.querySingleComponent(ctx, stmt, fmt.Sprintf("component ID %d not found", componentID))
}

// ListComponents returns all registered component records.
func (s *SpannerStore) ListComponents(ctx context.Context) ([]*ComponentRecord, error) {
	stmt := spanner.Statement{
		SQL: `SELECT ComponentId, Name, ConfigProto, AccessPayload, State, Generation, ObservedGeneration, CreateTime, UpdateTime
		      FROM Components
		      WHERE State != 5
		      ORDER BY Name`,
	}
	iter := s.client.Single().Query(ctx, stmt)
	defer iter.Stop()

	var records []*ComponentRecord
	for {
		row, err := iter.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			return nil, status.Errorf(codes.Internal, "failed to list components: %v", err)
		}
		rec, err := parseComponentRow(row)
		if err != nil {
			return nil, err
		}
		records = append(records, rec)
	}
	return records, nil
}

// PrepareDeactivation validates if a component can be safely deactivated.
func (s *SpannerStore) PrepareDeactivation(ctx context.Context, name string, requestCascade bool) (*PrepareDeactivationResult, error) {
	comp, err := s.GetComponent(ctx, name)
	if err != nil {
		return nil, err
	}

	// Check active dependent components.
	depStmt := spanner.Statement{
		SQL: `SELECT c.Name
		      FROM ComponentDependencies d
		      JOIN Components c ON d.ComponentId = c.ComponentId
		      WHERE d.DependencyComponentId = @compID AND c.State NOT IN (3, 5)`,
		Params: map[string]interface{}{"compID": comp.ComponentID},
	}
	depIter := s.client.Single().Query(ctx, depStmt)
	defer depIter.Stop()

	var blockingDeps []string
	for {
		row, err := depIter.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			return nil, status.Errorf(codes.Internal, "failed to check dependent components: %v", err)
		}
		var depName string
		if err := row.Column(0, &depName); err != nil {
			return nil, err
		}
		blockingDeps = append(blockingDeps, depName)
	}

	drainSec := int32(5)
	if comp.ConfigProto != nil && comp.ConfigProto.GetLifecycleControl() != nil && comp.ConfigProto.GetLifecycleControl().GetDrainTimeoutSeconds() > 0 {
		drainSec = comp.ConfigProto.GetLifecycleControl().GetDrainTimeoutSeconds()
	}

	if len(blockingDeps) > 0 && !requestCascade {
		return &PrepareDeactivationResult{
			IsSafeToDeactivate:  false,
			BlockingDependents:  blockingDeps,
			RecommendedDrainSec: drainSec,
			RejectionReason:     fmt.Sprintf("active dependent components exist: %s", strings.Join(blockingDeps, ", ")),
		}, nil
	}

	return &PrepareDeactivationResult{
		IsSafeToDeactivate:    true,
		BlockingDependents:    nil,
		RecommendedDrainSec:   drainSec,
		CascadedDeactivations: blockingDeps,
	}, nil
}

// CompleteDeactivation transitions a component from DISABLING to DISABLED in Spanner.
func (s *SpannerStore) CompleteDeactivation(ctx context.Context, name string) error {
	_, err := s.client.ReadWriteTransaction(ctx, func(ctx context.Context, txn *spanner.ReadWriteTransaction) error {
		comp, err := s.getComponentTxn(ctx, txn, name)
		if err != nil {
			return err
		}

		mutation := spanner.Update("Components",
			[]string{"ComponentId", "State", "UpdateTime"},
			[]interface{}{comp.ComponentID, int64(componentspb.ComponentState_COMPONENT_STATE_DISABLED), spanner.CommitTimestamp},
		)
		return txn.BufferWrite([]*spanner.Mutation{mutation})
	})
	return err
}

// UpdateObservedGeneration updates observed generation and access status in Spanner.
func (s *SpannerStore) UpdateObservedGeneration(ctx context.Context, name string, observedGen int64, access *componentspb.ComponentAccessStatusProto, statusCond string) error {
	_, err := s.client.ReadWriteTransaction(ctx, func(ctx context.Context, txn *spanner.ReadWriteTransaction) error {
		comp, err := s.getComponentTxn(ctx, txn, name)
		if err != nil {
			return err
		}

		var accessBytes []byte
		if access != nil {
			var marshalErr error
			accessBytes, marshalErr = proto.Marshal(access)
			if marshalErr != nil {
				return status.Errorf(codes.Internal, "failed to marshal access proto: %v", marshalErr)
			}
		}

		nextState := comp.State
		if comp.State == componentspb.ComponentState_COMPONENT_STATE_PENDING_RECONCILIATION && strings.Contains(statusCond, "True") {
			nextState = componentspb.ComponentState_COMPONENT_STATE_ENABLED
		}

		cols := []string{"ComponentId", "ObservedGeneration", "State", "UpdateTime"}
		vals := []interface{}{comp.ComponentID, observedGen, int64(nextState), spanner.CommitTimestamp}
		if len(accessBytes) > 0 {
			cols = append(cols, "AccessPayload")
			vals = append(vals, accessBytes)
		}

		mutation := spanner.Update("Components", cols, vals)
		return txn.BufferWrite([]*spanner.Mutation{mutation})
	})
	return err
}

// ExecuteDeletion soft-deletes a component row.
func (s *SpannerStore) ExecuteDeletion(ctx context.Context, name string) error {
	_, err := s.client.ReadWriteTransaction(ctx, func(ctx context.Context, txn *spanner.ReadWriteTransaction) error {
		comp, err := s.getComponentTxn(ctx, txn, name)
		if err != nil {
			return err
		}
		if comp.ConfigProto != nil && comp.ConfigProto.GetDeletionProtection() {
			return status.Errorf(codes.FailedPrecondition, "cannot delete %q: deletionProtection is enabled", name)
		}

		mutation := spanner.Update("Components",
			[]string{"ComponentId", "State", "UpdateTime"},
			[]interface{}{comp.ComponentID, int64(componentspb.ComponentState_COMPONENT_STATE_DELETED), spanner.CommitTimestamp},
		)
		return txn.BufferWrite([]*spanner.Mutation{mutation})
	})
	return err
}

// ResolveComponentSnapshot locks component configurations for an experiment at T0.
func (s *SpannerStore) ResolveComponentSnapshot(ctx context.Context, xid int64, requestedClasses []string) ([]*componentspb.ResolvedComponentCoordinates, error) {
	bitReversedXID := int64(bits.Reverse64(uint64(xid)))
	var coordinates []*componentspb.ResolvedComponentCoordinates

	_, err := s.client.ReadWriteTransaction(ctx, func(ctx context.Context, txn *spanner.ReadWriteTransaction) error {
		resolvedMap := make(map[int64]*ComponentRecord)
		var queue []string
		queue = append(queue, requestedClasses...)

		for len(queue) > 0 {
			cls := queue[0]
			queue = queue[1:]

			comp, err := s.getComponentByClassOrNameTxn(ctx, txn, cls)
			if err != nil {
				return err
			}
			if comp.State == componentspb.ComponentState_COMPONENT_STATE_DISABLED || comp.State == componentspb.ComponentState_COMPONENT_STATE_DELETED {
				return status.Errorf(codes.FailedPrecondition, "requested component %q is disabled or deleted", cls)
			}

			if _, exists := resolvedMap[comp.ComponentID]; !exists {
				resolvedMap[comp.ComponentID] = comp
				if comp.ConfigProto != nil {
					queue = append(queue, comp.ConfigProto.GetDependencies()...)
				}
			}
		}

		var mutations []*spanner.Mutation
		for _, comp := range resolvedMap {
			configBytes, _ := proto.Marshal(comp.ConfigProto)
			var accessBytes []byte
			if comp.AccessPayload != nil {
				accessBytes, _ = proto.Marshal(comp.AccessPayload)
			}

			mutations = append(mutations, spanner.InsertOrUpdate("ExperimentComponentSnapshots",
				[]string{"BitReversedXid", "ComponentId", "LockedGeneration", "ConfigProto", "AccessPayload"},
				[]interface{}{bitReversedXID, comp.ComponentID, comp.Generation, configBytes, accessBytes},
			))

			cls := comp.Name
			if comp.ConfigProto != nil && comp.ConfigProto.GetComponentClass() != "" {
				cls = comp.ConfigProto.GetComponentClass()
			}

			coordinates = append(coordinates, &componentspb.ResolvedComponentCoordinates{
				ComponentClass:   cls,
				ComponentName:    comp.Name,
				LockedGeneration: comp.Generation,
				Access:           comp.AccessPayload,
			})
		}

		return txn.BufferWrite(mutations)
	})

	if err != nil {
		return nil, err
	}
	return coordinates, nil
}

// ValidateComponentRequirement checks if a requested class is satisfied.
func (s *SpannerStore) ValidateComponentRequirement(ctx context.Context, requiredClass string, minParams map[string]string) (*RequirementValidationResult, error) {
	comp, err := s.GetComponent(ctx, requiredClass)
	if err != nil {
		// Try finding by component class
		stmt := spanner.Statement{
			SQL: `SELECT ComponentId, Name, ConfigProto, AccessPayload, State, Generation, ObservedGeneration, CreateTime, UpdateTime
			      FROM Components
			      WHERE State IN (1, 4)
			      ORDER BY Name`,
		}
		iter := s.client.Single().Query(ctx, stmt)
		defer iter.Stop()

		var matched *ComponentRecord
		for {
			row, err := iter.Next()
			if err == iterator.Done {
				break
			}
			if err != nil {
				return nil, status.Errorf(codes.Internal, "query failed: %v", err)
			}
			rec, err := parseComponentRow(row)
			if err != nil {
				return nil, err
			}
			if rec.ConfigProto != nil && rec.ConfigProto.GetComponentClass() == requiredClass {
				matched = rec
				break
			}
		}

		if matched == nil {
			return &RequirementValidationResult{
				IsSatisfied: false,
				ErrorDetail: fmt.Sprintf("no active component found satisfying class %q", requiredClass),
			}, nil
		}
		comp = matched
	}

	if comp.State == componentspb.ComponentState_COMPONENT_STATE_DISABLED || comp.State == componentspb.ComponentState_COMPONENT_STATE_DELETED {
		return &RequirementValidationResult{
			IsSatisfied:             false,
			SatisfyingComponentName: comp.Name,
			ErrorDetail:             fmt.Sprintf("component %q is not active (state: %s)", comp.Name, comp.State),
		}, nil
	}

	// Validate min parameters.
	if comp.ConfigProto != nil && len(minParams) > 0 {
		params := comp.ConfigProto.GetParameters()
		for k, v := range minParams {
			if actual, ok := params[k]; !ok || actual != v {
				return &RequirementValidationResult{
					IsSatisfied:             false,
					SatisfyingComponentName: comp.Name,
					ErrorDetail:             fmt.Sprintf("parameter mismatch for %q: expected %q, got %q", k, v, actual),
				}, nil
			}
		}
	}

	return &RequirementValidationResult{
		IsSatisfied:             true,
		SatisfyingComponentName: comp.Name,
	}, nil
}

// GetPlatformHealth computes platform-wide component health telemetry.
func (s *SpannerStore) GetPlatformHealth(ctx context.Context) (*componentspb.GetPlatformHealthResponse, error) {
	comps, err := s.ListComponents(ctx)
	if err != nil {
		return nil, err
	}

	resp := &componentspb.GetPlatformHealthResponse{}
	for _, c := range comps {
		observed := int64(0)
		if c.ObservedGeneration != nil {
			observed = *c.ObservedGeneration
		}
		lagging := observed < c.Generation
		cond := "Ready=True"
		if c.State == componentspb.ComponentState_COMPONENT_STATE_PENDING_RECONCILIATION {
			cond = "Ready=Unknown"
		} else if c.State == componentspb.ComponentState_COMPONENT_STATE_DISABLING || c.State == componentspb.ComponentState_COMPONENT_STATE_DISABLED {
			cond = "Ready=False"
		}

		if lagging || c.State == componentspb.ComponentState_COMPONENT_STATE_DISABLED {
			resp.IsPlatformDegraded = true
		}

		resp.ComponentMetrics = append(resp.ComponentMetrics, &componentspb.ComponentHealthTelemetry{
			ComponentName:          c.Name,
			StatusCondition:        cond,
			ObservedGeneration:     observed,
			DesiredGeneration:      c.Generation,
			IsCdcProjectionLagging: lagging,
		})
	}
	return resp, nil
}

// Helper methods for transactions.
func (s *SpannerStore) getComponentTxn(ctx context.Context, txn *spanner.ReadWriteTransaction, name string) (*ComponentRecord, error) {
	stmt := spanner.Statement{
		SQL: `SELECT ComponentId, Name, ConfigProto, AccessPayload, State, Generation, ObservedGeneration, CreateTime, UpdateTime
		      FROM Components
		      WHERE ActiveName = @name
		      LIMIT 1`,
		Params: map[string]interface{}{"name": name},
	}
	iter := txn.Query(ctx, stmt)
	defer iter.Stop()

	row, err := iter.Next()
	if err != nil {
		if err == iterator.Done {
			return nil, status.Errorf(codes.NotFound, "component %q not found", name)
		}
		return nil, status.Errorf(codes.Internal, "failed to query component: %v", err)
	}
	return parseComponentRow(row)
}

func (s *SpannerStore) getComponentByClassOrNameSingle(ctx context.Context, classOrName string) (*ComponentRecord, error) {
	rec, err := s.GetComponent(ctx, classOrName)
	if err == nil {
		return rec, nil
	}

	stmt := spanner.Statement{
		SQL: `SELECT ComponentId, Name, ConfigProto, AccessPayload, State, Generation, ObservedGeneration, CreateTime, UpdateTime
		      FROM Components
		      WHERE State != 5
		      ORDER BY Name`,
	}
	iter := s.client.Single().Query(ctx, stmt)
	defer iter.Stop()

	for {
		row, err := iter.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			return nil, err
		}
		r, err := parseComponentRow(row)
		if err != nil {
			return nil, err
		}
		if r.ConfigProto != nil && r.ConfigProto.GetComponentClass() == classOrName {
			return r, nil
		}
	}
	return nil, status.Errorf(codes.NotFound, "component with class or name %q not found", classOrName)
}

func (s *SpannerStore) getComponentByClassOrNameTxn(ctx context.Context, txn *spanner.ReadWriteTransaction, classOrName string) (*ComponentRecord, error) {
	rec, err := s.getComponentTxn(ctx, txn, classOrName)
	if err == nil {
		return rec, nil
	}

	stmt := spanner.Statement{
		SQL: `SELECT ComponentId, Name, ConfigProto, AccessPayload, State, Generation, ObservedGeneration, CreateTime, UpdateTime
		      FROM Components
		      WHERE State != 5
		      ORDER BY Name`,
	}
	iter := txn.Query(ctx, stmt)
	defer iter.Stop()

	for {
		row, err := iter.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			return nil, err
		}
		r, err := parseComponentRow(row)
		if err != nil {
			return nil, err
		}
		if r.ConfigProto != nil && r.ConfigProto.GetComponentClass() == classOrName {
			return r, nil
		}
	}
	return nil, status.Errorf(codes.NotFound, "component with class or name %q not found", classOrName)
}

func (s *SpannerStore) getDependentComponentsTxn(ctx context.Context, txn *spanner.ReadWriteTransaction, compID int64) ([]string, error) {
	stmt := spanner.Statement{
		SQL: `SELECT c.Name
		      FROM ComponentDependencies d
		      JOIN Components c ON d.ComponentId = c.ComponentId
		      WHERE d.DependencyComponentId = @compID AND c.State NOT IN (3, 5)`,
		Params: map[string]interface{}{"compID": compID},
	}
	iter := txn.Query(ctx, stmt)
	defer iter.Stop()

	var deps []string
	for {
		row, err := iter.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			return nil, err
		}
		var name string
		if err := row.Column(0, &name); err != nil {
			return nil, err
		}
		deps = append(deps, name)
	}
	return deps, nil
}

func parseComponentRow(row *spanner.Row) (*ComponentRecord, error) {
	var (
		compID      int64
		name        string
		configBytes []byte
		accessBytes spanner.NullRow
		state       int64
		gen         int64
		observedGen spanner.NullInt64
		createTime  time.Time
		updateTime  time.Time
	)

	if err := row.Columns(&compID, &name, &configBytes, &accessBytes, &state, &gen, &observedGen, &createTime, &updateTime); err != nil {
		// try unmarshaling with NullRow/nullable bytes
		var rawAccess []byte
		if err := row.Columns(&compID, &name, &configBytes, &rawAccess, &state, &gen, &observedGen, &createTime, &updateTime); err != nil {
			return nil, status.Errorf(codes.Internal, "failed to scan component row: %v", err)
		}
		configProto := &componentspb.ComponentSpecProto{}
		if err := proto.Unmarshal(configBytes, configProto); err != nil {
			return nil, status.Errorf(codes.Internal, "failed to unmarshal config proto: %v", err)
		}
		var accessProto *componentspb.ComponentAccessStatusProto
		if len(rawAccess) > 0 {
			accessProto = &componentspb.ComponentAccessStatusProto{}
			if err := proto.Unmarshal(rawAccess, accessProto); err != nil {
				return nil, status.Errorf(codes.Internal, "failed to unmarshal access proto: %v", err)
			}
		}
		var obsPtr *int64
		if observedGen.Valid {
			obsPtr = &observedGen.Int64
		}
		return &ComponentRecord{
			ComponentID:        compID,
			Name:               name,
			ConfigProto:        configProto,
			AccessPayload:      accessProto,
			State:              componentspb.ComponentState(state),
			Generation:         gen,
			ObservedGeneration: obsPtr,
			CreateTime:         createTime,
			UpdateTime:         updateTime,
		}, nil
	}

	configProto := &componentspb.ComponentSpecProto{}
	if err := proto.Unmarshal(configBytes, configProto); err != nil {
		return nil, status.Errorf(codes.Internal, "failed to unmarshal config proto: %v", err)
	}

	var obsPtr *int64
	if observedGen.Valid {
		obsPtr = &observedGen.Int64
	}

	return &ComponentRecord{
		ComponentID:        compID,
		Name:               name,
		ConfigProto:        configProto,
		State:              componentspb.ComponentState(state),
		Generation:         gen,
		ObservedGeneration: obsPtr,
		CreateTime:         createTime,
		UpdateTime:         updateTime,
	}, nil
}
