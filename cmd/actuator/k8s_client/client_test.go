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
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/testing/protocmp"
	timestamppb "google.golang.org/protobuf/types/known/timestamppb"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	"k8s.io/client-go/rest"
	clientgotesting "k8s.io/client-go/testing"
	esspb "xmanager_cloud/api/protos/experiment"

	// TODO - (internal issue): Consider using a mock instead of the emulator.
	actuatortaskpb "xmanager_cloud/api/protos/task_queue"
	constants "xmanager_cloud/internal/kubernetes/common/constants"
	spanner_lib "xmanager_cloud/internal/storage/spanner"
	"xmanager_cloud/internal/storage/spanner/emulator"
	"xmanager_cloud/internal/validator"
)

const (
	timeDelta        = 1 * time.Minute
	testExperimentID = 123
	testWorkUnitID   = 456
	testExecutionID  = 1
	testTarget       = "experiments/123/workUnits/456/executions/1"
	testJobSetName   = "xmc-operation-123-456"
	testNamespace    = "test-queue"
)

var (
	createPayload = &actuatortaskpb.ActuatorPayload{
		Target: proto.String(testTarget),
		Action: &actuatortaskpb.Action{
			Action: &actuatortaskpb.Action_Create{
				Create: &actuatortaskpb.CreateAction{},
			},
		},
		WorkUnit: &esspb.WorkUnit{
			Name: "work-unit-1",
			Executable: &esspb.Executable{
				SchedulingConstraints: &esspb.SchedulingConstraints{
					QueueName: testNamespace,
				},
				Type: &esspb.Executable_Jobset{
					Jobset: &esspb.JobSetExecutable{
						Jobs: []*esspb.KubernetesJob{
							{
								Name: "job1",
								Spec: &esspb.ExecutableSpec{
									Resources: &esspb.ResourceSet{
										Resources: &esspb.ResourceSet_CpuResources{
											CpuResources: &esspb.CpuResources{
												CpuMillicores: 100,
												MemoryBytes:   1024,
											},
										},
									},
									DockerImage: "foo/bar",
								},
								Replicas: 1,
							},
						},
					},
				},
			},
		},
		CreateTime: timestamppb.New(testTime),
	}
	deletePayload = &actuatortaskpb.ActuatorPayload{
		Target: proto.String(testTarget),
		Action: &actuatortaskpb.Action{
			Action: &actuatortaskpb.Action_Delete{
				Delete: &actuatortaskpb.DeleteAction{},
			},
		},
		WorkUnit: &esspb.WorkUnit{
			Name: "work-unit-1",
			Executable: &esspb.Executable{
				SchedulingConstraints: &esspb.SchedulingConstraints{
					QueueName: testNamespace,
				},
			},
		},
		CreateTime: timestamppb.New(testTime),
	}
	restartPayload = &actuatortaskpb.ActuatorPayload{
		Target: proto.String(testTarget),
		Action: &actuatortaskpb.Action{
			Action: &actuatortaskpb.Action_Restart{
				Restart: &actuatortaskpb.RestartAction{},
			},
		},
		WorkUnit: &esspb.WorkUnit{
			Name: "work-unit-1",
			Executable: &esspb.Executable{
				SchedulingConstraints: &esspb.SchedulingConstraints{
					QueueName: testNamespace,
				},
				Type: &esspb.Executable_Jobset{
					Jobset: &esspb.JobSetExecutable{
						Jobs: []*esspb.KubernetesJob{
							{
								Name: "job1",
								Spec: &esspb.ExecutableSpec{
									Resources: &esspb.ResourceSet{
										Resources: &esspb.ResourceSet_CpuResources{
											CpuResources: &esspb.CpuResources{
												CpuMillicores: 100,
												MemoryBytes:   1024,
											},
										},
									},
									DockerImage: "foo/bar",
								},
								Replicas: 1,
							},
						},
					},
				},
			},
			CurrentWorkUnitExecution: &esspb.WorkUnitExecution{Name: "experiments/123/workUnits/456/executions/2"},
		},
		CreateTime: timestamppb.New(testTime),
	}
)

func addTestResourcesToStorage(ctx context.Context, t *testing.T, storageClient *spanner_lib.Storage) string {
	t.Helper()
	_, err := storageClient.CreateExperiment(ctx, experimentForOperationTest)
	if err != nil {
		t.Fatalf("storageClient.CreateExperiment failed: %v", err)
	}

	spanner_lib.CreateAndStoreWorkUnit(ctx, t, storageClient, workUnitForOperationTest)

	lro, err := storageClient.AddWorkUnitOperation(ctx, testExperimentID, testWorkUnitID, &esspb.OperationMetadata{})
	if err != nil {
		t.Fatalf("storageClient.AddWorkUnitOperation failed: %v", err)
	}
	return lro.GetName()
}

func TestCreateWorkUnit(t *testing.T) {
	ctx, client, dynamicClient := setupTestClient(true /* createNewNamespaces */, &alwaysPassingPreconditionsValidator{})

	if err := client.CreateWorkUnit(ctx, createPayload); err != nil {
		t.Fatalf("CreateWorkUnit(%v) returned unexpected error: %v", createPayload, err)
	}

	jobsets, err := dynamicClient.Resource(JobSetGVR).Namespace(testNamespace).List(ctx, v1.ListOptions{})
	if err != nil {
		t.Fatalf("Listing jobsets failed: %v", err)
	}

	if len(jobsets.Items) != 1 {
		t.Errorf("Expected 1 jobset to be created, but got %d", len(jobsets.Items))
	} else {
		jobset := jobsets.Items[0]
		if jobset.GetName() != "xmc-operation-123-456" {
			t.Errorf("Expected jobset name to be 'xmc-operation-123-456', but got %q", jobset.GetName())
		}
		if jobset.GetNamespace() != testNamespace {
			t.Errorf("Expected jobset namespace to be %q, but got %q", testNamespace, jobset.GetNamespace())
		}
	}
}

func TestCreateWorkUnit_NamespaceCreationDisabled(t *testing.T) {
	ctx, client, dynamicClient := setupTestClient(false /* createNewNamespaces */, &alwaysPassingPreconditionsValidator{})

	err := client.CreateWorkUnit(ctx, createPayload)
	if err == nil {
		t.Fatalf("CreateWorkUnit(%v) succeeded, want error", createPayload)
	}

	jobsets, listErr := dynamicClient.Resource(JobSetGVR).Namespace(testNamespace).List(ctx, v1.ListOptions{})
	if listErr != nil {
		t.Fatalf("Listing jobsets failed: %v", listErr)
	}
	if len(jobsets.Items) != 0 {
		t.Errorf("Expected 0 jobsets to be created, but got %d", len(jobsets.Items))
	}

	wantErrMsg := "failed to create JobSet for target \"experiments/123/workUnits/456/executions/1\": failed to ensure namespace \"test-queue\" exists: namespace \"test-queue\" does not exist and namespace creation is disabled"
	if err.Error() != wantErrMsg {
		t.Errorf("CreateWorkUnit(%v) returned error %q, want %q", createPayload, err.Error(), wantErrMsg)
	}
}

func TestCreateWorkUnit_JobSetCreationFails(t *testing.T) {
	ctx, client, dynamicClient := setupTestClient(true /* createNewNamespaces */, &alwaysPassingPreconditionsValidator{})

	dynamicClient.PrependReactor("create", "jobsets", func(action clientgotesting.Action) (handled bool, ret runtime.Object, err error) {
		return true, nil, fmt.Errorf("simulated creation failure")
	})

	err := client.CreateWorkUnit(ctx, createPayload)
	if err == nil {
		t.Fatalf("CreateWorkUnit(%v) succeeded, want error", createPayload)
	}

	wantErrMsg := "failed to create JobSet for target \"experiments/123/workUnits/456/executions/1\": failed to create JobSet \"xmc-operation-123-456\" in Kubernetes: simulated creation failure"
	if err.Error() != wantErrMsg {
		t.Errorf("CreateWorkUnit(%v) returned error %q, want %q", createPayload, err.Error(), wantErrMsg)
	}
}

func TestCreateWorkUnit_AlreadyExists(t *testing.T) {
	tests := []struct {
		name        string
		setup       func(t *testing.T, ctx context.Context, client *Client, dynamicClient *dynamicfake.FakeDynamicClient)
		wantErr     bool
		errContains []string
	}{
		{
			name: "duplicate success with matching execution ID in labels",
			setup: func(t *testing.T, ctx context.Context, client *Client, dynamicClient *dynamicfake.FakeDynamicClient) {
				if err := client.CreateWorkUnit(ctx, createPayload); err != nil {
					t.Fatalf("First CreateWorkUnit failed: %v", err)
				}
			},
			wantErr: false,
		},
		{
			name: "duplicate success with matching execution ID in annotations",
			setup: func(t *testing.T, ctx context.Context, client *Client, dynamicClient *dynamicfake.FakeDynamicClient) {
				createTestJobSet(ctx, t, client, testNamespace, testTarget, map[string]string{
					constants.ManagedByLabel: constants.XmcLabelValue,
				})
				jobSet, err := client.getJobSetByTargetAndNamespace(ctx, testTarget, testNamespace)
				if err != nil {
					t.Fatalf("getJobSetByTargetAndNamespace failed: %v", err)
				}
				jobSetLabels := jobSet.GetLabels()
				delete(jobSetLabels, constants.ExecutionIDLabel)
				jobSet.SetLabels(jobSetLabels)
				jobSet.SetAnnotations(map[string]string{
					constants.ExecutionIDLabel: strconv.FormatInt(testExecutionID, 10),
				})
				if _, err := dynamicClient.Resource(JobSetGVR).Namespace(testNamespace).Update(ctx, jobSet, v1.UpdateOptions{}); err != nil {
					t.Fatalf("Update failed: %v", err)
				}
			},
			wantErr: false,
		},
		{
			name: "naming conflict when not managed by XMC",
			setup: func(t *testing.T, ctx context.Context, client *Client, dynamicClient *dynamicfake.FakeDynamicClient) {
				createTestJobSet(ctx, t, client, testNamespace, testTarget, map[string]string{
					constants.ManagedByLabel:   "external-controller",
					constants.ExecutionIDLabel: strconv.FormatInt(testExecutionID, 10),
				})
			},
			wantErr:     true,
			errContains: []string{"naming conflict", "not managed by XMC"},
		},
		{
			name: "naming conflict when execution ID mismatches",
			setup: func(t *testing.T, ctx context.Context, client *Client, dynamicClient *dynamicfake.FakeDynamicClient) {
				createTestJobSet(ctx, t, client, testNamespace, testTarget, map[string]string{
					constants.ManagedByLabel:   constants.XmcLabelValue,
					constants.ExecutionIDLabel: "999",
				})
			},
			wantErr:     true,
			errContains: []string{"naming conflict", "execution ID does not match"},
		},
		{
			name: "conflict when existing resource was deleted concurrently",
			setup: func(t *testing.T, ctx context.Context, client *Client, dynamicClient *dynamicfake.FakeDynamicClient) {
				dynamicClient.PrependReactor("create", "jobsets", func(action clientgotesting.Action) (handled bool, ret runtime.Object, err error) {
					return true, nil, apierrors.NewAlreadyExists(schema.GroupResource{Group: "jobset.x-k8s.io", Resource: "jobsets"}, testJobSetName)
				})
			},
			wantErr:     true,
			errContains: []string{"conflict creating JobSet", "deleted concurrently"},
		},
		{
			name: "conflict when existing resource is terminating",
			setup: func(t *testing.T, ctx context.Context, client *Client, dynamicClient *dynamicfake.FakeDynamicClient) {
				createTestJobSet(ctx, t, client, testNamespace, testTarget, map[string]string{
					constants.ManagedByLabel:   constants.XmcLabelValue,
					constants.ExecutionIDLabel: strconv.FormatInt(testExecutionID, 10),
				})
				now := v1.Now()
				jobSet, err := client.getJobSetByTargetAndNamespace(ctx, testTarget, testNamespace)
				if err != nil {
					t.Fatalf("getJobSetByTargetAndNamespace failed: %v", err)
				}
				jobSet.SetDeletionTimestamp(&now)
				if _, err := dynamicClient.Resource(JobSetGVR).Namespace(testNamespace).Update(ctx, jobSet, v1.UpdateOptions{}); err != nil {
					t.Fatalf("Update failed: %v", err)
				}
			},
			wantErr:     true,
			errContains: []string{"conflict creating JobSet", "is terminating"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, client, dynamicClient := setupTestClient(true /* createNewNamespaces */, &alwaysPassingPreconditionsValidator{})
			if tt.setup != nil {
				tt.setup(t, ctx, client, dynamicClient)
			}

			err := client.CreateWorkUnit(ctx, createPayload)
			if (err != nil) != tt.wantErr {
				t.Fatalf("CreateWorkUnit() error = %v, wantErr = %v", err, tt.wantErr)
			}
			for _, substr := range tt.errContains {
				if !strings.Contains(err.Error(), substr) {
					t.Errorf("CreateWorkUnit() error = %q, want it to contain %q", err.Error(), substr)
				}
			}

			if !tt.wantErr {
				// Subsequent duplicate call also succeeds safely (idempotency)
				if err := client.CreateWorkUnit(ctx, createPayload); err != nil {
					t.Fatalf("Subsequent CreateWorkUnit (duplicate) returned unexpected error: %v", err)
				}
				jobsets, err := dynamicClient.Resource(JobSetGVR).Namespace(testNamespace).List(ctx, v1.ListOptions{})
				if err != nil {
					t.Fatalf("Listing jobsets failed: %v", err)
				}
				if len(jobsets.Items) != 1 {
					t.Errorf("Expected exactly 1 jobset, got %d", len(jobsets.Items))
				}
			}
		})
	}
}

func TestDeleteWorkUnit(t *testing.T) {
	ctx, client, dynamicClient := setupTestClient(true /* createNewNamespaces */, &alwaysPassingPreconditionsValidator{})

	createTestJobSet(ctx, t, client, testNamespace, testTarget, nil)

	jobsets, err := dynamicClient.Resource(JobSetGVR).Namespace(testNamespace).List(ctx, v1.ListOptions{})
	if err != nil {
		t.Fatalf("Listing jobsets failed: %v", err)
	}
	if len(jobsets.Items) != 1 {
		t.Fatalf("Expected 1 jobset to be created, but got %d", len(jobsets.Items))
	}

	if err := client.DeleteWorkUnit(ctx, deletePayload); err != nil {
		t.Fatalf("DeleteWorkUnit(%v) returned unexpected error: %v", deletePayload, err)
	}

	jobsets, err = dynamicClient.Resource(JobSetGVR).Namespace(testNamespace).List(ctx, v1.ListOptions{})
	if err != nil {
		t.Fatalf("Listing jobsets failed: %v", err)
	}
	if len(jobsets.Items) != 0 {
		t.Errorf("Expected 0 jobset after deletion, but got %d", len(jobsets.Items))
	}
}

func TestDeleteWorkUnit_JobSetNotFound(t *testing.T) {
	ctx, client, _ := setupTestClient(true /* createNewNamespaces */, &alwaysPassingPreconditionsValidator{})

	err := client.DeleteWorkUnit(ctx, deletePayload)
	if err != nil {
		t.Fatalf("DeleteWorkUnit(%v) returned unexpected error: %v", deletePayload, err)
	}
}

func TestDeleteWorkUnit_DeleteFails(t *testing.T) {
	ctx, client, dynamicClient := setupTestClient(true /* createNewNamespaces */, &alwaysPassingPreconditionsValidator{})

	createTestJobSet(ctx, t, client, testNamespace, testTarget, nil)

	dynamicClient.PrependReactor("delete", "jobsets", func(action clientgotesting.Action) (handled bool, ret runtime.Object, err error) {
		return true, nil, fmt.Errorf("simulated delete failure")
	})

	err := client.DeleteWorkUnit(ctx, deletePayload)
	if err == nil {
		t.Fatalf("DeleteWorkUnit(%v) succeeded, want error", deletePayload)
	}

	wantErrMsg := "failed to delete JobSet for target \"experiments/123/workUnits/456/executions/1\": failed to delete JobSet \"xmc-operation-123-456\" in namespace \"test-queue\": simulated delete failure"
	if err.Error() != wantErrMsg {
		t.Errorf("DeleteWorkUnit(%v) returned error %q, want %q", deletePayload, err.Error(), wantErrMsg)
	}
}

func TestRestartWorkUnit(t *testing.T) {
	ctx, client, dynamicClient := setupTestClient(true /* createNewNamespaces */, &alwaysPassingPreconditionsValidator{})

	createTestJobSet(ctx, t, client, testNamespace, testTarget, nil)

	jobsets, err := dynamicClient.Resource(JobSetGVR).Namespace(testNamespace).List(ctx, v1.ListOptions{})
	if err != nil {
		t.Fatalf("Listing jobsets failed: %v", err)
	}
	if len(jobsets.Items) != 1 {
		t.Fatalf("Expected 1 jobset to be created, but got %d", len(jobsets.Items))
	}

	if err := client.RestartWorkUnit(ctx, restartPayload); err != nil {
		t.Fatalf("RestartWorkUnit(%v) returned unexpected error: %v", restartPayload, err)
	}

	jobsets, err = dynamicClient.Resource(JobSetGVR).Namespace(testNamespace).List(ctx, v1.ListOptions{})
	if err != nil {
		t.Fatalf("Listing jobsets failed: %v", err)
	}
	if len(jobsets.Items) != 1 {
		t.Errorf("Expected 1 jobset after restart, but got %d", len(jobsets.Items))
	}
}

func TestRestartWorkUnit_JobSetNotFoundCreatesJobSet(t *testing.T) {
	ctx, client, dynamicClient := setupTestClient(true /* createNewNamespaces */, &alwaysPassingPreconditionsValidator{})

	jobsets, err := dynamicClient.Resource(JobSetGVR).Namespace(testNamespace).List(ctx, v1.ListOptions{})
	if err != nil {
		t.Fatalf("Listing jobsets failed: %v", err)
	}
	if len(jobsets.Items) != 0 {
		t.Fatalf("Expected 0 jobset to exist, but got %d", len(jobsets.Items))
	}

	if err := client.RestartWorkUnit(ctx, restartPayload); err != nil {
		t.Fatalf("RestartWorkUnit(%v) returned unexpected error: %v", restartPayload, err)
	}

	jobsets, err = dynamicClient.Resource(JobSetGVR).Namespace(testNamespace).List(ctx, v1.ListOptions{})
	if err != nil {
		t.Fatalf("Listing jobsets failed: %v", err)
	}
	if len(jobsets.Items) != 1 {
		t.Errorf("Expected 1 jobset after restart, but got %d", len(jobsets.Items))
	}
}

func TestRestartWorkUnit_DeletionFails(t *testing.T) {
	ctx, client, dynamicClient := setupTestClient(true /* createNewNamespaces */, &alwaysPassingPreconditionsValidator{})

	createTestJobSet(ctx, t, client, testNamespace, testTarget, nil)

	dynamicClient.PrependReactor("delete", "jobsets", func(action clientgotesting.Action) (handled bool, ret runtime.Object, err error) {
		return true, nil, fmt.Errorf("simulated deletion failure")
	})

	err := client.RestartWorkUnit(ctx, restartPayload)
	if err == nil {
		t.Fatalf("RestartWorkUnit(%v) succeeded, want error", restartPayload)
	}

	wantErrMsg := "failed to restart JobSet for target \"experiments/123/workUnits/456/executions/1\": failed to create JobSet \"xmc-operation-123-456\" in Kubernetes during restart: failed to create JobSet \"xmc-operation-123-456\" in Kubernetes: jobsets.jobset.x-k8s.io \"xmc-operation-123-456\" already exists"
	if err.Error() != wantErrMsg {
		t.Errorf("RestartWorkUnit(%v) returned error %q, want %q", restartPayload, err.Error(), wantErrMsg)
	}
}

func TestRestartWorkUnit_RecreationFails(t *testing.T) {
	ctx, client, dynamicClient := setupTestClient(true /* createNewNamespaces */, &alwaysPassingPreconditionsValidator{})

	createTestJobSet(ctx, t, client, testNamespace, testTarget, nil)

	dynamicClient.PrependReactor("create", "jobsets", func(action clientgotesting.Action) (handled bool, ret runtime.Object, err error) {
		return true, nil, fmt.Errorf("simulated creation failure")
	})

	err := client.RestartWorkUnit(ctx, restartPayload)
	if err == nil {
		t.Fatalf("RestartWorkUnit(%v) succeeded, want error", restartPayload)
	}

	wantErrMsg := "failed to restart JobSet for target \"experiments/123/workUnits/456/executions/1\": failed to create JobSet \"xmc-operation-123-456\" in Kubernetes during restart: failed to create JobSet \"xmc-operation-123-456\" in Kubernetes: simulated creation failure"
	if err.Error() != wantErrMsg {
		t.Errorf("RestartWorkUnit(%v) returned error %q, want %q", restartPayload, err.Error(), wantErrMsg)
	}
}

func TestWorkUnitOperations_UpdateOperationOnSuccess(t *testing.T) {
	testCases := []struct {
		name        string
		workUnit    *esspb.WorkUnit
		action      *actuatortaskpb.Action
		needsJobSet bool
		method      func(c *Client, ctx context.Context, p *actuatortaskpb.ActuatorPayload) error
	}{
		{
			name:     "CreateWorkUnit",
			workUnit: testWorkUnitWithJob,
			action: &actuatortaskpb.Action{
				Action: &actuatortaskpb.Action_Create{Create: &actuatortaskpb.CreateAction{}},
			},
			needsJobSet: false,
			method:      (*Client).CreateWorkUnit,
		},
		{
			name:     "DeleteWorkUnit",
			workUnit: testWorkUnitWithJob,
			action: &actuatortaskpb.Action{
				Action: &actuatortaskpb.Action_Delete{Delete: &actuatortaskpb.DeleteAction{}},
			},
			needsJobSet: true,
			method:      (*Client).DeleteWorkUnit,
		},
		{
			name: "RestartWorkUnit",
			workUnit: &esspb.WorkUnit{
				Name:                     testWorkUnitWithJob.Name,
				Executable:               testWorkUnitWithJob.Executable,
				CurrentWorkUnitExecution: &esspb.WorkUnitExecution{Name: "experiments/123/workUnits/456/executions/2"},
			},
			action: &actuatortaskpb.Action{
				Action: &actuatortaskpb.Action_Restart{Restart: &actuatortaskpb.RestartAction{}},
			},
			needsJobSet: true,
			method:      (*Client).RestartWorkUnit,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			ctx, actuatorClient, _ := setupTestClient(true /* createNewNamespaces */, &alwaysPassingPreconditionsValidator{})
			_, spannerClient := emulator.Setup(ctx, t)
			storageClient, err := spanner_lib.NewStorage(spannerClient)
			if err != nil {
				t.Fatalf("spanner_lib.NewStorage failed: %v", err)
			}
			actuatorClient.storage = storageClient

			opName := addTestResourcesToStorage(ctx, t, storageClient)

			if tc.needsJobSet {
				createTestJobSet(ctx, t, actuatorClient, testWorkUnitWithJob.GetExecutable().GetSchedulingConstraints().GetQueueName(), testTarget, nil)
			}

			payload := &actuatortaskpb.ActuatorPayload{
				WorkUnit:   tc.workUnit,
				Target:     proto.String(testTarget),
				CreateTime: timestamppb.New(testTime),
				Metadata:   &actuatortaskpb.Metadata{OperationName: proto.String(opName)},
				Action:     tc.action,
			}
			if err := tc.method(actuatorClient, ctx, payload); err != nil {
				t.Fatalf("%s(%v) failed, want success: %v", tc.name, payload, err)
			}

			xid, wid, oid, err := validator.ValidateOperationName(opName)
			if err != nil {
				t.Fatalf("validator.ValidateOperationName(%q) failed: %v", opName, err)
			}
			op, err := storageClient.FetchWorkUnitOperation(ctx, xid, wid, oid)
			if err != nil {
				t.Fatalf("storageClient.FetchWorkUnitOperation failed: %v", err)
			}
			if !op.Done {
				t.Errorf("Operation.Done = false, want true")
			}
			resp := &esspb.WorkUnit{}
			if err := op.GetResponse().UnmarshalTo(resp); err != nil {
				t.Fatalf("Failed to unmarshal operation response: %v", err)
			}
			if diff := cmp.Diff(tc.workUnit, resp, protocmp.Transform()); diff != "" {
				t.Errorf("Operation response differs from expected (-want +got):\n%s", diff)
			}
		})
	}
}

func TestWorkUnitOperations_UpdateOperationOnError(t *testing.T) {
	testCases := []struct {
		name              string
		workUnit          *esspb.WorkUnit
		action            *actuatortaskpb.Action
		needsJobSet       bool
		method            func(c *Client, ctx context.Context, p *actuatortaskpb.ActuatorPayload) error
		reactorVerb       string
		reactorFailureMsg string
	}{
		{
			name:     "CreateWorkUnit",
			workUnit: testWorkUnitWithJob,
			action: &actuatortaskpb.Action{
				Action: &actuatortaskpb.Action_Create{Create: &actuatortaskpb.CreateAction{}},
			},
			needsJobSet:       false,
			method:            (*Client).CreateWorkUnit,
			reactorVerb:       "create",
			reactorFailureMsg: "simulated creation failure",
		},
		{
			name:     "DeleteWorkUnit",
			workUnit: testWorkUnitWithJob,
			action: &actuatortaskpb.Action{
				Action: &actuatortaskpb.Action_Delete{Delete: &actuatortaskpb.DeleteAction{}},
			},
			needsJobSet:       true,
			method:            (*Client).DeleteWorkUnit,
			reactorVerb:       "delete",
			reactorFailureMsg: "simulated delete failure",
		},
		{
			name: "RestartWorkUnit",
			workUnit: &esspb.WorkUnit{
				Name:                     testWorkUnitWithJob.Name,
				Executable:               testWorkUnitWithJob.Executable,
				CurrentWorkUnitExecution: &esspb.WorkUnitExecution{Name: "experiments/123/workUnits/456/executions/2"},
			},
			action: &actuatortaskpb.Action{
				Action: &actuatortaskpb.Action_Restart{Restart: &actuatortaskpb.RestartAction{}},
			},
			needsJobSet:       true,
			method:            (*Client).RestartWorkUnit,
			reactorVerb:       "create",
			reactorFailureMsg: "simulated creation failure",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			ctx, actuatorClient, dynamicClient := setupTestClient(true /* createNewNamespaces */, &alwaysPassingPreconditionsValidator{})
			_, spannerClient := emulator.Setup(ctx, t)
			storageClient, err := spanner_lib.NewStorage(spannerClient)
			if err != nil {
				t.Fatalf("spanner_lib.NewStorage failed: %v", err)
			}
			actuatorClient.storage = storageClient

			opName := addTestResourcesToStorage(ctx, t, storageClient)

			if tc.needsJobSet {
				createTestJobSet(ctx, t, actuatorClient, testWorkUnitWithJob.GetExecutable().GetSchedulingConstraints().GetQueueName(), testTarget, nil)
			}

			dynamicClient.PrependReactor(tc.reactorVerb, "jobsets", func(action clientgotesting.Action) (handled bool, ret runtime.Object, err error) {
				return true, nil, fmt.Errorf("%s", tc.reactorFailureMsg)
			})

			payload := &actuatortaskpb.ActuatorPayload{
				WorkUnit:   tc.workUnit,
				Target:     proto.String(testTarget),
				CreateTime: timestamppb.New(testTime),
				Metadata:   &actuatortaskpb.Metadata{OperationName: proto.String(opName)},
				Action:     tc.action,
			}

			if err := tc.method(actuatorClient, ctx, payload); err == nil {
				t.Fatalf("%s(%v) succeeded, want error", tc.name, payload)
			}

			xid, wid, oid, err := validator.ValidateOperationName(opName)
			if err != nil {
				t.Fatalf("validator.ValidateOperationName(%q) failed: %v", opName, err)
			}
			op, err := storageClient.FetchWorkUnitOperation(ctx, xid, wid, oid)
			if err != nil {
				t.Fatalf("storageClient.FetchWorkUnitOperation failed: %v", err)
			}
			if !op.Done {
				t.Errorf("Operation.Done = false, want true")
			}
			if op.GetError() == nil {
				t.Errorf("Operation.GetError() is nil, want error")
			}
			if !strings.Contains(op.GetError().GetMessage(), tc.reactorFailureMsg) {
				t.Errorf("Operation.GetError().GetMessage() = %q, want substring %q", op.GetError().GetMessage(), tc.reactorFailureMsg)
			}
		})
	}
}

func TestNew_WithKubeconfig(t *testing.T) {
	t.Run("valid kubeconfig", func(t *testing.T) {
		tmpFile, err := os.CreateTemp("", "kubeconfig-*")
		if err != nil {
			t.Fatalf("os.CreateTemp failed: %v", err)
		}
		defer os.Remove(tmpFile.Name())
		kubeconfigContent := `apiVersion: v1
kind: Config
clusters:
- cluster:
    server: http://127.0.0.1:65534
  name: test-cluster
contexts:
- context:
    cluster: test-cluster
    user: test-user
  name: test-context
current-context: test-context
users:
- name: test-user
  user:
    token: fake-token
`
		if _, err := tmpFile.WriteString(kubeconfigContent); err != nil {
			t.Fatalf("tmpFile.WriteString failed: %v", err)
		}
		tmpFile.Close()

		t.Setenv("KUBECONFIG", tmpFile.Name())
		client, err := New(ActuatorRuntimeConfig{}, nil, nil)
		if err != nil {
			t.Fatalf("New() with valid KUBECONFIG failed: %v", err)
		}
		if client == nil {
			t.Fatal("New() returned nil client, want non-nil")
		}
	})

	t.Run("invalid kubeconfig path", func(t *testing.T) {
		t.Setenv("KUBECONFIG", "/non/existent/path/kubeconfig")
		_, err := New(ActuatorRuntimeConfig{}, nil, nil)
		if err == nil {
			t.Fatal("New() with non-existent KUBECONFIG succeeded, want error")
		}
	})
}

func TestBuildRESTConfig_InClusterDetected_FailsFast(t *testing.T) {
	t.Setenv("KUBECONFIG", "")
	t.Setenv("KUBERNETES_SERVICE_HOST", "10.0.0.1")
	t.Setenv("KUBERNETES_SERVICE_PORT", "443")

	orig := inClusterConfig
	inClusterConfig = func() (*rest.Config, error) {
		return nil, errors.New("open /var/run/secrets/kubernetes.io/serviceaccount/token: no such file or directory")
	}
	t.Cleanup(func() { inClusterConfig = orig })

	_, err := buildRESTConfig()
	if err == nil {
		t.Fatal("buildRESTConfig() with in-cluster environment succeeded unexpectedly, want error")
	}
	expectedSub := "running in-cluster (KUBERNETES_SERVICE_HOST is set), but failed to load in-cluster config"
	if !strings.Contains(err.Error(), expectedSub) {
		t.Errorf("buildRESTConfig() error = %q, want substring %q", err.Error(), expectedSub)
	}
}

func TestBuildRESTConfig_InClusterDetected_Succeeds(t *testing.T) {
	t.Setenv("KUBECONFIG", "")
	t.Setenv("KUBERNETES_SERVICE_HOST", "10.0.0.1")
	t.Setenv("KUBERNETES_SERVICE_PORT", "443")

	expectedCfg := &rest.Config{Host: "https://10.0.0.1:443"}
	orig := inClusterConfig
	inClusterConfig = func() (*rest.Config, error) {
		return expectedCfg, nil
	}
	t.Cleanup(func() { inClusterConfig = orig })

	cfg, err := buildRESTConfig()
	if err != nil {
		t.Fatalf("buildRESTConfig() with valid in-cluster environment failed: %v", err)
	}
	if cfg != expectedCfg {
		t.Errorf("buildRESTConfig() = %v, want %v", cfg, expectedCfg)
	}
}

func TestBuildRESTConfig_DefaultFallback_FailsWhenNoConfig(t *testing.T) {
	t.Setenv("KUBECONFIG", "")
	t.Setenv("KUBERNETES_SERVICE_HOST", "")
	t.Setenv("KUBERNETES_SERVICE_PORT", "")
	t.Setenv("HOME", t.TempDir())

	orig := inClusterConfig
	inClusterConfig = func() (*rest.Config, error) {
		return nil, rest.ErrNotInCluster
	}
	t.Cleanup(func() { inClusterConfig = orig })

	_, err := buildRESTConfig()
	if err == nil {
		t.Fatal("buildRESTConfig() without any config succeeded unexpectedly, want error")
	}
	expectedSub := "failed to load default kubeconfig"
	if !strings.Contains(err.Error(), expectedSub) {
		t.Errorf("buildRESTConfig() error = %q, want substring %q", err.Error(), expectedSub)
	}
}
