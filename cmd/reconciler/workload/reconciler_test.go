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
package workload

import (
	"context"
	"errors"
	"log/slog"
	"slices"
	"testing"
	"time"

	"golang/cmp/cmp"
	"golang/prometheus/client/prometheus/testutil/testutil"
	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/utils/ptr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
	"sigs.k8s.io/controller-runtime/pkg/event"
	ctrlmetrics "sigs.k8s.io/controller-runtime/pkg/metrics"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
	v1beta1 "sigs.k8s.io/kueue/apis/kueue/v1beta1"
	spb "xmanager_cloud/experiment_state_server/proto/messages_go_proto"
	"xmanager_cloud/kubernetes/common/constants"
	"xmanager_cloud/kubernetes/reconciler/shared/shared"
	"xmanager_cloud/logging/logging"
	storagepkg "xmanager_cloud/storage/storage"
)

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

func newScheme() *runtime.Scheme {
	s := runtime.NewScheme()
	clientgoscheme.AddToScheme(s)
	v1beta1.AddToScheme(s)
	return s
}

func TestNewReconciler_Validation(t *testing.T) {
	scheme := newScheme()
	cl := fake.NewClientBuilder().WithScheme(scheme).Build()
	batcher := shared.NewUpdateBatcher(t.Context(), &shared.BatcherConfig{TickDuration: 250 * time.Millisecond}, &mockStorageUpdater{})

	tests := []struct {
		name    string
		params  Params
		wantErr bool
	}{
		{
			name: "valid params",
			params: Params{
				Client:  cl,
				Scheme:  scheme,
				Batcher: batcher,
			},
			wantErr: false,
		},
		{
			name: "missing client",
			params: Params{
				Scheme:  scheme,
				Batcher: batcher,
			},
			wantErr: true,
		},
		{
			name: "missing scheme",
			params: Params{
				Client:  cl,
				Batcher: batcher,
			},
			wantErr: true,
		},
		{
			name: "missing batcher",
			params: Params{
				Client: cl,
				Scheme: scheme,
			},
			wantErr: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rec, err := NewReconciler(tc.params)
			if (err != nil) != tc.wantErr {
				t.Fatalf("NewReconciler() error = %v, wantErr %v", err, tc.wantErr)
			}
			if !tc.wantErr && rec == nil {
				t.Fatal("NewReconciler() returned nil reconciler without error")
			}
		})
	}
}

func TestWorkloadReconciler_GetErrors(t *testing.T) {
	scheme := newScheme()
	cl := fake.NewClientBuilder().WithScheme(scheme).WithInterceptorFuncs(interceptor.Funcs{
		Get: func(context.Context, client.WithWatch, client.ObjectKey, client.Object, ...client.GetOption) error {
			return k8serrors.NewInternalError(errors.New("API server unavailable"))
		},
	}).Build()
	rec, err := NewReconciler(Params{
		Client:  cl,
		Scheme:  scheme,
		Batcher: shared.NewUpdateBatcher(t.Context(), &shared.BatcherConfig{TickDuration: 250 * time.Millisecond}, &mockStorageUpdater{}),
		ManagementClusterInfo: &spb.ClusterInfo{
			Project: "mgmt-project",
		},
	})
	if err != nil {
		t.Fatalf("NewReconciler() unexpected error: %v", err)
	}

	_, err = rec.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{
			Name:      "test-workload",
			Namespace: "default",
		},
	})
	if err == nil {
		t.Errorf("Reconcile() returned nil error, want non-nil")
	}
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
			ClusterName: ptr.To("worker-cluster"),
		},
	}
	mkc := &v1beta1.MultiKueueCluster{
		ObjectMeta: metav1.ObjectMeta{
			Name: "worker-cluster",
		},
	}
	cl := fake.NewClientBuilder().WithScheme(scheme).WithRuntimeObjects([]runtime.Object{workload, mkc}...).Build()
	rec, err := NewReconciler(Params{
		Client:  cl,
		Scheme:  scheme,
		Batcher: shared.NewUpdateBatcher(t.Context(), &shared.BatcherConfig{TickDuration: 250 * time.Millisecond}, &mockStorageUpdater{}),
		ManagementClusterInfo: &spb.ClusterInfo{
			Project: "mgmt-project",
		},
	})
	if err != nil {
		t.Fatalf("NewReconciler() unexpected error: %v", err)
	}

	_, err = rec.Reconcile(context.Background(), ctrl.Request{
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
	rec, err := NewReconciler(Params{
		Client:  cl,
		Scheme:  scheme,
		Batcher: shared.NewUpdateBatcher(t.Context(), &shared.BatcherConfig{TickDuration: 250 * time.Millisecond}, &mockStorageUpdater{}),
		ManagementClusterInfo: &spb.ClusterInfo{
			Project: "mgmt-project",
		},
	})
	if err != nil {
		t.Fatalf("NewReconciler() unexpected error: %v", err)
	}

	_, err = rec.Reconcile(context.Background(), ctrl.Request{
		NamespacedName: types.NamespacedName{
			Name:      "test-workload",
			Namespace: "default",
		},
	})
	if !errors.Is(err, reconcile.TerminalError(nil)) {
		t.Fatalf("Reconcile() error = %v, want terminal error", err)
	}
	if len(rec.Batcher.Updates) != 0 {
		t.Errorf("Expected 0 updates in batcher, got %d", len(rec.Batcher.Updates))
	}
}

func TestWorkloadReconciler_Inadmissible(t *testing.T) {
	scheme := newScheme()

	testCases := []struct {
		name           string
		workload       *v1beta1.Workload
		wantSubstring  string
		batcherEntries int
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
			wantSubstring:  `Work unit is inadmissible because queue "my-queue" was not found.`,
			batcherEntries: 1,
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
			wantSubstring:  "Work unit is inadmissible, please check the workload status for more details: Some other reason",
			batcherEntries: 1,
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
			wantSubstring:  "",
			batcherEntries: 1,
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
			wantSubstring:  "Work unit is inadmissible, please check the workload status for more details: Some other reason",
			batcherEntries: 1,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			cl := fake.NewClientBuilder().WithScheme(scheme).WithRuntimeObjects([]runtime.Object{tc.workload}...).Build()
			storageUpdater := &mockStorageUpdater{}
			rec, err := NewReconciler(Params{
				Client:                cl,
				Scheme:                scheme,
				Batcher:               shared.NewUpdateBatcher(t.Context(), &shared.BatcherConfig{TickDuration: 250 * time.Millisecond}, storageUpdater),
				ManagementClusterInfo: &spb.ClusterInfo{Project: "mgmt-project"},
			})
			if err != nil {
				t.Fatalf("NewReconciler() unexpected error: %v", err)
			}

			_, err = rec.Reconcile(context.Background(), ctrl.Request{
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
				return
			}

			if update.WorkUnitExecution.GetStatus().GetSchedulingInformation() != nil {
				t.Errorf("Expected scheduling information to be nil, got %v", update.WorkUnitExecution.GetStatus().GetSchedulingInformation())
			}
			if slices.Contains(update.UpdateMask.GetPaths(), "status.scheduling_information") {
				t.Errorf("Expected status.scheduling_information to not be in update mask, got %v", update.UpdateMask.GetPaths())
			}
		})
	}
}

// levelRecorder is a slog.Handler that records the level of every log record
// it receives, so tests can assert on emitted log severities. Handlers derived
// via WithAttrs/WithGroup are new instances (per the slog.Handler contract) that
// share the same levels slice.
type levelRecorder struct {
	levels *[]slog.Level
}

func (h *levelRecorder) Enabled(context.Context, slog.Level) bool { return true }

func (h *levelRecorder) Handle(_ context.Context, r slog.Record) error {
	*h.levels = append(*h.levels, r.Level)
	return nil
}

func (h *levelRecorder) WithAttrs([]slog.Attr) slog.Handler { return &levelRecorder{levels: h.levels} }

func (h *levelRecorder) WithGroup(string) slog.Handler { return &levelRecorder{levels: h.levels} }

func TestHasValidXMCLabels(t *testing.T) {
	testCases := []struct {
		name       string
		podLabels  map[string]string
		want       bool
		wantLevels []slog.Level
	}{
		{
			name:       "valid labels logs nothing",
			podLabels:  workloadSpecWithNecessaryLabels.PodSets[0].Template.Labels,
			want:       true,
			wantLevels: nil,
		},
		{
			name:       "missing labels logs warning",
			podLabels:  nil,
			want:       false,
			wantLevels: []slog.Level{slog.LevelWarn},
		},
		{
			name: "invalid labels logs error",
			podLabels: map[string]string{
				constants.ExperimentIDLabel: "invalid",
				constants.WorkUnitIDLabel:   "1",
				constants.ExecutionIDLabel:  "1",
			},
			want:       false,
			wantLevels: []slog.Level{slog.LevelError},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			var recordedLevels []slog.Level
			ctx := logging.WithLogger(t.Context(), slog.New(&levelRecorder{levels: &recordedLevels}))
			workload := &v1beta1.Workload{
				ObjectMeta: metav1.ObjectMeta{Name: "test-workload"},
				Spec: v1beta1.WorkloadSpec{
					PodSets: []v1beta1.PodSet{{
						Template: corev1.PodTemplateSpec{
							ObjectMeta: metav1.ObjectMeta{Labels: tc.podLabels},
						},
					}},
				},
			}

			if got := hasValidXMCLabels(ctx, workload); got != tc.want {
				t.Errorf("hasValidXMCLabels() = %v, want %v", got, tc.want)
			}
			if diff := cmp.Diff(tc.wantLevels, recordedLevels); diff != "" {
				t.Errorf("hasValidXMCLabels() logged unexpected levels (-want +got):\n%s", diff)
			}
		})
	}
}

func TestWorkloadPredicate_Create(t *testing.T) {
	p := workloadPredicate(context.Background())
	testCases := []struct {
		name string
		obj  *v1beta1.Workload
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
			name: "nil object",
			obj:  nil,
			want: false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			evt := event.TypedCreateEvent[*v1beta1.Workload]{Object: tc.obj}
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
			ClusterName: ptr.To("cluster-a"),
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
				w.Status.ClusterName = ptr.To("cluster-b")
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
		{
			name: "nil old object",
			old:  nil,
			new:  baseWorkload,
			want: false,
		},
	}

	for _, tc := range updateCases {
		t.Run(tc.name, func(t *testing.T) {
			evt := event.TypedUpdateEvent[*v1beta1.Workload]{
				ObjectOld: tc.old,
				ObjectNew: tc.new,
			}
			if got := p.Update(evt); got != tc.want {
				t.Errorf("Predicate UpdateFunc() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestWorkloadPredicate_DeleteAndGeneric(t *testing.T) {
	p := workloadPredicate(context.Background())
	obj := &v1beta1.Workload{Spec: workloadSpecWithNecessaryLabels}
	if p.Delete(event.TypedDeleteEvent[*v1beta1.Workload]{Object: obj}) {
		t.Error("Predicate DeleteFunc() = true, want false")
	}
	if p.Generic(event.TypedGenericEvent[*v1beta1.Workload]{Object: obj}) {
		t.Error("Predicate GenericFunc() = true, want false")
	}
}

func TestMetricsRegisteredOnManagerRegistry(t *testing.T) {
	// Label-less vectors only appear in Gather once a child exists.
	reconcileTotal.WithLabelValues()
	reconcileErrorsTotal.WithLabelValues()
	reconcileDuration.WithLabelValues()
	updatesSentTotal.WithLabelValues()
	mfs, err := ctrlmetrics.Registry.Gather()
	if err != nil {
		t.Fatalf("ctrlmetrics.Registry.Gather() failed: %v", err)
	}
	registered := make(map[string]bool, len(mfs))
	for _, mf := range mfs {
		registered[mf.GetName()] = true
	}
	for _, name := range []string{"workload_reconcile_total", "workload_reconcile_errors_total", "workload_reconcile_duration_milliseconds", "workload_updates_sent_to_batcher_total"} {
		if !registered[name] {
			t.Errorf("metric family %q not registered on ctrlmetrics.Registry", name)
		}
	}
}
