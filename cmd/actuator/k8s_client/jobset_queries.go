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
	"fmt"

	apierrors "golang/k8s_io/apimachinery/v/v0_23/pkg/api/errors/errors"
	"golang/k8s_io/apimachinery/v/v0_23/pkg/apis/meta/v1/unstructured/unstructured"
	metav1 "golang/k8s_io/apimachinery/v/v0_23/pkg/apis/meta/v1/v1"
	"golang/k8s_io/apimachinery/v/v0_23/pkg/types/types"
	esspb "xmanager_cloud/experiment_state_server/proto/messages_go_proto"
	"xmanager_cloud/logging/logging"
)

// JobSetUIDResult contains result metadata for jobset UID lookups.
type JobSetUIDResult struct {
	IsAligned bool
	UID       types.UID
}

// getJobsetUIDByTarget checks if the EID in the Jobset object matches the EID for the
// current target.
func (c *Client) getJobsetUIDByTarget(ctx context.Context, target string, namespace string) (JobSetUIDResult, error) {
	ctx = logging.WithWorkUnitExecutionName(ctx, target)

	// Generate the jobset name for the current target.
	jobSetName := buildJobSetName(target)
	if jobSetName == "" {
		return JobSetUIDResult{}, fmt.Errorf("failed to generate jobset name for target %q", target)
	}

	ctx = logging.WithJobSetName(ctx, jobSetName, nil)

	// Get the Jobset object so we can check if it's the correct execution ID.
	jobSet, err := c.dynamicClient.Resource(JobSetGVR).Namespace(namespace).Get(ctx, jobSetName, metav1.GetOptions{})
	if err != nil {
		if apierrors.IsNotFound(err) {
			logging.Info(ctx, "JobSet not found in namespace for target, assuming it's a new target", "namespace", namespace, "target", target)
			return JobSetUIDResult{IsAligned: true, UID: ""}, nil
		}
		logging.Warn(ctx, "Failed to get JobSet", "error", err, "namespace", namespace, "target", target)
		return JobSetUIDResult{}, fmt.Errorf("failed to get JobSet %q in namespace %q for target %q: %w", jobSetName, namespace, target, err)
	}

	aligned, err := c.preconditionsValidator.IsExecutionIDAligned(ctx, target, jobSet)
	if err != nil {
		logging.Warn(ctx, "Failed during execution ID alignment check", "error", err, "target", target)
		return JobSetUIDResult{}, fmt.Errorf("failed during execution ID alignment check for %q: %w", target, err)
	}
	if !aligned {
		return JobSetUIDResult{}, fmt.Errorf("execution ID not aligned for %q", target)
	}

	return JobSetUIDResult{IsAligned: true, UID: jobSet.GetUID()}, nil
}

func (c *Client) getNamespaceFromTarget(ctx context.Context, target string) (string, error) {
	ctx = logging.WithWorkUnitExecutionName(ctx, target)

	jobSetName := buildJobSetName(target)

	ctx = logging.WithJobSetName(ctx, jobSetName, nil)

	fieldSelector := fmt.Sprintf("metadata.name=%s", jobSetName)
	listOptions := metav1.ListOptions{
		FieldSelector: fieldSelector,
	}
	jobSets, err := c.dynamicClient.Resource(JobSetGVR).List(ctx, listOptions)
	if err != nil {
		logging.Warn(ctx, "Failed to list JobSets", "error", err)
		return "", fmt.Errorf("failed to list JobSets with field selector %q: %w", fieldSelector, err)
	}

	if len(jobSets.Items) == 0 {
		logging.Warn(ctx, "No JobSet found for target")
		return "", fmt.Errorf("no JobSet found for %q", jobSetName)
	}

	if len(jobSets.Items) > 1 {
		logging.Warn(ctx, "Multiple JobSets found for target")
		return "", fmt.Errorf("multiple JobSets found for %q: %w", jobSetName, err)
	}

	return jobSets.Items[0].GetNamespace(), nil
}

func (c *Client) getJobSetByTargetAndNamespace(ctx context.Context, target string, namespace string) (*unstructured.Unstructured, error) {
	ctx = logging.WithWorkUnitExecutionName(ctx, target)

	jobSetName := buildJobSetName(target)

	ctx = logging.WithJobSetName(ctx, jobSetName, nil)

	jobSetUnstructured, err := c.dynamicClient.Resource(JobSetGVR).Namespace(namespace).Get(ctx, jobSetName, metav1.GetOptions{})
	if err != nil {
		if apierrors.IsNotFound(err) {
			logging.Info(ctx, "JobSet not found in namespace for target, assuming it's a new target", "namespace", namespace, "target", target)
			return nil, nil
		}
		return nil, fmt.Errorf("failed to get JobSet %q in namespace %q for target %q: %w", jobSetName, namespace, target, err)
	}
	return jobSetUnstructured, nil
}

func (c *Client) getNamespaceFromWorkUnitWithFallback(ctx context.Context, workUnit *esspb.WorkUnit, target string) (string, error) {
	namespace := getNamespaceFromWorkUnit(workUnit)
	if namespace != "" {
		return namespace, nil
	}

	logging.Warn(ctx, "No namespace found in work unit for target, querying Kubernetes API", "target", target)
	return c.getNamespaceFromTarget(ctx, target)
}
