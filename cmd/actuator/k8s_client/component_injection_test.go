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

	corev1 "golang/k8s_io/api/v/v0_23/core/v1/v1"
	apierrors "golang/k8s_io/apimachinery/v/v0_23/pkg/api/errors/errors"
	"golang/k8s_io/apimachinery/v/v0_23/pkg/apis/meta/v1/unstructured/unstructured"
	metav1 "golang/k8s_io/apimachinery/v/v0_23/pkg/apis/meta/v1/v1"
	"golang/k8s_io/apimachinery/v/v0_23/pkg/runtime/runtime"
	"golang/k8s_io/apimachinery/v/v0_23/pkg/runtime/schema/schema"
	"golang/protobuf/v2/proto/proto"
	runtimeclient "golang/sigs_k8s_io/controller_runtime/v/v0_11/pkg/client/client"
	runtimefake "sigs.k8s.io/controller-runtime/pkg/client/fake"
	jobsetv1alpha2 "sigs.k8s.io/jobset/api/jobset/v1alpha2"
	esspb "xmanager_cloud/experiment_state_server/proto/messages_go_proto"
	constants "xmanager_cloud/kubernetes/common/constants"
	actuatortaskpb "xmanager_cloud/task_queue/proto/task_go_proto"
)

func createTestComponentCR(name, namespace string, gen int64, enabled bool, state string, conditions []map[string]any, access map[string]any) *unstructured.Unstructured {
	status := map[string]any{"observedGeneration": gen, "state": state}
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
	client.RuntimeConfig.EnableComponents = true
	return ctx, client
}

func TestConstructJobSet_ComponentSnapshot_Success(t *testing.T) {
	ctx, client := setupComponentTestClient()
	targetNS := constants.XmcLabelValue

	compCR := createTestComponentCR("metrics-collector", targetNS, 1, true, "COMPONENT_STATE_ENABLED",
		[]map[string]any{
			{"type": "Ready", "status": "True", "reason": "ComponentHealthy", "message": "ready"},
			{"type": "Degraded", "status": "False", "reason": "ComponentHealthy", "message": "not degraded"},
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
			name: "degraded_true",
			conditions: []map[string]any{
				{"type": "Ready", "status": "True", "reason": "OK", "message": "ready"},
				{"type": "Degraded", "status": "True", "reason": "QuotaExceeded", "message": "resource quota reached"},
			},
			wantSub: "QuotaExceeded",
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
			{"type": "Degraded", "status": "False", "reason": "ComponentHealthy", "message": "not degraded"},
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
	expectedDetail := `component "drifted-comp" generation mismatch: observed 2, locked 1`
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
	if !strings.Contains(warning, `component "comp-a" generation mismatch: observed 3, locked 1`) ||
		!strings.Contains(warning, `component "comp-b" generation mismatch: observed 5, locked 2`) {
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

func TestExtractObservedGeneration(t *testing.T) {
	tests := []struct {
		name string
		val  any
		want int64
	}{
		{name: "int64", val: int64(42), want: 42},
		{name: "int", val: 42, want: 42},
		{name: "float64", val: float64(42), want: 42},
		{name: "json_number", val: json.Number("42"), want: 42},
		{name: "string", val: "42", want: 42},
		{name: "invalid_string", val: "abc", want: 0},
		{name: "nil", val: nil, want: 0},
	}
	for _, tt := range tests {
		comp := &unstructured.Unstructured{Object: map[string]any{"status": map[string]any{"observedGeneration": tt.val}}}
		if got := extractObservedGeneration(comp); got != tt.want {
			t.Errorf("extractObservedGeneration(%s) = %v, want %v", tt.name, got, tt.want)
		}
	}
	if got := extractObservedGeneration(nil); got != 0 {
		t.Errorf("extractObservedGeneration(nil) = %v, want 0", got)
	}
}

func TestConstructJobSet_ComponentSnapshot_ClusterWideFallback(t *testing.T) {
	ctx, client := setupComponentTestClient()
	targetNS := "workunit-specific-ns"

	compCR := createTestComponentCR("cluster-comp", constants.DefaultComponentsNamespace, 1, true, "COMPONENT_STATE_ENABLED",
		[]map[string]any{
			{"type": "Ready", "status": "True", "reason": "ComponentHealthy", "message": "ready"},
			{"type": "Degraded", "status": "False", "reason": "ComponentHealthy", "message": "not degraded"},
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
	client.RuntimeConfig.EnableComponents = true
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

func TestConstructJobSet_ComponentsDisabled_WithSnapshots_FailsFast(t *testing.T) {
	ctx, client, _ := setupTestClient(true, &alwaysPassingPreconditionsValidator{})
	targetNS := constants.XmcLabelValue

	workUnit := createWorkUnitWithSnapshots(targetNS, `{"metrics-collector": 1}`)
	jobSet, err := client.constructJobSet(ctx, workUnit, testTarget, "test-author", targetNS, testTime, Priorities{})
	if err == nil {
		t.Fatalf("constructJobSet succeeded when EnableComponents=false, want non-retryable error")
	}
	if jobSet != nil {
		t.Errorf("expected jobSet to be nil on error, got: %v", jobSet)
	}

	expectedSub := "components feature is disabled on this cluster (enable_components=false)"
	if !strings.Contains(err.Error(), expectedSub) {
		t.Errorf("error %q does not contain expected substring %q", err.Error(), expectedSub)
	}

	if apierrors.IsServiceUnavailable(err) {
		t.Errorf("expected error to be terminal/non-retryable, but got ServiceUnavailable")
	}
}

func TestCreateWorkUnit_ComponentSnapshotValidation_Success(t *testing.T) {
	ctx, actuatorClient, dynamicClient := setupTestClient(true /* createNewNamespaces */, &alwaysPassingPreconditionsValidator{})
	actuatorClient.RuntimeConfig.EnableComponents = true

	compCR := createTestComponentCR("audit-sidecar", testNamespace, 1, true, "COMPONENT_STATE_ENABLED",
		[]map[string]any{
			{"type": "Ready", "status": "True", "reason": "ComponentHealthy", "message": "ready"},
			{"type": "Degraded", "status": "False", "reason": "ComponentHealthy", "message": "healthy"},
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
	actuatorClient.RuntimeConfig.EnableComponents = true
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
