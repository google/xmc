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
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
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
	"xmanager_cloud/cmd/reconciler/shared"
	spb "xmanager_cloud/experiment_state_server/proto/messages_go_proto"
	"xmanager_cloud/internal/grpcerr"
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
			name: "missing storage",
			params: Params{
				Client:  cl,
				Scheme:  scheme,
				Batcher: batcher,
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
			reconciler, err := NewReconciler(Params{
				Client:  cl,
				Scheme:  scheme,
				Batcher: batcher,
				Storage: &mockStorageUpdater{},
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
			// The STOPPED write and its batcher enqueue happen before the patch, so
			// they are unaffected by patch failures.
			if gotSent := testutil.ToFloat64(updatesSentTotal.WithLabelValues()); gotSent != 1 {
				t.Errorf("updatesSentTotal = %v, want 1 (STOPPED enqueued before patch)", gotSent)
			}
			if got := batcher.Updates[123][1][shared.JobsetSource].WorkUnitExecution.GetStatus().GetState(); got != spb.WorkUnitStatus_STOPPED {
				t.Errorf("batcher JobsetSource state = %v, want STOPPED", got)
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
		name          string
		jobSet        *v1alpha2.JobSet
		pending       *storagepkg.WorkUnitExecutionUpdate
		updateErr     error
		sinceDeletion time.Duration
		wantErr       bool
		wantDeleted   bool
		wantWrites    []stoppedWrite
		wantBatched   bool
		wantAbandoned map[string]float64
	}{
		{
			name:        "write succeeds before finalizer is removed and is enqueued",
			jobSet:      deletingJobSet(),
			wantDeleted: true,
			wantWrites:  wantStopped,
			wantBatched: true,
		},
		{
			name:        "successful write replaces stale pending batcher update",
			jobSet:      deletingJobSet(),
			pending:     pendingRunning,
			wantDeleted: true,
			wantWrites:  wantStopped,
			wantBatched: true,
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
			wantBatched: true,
		},
		{
			name:       "transient Unavailable keeps finalizer",
			jobSet:     deletingJobSet(),
			updateErr:  fmt.Errorf("spanner: %w", grpcerr.ErrUnavailable),
			wantErr:    true,
			wantWrites: wantStopped,
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
			name:          "transient error past retry budget releases finalizer",
			jobSet:        deletingJobSet(),
			updateErr:     grpcerr.ErrUnavailable,
			sinceDeletion: 16 * time.Minute,
			wantDeleted:   true,
			wantWrites:    wantStopped,
			wantAbandoned: map[string]float64{abandonReasonRetryBudgetExhausted: 1},
		},
		{
			name:          "NotFound releases finalizer",
			jobSet:        deletingJobSet(),
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
			var lastWritten *storagepkg.WorkUnitExecutionUpdate
			store := &mockStorageUpdater{updateErr: tc.updateErr}
			store.onUpdate = func(ctx context.Context, xid int64, updates []*storagepkg.WorkUnitExecutionUpdate) {
				if deadline, ok := ctx.Deadline(); !ok || time.Until(deadline) > stoppedWriteTimeout {
					t.Errorf("storage ctx deadline = %v (set %v), want within %v", deadline, ok, stoppedWriteTimeout)
				}
				var js v1alpha2.JobSet
				if err := cl.Get(context.Background(), key, &js); err != nil {
					t.Errorf("Get(JobSet) at write time failed: %v", err)
				}
				for _, u := range updates {
					lastWritten = u
					gotWrites = append(gotWrites, stoppedWrite{
						XID:               xid,
						WID:               u.WID,
						EID:               u.EID,
						State:             u.WorkUnitExecution.GetStatus().GetState(),
						FinalizersAtWrite: js.Finalizers,
					})
				}
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
			assertStoppedBatched(t, batcher, tc.wantBatched, lastWritten)
		})
	}
}

// assertStoppedBatched checks that the batcher holds exactly the written STOPPED
// update for xid=123/wid=1 (and updatesSentTotal is 1) when wantBatched, or is
// empty (and updatesSentTotal is 0) otherwise.
func assertStoppedBatched(t *testing.T, batcher *shared.UpdateBatcher, wantBatched bool, written *storagepkg.WorkUnitExecutionUpdate) {
	t.Helper()
	wantSent := 0.0
	if wantBatched {
		wantSent = 1
	}
	if got := testutil.ToFloat64(updatesSentTotal.WithLabelValues()); got != wantSent {
		t.Errorf("updatesSentTotal = %v, want %v", got, wantSent)
	}
	batcher.UpdatesMu.Lock()
	defer batcher.UpdatesMu.Unlock()
	if !wantBatched {
		if len(batcher.Updates) != 0 {
			t.Errorf("batcher.Updates = %v, want empty", batcher.Updates)
		}
		return
	}
	got, ok := batcher.Updates[123][1][shared.JobsetSource]
	if !ok || written == nil {
		t.Fatalf("batcher slot present = %v, written = %v; want both", ok, written)
	}
	if diff := cmp.Diff(*written, got, protocmp.Transform()); diff != "" {
		t.Errorf("batcher slot mismatch, want the written STOPPED update (-want +got):\n%s", diff)
	}
}

func TestIsPermanentStorageError(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{name: "nil", err: nil, want: false},
		{name: "wrapped NotFound sentinel", err: fmt.Errorf("lookup: %w", grpcerr.ErrNotFound), want: true},
		{name: "raw InvalidArgument status", err: status.Error(codes.InvalidArgument, "bad"), want: true},
		{name: "FailedPrecondition sentinel", err: grpcerr.ErrFailedPrecondition, want: true},
		{name: "PermissionDenied sentinel", err: grpcerr.ErrPermissionDenied, want: true},
		{name: "Unavailable", err: grpcerr.ErrUnavailable, want: false},
		{name: "Aborted", err: status.Error(codes.Aborted, "retry"), want: false},
		{name: "Unknown status", err: status.Error(codes.Unknown, "?"), want: false},
		{name: "plain error", err: errors.New("boom"), want: false},
		{name: "context deadline", err: context.DeadlineExceeded, want: false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := isPermanentStorageError(tc.err); got != tc.want {
				t.Errorf("isPermanentStorageError(%v) = %v, want %v", tc.err, got, tc.want)
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
