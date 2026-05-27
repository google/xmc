// Copyright 2025 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//      http://www.apache.org/licenses/LICENSE-2.0
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
	"strings"
	"testing"
	"time"

	timestamppb "protobuf/timestamp_go_proto"
	"golang/cmp/cmp"
	"golang/k8s_io/apimachinery/v/v0_23/pkg/apis/meta/v1/v1"
	"golang/k8s_io/apimachinery/v/v0_23/pkg/runtime/runtime"
	clientgotesting "golang/k8s_io/client_go/v/v0_23/testing/testing"
	"golang/protobuf/v2/proto/proto"
	"golang/protobuf/v2/testing/protocmp/protocmp"
	esspb "xmanager_cloud/experiment_state_server/proto/messages_go_proto"

	// TODO - (internal issue): Consider using a mock instead of the emulator.
	"xmanager_cloud/storage/spanner/emulator/emulator"
	spanner_lib "xmanager_cloud/storage/spanner/spanner"
	actuatortaskpb "xmanager_cloud/task_queue/proto/task_go_proto"
	"xmanager_cloud/validator/validator"
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

	_, err = storageClient.CreateWorkUnit(ctx, workUnitForOperationTest)
	if err != nil {
		t.Fatalf("storageClient.CreateWorkUnit failed: %v", err)
	}

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
