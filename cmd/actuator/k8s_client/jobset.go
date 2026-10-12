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
package k8sclient

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/xmc/internal/logging"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/wait"

	jobsetv1alpha2 "sigs.k8s.io/jobset/api/jobset/v1alpha2"

	esspb "github.com/google/xmc/api/protos/experiment"
)

const (
	kueueQueueLabel         = "kueue.x-k8s.io/queue-name"
	kueuePriorityClassLabel = "kueue.x-k8s.io/priority-class"
	// defaultTTLSeconds is the default time-to-live for JobSet and Jobs after finishing.
	defaultTTLSeconds = 7 * 24 * 60 * 60 // 7 days
)

var (
	// JobSetGVR is the GroupVersionResource for the JobSet custom resource.
	JobSetGVR = schema.GroupVersionResource{
		Group:    "jobset.x-k8s.io",
		Version:  "v1alpha2",
		Resource: "jobsets",
	}
	// KueueWorkloadGVR is the GroupVersionResource for the Kueue Workload custom resource.
	KueueWorkloadGVR = schema.GroupVersionResource{
		Group:    "kueue.x-k8s.io",
		Version:  "v1beta1",
		Resource: "workloads",
	}
)

func (c *Client) createJobSet(ctx context.Context, workUnit *esspb.WorkUnit, target, author string, operationTime time.Time, priorities Priorities) error {
	ctx = logging.WithWorkUnitExecutionName(ctx, target)
	namespace := getNamespaceFromWorkUnit(workUnit)

	jobSet, err := c.constructJobSet(ctx, workUnit, target, author, namespace, operationTime, priorities)
	if err != nil {
		logging.Warn(ctx, "Failed to construct JobSet manifest", "error", err)
		return fmt.Errorf("failed to construct JobSet manifest: %w", err)
	}
	name := jobSet.GetName()
	logging.Info(ctx, "Create JobSet", "jobSet", name)
	ctx = logging.WithJobSetName(ctx, name, nil)

	jobSetUnstructured, err := c.prepareAndValidateUnstructuredJobSet(ctx, jobSet, target)
	if err != nil {
		return err
	}
	if err := c.ensureNamespaceExists(ctx, namespace); err != nil {
		logging.Error(ctx, "Failed to ensure namespace exists", "namespace", namespace, "error", err)
		return fmt.Errorf("failed to ensure namespace %q exists: %w", namespace, err)
	}
	if _, err := c.dynamicClient.Resource(JobSetGVR).Namespace(namespace).Create(ctx, jobSetUnstructured, metav1.CreateOptions{}); err != nil {
		logging.Warn(ctx, "Failed to create JobSet", "error", err)
		return fmt.Errorf("failed to create JobSet %q in Kubernetes: %w", name, err)
	}
	logging.Info(ctx, "Created JobSet", "name", name, "namespace", namespace)
	return nil
}

func (c *Client) prepareAndValidateUnstructuredJobSet(ctx context.Context, jobSet *jobsetv1alpha2.JobSet, target string) (*unstructured.Unstructured, error) {
	jobSetUnstructured, err := jobSetToUnstructured(jobSet)
	if err != nil {
		logging.Warn(ctx, "Failed to convert JobSet to unstructured", "error", err)
		return nil, fmt.Errorf("failed to convert JobSet to unstructured: %w", err)
	}
	if err := c.preconditionsValidator.ValidateJobSetManifest(jobSetUnstructured); err != nil {
		logging.Warn(ctx, "Failed to validate JobSet manifest", "target", target, "error", err)
		return nil, fmt.Errorf("failed to validate JobSet manifest for %q: %w", target, err)
	}
	return jobSetUnstructured, nil
}

func (c *Client) deleteJobSet(ctx context.Context, target, namespace string, operationTime time.Time) (bool, error) {
	ctx = logging.WithWorkUnitExecutionName(ctx, target)

	jobSetUnstructured, err := c.getJobSetByTargetAndNamespace(ctx, target, namespace)
	if err != nil {
		return false, err
	}
	if jobSetUnstructured == nil {
		logging.Warn(ctx, "JobSet not found for target, skipping deletion")
		return false, nil
	}

	jobSetName := jobSetUnstructured.GetName()
	ctx = logging.WithJobSetName(ctx, jobSetName, jobSetUnstructured.GetLabels())
	logging.Info(ctx, "Delete JobSet", "namespace", namespace, "jobSetName", jobSetName)

	shouldDelete, err := c.validateJobSetDeletion(ctx, target, operationTime, jobSetUnstructured)
	if err != nil || !shouldDelete {
		return false, err
	}
	return c.executeAndWaitJobSetDeletion(ctx, namespace, jobSetName, jobSetUnstructured.GetUID())
}

func (c *Client) validateJobSetDeletion(ctx context.Context, target string, operationTime time.Time, jobSetUnstructured *unstructured.Unstructured) (bool, error) {
	valid, err := c.preconditionsValidator.IsActuationRequestValid(ctx, target, operationTime, jobSetUnstructured)
	if err != nil {
		logging.Warn(ctx, "Failed to validate deleteJobSet request", "error", err)
		return false, err
	}
	if !valid {
		logging.Warn(ctx, "Discarding delete request")
		return false, nil
	}
	aligned, err := c.preconditionsValidator.IsExecutionIDAligned(ctx, target, jobSetUnstructured)
	if err != nil {
		logging.Warn(ctx, "Failed during execution ID alignment check", "error", err, "target", target)
		return false, fmt.Errorf("failed during execution ID alignment check for %q: %w", target, err)
	}
	if !aligned {
		logging.Warn(ctx, "Jobset EID does not match target, skipping deletion")
		return false, nil
	}
	return true, nil
}

func (c *Client) executeAndWaitJobSetDeletion(ctx context.Context, namespace, jobSetName string, uid types.UID) (bool, error) {
	// Set the UID in the options, so we make sure we ONLY delete the Jobset for the current target
	// and not any other Jobset that may have been created in the meantime.
	deleteOptions := metav1.DeleteOptions{
		Preconditions: &metav1.Preconditions{UID: &uid},
	}
	if err := c.dynamicClient.Resource(JobSetGVR).Namespace(namespace).Delete(ctx, jobSetName, deleteOptions); err != nil {
		logging.Warn(ctx, "Failed to delete JobSet", "namespace", namespace, "error", err)
		if apierrors.IsNotFound(err) {
			logging.Warn(ctx, "JobSet %s not found, assuming it was already deleted", jobSetName)
			return false, nil
		}
		return false, fmt.Errorf("failed to delete JobSet %q in namespace %q: %w", jobSetName, namespace, err)
	}
	logging.Debug(ctx, "Successfully initiated deletion of JobSet", "namespace", namespace)
	if err := c.waitForJobSetDeletion(ctx, namespace, jobSetName); err != nil {
		return false, err
	}
	logging.Info(ctx, "Deleted JobSet", "namespace", namespace, "jobSetName", jobSetName)
	return true, nil
}

func (c *Client) waitForJobSetDeletion(ctx context.Context, namespace, jobSetName string) error {
	err := wait.PollImmediate(2*time.Second, 30*time.Second, func() (bool, error) {
		_, err := c.dynamicClient.Resource(JobSetGVR).Namespace(namespace).Get(ctx, jobSetName, metav1.GetOptions{})
		if apierrors.IsNotFound(err) {
			return true, nil
		}
		return false, err
	})
	if err == wait.ErrWaitTimeout {
		logging.Warn(ctx, "Timed out waiting for JobSet to be deleted", "namespace", namespace, "jobSetName", jobSetName, "error", err)
		return fmt.Errorf("timed out waiting for jobset %q to be deleted: %w", jobSetName, err)
	}
	if err != nil {
		logging.Warn(ctx, "Failed while waiting for JobSet to be deleted", "namespace", namespace, "jobSetName", jobSetName, "error", err)
		return fmt.Errorf("failed while waiting for jobset %q to be deleted: %w", jobSetName, err)
	}
	return nil
}

func (c *Client) restartJobSet(ctx context.Context, workUnit *esspb.WorkUnit, target, author string, operationTime time.Time, priorities Priorities) error {
	ctx = logging.WithWorkUnitExecutionName(ctx, target)

	logging.Info(ctx, "Restart JobSet", "target", target)
	namespace, err := c.getNamespaceFromWorkUnitWithFallback(ctx, workUnit, target)
	if err != nil {
		logging.Warn(ctx, "Failed to get namespace for target", "target", target, "error", err)
		return fmt.Errorf("failed to get namespace for target %q: %w", target, err)
	}

	jobSetUnstructured, err := c.getJobSetByTargetAndNamespace(ctx, target, namespace)
	if err == nil && jobSetUnstructured != nil {
		shouldCreate, err := c.validateAndDeleteJobSetForRestart(ctx, jobSetUnstructured, target, namespace, operationTime)
		if err != nil {
			return err
		}
		if !shouldCreate {
			return nil
		}
	}

	newTarget := workUnit.GetCurrentWorkUnitExecution().GetName()
	ensureQueueName(workUnit, namespace)

	if err := c.createJobSet(ctx, workUnit, newTarget, author, operationTime, priorities); err != nil {
		return fmt.Errorf("failed to create JobSet %q in Kubernetes during restart: %w", buildJobSetName(target), err)
	}
	logging.Info(ctx, "Restarted JobSet", "target", target)
	return nil
}

func (c *Client) validateAndDeleteJobSetForRestart(ctx context.Context, jobSetUnstructured *unstructured.Unstructured, target, namespace string, operationTime time.Time) (bool, error) {
	ctx = logging.WithJobSetName(ctx, jobSetUnstructured.GetName(), jobSetUnstructured.GetLabels())

	valid, err := c.preconditionsValidator.IsActuationRequestValid(ctx, target, operationTime, jobSetUnstructured)
	if err != nil {
		logging.Warn(ctx, "Failed to validate restartJobSet request", "error", err)
		return false, err
	}
	if !valid {
		logging.Warn(ctx, "Discarding restart request")
		return false, nil
	}

	deleted, err := c.deleteJobSet(ctx, target, namespace, operationTime)
	if err != nil {
		logging.Warn(ctx, "Failed to delete JobSet during restart", "error", err)
	}

	if !deleted {
		logging.Warn(ctx, "JobSet not deleted during restart, proceeding with creation")
	}
	return true, nil
}

func (c *Client) updateJobSetPriority(ctx context.Context, target string, priorities Priorities) error {
	ctx = logging.WithWorkUnitExecutionName(ctx, target)
	logging.Info(ctx, "Update JobSet priority", "target", target, "priorities", priorities)

	jobSetName := buildJobSetName(target)
	if jobSetName == "" {
		return fmt.Errorf("failed to generate jobset name for target %q", target)
	}
	ctx = logging.WithJobSetName(ctx, jobSetName, nil)

	namespace, err := c.getNamespaceFromTarget(ctx, target)
	if err != nil {
		return fmt.Errorf("failed to get namespace by target %q: %w", target, err)
	}

	priorityClassName := getPriorityClassName(priorities)
	patchBytes, err := marshalPriorityPatchPayload(jobSetName, priorityClassName)
	if err != nil {
		return err
	}
	if _, err := c.dynamicClient.Resource(JobSetGVR).Namespace(namespace).Patch(ctx, jobSetName, types.MergePatchType, patchBytes, metav1.PatchOptions{}); err != nil {
		logging.Warn(ctx, "Failed to patch JobSet", "namespace", namespace, "error", err)
		return fmt.Errorf("failed to patch JobSet %q in namespace %q: %w", jobSetName, namespace, err)
	}
	logging.Info(ctx, "Updated JobSet priority", "namespace", namespace, "priorityClassName", priorityClassName)
	return nil
}

func marshalPriorityPatchPayload(jobSetName, priorityClassName string) ([]byte, error) {
	patchPayload := map[string]any{
		"metadata": map[string]any{
			"labels": map[string]string{
				kueuePriorityClassLabel: priorityClassName,
			},
		},
	}
	patchBytes, err := json.Marshal(patchPayload)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal patch payload for jobset %q: %w", jobSetName, err)
	}
	return patchBytes, nil
}

func jobSetToUnstructured(jobSet *jobsetv1alpha2.JobSet) (*unstructured.Unstructured, error) {
	jobSetObj, err := runtime.DefaultUnstructuredConverter.ToUnstructured(jobSet)
	if err != nil {
		return nil, fmt.Errorf("failed to convert JobSet to unstructured: %w", err)
	}
	jobSetUnstructured := &unstructured.Unstructured{Object: jobSetObj}

	return jobSetUnstructured, nil
}

func ensureQueueName(workUnit *esspb.WorkUnit, namespace string) {
	if workUnit.GetExecutable() == nil {
		workUnit.Executable = &esspb.Executable{}
	}
	if workUnit.GetExecutable().GetSchedulingConstraints() == nil {
		workUnit.GetExecutable().SchedulingConstraints = &esspb.SchedulingConstraints{}
	}
	if workUnit.GetExecutable().GetSchedulingConstraints().GetQueueName() == "" {
		workUnit.GetExecutable().GetSchedulingConstraints().QueueName = namespace
	}
}

func getNamespaceFromWorkUnit(workUnit *esspb.WorkUnit) string {
	return workUnit.GetExecutable().GetSchedulingConstraints().GetQueueName()
}

func getPriorityClassName(priorities Priorities) string {
	expLevel := priorityToString(priorities.ExperimentPriority)
	wuLevel := priorityToString(priorities.WorkUnitPriority)

	// This matches the priority class names in
	// /workload_priority_classes/*.yaml
	return fmt.Sprintf("exp-%s-wu-%s", expLevel, wuLevel)
}

func priorityToString(priority esspb.Priority) string {
	switch priority {
	case esspb.Priority_HIGH:
		return "high"
	case esspb.Priority_NORMAL:
		return "medium"
	case esspb.Priority_LOW:
		return "low"
	default:
		return "low"
	}
}
