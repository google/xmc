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
// Package preconditionsvalidator provides functionalities for the actuator client to check
// preconditions that requests are required to satisfy.
package preconditionsvalidator

import (
	"context"
	"fmt"
	"strconv"
	"time"

	"github.com/google/xmc/internal/kubernetes/common/constants"
	"github.com/google/xmc/internal/logging"
	"github.com/google/xmc/internal/validator"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// Validator validates preconditions for actuation requests.
type Validator interface {
	// IsActuationRequestValid checks if the actuation request is valid.
	// It checks for execution ID alignment and if the operation is ordered
	// based on operationTime.
	IsActuationRequestValid(ctx context.Context, target string, operationTime time.Time, jobSetUnstructured *unstructured.Unstructured) (bool, error)
	// IsExecutionIDAligned checks if the execution ID of the jobset is
	// less than or equal to the execution ID of the target.
	IsExecutionIDAligned(ctx context.Context, target string, jobSetUnstructured *unstructured.Unstructured) (bool, error)
	// ValidateJobSetManifest performs creation-time structural validation on the JobSet spec.
	ValidateJobSetManifest(jobSetUnstructured *unstructured.Unstructured) error
}

type validatorImpl struct{}

// New creates a new preconditions validator.
func New() Validator {
	return &validatorImpl{}
}

// IsActuationRequestValid checks if the actuation request is valid.
func (v *validatorImpl) IsActuationRequestValid(ctx context.Context, target string, operationTime time.Time, jobSetUnstructured *unstructured.Unstructured) (bool, error) {
	if jobSetUnstructured == nil {
		return true, nil
	}

	aligned, err := v.IsExecutionIDAligned(ctx, target, jobSetUnstructured)
	if err != nil {
		logging.Warn(ctx, "Execution ID alignment check failed, skipping actuation", "target", target, "jobset-name", jobSetUnstructured.GetName(), "jobset-namespace", jobSetUnstructured.GetNamespace())
		return false, err
	}
	if !aligned {
		logging.Warn(ctx, "Execution ID not aligned, skipping actuation", "target", target, "jobset-name", jobSetUnstructured.GetName(), "jobset-namespace", jobSetUnstructured.GetNamespace())
		return false, nil
	}

	ordered, err := isOperationOrdered(ctx, jobSetUnstructured, operationTime)
	if err != nil {
		logging.Warn(ctx, "Failed to check if operation is ordered, skipping actuation", "target", target, "jobset-name", jobSetUnstructured.GetName(), "jobset-namespace", jobSetUnstructured.GetNamespace())
		return false, err
	}
	if !ordered {
		logging.Warn(ctx, "Operation is out of order, skipping actuation", "target", target, "jobset-name", jobSetUnstructured.GetName(), "jobset-namespace", jobSetUnstructured.GetNamespace())
		return false, nil
	}

	validContract, err := isNonRestartingJobSetContractValid(jobSetUnstructured)
	if err != nil {
		logging.Warn(ctx, "Non-restarting JobSet contract check error, proceeding with actuation", "target", target, "jobset-name", jobSetUnstructured.GetName(), "error", err)
	} else if !validContract {
		logging.Warn(ctx, "Existing JobSet violates non-restarting contract, proceeding with actuation to clean up or restart", "target", target, "jobset-name", jobSetUnstructured.GetName())
	}

	return true, nil
}

// ValidateJobSetManifest performs creation-time structural validation on the JobSet spec.
func (v *validatorImpl) ValidateJobSetManifest(jobSetUnstructured *unstructured.Unstructured) error {
	return validateHostNetworkExclusiveTopology(jobSetUnstructured)
}

func isNonRestartingJobSetContractValid(jobSetUnstructured *unstructured.Unstructured) (bool, error) {
	maxRestarts, found, err := unstructured.NestedInt64(jobSetUnstructured.Object, "spec", "failurePolicy", "maxRestarts")
	if err != nil {
		return false, fmt.Errorf("failed to read spec.failurePolicy.maxRestarts: %w", err)
	}
	if found && maxRestarts != 0 {
		return false, nil
	}

	replicatedJobs, found, err := unstructured.NestedSlice(jobSetUnstructured.Object, "spec", "replicatedJobs")
	if err != nil {
		return false, fmt.Errorf("failed to read spec.replicatedJobs: %w", err)
	}
	if found {
		for i, rj := range replicatedJobs {
			rjMap, ok := rj.(map[string]any)
			if !ok {
				continue
			}
			restartPolicy, _, err := unstructured.NestedString(rjMap, "template", "spec", "template", "spec", "restartPolicy")
			if err != nil {
				return false, fmt.Errorf("failed to read replicatedJobs[%d] restartPolicy: %w", i, err)
			}
			if restartPolicy != "Never" {
				return false, nil
			}
		}
	}
	return true, nil
}

const exclusiveTopologyAnnotation = "alpha.jobset.sigs.k8s.io/exclusive-topology"

func validateHostNetworkExclusiveTopology(jobSetUnstructured *unstructured.Unstructured) error {
	if jobSetUnstructured == nil {
		return nil
	}

	spec, ok := jobSetUnstructured.Object["spec"].(map[string]any)
	if !ok {
		return nil
	}

	replicatedJobs, ok := spec["replicatedJobs"].([]any)
	if !ok {
		return nil
	}

	hasJobSetExclusiveTopology := jobSetUnstructured.GetAnnotations()[exclusiveTopologyAnnotation] != ""

	for _, rjAny := range replicatedJobs {
		rj, ok := rjAny.(map[string]any)
		if !ok {
			continue
		}

		template, ok := rj["template"].(map[string]any)
		if !ok {
			continue
		}

		jobSpec, ok := template["spec"].(map[string]any)
		if !ok {
			continue
		}

		podTemplate, ok := jobSpec["template"].(map[string]any)
		if !ok {
			continue
		}

		podSpec, ok := podTemplate["spec"].(map[string]any)
		if !ok {
			continue
		}

		hostNetwork, _ := podSpec["hostNetwork"].(bool)
		if !hostNetwork {
			continue
		}

		hasRJExclusiveTopology := checkNestedAnnotation(template, exclusiveTopologyAnnotation) ||
			checkNestedAnnotation(podTemplate, exclusiveTopologyAnnotation)

		if !hasJobSetExclusiveTopology && !hasRJExclusiveTopology {
			return fmt.Errorf("pods with hostNetwork=true must specify exclusive node placement annotation (%q)", exclusiveTopologyAnnotation)
		}
	}

	return nil
}

func checkNestedAnnotation(obj map[string]any, annotationKey string) bool {
	meta, ok := obj["metadata"].(map[string]any)
	if !ok {
		return false
	}
	annots, ok := meta["annotations"].(map[string]any)
	if !ok {
		return false
	}
	val, ok := annots[annotationKey].(string)
	return ok && val != ""
}

// IsExecutionIDAligned checks if the execution ID of the jobset is less than or equal to the execution ID of the target.
func (v *validatorImpl) IsExecutionIDAligned(ctx context.Context, target string, jobSetUnstructured *unstructured.Unstructured) (bool, error) {
	_, _, targetEid, err := validator.ValidateWorkUnitExecutionName(target)
	if err != nil {
		return false, fmt.Errorf("failed to extract IDs from %q: %w", target, err)
	}

	labelEid := jobSetUnstructured.GetLabels()[constants.ExecutionIDLabel]
	k8sEid, err := strconv.ParseInt(labelEid, 10, 64)
	if err != nil {
		return false, fmt.Errorf("failed to parse execution ID from %q: %w", labelEid, err)
	}

	return k8sEid <= targetEid, nil
}

func isOperationOrdered(ctx context.Context, jobSetUnstructured *unstructured.Unstructured, operationTime time.Time) (bool, error) {
	labels := jobSetUnstructured.GetLabels()
	if labels == nil {
		return false, fmt.Errorf("jobset %q in namespace %q has no labels, which is unexpected", jobSetUnstructured.GetName(), jobSetUnstructured.GetNamespace())
	}

	lastEditLabel, ok := labels[constants.LastEditLabel]
	if !ok {
		return false, fmt.Errorf("jobset %q in namespace %q has no %s label, which is unexpected", jobSetUnstructured.GetName(), jobSetUnstructured.GetNamespace(), constants.LastEditLabel)
	}

	lastEdit, err := strconv.ParseInt(lastEditLabel, 10, 64)
	if err != nil {
		return false, fmt.Errorf("failed to parse last-edit label for JobSet %s/%s: %v", jobSetUnstructured.GetNamespace(), jobSetUnstructured.GetName(), err)
	}
	lastEditTime := time.Unix(lastEdit, 0)

	return operationTime.After(lastEditTime) || operationTime.Equal(lastEditTime), nil
}
