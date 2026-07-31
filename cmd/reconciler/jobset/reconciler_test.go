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
	"net/http"
	"strings"
	"testing"
	"time"

	"golang/cmp/cmp"
	batchv1 "golang/k8s_io/api/v/v0_23/batch/v1/v1"
	corev1 "golang/k8s_io/api/v/v0_23/core/v1/v1"
	k8serrors "golang/k8s_io/apimachinery/v/v0_23/pkg/api/errors/errors"
	"golang/k8s_io/apimachinery/v/v0_23/pkg/runtime/runtime"
	"golang/k8s_io/apimachinery/v/v0_23/pkg/types/types"
	clientgoscheme "golang/k8s_io/client_go/v/v0_23/kubernetes/scheme/scheme"
	"golang/prometheus/client/prometheus/testutil/testutil"
	ctrl "golang/sigs_k8s_io/controller_runtime/v/v0_11/controllerruntime"
	"golang/sigs_k8s_io/controller_runtime/v/v0_11/pkg/client/client"
	"golang/sigs_k8s_io/controller_runtime/v/v0_11/pkg/client/fake/fake"
	"golang/sigs_k8s_io/controller_runtime/v/v0_11/pkg/event/event"
	"xmanager_cloud/kubernetes/reconciler/shared/shared"

	metav1 "golang/k8s_io/apimachinery/v/v0_23/pkg/apis/meta/v1/v1"
	v1alpha2 "sigs.k8s.io/jobset/api/jobset/v1alpha2"
	spb "xmanager_cloud/experiment_state_server/proto/messages_go_proto"
	"xmanager_cloud/kubernetes/common/constants"
	"xmanager_cloud/kubernetes/reconciler/hypercomputeclient/hypercomputeclient"
	storagepkg "xmanager_cloud/storage/storage"
)

func boolPtr(b bool) *bool {
	return &b
}

type mockStorageUpdater struct {
	calls     map[int64][]*storagepkg.WorkUnitExecutionUpdate
	updateErr error
	updateRes map[int64]*spb.WorkUnitExecution
	fetchErr  error
	fetchRes  *spb.WorkUnitExecution

	// listArtifactsPages returns one page per call, in order; every page except
	// the last reports a non-empty nextToken so tests can exercise multi-page
	// pagination. listArtifactsErr, when set, is returned on the first call.
	listArtifactsPages [][]*spb.Artifact
	listArtifactsErr   error
	listArtifactsCalls int

	// listArtifactsStuckToken, when set, makes ListArtifacts always return the same
	// non-advancing cursor (and no artifacts) to exercise the loop's repeating-token
	// guard.
	listArtifactsStuckToken bool
}

func (m *mockStorageUpdater) FetchCurrentWorkUnitExecution(ctx context.Context, xid, wid int64) (*spb.WorkUnitExecution, error) {
	return m.fetchRes, m.fetchErr
}

func (m *mockStorageUpdater) ListArtifacts(ctx context.Context, parent string, pageSize int32, pageToken, filter, orderBy string, staleness time.Duration) ([]*spb.Artifact, string, error) {
	if m.listArtifactsErr != nil {
		return nil, "", m.listArtifactsErr
	}
	idx := m.listArtifactsCalls
	m.listArtifactsCalls++
	if m.listArtifactsStuckToken {
		// Fail fast rather than hang the suite if the loop guard ever regresses.
		if m.listArtifactsCalls > 100 {
			return nil, "", errors.New("repeating-token loop guard failed")
		}
		return nil, "stuck", nil
	}
	if idx >= len(m.listArtifactsPages) {
		return nil, "", nil
	}
	nextToken := ""
	if idx < len(m.listArtifactsPages)-1 {
		nextToken = fmt.Sprintf("page-%d", idx+1)
	}
	return m.listArtifactsPages[idx], nextToken, nil
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

type clientWithError struct {
	client.Client
	listErr  error
	getErr   error
	patchErr error
}

func (c *clientWithError) List(ctx context.Context, list client.ObjectList, opts ...client.ListOption) error {
	if c.listErr != nil {
		return c.listErr
	}
	return c.Client.List(ctx, list, opts...)
}

func (c *clientWithError) Get(ctx context.Context, key client.ObjectKey, obj client.Object) error {
	if c.getErr != nil {
		return c.getErr
	}
	return c.Client.Get(ctx, key, obj)
}

func (c *clientWithError) Patch(ctx context.Context, obj client.Object, patch client.Patch, opts ...client.PatchOption) error {
	if c.patchErr != nil {
		return c.patchErr
	}
	return c.Client.Patch(ctx, obj, patch, opts...)
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
			cl := fake.NewClientBuilder().WithScheme(scheme).WithRuntimeObjects(tc.initialObjs...).Build()
			var reconcilerClient client.Client = cl
			if tc.getErr != nil {
				reconcilerClient = &clientWithError{Client: cl, getErr: tc.getErr}
			}

			reconciler := &Reconciler{
				Client:  reconcilerClient,
				Scheme:  scheme,
				Batcher: shared.NewUpdateBatcher(t.Context(), &shared.BatcherConfig{TickDuration: 250 * time.Millisecond}, &mockStorageUpdater{}),
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

	r := &Reconciler{
		Client:  fakeClient,
		Scheme:  scheme,
		Batcher: shared.NewUpdateBatcher(t.Context(), &shared.BatcherConfig{TickDuration: 250 * time.Millisecond}, &mockStorageUpdater{}),
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

func TestJobSetPredicate_Create(t *testing.T) {
	p := jobSetPredicate(context.Background())
	jobSetWithLabels := &v1alpha2.JobSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "jobset-with-labels",
			Namespace: "default",
			Labels: map[string]string{
				constants.ExperimentIDLabel: "123",
				constants.WorkUnitIDLabel:   "1",
				constants.ExecutionIDLabel:  "1",
			},
		},
	}
	jobSetWithoutLabels := &v1alpha2.JobSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "jobset-without-labels",
			Namespace: "default",
		},
	}
	jobSetWithInvalidLabels := &v1alpha2.JobSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "jobset-with-invalid-labels",
			Namespace: "default",
			Labels: map[string]string{
				constants.ExperimentIDLabel: "abc",
				constants.WorkUnitIDLabel:   "1",
				constants.ExecutionIDLabel:  "1",
			},
		},
	}
	if !p.Create(event.CreateEvent{Object: jobSetWithLabels}) {
		t.Errorf("Predicate CreateFunc with labels failed: expected true, got false")
	}
	if p.Create(event.CreateEvent{Object: jobSetWithoutLabels}) {
		t.Errorf("Predicate CreateFunc without labels failed: expected false, got true")
	}
	if p.Create(event.CreateEvent{Object: jobSetWithInvalidLabels}) {
		t.Errorf("Predicate CreateFunc with invalid labels failed: expected false, got true")
	}
}

func TestJobSetPredicate_Delete(t *testing.T) {
	p := jobSetPredicate(context.Background())
	if p.Delete(event.DeleteEvent{}) {
		t.Errorf("Predicate DeleteFunc failed: expected false, got true")
	}
}

func TestJobSetPredicate_Generic(t *testing.T) {
	p := jobSetPredicate(context.Background())
	jobSetWithLabels := &v1alpha2.JobSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "jobset-with-labels",
			Namespace: "default",
			Labels: map[string]string{
				constants.ExperimentIDLabel: "123",
				constants.WorkUnitIDLabel:   "1",
				constants.ExecutionIDLabel:  "1",
			},
		},
	}
	jobSetWithoutLabels := &v1alpha2.JobSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "jobset-without-labels",
			Namespace: "default",
		},
	}
	if !p.Generic(event.GenericEvent{Object: jobSetWithLabels}) {
		t.Errorf("Predicate GenericFunc with labels failed: expected true, got false")
	}
	if p.Generic(event.GenericEvent{Object: jobSetWithoutLabels}) {
		t.Errorf("Predicate GenericFunc without labels failed: expected false, got true")
	}
}

func TestJobSetPredicate_WrongObjectType(t *testing.T) {
	p := jobSetPredicate(context.Background())
	jobSetWithLabels := &v1alpha2.JobSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "jobset-with-labels",
			Namespace: "default",
			Labels: map[string]string{
				constants.ExperimentIDLabel: "123",
				constants.WorkUnitIDLabel:   "1",
				constants.ExecutionIDLabel:  "1",
			},
		},
	}
	wrongObjectType := &metav1.PartialObjectMetadata{}
	if !p.Create(event.CreateEvent{Object: wrongObjectType}) {
		t.Errorf("Predicate CreateFunc with wrong object type failed: expected true, got false")
	}
	if !p.Generic(event.GenericEvent{Object: wrongObjectType}) {
		t.Errorf("Predicate GenericFunc with wrong object type failed: expected true, got false")
	}
	if !p.Update(event.UpdateEvent{ObjectOld: wrongObjectType, ObjectNew: jobSetWithLabels}) {
		t.Errorf("Predicate UpdateFunc with wrong old object type failed: expected true, got false")
	}
	if !p.Update(event.UpdateEvent{ObjectOld: jobSetWithLabels, ObjectNew: wrongObjectType}) {
		t.Errorf("Predicate UpdateFunc with wrong new object type failed: expected true, got false")
	}
}

func TestJobSetPredicate_Update(t *testing.T) {
	p := jobSetPredicate(context.Background())
	baseJobSet := &v1alpha2.JobSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:            "test-jobset",
			Namespace:       "default",
			ResourceVersion: "1",
			Labels: map[string]string{
				constants.ExperimentIDLabel: "123",
				constants.WorkUnitIDLabel:   "1",
				constants.ExecutionIDLabel:  "1",
			},
			Annotations: map[string]string{
				"key1": "val1",
			},
			Finalizers: []string{constants.JobSetFinalizer},
		},
		Spec: v1alpha2.JobSetSpec{
			ReplicatedJobs: []v1alpha2.ReplicatedJob{
				{Name: "worker", Replicas: 2},
			},
			Suspend: boolPtr(false),
		},
		Status: v1alpha2.JobSetStatus{
			Conditions: []metav1.Condition{
				{Type: "Completed", Status: metav1.ConditionFalse},
			},
		},
	}
	jobSetWithoutLabelsForUpdate := baseJobSet.DeepCopy()
	jobSetWithoutLabelsForUpdate.Labels = nil
	jobSetWithoutLabelsForUpdate.ResourceVersion = "2"

	updateCases := []struct {
		name   string
		oldObj *v1alpha2.JobSet
		newObj *v1alpha2.JobSet
		want   bool
	}{
		{
			name:   "No relevant change",
			oldObj: baseJobSet,
			newObj: func() *v1alpha2.JobSet {
				js := baseJobSet.DeepCopy()
				js.ResourceVersion = "2"
				js.Annotations["key2"] = "val2"
				return js
			}(),
			want: false,
		},
		{
			name:   "DeletionTimestamp set",
			oldObj: baseJobSet,
			newObj: func() *v1alpha2.JobSet {
				js := baseJobSet.DeepCopy()
				js.ResourceVersion = "2"
				js.DeletionTimestamp = &metav1.Time{Time: time.Date(2025, time.November, 19, 15, 59, 26, 0, time.UTC)}
				return js
			}(),
			want: true,
		},
		{
			name: "DeletionTimestamp already set",
			oldObj: func() *v1alpha2.JobSet {
				js := baseJobSet.DeepCopy()
				js.DeletionTimestamp = &metav1.Time{Time: time.Date(2025, time.November, 19, 15, 59, 26, 0, time.UTC)}
				return js
			}(),
			newObj: func() *v1alpha2.JobSet {
				js := baseJobSet.DeepCopy()
				js.DeletionTimestamp = &metav1.Time{Time: time.Date(2025, time.November, 19, 15, 59, 26, 0, time.UTC)}
				js.ResourceVersion = "2"
				return js
			}(),
			want: false,
		},
		{
			name: "Unmanaged JobSet deleted",
			oldObj: func() *v1alpha2.JobSet {
				js := baseJobSet.DeepCopy()
				js.Labels = nil
				return js
			}(),
			newObj: func() *v1alpha2.JobSet {
				js := baseJobSet.DeepCopy()
				js.Labels = nil
				js.ResourceVersion = "2"
				js.DeletionTimestamp = &metav1.Time{Time: time.Date(2025, time.November, 19, 15, 59, 26, 0, time.UTC)}
				return js
			}(),
			want: false,
		},
		{
			name:   "New object without labels",
			oldObj: baseJobSet,
			newObj: jobSetWithoutLabelsForUpdate,
			want:   false,
		},
		{
			name: "Labels added",
			oldObj: func() *v1alpha2.JobSet {
				js := baseJobSet.DeepCopy()
				js.Labels = nil
				return js
			}(),
			newObj: func() *v1alpha2.JobSet {
				js := baseJobSet.DeepCopy()
				js.ResourceVersion = "2"
				return js
			}(),
			want: false,
		},
		{
			name:   "WorkUnitIDLabel changed",
			oldObj: baseJobSet,
			newObj: func() *v1alpha2.JobSet {
				js := baseJobSet.DeepCopy()
				js.ResourceVersion = "2"
				js.Labels[constants.WorkUnitIDLabel] = "2"
				return js
			}(),
			want: true,
		},
		{
			name:   "ExperimentIDLabel changed",
			oldObj: baseJobSet,
			newObj: func() *v1alpha2.JobSet {
				js := baseJobSet.DeepCopy()
				js.ResourceVersion = "2"
				js.Labels[constants.ExperimentIDLabel] = "456"
				return js
			}(),
			want: true,
		},
		{
			name:   "Spec.Suspend changed",
			oldObj: baseJobSet,
			newObj: func() *v1alpha2.JobSet {
				js := baseJobSet.DeepCopy()
				js.ResourceVersion = "2"
				js.Spec.Suspend = boolPtr(true)
				return js
			}(),
			want: true,
		},
		{
			name:   "Replicas count changed",
			oldObj: baseJobSet,
			newObj: func() *v1alpha2.JobSet {
				js := baseJobSet.DeepCopy()
				js.ResourceVersion = "2"
				js.Spec.ReplicatedJobs[0].Replicas = 3
				return js
			}(),
			want: true,
		},
		{
			name:   "Status.Conditions changed",
			oldObj: baseJobSet,
			newObj: func() *v1alpha2.JobSet {
				js := baseJobSet.DeepCopy()
				js.ResourceVersion = "2"
				js.Status.Conditions[0].Status = metav1.ConditionTrue
				return js
			}(),
			want: true,
		},
		{
			name:   "Status.ReplicatedJobsStatus changed",
			oldObj: baseJobSet,
			newObj: func() *v1alpha2.JobSet {
				js := baseJobSet.DeepCopy()
				js.ResourceVersion = "2"
				js.Status.ReplicatedJobsStatus = []v1alpha2.ReplicatedJobStatus{
					{Name: "worker", Ready: 1},
				}
				return js
			}(),
			want: true,
		},
		{
			name:   "ResourceVersion is the same",
			oldObj: baseJobSet,
			newObj: func() *v1alpha2.JobSet {
				js := baseJobSet.DeepCopy()
				// change something that would normally trigger update, but resource version is same
				js.Spec.Suspend = boolPtr(true)
				return js
			}(),
			want: false,
		},
	}

	for _, tc := range updateCases {
		t.Run(tc.name, func(t *testing.T) {
			evt := event.UpdateEvent{
				ObjectOld: tc.oldObj,
				ObjectNew: tc.newObj,
			}
			if got := p.Update(evt); got != tc.want {
				t.Errorf("Predicate UpdateFunc() = %v, want %v", got, tc.want)
			}
		})
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
		name       string
		patchErr   error
		wantResult ctrl.Result
		wantErr    bool
	}{
		{
			name:       "NotFound",
			patchErr:   &k8serrors.StatusError{ErrStatus: metav1.Status{Reason: metav1.StatusReasonNotFound}},
			wantResult: ctrl.Result{},
			wantErr:    false,
		},
		{
			name:       "Conflict",
			patchErr:   &k8serrors.StatusError{ErrStatus: metav1.Status{Reason: metav1.StatusReasonConflict}},
			wantResult: ctrl.Result{Requeue: true},
			wantErr:    false,
		},
		{
			name:       "OtherError",
			patchErr:   errors.New("other error"),
			wantResult: ctrl.Result{},
			wantErr:    true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			initialJobSet := jobSetWithLabelsAndFinalizer.DeepCopy()
			initialJobSet.DeletionTimestamp = &metav1.Time{Time: time.Date(2025, time.November, 19, 15, 59, 26, 0, time.UTC)}
			cl := fake.NewClientBuilder().WithScheme(scheme).WithRuntimeObjects(initialJobSet).Build()
			reconcilerClient := &clientWithError{Client: cl, patchErr: tc.patchErr}
			batcher := shared.NewUpdateBatcher(t.Context(), &shared.BatcherConfig{TickDuration: 250 * time.Millisecond}, &mockStorageUpdater{})
			reconciler := &Reconciler{
				Client:  reconcilerClient,
				Scheme:  scheme,
				Batcher: batcher,
			}

			result, err := reconciler.Reconcile(context.Background(), req)
			if (err != nil) != tc.wantErr {
				t.Errorf("Reconcile() error = %v, wantErr %v", err, tc.wantErr)
			}
			if result != tc.wantResult {
				t.Errorf("Reconcile() result = %v, want %v", result, tc.wantResult)
			}
		})
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
			reconciler := &Reconciler{
				Client:  fakeClient,
				Scheme:  scheme,
				Batcher: batcher,
			}

			result, err := reconciler.Reconcile(context.Background(), tc.req)
			if err != nil {
				t.Fatalf("Reconcile() failed: %v", err)
			}

			if result.Requeue != tc.wantRequeue {
				t.Errorf("Reconcile() requeue = %v, want %v", result.Requeue, tc.wantRequeue)
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
	reconciler := &Reconciler{
		Client:  fakeClient,
		Scheme:  scheme,
		Batcher: batcher,
	}

	result, err := reconciler.Reconcile(context.Background(), reqNoLabels)
	if err != nil {
		t.Fatalf("Reconcile() failed: %v", err)
	}

	if result.Requeue {
		t.Errorf("Reconcile() requeue = %v, want %v", result.Requeue, false)
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
	reconciler := &Reconciler{
		Client:  fakeClient,
		Scheme:  scheme,
		Batcher: batcher,
	}

	result, err := reconciler.Reconcile(context.Background(), req)
	if err != nil {
		t.Fatalf("Reconcile() failed: %v", err)
	}

	if result.Requeue {
		t.Errorf("Reconcile() requeue = %v, want %v", result.Requeue, false)
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

func TestReconcileImagePullBackOff(t *testing.T) {
	scheme, err := newScheme()
	if err != nil {
		t.Fatalf("Failed to create scheme: %v", err)
	}

	jobSet := &v1alpha2.JobSet{
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
			name:      "Starting more than 120s with ImagePullBackOff - FAILED",
			jobSetAge: 130 * time.Second,
			pods: []runtime.Object{
				&corev1.Pod{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "pod-1",
						Namespace: "default",
						Labels: map[string]string{
							"jobset.sigs.k8s.io/jobset-name": "test-jobset",
						},
					},
					Status: corev1.PodStatus{
						ContainerStatuses: []corev1.ContainerStatus{
							{
								State: corev1.ContainerState{
									Waiting: &corev1.ContainerStateWaiting{
										Reason:  "ImagePullBackOff",
										Message: "Back-off pulling image",
									},
								},
							},
						},
					},
				},
			},
			wantRequeue:      false,
			wantState:        spb.WorkUnitStatus_FAILED,
			wantErrorMsgPart: "Failed to pull the container image (ImagePullBackOff)",
		},
		{
			name:      "Starting more than 120s with ErrImagePull - FAILED",
			jobSetAge: 130 * time.Second,
			pods: []runtime.Object{
				&corev1.Pod{
					ObjectMeta: metav1.ObjectMeta{
						Name:      "pod-2",
						Namespace: "default",
						Labels: map[string]string{
							"jobset.sigs.k8s.io/jobset-name": "test-jobset",
						},
					},
					Status: corev1.PodStatus{
						ContainerStatuses: []corev1.ContainerStatus{
							{
								State: corev1.ContainerState{
									Waiting: &corev1.ContainerStateWaiting{
										Reason:  "ErrImagePull",
										Message: "Error pulling image",
									},
								},
							},
						},
					},
				},
			},
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

			objs := []runtime.Object{js}
			objs = append(objs, tc.pods...)

			fakeClient := fake.NewClientBuilder().
				WithScheme(scheme).
				WithRuntimeObjects(objs...).
				WithIndex(&corev1.Pod{}, v1alpha2.JobSetNameKey, func(obj client.Object) []string {
					return []string{obj.(*corev1.Pod).Labels[v1alpha2.JobSetNameKey]}
				}).
				Build()
			batcher := shared.NewUpdateBatcher(t.Context(), &shared.BatcherConfig{TickDuration: 250 * time.Millisecond}, &mockStorageUpdater{})
			r := &Reconciler{
				Client:                      fakeClient,
				Scheme:                      scheme,
				Batcher:                     batcher,
				StartupPodErrorCheckEnabled: true,
				StartupPodErrorCheckTime:    2 * time.Minute,
			}

			req := ctrl.Request{NamespacedName: types.NamespacedName{Name: "test-jobset", Namespace: "default"}}
			res, err := r.Reconcile(context.Background(), req)
			if err != nil {
				t.Fatalf("Reconcile() failed: %v", err)
			}

			if tc.wantRequeue {
				if res.RequeueAfter <= 0 {
					t.Errorf("Expected RequeueAfter > 0, got %v", res.RequeueAfter)
				}
				if res.RequeueAfter > r.StartupPodErrorCheckTime {
					t.Errorf("RequeueAfter %v out of expected range, want < %v", res.RequeueAfter, r.StartupPodErrorCheckTime)
				}
			} else {
				if res.RequeueAfter != 0 {
					t.Errorf("Expected RequeueAfter 0, got %v", res.RequeueAfter)
				}
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
			} else {
				if strings.Contains(msg, "Failed to pull the container image") {
					t.Errorf("Expected message not to contain 'Failed to pull the container image', got %q", msg)
				}
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

	jobSet := &v1alpha2.JobSet{
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
				WithIndex(&corev1.Pod{}, v1alpha2.JobSetNameKey, func(obj client.Object) []string {
					return []string{obj.(*corev1.Pod).Labels[v1alpha2.JobSetNameKey]}
				}).
				Build()
			batcher := shared.NewUpdateBatcher(t.Context(), &shared.BatcherConfig{TickDuration: 250 * time.Millisecond}, &mockStorageUpdater{})
			r := &Reconciler{
				Client:                      fakeClient,
				Scheme:                      scheme,
				Batcher:                     batcher,
				StartupPodErrorCheckEnabled: true,
				StartupPodErrorCheckTime:    2 * time.Minute,
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
			Name: "tpu-node-1",
			Labels: map[string]string{
				constants.TPUTopologySelectorKey: "8x8",
			},
		},
	}
	fakeClient := fake.NewClientBuilder().WithScheme(scheme).WithRuntimeObjects(node).Build()
	r := &Reconciler{
		Client: fakeClient,
	}

	ctx := context.Background()

	t.Run("topology mismatch surfaces detailed warning", func(t *testing.T) {
		pods := []corev1.Pod{
			{
				ObjectMeta: metav1.ObjectMeta{Name: "pod-tpu-pending", Namespace: "default"},
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
						constants.TPUTopologySelectorKey:    "2x2",
						constants.TPUAcceleratorSelectorKey: "tpu-v6e-slice",
					},
				},
			},
		}
		got := r.checkPodsForUnschedulable(ctx, pods)
		want := `Work unit is unschedulable: requested TPU topology "2x2" (type "tpu-v6e-slice") does not match any node pool's advertised topology in the cluster. Available TPU topologies: [8x8]. Please request a topology supported by the cluster's TPU node pools.`
		if got != want {
			t.Errorf("checkPodsForUnschedulable() = %q, want %q", got, want)
		}
	})

	t.Run("topology match falls back to generic warning", func(t *testing.T) {
		pods := []corev1.Pod{
			{
				ObjectMeta: metav1.ObjectMeta{Name: "pod-tpu-pending-quota", Namespace: "default"},
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
						constants.TPUTopologySelectorKey:    "8x8",
						constants.TPUAcceleratorSelectorKey: "tpu-v6e-slice",
					},
				},
			},
		}
		got := r.checkPodsForUnschedulable(ctx, pods)
		if strings.Contains(got, "does not match any node pool's advertised topology") {
			t.Errorf("Expected generic unschedulable warning when topology matches, got %q", got)
		}
	})

	t.Run("empty topologies falls back to generic warning", func(t *testing.T) {
		emptyClient := fake.NewClientBuilder().WithScheme(scheme).Build()
		rEmpty := &Reconciler{Client: emptyClient}
		pods := []corev1.Pod{
			{
				ObjectMeta: metav1.ObjectMeta{Name: "pod-tpu-pending-autoscale", Namespace: "default"},
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
						constants.TPUTopologySelectorKey:    "2x2",
						constants.TPUAcceleratorSelectorKey: "tpu-v6e-slice",
					},
				},
			},
		}
		got := rEmpty.checkPodsForUnschedulable(ctx, pods)
		if strings.Contains(got, "does not match any node pool's advertised topology") {
			t.Errorf("Expected generic unschedulable warning when no TPU nodes are present, got %q", got)
		}
		if !strings.Contains(got, "Work unit may be unschedulable") {
			t.Errorf("Expected generic 'Work unit may be unschedulable' warning, got %q", got)
		}
	})
}

func withManagedXprofEnvs(jobSet *v1alpha2.JobSet) *v1alpha2.JobSet {
	jobSet.Spec.ReplicatedJobs = []v1alpha2.ReplicatedJob{
		{
			Name: "worker",
			Template: batchv1.JobTemplateSpec{
				Spec: batchv1.JobSpec{
					Template: corev1.PodTemplateSpec{
						Spec: corev1.PodSpec{
							Containers: []corev1.Container{
								{
									Env: []corev1.EnvVar{
										{Name: constants.XmcEnvVarManagedXprofDisplayName, Value: "test-display-name"},
										{Name: constants.XmcEnvVarManagedXprofRunGroup, Value: "test-run-group"},
										{Name: constants.XmcEnvVarManagedXprofGCSObjectFolder, Value: "gs://test-bucket/xmc-123-1-timestamp"},
									},
								},
							},
						},
					},
				},
			},
		},
	}
	return jobSet
}

func TestGetManagedXprofEnvs(t *testing.T) {
	tests := []struct {
		name            string
		jobSet          *v1alpha2.JobSet
		wantDisplayName string
		wantRunGroup    string
		wantGCSPath     string
	}{
		{
			name:   "empty jobset",
			jobSet: &v1alpha2.JobSet{},
		},
		{
			name: "no replicated jobs",
			jobSet: &v1alpha2.JobSet{
				Spec: v1alpha2.JobSetSpec{
					ReplicatedJobs: []v1alpha2.ReplicatedJob{},
				},
			},
		},
		{
			name: "no containers",
			jobSet: &v1alpha2.JobSet{
				Spec: v1alpha2.JobSetSpec{
					ReplicatedJobs: []v1alpha2.ReplicatedJob{
						{
							Name: "worker",
							Template: batchv1.JobTemplateSpec{
								Spec: batchv1.JobSpec{
									Template: corev1.PodTemplateSpec{
										Spec: corev1.PodSpec{
											Containers: []corev1.Container{},
										},
									},
								},
							},
						},
					},
				},
			},
		},
		{
			name: "no env vars",
			jobSet: &v1alpha2.JobSet{
				Spec: v1alpha2.JobSetSpec{
					ReplicatedJobs: []v1alpha2.ReplicatedJob{
						{
							Name: "worker",
							Template: batchv1.JobTemplateSpec{
								Spec: batchv1.JobSpec{
									Template: corev1.PodTemplateSpec{
										Spec: corev1.PodSpec{
											Containers: []corev1.Container{
												{},
											},
										},
									},
								},
							},
						},
					},
				},
			},
		},
		{
			name: "irrelevant env vars",
			jobSet: &v1alpha2.JobSet{
				Spec: v1alpha2.JobSetSpec{
					ReplicatedJobs: []v1alpha2.ReplicatedJob{
						{
							Name: "worker",
							Template: batchv1.JobTemplateSpec{
								Spec: batchv1.JobSpec{
									Template: corev1.PodTemplateSpec{
										Spec: corev1.PodSpec{
											Containers: []corev1.Container{
												{
													Env: []corev1.EnvVar{
														{Name: "OTHER", Value: "value"},
													},
												},
											},
										},
									},
								},
							},
						},
					},
				},
			},
		},
		{
			name: "partial env vars - display name only",
			jobSet: &v1alpha2.JobSet{
				Spec: v1alpha2.JobSetSpec{
					ReplicatedJobs: []v1alpha2.ReplicatedJob{
						{
							Name: "worker",
							Template: batchv1.JobTemplateSpec{
								Spec: batchv1.JobSpec{
									Template: corev1.PodTemplateSpec{
										Spec: corev1.PodSpec{
											Containers: []corev1.Container{
												{
													Env: []corev1.EnvVar{
														{Name: constants.XmcEnvVarManagedXprofDisplayName, Value: "test-display-name"},
													},
												},
											},
										},
									},
								},
							},
						},
					},
				},
			},
			wantDisplayName: "test-display-name",
		},
		{
			name:            "all env vars",
			jobSet:          withManagedXprofEnvs(&v1alpha2.JobSet{}),
			wantDisplayName: "test-display-name",
			wantRunGroup:    "test-run-group",
			wantGCSPath:     "gs://test-bucket/xmc-123-1-timestamp",
		},
		{
			name: "multiple containers - early return",
			jobSet: &v1alpha2.JobSet{
				Spec: v1alpha2.JobSetSpec{
					ReplicatedJobs: []v1alpha2.ReplicatedJob{
						{
							Name: "worker",
							Template: batchv1.JobTemplateSpec{
								Spec: batchv1.JobSpec{
									Template: corev1.PodTemplateSpec{
										Spec: corev1.PodSpec{
											Containers: []corev1.Container{
												{
													Env: []corev1.EnvVar{
														{Name: constants.XmcEnvVarManagedXprofDisplayName, Value: "test-display-name"},
													},
												},
												{
													Env: []corev1.EnvVar{
														{Name: constants.XmcEnvVarManagedXprofRunGroup, Value: "test-run-group"},
													},
												},
											},
										},
									},
								},
							},
						},
					},
				},
			},
			wantDisplayName: "test-display-name",
			wantRunGroup:    "",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			gotDisplayName, gotRunGroup, gotGCSPath := getManagedXprofEnvs(tc.jobSet)
			if gotDisplayName != tc.wantDisplayName {
				t.Errorf("getManagedXprofEnvs() gotDisplayName = %q, want %q", gotDisplayName, tc.wantDisplayName)
			}
			if gotRunGroup != tc.wantRunGroup {
				t.Errorf("getManagedXprofEnvs() gotRunGroup = %q, want %q", gotRunGroup, tc.wantRunGroup)
			}
			if gotGCSPath != tc.wantGCSPath {
				t.Errorf("getManagedXprofEnvs() gotGCSPath = %q, want %q", gotGCSPath, tc.wantGCSPath)
			}
		})
	}
}

func TestConstructMLRunID(t *testing.T) {
	creationTime := time.UnixMilli(1680000000000)
	tests := []struct {
		name        string
		project     string
		location    string
		clusterName string
		xid         int64
		wid         int64
		eid         int64
		want        string
	}{
		{
			name:        "normal lowercase fields",
			project:     "proj-a",
			location:    "us-central1",
			clusterName: "cluster-1",
			xid:         123,
			wid:         456,
			eid:         789,
			want:        "xmc-proj-a-us-central1-cluster-1-123-456-789-1680000000000",
		},
		{
			name:        "uppercase and special characters requiring sanitization",
			project:     "My_Project!",
			location:    "US-Central1",
			clusterName: "Cluster#2",
			xid:         1,
			wid:         2,
			eid:         3,
			want:        "xmc-my-project-us-central1-cluster-2-1-2-3-1680000000000",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := ConstructMLRunID(tc.project, tc.location, tc.clusterName, tc.xid, tc.wid, tc.eid, creationTime)
			if got != tc.want {
				t.Errorf("ConstructMLRunID() = %q, want %q", got, tc.want)
			}
		})
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
		name       string
		client     MLRunCreator
		jobSet     *v1alpha2.JobSet
		wantCalls  int
		wantID     string
		wantGroup  string
		wantBucket string
	}{
		{
			name:      "nil client",
			client:    nil,
			jobSet:    jobSet,
			wantCalls: 0,
		},
		{
			name:      "nil jobSet",
			client:    &mockMLRunCreator{},
			jobSet:    nil,
			wantCalls: 0,
		},
		{
			name:       "successful creation with correct options",
			client:     &mockMLRunCreator{retRes: map[string]any{"name": "op-123", "url": "https://mlrun/op-123"}},
			jobSet:     jobSet,
			wantCalls:  1,
			wantID:     "xmc-test-proj-us-central1-cluster-a-10-20-30-1680000000000",
			wantGroup:  "group-abc",
			wantBucket: "gs://bucket/folder",
		},
		{
			name:       "creation error handled without panic",
			client:     &mockMLRunCreator{retErr: errors.New("api error")},
			jobSet:     jobSet,
			wantCalls:  1,
			wantID:     "xmc-test-proj-us-central1-cluster-a-10-20-30-1680000000000",
			wantGroup:  "group-abc",
			wantBucket: "gs://bucket/folder",
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
			_ = r.ensureMLRunCreated(context.Background(), tc.jobSet, 10, 20, 30, "display-name", "group-abc", "gs://bucket/folder")

			if !ok && tc.wantCalls > 0 {
				t.Fatalf("unexpected non-mock client")
			}
			if ok {
				if mock.createCalls != tc.wantCalls {
					t.Errorf("createCalls = %d, want %d", mock.createCalls, tc.wantCalls)
				}
				if tc.wantCalls > 0 {
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

func TestParseExistingMLRunID(t *testing.T) {
	tests := []struct {
		name string
		text string
		want string
	}{
		{name: "no match", text: "some other error", want: ""},
		{name: "match", text: `already exists: projects/p/locations/l/machineLearningRuns/xmc-abc-123`, want: "xmc-abc-123"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := parseExistingMLRunID(tc.text); got != tc.want {
				t.Errorf("parseExistingMLRunID(%q) = %q, want %q", tc.text, got, tc.want)
			}
		})
	}
}

func TestIsMLRunAlreadyExistsError(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{name: "nil", err: nil, want: false},
		{name: "other error", err: errors.New("deadline exceeded"), want: false},
		{name: "http 409 conflict", err: &hypercomputeclient.APIError{StatusCode: http.StatusConflict, Body: "ALREADY_EXISTS"}, want: true},
		{name: "http other status", err: &hypercomputeclient.APIError{StatusCode: http.StatusInternalServerError}, want: false},
		{name: "lro already exists", err: &hypercomputeclient.OperationError{Code: hypercomputeclient.CodeAlreadyExists}, want: true},
		{name: "lro other code", err: &hypercomputeclient.OperationError{Code: 3}, want: false},
		{name: "wrapped http 409", err: fmt.Errorf("create failed: %w", &hypercomputeclient.APIError{StatusCode: http.StatusConflict}), want: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := isMLRunAlreadyExistsError(tc.err); got != tc.want {
				t.Errorf("isMLRunAlreadyExistsError(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}

func TestMLRunOwnedByXManager(t *testing.T) {
	tests := []struct {
		name  string
		mlRun map[string]any
		want  bool
	}{
		{name: "no labels", mlRun: map[string]any{}, want: false},
		{name: "other owner", mlRun: map[string]any{"labels": map[string]any{"created-by": "someone"}}, want: false},
		{name: "owned", mlRun: map[string]any{"labels": map[string]any{"created-by": createdByXManagerLabelValue}}, want: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := mlRunOwnedByXManager(tc.mlRun); got != tc.want {
				t.Errorf("mlRunOwnedByXManager(%v) = %v, want %v", tc.mlRun, got, tc.want)
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
			name:            "already exists and owned is idempotent success",
			client:          &mockMLRunCreator{getRes: map[string]any{"labels": map[string]any{"created-by": createdByXManagerLabelValue}}},
			wantURLContains: "console.cloud.google.com",
			wantGetCalls:    1,
			wantUpdateCalls: 0,
		},
		{
			name: "already exists and foreign is updated",
			client: &mockMLRunCreator{
				getRes:    map[string]any{"labels": map[string]any{"created-by": "someone-else"}},
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

func managedXprofArtifact(displayName, runGroup string) *spb.Artifact {
	return &spb.Artifact{
		Payload: &spb.ArtifactPayload{
			Type: &spb.ArtifactPayload_ManagedXprof{
				ManagedXprof: &spb.ManagedXprofArtifact{DisplayName: displayName, RunGroup: runGroup},
			},
		},
	}
}

// managedXprofWant is the expected (displayName, runGroup) of a ManagedXprof artifact.
type managedXprofWant struct {
	display, runGroup string
}

func TestManagedXprofMisconfigured(t *testing.T) {
	tests := []struct {
		name        string
		displayName string
		runGroup    string
		gcsPath     string
		want        bool
	}{
		{name: "standard jobset all empty", want: false},
		{name: "fully configured", displayName: "d", runGroup: "g", gcsPath: "gs://b", want: false},
		{name: "gcsPath set metadata missing is transient", gcsPath: "gs://b", want: false},
		{name: "gcsPath set only run group missing is transient", displayName: "d", gcsPath: "gs://b", want: false},
		{name: "metadata present but no gcsPath is misconfig", displayName: "d", runGroup: "g", want: true},
		{name: "only display no gcsPath not enough signal", displayName: "d", want: false},
		{name: "only run group no gcsPath not enough signal", runGroup: "g", want: false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := managedXprofMisconfigured(tc.displayName, tc.runGroup, tc.gcsPath); got != tc.want {
				t.Errorf("managedXprofMisconfigured(%q,%q,%q) = %v, want %v", tc.displayName, tc.runGroup, tc.gcsPath, got, tc.want)
			}
		})
	}
}

func TestCollectManagedXprofArtifacts(t *testing.T) {
	tests := []struct {
		name      string
		artifacts []*spb.Artifact
		want      []managedXprofWant
	}{
		{name: "nil list", artifacts: nil},
		{name: "empty list", artifacts: []*spb.Artifact{}},
		{name: "no managed xprof payload", artifacts: []*spb.Artifact{{}}},
		{
			name:      "single",
			artifacts: []*spb.Artifact{managedXprofArtifact("disp", "grp")},
			want:      []managedXprofWant{{"disp", "grp"}},
		},
		{
			name:      "collects all in order, skipping non-managed",
			artifacts: []*spb.Artifact{{}, managedXprofArtifact("d1", "g1"), managedXprofArtifact("d2", "g2")},
			want:      []managedXprofWant{{"d1", "g1"}, {"d2", "g2"}},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := collectManagedXprofArtifacts(tc.artifacts)
			if len(got) != len(tc.want) {
				t.Fatalf("collectManagedXprofArtifacts() returned %d items, want %d", len(got), len(tc.want))
			}
			for i, w := range tc.want {
				if got[i].GetDisplayName() != w.display || got[i].GetRunGroup() != w.runGroup {
					t.Errorf("item %d = (%q,%q), want (%q,%q)", i, got[i].GetDisplayName(), got[i].GetRunGroup(), w.display, w.runGroup)
				}
			}
		})
	}
}

func TestLookupManagedXprofArtifact(t *testing.T) {
	tests := []struct {
		name         string
		storage      ReconcilerStorage
		wantDisplay  string
		wantRunGroup string
		// wantCalls, when > 0, asserts the number of ListArtifacts calls, verifying
		// early termination (stop once found) and the repeating-cursor loop guard.
		wantCalls int
	}{
		{name: "nil storage", storage: nil},
		{name: "list error", storage: &mockStorageUpdater{listArtifactsErr: errors.New("boom")}},
		{name: "no artifact", storage: &mockStorageUpdater{}},
		{
			name:         "artifact present",
			storage:      &mockStorageUpdater{listArtifactsPages: [][]*spb.Artifact{{managedXprofArtifact("disp", "grp")}}},
			wantDisplay:  "disp",
			wantRunGroup: "grp",
		},
		{
			name:         "multiple artifacts uses first",
			storage:      &mockStorageUpdater{listArtifactsPages: [][]*spb.Artifact{{managedXprofArtifact("d1", "g1"), managedXprofArtifact("d2", "g2")}}},
			wantDisplay:  "d1",
			wantRunGroup: "g1",
		},
		{
			name:         "paginates across pages",
			storage:      &mockStorageUpdater{listArtifactsPages: [][]*spb.Artifact{{}, {managedXprofArtifact("paged", "grp")}}},
			wantDisplay:  "paged",
			wantRunGroup: "grp",
		},
		{
			// A match on the first page must short-circuit paging: only the first
			// artifact is used, so paging further is wasted work on the hot path.
			name:         "stops paging once found",
			storage:      &mockStorageUpdater{listArtifactsPages: [][]*spb.Artifact{{managedXprofArtifact("first", "g1")}, {managedXprofArtifact("second", "g2")}}},
			wantDisplay:  "first",
			wantRunGroup: "g1",
			wantCalls:    1,
		},
		{
			// A non-advancing cursor must not loop forever; the guard breaks when
			// nextToken == pageToken. The first call advances "" -> "stuck", the
			// second sees "stuck" == "stuck" and stops.
			name:      "repeating cursor terminates",
			storage:   &mockStorageUpdater{listArtifactsStuckToken: true},
			wantCalls: 2,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r := &Reconciler{Storage: tc.storage}
			gotDisplay, gotRunGroup := r.lookupManagedXprofArtifact(context.Background(), 1, 2)
			if gotDisplay != tc.wantDisplay || gotRunGroup != tc.wantRunGroup {
				t.Errorf("got (%q,%q), want (%q,%q)", gotDisplay, gotRunGroup, tc.wantDisplay, tc.wantRunGroup)
			}
			if tc.wantCalls > 0 {
				m, ok := tc.storage.(*mockStorageUpdater)
				if !ok {
					t.Fatalf("wantCalls set but storage is %T, not *mockStorageUpdater", tc.storage)
				}
				if m.listArtifactsCalls != tc.wantCalls {
					t.Errorf("ListArtifacts called %d times, want %d", m.listArtifactsCalls, tc.wantCalls)
				}
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
