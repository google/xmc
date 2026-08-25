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

package linter_test

import (
	"testing"

	"gopkg.in/yaml.v3"

	"xmanager_cloud/pkg/components/linter"
)

func TestManifest_YAMLUnmarshaling(t *testing.T) {
	t.Parallel()

	t.Run("unmarshal flexible dependencies list of strings and objects", func(t *testing.T) {
		t.Parallel()
		rawYAML := "spec:\n  dependencies:\n    - cert-manager\n    - name: kueue\n      version: \">=0.19.0\"\n      optional: true\n"
		var comp linter.ComponentManifest
		if err := yaml.Unmarshal([]byte(rawYAML), &comp); err != nil || len(comp.Spec.Dependencies) != 2 {
			t.Fatalf("failed to unmarshal: %v", err)
		}
		if comp.Spec.Dependencies[0].Name != "cert-manager" || comp.Spec.Dependencies[1].Name != "kueue" || !comp.Spec.Dependencies[1].Optional {
			t.Errorf("dep mismatch: %+v", comp.Spec.Dependencies)
		}
	})

	t.Run("unmarshal mapping dependencies format", func(t *testing.T) {
		t.Parallel()
		var comp, compNull linter.ComponentManifest
		if err := yaml.Unmarshal([]byte(`spec: { dependencies: { cert-manager: "^1.12.0", kueue: { version: ">=0.19.0", optional: true } } }`), &comp); err != nil || len(comp.Spec.Dependencies) != 2 {
			t.Fatalf("unmarshal mapping deps failed: %v", err)
		}
		if err := yaml.Unmarshal([]byte(`spec: { dependencies: { cert-manager: null, kueue: "~" } }`), &compNull); err != nil || len(compNull.Spec.Dependencies) != 2 || compNull.Spec.Dependencies[0].Version != "" {
			t.Fatalf("unmarshal null mapping deps failed: %v", err)
		}
	})

	t.Run("unmarshal wrapperRef alias fallback", func(t *testing.T) {
		t.Parallel()
		var comp linter.ComponentManifest
		if err := yaml.Unmarshal([]byte(`spec: { componentType: CUSTOM_3P_WRAPPER, wrapperRef: { group: jobset.x-k8s.io, version: v1alpha2, kind: JobSet, namespace: xmc-system, name: jobset-controller } }`), &comp); err != nil {
			t.Fatalf("unmarshal wrapperRef: %v", err)
		}
		w := comp.Spec.WrapperRef
		if w == nil || w.CrdGroup != "jobset.x-k8s.io" || w.TargetName != "jobset-controller" {
			t.Errorf("wrapperRef mismatch: %+v", w)
		}
	})

	t.Run("unmarshal lifecycleControl durations", func(t *testing.T) {
		t.Parallel()
		var comp, compMs, compZero, compNull, compExplicitZero, compStringFields linter.ComponentManifest
		if err := yaml.Unmarshal([]byte(`spec: { lifecycleControl: { drainingTimeout: "5m", gracePeriod: "30", forceDeletion: true } }`), &comp); err != nil || comp.Spec.LifecycleControl.DrainTimeoutSeconds == nil || *comp.Spec.LifecycleControl.DrainTimeoutSeconds != 300 {
			t.Errorf("lifecycleControl mismatch: %+v", comp.Spec.LifecycleControl)
		}
		if err := yaml.Unmarshal([]byte(`spec: { lifecycleControl: { drainingTimeout: "500ms" } }`), &compMs); err != nil || compMs.Spec.LifecycleControl.DrainTimeoutSeconds == nil || *compMs.Spec.LifecycleControl.DrainTimeoutSeconds != 1 {
			t.Errorf("sub-second round failed: %+v", compMs.Spec.LifecycleControl)
		}
		if err := yaml.Unmarshal([]byte(`spec: { lifecycleControl: { drainingTimeout: "0s" } }`), &compZero); err != nil || compZero.Spec.LifecycleControl.DrainTimeoutSeconds == nil || *compZero.Spec.LifecycleControl.DrainTimeoutSeconds != 0 {
			t.Errorf("zero seconds failed: %+v", compZero.Spec.LifecycleControl)
		}
		if err := yaml.Unmarshal([]byte(`spec: { lifecycleControl: { drainingTimeout: null, gracePeriod: "~" } }`), &compNull); err != nil || compNull.Spec.LifecycleControl.DrainTimeoutSeconds == nil || *compNull.Spec.LifecycleControl.DrainTimeoutSeconds != 0 {
			t.Errorf("null duration failed: %+v", compNull.Spec.LifecycleControl)
		}
		if err := yaml.Unmarshal([]byte(`spec: { lifecycleControl: { drainTimeoutSeconds: 0, drainingTimeout: "5m" } }`), &compExplicitZero); err != nil || compExplicitZero.Spec.LifecycleControl.DrainTimeoutSeconds == nil || *compExplicitZero.Spec.LifecycleControl.DrainTimeoutSeconds != 0 {
			t.Errorf("explicit zero timeout failed: %+v", compExplicitZero.Spec.LifecycleControl)
		}
		if err := yaml.Unmarshal([]byte(`spec: { lifecycleControl: { drainTimeoutSeconds: "60s", gracePeriodSeconds: "30s" } }`), &compStringFields); err != nil || compStringFields.Spec.LifecycleControl.DrainTimeoutSeconds == nil || *compStringFields.Spec.LifecycleControl.DrainTimeoutSeconds != 60 || compStringFields.Spec.LifecycleControl.GracePeriodSeconds == nil || *compStringFields.Spec.LifecycleControl.GracePeriodSeconds != 30 {
			t.Errorf("string fields parsing failed: %+v", compStringFields.Spec.LifecycleControl)
		}
		var compOmitted linter.ComponentManifest
		if err := yaml.Unmarshal([]byte(`spec: { lifecycleControl: { forceDeletion: true } }`), &compOmitted); err != nil || compOmitted.Spec.LifecycleControl.DrainTimeoutSeconds != nil || compOmitted.Spec.LifecycleControl.GracePeriodSeconds != nil {
			t.Errorf("omitted fields should be nil: %+v", compOmitted.Spec.LifecycleControl)
		}
	})

	t.Run("component isEnabled defaulting", func(t *testing.T) {
		t.Parallel()
		var c1, c2, c3 linter.ComponentManifest
		_ = yaml.Unmarshal([]byte(`spec: { enabled: true }`), &c1)
		_ = yaml.Unmarshal([]byte(`spec: { enabled: false }`), &c2)
		_ = yaml.Unmarshal([]byte(`spec: { version: "1.0.0" }`), &c3)
		if !c1.IsEnabled() || c2.IsEnabled() || !c3.IsEnabled() {
			t.Errorf("enabled defaulting failed: c1=%v, c2=%v, c3=%v", c1.IsEnabled(), c2.IsEnabled(), c3.IsEnabled())
		}
	})

	t.Run("unmarshal invalid inputs returns error", func(t *testing.T) {
		t.Parallel()
		for _, s := range []string{
			`spec: { lifecycleControl: { drainingTimeout: "invalid" } }`,
			`spec: { lifecycleControl: { drainingTimeout: "-30s" } }`,
			`spec: { lifecycleControl: { drainingTimeout: "-100ms" } }`,
			`spec: { lifecycleControl: { drainingTimeout: -10 } }`,
			`spec: { lifecycleControl: { gracePeriod: -5 } }`,
			`spec: { lifecycleControl: { gracePeriod: "-10s" } }`,
			`spec: { lifecycleControl: { drainingTimeout: [1, 2] } }`,
			`spec: { lifecycleControl: { gracePeriod: { k: v } } }`,
			`spec: { lifecycleControl: "invalid-string" }`,
			`spec: { wrapperRef: "invalid-string" }`,
			`spec: { dependencies: { comp-a: [nested, list] } }`,
			`spec: { dependencies: "scalar-string" }`,
			`spec: { dependencies: [ { name: [ nested ] } ] }`,
		} {
			var comp linter.ComponentManifest
			if err := yaml.Unmarshal([]byte(s), &comp); err == nil {
				t.Errorf("expected error for %s", s)
			}
		}
	})

	t.Run("unmarshal yaml alias nodes", func(t *testing.T) {
		t.Parallel()
		rawYAML := "shared_dep: &cert_dep { name: cert-manager, version: \">=1.0.0\" }\nshared_key: &dep_key \"kueue\"\nshared_timeout: &timeout \"60s\"\nspec:\n  dependencies:\n    - *cert_dep\n  lifecycleControl: { drainingTimeout: *timeout }\n"
		var comp linter.ComponentManifest
		if err := yaml.Unmarshal([]byte(rawYAML), &comp); err != nil || len(comp.Spec.Dependencies) != 1 || comp.Spec.LifecycleControl.DrainTimeoutSeconds == nil || *comp.Spec.LifecycleControl.DrainTimeoutSeconds != 60 {
			t.Fatalf("alias decoding failed: %v", err)
		}

		mapYAML := "shared_key: &dep_key \"kueue\"\nspec:\n  dependencies:\n    *dep_key: \">=0.19.0\"\n"
		var compMap linter.ComponentManifest
		if err := yaml.Unmarshal([]byte(mapYAML), &compMap); err != nil || len(compMap.Spec.Dependencies) != 1 || compMap.Spec.Dependencies[0].Name != "kueue" {
			t.Fatalf("mapping key alias decoding failed: %v", err)
		}
	})

	t.Run("unmarshal lifecycleControl invalid drain and grace durations", func(t *testing.T) {
		t.Parallel()
		for _, s := range []string{
			`spec: { lifecycleControl: { drainTimeoutSeconds: "invalid" } }`,
			`spec: { lifecycleControl: { gracePeriodSeconds: "invalid" } }`,
			`spec: { lifecycleControl: { drainingTimeout: "" } }`,
			`spec: { dependencies: [ [ nested ] ] }`,
			`spec: { dependencies: { cert-manager: { version: [1, 2] } } }`,
		} {
			var comp linter.ComponentManifest
			if err := yaml.Unmarshal([]byte(s), &comp); err == nil && s != `spec: { lifecycleControl: { drainingTimeout: "" } }` {
				t.Errorf("expected error for %s", s)
			}
		}
	})

	t.Run("unmarshal dependency reference direct and alias", func(t *testing.T) {
		t.Parallel()
		var dep linter.ComponentDependencyRef
		if err := yaml.Unmarshal([]byte(`cert-manager`), &dep); err != nil || dep.Name != "cert-manager" {
			t.Errorf("failed scalar unmarshal: %v", err)
		}
		if err := yaml.Unmarshal([]byte(`[nested]`), &dep); err == nil {
			t.Errorf("expected error for sequence dependency")
		}
		aliasYAML := "alias: &ref\n  name: kueue\n  version: \">=1.0.0\"\ndep:\n  *ref\n"
		var wrapper struct {
			Dep linter.ComponentDependencyRef `yaml:"dep"`
		}
		if err := yaml.Unmarshal([]byte(aliasYAML), &wrapper); err != nil || wrapper.Dep.Name != "kueue" {
			t.Errorf("alias unmarshal failed: %v, got: %+v", err, wrapper.Dep)
		}
	})
}
