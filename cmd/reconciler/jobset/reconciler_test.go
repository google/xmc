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
	storagepkg "xmanager_cloud/storage/storage"
)

func boolPtr(b bool) *bool {
	return &b
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
}
