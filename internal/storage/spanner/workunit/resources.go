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

package workunit

import (
	essmsgpb "xmanager_cloud/experiment_state_server/proto/messages_go_proto"
	"xmanager_cloud/internal/domain/experimentdomain"
)

// GetWorkUnitResources extracts non-nil ResourceSets from a WorkUnit by delegating to the domain package.
func GetWorkUnitResources(wu *essmsgpb.WorkUnit) []*essmsgpb.ResourceSet {
	return experimentdomain.GetWorkUnitResources(wu)
}

// MergeResourceSets merges resource sets from a WorkUnit into an Experiment's ResourceSet slice by delegating to the domain package.
func MergeResourceSets(expRes []*essmsgpb.ResourceSet, wuRes []*essmsgpb.ResourceSet) []*essmsgpb.ResourceSet {
	return experimentdomain.MergeResourceSets(expRes, wuRes)
}
