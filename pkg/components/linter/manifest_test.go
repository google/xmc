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
		if err := yaml.Unmarshal([]byte(rawYAML), &comp); err != nil {
			t.Fatalf("failed to unmarshal: %v", err)
		}
		if len(comp.Spec.Dependencies) != 2 || comp.Spec.Dependencies[0].Name != "cert-manager" {
			t.Fatalf("expected 2 dependencies with [0]=cert-manager, got %+v", comp.Spec.Dependencies)
		}
		if comp.Spec.Dependencies[1].Name != "kueue" || comp.Spec.Dependencies[1].Version != ">=0.19.0" || !comp.Spec.Dependencies[1].Optional {
			t.Errorf("dep[1] mismatch: %+v", comp.Spec.Dependencies[1])
		}
	})

	t.Run("unmarshal mapping dependencies format", func(t *testing.T) {
		t.Parallel()
		rawYAML := `spec: { dependencies: { cert-manager: "^1.12.0", kueue: { version: ">=0.19.0", optional: true } } }`
		var comp linter.ComponentManifest
		if err := yaml.Unmarshal([]byte(rawYAML), &comp); err != nil || len(comp.Spec.Dependencies) != 2 {
			t.Fatalf("unmarshal mapping deps failed: err=%v, count=%d", err, len(comp.Spec.Dependencies))
		}
	})

	t.Run("unmarshal wrapperRef alias fallback", func(t *testing.T) {
		t.Parallel()
		rawYAML := `spec: { componentType: CUSTOM_3P_WRAPPER, wrapperRef: { group: jobset.x-k8s.io, version: v1alpha2, kind: JobSet, namespace: xmc-system, name: jobset-controller } }`
		var comp linter.ComponentManifest
		if err := yaml.Unmarshal([]byte(rawYAML), &comp); err != nil {
			t.Fatalf("failed to unmarshal wrapperRef: %v", err)
		}
		w := comp.Spec.WrapperRef
		if w == nil || w.CrdGroup != "jobset.x-k8s.io" || w.CrdVersion != "v1alpha2" || w.CrdKind != "JobSet" || w.TargetNamespace != "xmc-system" || w.TargetName != "jobset-controller" {
			t.Errorf("wrapperRef mismatch: %+v", w)
		}
	})

	t.Run("unmarshal lifecycleControl duration strings and integers", func(t *testing.T) {
		t.Parallel()
		rawYAML := `spec: { lifecycleControl: { drainingTimeout: "5m", gracePeriod: "30", forceDeletion: true } }`
		var comp linter.ComponentManifest
		if err := yaml.Unmarshal([]byte(rawYAML), &comp); err != nil {
			t.Fatalf("failed to unmarshal: %v", err)
		}
		lc := comp.Spec.LifecycleControl
		if lc == nil || lc.DrainTimeoutSeconds != 300 || lc.GracePeriodSeconds != 30 || !lc.ForceDeletion {
			t.Errorf("lifecycleControl mismatch: %+v", lc)
		}
	})

	t.Run("unmarshal sub-second duration rounds to at least one second", func(t *testing.T) {
		t.Parallel()
		var comp linter.ComponentManifest
		if err := yaml.Unmarshal([]byte(`spec: { lifecycleControl: { drainingTimeout: "500ms" } }`), &comp); err != nil || comp.Spec.LifecycleControl.DrainTimeoutSeconds != 1 {
			t.Errorf("DrainTimeoutSeconds = %v, want 1", comp.Spec.LifecycleControl)
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

	t.Run("unmarshal invalid or negative duration returns error", func(t *testing.T) {
		t.Parallel()
		var c1, c2, c3 linter.ComponentManifest
		if err := yaml.Unmarshal([]byte(`spec: { lifecycleControl: { drainingTimeout: "invalid" } }`), &c1); err == nil {
			t.Errorf("expected error for invalid duration")
		}
		if err := yaml.Unmarshal([]byte(`spec: { lifecycleControl: { drainingTimeout: "-30s" } }`), &c2); err == nil {
			t.Errorf("expected error for negative duration")
		}
		if err := yaml.Unmarshal([]byte(`spec: { lifecycleControl: { drainingTimeout: "-100ms" } }`), &c3); err == nil {
			t.Errorf("expected error for negative sub-second duration")
		}
	})

	t.Run("unmarshal non-scalar lifecycleControl returns error", func(t *testing.T) {
		t.Parallel()
		var c1, c2 linter.ComponentManifest
		if err := yaml.Unmarshal([]byte(`spec: { lifecycleControl: { drainingTimeout: [1, 2] } }`), &c1); err == nil {
			t.Errorf("expected error for sequence in drainingTimeout")
		}
		if err := yaml.Unmarshal([]byte(`spec: { lifecycleControl: { gracePeriod: { k: v } } }`), &c2); err == nil {
			t.Errorf("expected error for mapping in gracePeriod")
		}
	})

	t.Run("unmarshal yaml alias nodes", func(t *testing.T) {
		t.Parallel()
		rawYAML := `
shared_dep: &cert_dep { name: cert-manager, version: ">=1.0.0" }
shared_timeout: &timeout "60s"
spec:
  dependencies: [ *cert_dep ]
  lifecycleControl: { drainingTimeout: *timeout }
`
		var comp linter.ComponentManifest
		if err := yaml.Unmarshal([]byte(rawYAML), &comp); err != nil {
			t.Fatalf("failed to unmarshal with yaml aliases: %v", err)
		}
		if len(comp.Spec.Dependencies) != 1 || comp.Spec.Dependencies[0].Name != "cert-manager" || comp.Spec.LifecycleControl.DrainTimeoutSeconds != 60 {
			t.Errorf("alias decoding failed: deps=%+v, lc=%+v", comp.Spec.Dependencies, comp.Spec.LifecycleControl)
		}
	})

	t.Run("unmarshal unexpected dependency format in mapping returns error", func(t *testing.T) {
		t.Parallel()
		var comp linter.ComponentManifest
		if err := yaml.Unmarshal([]byte(`spec: { dependencies: { comp-a: [nested, list] } }`), &comp); err == nil {
			t.Errorf("expected error for unexpected list in mapping dependency value")
		}
	})
}
