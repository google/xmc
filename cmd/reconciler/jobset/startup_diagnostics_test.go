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

package jobset

import (
	"context"
	"strings"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
	v1alpha2 "sigs.k8s.io/jobset/api/jobset/v1alpha2"
	"xmanager_cloud/cmd/reconciler/shared"
	spb "xmanager_cloud/experiment_state_server/proto/messages_go_proto"
	"xmanager_cloud/kubernetes/common/constants"
)

func newStartingTestJobSet() *v1alpha2.JobSet {
	return &v1alpha2.JobSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-jobset",
			Namespace: "default",
			Labels: map[string]string{
				constants.ExperimentIDLabel:      "123",
				constants.WorkUnitIDLabel:        "1",
				constants.ExecutionIDLabel:       "1",
				"jobset.sigs.k8s.io/jobset-name": "test-jobset",
			},
		},
		Spec: v1alpha2.JobSetSpec{
			ReplicatedJobs: []v1alpha2.ReplicatedJob{{Name: "worker", Replicas: 1}},
		},
		Status: v1alpha2.JobSetStatus{
			ReplicatedJobsStatus: []v1alpha2.ReplicatedJobStatus{
				{Name: "worker", Active: 1, Ready: 0, Succeeded: 0, Failed: 0},
			},
		},
	}
}

func newWaitingPod(name, reason, msg string) *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: "default",
			Labels:    map[string]string{"jobset.sigs.k8s.io/jobset-name": "test-jobset"},
		},
		Status: corev1.PodStatus{
			ContainerStatuses: []corev1.ContainerStatus{
				{
					State: corev1.ContainerState{
						Waiting: &corev1.ContainerStateWaiting{Reason: reason, Message: msg},
					},
				},
			},
		},
	}
}

func newUnschedulableTPUPods(name, topology string) []corev1.Pod {
	return []corev1.Pod{
		{
			ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default"},
			Status: corev1.PodStatus{
				Phase: corev1.PodPending,
				Conditions: []corev1.PodCondition{
					{
						Type:    corev1.PodScheduled,
						Status:  corev1.ConditionFalse,
						Reason:  corev1.PodReasonUnschedulable,
						Message: "0/1 nodes are available: 1 node(s) didn't match Pod's node affinity/selector.",
					},
				},
			},
			Spec: corev1.PodSpec{
				NodeSelector: map[string]string{
					constants.TPUTopologySelectorKey:    topology,
					constants.TPUAcceleratorSelectorKey: "tpu-v6e-slice",
				},
			},
		},
	}
}

func TestReconcileImagePullBackOff(t *testing.T) {
	scheme, err := newScheme()
	if err != nil {
		t.Fatalf("Failed to create scheme: %v", err)
	}
	jobSet := newStartingTestJobSet()

	tests := []struct {
		name             string
		jobSetAge        time.Duration
		pods             []runtime.Object
		wantRequeue      bool
		wantState        spb.WorkUnitStatus_WorkUnitState
		wantErrorMsgPart string
		jobSetStatus     *v1alpha2.JobSetStatus
	}{
		{
			name:        "Starting less than 120s - Requeue",
			jobSetAge:   30 * time.Second,
			wantRequeue: true,
			wantState:   spb.WorkUnitStatus_STARTING,
		},
		{
			name:             "Starting more than 120s with ImagePullBackOff - FAILED",
			jobSetAge:        130 * time.Second,
			pods:             []runtime.Object{newWaitingPod("pod-1", "ImagePullBackOff", "Back-off pulling image")},
			wantRequeue:      false,
			wantState:        spb.WorkUnitStatus_FAILED,
			wantErrorMsgPart: "Failed to pull the container image (ImagePullBackOff)",
		},
		{
			name:             "Starting more than 120s with ErrImagePull - FAILED",
			jobSetAge:        130 * time.Second,
			pods:             []runtime.Object{newWaitingPod("pod-2", "ErrImagePull", "Error pulling image")},
			wantRequeue:      false,
			wantState:        spb.WorkUnitStatus_FAILED,
			wantErrorMsgPart: "Failed to pull the container image (ErrImagePull)",
		},
		{
			name:        "Starting more than 120s without ImagePullBackOff - STARTING",
			jobSetAge:   130 * time.Second,
			wantRequeue: false,
			wantState:   spb.WorkUnitStatus_STARTING,
		},
		{
			name:        "Running state - No Requeue",
			jobSetAge:   30 * time.Second,
			wantRequeue: false,
			wantState:   spb.WorkUnitStatus_RUNNING,
			jobSetStatus: &v1alpha2.JobSetStatus{
				ReplicatedJobsStatus: []v1alpha2.ReplicatedJobStatus{
					{Name: "worker", Active: 1, Ready: 1, Succeeded: 0, Failed: 0},
				},
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			js := jobSet.DeepCopy()
			js.CreationTimestamp = metav1.Time{Time: time.Now().Add(-tc.jobSetAge)}
			if tc.jobSetStatus != nil {
				js.Status = *tc.jobSetStatus
			}

			objs := append([]runtime.Object{js}, tc.pods...)
			fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithRuntimeObjects(objs...).Build()
			batcher := shared.NewUpdateBatcher(t.Context(), &shared.BatcherConfig{TickDuration: 250 * time.Millisecond}, &mockStorageUpdater{})
			r, err := NewReconciler(Params{
				Client:                   fakeClient,
				Scheme:                   scheme,
				Batcher:                  batcher,
				StartupPodErrorCheckTime: 2 * time.Minute,
			})
			if err != nil {
				t.Fatalf("NewReconciler() unexpected error: %v", err)
			}

			req := ctrl.Request{NamespacedName: types.NamespacedName{Name: "test-jobset", Namespace: "default"}}
			res, err := r.Reconcile(context.Background(), req)
			if err != nil {
				t.Fatalf("Reconcile() failed: %v", err)
			}

			if tc.wantRequeue && res.RequeueAfter <= 0 {
				t.Errorf("Expected RequeueAfter > 0, got %v", res.RequeueAfter)
			}
			if tc.wantRequeue && res.RequeueAfter > r.StartupPodErrorCheckTime {
				t.Errorf("RequeueAfter %v out of expected range, want < %v", res.RequeueAfter, r.StartupPodErrorCheckTime)
			}
			if !tc.wantRequeue && res.RequeueAfter != 0 {
				t.Errorf("Expected RequeueAfter 0, got %v", res.RequeueAfter)
			}

			batcher.UpdatesMu.Lock()
			defer batcher.UpdatesMu.Unlock()
			if len(batcher.Updates) != 1 {
				t.Fatalf("Expected 1 update, got %d", len(batcher.Updates))
			}
			update := batcher.Updates[123][1][shared.JobsetSource]
			if update.WorkUnitExecution.GetStatus().GetState() != tc.wantState {
				t.Errorf("Expected state %v, got %v", tc.wantState, update.WorkUnitExecution.GetStatus().GetState())
			}

			msg := update.WorkUnitExecution.GetStatus().GetSystemMessage().GetUserFacingText()
			if tc.wantErrorMsgPart != "" {
				if !strings.Contains(msg, tc.wantErrorMsgPart) {
					t.Errorf("Expected message to contain %q, got %q", tc.wantErrorMsgPart, msg)
				}
			} else if strings.Contains(msg, "Failed to pull the container image") {
				t.Errorf("Expected message not to contain 'Failed to pull the container image', got %q", msg)
			}
		})
	}
}

// TestReconcileUnschedulablePods verifies that a JobSet whose pods report an
// Unschedulable verdict stays in STARTING with a WARNING, requeues only until
// unschedulablePodsCheckTime (so it does not loop forever), and that a single
// shared pod fetch drives both startup checks.
func TestReconcileUnschedulablePods(t *testing.T) {
	scheme, err := newScheme()
	if err != nil {
		t.Fatalf("Failed to create scheme: %v", err)
	}

	jobSet := newStartingTestJobSet()
	unschedulablePod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "pod-unsched",
			Namespace: "default",
			Labels:    map[string]string{"jobset.sigs.k8s.io/jobset-name": "test-jobset"},
		},
		Status: corev1.PodStatus{
			Phase: corev1.PodPending,
			Conditions: []corev1.PodCondition{{
				Type:    corev1.PodScheduled,
				Status:  corev1.ConditionFalse,
				Reason:  corev1.PodReasonUnschedulable,
				Message: "0/3 nodes are available: 3 Insufficient nvidia.com/gpu.",
			}},
		},
	}

	tests := []struct {
		name        string
		jobSetAge   time.Duration
		wantRequeue bool
	}{
		{
			name:        "Unschedulable before unschedulablePodsCheckTime - warn + requeue",
			jobSetAge:   130 * time.Second,
			wantRequeue: true,
		},
		{
			name:        "Unschedulable past unschedulablePodsCheckTime - warn, no requeue",
			jobSetAge:   6 * time.Minute,
			wantRequeue: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			js := jobSet.DeepCopy()
			js.CreationTimestamp = metav1.Time{Time: time.Now().Add(-tc.jobSetAge)}

			fakeClient := fake.NewClientBuilder().
				WithScheme(scheme).
				WithRuntimeObjects(js, unschedulablePod.DeepCopy()).
				Build()
			batcher := shared.NewUpdateBatcher(t.Context(), &shared.BatcherConfig{TickDuration: 250 * time.Millisecond}, &mockStorageUpdater{})
			r, err := NewReconciler(Params{
				Client:                   fakeClient,
				Scheme:                   scheme,
				Batcher:                  batcher,
				StartupPodErrorCheckTime: 2 * time.Minute,
			})
			if err != nil {
				t.Fatalf("NewReconciler() unexpected error: %v", err)
			}

			req := ctrl.Request{NamespacedName: types.NamespacedName{Name: "test-jobset", Namespace: "default"}}
			res, err := r.Reconcile(context.Background(), req)
			if err != nil {
				t.Fatalf("Reconcile() failed: %v", err)
			}

			if tc.wantRequeue {
				if res.RequeueAfter <= 0 || res.RequeueAfter > unschedulablePodsCheckTime {
					t.Errorf("RequeueAfter %v out of expected range (0, %v]", res.RequeueAfter, unschedulablePodsCheckTime)
				}
			} else if res.RequeueAfter != 0 {
				t.Errorf("Expected RequeueAfter 0 past threshold, got %v", res.RequeueAfter)
			}

			batcher.UpdatesMu.Lock()
			defer batcher.UpdatesMu.Unlock()
			update := batcher.Updates[123][1][shared.JobsetSource]
			if got := update.WorkUnitExecution.GetStatus().GetState(); got != spb.WorkUnitStatus_STARTING {
				t.Errorf("Expected state STARTING, got %v", got)
			}
			msg := update.WorkUnitExecution.GetStatus().GetSystemMessage()
			if !strings.Contains(msg.GetUserFacingText(), "Work unit may be unschedulable") {
				t.Errorf("Expected unschedulable warning, got %q", msg.GetUserFacingText())
			}
			if msg.GetSeverity() != spb.StructuredMessage_WARNING {
				t.Errorf("Expected severity WARNING, got %v", msg.GetSeverity())
			}
		})
	}
}

func TestCheckPodsForUnschedulableTPUTopologyMismatch(t *testing.T) {
	scheme, err := newScheme()
	if err != nil {
		t.Fatalf("Failed to create scheme: %v", err)
	}
	node := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{
			Name:   "tpu-node-1",
			Labels: map[string]string{constants.TPUTopologySelectorKey: "8x8"},
		},
	}
	fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithRuntimeObjects(node).Build()
	r := &Reconciler{Client: fakeClient}
	ctx := context.Background()

	t.Run("topology mismatch surfaces detailed warning", func(t *testing.T) {
		got := r.checkPodsForUnschedulable(ctx, newUnschedulableTPUPods("pod-tpu-pending", "2x2"))
		want := `Work unit is unschedulable: requested TPU topology "2x2" (type "tpu-v6e-slice") does not match any node pool's advertised topology in the cluster. Available TPU topologies: [8x8]. Please request a topology supported by the cluster's TPU node pools.`
		if got != want {
			t.Errorf("checkPodsForUnschedulable() = %q, want %q", got, want)
		}
	})

	t.Run("topology match falls back to generic warning", func(t *testing.T) {
		got := r.checkPodsForUnschedulable(ctx, newUnschedulableTPUPods("pod-tpu-pending-quota", "8x8"))
		if strings.Contains(got, "does not match any node pool's advertised topology") {
			t.Errorf("Expected generic unschedulable warning when topology matches, got %q", got)
		}
	})

	t.Run("empty topologies falls back to generic warning", func(t *testing.T) {
		emptyClient := fake.NewClientBuilder().WithScheme(scheme).Build()
		rEmpty := &Reconciler{Client: emptyClient}
		got := rEmpty.checkPodsForUnschedulable(ctx, newUnschedulableTPUPods("pod-tpu-pending-autoscale", "2x2"))
		if strings.Contains(got, "does not match any node pool's advertised topology") {
			t.Errorf("Expected generic unschedulable warning when no TPU nodes are present, got %q", got)
		}
		if !strings.Contains(got, "Work unit may be unschedulable") {
			t.Errorf("Expected generic 'Work unit may be unschedulable' warning, got %q", got)
		}
	})

	t.Run("node list error bounds context with deadline and falls back to generic warning", func(t *testing.T) {
		var listHasDeadline bool
		errClient := fake.NewClientBuilder().
			WithScheme(scheme).
			WithInterceptorFuncs(interceptor.Funcs{
				List: func(ctx context.Context, _ client.WithWatch, _ client.ObjectList, _ ...client.ListOption) error {
					_, listHasDeadline = ctx.Deadline()
					return context.DeadlineExceeded
				},
			}).
			Build()
		rErr := &Reconciler{Client: errClient}
		got := rErr.checkPodsForUnschedulable(ctx, newUnschedulableTPUPods("pod-tpu-pending-rbac", "2x2"))
		if !listHasDeadline {
			t.Error("Expected listAvailableTPUTopologies to set a deadline on the List context")
		}
		if !strings.Contains(got, "Work unit may be unschedulable") {
			t.Errorf("Expected generic 'Work unit may be unschedulable' warning on node list error, got %q", got)
		}
	})
}

func TestReconcileMultiKueueSkipsStartupPodChecks(t *testing.T) {
	scheme, err := newScheme()
	if err != nil {
		t.Fatalf("Failed to create scheme: %v", err)
	}
	js := newStartingTestJobSet()
	js.CreationTimestamp = metav1.Time{Time: time.Now().Add(-30 * time.Second)}
	managedBy := constants.KueueMultiKueueControllerName
	js.Spec.ManagedBy = &managedBy

	fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithRuntimeObjects(js).Build()
	batcher := shared.NewUpdateBatcher(t.Context(), &shared.BatcherConfig{TickDuration: 250 * time.Millisecond}, &mockStorageUpdater{})
	r, err := NewReconciler(Params{
		Client:                   fakeClient,
		Scheme:                   scheme,
		Batcher:                  batcher,
		StartupPodErrorCheckTime: 2 * time.Minute,
	})
	if err != nil {
		t.Fatalf("NewReconciler() unexpected error: %v", err)
	}

	req := ctrl.Request{NamespacedName: types.NamespacedName{Name: "test-jobset", Namespace: "default"}}
	res, err := r.Reconcile(context.Background(), req)
	if err != nil {
		t.Fatalf("Reconcile() failed: %v", err)
	}
	if res.RequeueAfter != 0 {
		t.Errorf("Expected RequeueAfter 0 for MultiKueue-managed JobSet, got %v", res.RequeueAfter)
	}
	if got := r.handleStartupPodChecks(context.Background(), js); got.Result.RequeueAfter != 0 || got.PodErrorMsg != "" || got.StartupWarningMsg != "" {
		t.Errorf("handleStartupPodChecks() on MultiKueue JobSet = %+v, want zero result", got)
	}
}
