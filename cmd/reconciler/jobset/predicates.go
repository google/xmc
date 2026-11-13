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

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	v1alpha2 "sigs.k8s.io/jobset/api/jobset/v1alpha2"
	"xmanager_cloud/internal/kubernetes/common/constants"
	"xmanager_cloud/internal/kubernetes/common/labels"
	"xmanager_cloud/internal/logging"
)

func hasValidXMCLabels(ctx context.Context, jobSet *v1alpha2.JobSet) bool {
	if jobSet == nil {
		return false
	}
	ctx = logging.WithJobSetName(ctx, jobSet.Name, nil)

	_, err := labels.ParseXMCLabels(jobSet.Labels)
	if err != nil {
		if errors.Is(err, labels.ErrMissingXMCLabels) {
			logging.Warn(ctx, "JobSet is missing XMC labels and will be skipped", "error", err)
		} else {
			logging.Error(ctx, "JobSet has invalid XMC labels and will be skipped", "error", err)
		}
		return false
	}
	return true
}

func jobSetPredicate(ctx context.Context) predicate.TypedPredicate[*v1alpha2.JobSet] {
	return predicate.TypedFuncs[*v1alpha2.JobSet]{
		CreateFunc: func(e event.TypedCreateEvent[*v1alpha2.JobSet]) bool {
			return hasValidXMCLabels(logging.Strip(ctx), e.Object)
		},

		DeleteFunc: func(event.TypedDeleteEvent[*v1alpha2.JobSet]) bool {
			return false
		},

		UpdateFunc: func(e event.TypedUpdateEvent[*v1alpha2.JobSet]) bool {
			return shouldProcessJobSetUpdate(ctx, e.ObjectOld, e.ObjectNew)
		},

		// Reconcile existing resources on controller startup.
		GenericFunc: func(e event.TypedGenericEvent[*v1alpha2.JobSet]) bool {
			return hasValidXMCLabels(logging.Strip(ctx), e.Object)
		},
	}
}

func shouldProcessJobSetUpdate(ctx context.Context, oldJobSet, newJobSet *v1alpha2.JobSet) bool {
	if oldJobSet == nil || newJobSet == nil {
		return false
	}
	if oldJobSet.GetResourceVersion() == newJobSet.GetResourceVersion() {
		return false
	}
	if !jobSetChanged(oldJobSet, newJobSet) {
		return false
	}

	ctx = logging.Strip(ctx)
	if !hasValidXMCLabels(ctx, newJobSet) {
		return false
	}

	if oldJobSet.GetDeletionTimestamp() == nil && newJobSet.GetDeletionTimestamp() != nil {
		return true
	}

	return newJobSet.GetDeletionTimestamp() == nil
}

// jobSetChanged reports whether any field the reconciler acts on differs
// between oldJobSet and newJobSet: spec (generation, suspension, replica
// count), XMC identity labels, status, or deletion state.
func jobSetChanged(oldJobSet, newJobSet *v1alpha2.JobSet) bool {
	generationSame := oldJobSet.Generation == newJobSet.Generation
	labelsSame := oldJobSet.Labels[constants.WorkUnitIDLabel] == newJobSet.Labels[constants.WorkUnitIDLabel] &&
		oldJobSet.Labels[constants.ExperimentIDLabel] == newJobSet.Labels[constants.ExperimentIDLabel] &&
		oldJobSet.Labels[constants.ExecutionIDLabel] == newJobSet.Labels[constants.ExecutionIDLabel]
	statusSame := isJobSetStatusEqual(&oldJobSet.Status, &newJobSet.Status)
	deletionSame := (oldJobSet.GetDeletionTimestamp() == nil) == (newJobSet.GetDeletionTimestamp() == nil)

	oldSus := oldJobSet.Spec.Suspend != nil && *oldJobSet.Spec.Suspend
	newSus := newJobSet.Spec.Suspend != nil && *newJobSet.Spec.Suspend
	specSame := generationSame && oldSus == newSus && calculateTotalReplicas(oldJobSet) == calculateTotalReplicas(newJobSet)

	return !(specSame && labelsSame && statusSame && deletionSame)
}

func isJobSetStatusEqual(oldStatus, newStatus *v1alpha2.JobSetStatus) bool {
	if oldStatus.Restarts != newStatus.Restarts {
		return false
	}
	if !isJobSetConditionsEqual(oldStatus.Conditions, newStatus.Conditions) {
		return false
	}
	return isReplicatedJobsStatusEqual(oldStatus.ReplicatedJobsStatus, newStatus.ReplicatedJobsStatus)
}

// TODO: Compare conditions order-independently by Type rather than index-by-index.
func isJobSetConditionsEqual(oldConds, newConds []metav1.Condition) bool {
	if len(oldConds) != len(newConds) {
		return false
	}
	for i := range oldConds {
		if oldConds[i].Type != newConds[i].Type ||
			oldConds[i].Status != newConds[i].Status ||
			oldConds[i].Reason != newConds[i].Reason ||
			oldConds[i].Message != newConds[i].Message {
			return false
		}
	}
	return true
}

func isReplicatedJobsStatusEqual(oldJobs, newJobs []v1alpha2.ReplicatedJobStatus) bool {
	if len(oldJobs) != len(newJobs) {
		return false
	}
	for i := range oldJobs {
		if oldJobs[i].Name != newJobs[i].Name ||
			oldJobs[i].Ready != newJobs[i].Ready ||
			oldJobs[i].Succeeded != newJobs[i].Succeeded ||
			oldJobs[i].Failed != newJobs[i].Failed ||
			oldJobs[i].Active != newJobs[i].Active ||
			oldJobs[i].Suspended != newJobs[i].Suspended {
			return false
		}
	}
	return true
}
