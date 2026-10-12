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
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
	"sigs.k8s.io/controller-runtime/pkg/event"
	v1alpha2 "sigs.k8s.io/jobset/api/jobset/v1alpha2"
	"xmanager_cloud/internal/kubernetes/common/constants"
)

func newTestLabeledJobSet(name string) *v1alpha2.JobSet {
	return &v1alpha2.JobSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: "default",
			Labels: map[string]string{
				constants.ExperimentIDLabel: "123",
				constants.WorkUnitIDLabel:   "1",
				constants.ExecutionIDLabel:  "1",
			},
		},
	}
}

func mutateJobSet(base *v1alpha2.JobSet, fn func(*v1alpha2.JobSet)) *v1alpha2.JobSet {
	js := base.DeepCopy()
	js.ResourceVersion = "2"
	if fn != nil {
		fn(js)
	}
	return js
}

func TestJobSetPredicate_Create(t *testing.T) {
	p := jobSetPredicate(context.Background())
	jobSetWithLabels := newTestLabeledJobSet("jobset-with-labels")
	jobSetWithoutLabels := &v1alpha2.JobSet{
		ObjectMeta: metav1.ObjectMeta{Name: "jobset-without-labels", Namespace: "default"},
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
	if !p.Create(event.TypedCreateEvent[*v1alpha2.JobSet]{Object: jobSetWithLabels}) {
		t.Errorf("Predicate CreateFunc with labels failed: expected true, got false")
	}
	if p.Create(event.TypedCreateEvent[*v1alpha2.JobSet]{Object: jobSetWithoutLabels}) {
		t.Errorf("Predicate CreateFunc without labels failed: expected false, got true")
	}
	if p.Create(event.TypedCreateEvent[*v1alpha2.JobSet]{Object: jobSetWithInvalidLabels}) {
		t.Errorf("Predicate CreateFunc with invalid labels failed: expected false, got true")
	}
}

func TestJobSetPredicate_Delete(t *testing.T) {
	p := jobSetPredicate(context.Background())
	if p.Delete(event.TypedDeleteEvent[*v1alpha2.JobSet]{}) {
		t.Errorf("Predicate DeleteFunc failed: expected false, got true")
	}
}

func TestJobSetPredicate_Generic(t *testing.T) {
	p := jobSetPredicate(context.Background())
	jobSetWithLabels := newTestLabeledJobSet("jobset-with-labels")
	jobSetWithoutLabels := &v1alpha2.JobSet{
		ObjectMeta: metav1.ObjectMeta{Name: "jobset-without-labels", Namespace: "default"},
	}
	if !p.Generic(event.TypedGenericEvent[*v1alpha2.JobSet]{Object: jobSetWithLabels}) {
		t.Errorf("Predicate GenericFunc with labels failed: expected true, got false")
	}
	if p.Generic(event.TypedGenericEvent[*v1alpha2.JobSet]{Object: jobSetWithoutLabels}) {
		t.Errorf("Predicate GenericFunc without labels failed: expected false, got true")
	}
}

func TestJobSetPredicate_NilObject(t *testing.T) {
	p := jobSetPredicate(context.Background())
	jobSetWithLabels := newTestLabeledJobSet("jobset-with-labels")
	if p.Create(event.TypedCreateEvent[*v1alpha2.JobSet]{}) {
		t.Errorf("Predicate CreateFunc with nil object: expected false, got true")
	}
	if p.Generic(event.TypedGenericEvent[*v1alpha2.JobSet]{}) {
		t.Errorf("Predicate GenericFunc with nil object: expected false, got true")
	}
	if p.Update(event.TypedUpdateEvent[*v1alpha2.JobSet]{ObjectNew: jobSetWithLabels}) {
		t.Errorf("Predicate UpdateFunc with nil old object: expected false, got true")
	}
	if p.Update(event.TypedUpdateEvent[*v1alpha2.JobSet]{ObjectOld: jobSetWithLabels}) {
		t.Errorf("Predicate UpdateFunc with nil new object: expected false, got true")
	}
}

func TestJobSetPredicate_Update(t *testing.T) {
	p := jobSetPredicate(context.Background())
	delTime := &metav1.Time{Time: time.Date(2025, time.November, 19, 15, 59, 26, 0, time.UTC)}
	baseJobSet := newTestLabeledJobSet("test-jobset")
	baseJobSet.ResourceVersion = "1"
	baseJobSet.Annotations = map[string]string{"key1": "val1"}
	baseJobSet.Finalizers = []string{constants.JobSetFinalizer}
	baseJobSet.Spec = v1alpha2.JobSetSpec{
		ReplicatedJobs: []v1alpha2.ReplicatedJob{{Name: "worker", Replicas: 2}},
		Suspend:        ptr.To(false),
	}
	baseJobSet.Status = v1alpha2.JobSetStatus{
		Conditions: []metav1.Condition{{Type: "Completed", Status: metav1.ConditionFalse}},
	}

	updateCases := []struct {
		name   string
		oldObj *v1alpha2.JobSet
		newObj *v1alpha2.JobSet
		want   bool
	}{
		{
			name:   "No relevant change",
			oldObj: baseJobSet,
			newObj: mutateJobSet(baseJobSet, func(js *v1alpha2.JobSet) { js.Annotations["key2"] = "val2" }),
			want:   false,
		},
		{
			name:   "DeletionTimestamp set",
			oldObj: baseJobSet,
			newObj: mutateJobSet(baseJobSet, func(js *v1alpha2.JobSet) { js.DeletionTimestamp = delTime }),
			want:   true,
		},
		{
			name:   "DeletionTimestamp already set",
			oldObj: mutateJobSet(baseJobSet, func(js *v1alpha2.JobSet) { js.ResourceVersion = "1"; js.DeletionTimestamp = delTime }),
			newObj: mutateJobSet(baseJobSet, func(js *v1alpha2.JobSet) { js.DeletionTimestamp = delTime }),
			want:   false,
		},
		{
			name:   "Unmanaged JobSet deleted",
			oldObj: mutateJobSet(baseJobSet, func(js *v1alpha2.JobSet) { js.ResourceVersion = "1"; js.Labels = nil }),
			newObj: mutateJobSet(baseJobSet, func(js *v1alpha2.JobSet) { js.Labels = nil; js.DeletionTimestamp = delTime }),
			want:   false,
		},
		{
			name:   "New object without labels",
			oldObj: baseJobSet,
			newObj: mutateJobSet(baseJobSet, func(js *v1alpha2.JobSet) { js.Labels = nil }),
			want:   false,
		},
		{
			name:   "Labels added",
			oldObj: mutateJobSet(baseJobSet, func(js *v1alpha2.JobSet) { js.ResourceVersion = "1"; js.Labels = nil }),
			newObj: mutateJobSet(baseJobSet, nil),
			want:   true,
		},
		{
			name:   "WorkUnitIDLabel changed",
			oldObj: baseJobSet,
			newObj: mutateJobSet(baseJobSet, func(js *v1alpha2.JobSet) { js.Labels[constants.WorkUnitIDLabel] = "2" }),
			want:   true,
		},
		{
			name:   "ExperimentIDLabel changed",
			oldObj: baseJobSet,
			newObj: mutateJobSet(baseJobSet, func(js *v1alpha2.JobSet) { js.Labels[constants.ExperimentIDLabel] = "456" }),
			want:   true,
		},
		{
			name:   "ExecutionIDLabel changed",
			oldObj: baseJobSet,
			newObj: mutateJobSet(baseJobSet, func(js *v1alpha2.JobSet) { js.Labels[constants.ExecutionIDLabel] = "2" }),
			want:   true,
		},
		{
			name:   "Spec.Suspend changed",
			oldObj: baseJobSet,
			newObj: mutateJobSet(baseJobSet, func(js *v1alpha2.JobSet) { js.Spec.Suspend = ptr.To(true) }),
			want:   true,
		},
		{
			name:   "Replicas count changed",
			oldObj: baseJobSet,
			newObj: mutateJobSet(baseJobSet, func(js *v1alpha2.JobSet) { js.Spec.ReplicatedJobs[0].Replicas = 3 }),
			want:   true,
		},
		{
			name:   "Status.Conditions changed",
			oldObj: baseJobSet,
			newObj: mutateJobSet(baseJobSet, func(js *v1alpha2.JobSet) { js.Status.Conditions[0].Status = metav1.ConditionTrue }),
			want:   true,
		},
		{
			name:   "Status.ReplicatedJobsStatus changed",
			oldObj: baseJobSet,
			newObj: mutateJobSet(baseJobSet, func(js *v1alpha2.JobSet) {
				js.Status.ReplicatedJobsStatus = []v1alpha2.ReplicatedJobStatus{{Name: "worker", Ready: 1}}
			}),
			want: true,
		},
		{
			name:   "ResourceVersion is the same",
			oldObj: baseJobSet,
			newObj: mutateJobSet(baseJobSet, func(js *v1alpha2.JobSet) { js.ResourceVersion = "1"; js.Spec.Suspend = ptr.To(true) }),
			want:   false,
		},
	}

	for _, tc := range updateCases {
		t.Run(tc.name, func(t *testing.T) {
			evt := event.TypedUpdateEvent[*v1alpha2.JobSet]{ObjectOld: tc.oldObj, ObjectNew: tc.newObj}
			if got := p.Update(evt); got != tc.want {
				t.Errorf("Predicate UpdateFunc() = %v, want %v", got, tc.want)
			}
		})
	}
}
