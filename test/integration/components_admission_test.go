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

// Package integration contains hermetic tests that run against a real Kubernetes API server
// started by controller-runtime envtest.
//
// This file asserts the admission contract of the Component CRD: the CEL schema validations that
// reject malformed specs, and the ValidatingAdmissionPolicy that blocks deletion of protected
// Components. Both are loaded from the manifests the Helm chart ships.
package integration

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"

	"xmanager_cloud/test/integration/componentenv"
)

// harness is the control plane shared by every test in this binary. Booting an API server costs
// seconds, so it is started once in TestMain and isolated per test via componentenv.NewNamespace.
var harness *componentenv.Harness

func TestMain(m *testing.M) {
	os.Exit(runSuite(m))
}

// runSuite owns the control-plane lifecycle so that deferred shutdown runs before os.Exit.
func runSuite(m *testing.M) int {
	ctx := context.Background()
	started, err := componentenv.New(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to start envtest control plane: %v\n", err)
		return 1
	}
	defer func() {
		if err := started.Stop(); err != nil {
			fmt.Fprintf(os.Stderr, "failed to stop envtest control plane: %v\n", err)
		}
	}()
	if err := started.WaitForDeletionPolicy(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "deletion-protection policy never became active: %v\n", err)
		return 1
	}
	harness = started
	return m.Run()
}

func TestComponentSchemaValidation(t *testing.T) {
	tests := []struct {
		name string
		// spec is merged over a minimal valid spec before the Component is created.
		spec map[string]any
		// wantMessage is a substring of the expected rejection message. Empty means the
		// Component is expected to be admitted.
		wantMessage string
	}{
		{
			name: "minimal valid spec is admitted",
			spec: map[string]any{},
		},
		{
			name: "1P component without wrapperRef is admitted",
			spec: map[string]any{"componentType": "COMPONENT_TYPE_CUSTOM_1P_COMPONENT"},
		},
		{
			name: "3P wrapper with wrapperRef is admitted",
			spec: map[string]any{
				"componentType": "COMPONENT_TYPE_CUSTOM_3P_WRAPPER",
				"wrapperRef": map[string]any{
					"group": "example.com", "version": "v1", "kind": "Widget", "name": "w",
				},
			},
		},
		{
			name:        "3P wrapper without wrapperRef is rejected",
			spec:        map[string]any{"componentType": "COMPONENT_TYPE_CUSTOM_3P_WRAPPER"},
			wantMessage: "wrapperRef must be specified when componentType is COMPONENT_TYPE_CUSTOM_3P_WRAPPER",
		},
		{
			name:        "unknown componentType is rejected by the enum",
			spec:        map[string]any{"componentType": "COMPONENT_TYPE_UNSPECIFIED"},
			wantMessage: "supported values",
		},
		{
			name: "valid deletionPolicy is admitted",
			spec: map[string]any{"deletionPolicy": "Orphan"},
		},
		{
			name:        "unknown deletionPolicy is rejected by the enum",
			spec:        map[string]any{"deletionPolicy": "Retain"},
			wantMessage: "supported values",
		},
		{
			name: "zero drainTimeoutSeconds is admitted",
			spec: map[string]any{"lifecycleControl": map[string]any{"drainTimeoutSeconds": int64(0)}},
		},
		{
			name:        "negative drainTimeoutSeconds is rejected",
			spec:        map[string]any{"lifecycleControl": map[string]any{"drainTimeoutSeconds": int64(-1)}},
			wantMessage: "should be greater than or equal to 0",
		},
		{
			name: "3P wrapperRef missing required name is rejected",
			spec: map[string]any{
				"componentType": "COMPONENT_TYPE_CUSTOM_3P_WRAPPER",
				"wrapperRef":    map[string]any{"group": "example.com", "version": "v1", "kind": "Widget"},
			},
			wantMessage: "spec.wrapperRef.name: Required value",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			clients := newNamespace(t)
			component := componentenv.NewComponent(componentName(test.name), true)
			mergeSpec(t, component, test.spec)

			_, err := clients.Admin.Create(t.Context(), component, metav1.CreateOptions{})

			if test.wantMessage == "" {
				if err != nil {
					t.Fatalf("Create() returned unexpected error: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("Create() succeeded, want rejection containing %q", test.wantMessage)
			}
			if !apierrors.IsInvalid(err) {
				t.Errorf("Create() error is %T, want an Invalid API status error: %v", err, err)
			}
			if !strings.Contains(err.Error(), test.wantMessage) {
				t.Errorf("Create() error = %q, want it to contain %q", err.Error(), test.wantMessage)
			}
		})
	}
}

func TestComponentRequiredFieldValidation(t *testing.T) {
	t.Parallel()
	clients := newNamespace(t)
	component := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": componentenv.ComponentGVR.Group + "/" + componentenv.ComponentGVR.Version,
		"kind":       "Component",
		"metadata":   map[string]any{"name": "missing-enabled"},
		"spec":       map[string]any{},
	}}

	_, err := clients.Admin.Create(t.Context(), component, metav1.CreateOptions{})

	if err == nil {
		t.Fatalf("Create() succeeded for a spec without the required 'enabled' field, want rejection")
	}
	if !strings.Contains(err.Error(), "spec.enabled: Required value") {
		t.Errorf("Create() error = %q, want it to report the missing 'enabled' field", err.Error())
	}
}

func TestComponentMissingSpecIsRejected(t *testing.T) {
	t.Parallel()
	clients := newNamespace(t)
	// No spec block at all. The per-field "required" list inside spec cannot catch this, because
	// it only applies once spec exists.
	component := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": componentenv.ComponentGVR.Group + "/" + componentenv.ComponentGVR.Version,
		"kind":       "Component",
		"metadata":   map[string]any{"name": "missing-spec"},
	}}

	_, err := clients.Admin.Create(t.Context(), component, metav1.CreateOptions{})

	if err == nil {
		t.Fatalf("Create() succeeded for a Component with no spec, want rejection")
	}
	if !strings.Contains(err.Error(), "spec: Required value") {
		t.Errorf("Create() error = %q, want the standard schema message 'spec: Required value'", err.Error())
	}
}

func TestComponentSchemaDefaults(t *testing.T) {
	t.Parallel()
	clients := newNamespace(t)
	component := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": componentenv.ComponentGVR.Group + "/" + componentenv.ComponentGVR.Version,
		"kind":       "Component",
		"metadata":   map[string]any{"name": "schema-defaults"},
		"spec":       map[string]any{"enabled": true},
	}}

	created, err := clients.Admin.Create(t.Context(), component, metav1.CreateOptions{})
	if err != nil {
		t.Fatalf("Create() returned unexpected error: %v", err)
	}

	// deletionProtection must default to true: the deletion-protection policy treats an absent
	// value as protected, so a defaulting regression would silently allow deletions.
	assertSpecField(t, created, true, "deletionProtection")
	assertSpecField(t, created, "Delete", "deletionPolicy")
	assertSpecField(t, created, "COMPONENT_TYPE_CUSTOM_1P_COMPONENT", "componentType")
}

func TestDeletionProtectionPolicy(t *testing.T) {
	tests := []struct {
		name string
		// deletionProtection is the spec value the Component is created with.
		deletionProtection bool
		// deleteAsAdmin issues the delete as a system:masters user, which the policy exempts.
		deleteAsAdmin bool
		// wantRejected is whether the DELETE is expected to be denied by the policy.
		wantRejected bool
	}{
		{
			name:               "protected component cannot be deleted",
			deletionProtection: true,
			wantRejected:       true,
		},
		{
			name:               "unprotected component can be deleted",
			deletionProtection: false,
			wantRejected:       false,
		},
		{
			name:               "system:masters breakglass bypasses protection",
			deletionProtection: true,
			deleteAsAdmin:      true,
			wantRejected:       false,
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			clients := newNamespace(t)
			component := componentenv.NewComponent(componentName(test.name), test.deletionProtection)
			created, err := clients.Admin.Create(t.Context(), component, metav1.CreateOptions{})
			if err != nil {
				t.Fatalf("Create() returned unexpected error: %v", err)
			}

			deleter := clients.PolicySubject
			if test.deleteAsAdmin {
				deleter = clients.Admin
			}
			err = deleter.Delete(t.Context(), created.GetName(), metav1.DeleteOptions{})

			if !test.wantRejected {
				if err != nil {
					t.Fatalf("Delete() returned unexpected error: %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("Delete() succeeded, want rejection by the deletion-protection policy")
			}
			if !componentenv.IsDeniedByPolicy(err) {
				t.Errorf("Delete() error is %T, want a ValidatingAdmissionPolicy denial: %v", err, err)
			}
			const wantMessage = "Deletion rejected: Component has deletionProtection enabled"

			if !strings.Contains(err.Error(), wantMessage) {
				t.Errorf("Delete() error = %q, want it to contain %q", err.Error(), wantMessage)
			}
		})
	}
}

func TestDeletionProtectionPolicyIgnoresUpdates(t *testing.T) {
	t.Parallel()
	clients := newNamespace(t)
	created, err := clients.Admin.Create(t.Context(), componentenv.NewComponent("update-protected", true), metav1.CreateOptions{})
	if err != nil {
		t.Fatalf("Create() returned unexpected error: %v", err)
	}

	// The policy matches DELETE only, so clearing protection must itself be permitted. This is
	// the documented escape hatch: disable protection, then delete.
	if err := unstructured.SetNestedField(created.Object, false, "spec", "deletionProtection"); err != nil {
		t.Fatalf("SetNestedField() returned unexpected error: %v", err)
	}
	updated, err := clients.PolicySubject.Update(t.Context(), created, metav1.UpdateOptions{})
	if err != nil {
		t.Fatalf("Update() clearing deletionProtection returned unexpected error: %v", err)
	}
	if err := clients.PolicySubject.Delete(t.Context(), updated.GetName(), metav1.DeleteOptions{}); err != nil {
		t.Fatalf("Delete() after clearing deletionProtection returned unexpected error: %v", err)
	}
}

// newNamespace provisions an isolated namespace and its Component clients for a single test.
func newNamespace(t *testing.T) *componentenv.Clients {
	t.Helper()
	clients, err := harness.NewNamespace(t.Context())
	if err != nil {
		t.Fatalf("creating test namespace: %v", err)
	}
	return clients
}

// mergeSpec overlays the supplied fields onto the Component's existing spec.
func mergeSpec(t *testing.T, component *unstructured.Unstructured, spec map[string]any) {
	t.Helper()
	for key, value := range spec {
		if err := unstructured.SetNestedField(component.Object, value, "spec", key); err != nil {
			t.Fatalf("SetNestedField(spec.%s) returned unexpected error: %v", key, err)
		}
	}
}

// assertSpecField fails the test unless the Component's spec field equals want.
func assertSpecField(t *testing.T, component *unstructured.Unstructured, want any, fields ...string) {
	t.Helper()
	path := append([]string{"spec"}, fields...)
	got, found, err := unstructured.NestedFieldNoCopy(component.Object, path...)
	if err != nil {
		t.Fatalf("NestedFieldNoCopy(%v) returned unexpected error: %v", path, err)
	}
	if !found {
		t.Errorf("spec.%s is absent, want %v", strings.Join(fields, "."), want)
		return
	}
	if got != want {
		t.Errorf("spec.%s = %v, want %v", strings.Join(fields, "."), got, want)
	}
}

// componentName derives a DNS-compatible object name from a test case name.
func componentName(testName string) string {
	name := strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			return r
		case r >= 'A' && r <= 'Z':
			return r + ('a' - 'A')
		default:
			return '-'
		}
	}, testName)
	return strings.Trim(name, "-")
}
