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

	"google.golang.org/grpc/codes"

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
