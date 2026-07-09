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
// Package jobsetutil provides utility functions for working with JobSets.
package jobsetutil

import (
	"fmt"

	metav1 "golang/k8s_io/apimachinery/v/v0_23/pkg/apis/meta/v1/v1"
	v1alpha2 "sigs.k8s.io/jobset/api/jobset/v1alpha2"
	esspb "xmanager_cloud/experiment_state_server/proto/messages_go_proto"
)

// PrimaryContainerName returns the primary container name for a job, defaulting to the job name if unset.
func PrimaryContainerName(job *esspb.KubernetesJob) string {
	if name := job.GetSpec().GetName(); name != "" {
		return name
	}
	return job.GetName()
}

// SidecarContainerName returns the container name for a sidecar at the given index,
// defaulting to "<jobName>-sidecar-<i>" if unset.
func SidecarContainerName(job *esspb.KubernetesJob, sidecar *esspb.ExecutableSpec, idx int) string {
	if name := sidecar.GetName(); name != "" {
		return name
	}
	return fmt.Sprintf("%s-sidecar-%d", job.GetName(), idx)
}

// InitContainerName returns the container name for an init container at the given index,
// defaulting to "<jobName>-init-<i>" if unset.
func InitContainerName(job *esspb.KubernetesJob, initContainer *esspb.ExecutableSpec, idx int) string {
	if name := initContainer.GetName(); name != "" {
		return name
	}
	return fmt.Sprintf("%s-init-%d", job.GetName(), idx)
}

// HasCondition checks if a JobSet has a condition of the given type and status.
func HasCondition(jobSet *v1alpha2.JobSet, conditionType v1alpha2.JobSetConditionType, status metav1.ConditionStatus) bool {
	for _, condition := range jobSet.Status.Conditions {
		if condition.Type == string(conditionType) && condition.Status == status {
			return true
		}
	}
	return false
}

// FindConditionWithType finds a condition of the given type with status True.
func FindConditionWithType(jobSet *v1alpha2.JobSet, conditionType v1alpha2.JobSetConditionType) *metav1.Condition {
	for i := range jobSet.Status.Conditions {
		if jobSet.Status.Conditions[i].Type == string(conditionType) && jobSet.Status.Conditions[i].Status == metav1.ConditionTrue {
			return &jobSet.Status.Conditions[i]
		}
	}
	return nil
}
