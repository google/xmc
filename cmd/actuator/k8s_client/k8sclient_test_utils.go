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
	"testing"
	"time"

	"google.golang.org/protobuf/proto"

	"golang/k8s_io/apimachinery/v/v0_23/pkg/apis/meta/v1/unstructured/unstructured"
	"golang/k8s_io/apimachinery/v/v0_23/pkg/apis/meta/v1/v1"
	"golang/k8s_io/apimachinery/v/v0_23/pkg/runtime/runtime"
	"golang/k8s_io/apimachinery/v/v0_23/pkg/runtime/schema/schema"
	dynamicfake "golang/k8s_io/client_go/v/v0_23/dynamic/fake/fake"
	kubernetesfake "golang/k8s_io/client_go/v/v0_23/kubernetes/fake/fake"
	esspb "xmanager_cloud/experiment_state_server/proto/messages_go_proto"
	"xmanager_cloud/kubernetes/actuator/k8s_client/preconditions_validator/preconditionsvalidator"
)

var testTime = time.Date(2025, time.November, 26, 9, 32, 58, 0, time.UTC)

type alwaysPassingPreconditionsValidator struct{}

func (m *alwaysPassingPreconditionsValidator) IsActuationRequestValid(ctx context.Context, target string, operationTime time.Time, jobSetUnstructured *unstructured.Unstructured) (bool, error) {
	return true, nil
}
func (m *alwaysPassingPreconditionsValidator) IsExecutionIDAligned(ctx context.Context, target string, jobSetUnstructured *unstructured.Unstructured) (bool, error) {
	return true, nil
}

type alwaysFailingPreconditionsValidator struct{}

func (m *alwaysFailingPreconditionsValidator) IsActuationRequestValid(ctx context.Context, target string, operationTime time.Time, jobSetUnstructured *unstructured.Unstructured) (bool, error) {
	return false, nil
}
func (m *alwaysFailingPreconditionsValidator) IsExecutionIDAligned(ctx context.Context, target string, jobSetUnstructured *unstructured.Unstructured) (bool, error) {
	return false, nil
}

func setupTestClient(createNewNamespaces bool, validator preconditionsvalidator.Validator) (context.Context, *Client, *dynamicfake.FakeDynamicClient) {
	ctx := context.Background()
	scheme := runtime.NewScheme()
	dynamicClient := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(scheme, map[schema.GroupVersionResource]string{
		JobSetGVR:        "JobSetList",
		KueueWorkloadGVR: "WorkloadList",
	})
	client := &Client{
		clientset:              kubernetesfake.NewSimpleClientset(),
		dynamicClient:          dynamicClient,
		RuntimeConfig:          ActuatorRuntimeConfig{CreateNewNamespaces: createNewNamespaces, DefaultExecutionEnvironment: "gke"},
		preconditionsValidator: validator,
	}
	return ctx, client, dynamicClient
}

func createTestJobSet(ctx context.Context, t *testing.T, client *Client, namespace, target string, labels map[string]string) {
	t.Helper()
	workUnit := &esspb.WorkUnit{
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
	jobSet, err := client.constructJobSet(ctx, workUnit, target, "test-author", namespace, testTime, Priorities{})
	if err != nil {
		t.Fatalf("constructJobSet failed: %v", err)
	}
	if len(labels) > 0 {
		if jobSet.Labels == nil {
			jobSet.Labels = make(map[string]string)
		}
		for k, v := range labels {
			jobSet.Labels[k] = v
		}
	}
	jobSet.ObjectMeta.UID = "test-uid"
	jobSetUnstructured, err := jobSetToUnstructured(jobSet)
	if err != nil {
		t.Fatalf("jobSetToUnstructured failed: %v", err)
	}
	_, err = client.dynamicClient.Resource(JobSetGVR).Namespace(namespace).Create(ctx, jobSetUnstructured, v1.CreateOptions{})
	if err != nil {
		t.Fatalf("Creating jobset failed: %v", err)
	}
}

func modifyProto[T proto.Message](m T, f func(T)) T {
	cloned := proto.Clone(m).(T)
	f(cloned)
	return cloned
}
