// Copyright 2025 Google LLC
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

package k8sclient

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"

	"google.golang.org/protobuf/proto"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	runtimeclient "sigs.k8s.io/controller-runtime/pkg/client"
	runtimefake "sigs.k8s.io/controller-runtime/pkg/client/fake"
	jobsetv1alpha2 "sigs.k8s.io/jobset/api/jobset/v1alpha2"
	esspb "xmanager_cloud/api/protos/experiment"
	actuatortaskpb "xmanager_cloud/api/protos/task_queue"
	constants "xmanager_cloud/internal/kubernetes/common/constants"
)

// createTestComponentCR returns a Component custom resource whose bridge-written status reports
// CMS component generation gen; gen 0 leaves status.componentGeneration unset (not synced).
func createTestComponentCR(name, namespace string, gen int64, enabled bool, state string, conditions []map[string]any, access map[string]any) *unstructured.Unstructured {
	status := map[string]any{"observedGeneration": int64(1), "state": state}
	if gen != 0 {
		status["componentGeneration"] = gen
	}
	if conditions != nil {
		condSlice := make([]any, len(conditions))
		for i, c := range conditions {
			condSlice[i] = c
		}
		status["conditions"] = condSlice
	}
	if access != nil {
		status["access"] = access
	}
	return &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": fmt.Sprintf("%s/%s", constants.ComponentCRDGroup, constants.ComponentCRDVersion),
			"kind":       constants.ComponentCRDKind,
			"metadata":   map[string]any{"name": name, "namespace": namespace},
			"spec":       map[string]any{"enabled": enabled},
			"status":     status,
		},
	}
}

func createFakeManagerClient(objs ...*unstructured.Unstructured) runtimeclient.Reader {
	s := runtime.NewScheme()
	_ = corev1.AddToScheme(s)
	gvk := schema.GroupVersionKind{
		Group:   constants.ComponentCRDGroup,
		Version: constants.ComponentCRDVersion,
		Kind:    constants.ComponentCRDKind,
	}
	s.AddKnownTypeWithName(gvk, &unstructured.Unstructured{})
	builder := runtimefake.NewClientBuilder().WithScheme(s)
	for _, obj := range objs {
		builder = builder.WithObjects(obj)
	}
	return builder.Build()
}

func createWorkUnitWithSnapshots(namespace string, snapshotsJSON string) *esspb.WorkUnit {
	wu := proto.Clone(baseCPUWorkUnit).(*esspb.WorkUnit)
	if wu.GetExecutable().GetSchedulingConstraints() == nil {
		wu.GetExecutable().SchedulingConstraints = &esspb.SchedulingConstraints{}
	}
	wu.GetExecutable().SchedulingConstraints.QueueName = namespace

	job := wu.GetExecutable().GetJobset().GetJobs()[0]
	job.InitContainers = []*esspb.ExecutableSpec{
		{
			DockerImage: "gcr.io/test/init:latest",
			Command:     []string{"echo", "init"},
		},
	}
	if snapshotsJSON != "" {
		if wu.GetExecutable().GetJobset().Annotations == nil {
			wu.GetExecutable().GetJobset().Annotations = make(map[string]string)
		}
		wu.GetExecutable().GetJobset().Annotations[constants.ComponentSnapshotsAnnotation] = snapshotsJSON
	}
	return wu
}

func setupComponentTestClient() (context.Context, *Client) {
	ctx, client, _ := setupTestClient(true, &alwaysPassingPreconditionsValidator{})
	return ctx, client
}

func TestConstructJobSet_ComponentSnapshot_Success(t *testing.T) {
	ctx, client := setupComponentTestClient()
	targetNS := constants.XmcLabelValue

	compCR := createTestComponentCR("metrics-collector", targetNS, 1, true, "COMPONENT_STATE_ENABLED",
		[]map[string]any{
			{"type": "Ready", "status": "True", "reason": "ComponentHealthy", "message": "ready"},
		},
		nil,
	)
	client.SetManagerClient(createFakeManagerClient(compCR))

	workUnit := createWorkUnitWithSnapshots(targetNS, `{"metrics-collector": 1}`)
	jobSet, err := client.constructJobSet(ctx, workUnit, testTarget, "test-author", targetNS, testTime, Priorities{})
	if err != nil {
		t.Fatalf("constructJobSet failed: %v", err)
	}
	if jobSet == nil {
		t.Fatalf("expected jobSet created")
	}

	if _, exists := jobSet.Annotations[constants.ComponentDriftWarningAnnotation]; exists {
		t.Errorf("unexpected drift warning annotation on matching generation")
	}
}

func TestConstructJobSet_ComponentSnapshot_DisabledComponent(t *testing.T) {
	tests := []struct {
		name    string
		enabled bool
		state   string
	}{
		{name: "spec_disabled", enabled: false, state: "COMPONENT_STATE_ENABLED"},
		{name: "state_disabled", enabled: true, state: "COMPONENT_STATE_DISABLED"},
		{name: "state_disabling", enabled: true, state: "COMPONENT_STATE_DISABLING"},
		{name: "state_deleted", enabled: true, state: "COMPONENT_STATE_DELETED"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, client := setupComponentTestClient()
			targetNS := constants.XmcLabelValue

			compCR := createTestComponentCR("disabled-comp", targetNS, 1, tt.enabled, tt.state,
				[]map[string]any{
					{"type": "Ready", "status": "True", "reason": "OK", "message": "ready"},
				},
				nil,
			)
			client.SetManagerClient(createFakeManagerClient(compCR))

			workUnit := createWorkUnitWithSnapshots(targetNS, `{"disabled-comp": 1}`)
			_, err := client.constructJobSet(ctx, workUnit, testTarget, "test-author", targetNS, testTime, Priorities{})
			if err == nil || !apierrors.IsServiceUnavailable(err) || !strings.Contains(err.Error(), `required component "disabled-comp" is disabled`) {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

func TestConstructJobSet_ComponentSnapshot_UnhealthyComponent(t *testing.T) {
	tests := []struct {
		name       string
		conditions []map[string]any
		wantSub    string
	}{
		{
			name: "ready_false",
			conditions: []map[string]any{
				{"type": "Ready", "status": "False", "reason": "CrashLoopBackOff", "message": "pod crashed"},
			},
			wantSub: "CrashLoopBackOff",
		},
		{
			name: "ready_unknown",
			conditions: []map[string]any{
				{"type": "Ready", "status": "Unknown", "reason": "NodeUnreachable", "message": "node down"},
			},
			wantSub: "NodeUnreachable",
		},
		{
			name:       "no_conditions",
			conditions: nil,
			wantSub:    "component conditions are uninitialized",
		},
		{
			name:       "empty_conditions",
			conditions: []map[string]any{},
			wantSub:    "component conditions are uninitialized",
		},
		{
			name: "missing_ready_condition",
			conditions: []map[string]any{
				{"type": "Initialized", "status": "True", "reason": "OK", "message": "initialized"},
			},
			wantSub: "Ready condition is missing",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, client := setupComponentTestClient()
			targetNS := constants.XmcLabelValue

			compCR := createTestComponentCR("unhealthy-comp", targetNS, 1, true, "COMPONENT_STATE_ENABLED", tt.conditions, nil)
			client.SetManagerClient(createFakeManagerClient(compCR))

			workUnit := createWorkUnitWithSnapshots(targetNS, `{"unhealthy-comp": 1}`)
			_, err := client.constructJobSet(ctx, workUnit, testTarget, "test-author", targetNS, testTime, Priorities{})
			if err == nil || !apierrors.IsServiceUnavailable(err) || !strings.Contains(err.Error(), tt.wantSub) {
				t.Fatalf("unexpected error for %s: %v, want substring %q", tt.name, err, tt.wantSub)
			}
		})
	}
}

func TestConstructJobSet_ComponentSnapshot_DriftWarning(t *testing.T) {
	ctx, client := setupComponentTestClient()
	targetNS := constants.XmcLabelValue

	compCR := createTestComponentCR("drifted-comp", targetNS, 2, true, "COMPONENT_STATE_ENABLED",
		[]map[string]any{
			{"type": "Ready", "status": "True", "reason": "ComponentHealthy", "message": "ready"},
		},
		nil,
	)
	client.SetManagerClient(createFakeManagerClient(compCR))

	workUnit := createWorkUnitWithSnapshots(targetNS, `{"drifted-comp": 1}`)
	jobSet, err := client.constructJobSet(ctx, workUnit, testTarget, "test-author", targetNS, testTime, Priorities{})
	if err != nil {
		t.Fatalf("constructJobSet failed: %v", err)
	}

	warning, exists := jobSet.Annotations[constants.ComponentDriftWarningAnnotation]
	if !exists {
		t.Fatalf("expected drift warning annotation %q on JobSet, got nil", constants.ComponentDriftWarningAnnotation)
	}
	expectedDetail := `component "drifted-comp" generation mismatch: component generation 2, locked 1`
	if !strings.Contains(warning, expectedDetail) {
		t.Errorf("warning %q does not contain %q", warning, expectedDetail)
	}
}

func TestConstructJobSet_ComponentSnapshot_MultipleDriftWarnings(t *testing.T) {
	ctx, client := setupComponentTestClient()
	targetNS := constants.XmcLabelValue

	comp1 := createTestComponentCR("comp-a", targetNS, 3, true, "COMPONENT_STATE_ENABLED",
		[]map[string]any{{"type": "Ready", "status": "True"}}, nil)
	comp2 := createTestComponentCR("comp-b", targetNS, 5, true, "COMPONENT_STATE_ENABLED",
		[]map[string]any{{"type": "Ready", "status": "True"}}, nil)
	client.SetManagerClient(createFakeManagerClient(comp1, comp2))

	workUnit := createWorkUnitWithSnapshots(targetNS, `{"comp-a": 1, "comp-b": 2}`)
	jobSet, err := client.constructJobSet(ctx, workUnit, testTarget, "test-author", targetNS, testTime, Priorities{})
	if err != nil {
		t.Fatalf("constructJobSet failed: %v", err)
	}

	warning := jobSet.Annotations[constants.ComponentDriftWarningAnnotation]
	if !strings.Contains(warning, `component "comp-a" generation mismatch: component generation 3, locked 1`) ||
		!strings.Contains(warning, `component "comp-b" generation mismatch: component generation 5, locked 2`) {
		t.Errorf("unexpected multiple drift warning: %q", warning)
	}
}

func TestConstructJobSet_ComponentSnapshot_ZeroOrNegativeLockedGen(t *testing.T) {
	ctx, client := setupComponentTestClient()
	targetNS := constants.XmcLabelValue

	comp := createTestComponentCR("unlocked-comp", targetNS, 5, true, "COMPONENT_STATE_ENABLED",
		[]map[string]any{{"type": "Ready", "status": "True"}}, nil)
	client.SetManagerClient(createFakeManagerClient(comp))

	workUnit := createWorkUnitWithSnapshots(targetNS, `{"unlocked-comp": 0}`)
	jobSet, err := client.constructJobSet(ctx, workUnit, testTarget, "test-author", targetNS, testTime, Priorities{})
	if err != nil {
		t.Fatalf("constructJobSet failed: %v", err)
	}

	if _, exists := jobSet.Annotations[constants.ComponentDriftWarningAnnotation]; exists {
		t.Errorf("expected no drift warning when lockedGen <= 0")
	}
}

func TestConstructJobSet_ComponentSnapshot_NotSyncedBlocksPlacement(t *testing.T) {
	for _, lockedGen := range []string{"0", "1"} {
		t.Run("locked_"+lockedGen, func(t *testing.T) {
			ctx, client := setupComponentTestClient()
			targetNS := constants.XmcLabelValue

			// Ready=True with observedGeneration but no componentGeneration: never synced with CMS.
			comp := createTestComponentCR("unsynced-comp", targetNS, 0, true, "COMPONENT_STATE_ENABLED",
				[]map[string]any{{"type": "Ready", "status": "True"}}, nil)
			client.SetManagerClient(createFakeManagerClient(comp))

			workUnit := createWorkUnitWithSnapshots(targetNS, `{"unsynced-comp": `+lockedGen+`}`)
			_, err := client.constructJobSet(ctx, workUnit, testTarget, "test-author", targetNS, testTime, Priorities{})
			if err == nil || !apierrors.IsServiceUnavailable(err) || !strings.Contains(err.Error(), `required component "unsynced-comp" is not synced with CMS`) {
				t.Fatalf("constructJobSet() error = %v, want ServiceUnavailable for an unsynced component", err)
			}
		})
	}
}

func TestConstructJobSet_ComponentSnapshot_MalformedAnnotationJSON(t *testing.T) {
	ctx, client := setupComponentTestClient()
	targetNS := constants.XmcLabelValue

	workUnit := createWorkUnitWithSnapshots(targetNS, `not-valid-json`)
	_, err := client.constructJobSet(ctx, workUnit, testTarget, "test-author", targetNS, testTime, Priorities{})
	if err == nil || !strings.Contains(err.Error(), "failed to parse xmc.dev/component-snapshots annotation") {
		t.Fatalf("expected malformed annotation error, got: %v", err)
	}
}

func TestParseComponentSnapshotsAnnotation_Fallback(t *testing.T) {
	workUnit := createWorkUnitWithSnapshots(constants.XmcLabelValue, "")
	jobSet := &jobsetv1alpha2.JobSet{
		ObjectMeta: metav1.ObjectMeta{
			Annotations: map[string]string{
				constants.ComponentSnapshotsAnnotation: `{"fallback-comp": 1}`,
			},
		},
	}
	snapshots, err := parseComponentSnapshotsAnnotation(workUnit, jobSet)
	if err != nil {
		t.Fatalf("parseComponentSnapshotsAnnotation failed: %v", err)
	}
	if snapshots["fallback-comp"] != 1 {
		t.Errorf("expected fallback-comp locked generation 1, got %v", snapshots["fallback-comp"])
	}
}

func TestConstructJobSet_ComponentSnapshot_ContextCanceled(t *testing.T) {
	ctx, client := setupComponentTestClient()
	targetNS := constants.XmcLabelValue

	canceledCtx, cancel := context.WithCancel(ctx)
	cancel()

	workUnit := createWorkUnitWithSnapshots(targetNS, `{"metrics-collector": 1}`)
	_, err := client.constructJobSet(canceledCtx, workUnit, testTarget, "test-author", targetNS, testTime, Priorities{})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got: %v", err)
	}
}

func TestExtractStatusInt(t *testing.T) {
	withObservedGeneration := func(val any) *unstructured.Unstructured {
		return &unstructured.Unstructured{Object: map[string]any{"status": map[string]any{"observedGeneration": val}}}
	}
	tests := []struct {
		name string
		comp *unstructured.Unstructured
		want int64
	}{
		{name: "int64", comp: withObservedGeneration(int64(42)), want: 42},
		{name: "int", comp: withObservedGeneration(42), want: 42},
		{name: "float64", comp: withObservedGeneration(float64(42)), want: 42},
		{name: "json_number", comp: withObservedGeneration(json.Number("42")), want: 42},
		{name: "string", comp: withObservedGeneration("42"), want: 42},
		{name: "invalid_string", comp: withObservedGeneration("abc"), want: 0},
		{name: "nil", comp: withObservedGeneration(nil), want: 0},
		{name: "nil_component", comp: nil, want: 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := extractStatusInt(tt.comp, "observedGeneration"); got != tt.want {
				t.Errorf("extractStatusInt() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestExtractComponentGeneration(t *testing.T) {
	tests := []struct {
		name   string
		status map[string]any
		want   int64
	}{
		{name: "component generation", status: map[string]any{"observedGeneration": int64(7), "componentGeneration": int64(2)}, want: 2},
		{name: "observed generation is not a fallback", status: map[string]any{"observedGeneration": int64(7)}, want: 0},
		{name: "no generations", status: map[string]any{}, want: 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			comp := &unstructured.Unstructured{Object: map[string]any{"status": tt.status}}
			if got := extractComponentGeneration(comp); got != tt.want {
				t.Errorf("extractComponentGeneration() = %d, want %d", got, tt.want)
			}
		})
	}
}

func TestConstructJobSet_ComponentSnapshot_ClusterWideFallback(t *testing.T) {
	ctx, client := setupComponentTestClient()
	targetNS := "workunit-specific-ns"

	compCR := createTestComponentCR("cluster-comp", constants.DefaultComponentsNamespace, 1, true, "COMPONENT_STATE_ENABLED",
		[]map[string]any{
			{"type": "Ready", "status": "True", "reason": "ComponentHealthy", "message": "ready"},
		},
		nil,
	)
	client.SetManagerClient(createFakeManagerClient(compCR))

	workUnit := createWorkUnitWithSnapshots(targetNS, `{"cluster-comp": 1}`)
	jobSet, err := client.constructJobSet(ctx, workUnit, testTarget, "test-author", targetNS, testTime, Priorities{})
	if err != nil {
		t.Fatalf("constructJobSet failed: %v", err)
	}
	if jobSet == nil {
		t.Fatalf("expected jobSet created")
	}
}

func TestConstructJobSet_ComponentSnapshot_DynamicClientFallback(t *testing.T) {
	ctx, client, _ := setupTestClient(true, &alwaysPassingPreconditionsValidator{})
	targetNS := constants.XmcLabelValue

	compCR := createTestComponentCR("dynamic-comp", targetNS, 1, true, "COMPONENT_STATE_ENABLED",
		[]map[string]any{
			{"type": "Ready", "status": "True", "reason": "ComponentHealthy", "message": "ready"},
		},
		nil,
	)

	client.ManagerClient = nil

	gvr := schema.GroupVersionResource{
		Group:    constants.ComponentCRDGroup,
		Version:  constants.ComponentCRDVersion,
		Resource: constants.ComponentCRDResource,
	}
	_, err := client.dynamicClient.Resource(gvr).Namespace(targetNS).Create(ctx, compCR, metav1.CreateOptions{})
	if err != nil {
		t.Fatalf("failed to create Component in dynamicClient: %v", err)
	}

	workUnit := createWorkUnitWithSnapshots(targetNS, `{"dynamic-comp": 1}`)
	jobSet, err := client.constructJobSet(ctx, workUnit, testTarget, "test-author", targetNS, testTime, Priorities{})
	if err != nil {
		t.Fatalf("constructJobSet failed with dynamic client fallback: %v", err)
	}
	if jobSet == nil {
		t.Fatalf("expected jobSet created")
	}
}

func TestConstructJobSet_ComponentSnapshot_NormalWorkUnit(t *testing.T) {
	ctx, client, _ := setupTestClient(true, &alwaysPassingPreconditionsValidator{})
	targetNS := constants.XmcLabelValue

	workUnit := createWorkUnitWithSnapshots(targetNS, "")
	jobSet, err := client.constructJobSet(ctx, workUnit, testTarget, "test-author", targetNS, testTime, Priorities{})
	if err != nil {
		t.Fatalf("constructJobSet failed for normal workunit: %v", err)
	}

	if warning, exists := jobSet.Annotations[constants.ComponentDriftWarningAnnotation]; exists {
		t.Errorf("unexpected drift warning annotation on normal workunit: %q", warning)
	}
}

func TestCreateWorkUnit_ComponentSnapshotValidation_Success(t *testing.T) {
	ctx, actuatorClient, dynamicClient := setupTestClient(true /* createNewNamespaces */, &alwaysPassingPreconditionsValidator{})

	compCR := createTestComponentCR("audit-sidecar", testNamespace, 1, true, "COMPONENT_STATE_ENABLED",
		[]map[string]any{
			{"type": "Ready", "status": "True", "reason": "ComponentHealthy", "message": "ready"},
		},
		map[string]any{},
	)
	actuatorClient.SetManagerClient(createFakeManagerClient(compCR))

	payload := proto.Clone(createPayload).(*actuatortaskpb.ActuatorPayload)
	payload.WorkUnit = createWorkUnitWithSnapshots(testNamespace, `{"audit-sidecar": 1}`)

	if err := actuatorClient.CreateWorkUnit(ctx, payload); err != nil {
		t.Fatalf("CreateWorkUnit failed: %v", err)
	}

	jobSetUnstructured, err := dynamicClient.Resource(JobSetGVR).Namespace(testNamespace).Get(ctx, testJobSetName, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("dynamicClient.Resource().Get() failed: %v", err)
	}
	if jobSetUnstructured == nil {
		t.Fatalf("expected JobSet to be created")
	}
}

func TestCreateWorkUnit_ComponentSnapshotInjection_MissingComponent_Retryable(t *testing.T) {
	ctx, actuatorClient, _ := setupTestClient(true /* createNewNamespaces */, &alwaysPassingPreconditionsValidator{})
	actuatorClient.SetManagerClient(createFakeManagerClient())

	payload := proto.Clone(createPayload).(*actuatortaskpb.ActuatorPayload)
	payload.WorkUnit = createWorkUnitWithSnapshots(testNamespace, `{"missing-audit": 1}`)

	err := actuatorClient.CreateWorkUnit(ctx, payload)
	if err == nil {
		t.Fatalf("CreateWorkUnit succeeded, want error")
	}

	if !apierrors.IsServiceUnavailable(err) {
		t.Errorf("expected apierrors.IsServiceUnavailable(err) to be true, got: %v", err)
	}
	expectedSub := `required component "missing-audit" not found: hydration lag or missing CR`
	if !strings.Contains(err.Error(), expectedSub) {
		t.Errorf("error %q does not contain %q", err.Error(), expectedSub)
	}
}

func TestCreateWorkUnit_ComponentSnapshotInjection_EnvVars_Success(t *testing.T) {
	ctx, actuatorClient, dynamicClient := setupTestClient(true /* createNewNamespaces */, &alwaysPassingPreconditionsValidator{})

	compCR := createTestComponentCR("audit-sidecar", testNamespace, 1, true, "COMPONENT_STATE_ENABLED",
		[]map[string]any{
			{"type": "Ready", "status": "True", "reason": "ComponentHealthy", "message": "ready"},
		},
		map[string]any{
			"envVars": map[string]any{
				"AUDIT_MODE": "verbose",
			},
			"secretEnvVars": map[string]any{
				"AUDIT_TOKEN": "audit-secret:token-key",
			},
		},
	)
	actuatorClient.SetManagerClient(createFakeManagerClient(compCR))

	payload := proto.Clone(createPayload).(*actuatortaskpb.ActuatorPayload)
	payload.WorkUnit = createWorkUnitWithSnapshots(testNamespace, `{"audit-sidecar": 1}`)

	if err := actuatorClient.CreateWorkUnit(ctx, payload); err != nil {
		t.Fatalf("CreateWorkUnit failed: %v", err)
	}

	jobSetUnstructured, err := dynamicClient.Resource(JobSetGVR).Namespace(testNamespace).Get(ctx, testJobSetName, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("dynamicClient.Resource().Get() failed: %v", err)
	}
	if jobSetUnstructured == nil {
		t.Fatalf("expected JobSet to be created")
	}

	replicatedJobs, ok, err := unstructured.NestedSlice(jobSetUnstructured.Object, "spec", "replicatedJobs")
	if err != nil || !ok || len(replicatedJobs) == 0 {
		t.Fatalf("failed to retrieve replicatedJobs: %v", err)
	}
	firstRj := replicatedJobs[0].(map[string]any)
	containers, ok, err := unstructured.NestedSlice(firstRj, "template", "spec", "template", "spec", "containers")
	if err != nil || !ok || len(containers) == 0 {
		t.Fatalf("failed to retrieve containers: %v", err)
	}
	firstContainer := containers[0].(map[string]any)
	envList, ok, err := unstructured.NestedSlice(firstContainer, "env")
	if err != nil || !ok {
		t.Fatalf("failed to retrieve env: %v", err)
	}

	foundPlain := false
	foundSecret := false
	for _, raw := range envList {
		ev := raw.(map[string]any)
		if ev["name"] == "AUDIT_MODE" && ev["value"] == "verbose" {
			foundPlain = true
		}
		if ev["name"] == "AUDIT_TOKEN" {
			valFrom, ok := ev["valueFrom"].(map[string]any)
			if ok {
				secRef, ok := valFrom["secretKeyRef"].(map[string]any)
				if ok && secRef["name"] == "audit-secret" && secRef["key"] == "token-key" {
					foundSecret = true
				}
			}
		}
	}
	if !foundPlain {
		t.Errorf("expected AUDIT_MODE=verbose in created JobSet container")
	}
	if !foundSecret {
		t.Errorf("expected AUDIT_TOKEN secretKeyRef in created JobSet container")
	}
}

func TestConstructJobSet_ComponentSnapshot_EnvVars_Injection(t *testing.T) {
	ctx, client := setupComponentTestClient()
	targetNS := constants.XmcLabelValue

	compCR := createTestComponentCR("env-comp", targetNS, 1, true, "COMPONENT_STATE_ENABLED",
		[]map[string]any{
			{"type": "Ready", "status": "True", "reason": "ComponentHealthy", "message": "ready"},
		},
		map[string]any{
			"envVars": map[string]any{
				"PLAIN_VAR":   "plain_val",
				"EXISTING_EV": "overridden_val",
			},
			"secretEnvVars": map[string]any{
				"SECRET_VAR": "my-secret:secret-token",
			},
		},
	)
	client.SetManagerClient(createFakeManagerClient(compCR))

	workUnit := createWorkUnitWithSnapshots(targetNS, `{"env-comp": 1}`)
	jobSet, err := client.constructJobSet(ctx, workUnit, testTarget, "test-author", targetNS, testTime, Priorities{})
	if err != nil {
		t.Fatalf("constructJobSet failed: %v", err)
	}

	if len(jobSet.Spec.ReplicatedJobs) == 0 {
		t.Fatalf("expected at least 1 replicatedJob")
	}

	containers := jobSet.Spec.ReplicatedJobs[0].Template.Spec.Template.Spec.Containers
	if len(containers) == 0 {
		t.Fatalf("expected at least 1 container")
	}

	envMap := make(map[string]corev1.EnvVar)
	for _, ev := range containers[0].Env {
		envMap[ev.Name] = ev
	}

	if ev, ok := envMap["PLAIN_VAR"]; !ok || ev.Value != "plain_val" {
		t.Errorf("expected PLAIN_VAR=plain_val, got: %v", ev)
	}

	if ev, ok := envMap["SECRET_VAR"]; !ok {
		t.Errorf("expected SECRET_VAR to be present")
	} else if ev.ValueFrom == nil || ev.ValueFrom.SecretKeyRef == nil {
		t.Errorf("expected SECRET_VAR to have SecretKeyRef, got: %v", ev)
	} else {
		if ev.ValueFrom.SecretKeyRef.Name != "my-secret" || ev.ValueFrom.SecretKeyRef.Key != "secret-token" {
			t.Errorf("expected SecretKeyRef my-secret:secret-token, got %s:%s",
				ev.ValueFrom.SecretKeyRef.Name, ev.ValueFrom.SecretKeyRef.Key)
		}
	}
}

func TestConstructJobSet_ComponentSnapshot_EnvVars_UpdateExisting(t *testing.T) {
	c := &corev1.Container{
		Name: "worker",
		Env: []corev1.EnvVar{
			{Name: "VAR_A", Value: "initial_a"},
			{Name: "VAR_B", Value: "initial_b"},
		},
	}
	appendOrUpdateEnvVar(c, corev1.EnvVar{Name: "VAR_A", Value: "updated_a"})
	appendOrUpdateEnvVar(c, corev1.EnvVar{Name: "VAR_C", Value: "new_c"})

	if len(c.Env) != 3 {
		t.Fatalf("expected 3 env vars, got %d", len(c.Env))
	}
	if c.Env[0].Value != "updated_a" {
		t.Errorf("expected VAR_A to be updated_a, got %s", c.Env[0].Value)
	}
	if c.Env[2].Value != "new_c" {
		t.Errorf("expected VAR_C to be new_c, got %s", c.Env[2].Value)
	}
}

func TestConstructJobSet_ComponentSnapshot_AccessExtraction_Error(t *testing.T) {
	ctx, client := setupComponentTestClient()
	targetNS := constants.XmcLabelValue

	compCR := &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": fmt.Sprintf("%s/%s", constants.ComponentCRDGroup, constants.ComponentCRDVersion),
			"kind":       constants.ComponentCRDKind,
			"metadata": map[string]any{
				"name":      "invalid-access-comp",
				"namespace": targetNS,
			},
			"spec": map[string]any{
				"enabled": true,
			},
			"status": map[string]any{
				"observedGeneration":  int64(1),
				"componentGeneration": int64(1),
				"state":               "COMPONENT_STATE_ENABLED",
				"conditions": []any{
					map[string]any{"type": "Ready", "status": "True"},
				},
				"access": "not-a-map",
			},
		},
	}
	client.SetManagerClient(createFakeManagerClient(compCR))

	workUnit := createWorkUnitWithSnapshots(targetNS, `{"invalid-access-comp": 1}`)
	_, err := client.constructJobSet(ctx, workUnit, testTarget, "test-author", targetNS, testTime, Priorities{})
	if err == nil || !strings.Contains(err.Error(), "failed to extract access configuration") {
		t.Fatalf("expected access extraction error, got: %v", err)
	}
}

func TestConstructJobSet_ComponentSnapshot_EmptyAccess(t *testing.T) {
	ctx, client := setupComponentTestClient()
	targetNS := constants.XmcLabelValue

	compCR := createTestComponentCR("empty-access-comp", targetNS, 1, true, "COMPONENT_STATE_ENABLED",
		[]map[string]any{
			{"type": "Ready", "status": "True", "reason": "ComponentHealthy", "message": "ready"},
		},
		nil,
	)
	client.SetManagerClient(createFakeManagerClient(compCR))

	workUnit := createWorkUnitWithSnapshots(targetNS, `{"empty-access-comp": 1}`)
	jobSet, err := client.constructJobSet(ctx, workUnit, testTarget, "test-author", targetNS, testTime, Priorities{})
	if err != nil {
		t.Fatalf("constructJobSet failed with nil access: %v", err)
	}
	if jobSet == nil {
		t.Fatalf("expected jobSet created")
	}
}

func TestExtractStringMap_Types(t *testing.T) {
	raw := map[string]any{
		"str":    "hello",
		"number": json.Number("42"),
		"int":    123,
		"nilVal": nil,
	}
	m := extractStringMap(raw)
	if m["str"] != "hello" {
		t.Errorf("expected str=hello, got %q", m["str"])
	}
	if m["number"] != "42" {
		t.Errorf("expected number=42, got %q", m["number"])
	}
	if m["int"] != "123" {
		t.Errorf("expected int=123, got %q", m["int"])
	}
	if _, exists := m["nilVal"]; exists {
		t.Errorf("expected nilVal to be omitted")
	}

	if empty := extractStringMap(nil); empty != nil {
		t.Errorf("expected nil for nil input")
	}
	if empty := extractStringMap("not-a-map"); empty != nil {
		t.Errorf("expected nil for non-map input")
	}
}

func TestBuildSecretEnvVars_EdgeCases(t *testing.T) {
	cases := map[string]string{
		"VALID":      "sec:key",
		"NO_COLON":   "no-colon-val",
		"EMPTY_NAME": ":key-only",
		"EMPTY_KEY":  "sec-only:",
	}
	envVars := buildSecretEnvVars(cases)
	if len(envVars) != 1 {
		t.Fatalf("expected exactly 1 valid secret env var, got %d", len(envVars))
	}
	if envVars[0].Name != "VALID" {
		t.Errorf("expected VALID secret env var, got %s", envVars[0].Name)
	}
	if envVars[0].ValueFrom.SecretKeyRef.Name != "sec" || envVars[0].ValueFrom.SecretKeyRef.Key != "key" {
		t.Errorf("expected sec:key, got %s:%s", envVars[0].ValueFrom.SecretKeyRef.Name, envVars[0].ValueFrom.SecretKeyRef.Key)
	}

	if empty := buildSecretEnvVars(nil); empty != nil {
		t.Errorf("expected nil for empty secretEnv")
	}
}

func TestCreateWorkUnit_ComponentSnapshotInjection_Volumes_Success(t *testing.T) {
	ctx, actuatorClient, dynamicClient := setupTestClient(true /* createNewNamespaces */, &alwaysPassingPreconditionsValidator{})

	compCR := createTestComponentCR("vol-sidecar", testNamespace, 1, true, "COMPONENT_STATE_ENABLED",
		[]map[string]any{
			{"type": "Ready", "status": "True", "reason": "ComponentHealthy", "message": "ready"},
		},
		map[string]any{
			"secretVolumeMounts": map[string]any{
				"vol-key": "app-secret:/etc/app/certs",
			},
		},
	)
	actuatorClient.SetManagerClient(createFakeManagerClient(compCR))

	payload := proto.Clone(createPayload).(*actuatortaskpb.ActuatorPayload)
	payload.WorkUnit = createWorkUnitWithSnapshots(testNamespace, `{"vol-sidecar": 1}`)

	if err := actuatorClient.CreateWorkUnit(ctx, payload); err != nil {
		t.Fatalf("CreateWorkUnit failed: %v", err)
	}

	jobSetUnstructured, err := dynamicClient.Resource(JobSetGVR).Namespace(testNamespace).Get(ctx, testJobSetName, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("dynamicClient.Resource().Get() failed: %v", err)
	}
	if jobSetUnstructured == nil {
		t.Fatalf("expected JobSet to be created")
	}

	replicatedJobs, ok, err := unstructured.NestedSlice(jobSetUnstructured.Object, "spec", "replicatedJobs")
	if err != nil || !ok || len(replicatedJobs) == 0 {
		t.Fatalf("failed to retrieve replicatedJobs: %v", err)
	}
	firstRj := replicatedJobs[0].(map[string]any)
	podSpecMap, ok, err := unstructured.NestedMap(firstRj, "template", "spec", "template", "spec")
	if err != nil || !ok {
		t.Fatalf("failed to retrieve podSpec: %v", err)
	}

	volumes, ok, err := unstructured.NestedSlice(podSpecMap, "volumes")
	if err != nil || !ok {
		t.Fatalf("failed to retrieve volumes: %v", err)
	}
	foundVol := false
	for _, vRaw := range volumes {
		v := vRaw.(map[string]any)
		if sec, ok := v["secret"].(map[string]any); ok && sec["secretName"] == "app-secret" {
			foundVol = true
			break
		}
	}
	if !foundVol {
		t.Errorf("expected secret volume app-secret in created JobSet volumes")
	}

	containers, ok, err := unstructured.NestedSlice(podSpecMap, "containers")
	if err != nil || !ok || len(containers) == 0 {
		t.Fatalf("failed to retrieve containers: %v", err)
	}
	firstContainer := containers[0].(map[string]any)
	mounts, ok, err := unstructured.NestedSlice(firstContainer, "volumeMounts")
	if err != nil || !ok {
		t.Fatalf("failed to retrieve volumeMounts: %v", err)
	}
	foundMount := false
	for _, mRaw := range mounts {
		m := mRaw.(map[string]any)
		readOnly, _ := m["readOnly"].(bool)
		if m["mountPath"] == "/etc/app/certs" && readOnly {
			foundMount = true
			break
		}
	}
	if !foundMount {
		t.Errorf("expected readOnly volume mount /etc/app/certs in created JobSet container")
	}
}

func TestConstructJobSet_ComponentSnapshot_SecretVolumeMounts(t *testing.T) {
	ctx, client := setupComponentTestClient()
	targetNS := constants.XmcLabelValue

	compCR := createTestComponentCR("vol-comp", targetNS, 1, true, "COMPONENT_STATE_ENABLED",
		[]map[string]any{
			{"type": "Ready", "status": "True", "reason": "ComponentHealthy", "message": "ready"},
		},
		map[string]any{
			"secretVolumeMounts": map[string]any{
				"mount-1": "cred-secret:/etc/certs",
				"mount-2": "cred-secret:/etc/keys",
				"mount-3": "config-secret:/etc/config",
			},
		},
	)
	client.SetManagerClient(createFakeManagerClient(compCR))

	workUnit := createWorkUnitWithSnapshots(targetNS, `{"vol-comp": 1}`)
	jobSet, err := client.constructJobSet(ctx, workUnit, testTarget, "test-author", targetNS, testTime, Priorities{})
	if err != nil {
		t.Fatalf("constructJobSet failed: %v", err)
	}

	podSpec := jobSet.Spec.ReplicatedJobs[0].Template.Spec.Template.Spec
	var secretVolumes []corev1.Volume
	for _, v := range podSpec.Volumes {
		if v.Secret != nil {
			secretVolumes = append(secretVolumes, v)
		}
	}
	if len(secretVolumes) != 2 {
		t.Fatalf("expected 2 secret volumes in PodSpec, got %d (total volumes: %d)", len(secretVolumes), len(podSpec.Volumes))
	}

	volMap := make(map[string]string)
	for _, v := range secretVolumes {
		volMap[v.Name] = v.Secret.SecretName
	}
	if len(volMap) != 2 {
		t.Errorf("expected 2 secret volumes, got %v", volMap)
	}

	containers := podSpec.Containers
	if len(containers) == 0 {
		t.Fatalf("expected containers")
	}
	mountsByPath := make(map[string]corev1.VolumeMount)
	for _, m := range containers[0].VolumeMounts {
		mountsByPath[m.MountPath] = m
	}
	for _, expectedPath := range []string{"/etc/certs", "/etc/keys", "/etc/config"} {
		m, ok := mountsByPath[expectedPath]
		if !ok {
			t.Errorf("expected mount path %q in container", expectedPath)
		} else if !m.ReadOnly {
			t.Errorf("expected volume mount %q to be ReadOnly", expectedPath)
		}
	}
}

func TestBuildSecretVolumeMounts_InvalidSpecs(t *testing.T) {
	podSpec := &corev1.PodSpec{}
	specs := map[string]string{
		"NO_COLON": "nocolon",
		"EMPTY_S":  ":/path",
		"EMPTY_M":  "sec:",
		"RELATIVE": "sec:relative/path",
	}
	mounts := buildSecretVolumeMounts(podSpec, specs)
	if len(mounts) != 0 {
		t.Errorf("expected 0 mounts for invalid specs, got %d", len(mounts))
	}
	if len(podSpec.Volumes) != 0 {
		t.Errorf("expected 0 volumes in PodSpec, got %d", len(podSpec.Volumes))
	}
	if empty := buildSecretVolumeMounts(nil, nil); empty != nil {
		t.Errorf("expected nil for nil input")
	}
}

func TestSanitizeVolumeName(t *testing.T) {
	cases := []struct {
		input    string
		expected string
	}{
		{"Secret.Name_123", "secret-name-123"},
		{"---special---", "special"},
		{"", "secret-vol"},
		{strings.Repeat("a", 60), strings.Repeat("a", 50)},
	}
	for _, tc := range cases {
		got := sanitizeVolumeName(tc.input)
		if got != tc.expected {
			t.Errorf("sanitizeVolumeName(%q) = %q, want %q", tc.input, got, tc.expected)
		}
	}
}

func TestGetOrAddSecretVolume_NameDisambiguation(t *testing.T) {
	podSpec := &corev1.PodSpec{
		Volumes: []corev1.Volume{
			{Name: "my-sec", VolumeSource: corev1.VolumeSource{Secret: &corev1.SecretVolumeSource{SecretName: "original-sec"}}},
		},
	}
	// Asking for "my-sec" with different secret name should create "my-sec-1"
	vol1 := getOrAddSecretVolume(podSpec, "my-sec")
	if vol1 != "my-sec-1" {
		t.Errorf("expected disambiguated volume name my-sec-1, got %q", vol1)
	}

	// Asking again for "original-sec" should return "my-sec"
	volOrig := getOrAddSecretVolume(podSpec, "original-sec")
	if volOrig != "my-sec" {
		t.Errorf("expected existing volume name my-sec, got %q", volOrig)
	}
}

func TestAppendVolumeMountIfNotPresent_Deduplication(t *testing.T) {
	c := &corev1.Container{
		VolumeMounts: []corev1.VolumeMount{
			{Name: "vol-a", MountPath: "/data"},
		},
	}
	appendVolumeMountIfNotPresent(c, corev1.VolumeMount{Name: "vol-b", MountPath: "/data"})
	if len(c.VolumeMounts) != 1 {
		t.Errorf("expected mount path /data to be deduplicated, got %d mounts", len(c.VolumeMounts))
	}
	appendVolumeMountIfNotPresent(c, corev1.VolumeMount{Name: "vol-c", MountPath: "/other"})
	if len(c.VolumeMounts) != 2 {
		t.Errorf("expected 2 mounts, got %d", len(c.VolumeMounts))
	}
}
