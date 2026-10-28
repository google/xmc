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
	"fmt"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"google.golang.org/protobuf/testing/protocmp"
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
	spb "xmanager_cloud/api/protos/experiment"
	"xmanager_cloud/cmd/reconciler/shared"
	"xmanager_cloud/internal/grpcerr"
	"xmanager_cloud/internal/kubernetes/common/constants"
	storagepkg "xmanager_cloud/internal/storage"
	"xmanager_cloud/internal/workunit"
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

	// onUpdate, when set, is invoked at the start of each UpdateWorkUnitExecutions
	// call so tests can observe external state (e.g. finalizers) at write time.
	onUpdate func(ctx context.Context, xid int64, updates []*storagepkg.WorkUnitExecutionUpdate)
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

// UpdateWorkUnitExecutionsWithTransitions records the call like
// UpdateWorkUnitExecutions and reports no committed transitions.
func (m *mockStorageUpdater) UpdateWorkUnitExecutionsWithTransitions(ctx context.Context, xid int64, updates []*storagepkg.WorkUnitExecutionUpdate) (map[int64]*spb.WorkUnitExecution, map[int64]workunit.StateTransition, error) {
	res, err := m.UpdateWorkUnitExecutions(ctx, xid, updates)
	return res, nil, err
}

func (m *mockStorageUpdater) UpdateWorkUnitExecutions(ctx context.Context, xid int64, updates []*storagepkg.WorkUnitExecutionUpdate) (map[int64]*spb.WorkUnitExecution, error) {
	if m.onUpdate != nil {
		m.onUpdate(ctx, xid, updates)
	}
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
	store := &mockStorageUpdater{}

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
				Storage: store,
			},
			wantErr: false,
		},
		{
			name: "missing client",
			params: Params{
				Scheme:  scheme,
				Batcher: batcher,
				Storage: store,
			},
			wantErr: true,
		},
		{
			name: "missing scheme",
			params: Params{
				Client:  cl,
				Batcher: batcher,
				Storage: store,
			},
			wantErr: true,
		},
		{
			name: "missing batcher",
			params: Params{
				Client:  cl,
				Scheme:  scheme,
				Storage: store,
			},
			wantErr: true,
		},
		{
			name: "missing storage is allowed",
			params: Params{
				Client:  cl,
				Scheme:  scheme,
				Batcher: batcher,
			},
			wantErr: false,
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
				Storage: &mockStorageUpdater{},
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
		Storage: &mockStorageUpdater{},
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
			store := &mockStorageUpdater{}
			reconciler, err := NewReconciler(Params{
				Client:  cl,
				Scheme:  scheme,
				Batcher: batcher,
				Storage: store,
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
			// The synchronous STOPPED write happens before the patch, so it is
			// unaffected by patch failures and does not re-enqueue to the batcher.
			if got := len(store.calls[123]); got != 1 {
				t.Fatalf("storage writes for xid 123 = %d, want 1", got)
			}
			if got := store.calls[123][0].WorkUnitExecution.GetStatus().GetState(); got != spb.WorkUnitStatus_STOPPED {
				t.Errorf("written state = %v, want STOPPED", got)
			}
			if len(batcher.Updates) != 0 {
				t.Errorf("batcher.Updates = %v, want empty", batcher.Updates)
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
		Storage: &mockStorageUpdater{},
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

// deletingJobSet returns the baseline JobSet for finalizer tests: being deleted,
// carrying valid XMC labels (xid=123, wid=1, eid=1) and the reconciler finalizer.
func deletingJobSet() *v1alpha2.JobSet {
	return &v1alpha2.JobSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-jobset",
			Namespace: "default",
			Labels: map[string]string{
				constants.ExperimentIDLabel: "123",
				constants.WorkUnitIDLabel:   "1",
				constants.ExecutionIDLabel:  "1",
			},
			Finalizers:        []string{constants.JobSetFinalizer},
			DeletionTimestamp: &metav1.Time{Time: time.Date(2025, time.November, 19, 15, 59, 26, 0, time.UTC)},
		},
	}
}

// stoppedWrite captures the storage-write fields asserted by finalizer tests, plus
// the JobSet finalizers observed at the moment of the write.
type stoppedWrite struct {
	XID, WID, EID     int64
	State             spb.WorkUnitStatus_WorkUnitState
	SystemMessageText string
	FinalizersAtWrite []string
}

func TestHandleJobSetFinalizerRemoval(t *testing.T) {
	scheme, err := newScheme()
	if err != nil {
		t.Fatalf("Failed to create scheme: %v", err)
	}
	key := types.NamespacedName{Name: "test-jobset", Namespace: "default"}
	creationTime := metav1.Time{Time: time.Date(2025, time.November, 19, 15, 59, 26, 0, time.UTC)}
	// A STOPPED write for the baseline labels, made while the finalizer is still present.
	wantStopped := []stoppedWrite{{
		XID:               123,
		WID:               1,
		EID:               1,
		State:             spb.WorkUnitStatus_STOPPED,
		SystemMessageText: "Work unit has been fully stopped.",
		FinalizersAtWrite: []string{constants.JobSetFinalizer},
	}}
	// A stale pending JobsetSource update for the same work unit execution.
	pendingRunning := &storagepkg.WorkUnitExecutionUpdate{
		WID: 1,
		EID: 1,
		WorkUnitExecution: &spb.WorkUnitExecution{
			Status: &spb.WorkUnitStatus{State: spb.WorkUnitStatus_RUNNING},
		},
	}

	tests := []struct {
		name            string
		jobSet          *v1alpha2.JobSet
		pending         *storagepkg.WorkUnitExecutionUpdate
		nilStorage      bool
		updateErr       error
		sinceDeletion   time.Duration
		wantErr         bool
		wantDeleted     bool
		wantWrites      []stoppedWrite
		wantPendingKept bool
		wantAbandoned   map[string]float64
	}{
		{
			name:        "write succeeds before finalizer is removed",
			jobSet:      deletingJobSet(),
			wantDeleted: true,
			wantWrites:  wantStopped,
		},
		{
			name:        "successful write evicts stale pending batcher update",
			jobSet:      deletingJobSet(),
			pending:     pendingRunning,
			wantDeleted: true,
			wantWrites:  wantStopped,
		},
		{
			name: "deleting already-Completed JobSet persists COMPLETED instead of STOPPED",
			jobSet: func() *v1alpha2.JobSet {
				js := deletingJobSet()
				js.Status.Conditions = []metav1.Condition{{
					Type:   string(v1alpha2.JobSetCompleted),
					Status: metav1.ConditionTrue,
				}}
				return js
			}(),
			pending:     pendingRunning,
			wantDeleted: true,
			wantWrites: []stoppedWrite{{
				XID:               123,
				WID:               1,
				EID:               1,
				State:             spb.WorkUnitStatus_COMPLETED,
				SystemMessageText: "Work unit completed successfully.",
				FinalizersAtWrite: []string{constants.JobSetFinalizer},
			}},
		},
		{
			name: "deleting already-Failed JobSet persists FAILED instead of STOPPED",
			jobSet: func() *v1alpha2.JobSet {
				js := deletingJobSet()
				js.Status.Conditions = []metav1.Condition{{
					Type:    string(v1alpha2.JobSetFailed),
					Status:  metav1.ConditionTrue,
					Reason:  "BackoffLimitExceeded",
					Message: "job failed",
				}}
				return js
			}(),
			pending:     pendingRunning,
			wantDeleted: true,
			wantWrites: []stoppedWrite{{
				XID:               123,
				WID:               1,
				EID:               1,
				State:             spb.WorkUnitStatus_FAILED,
				SystemMessageText: "Work unit failed: job failed (Reason: BackoffLimitExceeded)",
				FinalizersAtWrite: []string{constants.JobSetFinalizer},
			}},
		},
		{
			name:        "nil Storage evicts pending batcher update and releases finalizer",
			jobSet:      deletingJobSet(),
			pending:     pendingRunning,
			nilStorage:  true,
			wantDeleted: true,
		},
		{
			name: "nested CreationTimestamp succeeds",
			jobSet: func() *v1alpha2.JobSet {
				js := deletingJobSet()
				js.Spec.ReplicatedJobs = []v1alpha2.ReplicatedJob{{
					Name: "rj1",
					Template: batchv1.JobTemplateSpec{
						ObjectMeta: metav1.ObjectMeta{CreationTimestamp: creationTime},
						Spec: batchv1.JobSpec{Template: corev1.PodTemplateSpec{
							ObjectMeta: metav1.ObjectMeta{CreationTimestamp: creationTime},
						}},
					},
				}}
				return js
			}(),
			wantDeleted: true,
			wantWrites:  wantStopped,
		},
		{
			name:            "transient Unavailable keeps finalizer and pending batcher update",
			jobSet:          deletingJobSet(),
			pending:         pendingRunning,
			updateErr:       fmt.Errorf("spanner: %w", grpcerr.ErrUnavailable),
			wantErr:         true,
			wantWrites:      wantStopped,
			wantPendingKept: true,
		},
		{
			name:       "context deadline keeps finalizer",
			jobSet:     deletingJobSet(),
			updateErr:  context.DeadlineExceeded,
			wantErr:    true,
			wantWrites: wantStopped,
		},
		{
			name:       "unclassified error keeps finalizer",
			jobSet:     deletingJobSet(),
			updateErr:  errors.New("boom"),
			wantErr:    true,
			wantWrites: wantStopped,
		},
		{
			name:          "transient error within retry budget keeps finalizer",
			jobSet:        deletingJobSet(),
			updateErr:     grpcerr.ErrUnavailable,
			sinceDeletion: 14 * time.Minute,
			wantErr:       true,
			wantWrites:    wantStopped,
		},
		{
			name:          "transient error past retry budget evicts pending update and releases finalizer",
			jobSet:        deletingJobSet(),
			pending:       pendingRunning,
			updateErr:     grpcerr.ErrUnavailable,
			sinceDeletion: 16 * time.Minute,
			wantDeleted:   true,
			wantWrites:    wantStopped,
			wantAbandoned: map[string]float64{abandonReasonRetryBudgetExhausted: 1},
		},
		{
			name:          "NotFound evicts pending update and releases finalizer",
			jobSet:        deletingJobSet(),
			pending:       pendingRunning,
			updateErr:     fmt.Errorf("work unit execution 1 not found: %w", grpcerr.ErrNotFound),
			wantDeleted:   true,
			wantWrites:    wantStopped,
			wantAbandoned: map[string]float64{abandonReasonPermanent: 1},
		},
		{
			name:          "FailedPrecondition releases finalizer",
			jobSet:        deletingJobSet(),
			updateErr:     grpcerr.ErrFailedPrecondition,
			wantDeleted:   true,
			wantWrites:    wantStopped,
			wantAbandoned: map[string]float64{abandonReasonPermanent: 1},
		},
		{
			name:          "InvalidArgument releases finalizer",
			jobSet:        deletingJobSet(),
			updateErr:     grpcerr.ErrInvalidArgument,
			wantDeleted:   true,
			wantWrites:    wantStopped,
			wantAbandoned: map[string]float64{abandonReasonPermanent: 1},
		},
		{
			name:          "PermissionDenied releases finalizer",
			jobSet:        deletingJobSet(),
			updateErr:     grpcerr.ErrPermissionDenied,
			wantDeleted:   true,
			wantWrites:    wantStopped,
			wantAbandoned: map[string]float64{abandonReasonPermanent: 1},
		},
		{
			name: "invalid labels skip write and release finalizer",
			jobSet: func() *v1alpha2.JobSet {
				js := deletingJobSet()
				js.Labels = nil
				return js
			}(),
			wantDeleted: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			updatesSentTotal.Reset()
			stoppedWriteAbandonedTotal.Reset()
			cl := fake.NewClientBuilder().WithScheme(scheme).WithRuntimeObjects(tc.jobSet).Build()
			batcher := shared.NewUpdateBatcher(t.Context(), &shared.BatcherConfig{TickDuration: 250 * time.Millisecond}, &mockStorageUpdater{})
			if tc.pending != nil {
				batcher.Add(t.Context(), 123, *tc.pending, shared.JobsetSource)
			}
			var gotWrites []stoppedWrite
			var store ReconcilerStorage
			if !tc.nilStorage {
				mockStore := &mockStorageUpdater{updateErr: tc.updateErr}
				mockStore.onUpdate = func(ctx context.Context, xid int64, updates []*storagepkg.WorkUnitExecutionUpdate) {
					if deadline, ok := ctx.Deadline(); !ok || time.Until(deadline) > stoppedWriteTimeout {
						t.Errorf("storage ctx deadline = %v (set %v), want within %v", deadline, ok, stoppedWriteTimeout)
					}
					var js v1alpha2.JobSet
					if err := cl.Get(context.Background(), key, &js); err != nil {
						t.Errorf("Get(JobSet) at write time failed: %v", err)
					}
					for _, u := range updates {
						gotWrites = append(gotWrites, stoppedWrite{
							XID:               xid,
							WID:               u.WID,
							EID:               u.EID,
							State:             u.WorkUnitExecution.GetStatus().GetState(),
							SystemMessageText: u.WorkUnitExecution.GetStatus().GetSystemMessage().GetUserFacingText(),
							FinalizersAtWrite: js.Finalizers,
						})
					}
				}
				store = mockStore
			}
			reconciler, err := NewReconciler(Params{Client: cl, Scheme: scheme, Batcher: batcher, Storage: store})
			if err != nil {
				t.Fatalf("NewReconciler() unexpected error: %v", err)
			}
			reconciler.now = func() time.Time { return tc.jobSet.DeletionTimestamp.Add(tc.sinceDeletion) }

			result, err := reconciler.Reconcile(context.Background(), ctrl.Request{NamespacedName: key})
			if (err != nil) != tc.wantErr {
				t.Errorf("Reconcile() error = %v, wantErr %v", err, tc.wantErr)
			}
			if !result.IsZero() {
				t.Errorf("Reconcile() result = %+v, want zero result", result)
			}
			if diff := cmp.Diff(tc.wantWrites, gotWrites); diff != "" {
				t.Errorf("storage writes mismatch (-want +got):\n%s", diff)
			}

			var gotJobSet v1alpha2.JobSet
			getErr := cl.Get(context.Background(), key, &gotJobSet)
			if gotDeleted := k8serrors.IsNotFound(getErr); gotDeleted != tc.wantDeleted {
				t.Errorf("JobSet deleted = %v (Get err: %v), want %v", gotDeleted, getErr, tc.wantDeleted)
			}
			if !tc.wantDeleted {
				if diff := cmp.Diff([]string{constants.JobSetFinalizer}, gotJobSet.Finalizers); diff != "" {
					t.Errorf("JobSet finalizers mismatch (-want +got):\n%s", diff)
				}
			}

			for _, reason := range []string{abandonReasonPermanent, abandonReasonRetryBudgetExhausted} {
				if got := testutil.ToFloat64(stoppedWriteAbandonedTotal.WithLabelValues(reason)); got != tc.wantAbandoned[reason] {
					t.Errorf("stoppedWriteAbandonedTotal{reason=%q} = %v, want %v", reason, got, tc.wantAbandoned[reason])
				}
			}
			assertBatcherAfterFinalizer(t, batcher, tc.wantPendingKept, tc.pending)
		})
	}
}

// assertBatcherAfterFinalizer checks that updatesSentTotal is 0 on finalizer
// removal and that the batcher either retains pending (when wantPendingKept) or
// has been emptied via Batcher.Remove.
func assertBatcherAfterFinalizer(t *testing.T, batcher *shared.UpdateBatcher, wantPendingKept bool, pending *storagepkg.WorkUnitExecutionUpdate) {
	t.Helper()
	if got := testutil.ToFloat64(updatesSentTotal.WithLabelValues()); got != 0 {
		t.Errorf("updatesSentTotal = %v, want 0", got)
	}
	batcher.UpdatesMu.Lock()
	defer batcher.UpdatesMu.Unlock()
	if !wantPendingKept {
		if len(batcher.Updates) != 0 {
			t.Errorf("batcher.Updates = %v, want empty", batcher.Updates)
		}
		return
	}
	got, ok := batcher.Updates[123][1][shared.JobsetSource]
	if !ok || pending == nil {
		t.Fatalf("batcher slot present = %v, pending = %v; want both", ok, pending)
	}
	if diff := cmp.Diff(*pending, got.WorkUnitExecutionUpdate, protocmp.Transform()); diff != "" {
		t.Errorf("batcher slot mismatch (-want +got):\n%s", diff)
	}
}

func TestStoppedWriteAbandonReason_NilClockFallsBackToTimeNow(t *testing.T) {
	r := &Reconciler{} // Constructed directly, so the injected clock is nil.
	tests := []struct {
		name         string
		deletedSince time.Duration
		want         string
	}{
		{name: "within retry budget", deletedSince: time.Minute, want: ""},
		{name: "past retry budget", deletedSince: maxStoppedWriteRetryAge + time.Minute, want: abandonReasonRetryBudgetExhausted},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			deletedAt := metav1.NewTime(time.Now().Add(-tc.deletedSince))
			jobSet := &v1alpha2.JobSet{ObjectMeta: metav1.ObjectMeta{DeletionTimestamp: &deletedAt}}
			if got := r.stoppedWriteAbandonReason(jobSet, grpcerr.ErrUnavailable); got != tc.want {
				t.Errorf("stoppedWriteAbandonReason() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestActuatorReconcilerFinalizer_WithoutXMCFinalizerSkipsStoppedWrite(t *testing.T) {
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
	store := &mockStorageUpdater{}
	reconciler, err := NewReconciler(Params{
		Client:  fakeClient,
		Scheme:  scheme,
		Batcher: batcher,
		Storage: store,
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
	if len(store.calls) != 0 {
		t.Errorf("Expected no storage writes, got %v", store.calls)
	}
}

func TestMetricsRegisteredOnManagerRegistry(t *testing.T) {
	// Label-less vectors only appear in Gather once a child exists.
	reconcileTotal.WithLabelValues()
	reconcileErrorsTotal.WithLabelValues()
	reconcileDuration.WithLabelValues()
	updatesSentTotal.WithLabelValues()
	stoppedWriteAbandonedTotal.WithLabelValues(abandonReasonPermanent)
	mfs, err := ctrlmetrics.Registry.Gather()
	if err != nil {
		t.Fatalf("ctrlmetrics.Registry.Gather() failed: %v", err)
	}
	registered := make(map[string]bool, len(mfs))
	for _, mf := range mfs {
		registered[mf.GetName()] = true
	}
	for _, name := range []string{"jobset_reconcile_total", "jobset_reconcile_errors_total", "jobset_reconcile_duration_milliseconds", "jobset_updates_sent_to_batcher_total", "jobset_stopped_write_abandoned_total", "jobset_reconciler_mlrun_sync_duration_seconds"} {
		if !registered[name] {
			t.Errorf("metric family %q not registered on ctrlmetrics.Registry", name)
		}
	}
}

// notifyWhenJobSet returns a running JobSet for notify-when tracking tests.
func notifyWhenJobSet(uid types.UID, eid, notifyWhen string) *v1alpha2.JobSet {
	js := &v1alpha2.JobSet{
		ObjectMeta: metav1.ObjectMeta{
			Namespace: "default",
			Name:      "xmc-operation-10-20",
			UID:       uid,
			Labels:    map[string]string{constants.ExecutionIDLabel: eid},
		},
		Spec: v1alpha2.JobSetSpec{
			ReplicatedJobs: []v1alpha2.ReplicatedJob{{Replicas: 1}},
		},
		Status: v1alpha2.JobSetStatus{
			ReplicatedJobsStatus: []v1alpha2.ReplicatedJobStatus{{Ready: 1, Active: 1}},
		},
	}
	if notifyWhen != "" {
		js.Annotations = map[string]string{constants.NotifyWhenAnnotation: notifyWhen}
	}
	return js
}

func TestCheckNotifyWhenUpdate(t *testing.T) {
	tests := []struct {
		name string
		// seq is observed in order; want holds the result for each observation.
		seq  []*v1alpha2.JobSet
		want []bool
	}{
		{
			name: "annotation newly added to running JobSet",
			seq: []*v1alpha2.JobSet{
				notifyWhenJobSet("uid-1", "30", ""),
				notifyWhenJobSet("uid-1", "30", "STARTING.RUNNING"),
			},
			want: []bool{false, true},
		},
		{
			name: "unchanged annotation",
			seq: []*v1alpha2.JobSet{
				notifyWhenJobSet("uid-1", "30", "STARTING.RUNNING"),
				notifyWhenJobSet("uid-1", "30", "STARTING.RUNNING"),
			},
			want: []bool{true, false},
		},
		{
			name: "annotation changed without adding current state",
			seq: []*v1alpha2.JobSet{
				notifyWhenJobSet("uid-1", "30", "RUNNING"),
				notifyWhenJobSet("uid-1", "30", "RUNNING.COMPLETED"),
			},
			want: []bool{true, false},
		},
		{
			name: "first-seen annotation not matching current state",
			seq: []*v1alpha2.JobSet{
				notifyWhenJobSet("uid-1", "30", "COMPLETED"),
			},
			want: []bool{false},
		},
		{
			name: "same name different UID does not reuse state",
			seq: []*v1alpha2.JobSet{
				notifyWhenJobSet("uid-1", "30", "STARTING.RUNNING"),
				notifyWhenJobSet("uid-2", "30", "STARTING.RUNNING"),
			},
			want: []bool{true, true},
		},
		{
			name: "same UID different EID does not reuse state",
			seq: []*v1alpha2.JobSet{
				notifyWhenJobSet("uid-1", "30", "STARTING.RUNNING"),
				notifyWhenJobSet("uid-1", "31", "STARTING.RUNNING"),
			},
			want: []bool{true, true},
		},
		{
			name: "nil JobSet",
			seq:  []*v1alpha2.JobSet{nil},
			want: []bool{false},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r := &Reconciler{}
			got := make([]bool, 0, len(tc.seq))
			for _, js := range tc.seq {
				got = append(got, r.checkNotifyWhenUpdate(js))
			}
			if diff := cmp.Diff(tc.want, got); diff != "" {
				t.Errorf("checkNotifyWhenUpdate() results mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestHandleJobSetFinalizerRemoval_ClearsNotifyWhenTracking(t *testing.T) {
	r := &Reconciler{}
	js := notifyWhenJobSet("uid-1", "30", "STARTING.RUNNING")
	r.checkNotifyWhenUpdate(js)
	js.DeletionTimestamp = &metav1.Time{Time: time.Now()}

	if _, err := r.handleJobSetFinalizerRemoval(t.Context(), js); err != nil {
		t.Fatalf("handleJobSetFinalizerRemoval() error = %v", err)
	}

	if _, ok := r.lastSeenNotifyWhen.Load(newNotifyWhenKey(js)); ok {
		t.Errorf("notify-when tracking entry still present after finalizer removal")
	}
}

func TestSyncJobSetStatus_ArtifactLifecycleFlagCoexistence(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = v1alpha2.AddToScheme(scheme)

	makeJobSet := func() *v1alpha2.JobSet {
		js := withManagedXprofEnvs(&v1alpha2.JobSet{
			ObjectMeta: metav1.ObjectMeta{
				Namespace: "default",
				Name:      "test-jobset",
				Labels: map[string]string{
					constants.ExperimentIDLabel: "10",
					constants.WorkUnitIDLabel:   "20",
					constants.ExecutionIDLabel:  "30",
				},
				Annotations: map[string]string{
					constants.NotifyWhenAnnotation: "STARTING.RUNNING",
				},
			},
		})
		js.Spec.ReplicatedJobs[0].Replicas = 1
		js.Status = v1alpha2.JobSetStatus{
			ReplicatedJobsStatus: []v1alpha2.ReplicatedJobStatus{{Ready: 1, Active: 1}},
		}
		return js
	}

	t.Run("flag false calls legacy syncMLRun and skips ForceNotify", func(t *testing.T) {
		js := makeJobSet()
		fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(js).Build()
		store := &mockStorageUpdater{
			fetchRes: &spb.WorkUnitExecution{Status: &spb.WorkUnitStatus{State: spb.WorkUnitStatus_RUNNING}},
		}
		batcher := shared.NewUpdateBatcher(t.Context(), &shared.BatcherConfig{TickDuration: time.Hour, MaxParallelExperiments: 1}, store)
		mlRun := &mockMLRunCreator{retRes: map[string]any{"name": "projects/p/locations/l/machineLearningRuns/run-1"}}

		r, err := NewReconciler(Params{
			Client:                   fakeClient,
			Scheme:                   scheme,
			Batcher:                  batcher,
			Project:                  "test-proj",
			Location:                 "us-central1",
			ClusterName:              "test-cluster",
			Storage:                  store,
			MLRunClient:              mlRun,
			ArtifactLifecycleEnabled: false,
		})
		if err != nil {
			t.Fatalf("NewReconciler() error = %v", err)
		}
		if _, err := r.syncJobSetStatus(t.Context(), js); err != nil {
			t.Fatalf("syncJobSetStatus() error = %v", err)
		}
		if mlRun.createCalls != 1 {
			t.Errorf("legacy syncMLRun createCalls = %d, want 1", mlRun.createCalls)
		}
	})

	t.Run("flag true skips legacy syncMLRun and evaluates checkNotifyWhenUpdate", func(t *testing.T) {
		js := makeJobSet()
		fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithObjects(js).Build()
		store := &mockStorageUpdater{}
		batcher := shared.NewUpdateBatcher(t.Context(), &shared.BatcherConfig{TickDuration: time.Hour, MaxParallelExperiments: 1}, store)
		mlRun := &mockMLRunCreator{}

		r, err := NewReconciler(Params{
			Client:                   fakeClient,
			Scheme:                   scheme,
			Batcher:                  batcher,
			Project:                  "test-proj",
			Location:                 "us-central1",
			ClusterName:              "test-cluster",
			Storage:                  store,
			MLRunClient:              mlRun,
			ArtifactLifecycleEnabled: true,
		})
		if err != nil {
			t.Fatalf("NewReconciler() error = %v", err)
		}
		if _, err := r.syncJobSetStatus(t.Context(), js); err != nil {
			t.Fatalf("syncJobSetStatus() error = %v", err)
		}
		if mlRun.createCalls != 0 {
			t.Errorf("legacy syncMLRun createCalls = %d, want 0 when ArtifactLifecycleEnabled is true", mlRun.createCalls)
		}
		got := batcher.Updates[10][20][shared.JobsetSource].Hints
		if gotName := got.RuntimeContext.GetJobSetName(); gotName != "test-jobset" {
			t.Errorf("pending hints RuntimeContext.JobSetName = %q, want %q", gotName, "test-jobset")
		}
		got.RuntimeContext = nil // Covered by TestExtractJobSetRuntimeContext.
		want := shared.LifecycleHints{NotifyWhen: "STARTING.RUNNING", ForceNotify: true}
		if diff := cmp.Diff(want, got); diff != "" {
			t.Errorf("pending lifecycle hints mismatch (-want +got):\n%s", diff)
		}
	})
}
