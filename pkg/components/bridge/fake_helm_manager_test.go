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

package bridge

import (
	"context"
	"errors"
	"testing"
)

func TestFakeHelmManager(t *testing.T) {
	ctx := context.Background()
	mgr := NewFakeHelmManager()

	ref := ComponentRef{
		Namespace: "xmc",
		Name:      "ingestion-gateway",
	}
	opts := HelmComponentOptions{
		Ref:      ref,
		Enabled:  true,
		ImageTag: "v1.0.0",
	}

	if err := mgr.Ensure1PComponent(ctx, ref, opts); err != nil {
		t.Fatalf("Ensure1PComponent failed: %v", err)
	}

	if !mgr.IsComponentActive(ref) {
		t.Error("expected ingestion-gateway to be active")
	}

	if err := mgr.Delete1PComponent(ctx, ref); err != nil {
		t.Fatalf("Delete1PComponent failed: %v", err)
	}

	if mgr.IsComponentActive(ref) {
		t.Error("expected ingestion-gateway to be inactive after delete")
	}

	// Test error injection
	mgr.EnsureErr = errors.New("ensure error")
	if err := mgr.Ensure1PComponent(ctx, ref, opts); err == nil {
		t.Fatal("expected error on EnsureErr injection")
	}

	mgr.DeleteErr = errors.New("delete error")
	if err := mgr.Delete1PComponent(ctx, ref); err == nil {
		t.Fatal("expected error on DeleteErr injection")
	}
	if err := mgr.Purge1PComponent(ctx, ref); err == nil {
		t.Fatal("expected error on Purge1PComponent with DeleteErr injection")
	}
	if err := mgr.Orphan1PComponent(ctx, ref); err == nil {
		t.Fatal("expected error on Orphan1PComponent with DeleteErr injection")
	}

	mgr.DeleteErr = nil
	if err := mgr.Orphan1PComponent(ctx, ref); err != nil {
		t.Fatalf("Orphan1PComponent failed: %v", err)
	}

	// Test nil receiver
	var nilMgr *FakeHelmManager
	if err := nilMgr.Ensure1PComponent(ctx, ref, opts); err != nil {
		t.Errorf("expected nil error on nil receiver, got %v", err)
	}
	if err := nilMgr.Delete1PComponent(ctx, ref); err != nil {
		t.Errorf("expected nil error on nil receiver, got %v", err)
	}
	if err := nilMgr.Purge1PComponent(ctx, ref); err != nil {
		t.Errorf("expected nil error on nil receiver Purge1PComponent, got %v", err)
	}
	if err := nilMgr.Orphan1PComponent(ctx, ref); err != nil {
		t.Errorf("expected nil error on nil receiver Orphan1PComponent, got %v", err)
	}
	if nilMgr.IsComponentActive(ref) {
		t.Error("expected false on nil receiver IsComponentActive")
	}
	if nilMgr.GetAppliedValues(ref) != nil {
		t.Error("expected nil on nil receiver GetAppliedValues")
	}

	// Test applied values lifecycle and getter
	mgr = NewFakeHelmManager()
	if err := mgr.Ensure1PComponent(ctx, ref, opts); err != nil {
		t.Fatalf("Ensure1PComponent failed: %v", err)
	}
	applied := mgr.GetAppliedValues(ref)
	if applied == nil {
		t.Fatal("expected applied values to be non-nil after Ensure")
	}
	if err := mgr.Orphan1PComponent(ctx, ref); err != nil {
		t.Fatalf("Orphan1PComponent failed: %v", err)
	}
	if mgr.GetAppliedValues(ref) == nil {
		t.Fatal("expected applied values to be retained after Orphan")
	}
	if err := mgr.Delete1PComponent(ctx, ref); err != nil {
		t.Fatalf("Delete1PComponent failed: %v", err)
	}
	appliedAfterDelete := mgr.GetAppliedValues(ref)
	if appliedAfterDelete == nil {
		t.Fatal("expected applied values to be retained with enabled=false after Delete")
	}
	depMap, ok := appliedAfterDelete["ingestionGatewayDeployment"].(map[string]any)
	enabled, isBool := depMap["enabled"].(bool)
	if !ok || !isBool || enabled {
		t.Errorf("expected enabled=false after Delete, got %+v", appliedAfterDelete)
	}
	if err := mgr.Purge1PComponent(ctx, ref); err != nil {
		t.Fatalf("Purge1PComponent failed: %v", err)
	}
	if mgr.GetAppliedValues(ref) != nil {
		t.Fatal("expected applied values to be removed after Purge")
	}

	// Test empty name validation
	emptyRef := ComponentRef{Namespace: "xmc", Name: ""}
	if err := mgr.Ensure1PComponent(ctx, emptyRef, opts); err == nil {
		t.Error("expected error on empty component name for Ensure1PComponent")
	}
	if err := mgr.Delete1PComponent(ctx, emptyRef); err == nil {
		t.Error("expected error on empty component name for Delete1PComponent")
	}
	if err := mgr.Purge1PComponent(ctx, emptyRef); err == nil {
		t.Error("expected error on empty component name for Purge1PComponent")
	}
	if err := mgr.Orphan1PComponent(ctx, emptyRef); err == nil {
		t.Error("expected error on empty component name for Orphan1PComponent")
	}
	if mgr.IsComponentActive(emptyRef) {
		t.Error("expected false for empty component name IsComponentActive")
	}
	if mgr.GetAppliedValues(emptyRef) != nil {
		t.Error("expected nil for empty component name GetAppliedValues")
	}
}

func TestFakeHelmManager_GetAppliedValues_CloneIsolation(t *testing.T) {
	ctx := context.Background()
	mgr := NewFakeHelmManager()
	ref := ComponentRef{Namespace: "xmc", Name: "test-comp"}
	opts := HelmComponentOptions{
		Ref:     ref,
		Enabled: true,
		RawValues: map[string]any{
			"config": map[string]any{
				"timeout": 30,
			},
		},
	}
	if err := mgr.Ensure1PComponent(ctx, ref, opts); err != nil {
		t.Fatalf("Ensure1PComponent failed: %v", err)
	}

	values := mgr.GetAppliedValues(ref)
	if values == nil {
		t.Fatal("expected non-nil applied values")
	}

	depKey := ComponentNameToHelmKey(ref.Name)
	depMap, ok := values[depKey].(map[string]any)
	if !ok {
		t.Fatalf("expected values[%q] to be a map[string]any", depKey)
	}

	// Mutate the returned copy
	if configMap, ok := depMap["config"].(map[string]any); ok {
		configMap["timeout"] = 999
	}
	values["newKey"] = "newValue"

	// Verify internal state in FakeHelmManager was not mutated
	freshValues := mgr.GetAppliedValues(ref)
	if freshValues["newKey"] != nil {
		t.Error("expected fresh values not to contain newKey added to external copy")
	}
	freshDepMap, ok := freshValues[depKey].(map[string]any)
	if !ok {
		t.Fatalf("expected fresh values[%q] to be map[string]any", depKey)
	}
	if freshConfig, ok := freshDepMap["config"].(map[string]any); !ok || freshConfig["timeout"] != 30 {
		t.Errorf("expected internal timeout to remain 30, got %v", freshDepMap["config"])
	}
}
