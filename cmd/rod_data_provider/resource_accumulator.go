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

package main

import (
	essmsgpb "github.com/google/xmc/api/protos/experiment"
	spb "github.com/google/xmc/api/protos/rod"
	"github.com/google/xmc/internal/common/resourceaccumulator"
)

// extractRequestedResources converts domain ResourceSets from workunit.GetWorkUnitResources
// into presentation DTOs ([]*spb.Resource) required by the ROD API, aggregating identical resource types.
func extractRequestedResources(wu *essmsgpb.WorkUnit) []*spb.Resource {
	if wu == nil || wu.GetExecutable() == nil {
		return nil
	}
	acc := resourceaccumulator.New()
	acc.AccumulateExecutable(wu.GetExecutable())
	totals := acc.Totals()
	order := acc.Order()
	return buildResourceDTOs(totals, order)
}

func buildResourceDTOs(totals map[string]float64, order []string) []*spb.Resource {
	resources := make([]*spb.Resource, 0, len(order))
	for _, resType := range order {
		resources = append(resources, &spb.Resource{
			ResourceType: resType,
			Count:        totals[resType],
		})
	}
	return resources
}
