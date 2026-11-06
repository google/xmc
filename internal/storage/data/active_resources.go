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

package data

import (
	"slices"

	essmsgpb "xmanager_cloud/api/protos/experiment"
	"xmanager_cloud/internal/common/resourceaccumulator"
	"xmanager_cloud/internal/storage"
	"xmanager_cloud/internal/storage/state"
)

// ActiveWorkUnitResourceSample represents a single active work unit's author, executable, and state.
type ActiveWorkUnitResourceSample struct {
	Creator        string
	Executable     *essmsgpb.Executable
	ExecutionState essmsgpb.WorkUnitStatus_WorkUnitState
}

type authorResourceKey struct {
	author       string
	resourceType string
}

// AggregateAuthorResourceSamples sums normalized executable resources by (Creator, ResourceType),
// split into running and pending units. Samples must be active work units (the storage queries
// select only those); any state other than a running one counts as pending.
func AggregateAuthorResourceSamples(samples []ActiveWorkUnitResourceSample) []storage.AuthorResourceAllocation {
	totals := make(map[authorResourceKey]*storage.AuthorResourceAllocation)
	for _, sample := range samples {
		running := slices.Contains(state.WorkUnitRunningStates, sample.ExecutionState)
		for resType, units := range resourceaccumulator.ExtractNormalizedExecutableResources(sample.Executable) {
			key := authorResourceKey{author: sample.Creator, resourceType: resType}
			entry := totals[key]
			if entry == nil {
				entry = &storage.AuthorResourceAllocation{Author: sample.Creator, ResourceType: resType}
				totals[key] = entry
			}
			if running {
				entry.RunningUnits += units
			} else {
				entry.PendingUnits += units
			}
		}
	}
	result := make([]storage.AuthorResourceAllocation, 0, len(totals))
	for _, entry := range totals {
		result = append(result, *entry)
	}
	return result
}
