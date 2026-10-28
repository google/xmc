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

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	esspb "xmanager_cloud/api/protos/experiment"
	"xmanager_cloud/internal/logging"
)

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
		return "", apierrors.NewNotFound(JobSetGVR.GroupResource(), jobSetName)
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
