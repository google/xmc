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

package spannerdata

import (
	"fmt"
	"maps"

	"cloud.google.com/go/spanner"

	componentspb "xmanager_cloud/api/protos/components/v1alpha1"
	"xmanager_cloud/internal/grpcerr"
	"xmanager_cloud/internal/storage/data"
	"xmanager_cloud/pkg/components/store"
)

// ComponentInsertMutations builds Spanner mutations to insert rec and its dependency edges.
func ComponentInsertMutations(rec *store.ComponentRecord, depIDs map[string]int64) ([]*spanner.Mutation, error) {
	if rec == nil {
		return nil, fmt.Errorf("component record must not be nil: %w", grpcerr.ErrInvalidArgument)
	}
	configBytes, accessBytes, err := data.MarshalComponentPayloads(rec.Name, rec.Spec, rec.Labels, rec.Status)
	if err != nil {
		return nil, err
	}
	compClass := data.DetermineComponentClass(rec.Spec)
	insertCols := []string{"ComponentId", "Name", "ComponentClass", "ConfigProto", "AccessPayload", "State", "Generation", "CreateTime", "UpdateTime"}
	insertVals := []any{
		rec.ComponentID, rec.Name, spanner.NullString{StringVal: compClass, Valid: compClass != ""},
		configBytes, accessBytes, int64(rec.State), rec.Generation, spanner.CommitTimestamp, spanner.CommitTimestamp,
	}
	mutations := []*spanner.Mutation{spanner.Insert("Components", insertCols, insertVals)}
	return appendDependencyMutations(mutations, rec.ComponentID, rec.Dependencies, depIDs)
}

// ComponentIntentUpdateMutations builds Spanner mutations to update rec's intent and replace its dependency edges.
func ComponentIntentUpdateMutations(rec *store.ComponentRecord, depIDs map[string]int64) ([]*spanner.Mutation, error) {
	if rec == nil {
		return nil, fmt.Errorf("component record must not be nil: %w", grpcerr.ErrInvalidArgument)
	}
	configBytes, accessBytes, err := data.MarshalComponentPayloads(rec.Name, rec.Spec, rec.Labels, rec.Status)
	if err != nil {
		return nil, err
	}
	compClass := data.DetermineComponentClass(rec.Spec)
	update := spanner.UpdateMap("Components", map[string]any{
		"ComponentId":    rec.ComponentID,
		"ComponentClass": spanner.NullString{StringVal: compClass, Valid: compClass != ""},
		"ConfigProto":    configBytes,
		"AccessPayload":  accessBytes,
		"State":          int64(rec.State),
		"Generation":     rec.Generation,
		"UpdateTime":     spanner.CommitTimestamp,
	})
	delDeps := spanner.Delete("ComponentDependencies", spanner.KeyRange{
		Start: spanner.Key{rec.ComponentID},
		End:   spanner.Key{rec.ComponentID},
		Kind:  spanner.ClosedClosed,
	})
	return appendDependencyMutations([]*spanner.Mutation{update, delDeps}, rec.ComponentID, rec.Dependencies, depIDs)
}

// ComponentStateUpdateMutation builds a Spanner mutation to update componentID's state and access payload.
func ComponentStateUpdateMutation(componentID int64, state componentspb.ComponentState, access *componentspb.ComponentAccessStatus) (*spanner.Mutation, error) {
	accessBytes, err := data.MarshalAccessStatus(access)
	if err != nil {
		return nil, err
	}
	return spanner.UpdateMap("Components", map[string]any{
		"ComponentId":   componentID,
		"State":         int64(state),
		"AccessPayload": accessBytes,
		"UpdateTime":    spanner.CommitTimestamp,
	}), nil
}

// ComponentObservationUpdateMutation builds a Spanner mutation to update rec's observed generation, access payload, and optional state.
func ComponentObservationUpdateMutation(rec *store.ComponentRecord, updateState bool) (*spanner.Mutation, error) {
	if rec == nil {
		return nil, fmt.Errorf("component record must not be nil: %w", grpcerr.ErrInvalidArgument)
	}
	accessBytes, err := data.MarshalAccessStatus(rec.Status)
	if err != nil {
		return nil, err
	}
	cols := map[string]any{
		"ComponentId":        rec.ComponentID,
		"ObservedGeneration": spanner.NullInt64{},
		"AccessPayload":      accessBytes,
		"UpdateTime":         spanner.CommitTimestamp,
	}
	if rec.ObservedGeneration != nil {
		cols["ObservedGeneration"] = *rec.ObservedGeneration
	}
	if updateState {
		cols["State"] = int64(rec.State)
	}
	return spanner.UpdateMap("Components", cols), nil
}

// ComponentSoftDeleteMutation builds a Spanner mutation marking rec as DELETED and optionally updating its owner columns.
func ComponentSoftDeleteMutation(rec *store.ComponentRecord, owner *store.SourceOwner) (*spanner.Mutation, error) {
	if rec == nil {
		return nil, fmt.Errorf("component record must not be nil: %w", grpcerr.ErrInvalidArgument)
	}
	configBytes, accessBytes, err := data.MarshalComponentPayloads(rec.Name, rec.Spec, rec.Labels, rec.Status)
	if err != nil {
		return nil, err
	}
	cols := map[string]any{
		"ComponentId":   rec.ComponentID,
		"ConfigProto":   configBytes,
		"AccessPayload": accessBytes,
		"State":         int64(componentspb.ComponentState_COMPONENT_STATE_DELETED),
		"UpdateTime":    spanner.CommitTimestamp,
	}
	if owner != nil {
		maps.Copy(cols, ownerColumns(owner))
	}
	return spanner.UpdateMap("Components", cols), nil
}

// ComponentOwnerUpdateMutation builds a Spanner mutation updating componentID's source owner columns.
func ComponentOwnerUpdateMutation(componentID int64, owner *store.SourceOwner) *spanner.Mutation {
	if owner == nil {
		return nil
	}
	cols := ownerColumns(owner)
	cols["ComponentId"] = componentID
	return spanner.UpdateMap("Components", cols)
}

func appendDependencyMutations(mutations []*spanner.Mutation, componentID int64, deps []string, depIDs map[string]int64) ([]*spanner.Mutation, error) {
	for _, dep := range deps {
		depID, ok := depIDs[dep]
		if !ok {
			return nil, fmt.Errorf("missing component ID for dependency %q: %w", dep, grpcerr.ErrFailedPrecondition)
		}
		mutations = append(mutations, spanner.Insert(
			"ComponentDependencies",
			[]string{"ComponentId", "DependencyComponentId"},
			[]any{componentID, depID},
		))
	}
	return mutations, nil
}

func ownerColumns(owner *store.SourceOwner) map[string]any {
	return map[string]any{
		"SourceUid":          owner.UID,
		"SourceGeneration":   owner.Generation,
		"SourceNamespace":    owner.Namespace,
		"SourceClusterUid":   owner.ClusterUID,
		"SourceEpoch":        owner.Epoch,
		"SourceLastSeenTime": spanner.CommitTimestamp,
	}
}
