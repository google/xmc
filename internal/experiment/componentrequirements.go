// Copyright 2026 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//	http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package experiment

import (
	"context"
	"errors"
	"fmt"
	"slices"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	componentspb "xmanager_cloud/api/protos/components/v1alpha1"
	essmsgpb "xmanager_cloud/api/protos/experiment"
	"xmanager_cloud/internal/grpcerr"
	"xmanager_cloud/internal/storage"
	"xmanager_cloud/pkg/components/store"
	"xmanager_cloud/pkg/components/store/dependency"
	"xmanager_cloud/pkg/components/store/lifecycle"
)

// hpoComponentName is the catalog name of the hyperparameter optimization component.
const hpoComponentName = "hpo"

// ErrComponentRequirementNotMet is wrapped by every error reporting that a component an experiment
// requires is missing or not ready, so that callers can return it to clients as FailedPrecondition
// while reporting other failures as Internal.
var ErrComponentRequirementNotMet = status.Error(codes.FailedPrecondition, "component requirement not met")

// Requirement names a catalog component that an experiment needs. The component must be ENABLED; an
// unspecified Type or empty MinVersion is not checked.
type Requirement struct {
	Name       string
	Type       componentspb.ComponentType
	MinVersion string
}

// typeRequirements lists the components that an experiment type requires and the feature that
// needs them.
type typeRequirements struct {
	feature    string
	components []Requirement
}

// requirementsByType maps each experiment type that requires components to its requirements. It is
// the single place where experiment types map to components: add new experiment types here.
var requirementsByType = map[essmsgpb.Experiment_ExperimentType]typeRequirements{
	essmsgpb.Experiment_STUDY: {
		feature:    "HPO",
		components: []Requirement{{Name: hpoComponentName, Type: componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_1P_COMPONENT}},
	},
}

// RequirementsFor returns the components that exp requires, or nil when it requires none.
func RequirementsFor(exp *essmsgpb.Experiment) []Requirement {
	return slices.Clone(requirementsByType[exp.GetExperimentType()].components)
}

// CheckComponentsFeature returns a FailedPrecondition error when exp requires components and the
// Components feature is disabled.
func CheckComponentsFeature(exp *essmsgpb.Experiment, enableComponents bool) error {
	reqs, ok := requirementsByType[exp.GetExperimentType()]
	if !ok || enableComponents {
		return nil
	}
	return fmt.Errorf("%s requires the Components feature: %w", reqs.feature, grpcerr.ErrFailedPrecondition)
}

// ValidateComponentRequirements checks reqs and their transitive dependencies against one read-only
// snapshot of catalog without writing. It is a cheap pre-check: PinComponentRequirements repeats the
// check authoritatively in the transaction that pins the components.
func ValidateComponentRequirements(ctx context.Context, catalog storage.ComponentRepository, reqs []Requirement) error {
	if len(reqs) == 0 {
		return nil
	}
	return catalog.WithReadOnlySnapshot(ctx, func(ctx context.Context, repo storage.ComponentTxRepository) error {
		_, err := resolveRequirements(ctx, repo, reqs)
		return err
	})
}

// PinComponentRequirements checks reqs and their transitive dependencies through the caller's
// read-write transaction repo, and pins each resolved component to experiment xid at its current
// generation.
func PinComponentRequirements(ctx context.Context, repo storage.ComponentTxRepository, xid int64, reqs []Requirement) error {
	if len(reqs) == 0 {
		return nil
	}
	recs, err := resolveRequirements(ctx, repo, reqs)
	if err != nil {
		return err
	}
	for _, rec := range recs {
		if err := repo.InsertExperimentComponentSnapshot(ctx, xid, rec); err != nil {
			return fmt.Errorf("failed to pin component %q to experiment %d: %w", rec.Name, xid, err)
		}
	}
	return nil
}

// componentReader reads active component records by canonical name.
type componentReader interface {
	GetActiveComponent(ctx context.Context, canonicalName string) (*store.ComponentRecord, error)
}

// resolveRequirements returns the records that satisfy reqs and every transitive dependency they
// declare, each once, in breadth-first order. Requirements are resolved before dependencies, so a
// dependency that is also a requirement is checked against the requirement. Requirement names and
// extracted dependencies are canonical component names, so deduplicating by name is exact.
func resolveRequirements(ctx context.Context, repo componentReader, reqs []Requirement) ([]*store.ComponentRecord, error) {
	queue := slices.Clone(reqs)
	seen := make(map[string]bool, len(reqs))
	var recs []*store.ComponentRecord
	for len(queue) > 0 {
		req := queue[0]
		queue = queue[1:]
		if seen[req.Name] {
			continue
		}
		seen[req.Name] = true
		rec, err := resolveRequirement(ctx, repo, req)
		if err != nil {
			return nil, err
		}
		recs = append(recs, rec)
		queue = append(queue, dependencyRequirements(rec)...)
	}
	return recs, nil
}

// dependencyRequirements returns a requirement that each dependency declared by rec be ENABLED.
// Declared dependencies are used rather than catalog edges, which omit deleted dependencies.
func dependencyRequirements(rec *store.ComponentRecord) []Requirement {
	deps := dependency.ExtractDependencies(rec.Spec, rec.Labels)
	reqs := make([]Requirement, 0, len(deps))
	for _, dep := range deps {
		reqs = append(reqs, Requirement{Name: dep})
	}
	return reqs
}

// resolveRequirement returns the active record of req.Name when it satisfies req and has observed
// its current generation, so that pinning it does not report drift right away.
func resolveRequirement(ctx context.Context, repo componentReader, req Requirement) (*store.ComponentRecord, error) {
	rec, err := repo.GetActiveComponent(ctx, req.Name)
	if errors.Is(err, store.ErrComponentNotFound) {
		return nil, fmt.Errorf("component %q not found: %w", req.Name, ErrComponentRequirementNotMet)
	}
	if err != nil {
		return nil, fmt.Errorf("failed to read component %q: %w", req.Name, err)
	}
	result, err := lifecycle.EvaluateComponentRequirement(rec, req.Name, req.Type, req.MinVersion)
	if err != nil {
		return nil, fmt.Errorf("failed to evaluate component %q: %w", req.Name, err)
	}
	if !result.IsSatisfied {
		return nil, fmt.Errorf("%s: %w", result.Reason, ErrComponentRequirementNotMet)
	}
	if rec.ObservedGeneration == nil || *rec.ObservedGeneration != rec.Generation {
		return nil, fmt.Errorf("component %q has not observed generation %d yet: %w", req.Name, rec.Generation, ErrComponentRequirementNotMet)
	}
	return rec, nil
}
