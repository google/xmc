// Copyright 2025 Google LLC
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
package k8sclient

import (
	"testing"
)

func TestGetJobsetUIDByTarget(t *testing.T) {
	ctx, client, _ := setupTestClient( /*createNewNamespaces=*/ true, &alwaysPassingPreconditionsValidator{})
	target := "experiments/123/workUnits/456/executions/1"
	createTestJobSet(ctx, t, client, testNamespace, target, nil)

	res, err := client.getJobsetUIDByTarget(ctx, target, testNamespace)
	if err != nil {
		t.Fatalf("getJobsetUIDByTarget(%q, %q) returned an unexpected error: %v", target, testNamespace, err)
	}
	if res.UID != "test-uid" {
		t.Errorf("getJobsetUIDByTarget(%q, %q) returned UID %q, want %q", target, testNamespace, res.UID, "test-uid")
	}
}

func TestGetJobsetUIDByTarget_NotFound(t *testing.T) {
	ctx, client, _ := setupTestClient( /*createNewNamespaces=*/ true, &alwaysPassingPreconditionsValidator{})
	target := "experiments/123/workUnits/456/executions/1"

	res, err := client.getJobsetUIDByTarget(ctx, target, testNamespace)
	if err != nil {
		t.Fatalf("getJobsetUIDByTarget(%q, %q) returned an unexpected error: %v", target, testNamespace, err)
	}
	if res.UID != "" {
		t.Errorf("getJobsetUIDByTarget(%q, %q) returned UID %q, want empty", target, testNamespace, res.UID)
	}
}

func TestGetNamespaceFromTarget(t *testing.T) {
	ctx, client, _ := setupTestClient( /*createNewNamespaces=*/ true, &alwaysPassingPreconditionsValidator{})
	target := "experiments/123/workUnits/456/executions/1"
	createTestJobSet(ctx, t, client, testNamespace, target, nil)

	namespace, err := client.getNamespaceFromTarget(ctx, target)
	if err != nil {
		t.Fatalf("getNamespaceFromTarget(%q) returned an unexpected error: %v", target, err)
	}
	if namespace != testNamespace {
		t.Errorf("getNamespaceFromTarget(%q) = %q, want %q", target, namespace, testNamespace)
	}
}

func TestGetJobSetByTargetAndNamespace(t *testing.T) {
	ctx, client, _ := setupTestClient( /*createNewNamespaces=*/ true, &alwaysPassingPreconditionsValidator{})
	target := "experiments/123/workUnits/456/executions/1"
	createTestJobSet(ctx, t, client, testNamespace, target, nil)

	jobSet, err := client.getJobSetByTargetAndNamespace(ctx, target, testNamespace)
	if err != nil {
		t.Fatalf("getJobSetByTargetAndNamespace(%q, %q) returned an unexpected error: %v", target, testNamespace, err)
	}
	if jobSet.GetName() != "xmc-operation-123-456" {
		t.Errorf("getJobSetByTargetAndNamespace(%q, %q) returned JobSet with name %q, want %q", target, testNamespace, jobSet.GetName(), "xmc-operation-123-456")
	}
}

func TestGetNamespaceFromWorkUnit(t *testing.T) {
	ctx, client, _ := setupTestClient( /*createNewNamespaces=*/ true, &alwaysPassingPreconditionsValidator{})
	target := "experiments/123/workUnits/456/executions/1"
	workUnit := createTestWorkUnit(testNamespace)

	namespace, err := client.getNamespaceFromWorkUnitWithFallback(ctx, workUnit, target)
	if err != nil {
		t.Fatalf("getNamespaceFromWorkUnitWithFallback(%v, %q) returned an unexpected error: %v", workUnit, target, err)
	}
	if namespace != testNamespace {
		t.Errorf("getNamespaceFromWorkUnitWithFallback(%v, %q) = %q, want %q", workUnit, target, namespace, testNamespace)
	}
}

func TestGetNamespaceFromWorkUnit_Fallback(t *testing.T) {
	ctx, client, _ := setupTestClient( /*createNewNamespaces=*/ true, &alwaysPassingPreconditionsValidator{})
	target := "experiments/123/workUnits/456/executions/1"
	workUnitNoNamespace := createTestWorkUnit("")
	workUnitNoNamespace.Executable.SchedulingConstraints = nil
	createTestJobSet(ctx, t, client, testNamespace, target, nil)

	namespace, err := client.getNamespaceFromWorkUnitWithFallback(ctx, workUnitNoNamespace, target)
	if err != nil {
		t.Fatalf("getNamespaceFromWorkUnitWithFallback(%v, %q) returned an unexpected error: %v", workUnitNoNamespace, target, err)
	}
	if namespace != testNamespace {
		t.Errorf("getNamespaceFromWorkUnitWithFallback(%v, %q) = %q, want %q", workUnitNoNamespace, target, namespace, testNamespace)
	}
}
