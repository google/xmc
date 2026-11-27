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

package experimenttest

import (
	"google.golang.org/protobuf/proto"
	componentspb "xmanager_cloud/api/protos/components/v1alpha1"
	"xmanager_cloud/pkg/components/store"
)

// OrderedHPOCatalog returns ready clickhouse (STABLE), vizier (EXPERIMENTAL), and hpo
// (EXPERIMENTAL) records in dependency order with ComponentRecord.Dependencies populated.
func OrderedHPOCatalog(hpoParams map[string]string) []*store.ComponentRecord {
	c := HPOCatalog(hpoParams)
	c["clickhouse"].Spec.Stability = componentspb.ComponentStability_COMPONENT_STABILITY_STABLE
	c["vizier"].Spec.Stability = componentspb.ComponentStability_COMPONENT_STABILITY_EXPERIMENTAL
	c["hpo"].Spec.Stability = componentspb.ComponentStability_COMPONENT_STABILITY_EXPERIMENTAL
	return []*store.ComponentRecord{c["clickhouse"], c["vizier"], c["hpo"]}
}

// HPOCatalog returns new ready catalog records, keyed by name, that satisfy the components a study
// requires: an hpo component with hpoParams as its parameters, which depends on vizier and
// clickhouse components. Every record is ENABLED and has observed its generation.
func HPOCatalog(hpoParams map[string]string) map[string]*store.ComponentRecord {
	hpo := readyRecord(1, "hpo", componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_1P_COMPONENT, "vizier", "clickhouse")
	hpo.Spec.Parameters = hpoParams
	return map[string]*store.ComponentRecord{
		"hpo":        hpo,
		"vizier":     readyRecord(2, "vizier", componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_3P_WRAPPER),
		"clickhouse": readyRecord(3, "clickhouse", componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_3P_WRAPPER),
	}
}

func readyRecord(id int64, name string, componentType componentspb.ComponentType, deps ...string) *store.ComponentRecord {
	return &store.ComponentRecord{
		ComponentID:        id,
		Name:               name,
		Spec:               &componentspb.ComponentSpec{Type: componentType, Version: "v1.0.0", Dependencies: deps},
		Dependencies:       deps,
		State:              componentspb.ComponentState_COMPONENT_STATE_ENABLED,
		Generation:         1,
		ObservedGeneration: proto.Int64(1),
	}
}
