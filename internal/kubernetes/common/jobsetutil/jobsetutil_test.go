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
package jobsetutil

import (
	"testing"

	"github.com/google/go-cmp/cmp"
	esspb "github.com/google/xmc/api/protos/experiment"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	v1alpha2 "sigs.k8s.io/jobset/api/jobset/v1alpha2"
)

func TestHasCondition(t *testing.T) {
	testCases := []struct {
		name          string
		jobSet        *v1alpha2.JobSet
		conditionType v1alpha2.JobSetConditionType
		status        metav1.ConditionStatus
		want          bool
	}{
		{
			name: "Condition present and true",
			jobSet: &v1alpha2.JobSet{
				Status: v1alpha2.JobSetStatus{
					Conditions: []metav1.Condition{
						{Type: string(v1alpha2.JobSetCompleted), Status: metav1.ConditionTrue},
					},
				},
			},
			conditionType: v1alpha2.JobSetCompleted,
			status:        metav1.ConditionTrue,
			want:          true,
		},
		{
			name: "Condition present but false",
			jobSet: &v1alpha2.JobSet{
				Status: v1alpha2.JobSetStatus{
					Conditions: []metav1.Condition{
						{Type: string(v1alpha2.JobSetCompleted), Status: metav1.ConditionFalse},
					},
				},
			},
			conditionType: v1alpha2.JobSetCompleted,
			status:        metav1.ConditionTrue,
			want:          false,
		},
		{
			name: "Condition not present",
			jobSet: &v1alpha2.JobSet{
				Status: v1alpha2.JobSetStatus{
					Conditions: []metav1.Condition{},
				},
			},
			conditionType: v1alpha2.JobSetFailed,
			status:        metav1.ConditionTrue,
			want:          false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			got := HasCondition(tc.jobSet, tc.conditionType, tc.status)
			if got != tc.want {
				t.Errorf("HasCondition() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestFindConditionWithType(t *testing.T) {
	tests := []struct {
		name          string
		jobSet        *v1alpha2.JobSet
		conditionType v1alpha2.JobSetConditionType
		want          *metav1.Condition
	}{
		{
			name: "condition type correct and status true",
			jobSet: &v1alpha2.JobSet{
				Status: v1alpha2.JobSetStatus{
					Conditions: []metav1.Condition{
						{Type: "Completed", Status: metav1.ConditionTrue},
					},
				},
			},
			conditionType: "Completed",
			want:          &metav1.Condition{Type: "Completed", Status: metav1.ConditionTrue},
		},
		{
			name: "condition type correct but status false",
			jobSet: &v1alpha2.JobSet{
				Status: v1alpha2.JobSetStatus{
					Conditions: []metav1.Condition{
						{Type: "Completed", Status: metav1.ConditionFalse},
					},
				},
			},
			conditionType: "Completed",
			want:          nil,
		},
		{
			name: "condition type incorrect but status true",
			jobSet: &v1alpha2.JobSet{
				Status: v1alpha2.JobSetStatus{
					Conditions: []metav1.Condition{
						{Type: "Failed", Status: metav1.ConditionTrue},
					},
				},
			},
			conditionType: "Completed",
			want:          nil,
		},
		{
			name: "multiple conditions pick correct one",
			jobSet: &v1alpha2.JobSet{
				Status: v1alpha2.JobSetStatus{
					Conditions: []metav1.Condition{
						{Type: "Failed", Status: metav1.ConditionTrue},
						{Type: "Completed", Status: metav1.ConditionFalse},
						{Type: "Completed", Status: metav1.ConditionTrue, Message: "foo"},
					},
				},
			},
			conditionType: "Completed",
			want:          &metav1.Condition{Type: "Completed", Status: metav1.ConditionTrue, Message: "foo"},
		},
		{
			name: "no conditions",
			jobSet: &v1alpha2.JobSet{
				Status: v1alpha2.JobSetStatus{
					Conditions: []metav1.Condition{},
				},
			},
			conditionType: "Completed",
			want:          nil,
		},
		{
			name: "multiple conditions none matching exactly",
			jobSet: &v1alpha2.JobSet{
				Status: v1alpha2.JobSetStatus{
					Conditions: []metav1.Condition{
						{Type: "Completed", Status: metav1.ConditionFalse},
						{Type: "Failed", Status: metav1.ConditionTrue},
					},
				},
			},
			conditionType: "Completed",
			want:          nil,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := FindConditionWithType(tc.jobSet, tc.conditionType)
			if diff := cmp.Diff(tc.want, got); diff != "" {
				t.Errorf("FindConditionWithType(%v, %q) returned diff (-want +got):\n%s", tc.jobSet, tc.conditionType, diff)
			}
		})
	}
}

func TestPrimaryContainerName(t *testing.T) {
	job := &esspb.KubernetesJob{
		Name: "my-job",
		Spec: &esspb.ExecutableSpec{},
	}
	if got := PrimaryContainerName(job); got != "my-job" {
		t.Errorf("PrimaryContainerName() = %q, want %q", got, "my-job")
	}
	job.Spec.Name = "primary"
	if got := PrimaryContainerName(job); got != "primary" {
		t.Errorf("PrimaryContainerName() = %q, want %q", got, "primary")
	}
}

func TestSidecarContainerName(t *testing.T) {
	job := &esspb.KubernetesJob{
		Name: "my-job",
	}
	sidecar := &esspb.ExecutableSpec{}
	if got := SidecarContainerName(job, sidecar, 0); got != "my-job-sidecar-0" {
		t.Errorf("SidecarContainerName() = %q, want %q", got, "my-job-sidecar-0")
	}
	sidecar.Name = "sidecar-a"
	if got := SidecarContainerName(job, sidecar, 0); got != "sidecar-a" {
		t.Errorf("SidecarContainerName() = %q, want %q", got, "sidecar-a")
	}
}

func TestInitContainerName(t *testing.T) {
	job := &esspb.KubernetesJob{
		Name: "my-job",
	}
	initContainer := &esspb.ExecutableSpec{}
	if got := InitContainerName(job, initContainer, 0); got != "my-job-init-0" {
		t.Errorf("InitContainerName() = %q, want %q", got, "my-job-init-0")
	}
	initContainer.Name = "init-a"
	if got := InitContainerName(job, initContainer, 0); got != "init-a" {
		t.Errorf("InitContainerName() = %q, want %q", got, "init-a")
	}
}
