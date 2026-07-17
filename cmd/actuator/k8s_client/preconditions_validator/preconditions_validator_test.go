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
package preconditionsvalidator

import (
	"context"
	"strconv"
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"xmanager_cloud/internal/kubernetes/common/constants"
)

var testTime = time.Date(2025, time.November, 25, 14, 0, 0, 0, time.UTC)

func createJobSetWithLabels(labels map[string]string) *unstructured.Unstructured {
	l := make(map[string]any, len(labels))
	for k, v := range labels {
		l[k] = v
	}
	return &unstructured.Unstructured{
		Object: map[string]any{
			"metadata": map[string]any{
				"labels": l,
			},
		},
	}
}

func TestIsActuationRequestValid(t *testing.T) {
	ctx := context.Background()
	validator := New()
	now := testTime
	earlier := now.Add(-time.Minute)

	testCases := []struct {
		name          string
		jobSet        *unstructured.Unstructured
		target        string
		operationTime time.Time
		wantIsValid   bool
	}{
		{
			name:        "nil jobset",
			jobSet:      nil,
			wantIsValid: true,
		},
		{
			name: "k8s_eid_less_than_target_eid",
			jobSet: createJobSetWithLabels(map[string]string{
				constants.ExecutionIDLabel: "1",
				constants.LastEditLabel:    strconv.FormatInt(earlier.Unix(), 10),
			}),
			target:        "experiments/1/workUnits/1/executions/2",
			operationTime: now,
			wantIsValid:   true,
		},
		{
			name: "out of order",
			jobSet: createJobSetWithLabels(map[string]string{
				constants.ExecutionIDLabel: "1",
				constants.LastEditLabel:    strconv.FormatInt(now.Unix(), 10),
			}),
			target:        "experiments/1/workUnits/1/executions/1",
			operationTime: earlier,
			wantIsValid:   false,
		},
		{
			name: "in order and eid match",
			jobSet: createJobSetWithLabels(map[string]string{
				constants.ExecutionIDLabel: "1",
				constants.LastEditLabel:    strconv.FormatInt(earlier.Unix(), 10),
			}),
			target:        "experiments/1/workUnits/1/executions/1",
			operationTime: now,
			wantIsValid:   true,
		},
		{
			name: "equal timestamps",
			jobSet: createJobSetWithLabels(map[string]string{
				constants.ExecutionIDLabel: "1",
				constants.LastEditLabel:    strconv.FormatInt(now.Unix(), 10),
			}),
			target:        "experiments/1/workUnits/1/executions/1",
			operationTime: now,
			wantIsValid:   true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			gotIsValid, err := validator.IsActuationRequestValid(ctx, tc.target, tc.operationTime, tc.jobSet)
			if err != nil {
				t.Fatalf("IsActuationRequestValid() returned unexpected error: %v", err)
			}

			if gotIsValid != tc.wantIsValid {
				t.Errorf("IsActuationRequestValid() = %v, want %v", gotIsValid, tc.wantIsValid)
			}
		})
	}
}

func TestIsExecutionIDAligned(t *testing.T) {
	ctx := context.Background()
	validator := New()

	testCases := []struct {
		name          string
		jobSet        *unstructured.Unstructured
		target        string
		wantIsAligned bool
		wantErr       bool
	}{
		{
			name: "k8s_eid_less_than_target_eid",
			jobSet: createJobSetWithLabels(map[string]string{
				constants.ExecutionIDLabel: "1",
			}),
			target:        "experiments/1/workUnits/1/executions/2",
			wantIsAligned: true,
			wantErr:       false,
		},
		{
			name: "eid match",
			jobSet: createJobSetWithLabels(map[string]string{
				constants.ExecutionIDLabel: "1",
			}),
			target:        "experiments/1/workUnits/1/executions/1",
			wantIsAligned: true,
			wantErr:       false,
		},
		{
			name: "k8s_eid_greater_than_target_eid",
			jobSet: createJobSetWithLabels(map[string]string{
				constants.ExecutionIDLabel: "2",
			}),
			target:        "experiments/1/workUnits/1/executions/1",
			wantIsAligned: false,
			wantErr:       false,
		},
		{
			name: "invalid target",
			jobSet: createJobSetWithLabels(map[string]string{
				constants.ExecutionIDLabel: "1",
			}),
			target:        "invalid/target",
			wantIsAligned: false,
			wantErr:       true,
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			gotIsAligned, err := validator.IsExecutionIDAligned(ctx, tc.target, tc.jobSet)
			if (err != nil) != tc.wantErr {
				t.Errorf("IsExecutionIDAligned() returned error %v, wantErr: %v", err, tc.wantErr)
			}
			if gotIsAligned != tc.wantIsAligned {
				t.Errorf("IsExecutionIDAligned() = %v, want %v", gotIsAligned, tc.wantIsAligned)
			}
		})
	}
}
