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

package dependency

import (
	"fmt"
	"slices"

	"google.golang.org/grpc/codes"

	componentspb "xmanager_cloud/api/protos/components/v1alpha1"
	"xmanager_cloud/pkg/components/store"
)

// DependencyNotFoundMessagePrefix starts the FailedPrecondition message returned when a component
// declares a dependency that is not registered. The message is human-readable only: callers,
// including the GitOps bridge, match the google.rpc.ErrorInfo reason ReasonDependencyNotRegistered.
const DependencyNotFoundMessagePrefix = "dependency not found:"

// ValidateIntentDependencies checks that the dependencies deps of an intent for canonicalName keep
// the dependency graph acyclic and are all registered in catalog. MemoryStore and dbstore.Store both
// call it before applying an intent so they reject the same intents with the same errors. It
// returns the ComponentID of each dependency.
func ValidateIntentDependencies(catalog map[string]*store.ComponentRecord, canonicalName string, deps []string) (map[string]int64, error) {
	if err := ValidateProspectiveDependenciesFromMap(catalog, canonicalName, deps); err != nil {
		return nil, err
	}
	return ResolveDependencyIDs(catalog, canonicalName, deps)
}

// ValidateIntentStability rejects an intent with ReasonStabilityInversion when a STABLE (including
// UNSPECIFIED) or PREVIEW component would depend on an EXPERIMENTAL component, either because
// canonicalName itself declares an EXPERIMENTAL dependency in deps or because canonicalName is
// becoming EXPERIMENTAL while an active STABLE or PREVIEW component in catalog depends on it.
func ValidateIntentStability(catalog map[string]*store.ComponentRecord, canonicalName string, stability componentspb.ComponentStability, deps []string) error {
	if stability != componentspb.ComponentStability_COMPONENT_STABILITY_EXPERIMENTAL {
		for _, dep := range deps {
			depRec := catalog[dep]
			if depRec != nil && !store.IsDeletedRecord(depRec) && depRec.Spec.GetStability() == componentspb.ComponentStability_COMPONENT_STABILITY_EXPERIMENTAL {
				return stabilityInversionError(canonicalName, stability, dep)
			}
		}
		return nil
	}
	return validateExperimentalDependents(catalog, canonicalName)
}

func validateExperimentalDependents(catalog map[string]*store.ComponentRecord, experimentalDep string) error {
	var names []string
	for name, rec := range catalog {
		if name == experimentalDep || rec == nil || store.IsDeletedRecord(rec) {
			continue
		}
		if rec.Spec.GetStability() != componentspb.ComponentStability_COMPONENT_STABILITY_EXPERIMENTAL && slices.Contains(rec.Dependencies, experimentalDep) {
			names = append(names, name)
		}
	}
	if len(names) == 0 {
		return nil
	}
	slices.Sort(names)
	first := catalog[names[0]]
	return stabilityInversionError(first.Name, first.Spec.GetStability(), experimentalDep)
}

func stabilityInversionError(dependent string, dependentStability componentspb.ComponentStability, experimentalDep string) error {
	tier := "STABLE"
	if dependentStability == componentspb.ComponentStability_COMPONENT_STABILITY_PREVIEW {
		tier = "PREVIEW"
	}
	return store.ErrorWithInfo(codes.FailedPrecondition, store.ReasonStabilityInversion,
		map[string]string{
			store.MetadataKeyOperation:  store.OperationSetComponentIntent,
			store.MetadataKeyComponent:  dependent,
			store.MetadataKeyDependency: experimentalDep,
		},
		fmt.Sprintf("stability inversion: %s component %q cannot depend on EXPERIMENTAL component %q", tier, dependent, experimentalDep))
}

// ResolveDependencyIDs maps each dependency name to the ComponentID of its registered, non-deleted record.
// It returns a FailedPrecondition error naming the first dependency that is not registered.
func ResolveDependencyIDs(catalog map[string]*store.ComponentRecord, canonicalName string, deps []string) (map[string]int64, error) {
	depIDs := make(map[string]int64, len(deps))
	for _, dep := range deps {
		depRec, ok := catalog[dep]
		if !ok || depRec == nil || store.IsDeletedRecord(depRec) {
			return nil, dependencyNotRegisteredError(canonicalName, dep)
		}
		depIDs[dep] = depRec.ComponentID
	}
	return depIDs, nil
}

// dependencyNotRegisteredError returns the FailedPrecondition DEPENDENCY_NOT_REGISTERED rejection
// of an intent for canonicalName that depends on the unregistered dep.
func dependencyNotRegisteredError(canonicalName, dep string) error {
	return store.ErrorWithInfo(codes.FailedPrecondition, store.ReasonDependencyNotRegistered,
		map[string]string{store.MetadataKeyOperation: store.OperationSetComponentIntent, store.MetadataKeyComponent: canonicalName, store.MetadataKeyDependency: dep},
		fmt.Sprintf(DependencyNotFoundMessagePrefix+" component %q depends on %q, which is not registered", canonicalName, dep))
}
