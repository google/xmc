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
	"cmp"
	"slices"

	essmsgpb "xmanager_cloud/api/protos/experiment"
	"xmanager_cloud/internal/common/resourceaccumulator"
	"xmanager_cloud/internal/storage"
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
	state        storage.ActiveResourceState
}

// ClassifyActiveResourceState maps a WorkUnitState to running or pending resource allocation state.
func ClassifyActiveResourceState(state essmsgpb.WorkUnitStatus_WorkUnitState) (storage.ActiveResourceState, bool) {
	switch state {
	case essmsgpb.WorkUnitStatus_RUNNING, essmsgpb.WorkUnitStatus_STARTING:
		return storage.ActiveResourceStateRunning, true
	case essmsgpb.WorkUnitStatus_SCHEDULING, essmsgpb.WorkUnitStatus_INITIALIZING:
		return storage.ActiveResourceStatePending, true
	default:
		return "", false
	}
}

// AggregateAuthorResourceSamples sums normalized executable resources by (Creator, ResourceType, State).
func AggregateAuthorResourceSamples(samples []ActiveWorkUnitResourceSample) []storage.AuthorResourceAllocation {
	totals := make(map[authorResourceKey]float64)
	for _, sample := range samples {
		state, ok := ClassifyActiveResourceState(sample.ExecutionState)
		if !ok || sample.Executable == nil {
			continue
		}
		for resType, units := range resourceaccumulator.ExtractNormalizedExecutableResources(sample.Executable) {
			totals[authorResourceKey{author: sample.Creator, resourceType: resType, state: state}] += units
		}
	}
	return sortedAuthorResourceAllocations(totals)
}

func sortedAuthorResourceAllocations(totals map[authorResourceKey]float64) []storage.AuthorResourceAllocation {
	result := make([]storage.AuthorResourceAllocation, 0, len(totals))
	for key, units := range totals {
		result = append(result, storage.AuthorResourceAllocation{
			Author:       key.author,
			ResourceType: key.resourceType,
			State:        key.state,
			Units:        units,
		})
	}
	slices.SortFunc(result, func(a, b storage.AuthorResourceAllocation) int {
		if c := cmp.Compare(a.Author, b.Author); c != 0 {
			return c
		}
		if c := cmp.Compare(a.ResourceType, b.ResourceType); c != 0 {
			return c
		}
		return cmp.Compare(a.State, b.State)
	})
	return result
}
