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

	"golang/cmp/cmp"
	batchv1 "golang/k8s_io/api/v/v0_23/batch/v1/v1"
	corev1 "golang/k8s_io/api/v/v0_23/core/v1/v1"
	apierrors "golang/k8s_io/apimachinery/v/v0_23/pkg/api/errors/errors"
	"golang/k8s_io/apimachinery/v/v0_23/pkg/api/resource/resource"
	"golang/k8s_io/apimachinery/v/v0_23/pkg/apis/meta/v1/unstructured/unstructured"
	metav1 "golang/k8s_io/apimachinery/v/v0_23/pkg/apis/meta/v1/v1"
	"golang/k8s_io/apimachinery/v/v0_23/pkg/runtime/runtime"
	"golang/k8s_io/apimachinery/v/v0_23/pkg/runtime/schema/schema"
	dynamicfake "golang/k8s_io/client_go/v/v0_23/dynamic/fake/fake"
	clientgotesting "golang/k8s_io/client_go/v/v0_23/testing/testing"
	"golang/protobuf/v2/proto/proto"
	esspb "xmanager_cloud/experiment_state_server/proto/messages_go_proto"
	constants "xmanager_cloud/kubernetes/common/constants"

	jobsetv1alpha2 "sigs.k8s.io/jobset/api/jobset/v1alpha2"
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

func setupSkipTest(t *testing.T, target string) (*Client, *dynamicfake.FakeDynamicClient, string, time.Time) {
	t.Helper()
	ctx, client, dynamicClient := setupTestClient(true /* createNewNamespaces */, &alwaysPassingPreconditionsValidator{})
	jobSetName := buildJobSetName(target)
	earlierTimestamp := testTime
	laterTimestamp := earlierTimestamp.Add(time.Minute)
	labels := map[string]string{constants.LastEditLabel: strconv.FormatInt(laterTimestamp.Unix(), 10)}
	createTestJobSet(ctx, t, client, testNamespace, target, labels)
	return client, dynamicClient, jobSetName, earlierTimestamp
}

func setupPopulateTest(t *testing.T, target string) (*Client, *dynamicfake.FakeDynamicClient, time.Time) {
	t.Helper()
	ctx, client, dynamicClient := setupTestClient(true /* createNewNamespaces */, &alwaysPassingPreconditionsValidator{})
	labels := map[string]string{constants.LastEditLabel: strconv.FormatInt(testTime.Unix(), 10)}
	createTestJobSet(ctx, t, client, testNamespace, target, labels)
	return client, dynamicClient, testTime
}

func createTestWorkload(t *testing.T, dynamicClient *dynamicfake.FakeDynamicClient, namespace, name string) {
	t.Helper()
	ctx := t.Context()
	workload := &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": "kueue.x-k8s.io/v1beta1",
			"kind":       "Workload",
			"metadata": map[string]any{
				"name":      name,
				"namespace": namespace,
			},
			"spec": map[string]any{
				"queueName": namespace,
				"active":    true,
			},
		},
	}
	_, err := dynamicClient.Resource(KueueWorkloadGVR).Namespace(namespace).Create(ctx, workload, metav1.CreateOptions{})
	if err != nil {
		t.Fatalf("Failed to create test workload: %v", err)
	}
}

var jobSetWithNoJobs = &jobsetv1alpha2.JobSet{
	TypeMeta: metav1.TypeMeta{
		APIVersion: "jobset.x-k8s.io/v1alpha2",
		Kind:       "JobSet",
	},
	ObjectMeta: metav1.ObjectMeta{
		Name:      "xmc-operation-123-456",
		Namespace: "xmc",
		Labels: map[string]string{
			constants.ExperimentIDLabel: "123",
			constants.WorkUnitIDLabel:   "456",
			constants.ExecutionIDLabel:  "1",
			constants.ManagedByLabel:    constants.XmcLabelValue,
			kueueQueueLabel:             constants.XmcLabelValue,
			constants.LastEditLabel:     strconv.FormatInt(testTime.Unix(), 10),
			kueuePriorityClassLabel:     "exp-low-wu-low",
		},
		Annotations: map[string]string{
			constants.AuthorAnnotation:                "test-author",
			constants.AutoscalerSafeToEvictAnnotation: "false",
		},
		Finalizers: []string{constants.JobSetFinalizer},
	},
	Spec: jobsetv1alpha2.JobSetSpec{
		ReplicatedJobs: nil,
		Suspend:        boolPtr(false),
		SuccessPolicy: &jobsetv1alpha2.SuccessPolicy{
			Operator: jobsetv1alpha2.OperatorAll,
		},
		FailurePolicy: &jobsetv1alpha2.FailurePolicy{
			MaxRestarts: 0,
			Rules:       []jobsetv1alpha2.FailurePolicyRule{},
		},
		TTLSecondsAfterFinished: proto.Int32(604800),
	},
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

func testBuildJobSetName(t *testing.T) {
	got := buildJobSetName("experiments/1/workUnits/1/executions/1")
	want := "xmc-operation-1-1"
	if got != want {
		t.Errorf("buildJobSetName() = %v, want %v", got, want)
	}
}

func TestConstructJobSet(t *testing.T) {
	ctx, client, _ := setupTestClient(true /* createNewNamespaces */, &alwaysPassingPreconditionsValidator{})
	workUnit := proto.Modify(baseCPUWorkUnit, func(wu *esspb.WorkUnit) {
		job := wu.Executable.GetJobset().Jobs[0]
		job.Spec.Args = map[string]string{"arg1": "val1"}
		job.Spec.EnvVars = map[string]string{"env1": "val1"}
		job.Spec.Command = []string{"bash", "-c", "echo hello"}
		job.Replicas = 2
		job.RetryLimit = 1
		job.Annotations = map[string]string{"anno1": "val1"}
		job.ServiceAccount = "my-service-account"
	})
	namespace := "test-namespace"

	jobLabels := map[string]string{
		constants.ExperimentIDLabel: "123",
		constants.WorkUnitIDLabel:   "456",
		constants.ExecutionIDLabel:  "1",
		constants.ManagedByLabel:    constants.XmcLabelValue,
		kueueQueueLabel:             "test-namespace",
	}
	jobAnnotations := map[string]string{
		"anno1":                    "val1",
		constants.AuthorAnnotation: "test-author",
		constants.AutoscalerSafeToEvictAnnotation: "false",
	}

	want := &jobsetv1alpha2.JobSet{
		TypeMeta: metav1.TypeMeta{
			APIVersion: "jobset.x-k8s.io/v1alpha2",
			Kind:       "JobSet",
		},
		ObjectMeta: metav1.ObjectMeta{
			Name:      "xmc-operation-123-456",
			Namespace: "test-namespace",
			Labels: map[string]string{
				constants.ExperimentIDLabel: "123",
				constants.WorkUnitIDLabel:   "456",
				constants.ExecutionIDLabel:  "1",
				constants.ManagedByLabel:    constants.XmcLabelValue,
				kueueQueueLabel:             "test-namespace",
				constants.LastEditLabel:     strconv.FormatInt(testTime.Unix(), 10),
				kueuePriorityClassLabel:     "exp-low-wu-low",
			},
			Annotations: map[string]string{
				constants.AuthorAnnotation:                "test-author",
				constants.AutoscalerSafeToEvictAnnotation: "false",
			},
			Finalizers: []string{constants.JobSetFinalizer},
		},
		Spec: jobsetv1alpha2.JobSetSpec{
			ReplicatedJobs: []jobsetv1alpha2.ReplicatedJob{
				{
					Name:     "job1",
					Replicas: 2,
					Template: batchv1.JobTemplateSpec{
						ObjectMeta: metav1.ObjectMeta{
							Labels:      jobLabels,
							Annotations: jobAnnotations,
						},
						Spec: batchv1.JobSpec{
							BackoffLimit:   proto.Int32(1),
							CompletionMode: completionModePtr(batchv1.IndexedCompletion),
							Completions:    proto.Int32(1),
							PodFailurePolicy: &batchv1.PodFailurePolicy{
								Rules: []batchv1.PodFailurePolicyRule{
									{
										Action: batchv1.PodFailurePolicyActionIgnore,
										OnPodConditions: []batchv1.PodFailurePolicyOnPodConditionsPattern{
											{
												Type: "DisruptionTarget",
											},
										},
									},
									{
										Action: batchv1.PodFailurePolicyActionFailJob,
										OnExitCodes: &batchv1.PodFailurePolicyOnExitCodesRequirement{
											ContainerName: proto.String("job1"),
											Operator:      "In",
											Values:        []int32{50},
										},
									},
									{
										Action: batchv1.PodFailurePolicyActionIgnore,
										OnExitCodes: &batchv1.PodFailurePolicyOnExitCodesRequirement{
											ContainerName: proto.String("job1"),
											Operator:      "In",
											Values:        []int32{42},
										},
									},
								},
							},
							TTLSecondsAfterFinished: proto.Int32(604800),
							Template: corev1.PodTemplateSpec{
								ObjectMeta: metav1.ObjectMeta{
									Labels:      jobLabels,
									Annotations: jobAnnotations,
								},
								Spec: corev1.PodSpec{
									ServiceAccountName: "my-service-account",
									Containers: []corev1.Container{
										{
											Name:                     "job1",
											Image:                    "foo/bar",
											ImagePullPolicy:          corev1.PullAlways,
											TerminationMessagePath:   "/dev/termination-log",
											TerminationMessagePolicy: corev1.TerminationMessageReadFile,
											Command:                  []string{"bash", "-c", "echo hello"},
											Args:                     []string{"--arg1=val1"},
											Env: []corev1.EnvVar{
												{Name: "env1", Value: "val1"},
												{Name: constants.XmcEnvVarManagedBy, Value: "true"},
												{Name: constants.XmcEnvVarMetricsEnabled, Value: "false"},
												{
													Name: constants.XmcEnvVarNamespace,
													ValueFrom: &corev1.EnvVarSource{
														FieldRef: &corev1.ObjectFieldSelector{
															FieldPath: "metadata.namespace",
														},
													},
												},
												{
													Name: constants.XmcEnvVarQueueName,
													ValueFrom: &corev1.EnvVarSource{
														FieldRef: &corev1.ObjectFieldSelector{
															FieldPath: fmt.Sprintf("metadata.labels['%s']", kueueQueueLabel),
														},
													},
												},
												{
													Name: constants.XmcEnvVarExperimentID,
													ValueFrom: &corev1.EnvVarSource{
														FieldRef: &corev1.ObjectFieldSelector{
															FieldPath: fmt.Sprintf("metadata.labels['%s']", constants.ExperimentIDLabel),
														},
													},
												},
												{
													Name: constants.XmcEnvVarWorkUnitID,
													ValueFrom: &corev1.EnvVarSource{
														FieldRef: &corev1.ObjectFieldSelector{
															FieldPath: fmt.Sprintf("metadata.labels['%s']", constants.WorkUnitIDLabel),
														},
													},
												},
												{
													Name: constants.XmcEnvVarExecutionID,
													ValueFrom: &corev1.EnvVarSource{
														FieldRef: &corev1.ObjectFieldSelector{
															FieldPath: fmt.Sprintf("metadata.labels['%s']", constants.ExecutionIDLabel),
														},
													},
												},
												{
													Name: constants.XmcEnvVarAuthor,
													ValueFrom: &corev1.EnvVarSource{
														FieldRef: &corev1.ObjectFieldSelector{
															FieldPath: fmt.Sprintf("metadata.annotations['%s']", constants.AuthorAnnotation),
														},
													},
												},
												{
													Name: constants.XmcEnvVarJobIndex,
													ValueFrom: &corev1.EnvVarSource{
														FieldRef: &corev1.ObjectFieldSelector{
															FieldPath: fmt.Sprintf("metadata.labels['%s']", constants.JobSetJobIndexLabel),
														},
													},
												},
											},
											Resources: corev1.ResourceRequirements{
												Limits: corev1.ResourceList{
													corev1.ResourceMemory: resource.MustParse("1024"),
												},
												Requests: corev1.ResourceList{
													corev1.ResourceCPU:    resource.MustParse("100m"),
													corev1.ResourceMemory: resource.MustParse("1024"),
												},
											},
											VolumeMounts: []corev1.VolumeMount{
												{
													Name:      "ram-scratch",
													MountPath: "/dev/ram-scratch",
												},
												{
													Name:      "disk-scratch",
													MountPath: "/dev/disk-scratch",
												},
												{
													Name:      "dev-shm-scratch",
													MountPath: "/dev/shm",
												},
											},
											SecurityContext: &corev1.SecurityContext{
												AllowPrivilegeEscalation: proto.Bool(false),
												Capabilities: &corev1.Capabilities{
													Drop: []corev1.Capability{
														"ALL",
													},
												},
												Privileged: proto.Bool(false),
											},
										},
									},
									RestartPolicy: corev1.RestartPolicyNever,
									SecurityContext: &corev1.PodSecurityContext{
										RunAsGroup: proto.Int64(1000),
										RunAsUser:  proto.Int64(1000),
										SeccompProfile: &corev1.SeccompProfile{
											Type: corev1.SeccompProfileTypeRuntimeDefault,
										},
									},
									Volumes: []corev1.Volume{
										{
											Name: "ram-scratch",
											VolumeSource: corev1.VolumeSource{
												EmptyDir: &corev1.EmptyDirVolumeSource{
													Medium: corev1.StorageMediumMemory,
												},
											},
										},
										{
											Name: "disk-scratch",
											VolumeSource: corev1.VolumeSource{
												EmptyDir: &corev1.EmptyDirVolumeSource{
													Medium: corev1.StorageMediumDefault,
												},
											},
										},
										{
											Name: "dev-shm-scratch",
											VolumeSource: corev1.VolumeSource{
												EmptyDir: &corev1.EmptyDirVolumeSource{
													Medium: corev1.StorageMediumMemory,
												},
											},
										},
									},
								},
							},
						},
					},
				},
			},
			Network: &jobsetv1alpha2.Network{
				EnableDNSHostnames:       boolPtr(true),
				Subdomain:                "xmc-operation-123-456",
				PublishNotReadyAddresses: boolPtr(true),
			},
			Suspend: boolPtr(false),
			SuccessPolicy: &jobsetv1alpha2.SuccessPolicy{
				Operator: jobsetv1alpha2.OperatorAll,
			},
			FailurePolicy: &jobsetv1alpha2.FailurePolicy{
				MaxRestarts: 0,
				Rules:       []jobsetv1alpha2.FailurePolicyRule{},
			},
			TTLSecondsAfterFinished: proto.Int32(604800),
		},
	}

	got, err := client.constructJobSet(ctx, workUnit, testTarget, "test-author", namespace, testTime, Priorities{})
	if err != nil {
		t.Fatalf("constructJobSet() returned unexpected error: %v", err)
	}

	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("constructJobSet() returned diff (-want +got):\n%s", diff)
	}
}

func TestConstructJobSet_GroupName(t *testing.T) {
	ctx, client, _ := setupTestClient(true /* createNewNamespaces */, &alwaysPassingPreconditionsValidator{})

	t.Run("Empty groupName when omitted", func(t *testing.T) {
		jobSet, err := client.constructJobSet(ctx, baseCPUWorkUnit, testTarget, "test-author", testNamespace, testTime, Priorities{})
		if err != nil {
			t.Fatalf("constructJobSet() returned unexpected error: %v", err)
		}
		if len(jobSet.Spec.ReplicatedJobs) != 1 {
			t.Fatalf("Expected 1 ReplicatedJob, got %d", len(jobSet.Spec.ReplicatedJobs))
		}
		if got := jobSet.Spec.ReplicatedJobs[0].GroupName; got != "" {
			t.Errorf("ReplicatedJob.GroupName = %q, want %q", got, "")
		}
	})

	t.Run("Explicit groupName set on KubernetesJob", func(t *testing.T) {
		workUnit := proto.Modify(baseCPUWorkUnit, func(wu *esspb.WorkUnit) {
			wu.Executable.GetJobset().Jobs[0].GroupName = "custom-group"
		})
		jobSet, err := client.constructJobSet(ctx, workUnit, testTarget, "test-author", testNamespace, testTime, Priorities{})
		if err != nil {
			t.Fatalf("constructJobSet() returned unexpected error: %v", err)
		}
		if len(jobSet.Spec.ReplicatedJobs) != 1 {
			t.Fatalf("Expected 1 ReplicatedJob, got %d", len(jobSet.Spec.ReplicatedJobs))
		}
		if got := jobSet.Spec.ReplicatedJobs[0].GroupName; got != "custom-group" {
			t.Errorf("ReplicatedJob.GroupName = %q, want %q", got, "custom-group")
		}
	})
}

func TestConstructJobSet_PerJobCustomLabels(t *testing.T) {
	ctx, client, _ := setupTestClient(true /* createNewNamespaces */, &alwaysPassingPreconditionsValidator{})
	workUnit := proto.Modify(baseCPUWorkUnit, func(wu *esspb.WorkUnit) {
		wu.Executable.GetJobset().Jobs[0].Labels = map[string]string{
			"kueue.x-k8s.io/podset":     "pathways-head",
			"custom.label/key":          "value",
			constants.ExperimentIDLabel: "hacked-xid",
			constants.WorkUnitIDLabel:   "hacked-wid",
			constants.ExecutionIDLabel:  "hacked-eid",
			"kueue.x-k8s.io/queue-name": "hacked-queue",
		}
	})
	jobSet, err := client.constructJobSet(ctx, workUnit, testTarget, "test-author", testNamespace, testTime, Priorities{})
	if err != nil {
		t.Fatalf("constructJobSet() returned unexpected error: %v", err)
	}
	if len(jobSet.Spec.ReplicatedJobs) != 1 {
		t.Fatalf("Expected 1 ReplicatedJob, got %d", len(jobSet.Spec.ReplicatedJobs))
	}
	rj := jobSet.Spec.ReplicatedJobs[0]

	// Verify custom labels are applied
	if got := rj.Template.ObjectMeta.Labels["kueue.x-k8s.io/podset"]; got != "pathways-head" {
		t.Errorf("JobTemplateSpec.ObjectMeta.Labels[kueue.x-k8s.io/podset] = %q, want %q", got, "pathways-head")
	}
	if got := rj.Template.ObjectMeta.Labels["custom.label/key"]; got != "value" {
		t.Errorf("JobTemplateSpec.ObjectMeta.Labels[custom.label/key] = %q, want %q", got, "value")
	}

	podLabels := rj.Template.Spec.Template.ObjectMeta.Labels
	if got := podLabels["kueue.x-k8s.io/podset"]; got != "pathways-head" {
		t.Errorf("PodTemplateSpec.ObjectMeta.Labels[kueue.x-k8s.io/podset] = %q, want %q", got, "pathways-head")
	}
	if got := podLabels["custom.label/key"]; got != "value" {
		t.Errorf("PodTemplateSpec.ObjectMeta.Labels[custom.label/key] = %q, want %q", got, "value")
	}

	// Verify system jobLabels take precedence over job.GetLabels()
	expectedSystemLabels := map[string]string{
		constants.ExperimentIDLabel: "123",
		constants.WorkUnitIDLabel:   "456",
		constants.ExecutionIDLabel:  "1",
		"kueue.x-k8s.io/queue-name": testNamespace,
	}
	for labelKey, wantValue := range expectedSystemLabels {
		if got := rj.Template.ObjectMeta.Labels[labelKey]; got != wantValue {
			t.Errorf("JobTemplateSpec system label %s = %q, want %q (system label must take precedence)", labelKey, got, wantValue)
		}
		if got := podLabels[labelKey]; got != wantValue {
			t.Errorf("PodTemplateSpec system label %s = %q, want %q (system label must take precedence)", labelKey, got, wantValue)
		}
	}
}

func TestConstructJobSet_MetricsEnabled(t *testing.T) {
	ctx, client, _ := setupTestClient(true /* createNewNamespaces */, &alwaysPassingPreconditionsValidator{})
	client.RuntimeConfig.MetricsEnabled = true

	got, err := client.constructJobSet(ctx, baseCPUWorkUnit, testTarget, "test-author", testNamespace, testTime, Priorities{})
	if err != nil {
		t.Fatalf("constructJobSet() returned unexpected error: %v", err)
	}

	container := got.Spec.ReplicatedJobs[0].Template.Spec.Template.Spec.Containers[0]
	found := false
	for _, env := range container.Env {
		if env.Name == constants.XmcEnvVarMetricsEnabled {
			found = true
			if env.Value != "true" {
				t.Errorf("XMC_METRICS_ENABLED env var = %q, want %q", env.Value, "true")
			}
		}
	}
	if !found {
		t.Errorf("XMC_METRICS_ENABLED env var not found in container spec")
	}
}

func TestConstructJobSet_PerJobNodeSelector(t *testing.T) {
	ctx, client, _ := setupTestClient(true /* createNewNamespaces */, &alwaysPassingPreconditionsValidator{})
	workUnit := &esspb.WorkUnit{
		Name: "experiments/1/workUnits/1",
		Executable: &esspb.Executable{
			Type: &esspb.Executable_Jobset{
				Jobset: &esspb.JobSetExecutable{
					Jobs: []*esspb.KubernetesJob{
						{
							Name:     "head",
							Replicas: 1,
							Spec: &esspb.ExecutableSpec{
								DockerImage: "gcr.io/foo/bar:latest",
								Resources: &esspb.ResourceSet{
									Resources: &esspb.ResourceSet_CpuResources{
										CpuResources: &esspb.CpuResources{
											CpuMillicores: 2000,
											MemoryBytes:   8589934592,
										},
									},
								},
							},
							NodeSelector: map[string]string{
								"cloud.google.com/gke-nodepool": "cpups",
							},
						},
						{
							Name:     "worker",
							Replicas: 2,
							Spec: &esspb.ExecutableSpec{
								DockerImage: "gcr.io/foo/bar:latest",
								Resources: &esspb.ResourceSet{
									Resources: &esspb.ResourceSet_GpuResources{
										GpuResources: &esspb.GpuResources{
											Type:  "T4",
											Count: 1,
										},
									},
								},
							},
							NodeSelector: map[string]string{
								"cloud.google.com/gke-nodepool": "gpu-pool",
							},
						},
					},
				},
			},
		},
	}

	jobSet, err := client.constructJobSet(ctx, workUnit, "experiments/1/workUnits/1/executions/1", "test-author", "test-namespace", testTime, Priorities{})
	if err != nil {
		t.Fatalf("constructJobSet() returned unexpected error: %v", err)
	}

	replicatedJobs := make(map[string]*jobsetv1alpha2.ReplicatedJob)
	for i := range jobSet.Spec.ReplicatedJobs {
		rj := &jobSet.Spec.ReplicatedJobs[i]
		replicatedJobs[rj.Name] = rj
	}

	headJob, exists := replicatedJobs["head"]
	if !exists {
		t.Fatalf("Expected job 'head' in JobSet")
	}
	if headJob.Template.Spec.Template.Spec.NodeSelector["cloud.google.com/gke-nodepool"] != "cpups" {
		t.Errorf("head PodSpec.NodeSelector['cloud.google.com/gke-nodepool'] = %q, want 'cpups'", headJob.Template.Spec.Template.Spec.NodeSelector["cloud.google.com/gke-nodepool"])
	}

	workerJob, exists := replicatedJobs["worker"]
	if !exists {
		t.Fatalf("Expected job 'worker' in JobSet")
	}
	if workerJob.Template.Spec.Template.Spec.NodeSelector["cloud.google.com/gke-nodepool"] != "gpu-pool" {
		t.Errorf("worker PodSpec.NodeSelector['cloud.google.com/gke-nodepool'] = %q, want 'gpu-pool'", workerJob.Template.Spec.Template.Spec.NodeSelector["cloud.google.com/gke-nodepool"])
	}
	if workerJob.Template.Spec.Template.Spec.NodeSelector["cloud.google.com/gke-accelerator"] != "nvidia-tesla-t4" {
		t.Errorf("worker PodSpec.NodeSelector['cloud.google.com/gke-accelerator'] = %q, want 'nvidia-tesla-t4'", workerJob.Template.Spec.Template.Spec.NodeSelector["cloud.google.com/gke-accelerator"])
	}
}

func TestConstructJobSet_CustomEnvVars(t *testing.T) {
	ctx, client, _ := setupTestClient(true /* createNewNamespaces */, &alwaysPassingPreconditionsValidator{})
	workUnit := proto.Modify(baseCPUWorkUnit, func(wu *esspb.WorkUnit) {
		job := wu.Executable.GetJobset().Jobs[0]
		job.Spec.EnvVars = map[string]string{"env_vars_key": "env_vars_val"}
		job.Spec.Env = []*esspb.EnvVar{
			{
				Name: "literal_env",
				Source: &esspb.EnvVar_Value{
					Value: "literal_val",
				},
			},
			// nil entries should be safely ignored.
			nil,
			// empty name entries should be ignored.
			{
				Name: "",
				Source: &esspb.EnvVar_Value{
					Value: "empty_name_val",
				},
			},
			{
				Name: "field_ref_env",
				Source: &esspb.EnvVar_ValueFrom{
					ValueFrom: &esspb.EnvVarSource{
						Reference: &esspb.EnvVarSource_FieldRef{
							FieldRef: &esspb.FieldRef{
								FieldPath:  "status.podIP",
								ApiVersion: "v1",
							},
						},
					},
				},
			},
			{
				Name: "nil_value_from_env",
				Source: &esspb.EnvVar_ValueFrom{
					ValueFrom: nil,
				},
			},
			{
				Name: "nil_field_ref_env",
				Source: &esspb.EnvVar_ValueFrom{
					ValueFrom: &esspb.EnvVarSource{
						Reference: nil,
					},
				},
			},
			{
				Name: "nil_inner_field_ref_env",
				Source: &esspb.EnvVar_ValueFrom{
					ValueFrom: &esspb.EnvVarSource{
						Reference: &esspb.EnvVarSource_FieldRef{
							FieldRef: nil,
						},
					},
				},
			},
			{
				Name: "secret_ref_env_opt",
				Source: &esspb.EnvVar_ValueFrom{
					ValueFrom: &esspb.EnvVarSource{
						Reference: &esspb.EnvVarSource_SecretKeyRef{
							SecretKeyRef: &esspb.SecretKeyRef{
								Name:     "my-secret",
								Key:      "my-key",
								Optional: boolPtr(true),
							},
						},
					},
				},
			},
			{
				Name: "secret_ref_env_req",
				Source: &esspb.EnvVar_ValueFrom{
					ValueFrom: &esspb.EnvVarSource{
						Reference: &esspb.EnvVarSource_SecretKeyRef{
							SecretKeyRef: &esspb.SecretKeyRef{
								Name:     "other-secret",
								Key:      "other-key",
								Optional: nil,
							},
						},
					},
				},
			},
			{
				Name: "nil_inner_secret_key_ref_env",
				Source: &esspb.EnvVar_ValueFrom{
					ValueFrom: &esspb.EnvVarSource{
						Reference: &esspb.EnvVarSource_SecretKeyRef{
							SecretKeyRef: nil,
						},
					},
				},
			},
		}
	})
	namespace := "test-namespace"

	got, err := client.constructJobSet(ctx, workUnit, testTarget, "test-author", namespace, testTime, Priorities{})
	if err != nil {
		t.Fatalf("constructJobSet() returned unexpected error: %v", err)
	}

	if len(got.Spec.ReplicatedJobs) != 1 {
		t.Fatalf("Expected 1 ReplicatedJob, got %d", len(got.Spec.ReplicatedJobs))
	}

	container := got.Spec.ReplicatedJobs[0].Template.Spec.Template.Spec.Containers[0]

	wantEnv := []corev1.EnvVar{
		{Name: "env_vars_key", Value: "env_vars_val"},
		{Name: "literal_env", Value: "literal_val"},
		{
			Name: "field_ref_env",
			ValueFrom: &corev1.EnvVarSource{
				FieldRef: &corev1.ObjectFieldSelector{
					FieldPath:  "status.podIP",
					APIVersion: "v1",
				},
			},
		},
		{
			Name: "secret_ref_env_opt",
			ValueFrom: &corev1.EnvVarSource{
				SecretKeyRef: &corev1.SecretKeySelector{
					LocalObjectReference: corev1.LocalObjectReference{
						Name: "my-secret",
					},
					Key:      "my-key",
					Optional: boolPtr(true),
				},
			},
		},
		{
			Name: "secret_ref_env_req",
			ValueFrom: &corev1.EnvVarSource{
				SecretKeyRef: &corev1.SecretKeySelector{
					LocalObjectReference: corev1.LocalObjectReference{
						Name: "other-secret",
					},
					Key:      "other-key",
					Optional: nil,
				},
			},
		},
	}
	var gotEnv []corev1.EnvVar
	for _, env := range container.Env {
		if env.Name == constants.XmcEnvVarManagedBy ||
			env.Name == constants.XmcEnvVarMetricsEnabled ||
			env.Name == constants.XmcEnvVarNamespace ||
			env.Name == constants.XmcEnvVarQueueName ||
			env.Name == constants.XmcEnvVarExperimentID ||
			env.Name == constants.XmcEnvVarWorkUnitID ||
			env.Name == constants.XmcEnvVarExecutionID ||
			env.Name == constants.XmcEnvVarAuthor ||
			env.Name == constants.XmcEnvVarJobIndex {
			continue
		}
		gotEnv = append(gotEnv, env)
	}

	if diff := cmp.Diff(wantEnv, gotEnv); diff != "" {
		t.Errorf("container.Env returned diff (-want +got):\n%s", diff)
	}
}

func TestConstructJobSet_GPU_GKE(t *testing.T) {
	ctx, client, _ := setupTestClient(true /* createNewNamespaces */, &alwaysPassingPreconditionsValidator{})
	workUnit := proto.Modify(baseCPUWorkUnit, func(wu *esspb.WorkUnit) {
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

func TestConstructJobSet_ContainerDefaults(t *testing.T) {
	ctx, client, _ := setupTestClient(true /* createNewNamespaces */, &alwaysPassingPreconditionsValidator{})
	workUnit := baseCPUWorkUnit

	got, err := client.constructJobSet(ctx, workUnit, testTarget, "test-author", constants.XmcLabelValue, testTime, Priorities{})
	if err != nil {
		t.Fatalf("constructJobSet() returned unexpected error: %v", err)
	}

	if len(got.Spec.ReplicatedJobs) != 1 {
		t.Fatalf("Expected 1 replicated job, got %d", len(got.Spec.ReplicatedJobs))
	}

	if len(got.Spec.ReplicatedJobs[0].Template.Spec.Template.Spec.Containers) != 1 {
		t.Fatalf("Expected 1 container, got %d", len(got.Spec.ReplicatedJobs[0].Template.Spec.Template.Spec.Containers))
	}

	container := got.Spec.ReplicatedJobs[0].Template.Spec.Template.Spec.Containers[0]

	if container.ImagePullPolicy != corev1.PullAlways {
		t.Errorf("Expected ImagePullPolicy to be Always, got %v", container.ImagePullPolicy)
	}

	if container.TerminationMessagePath != "/dev/termination-log" {
		t.Errorf("Expected TerminationMessagePath to be /dev/termination-log, got %v", container.TerminationMessagePath)
	}

	if container.TerminationMessagePolicy != corev1.TerminationMessageReadFile {
		t.Errorf("Expected TerminationMessagePolicy to be File, got %v", container.TerminationMessagePolicy)
	}
}

func TestConstructJobSet_ArgsAndArgsList(t *testing.T) {
	ctx, client, _ := setupTestClient(true /* createNewNamespaces */, &alwaysPassingPreconditionsValidator{})
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
								Args: map[string]string{
									"z_flag": "z_val",
									"a_flag": "a_val",
								},
								ArgsList: []string{
									"positional_arg",
									"bare_key=val",
									"--gnu_flag=val",
								},
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
	if len(got.Spec.ReplicatedJobs[0].Template.Spec.Template.Spec.Containers) != 1 {
		t.Fatalf("Expected 1 container, got %d", len(got.Spec.ReplicatedJobs[0].Template.Spec.Template.Spec.Containers))
	}
	container := got.Spec.ReplicatedJobs[0].Template.Spec.Template.Spec.Containers[0]

	wantArgs := []string{
		"--a_flag=a_val",
		"--z_flag=z_val",
		"positional_arg",
		"bare_key=val",
		"--gnu_flag=val",
	}
	if diff := cmp.Diff(wantArgs, container.Args); diff != "" {
		t.Errorf("constructJobSet() container.Args mismatch (-want +got):\n%s", diff)
	}
}

func TestConstructJobSet_TTLSecondsAfterFinished(t *testing.T) {
	ctx, client, _ := setupTestClient(true /* createNewNamespaces */, &alwaysPassingPreconditionsValidator{})
	workUnit := baseCPUWorkUnit

	got, err := client.constructJobSet(ctx, workUnit, testTarget, "test-author", constants.XmcLabelValue, testTime, Priorities{})
	if err != nil {
		t.Fatalf("constructJobSet() returned unexpected error: %v", err)
	}

	if len(got.Spec.ReplicatedJobs) != 1 {
		t.Fatalf("Expected 1 replicated job, got %d", len(got.Spec.ReplicatedJobs))
	}

	// Check JobSet TTL
	if got.Spec.TTLSecondsAfterFinished == nil {
		t.Fatal("Expected JobSet TTLSecondsAfterFinished to be set, but it was nil")
	}
	if *got.Spec.TTLSecondsAfterFinished != 604800 {
		t.Errorf("Expected JobSet TTLSecondsAfterFinished to be 604800, got %d", *got.Spec.TTLSecondsAfterFinished)
	}

	// Check ReplicatedJob TTL
	jobSpec := got.Spec.ReplicatedJobs[0].Template.Spec
	if jobSpec.TTLSecondsAfterFinished == nil {
		t.Fatal("Expected ReplicatedJob TTLSecondsAfterFinished to be set, but it was nil")
	}
	if *jobSpec.TTLSecondsAfterFinished != 604800 {
		t.Errorf("Expected ReplicatedJob TTLSecondsAfterFinished to be 604800, got %d", *jobSpec.TTLSecondsAfterFinished)
	}
}

func TestConstructJobSet_SecurityContext(t *testing.T) {
	ctx, client, _ := setupTestClient(true /* createNewNamespaces */, &alwaysPassingPreconditionsValidator{})
	workUnit := baseCPUWorkUnit

	got, err := client.constructJobSet(ctx, workUnit, testTarget, "test-author", constants.XmcLabelValue, testTime, Priorities{})
	if err != nil {
		t.Fatalf("constructJobSet() returned unexpected error: %v", err)
	}

	if len(got.Spec.ReplicatedJobs) != 1 {
		t.Fatalf("Expected 1 replicated job, got %d", len(got.Spec.ReplicatedJobs))
	}

	podSpec := got.Spec.ReplicatedJobs[0].Template.Spec.Template.Spec

	// Check PodSecurityContext
	if podSpec.SecurityContext == nil {
		t.Fatal("Expected PodSecurityContext to be set, but it was nil")
	}
	if *podSpec.SecurityContext.RunAsUser != 1000 {
		t.Errorf("Expected RunAsUser to be 1000, got %d", *podSpec.SecurityContext.RunAsUser)
	}
	if *podSpec.SecurityContext.RunAsGroup != 1000 {
		t.Errorf("Expected RunAsGroup to be 1000, got %d", *podSpec.SecurityContext.RunAsGroup)
	}
	if podSpec.SecurityContext.SeccompProfile == nil {
		t.Fatal("Expected SeccompProfile to be set, but it was nil")
	}
	if podSpec.SecurityContext.SeccompProfile.Type != corev1.SeccompProfileTypeRuntimeDefault {
		t.Errorf("Expected SeccompProfile type to be RuntimeDefault, got %v", podSpec.SecurityContext.SeccompProfile.Type)
	}

	// Check Container SecurityContext
	if len(podSpec.Containers) != 1 {
		t.Fatalf("Expected 1 container, got %d", len(podSpec.Containers))
	}
	containerSC := podSpec.Containers[0].SecurityContext
	if containerSC == nil {
		t.Fatal("Expected Container SecurityContext to be set, but it was nil")
	}
	if *containerSC.AllowPrivilegeEscalation {
		t.Errorf("Expected AllowPrivilegeEscalation to be false, got true")
	}
	if *containerSC.Privileged {
		t.Errorf("Expected Privileged to be false, got true")
	}
	if containerSC.Capabilities == nil {
		t.Fatal("Expected Capabilities to be set, but it was nil")
	}
	if len(containerSC.Capabilities.Drop) != 1 || containerSC.Capabilities.Drop[0] != "ALL" {
		t.Errorf("Expected Capabilities.Drop to be ['ALL'], got %v", containerSC.Capabilities.Drop)
	}
}

func TestConstructJobSet_PodFailurePolicy(t *testing.T) {
	ctx, client, _ := setupTestClient(true /* createNewNamespaces */, &alwaysPassingPreconditionsValidator{})
	workUnit := baseCPUWorkUnit

	got, err := client.constructJobSet(ctx, workUnit, testTarget, "test-author", constants.XmcLabelValue, testTime, Priorities{})
	if err != nil {
		t.Fatalf("constructJobSet() returned unexpected error: %v", err)
	}

	if len(got.Spec.ReplicatedJobs) != 1 {
		t.Fatalf("Expected 1 replicated job, got %d", len(got.Spec.ReplicatedJobs))
	}

	pfp := got.Spec.ReplicatedJobs[0].Template.Spec.PodFailurePolicy
	if pfp == nil {
		t.Fatal("Expected PodFailurePolicy to be set, but it was nil")
	}

	if len(pfp.Rules) != 3 {
		t.Fatalf("Expected 3 PodFailurePolicy rules, got %d", len(pfp.Rules))
	}

	t.Run("Rule 1: DisruptionTarget", func(t *testing.T) {
		rule1 := pfp.Rules[0]
		if rule1.Action != batchv1.PodFailurePolicyActionIgnore {
			t.Errorf("Rule 1 Action: got %v, want %v", rule1.Action, batchv1.PodFailurePolicyActionIgnore)
		}
		if len(rule1.OnPodConditions) != 1 || rule1.OnPodConditions[0].Type != "DisruptionTarget" {
			t.Errorf("Rule 1 OnPodConditions: got %v, want DisruptionTarget", rule1.OnPodConditions)
		}
	})

	t.Run("Rule 2: Exit Code 50 -> FailJob", func(t *testing.T) {
		rule2 := pfp.Rules[1]
		if rule2.Action != batchv1.PodFailurePolicyActionFailJob {
			t.Errorf("Rule 2 Action: got %v, want %v", rule2.Action, batchv1.PodFailurePolicyActionFailJob)
		}
		if rule2.OnExitCodes == nil {
			t.Fatal("Rule 2 OnExitCodes is nil")
		}
		if *rule2.OnExitCodes.ContainerName != "job1" {
			t.Errorf("Rule 2 ContainerName: got %q, want %q", *rule2.OnExitCodes.ContainerName, "job1")
		}
		if len(rule2.OnExitCodes.Values) != 1 || rule2.OnExitCodes.Values[0] != 50 {
			t.Errorf("Rule 2 Values: got %v, want [50]", rule2.OnExitCodes.Values)
		}
	})

	t.Run("Rule 3: Exit Code 42 -> Ignore", func(t *testing.T) {
		rule3 := pfp.Rules[2]
		if rule3.Action != batchv1.PodFailurePolicyActionIgnore {
			t.Errorf("Rule 3 Action: got %v, want %v", rule3.Action, batchv1.PodFailurePolicyActionIgnore)
		}
		if rule3.OnExitCodes == nil {
			t.Fatal("Rule 3 OnExitCodes is nil")
		}
		if *rule3.OnExitCodes.ContainerName != "job1" {
			t.Errorf("Rule 3 ContainerName: got %q, want %q", *rule3.OnExitCodes.ContainerName, "job1")
		}
		if len(rule3.OnExitCodes.Values) != 1 || rule3.OnExitCodes.Values[0] != 42 {
			t.Errorf("Rule 3 Values: got %v, want [42]", rule3.OnExitCodes.Values)
		}
	})
}

func TestConstructJobSet_ActiveDeadlineSeconds(t *testing.T) {
	ctx, client, _ := setupTestClient(true /* createNewNamespaces */, &alwaysPassingPreconditionsValidator{})
	workUnit := proto.Modify(baseCPUWorkUnit, func(wu *esspb.WorkUnit) {
		wu.Executable.GetJobset().Jobs[0].ActiveDeadlineSeconds = 3600
	})

	got, err := client.constructJobSet(ctx, workUnit, testTarget, "test-author", constants.XmcLabelValue, testTime, Priorities{})
	if err != nil {
		t.Fatalf("constructJobSet() returned unexpected error: %v", err)
	}

	if len(got.Spec.ReplicatedJobs) != 1 {
		t.Fatalf("Expected 1 replicated job, got %d", len(got.Spec.ReplicatedJobs))
	}

	if got.Spec.ReplicatedJobs[0].Template.Spec.ActiveDeadlineSeconds == nil {
		t.Fatal("Expected ActiveDeadlineSeconds to be set, but it was nil")
	}

	if *got.Spec.ReplicatedJobs[0].Template.Spec.ActiveDeadlineSeconds != 3600 {
		t.Errorf("Expected ActiveDeadlineSeconds to be 3600, got %d", *got.Spec.ReplicatedJobs[0].Template.Spec.ActiveDeadlineSeconds)
	}
}

func TestConstructJobSet_DependencyOrder(t *testing.T) {
	ctx, client, _ := setupTestClient(true /* createNewNamespaces */, &alwaysPassingPreconditionsValidator{})
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
						{
							Name: "job2",
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
							DependencyOrder: &esspb.KubernetesJob_DependencyOrder{
								JobName:            "job1",
								JobDependencyState: esspb.KubernetesJob_DependencyOrder_READY,
							},
						},
						{
							Name: "job3",
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
							DependencyOrder: &esspb.KubernetesJob_DependencyOrder{
								JobName:            "job2",
								JobDependencyState: esspb.KubernetesJob_DependencyOrder_COMPLETED,
							},
						},
						{
							Name: "job4",
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
							DependencyOrder: &esspb.KubernetesJob_DependencyOrder{
								JobName:            "job3",
								JobDependencyState: esspb.KubernetesJob_DependencyOrder_JOB_DEPENDENCY_STATE_UNSPECIFIED,
							},
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

	if len(got.Spec.ReplicatedJobs) != 4 {
		t.Fatalf("Expected 4 replicated jobs, got %d", len(got.Spec.ReplicatedJobs))
	}

	// Check job1 (no dependencies)
	if len(got.Spec.ReplicatedJobs[0].DependsOn) != 0 {
		t.Errorf("Expected job1 to have no dependencies, got %v", got.Spec.ReplicatedJobs[0].DependsOn)
	}

	// Check job2 (depends on job1 being READY)
	if len(got.Spec.ReplicatedJobs[1].DependsOn) != 1 {
		t.Fatalf("Expected job2 to have 1 dependency, got %d", len(got.Spec.ReplicatedJobs[1].DependsOn))
	}
	if got.Spec.ReplicatedJobs[1].DependsOn[0].Name != "job1" {
		t.Errorf("Expected job2 to depend on job1, got %s", got.Spec.ReplicatedJobs[1].DependsOn[0].Name)
	}
	if got.Spec.ReplicatedJobs[1].DependsOn[0].Status != jobsetv1alpha2.DependencyReady {
		t.Errorf("Expected job2 to depend on job1 with status READY, got %v", got.Spec.ReplicatedJobs[1].DependsOn[0].Status)
	}

	// Check job3 (depends on job2 being COMPLETED)
	if len(got.Spec.ReplicatedJobs[2].DependsOn) != 1 {
		t.Fatalf("Expected job3 to have 1 dependency, got %d", len(got.Spec.ReplicatedJobs[2].DependsOn))
	}
	if got.Spec.ReplicatedJobs[2].DependsOn[0].Name != "job2" {
		t.Errorf("Expected job3 to depend on job2, got %s", got.Spec.ReplicatedJobs[2].DependsOn[0].Name)
	}
	if got.Spec.ReplicatedJobs[2].DependsOn[0].Status != jobsetv1alpha2.DependencyComplete {
		t.Errorf("Expected job3 to depend on job2 with status COMPLETED, got %v", got.Spec.ReplicatedJobs[2].DependsOn[0].Status)
	}

	// Check job4 (depends on job3 with UNSPECIFIED status)
	if len(got.Spec.ReplicatedJobs[3].DependsOn) != 1 {
		t.Fatalf("Expected job4 to have 1 dependency, got %d", len(got.Spec.ReplicatedJobs[3].DependsOn))
	}
	if got.Spec.ReplicatedJobs[3].DependsOn[0].Name != "job3" {
		t.Errorf("Expected job4 to depend on job3, got %s", got.Spec.ReplicatedJobs[3].DependsOn[0].Name)
	}
	if got.Spec.ReplicatedJobs[3].DependsOn[0].Status != jobsetv1alpha2.DependencyReady {
		t.Errorf("Expected job4 to depend on job3 with status READY, got %v", got.Spec.ReplicatedJobs[3].DependsOn[0].Status)
	}
}

func TestConstructJobSet_NoJobset(t *testing.T) {
	ctx, client, _ := setupTestClient(true /* createNewNamespaces */, &alwaysPassingPreconditionsValidator{})
	workUnit := &esspb.WorkUnit{
		Name:       "workUnit1",
		Executable: &esspb.Executable{},
	}

	_, err := client.constructJobSet(ctx, workUnit, testTarget, "test-author", constants.XmcLabelValue, testTime, Priorities{})
	if err == nil {
		t.Errorf("constructJobSet() with no jobset succeeded, want error")
	}
}

func TestConstructJobSet_NoExecutable(t *testing.T) {
	ctx, client, _ := setupTestClient(true /* createNewNamespaces */, &alwaysPassingPreconditionsValidator{})
	workUnit := &esspb.WorkUnit{
		Name:       "workUnit1",
		Executable: nil,
	}

	_, err := client.constructJobSet(ctx, workUnit, testTarget, "test-author", constants.XmcLabelValue, testTime, Priorities{})
	if err == nil {
		t.Errorf("constructJobSet() with no executable succeeded, want error")
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
	client, _, now := setupPopulateTest(t, target)
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
	client, _, now := setupPopulateTest(t, target)
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
	client, dynamicClient, now := setupPopulateTest(t, target)
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
	client, _, _ := setupPopulateTest(t, target)
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
	workUnit := proto.Modify(baseCPUWorkUnit, func(wu *esspb.WorkUnit) {
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

func TestConstructJobSet_GPU_MultiReplica_WithCoordinator(t *testing.T) {
	ctx, client, _ := setupTestClient(true /* createNewNamespaces */, &alwaysPassingPreconditionsValidator{})
	workUnit := &esspb.WorkUnit{
		Name: "workUnit1",
		Executable: &esspb.Executable{
			Type: &esspb.Executable_Jobset{
				Jobset: &esspb.JobSetExecutable{
					Coordinator: &esspb.JobSetExecutable_Coordinator{
						ReplicatedJob: "job1",
						JobIndex:      proto.Int32(2),
						PodIndex:      proto.Int32(0),
					},
					Jobs: []*esspb.KubernetesJob{
						{
							Name: "job1",
							Spec: &esspb.ExecutableSpec{
								Resources: &esspb.ResourceSet{
									Resources: &esspb.ResourceSet_GpuResources{
										GpuResources: &esspb.GpuResources{
											Type:  "H100",
											Count: 8,
										},
									},
								},
								DockerImage: "foo/bar",
							},
							Replicas: 4,
						},
					},
				},
			},
		},
	}

	got, err := client.constructJobSet(ctx, workUnit, testTarget, "test-author", "test-namespace", testTime, Priorities{})
	if err != nil {
		t.Fatalf("constructJobSet() returned unexpected error: %v", err)
	}

	if got.Spec.Coordinator == nil {
		t.Fatalf("Expected Coordinator to be set, got nil")
	}
	if got.Spec.Coordinator.JobIndex != 0 {
		t.Errorf("Expected Coordinator.JobIndex to be mapped to 0, got %d", got.Spec.Coordinator.JobIndex)
	}
	if got.Spec.Coordinator.PodIndex != 2 {
		t.Errorf("Expected Coordinator.PodIndex to be mapped to 2, got %d", got.Spec.Coordinator.PodIndex)
	}
}

func TestConstructJobSet_GPU_DefaultReplica(t *testing.T) {
	ctx, client, _ := setupTestClient(true /* createNewNamespaces */, &alwaysPassingPreconditionsValidator{})
	workUnit := proto.Modify(baseCPUWorkUnit, func(wu *esspb.WorkUnit) {
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
	workUnit := proto.Modify(baseCPUWorkUnit, func(wu *esspb.WorkUnit) {
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
	workUnit := proto.Modify(baseCPUWorkUnit, func(wu *esspb.WorkUnit) {
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
	workUnit := proto.Modify(baseCPUWorkUnit, func(wu *esspb.WorkUnit) {
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

func TestConstructJobSet_WithSidecars(t *testing.T) {
	ctx, client, _ := setupTestClient(true, &alwaysPassingPreconditionsValidator{})
	workUnit := createTestWorkUnit("test-namespace")
	workUnit.Executable.GetJobset().Jobs[0].Sidecars = []*esspb.ExecutableSpec{
		{
			Name:        "sidecar-1",
			DockerImage: "sidecar-image:v1",
			Command:     []string{"/bin/sidecar"},
			Resources: &esspb.ResourceSet{
				Resources: &esspb.ResourceSet_CpuResources{
					CpuResources: &esspb.CpuResources{
						CpuMillicores: 50,
						MemoryBytes:   512,
					},
				},
			},
		},
	}

	got, err := client.constructJobSet(ctx, workUnit, testTarget, "test-author", "test-namespace", testTime, Priorities{})
	if err != nil {
		t.Fatalf("constructJobSet() returned unexpected error: %v", err)
	}

	containers := got.Spec.ReplicatedJobs[0].Template.Spec.Template.Spec.Containers
	if len(containers) != 1 {
		t.Fatalf("Expected 1 container (primary), got %d", len(containers))
	}
	if containers[0].Name != "job1" {
		t.Errorf("Container name mismatch: got %s, want job1", containers[0].Name)
	}
	initContainers := got.Spec.ReplicatedJobs[0].Template.Spec.Template.Spec.InitContainers
	if len(initContainers) != 1 {
		t.Fatalf("Expected 1 init container (sidecar), got %d", len(initContainers))
	}
	if initContainers[0].Name != "sidecar-1" {
		t.Errorf("Sidecar name mismatch: got %s, want sidecar-1", initContainers[0].Name)
	}
	if initContainers[0].Image != "sidecar-image:v1" {
		t.Errorf("Sidecar image mismatch: got %q, want %q", initContainers[0].Image, "sidecar-image:v1")
	}
	if initContainers[0].RestartPolicy == nil || *initContainers[0].RestartPolicy != corev1.ContainerRestartPolicyAlways {
		t.Errorf("Sidecar restart policy mismatch: got %v, want %s", initContainers[0].RestartPolicy, corev1.ContainerRestartPolicyAlways)
	}
}

func TestConstructJobSet_WithInitContainers(t *testing.T) {
	ctx, client, _ := setupTestClient(true, &alwaysPassingPreconditionsValidator{})
	workUnit := createTestWorkUnit("test-namespace")
	workUnit.Executable.GetJobset().Jobs[0].InitContainers = []*esspb.ExecutableSpec{
		{
			Name:        "init-1",
			DockerImage: "init-image:v1",
			Command:     []string{"/bin/init"},
			Resources: &esspb.ResourceSet{
				Resources: &esspb.ResourceSet_CpuResources{
					CpuResources: &esspb.CpuResources{
						CpuMillicores: 50,
						MemoryBytes:   512,
					},
				},
			},
		},
	}

	got, err := client.constructJobSet(ctx, workUnit, testTarget, "test-author", "test-namespace", testTime, Priorities{})
	if err != nil {
		t.Fatalf("constructJobSet() returned unexpected error: %v", err)
	}

	initContainers := got.Spec.ReplicatedJobs[0].Template.Spec.Template.Spec.InitContainers
	if len(initContainers) != 1 {
		t.Fatalf("Expected 1 init container, got %d", len(initContainers))
	}
	if initContainers[0].Name != "init-1" {
		t.Errorf("InitContainer name mismatch: got %q, want %q", initContainers[0].Name, "init-1")
	}
	if initContainers[0].Image != "init-image:v1" {
		t.Errorf("InitContainer image mismatch: got %q, want %q", initContainers[0].Image, "init-image:v1")
	}
}

func TestConstructJobSet_DefaultNetworkMultiJob(t *testing.T) {
	ctx, client, _ := setupTestClient(true /* createNewNamespaces */, &alwaysPassingPreconditionsValidator{})
	workUnit := proto.Modify(baseCPUWorkUnit, func(wu *esspb.WorkUnit) {
		wu.Name = "workUnitMulti"
		job1 := wu.Executable.GetJobset().Jobs[0]
		job2 := proto.Clone(job1).(*esspb.KubernetesJob)
		job2.Name = "job2"
		wu.Executable.GetJobset().Jobs = []*esspb.KubernetesJob{job1, job2}
	})

	got, err := client.constructJobSet(ctx, workUnit, testTarget, "test-author", "test-namespace", testTime, Priorities{})
	if err != nil {
		t.Fatalf("constructJobSet() returned unexpected error: %v", err)
	}

	if got.Spec.Network == nil {
		t.Fatalf("Expected Spec.Network to be populated for multi-job WorkUnit, got nil")
	}
	if got.Spec.Network.EnableDNSHostnames == nil || !*got.Spec.Network.EnableDNSHostnames {
		t.Errorf("Expected EnableDNSHostnames to be true, got %v", got.Spec.Network.EnableDNSHostnames)
	}
	if got.Spec.Network.PublishNotReadyAddresses == nil || !*got.Spec.Network.PublishNotReadyAddresses {
		t.Errorf("Expected PublishNotReadyAddresses to be true, got %v", got.Spec.Network.PublishNotReadyAddresses)
	}
	wantSubdomain := got.Name
	if got.Spec.Network.Subdomain != wantSubdomain {
		t.Errorf("Subdomain mismatch: got %q, want %q", got.Spec.Network.Subdomain, wantSubdomain)
	}
}

func TestConstructJobSet_SingleJobNoNetwork(t *testing.T) {
	ctx, client, _ := setupTestClient(true /* createNewNamespaces */, &alwaysPassingPreconditionsValidator{})
	workUnit := proto.Modify(baseCPUWorkUnit, func(wu *esspb.WorkUnit) {
		wu.Name = "workUnitSingle"
	})

	got, err := client.constructJobSet(ctx, workUnit, testTarget, "test-author", "test-namespace", testTime, Priorities{})
	if err != nil {
		t.Fatalf("constructJobSet() returned unexpected error: %v", err)
	}

	if got.Spec.Network != nil {
		t.Errorf("Expected Spec.Network to be nil for single-replica single-job WorkUnit, got %+v", got.Spec.Network)
	}
}

func TestConstructJobSet_ExplicitNetworkOverride(t *testing.T) {
	ctx, client, _ := setupTestClient(true /* createNewNamespaces */, &alwaysPassingPreconditionsValidator{})
	enableDNS := true
	publishNotReady := false
	workUnit := proto.Modify(baseCPUWorkUnit, func(wu *esspb.WorkUnit) {
		wu.Name = "workUnitOverride"
		wu.Executable.GetJobset().Network = &esspb.JobSetExecutable_Network{
			EnableDnsHostnames:       &enableDNS,
			Subdomain:                "custom-subdomain",
			PublishNotReadyAddresses: &publishNotReady,
		}
	})

	got, err := client.constructJobSet(ctx, workUnit, testTarget, "test-author", "test-namespace", testTime, Priorities{})
	if err != nil {
		t.Fatalf("constructJobSet() returned unexpected error: %v", err)
	}

	if got.Spec.Network == nil {
		t.Fatalf("Expected Spec.Network to be populated when explicitly set, got nil")
	}
	if got.Spec.Network.EnableDNSHostnames == nil || !*got.Spec.Network.EnableDNSHostnames {
		t.Errorf("Expected EnableDNSHostnames to be true, got %v", got.Spec.Network.EnableDNSHostnames)
	}
	if got.Spec.Network.Subdomain != "custom-subdomain" {
		t.Errorf("Subdomain mismatch: got %q, want %q", got.Spec.Network.Subdomain, "custom-subdomain")
	}
	if got.Spec.Network.PublishNotReadyAddresses == nil || *got.Spec.Network.PublishNotReadyAddresses {
		t.Errorf("Expected PublishNotReadyAddresses to be false, got %v", got.Spec.Network.PublishNotReadyAddresses)
	}
}

func TestConstructJobSet_MultiReplicaNetwork(t *testing.T) {
	ctx, client, _ := setupTestClient(true /* createNewNamespaces */, &alwaysPassingPreconditionsValidator{})
	workUnit := proto.Modify(baseCPUWorkUnit, func(wu *esspb.WorkUnit) {
		wu.Name = "workUnitMultiReplica"
		wu.Executable.GetJobset().Jobs[0].Replicas = 2
	})

	got, err := client.constructJobSet(ctx, workUnit, testTarget, "test-author", "test-namespace", testTime, Priorities{})
	if err != nil {
		t.Fatalf("constructJobSet() returned unexpected error: %v", err)
	}

	if got.Spec.Network == nil {
		t.Fatalf("Expected Spec.Network to be populated for multi-replica WorkUnit, got nil")
	}
	if got.Spec.Network.EnableDNSHostnames == nil || !*got.Spec.Network.EnableDNSHostnames {
		t.Errorf("Expected EnableDNSHostnames to be true, got %v", got.Spec.Network.EnableDNSHostnames)
	}
}

func TestConstructJobSet_PriorityClassName(t *testing.T) {
	ctx, client, _ := setupTestClient(true /* createNewNamespaces */, &alwaysPassingPreconditionsValidator{})
	workUnit := proto.Modify(baseCPUWorkUnit, func(wu *esspb.WorkUnit) {
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

func TestShouldEnableJobSetNetwork_NilChecks(t *testing.T) {
	if got := shouldEnableJobSetNetwork(&esspb.WorkUnit{Executable: nil}); got {
		t.Errorf("shouldEnableJobSetNetwork(nil Executable) = true, want false")
	}
	if got := shouldEnableJobSetNetwork(&esspb.WorkUnit{Executable: &esspb.Executable{}}); got {
		t.Errorf("shouldEnableJobSetNetwork(nil Jobset) = true, want false")
	}
}

func TestShouldEnableJobSetNetwork_Conditions(t *testing.T) {
	trueVal, falseVal := true, false
	tests := []struct {
		name string
		wu   *esspb.WorkUnit
		want bool
	}{
		{
			name: "explicit_true",
			wu: &esspb.WorkUnit{Executable: &esspb.Executable{Type: &esspb.Executable_Jobset{Jobset: &esspb.JobSetExecutable{
				Network: &esspb.JobSetExecutable_Network{EnableDnsHostnames: &trueVal},
			}}}},
			want: true,
		},
		{
			name: "explicit_false",
			wu: &esspb.WorkUnit{Executable: &esspb.Executable{Type: &esspb.Executable_Jobset{Jobset: &esspb.JobSetExecutable{
				Network: &esspb.JobSetExecutable_Network{EnableDnsHostnames: &falseVal},
				Jobs:    []*esspb.KubernetesJob{{Replicas: 2}},
			}}}},
			want: false,
		},
		{
			name: "multi_job",
			wu: &esspb.WorkUnit{Executable: &esspb.Executable{Type: &esspb.Executable_Jobset{Jobset: &esspb.JobSetExecutable{
				Jobs: []*esspb.KubernetesJob{{Name: "j1"}, {Name: "j2"}},
			}}}},
			want: true,
		},
		{
			name: "multi_replica",
			wu: &esspb.WorkUnit{Executable: &esspb.Executable{Type: &esspb.Executable_Jobset{Jobset: &esspb.JobSetExecutable{
				Jobs: []*esspb.KubernetesJob{{Replicas: 2}},
			}}}},
			want: true,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := shouldEnableJobSetNetwork(tc.wu); got != tc.want {
				t.Errorf("shouldEnableJobSetNetwork() = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestBuildJobSetSuccessPolicy(t *testing.T) {
	tests := []struct {
		name string
		wu   *esspb.WorkUnit
		want *jobsetv1alpha2.SuccessPolicy
	}{
		{"nil_exec", &esspb.WorkUnit{}, &jobsetv1alpha2.SuccessPolicy{Operator: jobsetv1alpha2.OperatorAll}},
		{"explicit_any", &esspb.WorkUnit{Executable: &esspb.Executable{Type: &esspb.Executable_Jobset{Jobset: &esspb.JobSetExecutable{SuccessPolicy: &esspb.JobSetExecutable_SuccessPolicy{Operator: esspb.JobSetExecutable_SuccessPolicy_ANY, TargetReplicatedJobs: []string{"w"}}}}}}, &jobsetv1alpha2.SuccessPolicy{Operator: jobsetv1alpha2.OperatorAny, TargetReplicatedJobs: []string{"w"}}},
		{"default_pathways", &esspb.WorkUnit{Executable: &esspb.Executable{Type: &esspb.Executable_Jobset{Jobset: &esspb.JobSetExecutable{Jobs: []*esspb.KubernetesJob{{Name: constants.PathwaysHeadJobName}, {Name: "workers"}}}}}}, &jobsetv1alpha2.SuccessPolicy{Operator: jobsetv1alpha2.OperatorAll, TargetReplicatedJobs: []string{constants.PathwaysHeadJobName}}},
		{"fallback_all", &esspb.WorkUnit{Executable: &esspb.Executable{Type: &esspb.Executable_Jobset{Jobset: &esspb.JobSetExecutable{Jobs: []*esspb.KubernetesJob{{Name: "job1"}}}}}}, &jobsetv1alpha2.SuccessPolicy{Operator: jobsetv1alpha2.OperatorAll}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if diff := cmp.Diff(tc.want, buildJobSetSuccessPolicy(tc.wu)); diff != "" {
				t.Errorf("buildJobSetSuccessPolicy() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestConstructJobSet_SuccessPolicy(t *testing.T) {
	ctx, client, _ := setupTestClient(true, &alwaysPassingPreconditionsValidator{})
	workUnit := proto.Modify(baseCPUWorkUnit, func(wu *esspb.WorkUnit) {
		wu.Executable.GetJobset().Jobs[0].Name = constants.PathwaysHeadJobName
	})
	jobSet, err := client.constructJobSet(ctx, workUnit, "experiments/1/workUnits/1/executions/1", "user", "test-queue", time.Now(), Priorities{})
	if err != nil {
		t.Fatalf("constructJobSet() unexpected error: %v", err)
	}
	want := &jobsetv1alpha2.SuccessPolicy{
		Operator:             jobsetv1alpha2.OperatorAll,
		TargetReplicatedJobs: []string{constants.PathwaysHeadJobName},
	}
	if diff := cmp.Diff(want, jobSet.Spec.SuccessPolicy); diff != "" {
		t.Errorf("SuccessPolicy mismatch (-want +got):\n%s", diff)
	}
}

func TestBuildContainerPorts_ExplicitAndDefault(t *testing.T) {
	ctx, client, _ := setupTestClient(true, &alwaysPassingPreconditionsValidator{})
	workUnit := createTestWorkUnit("test-namespace")

	workUnit.Executable.GetJobset().Jobs[0].Spec.Ports = []*esspb.ContainerPort{
		{Name: "http", ContainerPort: 8080, Protocol: esspb.ContainerPort_TCP},
		{Name: "metrics", ContainerPort: 8431},
		{Name: "dns", ContainerPort: 53, Protocol: esspb.ContainerPort_UDP},
		{Name: "sctp-port", ContainerPort: 9999, Protocol: esspb.ContainerPort_SCTP, HostPort: 9999},
		{Name: "http-lower", ContainerPort: 8081, Protocol: esspb.ContainerPort_TCP},
		{Name: "dns-lower", ContainerPort: 54, Protocol: esspb.ContainerPort_UDP},
		{Name: "sctp-lower", ContainerPort: 10000, Protocol: esspb.ContainerPort_SCTP},
	}

	workUnit.Executable.GetJobset().Jobs[0].Sidecars = []*esspb.ExecutableSpec{
		{
			Name:        "sidecar-1",
			DockerImage: "sidecar-image:v1",
			Ports: []*esspb.ContainerPort{
				{Name: "sidecar-port", ContainerPort: 9090, Protocol: esspb.ContainerPort_TCP},
			},
		},
	}

	workUnit.Executable.GetJobset().Jobs[0].InitContainers = []*esspb.ExecutableSpec{
		{
			Name:        "init-1",
			DockerImage: "init-image:v1",
			Command:     []string{"/bin/init"},
			Ports: []*esspb.ContainerPort{
				{Name: "init-port", ContainerPort: 29001, Protocol: esspb.ContainerPort_TCP},
			},
		},
	}

	got, err := client.constructJobSet(ctx, workUnit, testTarget, "test-author", "test-namespace", testTime, Priorities{})
	if err != nil {
		t.Fatalf("constructJobSet() returned unexpected error: %v", err)
	}

	podSpec := got.Spec.ReplicatedJobs[0].Template.Spec.Template.Spec
	if len(podSpec.InitContainers) != 2 {
		t.Fatalf("Expected 2 init containers (init + sidecar), got %d", len(podSpec.InitContainers))
	}
	if len(podSpec.InitContainers[0].Ports) != 1 || podSpec.InitContainers[0].Ports[0].ContainerPort != 29001 {
		t.Errorf("Init container port mismatch: got %+v", podSpec.InitContainers[0].Ports)
	}
	if podSpec.InitContainers[0].RestartPolicy != nil {
		t.Errorf("Init container restart policy should be nil, got %v", *podSpec.InitContainers[0].RestartPolicy)
	}
	if podSpec.InitContainers[1].RestartPolicy == nil || *podSpec.InitContainers[1].RestartPolicy != corev1.ContainerRestartPolicyAlways {
		t.Errorf("Sidecar restart policy mismatch: got %v, want %s", podSpec.InitContainers[1].RestartPolicy, corev1.ContainerRestartPolicyAlways)
	}

	if len(podSpec.Containers) != 1 {
		t.Fatalf("Expected 1 container (primary), got %d", len(podSpec.Containers))
	}
	primaryPorts := podSpec.Containers[0].Ports
	if len(primaryPorts) != 7 {
		t.Fatalf("Expected 7 ports on primary container, got %d", len(primaryPorts))
	}
	wantPorts := []corev1.ContainerPort{
		{Name: "http", ContainerPort: 8080, Protocol: corev1.ProtocolTCP},
		{Name: "metrics", ContainerPort: 8431, Protocol: corev1.ProtocolTCP},
		{Name: "dns", ContainerPort: 53, Protocol: corev1.ProtocolUDP},
		{Name: "sctp-port", ContainerPort: 9999, Protocol: corev1.ProtocolSCTP, HostPort: 9999},
		{Name: "http-lower", ContainerPort: 8081, Protocol: corev1.ProtocolTCP},
		{Name: "dns-lower", ContainerPort: 54, Protocol: corev1.ProtocolUDP},
		{Name: "sctp-lower", ContainerPort: 10000, Protocol: corev1.ProtocolSCTP},
	}
	if diff := cmp.Diff(wantPorts, primaryPorts); diff != "" {
		t.Errorf("Primary container ports mismatch (-want +got):\n%s", diff)
	}

	sidecarPorts := podSpec.InitContainers[1].Ports
	if len(sidecarPorts) != 1 || sidecarPorts[0].ContainerPort != 9090 {
		t.Errorf("Sidecar ports mismatch: got %+v", sidecarPorts)
	}
}

func TestConstructJobSet_WithCoordinator(t *testing.T) {
	ctx, client, _ := setupTestClient(true /* createNewNamespaces */, &alwaysPassingPreconditionsValidator{})
	workUnit := proto.Modify(baseCPUWorkUnit, func(wu *esspb.WorkUnit) {
		wu.Name = "workUnitWithCoordinator"
		wu.Executable.GetJobset().Coordinator = &esspb.JobSetExecutable_Coordinator{
			ReplicatedJob: "head",
			JobIndex:      proto.Int32(0),
			PodIndex:      proto.Int32(0),
		}
		headJob := wu.Executable.GetJobset().Jobs[0]
		headJob.Name = "head"
		headJob.Replicas = 1

		workerJob := proto.Clone(headJob).(*esspb.KubernetesJob)
		workerJob.Name = "worker"
		workerJob.Replicas = 4

		wu.Executable.GetJobset().Jobs = []*esspb.KubernetesJob{headJob, workerJob}
	})

	got, err := client.constructJobSet(ctx, workUnit, testTarget, "test-author", "test-namespace", testTime, Priorities{})
	if err != nil {
		t.Fatalf("constructJobSet() returned unexpected error: %v", err)
	}

	if got.Spec.Coordinator == nil {
		t.Fatalf("Expected Spec.Coordinator to be populated, got nil")
	}
	if got.Spec.Coordinator.ReplicatedJob != "head" {
		t.Errorf("ReplicatedJob mismatch: got %q, want %q", got.Spec.Coordinator.ReplicatedJob, "head")
	}
	if got.Spec.Coordinator.JobIndex != 0 {
		t.Errorf("JobIndex mismatch: got %d, want %d", got.Spec.Coordinator.JobIndex, 0)
	}
	if got.Spec.Coordinator.PodIndex != 0 {
		t.Errorf("PodIndex mismatch: got %d, want %d", got.Spec.Coordinator.PodIndex, 0)
	}
}

func TestShouldEnableJobSetCoordinator(t *testing.T) {
	if got := shouldEnableJobSetCoordinator(nil); got {
		t.Errorf("shouldEnableJobSetCoordinator(nil) = true, want false")
	}
	if got := shouldEnableJobSetCoordinator(&esspb.WorkUnit{Executable: nil}); got {
		t.Errorf("shouldEnableJobSetCoordinator(nil Executable) = true, want false")
	}
	if got := shouldEnableJobSetCoordinator(&esspb.WorkUnit{Executable: &esspb.Executable{}}); got {
		t.Errorf("shouldEnableJobSetCoordinator(nil Jobset) = true, want false")
	}
	if got := shouldEnableJobSetCoordinator(&esspb.WorkUnit{Executable: &esspb.Executable{Type: &esspb.Executable_Jobset{Jobset: &esspb.JobSetExecutable{Coordinator: nil}}}}); got {
		t.Errorf("shouldEnableJobSetCoordinator(nil Coordinator) = true, want false")
	}
	if got := shouldEnableJobSetCoordinator(&esspb.WorkUnit{Executable: &esspb.Executable{Type: &esspb.Executable_Jobset{Jobset: &esspb.JobSetExecutable{Coordinator: &esspb.JobSetExecutable_Coordinator{ReplicatedJob: ""}}}}}); got {
		t.Errorf("shouldEnableJobSetCoordinator(empty ReplicatedJob) = true, want false")
	}
	if got := shouldEnableJobSetCoordinator(&esspb.WorkUnit{Executable: &esspb.Executable{Type: &esspb.Executable_Jobset{Jobset: &esspb.JobSetExecutable{Coordinator: &esspb.JobSetExecutable_Coordinator{ReplicatedJob: "worker"}}}}}); !got {
		t.Errorf("shouldEnableJobSetCoordinator(valid Coordinator) = false, want true")
	}
}

func TestConstructJobSet_ManagedXprofGCSInjection(t *testing.T) {
	// The actuator injects the GCS destination purely from the MANAGED_XPROF_GCS_PATH
	// env var, decoupled from the ManagedXprof artifact or any "requested" env flag.
	tests := []struct {
		name          string
		gcsPathEnv    string
		wantGCSBucket string
		wantGCSObject string
	}{
		{
			name:          "gcs path env present injects GCS env",
			gcsPathEnv:    "gs://my-awesome-profile-bucket",
			wantGCSBucket: "gs://my-awesome-profile-bucket",
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

			if got := envMap[constants.XmcEnvVarManagedXprofGCSBucket]; got != tc.wantGCSBucket {
				t.Errorf("GCS bucket env = %q, want %q", got, tc.wantGCSBucket)
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

func TestInjectManagedXprofGCSEnvVars_AllContainers(t *testing.T) {
	const (
		wantBucket = "gs://my-bucket"
		wantObject = "gs://my-bucket/xmc-1-1-100"
	)

	newReplicatedJob := func(numContainers int) *jobsetv1alpha2.ReplicatedJob {
		rj := &jobsetv1alpha2.ReplicatedJob{}
		for i := 0; i < numContainers; i++ {
			rj.Template.Spec.Template.Spec.Containers = append(
				rj.Template.Spec.Template.Spec.Containers,
				corev1.Container{Name: fmt.Sprintf("container-%d", i)},
			)
			rj.Template.Spec.Template.Spec.InitContainers = append(
				rj.Template.Spec.Template.Spec.InitContainers,
				corev1.Container{Name: fmt.Sprintf("init-container-%d", i)},
			)
		}
		return rj
	}

	tests := []struct {
		name          string
		numContainers int
		bucket        string
		object        string
		wantInjected  bool
	}{
		{
			name:          "injects into every container",
			numContainers: 3,
			bucket:        wantBucket,
			object:        wantObject,
			wantInjected:  true,
		},
		{
			name:          "single container still injected",
			numContainers: 1,
			bucket:        wantBucket,
			object:        wantObject,
			wantInjected:  true,
		},
		{
			name:          "no bucket or object is a no-op",
			numContainers: 2,
			wantInjected:  false,
		},
		{
			name:          "no containers is a safe no-op",
			numContainers: 0,
			bucket:        wantBucket,
			object:        wantObject,
			wantInjected:  false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rj := newReplicatedJob(tc.numContainers)
			injectManagedXprofGCSEnvVars(rj, tc.bucket, tc.object)

			for _, containers := range [][]corev1.Container{
				rj.Template.Spec.Template.Spec.Containers,
				rj.Template.Spec.Template.Spec.InitContainers,
			} {
				for i := range containers {
					envMap := make(map[string]string)
					for _, ev := range containers[i].Env {
						envMap[ev.Name] = ev.Value
					}
					gotBucket, hasBucket := envMap[constants.XmcEnvVarManagedXprofGCSBucket]
					gotObject, hasObject := envMap[constants.XmcEnvVarManagedXprofGCSObjectFolder]
					if tc.wantInjected {
						if gotBucket != tc.bucket {
							t.Errorf("container[%d] %s = %q, want %q", i, constants.XmcEnvVarManagedXprofGCSBucket, gotBucket, tc.bucket)
						}
						if gotObject != tc.object {
							t.Errorf("container[%d] %s = %q, want %q", i, constants.XmcEnvVarManagedXprofGCSObjectFolder, gotObject, tc.object)
						}
					} else if hasBucket || hasObject {
						t.Errorf("container[%d] unexpectedly got managed-xprof env vars: %v", i, envMap)
					}
				}
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

	workUnit := proto.Modify(baseCPUWorkUnit, func(wu *esspb.WorkUnit) {
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
