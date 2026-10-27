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
	"fmt"
	"strconv"
	"testing"
	"time"

	"google.golang.org/protobuf/proto"
	batchv1 "k8s.io/api/batch/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	clientgotesting "k8s.io/client-go/testing"
	esspb "xmanager_cloud/api/protos/experiment"
	constants "xmanager_cloud/internal/kubernetes/common/constants"
	"xmanager_cloud/internal/prototest"
)

const testOperationName = "experiments/1/workUnits/1/operations/1"

func createTestWorkUnit(namespace string) *esspb.WorkUnit {
	return &esspb.WorkUnit{
		Name: "work-unit-1",
		Executable: &esspb.Executable{
			SchedulingConstraints: &esspb.SchedulingConstraints{
				QueueName: namespace,
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
	}
}

type populateTestFixture struct {
	client        *Client
	dynamicClient *dynamicfake.FakeDynamicClient
	timestamp     time.Time
}

func setupPopulateTest(t *testing.T, target string) populateTestFixture {
	t.Helper()
	ctx, client, dynamicClient := setupTestClient(true /* createNewNamespaces */, &alwaysPassingPreconditionsValidator{})
	labels := map[string]string{constants.LastEditLabel: strconv.FormatInt(testTime.Unix(), 10)}
	createTestJobSet(ctx, t, client, testNamespace, target, labels)
	return populateTestFixture{
		client:        client,
		dynamicClient: dynamicClient,
		timestamp:     testTime,
	}
}

var testWorkUnitWithJob = &esspb.WorkUnit{
	Name: "workUnit1",
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
}

var baseCPUWorkUnit = &esspb.WorkUnit{
	Name: "workUnit1",
	Executable: &esspb.Executable{
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
}

var experimentForOperationTest = &esspb.Experiment{
	Name:      "experiments/123",
	Status:    &esspb.ExperimentStatus{},
	Urls:      []*esspb.Url{},
	Tags:      []string{},
	Resources: []*esspb.ResourceSet{},
}

var workUnitForOperationTest = &esspb.WorkUnit{
	Name: "experiments/123/workUnits/456",
	CurrentWorkUnitExecution: &esspb.WorkUnitExecution{
		Name: "experiments/123/workUnits/456/executions/1",
		Status: &esspb.WorkUnitStatus{
			State: esspb.WorkUnitStatus_INITIALIZING,
		},
		Metadata: &esspb.WorkUnitExecutionMetadata{},
	},
	Urls: []*esspb.Url{},
	Tags: []string{},
	Executable: &esspb.Executable{
		SchedulingConstraints: &esspb.SchedulingConstraints{
			QueueName: testNamespace,
		},
	},
}

func TestBuildJobSetName(t *testing.T) {
	got := buildJobSetName("experiments/1/workUnits/1/executions/1")
	want := "xmc-operation-1-1"
	if got != want {
		t.Errorf("buildJobSetName() = %v, want %v", got, want)
	}
}

func TestConstructJobSet_GPU_GKE(t *testing.T) {
	ctx, client, _ := setupTestClient(true /* createNewNamespaces */, &alwaysPassingPreconditionsValidator{})
	workUnit := prototest.Modify(baseCPUWorkUnit, func(wu *esspb.WorkUnit) {
		job := wu.Executable.GetJobset().Jobs[0]
		job.Spec.Resources = &esspb.ResourceSet{
			Resources: &esspb.ResourceSet_GpuResources{
				GpuResources: &esspb.GpuResources{
					Type:  "T4",
					Count: 2,
				},
			},
		}
		job.Spec.Args = map[string]string{"arg1": "val1"}
		job.Spec.EnvVars = map[string]string{"env1": "val1"}
		job.RetryLimit = 1
		job.Annotations = map[string]string{"anno1": "val1"}
	})
	namespace := "test-namespace"

	got, err := client.constructJobSet(ctx, workUnit, testTarget, "test-author", namespace, testTime, Priorities{})
	if err != nil {
		t.Fatalf("constructJobSet() returned unexpected error: %v", err)
	}

	if len(got.Spec.ReplicatedJobs) != 1 {
		t.Fatalf("Expected 1 replicated job, got %d", len(got.Spec.ReplicatedJobs))
	}
	podSpec := got.Spec.ReplicatedJobs[0].Template.Spec.Template.Spec
	if len(podSpec.Containers) != 1 {
		t.Fatalf("Expected 1 container, got %d", len(podSpec.Containers))
	}
	container := podSpec.Containers[0]

	// Check GPU limits
	gpuRes := container.Resources.Limits["nvidia.com/gpu"]
	wantGPURes := resource.MustParse("2")
	if gpuRes.Cmp(wantGPURes) != 0 {
		t.Errorf("Expected nvidia.com/gpu resource limit to be %v, got %v", wantGPURes, gpuRes)
	}

	// Check NodeSelector
	if podSpec.NodeSelector == nil {
		t.Fatal("Expected NodeSelector to be set, but it was nil")
	}
	wantNodeSelector := "nvidia-tesla-t4"
	if podSpec.NodeSelector["cloud.google.com/gke-accelerator"] != wantNodeSelector {
		t.Errorf("Expected NodeSelector cloud.google.com/gke-accelerator to be %q, got %q", wantNodeSelector, podSpec.NodeSelector["cloud.google.com/gke-accelerator"])
	}
}

func TestConstructJobSet_GPU_H100Mega(t *testing.T) {
	ctx, client, _ := setupTestClient(true /* createNewNamespaces */, &alwaysPassingPreconditionsValidator{})
	for _, gpuType := range []string{"nvidia-h100-mega-80gb", "H100_MEGA"} {
		t.Run(gpuType, func(t *testing.T) {
			workUnit := prototest.Modify(baseCPUWorkUnit, func(wu *esspb.WorkUnit) {
				job := wu.Executable.GetJobset().Jobs[0]
				job.Spec.Resources = &esspb.ResourceSet{
					Resources: &esspb.ResourceSet_GpuResources{
						GpuResources: &esspb.GpuResources{
							Type:  gpuType,
							Count: 8,
						},
					},
				}
			})

			got, err := client.constructJobSet(ctx, workUnit, testTarget, "test-author", "test-namespace", testTime, Priorities{})
			if err != nil {
				t.Fatalf("constructJobSet() for %q returned unexpected error: %v", gpuType, err)
			}

			podSpec := got.Spec.ReplicatedJobs[0].Template.Spec.Template.Spec
			container := podSpec.Containers[0]

			gpuRes := container.Resources.Limits["nvidia.com/gpu"]
			wantGPURes := resource.MustParse("8")
			if gpuRes.Cmp(wantGPURes) != 0 {
				t.Errorf("Expected nvidia.com/gpu resource limit to be %v, got %v", wantGPURes, gpuRes)
			}

			wantNodeSelector := "nvidia-h100-mega-80gb"
			if podSpec.NodeSelector["cloud.google.com/gke-accelerator"] != wantNodeSelector {
				t.Errorf("Expected NodeSelector cloud.google.com/gke-accelerator to be %q, got %q", wantNodeSelector, podSpec.NodeSelector["cloud.google.com/gke-accelerator"])
			}
		})
	}
}

func TestCreateJobSet(t *testing.T) {
	ctx, client, dynamicClient := setupTestClient(true /* createNewNamespaces */, &alwaysPassingPreconditionsValidator{})

	if err := client.createJobSet(ctx, testWorkUnitWithJob, testTarget, "test-author", testTime, Priorities{}); err != nil {
		t.Fatalf("createJobSet(%v, %q) returned unexpected error: %v", testWorkUnitWithJob, testTarget, err)
	}

	jobsets, err := dynamicClient.Resource(JobSetGVR).Namespace(testNamespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		t.Fatalf("Listing jobsets failed: %v", err)
	}
	if len(jobsets.Items) != 1 {
		t.Fatalf("Expected 1 jobset to be created, but got %d", len(jobsets.Items))
	}

	jobset := jobsets.Items[0]
	if jobset.GetName() != buildJobSetName(testTarget) {
		t.Errorf("JobSet has name %q, want %q", jobset.GetName(), buildJobSetName(testTarget))
	}
	if jobset.GetNamespace() != testNamespace {
		t.Errorf("JobSet has namespace %q, want %q", jobset.GetNamespace(), testNamespace)
	}
}

func TestCreateJobSet_CreationFails(t *testing.T) {
	ctx, client, dynamicClient := setupTestClient(true /* createNewNamespaces */, &alwaysPassingPreconditionsValidator{})

	dynamicClient.PrependReactor("create", "jobsets", func(action clientgotesting.Action) (handled bool, ret runtime.Object, err error) {
		return true, nil, fmt.Errorf("simulated creation failure")
	})

	err := client.createJobSet(ctx, testWorkUnitWithJob, testTarget, "test-author", testTime, Priorities{})
	if err == nil {
		t.Fatalf("createJobSet(%v, %q) succeeded, want error", testWorkUnitWithJob, testTarget)
	}

	wantErrMsg := "failed to create JobSet \"xmc-operation-123-456\" in Kubernetes: simulated creation failure"
	if err.Error() != wantErrMsg {
		t.Errorf("createJobSet(%v, %q) returned error %q, want %q", testWorkUnitWithJob, testTarget, err.Error(), wantErrMsg)
	}

	jobsets, err := dynamicClient.Resource(JobSetGVR).Namespace(testNamespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		t.Fatalf("Listing jobsets failed: %v", err)
	}
	if len(jobsets.Items) != 0 {
		t.Errorf("Expected 0 jobsets to be created, but got %d", len(jobsets.Items))
	}
}

func TestDeleteJobSet(t *testing.T) {
	ctx, client, dynamicClient := setupTestClient(true /* createNewNamespaces */, &alwaysPassingPreconditionsValidator{})

	createTestJobSet(ctx, t, client, testNamespace, testTarget, map[string]string{constants.LastEditLabel: strconv.FormatInt(testTime.Unix(), 10)})

	jobsets, err := dynamicClient.Resource(JobSetGVR).Namespace(testNamespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		t.Fatalf("Listing jobsets failed: %v", err)
	}
	if len(jobsets.Items) != 1 {
		t.Fatalf("Expected 1 jobset to be created, but got %d", len(jobsets.Items))
	}

	deleted, err := client.deleteJobSet(ctx, testTarget, testNamespace, testTime)
	if err != nil {
		t.Fatalf("deleteJobSet(%v) returned unexpected error: %v", testTarget, err)
	}
	if !deleted {
		t.Errorf("deleteJobSet(%v) returned false, want true", testTarget)
	}

	jobsets, err = dynamicClient.Resource(JobSetGVR).Namespace(testNamespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		t.Fatalf("Listing jobsets failed: %v", err)
	}
	if len(jobsets.Items) != 0 {
		t.Errorf("Expected 0 jobsets after deletion, but got %d", len(jobsets.Items))
	}
}

func TestDeleteJobSet_JobSetNotFound(t *testing.T) {
	ctx, client, _ := setupTestClient(true /* createNewNamespaces */, &alwaysPassingPreconditionsValidator{})

	deleted, err := client.deleteJobSet(ctx, testTarget, testNamespace, testTime)
	if err != nil {
		t.Fatalf("deleteJobSet(%v) returned unexpected error: %v", testTarget, err)
	}
	if deleted {
		t.Errorf("deleteJobSet(%v) returned true, want false", testTarget)
	}
}

func TestDeleteJobSet_DeletionFails(t *testing.T) {
	ctx, client, dynamicClient := setupTestClient(true /* createNewNamespaces */, &alwaysPassingPreconditionsValidator{})

	createTestJobSet(ctx, t, client, testNamespace, testTarget, map[string]string{constants.LastEditLabel: strconv.FormatInt(testTime.Unix(), 10)})

	dynamicClient.PrependReactor("delete", "jobsets", func(action clientgotesting.Action) (handled bool, ret runtime.Object, err error) {
		return true, nil, fmt.Errorf("simulated deletion failure")
	})

	_, err := client.deleteJobSet(ctx, testTarget, testNamespace, testTime)
	if err == nil {
		t.Fatalf("deleteJobSet(%v) succeeded, want error", testTarget)
	}

	wantErrMsg := "failed to delete JobSet \"xmc-operation-123-456\" in namespace \"test-queue\": simulated deletion failure"
	if err.Error() != wantErrMsg {
		t.Errorf("deleteJobSet(%v) returned error %q, want %q", testTarget, err.Error(), wantErrMsg)
	}
}

func TestDeleteJobSet_DeletionFailsWithNotFoundWithNoError(t *testing.T) {
	ctx, client, dynamicClient := setupTestClient(true /* createNewNamespaces */, &alwaysPassingPreconditionsValidator{})

	createTestJobSet(ctx, t, client, testNamespace, testTarget, map[string]string{constants.LastEditLabel: strconv.FormatInt(testTime.Unix(), 10)})

	dynamicClient.PrependReactor("delete", "jobsets", func(action clientgotesting.Action) (handled bool, ret runtime.Object, err error) {
		return true, nil, apierrors.NewNotFound(schema.GroupResource{Group: "jobset.x-k8s.io", Resource: "jobsets"}, "xmc-operation-123-456")
	})

	deleted, err := client.deleteJobSet(ctx, testTarget, testNamespace, testTime)
	if err != nil {
		t.Fatalf("deleteJobSet(%v) returned unexpected error: %v", testTarget, err)
	}
	if deleted {
		t.Errorf("deleteJobSet(%v) returned true, want false", testTarget)
	}
}

func TestDeleteJobSet_WaitGetFails(t *testing.T) {
	ctx, client, dynamicClient := setupTestClient(true /* createNewNamespaces */, &alwaysPassingPreconditionsValidator{})

	jobSetName := buildJobSetName(testTarget)

	createTestJobSet(ctx, t, client, testNamespace, testTarget, map[string]string{constants.LastEditLabel: strconv.FormatInt(testTime.Unix(), 10)})

	dynamicClient.PrependReactor("delete", "jobsets", func(action clientgotesting.Action) (handled bool, ret runtime.Object, err error) {
		return true, nil, nil
	})
	getCallCount := 0
	dynamicClient.PrependReactor("get", "jobsets", func(action clientgotesting.Action) (handled bool, ret runtime.Object, err error) {
		getCallCount++
		if getCallCount <= 2 {
			return false, nil, nil
		}
		// The third GET call is the first one in PollImmediate.
		// We make it fail to test if deleteJobSet handles errors from PollImmediate.
		return true, nil, fmt.Errorf("simulated get failure")
	})

	_, err := client.deleteJobSet(ctx, testTarget, testNamespace, testTime)
	if err == nil {
		t.Fatalf("deleteJobSet(%v) succeeded, want error", testTarget)
	}

	wantErrMsg := fmt.Sprintf("failed while waiting for jobset %q to be deleted: simulated get failure", jobSetName)
	if err.Error() != wantErrMsg {
		t.Errorf("deleteJobSet(%v) returned error %q, want %q", testTarget, err.Error(), wantErrMsg)
	}
}

func TestRestartJobSet(t *testing.T) {
	ctx, client, dynamicClient := setupTestClient(true /* createNewNamespaces */, &alwaysPassingPreconditionsValidator{})

	workUnit := createTestWorkUnit(testNamespace)
	workUnit.CurrentWorkUnitExecution = &esspb.WorkUnitExecution{Name: "experiments/123/workUnits/456/executions/2"}

	createTestJobSet(ctx, t, client, testNamespace, testTarget, nil)

	jobsets, err := dynamicClient.Resource(JobSetGVR).Namespace(testNamespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		t.Fatalf("Listing jobsets failed: %v", err)
	}
	if len(jobsets.Items) != 1 {
		t.Fatalf("Expected 1 jobset to be created, but got %d", len(jobsets.Items))
	}

	if err := client.restartJobSet(ctx, workUnit, testTarget, "test-author", testTime, Priorities{}); err != nil {
		t.Fatalf("restartJobSet(%v, %q) returned unexpected error: %v", workUnit, testTarget, err)
	}

	jobsets, err = dynamicClient.Resource(JobSetGVR).Namespace(testNamespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		t.Fatalf("Listing jobsets failed: %v", err)
	}
	if len(jobsets.Items) != 1 {
		t.Errorf("Expected 1 jobset after restart, but got %d", len(jobsets.Items))
	}
}

func TestRestartJobSet_DeletionFails(t *testing.T) {
	ctx, client, dynamicClient := setupTestClient(true /* createNewNamespaces */, &alwaysPassingPreconditionsValidator{})

	workUnit := createTestWorkUnit(testNamespace)
	workUnit.CurrentWorkUnitExecution = &esspb.WorkUnitExecution{Name: "experiments/123/workUnits/456/executions/2"}

	createTestJobSet(ctx, t, client, testNamespace, testTarget, nil)

	dynamicClient.PrependReactor("delete", "jobsets", func(action clientgotesting.Action) (handled bool, ret runtime.Object, err error) {
		return true, nil, fmt.Errorf("simulated deletion failure")
	})

	err := client.restartJobSet(ctx, workUnit, testTarget, "test-author", testTime, Priorities{})
	if err == nil {
		t.Fatalf("restartJobSet(%v, %q) succeeded, want error", workUnit, testTarget)
	}

	wantErrMsg := "failed to create JobSet \"xmc-operation-123-456\" in Kubernetes during restart: failed to create JobSet \"xmc-operation-123-456\" in Kubernetes: jobsets.jobset.x-k8s.io \"xmc-operation-123-456\" already exists"
	if err.Error() != wantErrMsg {
		t.Errorf("restartJobSet(%v, %q) returned error %q, want %q", workUnit, testTarget, err.Error(), wantErrMsg)
	}
}

func TestRestartJobSet_RecreationFails(t *testing.T) {
	ctx, client, dynamicClient := setupTestClient(true /* createNewNamespaces */, &alwaysPassingPreconditionsValidator{})

	workUnit := createTestWorkUnit(testNamespace)
	workUnit.CurrentWorkUnitExecution = &esspb.WorkUnitExecution{Name: "experiments/123/workUnits/456/executions/2"}

	createTestJobSet(ctx, t, client, testNamespace, testTarget, nil)

	dynamicClient.PrependReactor("create", "jobsets", func(action clientgotesting.Action) (handled bool, ret runtime.Object, err error) {
		// The first create call is from setup, ignore it.
		return true, nil, fmt.Errorf("simulated creation failure")
	})

	err := client.restartJobSet(ctx, workUnit, testTarget, "test-author", testTime, Priorities{})
	if err == nil {
		t.Fatalf("restartJobSet(%v, %q) succeeded, want error", workUnit, testTarget)
	}

	wantErrMsg := "failed to create JobSet \"xmc-operation-123-456\" in Kubernetes during restart: failed to create JobSet \"xmc-operation-123-456\" in Kubernetes: simulated creation failure"
	if err.Error() != wantErrMsg {
		t.Errorf("restartJobSet(%v, %q) returned error %q, want %q", workUnit, testTarget, err.Error(), wantErrMsg)
	}
}

func TestRestartJobSet_PopulatesLastEdit(t *testing.T) {
	target := "experiments/111/workUnits/555/executions/1"
	fix := setupPopulateTest(t, target)
	client, now := fix.client, fix.timestamp
	ctx := t.Context()
	namespace := testNamespace
	newTarget := "experiments/111/workUnits/555/executions/2"
	jobSetName := buildJobSetName(newTarget)
	workUnit := createTestWorkUnit(namespace)
	workUnit.CurrentWorkUnitExecution = &esspb.WorkUnitExecution{Name: newTarget}

	if err := client.restartJobSet(ctx, workUnit, target, "test-author", now, Priorities{}); err != nil {
		t.Fatalf("restartJobSet(%v) returned unexpected error: %v", target, err)
	}

	jobSetAfter, err := client.dynamicClient.Resource(JobSetGVR).Namespace(namespace).Get(ctx, jobSetName, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("Getting jobset after restartJobSet failed: %v", err)
	}

	labelsAfter := jobSetAfter.GetLabels()
	if labelsAfter[constants.LastEditLabel] != strconv.FormatInt(now.Unix(), 10) {
		t.Errorf("Expected last-edit label to be %q, but it is %q", strconv.FormatInt(now.Unix(), 10), labelsAfter[constants.LastEditLabel])
	}
}

func TestRestartJobSet_PopulatesPriority(t *testing.T) {
	target := "experiments/111/workUnits/555/executions/1"
	fix := setupPopulateTest(t, target)
	client, now := fix.client, fix.timestamp
	ctx := t.Context()
	namespace := testNamespace
	newTarget := "experiments/111/workUnits/555/executions/2"
	jobSetName := buildJobSetName(newTarget)
	workUnit := createTestWorkUnit(namespace)
	workUnit.CurrentWorkUnitExecution = &esspb.WorkUnitExecution{Name: newTarget}

	priorities := Priorities{
		WorkUnitPriority:   esspb.Priority_HIGH,
		ExperimentPriority: esspb.Priority_NORMAL,
	}
	if err := client.restartJobSet(ctx, workUnit, target, "test-author", now, priorities); err != nil {
		t.Fatalf("restartJobSet(%v) returned unexpected error: %v", target, err)
	}

	jobSetAfter, err := client.dynamicClient.Resource(JobSetGVR).Namespace(namespace).Get(ctx, jobSetName, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("Getting jobset after restartJobSet failed: %v", err)
	}

	labelsAfter := jobSetAfter.GetLabels()
	if labelsAfter[kueuePriorityClassLabel] != "exp-medium-wu-high" {
		t.Errorf("Expected kueuePriorityClassLabel to be %q, but it is %q", "exp-medium-wu-high", labelsAfter[kueuePriorityClassLabel])
	}
}

func TestDeleteJobSet_whenValidatorRejectsTheRequest_ShouldDiscard(t *testing.T) {
	ctx, client, dynamicClient := setupTestClient(true /* createNewNamespaces */, &alwaysFailingPreconditionsValidator{})

	createTestJobSet(ctx, t, client, testNamespace, testTarget, map[string]string{constants.LastEditLabel: strconv.FormatInt(testTime.Unix(), 10)})

	jobsets, err := dynamicClient.Resource(JobSetGVR).Namespace(testNamespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		t.Fatalf("Listing jobsets failed: %v", err)
	}
	if len(jobsets.Items) != 1 {
		t.Fatalf("Expected 1 jobset to be created, but got %d", len(jobsets.Items))
	}

	deleted, err := client.deleteJobSet(ctx, testTarget, testNamespace, testTime)
	if err != nil {
		t.Fatalf("deleteJobSet(%v) returned unexpected error: %v", testTarget, err)
	}
	if deleted {
		t.Errorf("deleteJobSet(%v) returned true, want false", testTarget)
	}

	jobsets, err = dynamicClient.Resource(JobSetGVR).Namespace(testNamespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		t.Fatalf("Listing jobsets failed: %v", err)
	}
	if len(jobsets.Items) != 1 {
		t.Errorf("Expected 1 jobset after deletion, but got %d", len(jobsets.Items))
	}
}

func TestRestartJobSet_whenValidatorRejectsTheRequest_ShouldDiscard(t *testing.T) {
	ctx, client, dynamicClient := setupTestClient(true /* createNewNamespaces */, &alwaysFailingPreconditionsValidator{})

	workUnit := createTestWorkUnit(testNamespace)
	workUnit.CurrentWorkUnitExecution = &esspb.WorkUnitExecution{Name: "experiments/123/workUnits/456/executions/2"}

	createTestJobSet(ctx, t, client, testNamespace, testTarget, nil)

	jobsets, err := dynamicClient.Resource(JobSetGVR).Namespace(testNamespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		t.Fatalf("Listing jobsets failed: %v", err)
	}
	if len(jobsets.Items) != 1 {
		t.Fatalf("Expected 1 jobset to be created, but got %d", len(jobsets.Items))
	}

	if err := client.restartJobSet(ctx, workUnit, testTarget, "test-author", testTime, Priorities{}); err != nil {
		t.Fatalf("restartJobSet(%v, %q) returned unexpected error: %v", workUnit, testTarget, err)
	}

	jobsets, err = dynamicClient.Resource(JobSetGVR).Namespace(testNamespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		t.Fatalf("Listing jobsets failed: %v", err)
	}
	if len(jobsets.Items) != 1 {
		t.Errorf("Expected 1 jobset after restart, but got %d", len(jobsets.Items))
	}
	if jobsets.Items[0].GetLabels()[constants.ExecutionIDLabel] != "1" {
		t.Errorf("Expected execution ID to be 1, but got %s", jobsets.Items[0].GetLabels()[constants.ExecutionIDLabel])
	}
}

func TestRestartJobSet_NamespaceFallback(t *testing.T) {
	target := "experiments/111/workUnits/555/executions/1"
	fix := setupPopulateTest(t, target)
	client, dynamicClient, now := fix.client, fix.dynamicClient, fix.timestamp
	ctx := t.Context()

	newTarget := "experiments/111/workUnits/555/executions/2"
	workUnit := createTestWorkUnit("" /* namespace */)
	workUnit.Executable.SchedulingConstraints = nil
	workUnit.CurrentWorkUnitExecution = &esspb.WorkUnitExecution{Name: newTarget}

	if err := client.restartJobSet(ctx, workUnit, target, "test-author", now, Priorities{}); err != nil {
		t.Fatalf("restartJobSet(%v) returned unexpected error: %v", target, err)
	}

	jobsets, err := dynamicClient.Resource(JobSetGVR).Namespace(testNamespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		t.Fatalf("Listing jobsets failed: %v", err)
	}
	if len(jobsets.Items) != 1 {
		t.Errorf("Expected 1 jobset after restart, but got %d", len(jobsets.Items))
	}
}

func TestUpdateJobSetPriority(t *testing.T) {
	target := "experiments/111/workUnits/222/executions/1"
	fix := setupPopulateTest(t, target)
	client := fix.client
	ctx := t.Context()
	jobSetName := buildJobSetName(target)

	jobSetBefore, err := client.dynamicClient.Resource(JobSetGVR).Namespace(testNamespace).Get(ctx, jobSetName, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("Getting jobset before updateJobSetPriority failed: %v", err)
	}
	labelsBefore := jobSetBefore.GetLabels()

	if labelsBefore[kueuePriorityClassLabel] != "exp-low-wu-low" {
		t.Errorf("Expected priority class label to be %q, but it is %q", "exp-low-wu-low", labelsBefore[kueuePriorityClassLabel])
	}

	expectedPriorities := Priorities{
		WorkUnitPriority:   esspb.Priority_HIGH,
		ExperimentPriority: esspb.Priority_NORMAL,
	}

	if err := client.updateJobSetPriority(ctx, target, expectedPriorities); err != nil {
		t.Fatalf("updateJobSetPriority(%v) returned unexpected error: %v", target, err)
	}

	jobSetAfter, err := client.dynamicClient.Resource(JobSetGVR).Namespace(testNamespace).Get(ctx, jobSetName, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("Getting jobset after updateJobSetPriority failed: %v", err)
	}

	labelsAfter := jobSetAfter.GetLabels()
	if labelsAfter[kueuePriorityClassLabel] != "exp-medium-wu-high" {
		t.Errorf("Expected priority class label to be %q, but it is %q", "exp-medium-wu-high", labelsAfter[kueuePriorityClassLabel])
	}
}

func TestUpdateJobSetNotifyWhen(t *testing.T) {
	target := "experiments/111/workUnits/222/executions/1"
	fix := setupPopulateTest(t, target)
	client := fix.client
	ctx := t.Context()
	jobSetName := buildJobSetName(target)

	notifyWhen := "STARTING.RUNNING.COMPLETED"
	if err := client.updateJobSetNotifyWhen(ctx, baseCPUWorkUnit, target, notifyWhen); err != nil {
		t.Fatalf("updateJobSetNotifyWhen(%v) returned unexpected error: %v", target, err)
	}

	jobSetAfter, err := client.dynamicClient.Resource(JobSetGVR).Namespace(testNamespace).Get(ctx, jobSetName, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("Getting jobset after updateJobSetNotifyWhen failed: %v", err)
	}

	annotationsAfter := jobSetAfter.GetAnnotations()
	if annotationsAfter[constants.NotifyWhenAnnotation] != notifyWhen {
		t.Errorf("Expected notify-when annotation to be %q, but got %q", notifyWhen, annotationsAfter[constants.NotifyWhenAnnotation])
	}
}

func TestUpdateJobSetNotifyWhen_NotFound(t *testing.T) {
	ctx, client, _ := setupTestClient(true /* createNewNamespaces */, &alwaysPassingPreconditionsValidator{})
	target := "experiments/999/workUnits/888/executions/1"

	err := client.updateJobSetNotifyWhen(ctx, baseCPUWorkUnit, target, "STARTING.RUNNING")
	if err == nil {
		t.Fatalf("expected error when JobSet does not exist, got nil")
	}
	if !apierrors.IsNotFound(err) {
		t.Errorf("expected apierrors.IsNotFound(err) to be true, got %v", err)
	}
}

func TestGetPriorityClassName(t *testing.T) {
	tests := []struct {
		priorities Priorities
		want       string
	}{
		// Priorities{WorkUnitPriority, ExperimentPriority}
		{Priorities{esspb.Priority_PRIORITY_UNSPECIFIED, esspb.Priority_PRIORITY_UNSPECIFIED}, "exp-low-wu-low"},
		{Priorities{esspb.Priority_LOW, esspb.Priority_PRIORITY_UNSPECIFIED}, "exp-low-wu-low"},
		{Priorities{esspb.Priority_NORMAL, esspb.Priority_PRIORITY_UNSPECIFIED}, "exp-low-wu-medium"},
		{Priorities{esspb.Priority_HIGH, esspb.Priority_PRIORITY_UNSPECIFIED}, "exp-low-wu-high"},
		{Priorities{esspb.Priority_PRIORITY_UNSPECIFIED, esspb.Priority_LOW}, "exp-low-wu-low"},
		{Priorities{esspb.Priority_LOW, esspb.Priority_LOW}, "exp-low-wu-low"},
		{Priorities{esspb.Priority_NORMAL, esspb.Priority_LOW}, "exp-low-wu-medium"},
		{Priorities{esspb.Priority_HIGH, esspb.Priority_LOW}, "exp-low-wu-high"},
		{Priorities{esspb.Priority_PRIORITY_UNSPECIFIED, esspb.Priority_NORMAL}, "exp-medium-wu-low"},
		{Priorities{esspb.Priority_LOW, esspb.Priority_NORMAL}, "exp-medium-wu-low"},
		{Priorities{esspb.Priority_NORMAL, esspb.Priority_NORMAL}, "exp-medium-wu-medium"},
		{Priorities{esspb.Priority_HIGH, esspb.Priority_NORMAL}, "exp-medium-wu-high"},
		{Priorities{esspb.Priority_PRIORITY_UNSPECIFIED, esspb.Priority_HIGH}, "exp-high-wu-low"},
		{Priorities{esspb.Priority_LOW, esspb.Priority_HIGH}, "exp-high-wu-low"},
		{Priorities{esspb.Priority_NORMAL, esspb.Priority_HIGH}, "exp-high-wu-medium"},
		{Priorities{esspb.Priority_HIGH, esspb.Priority_HIGH}, "exp-high-wu-high"},
	}

	for _, tc := range tests {
		t.Run(fmt.Sprintf("WU_%v_EXP_%v", tc.priorities.WorkUnitPriority, tc.priorities.ExperimentPriority), func(t *testing.T) {
			got := getPriorityClassName(tc.priorities)
			if got != tc.want {
				t.Errorf("getPriorityClassName(%v) = %q, want %q", tc.priorities, got, tc.want)
			}
		})
	}
}

func TestConstructJobSet_GPU_MultiReplica(t *testing.T) {
	ctx, client, _ := setupTestClient(true /* createNewNamespaces */, &alwaysPassingPreconditionsValidator{})
	workUnit := prototest.Modify(baseCPUWorkUnit, func(wu *esspb.WorkUnit) {
		job := wu.Executable.GetJobset().Jobs[0]
		job.Spec.Resources = &esspb.ResourceSet{
			Resources: &esspb.ResourceSet_GpuResources{
				GpuResources: &esspb.GpuResources{
					Type:  "H100",
					Count: 8,
				},
			},
		}
		job.Replicas = 4
	})

	got, err := client.constructJobSet(ctx, workUnit, testTarget, "test-author", "test-namespace", testTime, Priorities{})
	if err != nil {
		t.Fatalf("constructJobSet() returned unexpected error: %v", err)
	}

	if len(got.Spec.ReplicatedJobs) != 1 {
		t.Fatalf("Expected 1 replicated job, got %d", len(got.Spec.ReplicatedJobs))
	}
	rj := got.Spec.ReplicatedJobs[0]

	if rj.Replicas != 1 {
		t.Errorf("Expected ReplicatedJob.Replicas to be reset to 1, got %d", rj.Replicas)
	}
	if rj.Template.Spec.Parallelism == nil || *rj.Template.Spec.Parallelism != 4 {
		t.Errorf("Expected Parallelism=4, got %v", rj.Template.Spec.Parallelism)
	}
	if rj.Template.Spec.Completions == nil || *rj.Template.Spec.Completions != 4 {
		t.Errorf("Expected Completions=4, got %v", rj.Template.Spec.Completions)
	}
	if rj.Template.Spec.CompletionMode == nil || *rj.Template.Spec.CompletionMode != batchv1.IndexedCompletion {
		t.Errorf("Expected CompletionMode=Indexed, got %v", rj.Template.Spec.CompletionMode)
	}
}

func TestConstructJobSet_GPU_DefaultReplica(t *testing.T) {
	ctx, client, _ := setupTestClient(true /* createNewNamespaces */, &alwaysPassingPreconditionsValidator{})
	workUnit := prototest.Modify(baseCPUWorkUnit, func(wu *esspb.WorkUnit) {
		wu.Executable.GetJobset().Jobs[0].Spec.Resources = &esspb.ResourceSet{
			Resources: &esspb.ResourceSet_GpuResources{
				GpuResources: &esspb.GpuResources{
					Type:  "T4",
					Count: 1,
				},
			},
		}
	})

	got, err := client.constructJobSet(ctx, workUnit, testTarget, "test-author", "test-namespace", testTime, Priorities{})
	if err != nil {
		t.Fatalf("constructJobSet() returned unexpected error: %v", err)
	}

	rj := got.Spec.ReplicatedJobs[0]

	if rj.Replicas != 1 {
		t.Errorf("Expected ReplicatedJob.Replicas=1, got %d", rj.Replicas)
	}
	if rj.Template.Spec.Parallelism == nil || *rj.Template.Spec.Parallelism != 1 {
		t.Errorf("Expected Parallelism=1, got %v", rj.Template.Spec.Parallelism)
	}
	if rj.Template.Spec.Completions == nil || *rj.Template.Spec.Completions != 1 {
		t.Errorf("Expected Completions=1, got %v", rj.Template.Spec.Completions)
	}
	if rj.Template.Spec.CompletionMode == nil || *rj.Template.Spec.CompletionMode != batchv1.IndexedCompletion {
		t.Errorf("Expected CompletionMode=Indexed, got %v", rj.Template.Spec.CompletionMode)
	}
}

func TestConstructJobSet_TPU_SingleHost(t *testing.T) {
	ctx, client, _ := setupTestClient(true /* createNewNamespaces */, &alwaysPassingPreconditionsValidator{})
	workUnit := prototest.Modify(baseCPUWorkUnit, func(wu *esspb.WorkUnit) {
		wu.Executable.GetJobset().Jobs[0].Spec.Resources = &esspb.ResourceSet{
			Resources: &esspb.ResourceSet_TpuResources{
				TpuResources: &esspb.TpuResources{
					Type:     "tpu-v6e-slice",
					Topology: "2x2",
				},
			},
		}
	})

	got, err := client.constructJobSet(ctx, workUnit, testTarget, "test-author", "test-namespace", testTime, Priorities{})
	if err != nil {
		t.Fatalf("constructJobSet() returned unexpected error: %v", err)
	}

	rj := got.Spec.ReplicatedJobs[0]

	if rj.Template.Spec.Parallelism == nil || *rj.Template.Spec.Parallelism != 1 {
		t.Errorf("Expected Parallelism=1, got %v", rj.Template.Spec.Parallelism)
	}
	if rj.Template.Spec.Completions == nil || *rj.Template.Spec.Completions != 1 {
		t.Errorf("Expected Completions=1, got %v", rj.Template.Spec.Completions)
	}
	if rj.Template.Spec.CompletionMode == nil || *rj.Template.Spec.CompletionMode != batchv1.IndexedCompletion {
		t.Errorf("Expected CompletionMode=Indexed, got %v", rj.Template.Spec.CompletionMode)
	}

	podSpec := rj.Template.Spec.Template.Spec
	if podSpec.NodeSelector["cloud.google.com/gke-tpu-accelerator"] != "tpu-v6e-slice" {
		t.Errorf("Expected tpu-accelerator NodeSelector=%q, got %q", "tpu-v6e-slice", podSpec.NodeSelector["cloud.google.com/gke-tpu-accelerator"])
	}
	if podSpec.NodeSelector["cloud.google.com/gke-tpu-topology"] != "2x2" {
		t.Errorf("Expected tpu-topology NodeSelector=%q, got %q", "2x2", podSpec.NodeSelector["cloud.google.com/gke-tpu-topology"])
	}

	tpuRes := podSpec.Containers[0].Resources.Limits["google.com/tpu"]
	wantTPU := resource.MustParse("4")
	if tpuRes.Cmp(wantTPU) != 0 {
		t.Errorf("Expected google.com/tpu limit=%v, got %v", wantTPU, tpuRes)
	}

	if len(podSpec.Containers[0].Ports) != 2 || podSpec.Containers[0].Ports[0].ContainerPort != 8471 || podSpec.Containers[0].Ports[1].ContainerPort != 8431 {
		t.Errorf("Expected TPU container ports [8471, 8431], got %v", podSpec.Containers[0].Ports)
	}

	if _, hasAnnotation := got.Annotations["alpha.jobset.sigs.k8s.io/exclusive-topology"]; hasAnnotation {
		t.Error("Single-host TPU should NOT have exclusive-topology annotation")
	}
}

func TestConstructJobSet_TPU_MultiHost(t *testing.T) {
	ctx, client, _ := setupTestClient(true /* createNewNamespaces */, &alwaysPassingPreconditionsValidator{})
	workUnit := prototest.Modify(baseCPUWorkUnit, func(wu *esspb.WorkUnit) {
		wu.Executable.GetJobset().Jobs[0].Spec.Resources = &esspb.ResourceSet{
			Resources: &esspb.ResourceSet_TpuResources{
				TpuResources: &esspb.TpuResources{
					Type:     "tpu-v4-podslice",
					Topology: "2x2x4",
				},
			},
		}
	})

	got, err := client.constructJobSet(ctx, workUnit, testTarget, "test-author", "test-namespace", testTime, Priorities{})
	if err != nil {
		t.Fatalf("constructJobSet() returned unexpected error: %v", err)
	}

	rj := got.Spec.ReplicatedJobs[0]

	if rj.Template.Spec.Parallelism == nil || *rj.Template.Spec.Parallelism != 4 {
		t.Errorf("Expected Parallelism=4, got %v", rj.Template.Spec.Parallelism)
	}
	if rj.Template.Spec.Completions == nil || *rj.Template.Spec.Completions != 4 {
		t.Errorf("Expected Completions=4, got %v", rj.Template.Spec.Completions)
	}
	if rj.Template.Spec.CompletionMode == nil || *rj.Template.Spec.CompletionMode != batchv1.IndexedCompletion {
		t.Errorf("Expected CompletionMode=Indexed, got %v", rj.Template.Spec.CompletionMode)
	}

	podSpec := rj.Template.Spec.Template.Spec
	tpuRes := podSpec.Containers[0].Resources.Limits["google.com/tpu"]
	wantTPU := resource.MustParse("4")
	if tpuRes.Cmp(wantTPU) != 0 {
		t.Errorf("Expected google.com/tpu limit=%v, got %v", wantTPU, tpuRes)
	}

	if len(podSpec.Containers[0].Ports) != 2 || podSpec.Containers[0].Ports[0].ContainerPort != 8471 || podSpec.Containers[0].Ports[1].ContainerPort != 8431 {
		t.Errorf("Expected TPU container ports [8471, 8431], got %v", podSpec.Containers[0].Ports)
	}

	wantAnnotation := "cloud.google.com/gke-nodepool"
	if got.Annotations["alpha.jobset.sigs.k8s.io/exclusive-topology"] != wantAnnotation {
		t.Errorf("Expected exclusive-topology annotation=%q, got %q", wantAnnotation, got.Annotations["alpha.jobset.sigs.k8s.io/exclusive-topology"])
	}
}

func TestConstructJobSet_TPU_MultiSlice(t *testing.T) {
	ctx, client, _ := setupTestClient(true /* createNewNamespaces */, &alwaysPassingPreconditionsValidator{})
	workUnit := prototest.Modify(baseCPUWorkUnit, func(wu *esspb.WorkUnit) {
		job := wu.Executable.GetJobset().Jobs[0]
		job.Spec.Resources = &esspb.ResourceSet{
			Resources: &esspb.ResourceSet_TpuResources{
				TpuResources: &esspb.TpuResources{
					Type:     "tpu-v4-podslice",
					Topology: "2x2x4",
				},
			},
		}
		job.Replicas = 2
	})

	got, err := client.constructJobSet(ctx, workUnit, testTarget, "test-author", "test-namespace", testTime, Priorities{})
	if err != nil {
		t.Fatalf("constructJobSet() returned unexpected error: %v", err)
	}

	rj := got.Spec.ReplicatedJobs[0]

	if rj.Replicas != 2 {
		t.Errorf("Expected ReplicatedJob.Replicas to stay at 2 for multi-slice TPU, got %d", rj.Replicas)
	}
	if rj.Template.Spec.Parallelism == nil || *rj.Template.Spec.Parallelism != 4 {
		t.Errorf("Expected Parallelism=4 (nodes per slice), got %v", rj.Template.Spec.Parallelism)
	}
	if rj.Template.Spec.Completions == nil || *rj.Template.Spec.Completions != 4 {
		t.Errorf("Expected Completions=4 (nodes per slice), got %v", rj.Template.Spec.Completions)
	}
	if rj.Template.Spec.CompletionMode == nil || *rj.Template.Spec.CompletionMode != batchv1.IndexedCompletion {
		t.Errorf("Expected CompletionMode=Indexed, got %v", rj.Template.Spec.CompletionMode)
	}

	wantAnnotation := "cloud.google.com/gke-nodepool"
	if got.Annotations["alpha.jobset.sigs.k8s.io/exclusive-topology"] != wantAnnotation {
		t.Errorf("Expected exclusive-topology annotation=%q, got %q", wantAnnotation, got.Annotations["alpha.jobset.sigs.k8s.io/exclusive-topology"])
	}
}

func TestConstructJobSet_PriorityClassName(t *testing.T) {
	ctx, client, _ := setupTestClient(true /* createNewNamespaces */, &alwaysPassingPreconditionsValidator{})
	workUnit := prototest.Modify(baseCPUWorkUnit, func(wu *esspb.WorkUnit) {
		wu.Name = "workUnitPriorityClassName"
		job1 := wu.Executable.GetJobset().Jobs[0]
		job1.Name = "pathways-head"
		job1.PriorityClassName = "very-high"

		job2 := proto.Clone(job1).(*esspb.KubernetesJob)
		job2.Name = "worker"
		job2.PriorityClassName = ""

		wu.Executable.GetJobset().Jobs = []*esspb.KubernetesJob{job1, job2}
	})

	priorities := Priorities{
		ExperimentPriority: esspb.Priority_HIGH,
		WorkUnitPriority:   esspb.Priority_NORMAL,
	}
	got, err := client.constructJobSet(ctx, workUnit, testTarget, "test-author", "test-namespace", testTime, priorities)
	if err != nil {
		t.Fatalf("constructJobSet() returned unexpected error: %v", err)
	}

	if got.ObjectMeta.Labels[kueuePriorityClassLabel] != "exp-high-wu-medium" {
		t.Errorf("Expected kueuePriorityClassLabel to be %q, got %q", "exp-high-wu-medium", got.ObjectMeta.Labels[kueuePriorityClassLabel])
	}
	if len(got.Spec.ReplicatedJobs) != 2 {
		t.Fatalf("Expected 2 replicated jobs, got %d", len(got.Spec.ReplicatedJobs))
	}
	if got.Spec.ReplicatedJobs[0].Template.Spec.Template.Spec.PriorityClassName != "very-high" {
		t.Errorf("Expected pathways-head PriorityClassName %q, got %q", "very-high", got.Spec.ReplicatedJobs[0].Template.Spec.Template.Spec.PriorityClassName)
	}
	if got.Spec.ReplicatedJobs[1].Template.Spec.Template.Spec.PriorityClassName != "" {
		t.Errorf("Expected worker PriorityClassName %q, got %q", "", got.Spec.ReplicatedJobs[1].Template.Spec.Template.Spec.PriorityClassName)
	}
}

func TestConstructJobSet_ManagedXprofGCSInjection(t *testing.T) {
	// The actuator injects the GCS destination purely from the MANAGED_XPROF_GCS_PATH
	// env var, decoupled from the ManagedXprof artifact or any "requested" env flag.
	tests := []struct {
		name          string
		gcsPathEnv    string
		wantGCSObject string
	}{
		{
			name:          "gcs path env present injects GCS env",
			gcsPathEnv:    "gs://my-awesome-profile-bucket",
			wantGCSObject: "gs://my-awesome-profile-bucket/xmc-123-456-1764149578",
		},
		{
			name: "gcs path env absent skips GCS env",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ctx, client, _ := setupTestClient(true /* createNewNamespaces */, &alwaysPassingPreconditionsValidator{})
			if tc.gcsPathEnv != "" {
				t.Setenv(constants.XmcEnvVarManagedXprofGCSPath, tc.gcsPathEnv)
			}

			workUnit := &esspb.WorkUnit{
				Name: "workUnit1",
				Executable: &esspb.Executable{
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
			}

			got, err := client.constructJobSet(ctx, workUnit, testTarget, "test-author", constants.XmcLabelValue, testTime, Priorities{})
			if err != nil {
				t.Fatalf("constructJobSet() returned unexpected error: %v", err)
			}
			if len(got.Spec.ReplicatedJobs) != 1 {
				t.Fatalf("Expected 1 replicated job, got %d", len(got.Spec.ReplicatedJobs))
			}
			podSpec := got.Spec.ReplicatedJobs[0].Template.Spec.Template.Spec
			if len(podSpec.Containers) != 1 {
				t.Fatalf("Expected 1 container, got %d", len(podSpec.Containers))
			}

			envMap := make(map[string]string)
			for _, ev := range podSpec.Containers[0].Env {
				envMap[ev.Name] = ev.Value
			}

			if got := envMap[constants.XmcEnvVarManagedXprofGCSObjectFolder]; got != tc.wantGCSObject {
				t.Errorf("GCS object folder env = %q, want %q", got, tc.wantGCSObject)
			}
			// The actuator must no longer inject display name / run group; those are
			// resolved by the reconciler from the artifact.
			if _, ok := envMap[constants.XmcEnvVarManagedXprofDisplayName]; ok {
				t.Errorf("did not expect %s to be injected by the actuator", constants.XmcEnvVarManagedXprofDisplayName)
			}
			if _, ok := envMap[constants.XmcEnvVarManagedXprofRunGroup]; ok {
				t.Errorf("did not expect %s to be injected by the actuator", constants.XmcEnvVarManagedXprofRunGroup)
			}
		})
	}
}

func TestConstructJobSet_CustomAnnotationsAndLabels(t *testing.T) {
	ctx, client, _ := setupTestClient(true /* createNewNamespaces */, &alwaysPassingPreconditionsValidator{})

	workUnit := &esspb.WorkUnit{
		Name: "workUnit1",
		Executable: &esspb.Executable{
			SchedulingConstraints: &esspb.SchedulingConstraints{
				QueueName: testNamespace,
			},
			Type: &esspb.Executable_Jobset{
				Jobset: &esspb.JobSetExecutable{
					Annotations: map[string]string{
						"custom.company.com/release": "v1.2.3",
						"helm.sh/hook":               "pre-install",
						constants.AuthorAnnotation:   "malicious-author-override",
					},
					Labels: map[string]string{
						"custom.company.com/team":   "ml-platform",
						"env":                       "production",
						constants.ExperimentIDLabel: "99999",
						kueueQueueLabel:             "malicious-queue-override",
					},
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
	}

	got, err := client.constructJobSet(ctx, workUnit, testTarget, "test-author", testNamespace, testTime, Priorities{})
	if err != nil {
		t.Fatalf("constructJobSet() returned unexpected error: %v", err)
	}

	// Verify custom annotations were merged.
	if got.Annotations["custom.company.com/release"] != "v1.2.3" {
		t.Errorf("Annotations[\"custom.company.com/release\"] = %q, want \"v1.2.3\"", got.Annotations["custom.company.com/release"])
	}
	if got.Annotations["helm.sh/hook"] != "pre-install" {
		t.Errorf("Annotations[\"helm.sh/hook\"] = %q, want \"pre-install\"", got.Annotations["helm.sh/hook"])
	}
	// Verify reserved author annotation was not overwritten.
	if got.Annotations[constants.AuthorAnnotation] != "test-author" {
		t.Errorf("Annotations[%q] = %q, want \"test-author\"", constants.AuthorAnnotation, got.Annotations[constants.AuthorAnnotation])
	}

	// Verify custom labels were merged.
	if got.Labels["custom.company.com/team"] != "ml-platform" {
		t.Errorf("Labels[\"custom.company.com/team\"] = %q, want \"ml-platform\"", got.Labels["custom.company.com/team"])
	}
	if got.Labels["env"] != "production" {
		t.Errorf("Labels[\"env\"] = %q, want \"production\"", got.Labels["env"])
	}
	// Verify reserved experiment ID label and queue label were not overwritten.
	if got.Labels[constants.ExperimentIDLabel] != "123" {
		t.Errorf("Labels[%q] = %q, want \"123\"", constants.ExperimentIDLabel, got.Labels[constants.ExperimentIDLabel])
	}
	if got.Labels[kueueQueueLabel] != testNamespace {
		t.Errorf("Labels[%q] = %q, want %q", kueueQueueLabel, got.Labels[kueueQueueLabel], testNamespace)
	}

	// Verify custom top-level annotations and labels were NOT propagated to replicated jobs/pods.
	if len(got.Spec.ReplicatedJobs) != 1 {
		t.Fatalf("Expected 1 replicated job, got %d", len(got.Spec.ReplicatedJobs))
	}
	jobAnnotations := got.Spec.ReplicatedJobs[0].Template.ObjectMeta.Annotations
	if _, ok := jobAnnotations["custom.company.com/release"]; ok {
		t.Errorf("ReplicatedJob unexpectedly received top-level annotation \"custom.company.com/release\"")
	}
	if _, ok := jobAnnotations["helm.sh/hook"]; ok {
		t.Errorf("ReplicatedJob unexpectedly received top-level annotation \"helm.sh/hook\"")
	}

	jobLabels := got.Spec.ReplicatedJobs[0].Template.ObjectMeta.Labels
	if _, ok := jobLabels["custom.company.com/team"]; ok {
		t.Errorf("ReplicatedJob unexpectedly received top-level label \"custom.company.com/team\"")
	}
	if _, ok := jobLabels["env"]; ok {
		t.Errorf("ReplicatedJob unexpectedly received top-level label \"env\"")
	}
}

func TestConstructJobSet_KubernetesJobAnnotationsPropagation(t *testing.T) {
	ctx, client, _ := setupTestClient(true /* createNewNamespaces */, &alwaysPassingPreconditionsValidator{})

	workUnit := prototest.Modify(baseCPUWorkUnit, func(wu *esspb.WorkUnit) {
		job := wu.Executable.GetJobset().Jobs[0]
		job.Annotations = map[string]string{
			"alpha.jobset.sigs.k8s.io/exclusive-topology": "kubernetes.io/hostname",
			"custom.job.annotation/key":                   "custom-value",
		}
	})

	got, err := client.constructJobSet(ctx, workUnit, testTarget, "test-author", testNamespace, testTime, Priorities{})
	if err != nil {
		t.Fatalf("constructJobSet() returned unexpected error: %v", err)
	}

	if len(got.Spec.ReplicatedJobs) != 1 {
		t.Fatalf("Expected 1 replicated job, got %d", len(got.Spec.ReplicatedJobs))
	}

	jobTemplateAnnotations := got.Spec.ReplicatedJobs[0].Template.ObjectMeta.Annotations
	podTemplateAnnotations := got.Spec.ReplicatedJobs[0].Template.Spec.Template.ObjectMeta.Annotations

	wantExclusiveTopology := "kubernetes.io/hostname"
	wantCustomAnnotation := "custom-value"

	// Verify JobTemplateSpec annotations
	if gotVal := jobTemplateAnnotations["alpha.jobset.sigs.k8s.io/exclusive-topology"]; gotVal != wantExclusiveTopology {
		t.Errorf("JobTemplateSpec.Annotations[\"alpha.jobset.sigs.k8s.io/exclusive-topology\"] = %q, want %q", gotVal, wantExclusiveTopology)
	}
	if gotVal := jobTemplateAnnotations["custom.job.annotation/key"]; gotVal != wantCustomAnnotation {
		t.Errorf("JobTemplateSpec.Annotations[\"custom.job.annotation/key\"] = %q, want %q", gotVal, wantCustomAnnotation)
	}

	// Verify PodTemplateSpec annotations
	if gotVal := podTemplateAnnotations["alpha.jobset.sigs.k8s.io/exclusive-topology"]; gotVal != wantExclusiveTopology {
		t.Errorf("PodTemplateSpec.Annotations[\"alpha.jobset.sigs.k8s.io/exclusive-topology\"] = %q, want %q", gotVal, wantExclusiveTopology)
	}
	if gotVal := podTemplateAnnotations["custom.job.annotation/key"]; gotVal != wantCustomAnnotation {
		t.Errorf("PodTemplateSpec.Annotations[\"custom.job.annotation/key\"] = %q, want %q", gotVal, wantCustomAnnotation)
	}

	// Verify map pointer separation between JobTemplateSpec and PodTemplateSpec annotations
	jobTemplateAnnotations["test-mutation-key"] = "mutated"
	if _, ok := podTemplateAnnotations["test-mutation-key"]; ok {
		t.Errorf("PodTemplateSpec.Annotations shared map pointer with JobTemplateSpec.Annotations")
	}
}

func TestConstructJobSet_NotifyWhenAnnotation(t *testing.T) {
	ctx, client, _ := setupTestClient(true /* createNewNamespaces */, &alwaysPassingPreconditionsValidator{})

	workUnit := prototest.Modify(baseCPUWorkUnit, func(wu *esspb.WorkUnit) {})

	notifyWhen := "STARTING.RUNNING.COMPLETED.FAILED.STOPPED"
	got, err := client.constructJobSet(ctx, workUnit, testTarget, "test-author", testNamespace, testTime, Priorities{}, notifyWhen)
	if err != nil {
		t.Fatalf("constructJobSet() returned unexpected error: %v", err)
	}

	if gotAnn := got.Annotations[constants.NotifyWhenAnnotation]; gotAnn != notifyWhen {
		t.Errorf("JobSet.Annotations[%q] = %q, want %q", constants.NotifyWhenAnnotation, gotAnn, notifyWhen)
	}
}
