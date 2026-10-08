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
	"net/http"
	"strings"
	"testing"
	"time"

	"golang/cmp/cmp"
	"golang/prometheus/client/prometheus/testutil/testutil"
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
	"xmanager_cloud/cmd/reconciler/hypercomputeclient"
	"xmanager_cloud/cmd/reconciler/shared"
	spb "xmanager_cloud/experiment_state_server/proto/messages_go_proto"
	storagepkg "xmanager_cloud/internal/storage"
	"xmanager_cloud/kubernetes/common/constants"
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

// mockMLRunCreator records the arguments of the most recent CreateMLRun call so
// tests can assert on the request the reconciler builds, and returns canned
// results/errors to exercise both the success and failure paths.
type mockMLRunCreator struct {
	createCalls int
	lastID      string
	lastDisplay string
	lastPhase   string
	lastOpts    *hypercomputeclient.CreateMLRunOptions
	retRes      map[string]any
	retErr      error

	getCalls int
	getRes   map[string]any
	getErr   error

	updateCalls    int
	lastUpdateID   string
	lastUpdateOpts *hypercomputeclient.UpdateMLRunOptions
	updateRes      map[string]any
	updateErr      error
}

func (m *mockMLRunCreator) CreateMLRun(ctx context.Context, name, displayName, runPhase string, opts *hypercomputeclient.CreateMLRunOptions) (map[string]any, error) {
	m.createCalls++
	m.lastID = name
	m.lastDisplay = displayName
	m.lastPhase = runPhase
	m.lastOpts = opts
	return m.retRes, m.retErr
}

func (m *mockMLRunCreator) GetMLRun(ctx context.Context, name string) (map[string]any, error) {
	m.getCalls++
	return m.getRes, m.getErr
}

func (m *mockMLRunCreator) UpdateMLRun(ctx context.Context, name string, opts *hypercomputeclient.UpdateMLRunOptions) (map[string]any, error) {
	m.updateCalls++
	m.lastUpdateID = name
	m.lastUpdateOpts = opts
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

func TestEnsureMLRunCreated(t *testing.T) {
	now := time.UnixMilli(1680000000000)
	jobSet := &v1alpha2.JobSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "test-jobset",
			Namespace:         "test-ns",
			CreationTimestamp: metav1.NewTime(now),
		},
	}

	tests := []struct {
		name            string
		client          MLRunCreator
		jobSet          *v1alpha2.JobSet
		wantCalls       int
		wantID          string
		wantGroup       string
		wantBucket      string
		wantUpdateCalls int
		wantEmptyURL    bool
	}{
		{
			name:            "nil client",
			client:          nil,
			jobSet:          jobSet,
			wantCalls:       0,
			wantUpdateCalls: 0,
		},
		{
			name:            "nil jobSet",
			client:          &mockMLRunCreator{},
			jobSet:          nil,
			wantCalls:       0,
			wantUpdateCalls: 0,
		},
		{
			name:            "successful creation with correct options",
			client:          &mockMLRunCreator{retRes: map[string]any{"name": "op-123", "url": "https://mlrun/op-123"}},
			jobSet:          jobSet,
			wantCalls:       1,
			wantID:          "c0a5802815479054d7e6d629cf3339211c6f9520580f93a52b8d0e143495b033",
			wantGroup:       "group-abc",
			wantBucket:      "gs://bucket/folder",
			wantUpdateCalls: 1,
		},
		{
			name:            "creation error handled without panic",
			client:          &mockMLRunCreator{retErr: errors.New("api error")},
			jobSet:          jobSet,
			wantCalls:       1,
			wantID:          "c0a5802815479054d7e6d629cf3339211c6f9520580f93a52b8d0e143495b033",
			wantGroup:       "group-abc",
			wantBucket:      "gs://bucket/folder",
			wantUpdateCalls: 0,
			wantEmptyURL:    true,
		},
		{
			name:            "successful creation with zero creation timestamp leaves CreateTime empty",
			client:          &mockMLRunCreator{retRes: map[string]any{"name": "op-123", "url": "https://mlrun/op-123"}},
			jobSet:          &v1alpha2.JobSet{ObjectMeta: metav1.ObjectMeta{Name: "test-jobset", Namespace: "test-ns"}},
			wantCalls:       1,
			wantID:          "99b83bba7686db7f4ed15aebf53cf4ec5b2acc80f44c55c39168f355e2c930ac",
			wantGroup:       "group-abc",
			wantBucket:      "gs://bucket/folder",
			wantUpdateCalls: 1,
		},
		{
			name:            "update failure returns empty url",
			client:          &mockMLRunCreator{retRes: map[string]any{"name": "op-123", "url": "https://mlrun/op-123"}, updateErr: errors.New("update failed")},
			jobSet:          jobSet,
			wantCalls:       1,
			wantID:          "c0a5802815479054d7e6d629cf3339211c6f9520580f93a52b8d0e143495b033",
			wantGroup:       "group-abc",
			wantBucket:      "gs://bucket/folder",
			wantUpdateCalls: 1,
			wantEmptyURL:    true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			mock, ok := tc.client.(*mockMLRunCreator)
			r := &Reconciler{
				Project:     "test-proj",
				Location:    "us-central1",
				ClusterName: "cluster-a",
				MLRunClient: tc.client,
			}
			gotURL := r.ensureMLRunCreated(context.Background(), tc.jobSet, 10, 20, 30, "display-name", "group-abc", "gs://bucket/folder")
			if tc.wantEmptyURL && gotURL != "" {
				t.Errorf("ensureMLRunCreated() = %q, want empty", gotURL)
			}

			if !ok {
				if tc.wantCalls > 0 {
					t.Fatalf("unexpected non-mock client")
				}
				return
			}
			if mock.createCalls != tc.wantCalls {
				t.Errorf("createCalls = %d, want %d", mock.createCalls, tc.wantCalls)
			}
			if tc.wantCalls == 0 {
				return
			}
			if mock.lastID != tc.wantID {
				t.Errorf("lastID = %q, want %q", mock.lastID, tc.wantID)
			}
			if mock.lastPhase != "ACTIVE" {
				t.Errorf("lastPhase = %q, want %q", mock.lastPhase, "ACTIVE")
			}
			if mock.lastOpts.RunGroup != tc.wantGroup {
				t.Errorf("RunGroup = %q, want %q", mock.lastOpts.RunGroup, tc.wantGroup)
			}
			if gotPath := mock.lastOpts.Artifacts["gcs_path"]; gotPath != tc.wantBucket {
				t.Errorf("gcs_path artifact = %q, want %q", gotPath, tc.wantBucket)
			}
			wantCluster := "projects/test-proj/locations/us-central1/clusters/cluster-a"
			if mock.lastOpts.WorkloadDetails.Cluster != wantCluster {
				t.Errorf("WorkloadDetails.Cluster = %q, want %q", mock.lastOpts.WorkloadDetails.Cluster, wantCluster)
			}
			if diff := cmp.Diff(buildMLRunLabels(10, 20), mock.lastOpts.Labels); diff != "" {
				t.Errorf("Labels mismatch (-want +got):\n%s", diff)
			}
			var wantCreateTime string
			if tc.jobSet != nil {
				wantCreateTime = formatCreationTime(tc.jobSet.CreationTimestamp)
			}
			if mock.lastOpts.WorkloadDetails.CreateTime != wantCreateTime {
				t.Errorf("WorkloadDetails.CreateTime = %q, want %q", mock.lastOpts.WorkloadDetails.CreateTime, wantCreateTime)
			}
			if mock.updateCalls != tc.wantUpdateCalls {
				t.Errorf("updateCalls = %d, want %d", mock.updateCalls, tc.wantUpdateCalls)
			}
			if tc.wantUpdateCalls > 0 {
				if mock.lastUpdateID != tc.wantID {
					t.Errorf("lastUpdateID = %q, want %q", mock.lastUpdateID, tc.wantID)
				}
				wantUpdateLabels := map[string]string{listExistingSessionsOnlyLabel: "true"}
				if diff := cmp.Diff(wantUpdateLabels, mock.lastUpdateOpts.Labels); diff != "" {
					t.Errorf("update Labels mismatch (-want +got):\n%s", diff)
				}
			}
		})
	}
}

func TestCheckWorkUnitStatusAndMLRun(t *testing.T) {
	tests := []struct {
		name     string
		storage  ReconcilerStorage
		wantURL  string
		wantSkip bool
	}{
		{
			name:     "nil storage",
			storage:  nil,
			wantURL:  "",
			wantSkip: false,
		},
		{
			name:     "fetch error returns empty and false",
			storage:  &mockStorageUpdater{fetchErr: errors.New("fetch error")},
			wantURL:  "",
			wantSkip: false,
		},
		{
			name: "running execution with mlrun url",
			storage: &mockStorageUpdater{
				fetchRes: &spb.WorkUnitExecution{
					Status: &spb.WorkUnitStatus{State: spb.WorkUnitStatus_RUNNING},
					Metadata: &spb.WorkUnitExecutionMetadata{
						MlrunUrl: "https://mlrun.example.com/123",
					},
				},
			},
			wantURL:  "https://mlrun.example.com/123",
			wantSkip: false,
		},
		{
			name: "terminal status sets skip to true",
			storage: &mockStorageUpdater{
				fetchRes: &spb.WorkUnitExecution{
					Status: &spb.WorkUnitStatus{State: spb.WorkUnitStatus_COMPLETED},
					Metadata: &spb.WorkUnitExecutionMetadata{
						MlrunUrl: "https://mlrun.example.com/123",
					},
				},
			},
			wantURL:  "https://mlrun.example.com/123",
			wantSkip: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r := &Reconciler{Storage: tc.storage}
			gotURL, gotSkip := r.checkWorkUnitStatusAndMLRun(context.Background(), 1, 1, 1)
			if gotURL != tc.wantURL || gotSkip != tc.wantSkip {
				t.Errorf("checkWorkUnitStatusAndMLRun() = (%q, %v), want (%q, %v)", gotURL, gotSkip, tc.wantURL, tc.wantSkip)
			}
		})
	}
}

func TestUpsertExistingMLRun(t *testing.T) {
	// The conflict body does not name a run, so upsert falls back to our own id.
	conflictErr := &hypercomputeclient.APIError{StatusCode: http.StatusConflict, Body: "ALREADY_EXISTS"}
	tests := []struct {
		name            string
		client          *mockMLRunCreator
		wantURLContains string
		wantGetCalls    int
		wantUpdateCalls int
	}{
		{
			name: "already exists, owned and has scan label is idempotent success",
			client: &mockMLRunCreator{getRes: map[string]any{
				"labels": map[string]any{
					"created_by":                  createdByXManagerLabelValue,
					listExistingSessionsOnlyLabel: "true",
				},
			}},
			wantURLContains: "console.cloud.google.com",
			wantGetCalls:    1,
			wantUpdateCalls: 0,
		},
		{
			name: "already exists, owned but missing scan label triggers update",
			client: &mockMLRunCreator{
				getRes: map[string]any{
					"labels": map[string]any{"created_by": createdByXManagerLabelValue},
				},
				updateRes: map[string]any{"url": "https://mlrun/updated"},
			},
			wantURLContains: "https://mlrun/updated",
			wantGetCalls:    1,
			wantUpdateCalls: 1,
		},
		{
			name: "already exists and foreign is updated",
			client: &mockMLRunCreator{
				getRes:    map[string]any{"labels": map[string]any{"created_by": "someone-else"}},
				updateRes: map[string]any{"url": "https://mlrun/updated"},
			},
			wantURLContains: "https://mlrun/updated",
			wantGetCalls:    1,
			wantUpdateCalls: 1,
		},
		{
			name:            "get failure is treated as success via console url",
			client:          &mockMLRunCreator{getErr: errors.New("not found")},
			wantURLContains: "console.cloud.google.com",
			wantGetCalls:    1,
			wantUpdateCalls: 0,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r := &Reconciler{Project: "proj", Location: "us-central1", ClusterName: "clus", MLRunClient: tc.client}
			got := r.upsertExistingMLRun(context.Background(), conflictErr, "xmc-fallback-id", "disp", "grp", buildMLRunLabels(1, 2), map[string]string{"gcs_path": "gs://b/o"})
			if !strings.Contains(got, tc.wantURLContains) {
				t.Errorf("upsertExistingMLRun() = %q, want containing %q", got, tc.wantURLContains)
			}
			if tc.client.getCalls != tc.wantGetCalls {
				t.Errorf("getCalls = %d, want %d", tc.client.getCalls, tc.wantGetCalls)
			}
			if tc.client.updateCalls != tc.wantUpdateCalls {
				t.Errorf("updateCalls = %d, want %d", tc.client.updateCalls, tc.wantUpdateCalls)
			}
		})
	}
}

func TestIsStaleExecutionForMLRun(t *testing.T) {
	tests := []struct {
		name          string
		executionName string
		reconciledEID int64
		want          bool
	}{
		{
			name:          "same eid is not stale",
			executionName: "experiments/1/workUnits/2/executions/3",
			reconciledEID: 3,
			want:          false,
		},
		{
			name:          "different eid is stale",
			executionName: "experiments/1/workUnits/2/executions/3",
			reconciledEID: 4,
			want:          true,
		},
		{
			name:          "unparseable name is not stale",
			executionName: "garbage",
			reconciledEID: 3,
			want:          false,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := isStaleExecutionForMLRun(tc.executionName, tc.reconciledEID); got != tc.want {
				t.Errorf("isStaleExecutionForMLRun(%q, %d) = %v, want %v", tc.executionName, tc.reconciledEID, got, tc.want)
			}
		})
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
