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
	"testing"

	"github.com/google/go-cmp/cmp"
	componentspb "xmanager_cloud/api/protos/components/v1alpha1"
	"xmanager_cloud/pkg/components/store/lifecycle"
)

func TestHPOCatalog_IsReady(t *testing.T) {
	params := map[string]string{"maxTrialBudget": "5"}
	catalog := HPOCatalog(params)

	if diff := cmp.Diff(params, catalog["hpo"].Spec.GetParameters()); diff != "" {
		t.Errorf("hpo parameters mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff([]string{"vizier", "clickhouse"}, catalog["hpo"].Spec.GetDependencies()); diff != "" {
		t.Errorf("hpo dependencies mismatch (-want +got):\n%s", diff)
	}
	for name, rec := range catalog {
		result, err := lifecycle.EvaluateComponentRequirement(rec, name, componentspb.ComponentType_COMPONENT_TYPE_UNSPECIFIED, "")
		if err != nil || !result.IsSatisfied {
			t.Errorf("EvaluateComponentRequirement(%q) = %+v, %v, want satisfied", name, result, err)
		}
		if rec.ObservedGeneration == nil || *rec.ObservedGeneration != rec.Generation {
			t.Errorf("%q observed generation = %v, want %d", name, rec.ObservedGeneration, rec.Generation)
		}
	}
}

func TestHPOCatalog_ReturnsIndependentCopies(t *testing.T) {
	HPOCatalog(nil)["hpo"].Spec.Version = "mutated"
	if got := HPOCatalog(nil)["hpo"].Spec.GetVersion(); got == "mutated" {
		t.Errorf("HPOCatalog() shares state between calls: hpo version = %q", got)
	}
}
