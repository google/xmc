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

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"xmanager_cloud/internal/kubernetes/common/constants"
	"xmanager_cloud/internal/logging"
	"xmanager_cloud/internal/validator"
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

	return true, nil
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
