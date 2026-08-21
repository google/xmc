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
		rawYAML := `
apiVersion: components.xmc.dev/v1alpha1
kind: Component
metadata:
  name: demo-comp
spec:
  dependencies:
    - cert-manager
    - name: kueue
      version: ">=0.19.0"
      optional: true
`
		var comp linter.ComponentManifest
		if err := yaml.Unmarshal([]byte(rawYAML), &comp); err != nil {
			t.Fatalf("failed to unmarshal component manifest: %v", err)
		}
		if len(comp.Spec.Dependencies) != 2 || comp.Spec.Dependencies[0].Name != "cert-manager" {
			t.Fatalf("expected 2 dependencies with [0]=cert-manager, got %+v", comp.Spec.Dependencies)
		}
		if comp.Spec.Dependencies[1].Name != "kueue" || comp.Spec.Dependencies[1].Version != ">=0.19.0" || !comp.Spec.Dependencies[1].Optional {
			t.Errorf("dep[1] = %+v, want name=kueue, version='>=0.19.0', optional=true", comp.Spec.Dependencies[1])
		}
	})

	t.Run("unmarshal mapping dependencies format", func(t *testing.T) {
		t.Parallel()
		rawYAML := `
apiVersion: components.xmc.dev/v1alpha1
kind: Component
metadata:
  name: demo-comp
spec:
  dependencies:
    cert-manager: "^1.12.0"
    kueue:
      version: ">=0.19.0"
      optional: true
`
		var comp linter.ComponentManifest
		if err := yaml.Unmarshal([]byte(rawYAML), &comp); err != nil {
			t.Fatalf("failed to unmarshal component manifest: %v", err)
		}
		if len(comp.Spec.Dependencies) != 2 {
			t.Fatalf("expected 2 dependencies, got %d", len(comp.Spec.Dependencies))
		}
	})

	t.Run("unmarshal wrapperRef alias fallback", func(t *testing.T) {
		t.Parallel()
		rawYAML := `
apiVersion: components.xmc.dev/v1alpha1
kind: Component
metadata:
  name: 3p-demo
spec:
  componentType: CUSTOM_3P_WRAPPER
  wrapperRef:
    group: jobset.x-k8s.io
    version: v1alpha2
    kind: JobSet
    namespace: xmc-system
    name: jobset-controller
`
		var comp linter.ComponentManifest
		if err := yaml.Unmarshal([]byte(rawYAML), &comp); err != nil {
			t.Fatalf("failed to unmarshal component manifest: %v", err)
		}
		w := comp.Spec.WrapperRef
		if w == nil || w.CrdGroup != "jobset.x-k8s.io" || w.CrdVersion != "v1alpha2" || w.CrdKind != "JobSet" || w.TargetNamespace != "xmc-system" || w.TargetName != "jobset-controller" {
			t.Errorf("wrapperRef = %+v, mismatch with alias decoding", w)
		}
	})

	t.Run("unmarshal lifecycleControl duration strings and integers", func(t *testing.T) {
		t.Parallel()
		rawYAML := `
apiVersion: components.xmc.dev/v1alpha1
kind: Component
metadata:
  name: lc-demo
spec:
  lifecycleControl:
    drainingTimeout: "5m"
    gracePeriod: "30"
    forceDeletion: true
`
		var comp linter.ComponentManifest
		if err := yaml.Unmarshal([]byte(rawYAML), &comp); err != nil {
			t.Fatalf("failed to unmarshal component manifest: %v", err)
		}
		lc := comp.Spec.LifecycleControl
		if lc == nil || lc.DrainTimeoutSeconds != 300 || lc.GracePeriodSeconds != 30 || !lc.ForceDeletion {
			t.Errorf("lifecycleControl = %+v, want 300s, 30s, forceDeletion=true", lc)
		}
	})

	t.Run("unmarshal sub-second duration rounds to at least one second", func(t *testing.T) {
		t.Parallel()
		var comp linter.ComponentManifest
		if err := yaml.Unmarshal([]byte(`spec: { lifecycleControl: { drainingTimeout: "500ms" } }`), &comp); err != nil {
			t.Fatalf("failed to unmarshal: %v", err)
		}
		if comp.Spec.LifecycleControl.DrainTimeoutSeconds != 1 {
			t.Errorf("DrainTimeoutSeconds = %d, want 1", comp.Spec.LifecycleControl.DrainTimeoutSeconds)
		}
	})

	t.Run("component isEnabled defaulting", func(t *testing.T) {
		t.Parallel()
		var compEnabled, compDisabled, compDefault linter.ComponentManifest
		_ = yaml.Unmarshal([]byte(`spec: { enabled: true }`), &compEnabled)
		_ = yaml.Unmarshal([]byte(`spec: { enabled: false }`), &compDisabled)
		_ = yaml.Unmarshal([]byte(`spec: { version: "1.0.0" }`), &compDefault)
		if !compEnabled.IsEnabled() || compDisabled.IsEnabled() || !compDefault.IsEnabled() {
			t.Errorf("enabled checks failed: enabled=%v, disabled=%v, default=%v", compEnabled.IsEnabled(), compDisabled.IsEnabled(), compDefault.IsEnabled())
		}
	})

	t.Run("unmarshal invalid or negative duration returns error", func(t *testing.T) {
		t.Parallel()
		var compInvalid, compNegative linter.ComponentManifest
		if err := yaml.Unmarshal([]byte(`spec: { lifecycleControl: { drainingTimeout: "invalid-duration" } }`), &compInvalid); err == nil {
			t.Errorf("expected error for invalid drainingTimeout string")
		}
		if err := yaml.Unmarshal([]byte(`spec: { lifecycleControl: { drainingTimeout: "-30s" } }`), &compNegative); err == nil {
			t.Errorf("expected error for negative drainingTimeout")
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
