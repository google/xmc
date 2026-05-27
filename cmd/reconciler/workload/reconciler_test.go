// Copyright 2025 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//      http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.
package workload

import (
	"context"
	"errors"
	"slices"
	"testing"
	"time"

	corev1 "golang/k8s_io/api/v/v0_23/core/v1/v1"
	k8serrors "golang/k8s_io/apimachinery/v/v0_23/pkg/api/errors/errors"
	metav1 "golang/k8s_io/apimachinery/v/v0_23/pkg/apis/meta/v1/v1"
	"golang/k8s_io/apimachinery/v/v0_23/pkg/runtime/runtime"
	"golang/k8s_io/apimachinery/v/v0_23/pkg/types/types"
	clientgoscheme "golang/k8s_io/client_go/v/v0_23/kubernetes/scheme/scheme"
	"golang/prometheus/client/prometheus/testutil/testutil"
	ctrl "golang/sigs_k8s_io/controller_runtime/v/v0_11/controllerruntime"
	"golang/sigs_k8s_io/controller_runtime/v/v0_11/pkg/client/client"
	"golang/sigs_k8s_io/controller_runtime/v/v0_11/pkg/client/fake/fake"
	"golang/sigs_k8s_io/controller_runtime/v/v0_11/pkg/event/event"
	spb "xmanager_cloud/experiment_state_server/proto/messages_go_proto"
	"xmanager_cloud/kubernetes/api/v1beta1/v1beta1"
	"xmanager_cloud/kubernetes/common/constants"
	"xmanager_cloud/kubernetes/reconciler/shared/shared"
	storagepkg "xmanager_cloud/storage/storage"
)

type clientWithError struct {
	client.Client
	getErr error
}

func (c *clientWithError) Get(ctx context.Context, key client.ObjectKey, obj client.Object) error {
	if c.getErr != nil {
		return c.getErr
	}
	return c.Client.Get(ctx, key, obj)
}

type mockStorageUpdater struct {
	calls     map[int64][]*storagepkg.WorkUnitExecutionUpdate
	updateErr error
	updateRes map[int64]*spb.WorkUnitExecution
}

func (m *mockStorageUpdater) UpdateWorkUnitExecutions(ctx context.Context, xid int64, updates []*storagepkg.WorkUnitExecutionUpdate) (map[int64]*spb.WorkUnitExecution, error) {
	if m.calls == nil {
		m.calls = make(map[int64][]*storagepkg.WorkUnitExecutionUpdate)
	}
	m.calls[xid] = append(m.calls[xid], updates...)
	return m.updateRes, m.updateErr
}

func TestWorkloadReconciler_GetErrors(t *testing.T) {
	scheme := newScheme()
	cl := fake.NewClientBuilder().WithScheme(scheme).Build()
	rec := &Reconciler{
		Client: &clientWithError{
			Client: cl,
			getErr: k8serrors.NewInternalError(errors.New("API server unavailable")),
		},
		Scheme:  scheme,
		Batcher: shared.NewUpdateBatcher(t.Context(), &shared.BatcherConfig{TickDuration: 250 * time.Millisecond}, &mockStorageUpdater{}),
		ManagementClusterInfo: &spb.ClusterInfo{
			Project: "mgmt-project",
		},
	}

	_, err := rec.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{
			Name:      "test-workload",
			Namespace: "default",
		},
	})
	if err == nil {
		t.Errorf("Reconcile() returned nil error, want non-nil")
	}
}

func newScheme() *runtime.Scheme {
	s := runtime.NewScheme()
	clientgoscheme.AddToScheme(s)
	v1beta1.AddToScheme(s)
	return s
}

func TestWorkloadReconciler(t *testing.T) {
	reconcileTotal.Reset()
	reconcileErrorsTotal.Reset()
	updatesSentTotal.Reset()
	scheme := newScheme()
	workload := &v1beta1.Workload{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-workload",
			Namespace: "default",
		},
		Spec: workloadSpecWithNecessaryLabels,
		Status: v1beta1.WorkloadStatus{
			ClusterName: stringPtr("worker-cluster"),
		},
	}
	mkc := &v1beta1.MultiKueueCluster{
		ObjectMeta: metav1.ObjectMeta{
			Name: "worker-cluster",
		},
	}
	cl := fake.NewClientBuilder().WithScheme(scheme).WithRuntimeObjects([]runtime.Object{workload, mkc}...).Build()
	rec := &Reconciler{
		Client:  cl,
		Scheme:  scheme,
		Batcher: shared.NewUpdateBatcher(t.Context(), &shared.BatcherConfig{TickDuration: 250 * time.Millisecond}, &mockStorageUpdater{}),
		ManagementClusterInfo: &spb.ClusterInfo{
			Project: "mgmt-project",
		},
	}

	_, err := rec.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{
			Name:      "test-workload",
			Namespace: "default",
		},
	})
	if err != nil {
		t.Fatalf("Reconcile() returned unexpected error: %v", err)
	}

	if len(rec.Batcher.Updates) != 1 {
		t.Errorf("Expected 1 update in batcher, got %d", len(rec.Batcher.Updates))
	}
	if _, ok := rec.Batcher.Updates[1][1][shared.WorkloadSource]; !ok {
		t.Errorf("Expected wid 1 with workload source in updates map for xid 1")
	}

	if val := testutil.ToFloat64(reconcileTotal.WithLabelValues()); val != 1 {
		t.Errorf("reconcileTotal counter mismatch: got %v, want 1", val)
	}
	if val := testutil.ToFloat64(reconcileErrorsTotal.WithLabelValues()); val != 0 {
		t.Errorf("reconcileErrorsTotal counter mismatch: got %v, want 0", val)
	}
	if val := testutil.ToFloat64(updatesSentTotal.WithLabelValues()); val != 1 {
		t.Errorf("updatesSentTotal counter mismatch: got %v, want 1", val)
	}
}

func TestWorkloadReconciler_NoLabels(t *testing.T) {
	scheme := newScheme()
	workload := &v1beta1.Workload{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-workload",
			Namespace: "default",
		},
	}
	cl := fake.NewClientBuilder().WithScheme(scheme).WithRuntimeObjects([]runtime.Object{workload}...).Build()
	rec := &Reconciler{
		Client:  cl,
		Scheme:  scheme,
		Batcher: shared.NewUpdateBatcher(t.Context(), &shared.BatcherConfig{TickDuration: 250 * time.Millisecond}, &mockStorageUpdater{}),
		ManagementClusterInfo: &spb.ClusterInfo{
			Project: "mgmt-project",
		},
	}

	_, err := rec.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{
			Name:      "test-workload",
			Namespace: "default",
		},
	})
	if err != nil {
		t.Fatalf("Reconcile() returned unexpected error: %v", err)
	}

	if len(rec.Batcher.Updates) != 0 {
		t.Errorf("Expected 0 updates in batcher, got %d", len(rec.Batcher.Updates))
	}
}

func TestWorkloadReconciler_Inadmissible(t *testing.T) {
	scheme := newScheme()

	testCases := []struct {
		name                        string
		workload                    *v1beta1.Workload
		detectInadmissibleWorkUnits bool
		wantSubstring               string
		batcherEntries              int
	}{
		{
			name: "inadmissible queue not found",
			workload: &v1beta1.Workload{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-workload-inadmissible-queue",
					Namespace: "default",
				},
				Spec: workloadSpecWithNecessaryLabels,
				Status: v1beta1.WorkloadStatus{
					Conditions: []metav1.Condition{
						{
							Type:    "QuotaReserved",
							Status:  metav1.ConditionFalse,
							Reason:  "Inadmissible",
							Message: "LocalQueue my-queue doesn't exist",
						},
					},
				},
			},
			detectInadmissibleWorkUnits: true,
			wantSubstring:               `Work unit is inadmissible because queue "my-queue" was not found.`,
			batcherEntries:              1,
		},
		{
			name: "inadmissible generic",
			workload: &v1beta1.Workload{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-workload-inadmissible-generic",
					Namespace: "default",
				},
				Spec: workloadSpecWithNecessaryLabels,
				Status: v1beta1.WorkloadStatus{
					Conditions: []metav1.Condition{
						{
							Type:    "QuotaReserved",
							Status:  metav1.ConditionFalse,
							Reason:  "Inadmissible",
							Message: "Some other reason",
						},
					},
				},
			},
			detectInadmissibleWorkUnits: true,
			wantSubstring:               "Work unit is inadmissible, please check the workload status for more details: Some other reason",
			batcherEntries:              1,
		},
		{
			name: "inadmissible detection disabled",
			workload: &v1beta1.Workload{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-workload-inadmissible-detection-disabled",
					Namespace: "default",
				},
				Spec: workloadSpecWithNecessaryLabels,
				Status: v1beta1.WorkloadStatus{
					Conditions: []metav1.Condition{
						{
							Type:    "QuotaReserved",
							Status:  metav1.ConditionFalse,
							Reason:  "Inadmissible",
							Message: "LocalQueue my-queue doesn't exist",
						},
					},
				},
			},
			detectInadmissibleWorkUnits: false,
			wantSubstring:               "",
			batcherEntries:              1,
		},
		{
			name: "quota reserved but not inadmissible",
			workload: &v1beta1.Workload{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-workload-quota-reserved-not-inadmissible",
					Namespace: "default",
				},
				Spec: workloadSpecWithNecessaryLabels,
				Status: v1beta1.WorkloadStatus{
					Conditions: []metav1.Condition{
						{
							Type:    "QuotaReserved",
							Status:  metav1.ConditionFalse,
							Reason:  "Pending",
							Message: "flavor doesn't exist",
						},
					},
				},
			},
			detectInadmissibleWorkUnits: true,
			wantSubstring:               "",
			batcherEntries:              1,
		},
		{
			name: "inadmissible no status",
			workload: &v1beta1.Workload{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-workload-inadmissible-no-status",
					Namespace: "default",
				},
				Spec: workloadSpecWithNecessaryLabels,
				Status: v1beta1.WorkloadStatus{
					Conditions: []metav1.Condition{
						{
							Type:    "QuotaReserved",
							Status:  metav1.ConditionFalse,
							Reason:  "Inadmissible",
							Message: "Some other reason",
						},
					},
				},
			},
			detectInadmissibleWorkUnits: true,
			wantSubstring:               "Work unit is inadmissible, please check the workload status for more details: Some other reason",
			batcherEntries:              1,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			cl := fake.NewClientBuilder().WithScheme(scheme).WithRuntimeObjects([]runtime.Object{tc.workload}...).Build()
			storageUpdater := &mockStorageUpdater{}
			rec := &Reconciler{
				Client:                      cl,
				Scheme:                      scheme,
				Batcher:                     shared.NewUpdateBatcher(t.Context(), &shared.BatcherConfig{TickDuration: 250 * time.Millisecond}, storageUpdater),
				ManagementClusterInfo:       &spb.ClusterInfo{Project: "mgmt-project"},
				DetectInadmissibleWorkUnits: tc.detectInadmissibleWorkUnits,
			}

			_, err := rec.Reconcile(context.Background(), ctrl.Request{
				NamespacedName: types.NamespacedName{
					Name:      tc.workload.Name,
					Namespace: tc.workload.Namespace,
				},
			})
			if err != nil {
				t.Fatalf("Reconcile() returned unexpected error: %v", err)
			}

			if got := len(rec.Batcher.Updates); got != 1 {
				t.Fatalf("Expected 1 experiment with updates in batcher, got %d", got)
			}

			xid := int64(1)
			wid := int64(1)

			if got := len(rec.Batcher.Updates[xid][wid]); got != tc.batcherEntries {
				t.Errorf("Expected %d updates for xid=%d/wid=%d in batcher, got %d", tc.batcherEntries, xid, wid, got)
			}

			update := rec.Batcher.Updates[xid][wid][shared.WorkloadSource]

			if tc.wantSubstring != "" {
				if got := update.WorkUnitExecution.GetStatus().GetSchedulingInformation().GetUserFacingText(); got != tc.wantSubstring {
					t.Errorf("Unexpected inadmissible reason in batcher update: got %q, want %q", got, tc.wantSubstring)
				}
				if !slices.Contains(update.UpdateMask.GetPaths(), "status.scheduling_information") {
					t.Errorf("Expected status.scheduling_information to be in update mask, got %v", update.UpdateMask.GetPaths())
				}
			} else {
				if update.WorkUnitExecution.GetStatus().GetSchedulingInformation() != nil {
					t.Errorf("Expected scheduling information to be nil, got %v", update.WorkUnitExecution.GetStatus().GetSchedulingInformation())
				}
				if slices.Contains(update.UpdateMask.GetPaths(), "status.scheduling_information") {
					t.Errorf("Expected status.scheduling_information to not be in update mask, got %v", update.UpdateMask.GetPaths())
				}
			}

		})
	}
}

func TestWorkloadPredicate_Create(t *testing.T) {
	p := workloadPredicate(context.Background())
	testCases := []struct {
		name string
		obj  client.Object
		want bool
	}{
		{
			name: "valid labels",
			obj: &v1beta1.Workload{
				ObjectMeta: metav1.ObjectMeta{},
				Spec:       workloadSpecWithNecessaryLabels,
			},
			want: true,
		},
		{
			name: "missing labels",
			obj: &v1beta1.Workload{
				ObjectMeta: metav1.ObjectMeta{},
			},
			want: false,
		},
		{
			name: "invalid labels",
			obj: &v1beta1.Workload{
				ObjectMeta: metav1.ObjectMeta{},
				Spec: v1beta1.WorkloadSpec{
					PodSets: []v1beta1.PodSet{
						{
							Template: corev1.PodTemplateSpec{
								ObjectMeta: metav1.ObjectMeta{
									Labels: map[string]string{
										constants.ExperimentIDLabel: "invalid",
										constants.WorkUnitIDLabel:   "1",
										constants.ExecutionIDLabel:  "1",
									},
								},
							},
						},
					},
				},
			},
			want: false,
		},
		{
			name: "not a workload object",
			obj:  &metav1.PartialObjectMetadata{},
			want: false,
		},
		{
			name: "nil object",
			obj:  nil,
			want: false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			evt := event.CreateEvent{Object: tc.obj}
			if got := p.Create(evt); got != tc.want {
				t.Errorf("Predicate CreateFunc() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestWorkloadPredicate_Update(t *testing.T) {
	p := workloadPredicate(context.Background())
	baseWorkload := &v1beta1.Workload{
		ObjectMeta: metav1.ObjectMeta{
			Name:            "test-workload",
			Namespace:       "default",
			ResourceVersion: "1",
		},
		Spec: workloadSpecWithNecessaryLabels,
		Status: v1beta1.WorkloadStatus{
			ClusterName: stringPtr("cluster-a"),
			Admission: &v1beta1.Admission{
				PodSetAssignments: []v1beta1.PodSetAssignment{
					{
						Flavors: map[corev1.ResourceName]v1beta1.ResourceFlavorReference{
							"cpu": "flavor-a",
						},
					},
				},
			},
		},
	}

	updateCases := []struct {
		name string
		old  *v1beta1.Workload
		new  *v1beta1.Workload
		want bool
	}{
		{
			name: "no relevant change",
			old:  baseWorkload,
			new: func() *v1beta1.Workload {
				w := baseWorkload.DeepCopy()
				w.ResourceVersion = "2"
				return w
			}(),
			want: false,
		},
		{
			name: "cluster name changed",
			old:  baseWorkload,
			new: func() *v1beta1.Workload {
				w := baseWorkload.DeepCopy()
				w.ResourceVersion = "2"
				w.Status.ClusterName = stringPtr("cluster-b")
				return w
			}(),
			want: true,
		},
		{
			name: "flavor changed",
			old:  baseWorkload,
			new: func() *v1beta1.Workload {
				w := baseWorkload.DeepCopy()
				w.ResourceVersion = "2"
				w.Status.Admission.PodSetAssignments[0].Flavors["cpu"] = "flavor-b"
				return w
			}(),
			want: true,
		},
		{
			name: "admission nil to non-nil",
			old: func() *v1beta1.Workload {
				w := baseWorkload.DeepCopy()
				w.Status.Admission = nil
				return w
			}(),
			new: func() *v1beta1.Workload {
				w := baseWorkload.DeepCopy()
				w.ResourceVersion = "2"
				return w
			}(),
			want: true,
		},
		{
			name: "new workload no labels",
			old:  baseWorkload,
			new: func() *v1beta1.Workload {
				w := baseWorkload.DeepCopy()
				w.ResourceVersion = "2"
				w.Spec.PodSets[0].Template.Labels = nil
				return w
			}(),
			want: false,
		},
	}

	for _, tc := range updateCases {
		t.Run(tc.name, func(t *testing.T) {
			evt := event.UpdateEvent{
				ObjectOld: tc.old,
				ObjectNew: tc.new,
			}
			if got := p.Update(evt); got != tc.want {
				t.Errorf("Predicate UpdateFunc() = %v, want %v", got, tc.want)
			}
		})
	}
}
