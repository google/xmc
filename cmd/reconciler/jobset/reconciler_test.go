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
	"errors"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/prometheus/client_golang/prometheus/testutil"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	k8serrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
	ctrlmetrics "sigs.k8s.io/controller-runtime/pkg/metrics"
	v1alpha2 "sigs.k8s.io/jobset/api/jobset/v1alpha2"
	"xmanager_cloud/cmd/reconciler/shared"
	spb "xmanager_cloud/experiment_state_server/proto/messages_go_proto"
	"xmanager_cloud/internal/kubernetes/common/constants"
	storagepkg "xmanager_cloud/internal/storage"
)

type mockStorageUpdater struct {
	calls     map[int64][]*storagepkg.WorkUnitExecutionUpdate
	updateErr error
	updateRes map[int64]*spb.WorkUnitExecution
	fetchErr  error
	fetchRes  *spb.WorkUnitExecution

	// listArtifacts is returned by ListArtifacts. The reconciler reads a single
	// page, so no pagination is modeled. listArtifactsErr, when set, is returned
	// instead.
	listArtifacts      []*spb.Artifact
	listArtifactsErr   error
	listArtifactsCalls int

	// gotFilters records the filter argument passed to each ListArtifacts call so
	// tests can assert the reconciler applies the server-side ManagedXprof filter.
	gotFilters []string
}

func (m *mockStorageUpdater) FetchCurrentWorkUnitExecution(ctx context.Context, xid, wid int64) (*spb.WorkUnitExecution, error) {
	return m.fetchRes, m.fetchErr
}

func (m *mockStorageUpdater) ListArtifacts(ctx context.Context, parent string, pageSize int32, pageToken, filter, orderBy string, staleness time.Duration) ([]*spb.Artifact, string, error) {
	m.gotFilters = append(m.gotFilters, filter)
	m.listArtifactsCalls++
	if m.listArtifactsErr != nil {
		return nil, "", m.listArtifactsErr
	}
	return m.listArtifacts, "", nil
}

func (m *mockStorageUpdater) UpdateWorkUnitExecutions(ctx context.Context, xid int64, updates []*storagepkg.WorkUnitExecutionUpdate) (map[int64]*spb.WorkUnitExecution, error) {
	if m.calls == nil {
		m.calls = make(map[int64][]*storagepkg.WorkUnitExecutionUpdate)
	}
	m.calls[xid] = append(m.calls[xid], updates...)
	return m.updateRes, m.updateErr
}

// failingGet returns interceptor.Funcs whose Get always fails with err.
func failingGet(err error) interceptor.Funcs {
	return interceptor.Funcs{
		Get: func(context.Context, client.WithWatch, client.ObjectKey, client.Object, ...client.GetOption) error {
			return err
		},
	}
}

// failingPatch returns interceptor.Funcs whose Patch always fails with err.
func failingPatch(err error) interceptor.Funcs {
	return interceptor.Funcs{
		Patch: func(context.Context, client.WithWatch, client.Object, client.Patch, ...client.PatchOption) error {
			return err
		},
	}
}

// Helper to create a new scheme with our types registered.
func newScheme() (*runtime.Scheme, error) {
	scheme := runtime.NewScheme()
	if err := clientgoscheme.AddToScheme(scheme); err != nil {
		return nil, err
	}
	if err := v1alpha2.AddToScheme(scheme); err != nil {
		return nil, err
	}
	return scheme, nil
}

func TestNewReconciler_Validation(t *testing.T) {
	scheme, err := newScheme()
	if err != nil {
		t.Fatalf("Failed to create scheme: %v", err)
	}
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

func TestActuatorReconciler(t *testing.T) {
	scheme, err := newScheme()
	if err != nil {
		t.Fatalf("Failed to create scheme: %v", err)
	}

	tests := []struct {
		name         string
		initialObjs  []runtime.Object
		req          ctrl.Request
		expectResult ctrl.Result
		expectErr    bool
		getErr       error
	}{
		{
			name:        "JobSet Not Found",
			initialObjs: []runtime.Object{
				// No objects exist initially
			},
			req: ctrl.Request{
				NamespacedName: types.NamespacedName{
					Name:      "test-jobset",
					Namespace: "default",
				},
			},
			// Current Reconcile function just logs and returns no error.
			expectResult: ctrl.Result{},
			expectErr:    false,
			getErr:       nil,
		},
		{
			name: "JobSet Exists",
			initialObjs: []runtime.Object{
				&v1alpha2.JobSet{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "test-jobset",
						Namespace: "default",
						Labels: map[string]string{
							constants.ExperimentIDLabel: "123",
							constants.WorkUnitIDLabel:   "1",
							constants.ExecutionIDLabel:  "1",
						},
					},
				},
			},
			req: ctrl.Request{
				NamespacedName: types.NamespacedName{
					Name:      "test-jobset",
					Namespace: "default",
				},
			},
			// Current Reconcile function just logs and returns no error.
			expectResult: ctrl.Result{},
			expectErr:    false,
			getErr:       nil,
		},
		{
			name:        "JobSet Get fails",
			initialObjs: []runtime.Object{},
			req: ctrl.Request{
				NamespacedName: types.NamespacedName{
					Name:      "test-jobset",
					Namespace: "default",
				},
			},
			expectResult: ctrl.Result{},
			expectErr:    true,
			getErr:       k8serrors.NewInternalError(errors.New("API server unavailable")),
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			reconcileTotal.Reset()
			reconcileErrorsTotal.Reset()
			builder := fake.NewClientBuilder().WithScheme(scheme).WithRuntimeObjects(tc.initialObjs...)
			if tc.getErr != nil {
				builder = builder.WithInterceptorFuncs(failingGet(tc.getErr))
			}

			reconciler, err := NewReconciler(Params{
				Client:  builder.Build(),
				Scheme:  scheme,
				Batcher: shared.NewUpdateBatcher(t.Context(), &shared.BatcherConfig{TickDuration: 250 * time.Millisecond}, &mockStorageUpdater{}),
			})
			if err != nil {
				t.Fatalf("NewReconciler() unexpected error: %v", err)
			}

			result, err := reconciler.Reconcile(context.Background(), tc.req)

			if (err != nil) != tc.expectErr {
				t.Errorf("Reconcile() error = %v, wantErr %v", err, tc.expectErr)
			}

			if result != tc.expectResult {
				t.Errorf("Reconcile() result = %v, want %v", result, tc.expectResult)
			}

			if val := testutil.ToFloat64(reconcileTotal.WithLabelValues()); val != 1 {
				t.Errorf("reconcileTotal counter mismatch: got %v, want 1", val)
			}
			expectedErrorsCounter := 0
			if tc.expectErr {
				expectedErrorsCounter = 1
			}
			if val := testutil.ToFloat64(reconcileErrorsTotal.WithLabelValues()); val != float64(expectedErrorsCounter) {
				t.Errorf("reconcileErrorsTotal counter mismatch: got %v, want %v", val, expectedErrorsCounter)
			}
		})
	}
}

func TestReconcileAddsToBatcher(t *testing.T) {
	updatesSentTotal.Reset()
	scheme, err := newScheme()
	if err != nil {
		t.Fatalf("Failed to create scheme: %v", err)
	}

	jobSet := &v1alpha2.JobSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-jobset",
			Namespace: "default",
			Labels: map[string]string{
				constants.ExperimentIDLabel: "123",
				constants.WorkUnitIDLabel:   "1",
				constants.ExecutionIDLabel:  "1",
			},
			Finalizers: []string{constants.JobSetFinalizer},
		},
	}

	fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithRuntimeObjects(jobSet).Build()

	r, err := NewReconciler(Params{
		Client:  fakeClient,
		Scheme:  scheme,
		Batcher: shared.NewUpdateBatcher(t.Context(), &shared.BatcherConfig{TickDuration: 250 * time.Millisecond}, &mockStorageUpdater{}),
	})
	if err != nil {
		t.Fatalf("NewReconciler() unexpected error: %v", err)
	}

	req := ctrl.Request{
		NamespacedName: types.NamespacedName{
			Name:      "test-jobset",
			Namespace: "default",
		},
	}

	// Test Reconcile adds to updates map.
	_, err = r.Reconcile(context.Background(), req)
	if err != nil {
		t.Fatalf("Reconcile failed: %v", err)
	}

	r.Batcher.UpdatesMu.Lock()
	if len(r.Batcher.Updates) != 1 {
		t.Errorf("Expected 1 update in map, got %d", len(r.Batcher.Updates))
	}
	if _, ok := r.Batcher.Updates[123]; !ok {
		t.Errorf("Expected xid 123 in updates map")
	}
	if _, ok := r.Batcher.Updates[123][1][shared.JobsetSource]; !ok {
		t.Errorf("Expected wid 1 with jobset source in updates map for xid 123")
	}
	r.Batcher.UpdatesMu.Unlock()

	if val := testutil.ToFloat64(updatesSentTotal.WithLabelValues()); val != 1 {
		t.Errorf("updatesSentTotal counter mismatch: got %v, want 1", val)
	}
}

func TestActuatorReconcilerFinalizer_PatchErrors(t *testing.T) {
	scheme, err := newScheme()
	if err != nil {
		t.Fatalf("Failed to create scheme: %v", err)
	}
	jobSetWithLabelsAndFinalizer := &v1alpha2.JobSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-jobset",
			Namespace: "default",
			Labels: map[string]string{
				constants.ExperimentIDLabel: "123",
				constants.WorkUnitIDLabel:   "1",
				constants.ExecutionIDLabel:  "1",
			},
			Finalizers: []string{constants.JobSetFinalizer},
		},
	}
	req := ctrl.Request{NamespacedName: types.NamespacedName{Name: "test-jobset", Namespace: "default"}}

	testCases := []struct {
		name           string
		patchErr       error
		wantResult     ctrl.Result
		wantErr        bool
		wantErrorCount float64
	}{
		{
			name:           "NotFound",
			patchErr:       &k8serrors.StatusError{ErrStatus: metav1.Status{Reason: metav1.StatusReasonNotFound}},
			wantResult:     ctrl.Result{},
			wantErr:        false,
			wantErrorCount: 0,
		},
		{
			name:           "InvalidPreconditionFailed",
			patchErr:       &k8serrors.StatusError{ErrStatus: metav1.Status{Reason: metav1.StatusReasonInvalid}},
			wantResult:     ctrl.Result{RequeueAfter: finalizerPatchRetryDelay},
			wantErr:        false,
			wantErrorCount: 0,
		},
		{
			name:           "OtherError",
			patchErr:       errors.New("other error"),
			wantResult:     ctrl.Result{},
			wantErr:        true,
			wantErrorCount: 1,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			reconcileErrorsTotal.Reset()
			updatesSentTotal.Reset()
			initialJobSet := jobSetWithLabelsAndFinalizer.DeepCopy()
			initialJobSet.DeletionTimestamp = &metav1.Time{Time: time.Date(2025, time.November, 19, 15, 59, 26, 0, time.UTC)}
			cl := fake.NewClientBuilder().
				WithScheme(scheme).
				WithRuntimeObjects(initialJobSet).
				WithInterceptorFuncs(failingPatch(tc.patchErr)).
				Build()
			batcher := shared.NewUpdateBatcher(t.Context(), &shared.BatcherConfig{TickDuration: 250 * time.Millisecond}, &mockStorageUpdater{})
			reconciler, err := NewReconciler(Params{
				Client:  cl,
				Scheme:  scheme,
				Batcher: batcher,
			})
			if err != nil {
				t.Fatalf("NewReconciler() unexpected error: %v", err)
			}

			result, err := reconciler.Reconcile(context.Background(), req)
			if (err != nil) != tc.wantErr {
				t.Errorf("Reconcile() error = %v, wantErr %v", err, tc.wantErr)
			}
			if result != tc.wantResult {
				t.Errorf("Reconcile() result = %v, want %v", result, tc.wantResult)
			}
			if gotErrs := testutil.ToFloat64(reconcileErrorsTotal.WithLabelValues()); gotErrs != tc.wantErrorCount {
				t.Errorf("reconcileErrorsTotal = %v, want %v", gotErrs, tc.wantErrorCount)
			}
			if gotSent := testutil.ToFloat64(updatesSentTotal.WithLabelValues()); gotSent != 0 {
				t.Errorf("updatesSentTotal = %v, want 0 when patch fails", gotSent)
			}
			if len(batcher.Updates) != 0 {
				t.Errorf("batcher.Updates len = %d, want 0 when patch fails", len(batcher.Updates))
			}
		})
	}
}

func TestActuatorReconcilerFinalizer_PatchTestsFinalizerIndex(t *testing.T) {
	scheme, err := newScheme()
	if err != nil {
		t.Fatalf("Failed to create scheme: %v", err)
	}
	initialJobSet := &v1alpha2.JobSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-jobset",
			Namespace: "default",
			Labels: map[string]string{
				constants.ExperimentIDLabel: "123",
				constants.WorkUnitIDLabel:   "1",
				constants.ExecutionIDLabel:  "1",
			},
			Finalizers:        []string{"other-finalizer", constants.JobSetFinalizer},
			DeletionTimestamp: &metav1.Time{Time: time.Date(2025, time.November, 19, 15, 59, 26, 0, time.UTC)},
		},
	}
	var patchType types.PatchType
	var patchData string
	cl := fake.NewClientBuilder().
		WithScheme(scheme).
		WithRuntimeObjects(initialJobSet).
		WithInterceptorFuncs(interceptor.Funcs{
			Patch: func(ctx context.Context, c client.WithWatch, obj client.Object, patch client.Patch, opts ...client.PatchOption) error {
				patchType = patch.Type()
				data, err := patch.Data(obj)
				if err != nil {
					return err
				}
				patchData = string(data)
				return c.Patch(ctx, obj, patch, opts...)
			},
		}).
		Build()
	reconciler, err := NewReconciler(Params{
		Client:  cl,
		Scheme:  scheme,
		Batcher: shared.NewUpdateBatcher(t.Context(), &shared.BatcherConfig{TickDuration: 250 * time.Millisecond}, &mockStorageUpdater{}),
	})
	if err != nil {
		t.Fatalf("NewReconciler() unexpected error: %v", err)
	}

	req := ctrl.Request{NamespacedName: types.NamespacedName{Name: "test-jobset", Namespace: "default"}}
	if _, err := reconciler.Reconcile(context.Background(), req); err != nil {
		t.Fatalf("Reconcile() unexpected error: %v", err)
	}
	if patchType != types.JSONPatchType {
		t.Errorf("finalizer patch type = %q, want %q", patchType, types.JSONPatchType)
	}
	wantPatch := `[{"op":"test","path":"/metadata/finalizers/1","value":"jobset.xmanager.cloud/finalizer"},{"op":"remove","path":"/metadata/finalizers/1"}]`
	if patchData != wantPatch {
		t.Errorf("finalizer patch = %s, want %s", patchData, wantPatch)
	}
}

func TestActuatorReconcilerFinalizer_WithBatcherUpdate(t *testing.T) {
	scheme, err := newScheme()
	if err != nil {
		t.Fatalf("Failed to create scheme: %v", err)
	}

	baseJobSetWithLabels := &v1alpha2.JobSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-jobset",
			Namespace: "default",
			Labels: map[string]string{
				constants.ExperimentIDLabel: "123",
				constants.WorkUnitIDLabel:   "1",
				constants.ExecutionIDLabel:  "1",
			},
		},
	}

	jobSetWithLabelsAndFinalizer := baseJobSetWithLabels.DeepCopy()
	jobSetWithLabelsAndFinalizer.Finalizers = []string{constants.JobSetFinalizer}

	req := ctrl.Request{NamespacedName: types.NamespacedName{Name: "test-jobset", Namespace: "default"}}

	testCases := []struct {
		name           string
		initialJobSet  *v1alpha2.JobSet
		req            ctrl.Request
		wantRequeue    bool
		wantFinalizers []string
		wantState      spb.WorkUnitStatus_WorkUnitState
	}{
		{
			name: "Deletion with valid labels sends STOPPED update and removes finalizer",
			initialJobSet: func() *v1alpha2.JobSet {
				js := jobSetWithLabelsAndFinalizer.DeepCopy()
				js.DeletionTimestamp = &metav1.Time{Time: time.Date(2025, time.November, 19, 15, 59, 26, 0, time.UTC)}
				return js
			}(),
			req:            req,
			wantRequeue:    false,
			wantFinalizers: nil,
			wantState:      spb.WorkUnitStatus_STOPPED,
		},
		{
			name: "Deletion with nested CreationTimestamp succeeds",
			initialJobSet: func() *v1alpha2.JobSet {
				js := jobSetWithLabelsAndFinalizer.DeepCopy()
				js.DeletionTimestamp = &metav1.Time{Time: time.Date(2025, time.November, 19, 15, 59, 26, 0, time.UTC)}
				js.Spec.ReplicatedJobs = []v1alpha2.ReplicatedJob{
					{
						Name: "rj1",
						Template: batchv1.JobTemplateSpec{
							ObjectMeta: metav1.ObjectMeta{
								CreationTimestamp: metav1.Time{Time: time.Date(2025, time.November, 19, 15, 59, 26, 0, time.UTC)},
							},
							Spec: batchv1.JobSpec{
								Template: corev1.PodTemplateSpec{
									ObjectMeta: metav1.ObjectMeta{
										CreationTimestamp: metav1.Time{Time: time.Date(2025, time.November, 19, 15, 59, 26, 0, time.UTC)},
									},
								},
							},
						},
					},
				}
				return js
			}(),
			req:            req,
			wantRequeue:    false,
			wantFinalizers: nil,
			wantState:      spb.WorkUnitStatus_STOPPED,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			updatesSentTotal.Reset()
			fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithRuntimeObjects(tc.initialJobSet).Build()
			batcher := shared.NewUpdateBatcher(t.Context(), &shared.BatcherConfig{TickDuration: 250 * time.Millisecond}, &mockStorageUpdater{})
			reconciler, err := NewReconciler(Params{
				Client:  fakeClient,
				Scheme:  scheme,
				Batcher: batcher,
			})
			if err != nil {
				t.Fatalf("NewReconciler() unexpected error: %v", err)
			}

			result, err := reconciler.Reconcile(context.Background(), tc.req)
			if err != nil {
				t.Fatalf("Reconcile() failed: %v", err)
			}

			if gotRequeue := !result.IsZero(); gotRequeue != tc.wantRequeue {
				t.Errorf("Reconcile() result = %+v, want requeue %v", result, tc.wantRequeue)
			}

			var gotJobSet v1alpha2.JobSet
			err = fakeClient.Get(context.Background(), tc.req.NamespacedName, &gotJobSet)
			if tc.initialJobSet.DeletionTimestamp != nil && len(tc.initialJobSet.Finalizers) > 0 {
				if err == nil || !k8serrors.IsNotFound(err) {
					t.Fatalf("Expected NotFound error after finalizer removal during deletion, but got err: %v", err)
				}
			} else if err != nil {
				t.Fatalf("Failed to get JobSet after reconcile: %v", err)
			}

			if err == nil {
				if diff := cmp.Diff(tc.wantFinalizers, gotJobSet.Finalizers); diff != "" {
					t.Errorf("JobSet finalizers unexpected diff (-want +got):\n%s", diff)
				}
			}

			batcher.UpdatesMu.Lock()
			defer batcher.UpdatesMu.Unlock()
			if len(batcher.Updates) == 0 {
				t.Fatalf("Expected update in batcher, but batcher is empty")
			}
			update := batcher.Updates[123][1][shared.JobsetSource]
			if update.WorkUnitExecution.GetStatus().GetState() != tc.wantState {
				t.Errorf("Batcher update state = %v, want %v", update.WorkUnitExecution.GetStatus().GetState(), tc.wantState)
			}

			if val := testutil.ToFloat64(updatesSentTotal.WithLabelValues()); val != 1 {
				t.Errorf("updatesSentTotal counter mismatch: got %v, want 1", val)
			}
		})
	}
}

func TestActuatorReconcilerFinalizer_WithoutBatcherUpdateDeletesJobSet(t *testing.T) {
	scheme, err := newScheme()
	if err != nil {
		t.Fatalf("Failed to create scheme: %v", err)
	}

	jobSetWithoutLabels := &v1alpha2.JobSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-jobset-no-labels",
			Namespace: "default",
		},
	}

	jobSetWithoutLabelsWithFinalizer := jobSetWithoutLabels.DeepCopy()
	jobSetWithoutLabelsWithFinalizer.Finalizers = []string{constants.JobSetFinalizer}

	reqNoLabels := ctrl.Request{NamespacedName: types.NamespacedName{Name: "test-jobset-no-labels", Namespace: "default"}}

	initialJobSet := jobSetWithoutLabelsWithFinalizer.DeepCopy()
	initialJobSet.DeletionTimestamp = &metav1.Time{Time: time.Date(2025, time.November, 19, 15, 59, 26, 0, time.UTC)}

	fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithRuntimeObjects(initialJobSet).Build()
	batcher := shared.NewUpdateBatcher(t.Context(), &shared.BatcherConfig{TickDuration: 250 * time.Millisecond}, &mockStorageUpdater{})
	reconciler, err := NewReconciler(Params{
		Client:  fakeClient,
		Scheme:  scheme,
		Batcher: batcher,
	})
	if err != nil {
		t.Fatalf("NewReconciler() unexpected error: %v", err)
	}

	result, err := reconciler.Reconcile(context.Background(), reqNoLabels)
	if err != nil {
		t.Fatalf("Reconcile() failed: %v", err)
	}

	if !result.IsZero() {
		t.Errorf("Reconcile() result = %+v, want zero result", result)
	}

	var gotJobSet v1alpha2.JobSet
	err = fakeClient.Get(context.Background(), reqNoLabels.NamespacedName, &gotJobSet)
	if err == nil || !k8serrors.IsNotFound(err) {
		t.Fatalf("Expected NotFound error after finalizer removal during deletion, but got err: %v", err)
	}

	batcher.UpdatesMu.Lock()
	defer batcher.UpdatesMu.Unlock()
	if len(batcher.Updates) != 0 {
		t.Errorf("Expected no updates in batcher, got %d", len(batcher.Updates))
	}
}

func TestActuatorReconcilerFinalizer_WithoutBatcherUpdateDoesNotDeleteJobSet(t *testing.T) {
	scheme, err := newScheme()
	if err != nil {
		t.Fatalf("Failed to create scheme: %v", err)
	}

	baseJobSetWithLabels := &v1alpha2.JobSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-jobset",
			Namespace: "default",
			Labels: map[string]string{
				constants.ExperimentIDLabel: "123",
				constants.WorkUnitIDLabel:   "1",
				constants.ExecutionIDLabel:  "1",
			},
		},
	}

	req := ctrl.Request{NamespacedName: types.NamespacedName{Name: "test-jobset", Namespace: "default"}}

	initialJobSet := baseJobSetWithLabels.DeepCopy()
	initialJobSet.DeletionTimestamp = &metav1.Time{Time: time.Date(2025, time.November, 19, 15, 59, 26, 0, time.UTC)}
	initialJobSet.Finalizers = []string{"other-finalizer"}

	fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithRuntimeObjects(initialJobSet).Build()
	batcher := shared.NewUpdateBatcher(t.Context(), &shared.BatcherConfig{TickDuration: 250 * time.Millisecond}, &mockStorageUpdater{})
	reconciler, err := NewReconciler(Params{
		Client:  fakeClient,
		Scheme:  scheme,
		Batcher: batcher,
	})
	if err != nil {
		t.Fatalf("NewReconciler() unexpected error: %v", err)
	}

	result, err := reconciler.Reconcile(context.Background(), req)
	if err != nil {
		t.Fatalf("Reconcile() failed: %v", err)
	}

	if !result.IsZero() {
		t.Errorf("Reconcile() result = %+v, want zero result", result)
	}

	var gotJobSet v1alpha2.JobSet
	err = fakeClient.Get(context.Background(), req.NamespacedName, &gotJobSet)
	if err != nil {
		t.Fatalf("Failed to get JobSet after reconcile: %v", err)
	}
	if diff := cmp.Diff([]string{"other-finalizer"}, gotJobSet.Finalizers); diff != "" {
		t.Errorf("JobSet finalizers unexpected diff (-want +got):\n%s", diff)
	}

	batcher.UpdatesMu.Lock()
	defer batcher.UpdatesMu.Unlock()
	if len(batcher.Updates) != 0 {
		t.Errorf("Expected no updates in batcher, got %d", len(batcher.Updates))
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
	for _, name := range []string{"jobset_reconcile_total", "jobset_reconcile_errors_total", "jobset_reconcile_duration_milliseconds", "jobset_updates_sent_to_batcher_total", "jobset_reconciler_mlrun_sync_duration_seconds"} {
		if !registered[name] {
			t.Errorf("metric family %q not registered on ctrlmetrics.Registry", name)
		}
	}
}
