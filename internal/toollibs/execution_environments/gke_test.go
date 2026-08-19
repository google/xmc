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
package executionenvironments

import (
	"context"
	"testing"

	"golang/cmp/cmp"
	batchv1 "golang/k8s_io/api/v/v0_23/batch/v1/v1"
	corev1 "golang/k8s_io/api/v/v0_23/core/v1/v1"
	"golang/k8s_io/apimachinery/v/v0_23/pkg/api/resource/resource"
	metav1 "golang/k8s_io/apimachinery/v/v0_23/pkg/apis/meta/v1/v1"
	"golang/protobuf/v2/proto/proto"
	"golang/protobuf/v2/testing/protocmp/protocmp"
	jobsetv1alpha2 "sigs.k8s.io/jobset/api/jobset/v1alpha2"
	epb "xmanager_cloud/experiment_state_server/proto/messages_go_proto"
)

func TestGKEName(t *testing.T) {
	gke := &GKE{}
	if gke.Name() != name {
		t.Errorf("Name() = %q, want %q", gke.Name(), name)
	}
}

func TestGKEDescription(t *testing.T) {
	gke := &GKE{}
	if gke.Description() != description {
		t.Errorf("Description() = %q, want %q", gke.Description(), description)
	}
}

func TestGKEIsCompatibleWithWorkUnit(t *testing.T) {
	gke := &GKE{}
	tests := []struct {
		name       string
		workUnit   *epb.WorkUnit
		wantOK     bool
		wantReason string
	}{
		{
			name:     "nil work unit",
			workUnit: nil,
			wantOK:   true,
		},
		{
			name:     "work unit without jobset",
			workUnit: &epb.WorkUnit{},
			wantOK:   true,
		},
		{
			name: "work unit with valid TPU topology",
			workUnit: &epb.WorkUnit{
				Executable: &epb.Executable{
					Type: &epb.Executable_Jobset{Jobset: &epb.JobSetExecutable{
						Jobs: []*epb.KubernetesJob{
							{
								Name: "job1",
								Spec: &epb.ExecutableSpec{
									Resources: &epb.ResourceSet{
										Resources: &epb.ResourceSet_TpuResources{TpuResources: &epb.TpuResources{
											Type:     "tpu-v6e-slice",
											Topology: "2x2",
										}},
									},
								},
							},
						},
					}},
				},
			},
			wantOK: true,
		},
		{
			name: "work unit with invalid TPU topology",
			workUnit: &epb.WorkUnit{
				Executable: &epb.Executable{
					Type: &epb.Executable_Jobset{Jobset: &epb.JobSetExecutable{
						Jobs: []*epb.KubernetesJob{
							{
								Name: "job1",
								Spec: &epb.ExecutableSpec{
									Resources: &epb.ResourceSet{
										Resources: &epb.ResourceSet_TpuResources{TpuResources: &epb.TpuResources{
											Type:     "tpu-v6e-slice",
											Topology: "invalid",
										}},
									},
								},
							},
						},
					}},
				},
			},
			wantOK: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ok, reason := gke.IsCompatibleWithWorkUnit(tc.workUnit)
			if ok != tc.wantOK {
				t.Errorf("IsCompatibleWithWorkUnit() ok = %v (reason: %s), want %v", ok, reason, tc.wantOK)
			}
			if !tc.wantOK && tc.wantReason != "" && reason != tc.wantReason {
				t.Errorf("IsCompatibleWithWorkUnit() reason = %q, want %q", reason, tc.wantReason)
			}
		})
	}
}

func TestGKEInjectOnJobSetCreation(t *testing.T) {
	gke := &GKE{}
	baseCPUWorkUnit := &epb.WorkUnit{
		Executable: &epb.Executable{
			Type: &epb.Executable_Jobset{
				Jobset: &epb.JobSetExecutable{
					Jobs: []*epb.KubernetesJob{
						{
							Name: "test-job",
							Spec: &epb.ExecutableSpec{
								Resources: &epb.ResourceSet{
									Resources: &epb.ResourceSet_CpuResources{
										CpuResources: &epb.CpuResources{
											CpuMillicores: 1000,
											MemoryBytes:   2048,
										},
									},
								},
							},
						},
					},
				},
			},
		},
	}

	tests := []struct {
		name       string
		workUnit   *epb.WorkUnit
		jobSet     *jobsetv1alpha2.JobSet
		wantJobSet *jobsetv1alpha2.JobSet
	}{
		{
			name:     "cpu_resources",
			workUnit: baseCPUWorkUnit,
			jobSet: &jobsetv1alpha2.JobSet{
				Spec: jobsetv1alpha2.JobSetSpec{
					ReplicatedJobs: []jobsetv1alpha2.ReplicatedJob{
						{
							Name: "test-job",
							Template: batchv1.JobTemplateSpec{
								Spec: batchv1.JobSpec{
									Template: corev1.PodTemplateSpec{
										Spec: corev1.PodSpec{
											Containers:   []corev1.Container{{}},
											NodeSelector: map[string]string{},
										},
									},
								},
							},
						},
					},
				},
			},
			wantJobSet: &jobsetv1alpha2.JobSet{
				Spec: jobsetv1alpha2.JobSetSpec{
					ReplicatedJobs: []jobsetv1alpha2.ReplicatedJob{
						{
							Name: "test-job",
							Template: batchv1.JobTemplateSpec{
								Spec: batchv1.JobSpec{
									Template: corev1.PodTemplateSpec{
										Spec: corev1.PodSpec{
											Containers: []corev1.Container{
												{
													Resources: corev1.ResourceRequirements{
														Limits: corev1.ResourceList{
															corev1.ResourceMemory: resource.MustParse("2048"),
														},
														Requests: corev1.ResourceList{
															corev1.ResourceCPU:    resource.MustParse("1000m"),
															corev1.ResourceMemory: resource.MustParse("2048"),
														},
													},
												},
											},
											NodeSelector: map[string]string{},
										},
									},
								},
							},
						},
					},
				},
			},
		},
		{
			name: "cpu_resources_with_limit",
			workUnit: proto.Modify(baseCPUWorkUnit, func(wu *epb.WorkUnit) {
				wu.Executable.GetJobset().Jobs[0].Spec.Resources.GetCpuResources().LimitCpuMillicores = 2000
			}),
			jobSet: &jobsetv1alpha2.JobSet{
				Spec: jobsetv1alpha2.JobSetSpec{
					ReplicatedJobs: []jobsetv1alpha2.ReplicatedJob{
						{
							Name: "test-job",
							Template: batchv1.JobTemplateSpec{
								Spec: batchv1.JobSpec{
									Template: corev1.PodTemplateSpec{
										Spec: corev1.PodSpec{
											Containers:   []corev1.Container{{}},
											NodeSelector: map[string]string{},
										},
									},
								},
							},
						},
					},
				},
			},
			wantJobSet: &jobsetv1alpha2.JobSet{
				Spec: jobsetv1alpha2.JobSetSpec{
					ReplicatedJobs: []jobsetv1alpha2.ReplicatedJob{
						{
							Name: "test-job",
							Template: batchv1.JobTemplateSpec{
								Spec: batchv1.JobSpec{
									Template: corev1.PodTemplateSpec{
										Spec: corev1.PodSpec{
											Containers: []corev1.Container{
												{
													Resources: corev1.ResourceRequirements{
														Limits: corev1.ResourceList{
															corev1.ResourceCPU:    resource.MustParse("2000m"),
															corev1.ResourceMemory: resource.MustParse("2048"),
														},
														Requests: corev1.ResourceList{
															corev1.ResourceCPU:    resource.MustParse("1000m"),
															corev1.ResourceMemory: resource.MustParse("2048"),
														},
													},
												},
											},
											NodeSelector: map[string]string{},
										},
									},
								},
							},
						},
					},
				},
			},
		},
		{
			name: "gpu_resources",
			workUnit: &epb.WorkUnit{
				Executable: &epb.Executable{
					Type: &epb.Executable_Jobset{
						Jobset: &epb.JobSetExecutable{
							Jobs: []*epb.KubernetesJob{
								{
									Name: "test-job",
									Spec: &epb.ExecutableSpec{
										Resources: &epb.ResourceSet{
											Resources: &epb.ResourceSet_GpuResources{
												GpuResources: &epb.GpuResources{
													Type:  "T4",
													Count: 2,
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
			jobSet: &jobsetv1alpha2.JobSet{
				Spec: jobsetv1alpha2.JobSetSpec{
					ReplicatedJobs: []jobsetv1alpha2.ReplicatedJob{
						{
							Name: "test-job",
							Template: batchv1.JobTemplateSpec{
								Spec: batchv1.JobSpec{
									Template: corev1.PodTemplateSpec{
										Spec: corev1.PodSpec{
											Containers:   []corev1.Container{{}},
											NodeSelector: map[string]string{},
										},
									},
								},
							},
						},
					},
				},
			},
			wantJobSet: &jobsetv1alpha2.JobSet{
				Spec: jobsetv1alpha2.JobSetSpec{
					ReplicatedJobs: []jobsetv1alpha2.ReplicatedJob{
						{
							Name:     "test-job",
							Replicas: 1,
							Template: batchv1.JobTemplateSpec{
								Spec: batchv1.JobSpec{
									Parallelism:    proto.Int32(1),
									Completions:    proto.Int32(1),
									CompletionMode: completionModePtr(batchv1.IndexedCompletion),
									Template: corev1.PodTemplateSpec{
										Spec: corev1.PodSpec{
											Containers: []corev1.Container{
												{
													Resources: corev1.ResourceRequirements{
														Limits: corev1.ResourceList{
															nvidiaGPUKey: resource.MustParse("2"),
														},
														// Two cards' worth of host resources, in the units the
														// converter builds them with. See TestApplyGPUResources.
														Requests: corev1.ResourceList{
															corev1.ResourceCPU:    resource.MustParse("2000m"),
															corev1.ResourceMemory: resource.MustParse("8589934592"),
														},
													},
												},
											},
											NodeSelector: map[string]string{
												nvidiaNodeSelectorKey: "nvidia-tesla-t4",
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
		{
			name: "tpu_resources_multi_host",
			workUnit: &epb.WorkUnit{
				Executable: &epb.Executable{
					Type: &epb.Executable_Jobset{
						Jobset: &epb.JobSetExecutable{
							Jobs: []*epb.KubernetesJob{
								{
									Name: "test-job",
									Spec: &epb.ExecutableSpec{
										Resources: &epb.ResourceSet{
											Resources: &epb.ResourceSet_TpuResources{
												TpuResources: &epb.TpuResources{
													Type:     "tpu-v4-podslice",
													Topology: "2x2x4",
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
			jobSet: &jobsetv1alpha2.JobSet{
				Spec: jobsetv1alpha2.JobSetSpec{
					ReplicatedJobs: []jobsetv1alpha2.ReplicatedJob{
						{
							Name: "test-job",
							Template: batchv1.JobTemplateSpec{
								Spec: batchv1.JobSpec{
									Template: corev1.PodTemplateSpec{
										Spec: corev1.PodSpec{
											Containers: []corev1.Container{{}},
										},
									},
								},
							},
						},
					},
				},
			},
			wantJobSet: &jobsetv1alpha2.JobSet{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						exclusiveTopologyAnnotation: exclusiveTopologyValue,
					},
				},
				Spec: jobsetv1alpha2.JobSetSpec{
					Network: &jobsetv1alpha2.Network{
						EnableDNSHostnames:       proto.Bool(true),
						PublishNotReadyAddresses: proto.Bool(true),
					},
					ReplicatedJobs: []jobsetv1alpha2.ReplicatedJob{
						{
							Name: "test-job",
							Template: batchv1.JobTemplateSpec{
								Spec: batchv1.JobSpec{
									Parallelism:    proto.Int32(4),
									Completions:    proto.Int32(4),
									CompletionMode: completionModePtr(batchv1.IndexedCompletion),
									Template: corev1.PodTemplateSpec{
										Spec: corev1.PodSpec{
											Containers: []corev1.Container{
												{
													Ports: []corev1.ContainerPort{
														{ContainerPort: tpuICIPort},
														{ContainerPort: tpuMetricsPort},
													},
													Resources: corev1.ResourceRequirements{
														Limits: corev1.ResourceList{
															tpuResourceKey: resource.MustParse("4"),
														},
														Requests: corev1.ResourceList{
															tpuResourceKey: resource.MustParse("4"),
														},
													},
												},
											},
											NodeSelector: map[string]string{
												tpuAcceleratorSelectorKey: "tpu-v4-podslice",
												tpuTopologySelectorKey:    "2x2x4",
											},
											Tolerations: []corev1.Toleration{
												{
													Key:      "google.com/tpu",
													Operator: corev1.TolerationOpEqual,
													Value:    "present",
													Effect:   corev1.TaintEffectNoSchedule,
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
		},
		{
			name: "tpu_resources_multi_host_2d",
			workUnit: &epb.WorkUnit{
				Executable: &epb.Executable{
					Type: &epb.Executable_Jobset{
						Jobset: &epb.JobSetExecutable{
							Jobs: []*epb.KubernetesJob{
								{
									Name: "test-job",
									Spec: &epb.ExecutableSpec{
										Resources: &epb.ResourceSet{
											Resources: &epb.ResourceSet_TpuResources{
												TpuResources: &epb.TpuResources{
													Type:     "tpu-v5-lite-podslice",
													Topology: "2x4",
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
			jobSet: &jobsetv1alpha2.JobSet{
				Spec: jobsetv1alpha2.JobSetSpec{
					ReplicatedJobs: []jobsetv1alpha2.ReplicatedJob{
						{
							Name: "test-job",
							Template: batchv1.JobTemplateSpec{
								Spec: batchv1.JobSpec{
									Template: corev1.PodTemplateSpec{
										Spec: corev1.PodSpec{
											Containers: []corev1.Container{{}},
										},
									},
								},
							},
						},
					},
				},
			},
			wantJobSet: &jobsetv1alpha2.JobSet{
				ObjectMeta: metav1.ObjectMeta{
					Annotations: map[string]string{
						exclusiveTopologyAnnotation: exclusiveTopologyValue,
					},
				},
				Spec: jobsetv1alpha2.JobSetSpec{
					Network: &jobsetv1alpha2.Network{
						EnableDNSHostnames:       proto.Bool(true),
						PublishNotReadyAddresses: proto.Bool(true),
					},
					ReplicatedJobs: []jobsetv1alpha2.ReplicatedJob{
						{
							Name: "test-job",
							Template: batchv1.JobTemplateSpec{
								Spec: batchv1.JobSpec{
									Parallelism:    proto.Int32(2),
									Completions:    proto.Int32(2),
									CompletionMode: completionModePtr(batchv1.IndexedCompletion),
									Template: corev1.PodTemplateSpec{
										Spec: corev1.PodSpec{
											Containers: []corev1.Container{
												{
													Ports: []corev1.ContainerPort{
														{ContainerPort: tpuICIPort},
														{ContainerPort: tpuMetricsPort},
													},
													Resources: corev1.ResourceRequirements{
														Limits: corev1.ResourceList{
															tpuResourceKey: resource.MustParse("4"),
														},
														Requests: corev1.ResourceList{
															tpuResourceKey: resource.MustParse("4"),
														},
													},
												},
											},
											NodeSelector: map[string]string{
												tpuAcceleratorSelectorKey: "tpu-v5-lite-podslice",
												tpuTopologySelectorKey:    "2x4",
											},
											Tolerations: []corev1.Toleration{
												{
													Key:      "google.com/tpu",
													Operator: corev1.TolerationOpEqual,
													Value:    "present",
													Effect:   corev1.TaintEffectNoSchedule,
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
		},
		{
			name: "tpu_resources_single_host",
			workUnit: &epb.WorkUnit{
				Executable: &epb.Executable{
					Type: &epb.Executable_Jobset{
						Jobset: &epb.JobSetExecutable{
							Jobs: []*epb.KubernetesJob{
								{
									Name: "test-job",
									Spec: &epb.ExecutableSpec{
										Resources: &epb.ResourceSet{
											Resources: &epb.ResourceSet_TpuResources{
												TpuResources: &epb.TpuResources{
													Type:     "tpu-v6e-slice",
													Topology: "2x2",
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
			jobSet: &jobsetv1alpha2.JobSet{
				Spec: jobsetv1alpha2.JobSetSpec{
					ReplicatedJobs: []jobsetv1alpha2.ReplicatedJob{
						{
							Name: "test-job",
							Template: batchv1.JobTemplateSpec{
								Spec: batchv1.JobSpec{
									Template: corev1.PodTemplateSpec{
										Spec: corev1.PodSpec{
											Containers: []corev1.Container{
												{
													Ports: []corev1.ContainerPort{
														{ContainerPort: 8080},
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
			},
			wantJobSet: &jobsetv1alpha2.JobSet{
				Spec: jobsetv1alpha2.JobSetSpec{
					ReplicatedJobs: []jobsetv1alpha2.ReplicatedJob{
						{
							Name: "test-job",
							Template: batchv1.JobTemplateSpec{
								Spec: batchv1.JobSpec{
									Parallelism:    proto.Int32(1),
									Completions:    proto.Int32(1),
									CompletionMode: completionModePtr(batchv1.IndexedCompletion),
									Template: corev1.PodTemplateSpec{
										Spec: corev1.PodSpec{
											Containers: []corev1.Container{
												{
													Ports: []corev1.ContainerPort{
														{ContainerPort: 8080},
														{ContainerPort: tpuICIPort},
														{ContainerPort: tpuMetricsPort},
													},
													Resources: corev1.ResourceRequirements{
														Limits: corev1.ResourceList{
															tpuResourceKey: resource.MustParse("4"),
														},
														Requests: corev1.ResourceList{
															tpuResourceKey: resource.MustParse("4"),
														},
													},
												},
											},
											NodeSelector: map[string]string{
												tpuAcceleratorSelectorKey: "tpu-v6e-slice",
												tpuTopologySelectorKey:    "2x2",
											},
											Tolerations: []corev1.Toleration{
												{
													Key:      "google.com/tpu",
													Operator: corev1.TolerationOpEqual,
													Value:    "present",
													Effect:   corev1.TaintEffectNoSchedule,
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
		},
		{
			name: "tpu_resources_with_custom_ports_dedup",
			workUnit: &epb.WorkUnit{
				Executable: &epb.Executable{
					Type: &epb.Executable_Jobset{
						Jobset: &epb.JobSetExecutable{
							Jobs: []*epb.KubernetesJob{
								{
									Name: "test-job",
									Spec: &epb.ExecutableSpec{
										Resources: &epb.ResourceSet{
											Resources: &epb.ResourceSet_TpuResources{
												TpuResources: &epb.TpuResources{
													Type:     "tpu-v6e-slice",
													Topology: "2x2",
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
			jobSet: &jobsetv1alpha2.JobSet{
				Spec: jobsetv1alpha2.JobSetSpec{
					ReplicatedJobs: []jobsetv1alpha2.ReplicatedJob{
						{
							Name: "test-job",
							Template: batchv1.JobTemplateSpec{
								Spec: batchv1.JobSpec{
									Template: corev1.PodTemplateSpec{
										Spec: corev1.PodSpec{
											Containers: []corev1.Container{
												{
													Ports: []corev1.ContainerPort{
														{Name: "worker", ContainerPort: 29005},
														{Name: "data", ContainerPort: 29006},
														{Name: "ici", ContainerPort: tpuICIPort},
														{Name: "metrics", ContainerPort: 8080},
													},
												},
											},
											NodeSelector: map[string]string{},
										},
									},
								},
							},
						},
					},
				},
			},
			wantJobSet: &jobsetv1alpha2.JobSet{
				Spec: jobsetv1alpha2.JobSetSpec{
					ReplicatedJobs: []jobsetv1alpha2.ReplicatedJob{
						{
							Name: "test-job",
							Template: batchv1.JobTemplateSpec{
								Spec: batchv1.JobSpec{
									Parallelism:    proto.Int32(1),
									Completions:    proto.Int32(1),
									CompletionMode: completionModePtr(batchv1.IndexedCompletion),
									Template: corev1.PodTemplateSpec{
										Spec: corev1.PodSpec{
											Containers: []corev1.Container{
												{
													Ports: []corev1.ContainerPort{
														{Name: "worker", ContainerPort: 29005},
														{Name: "data", ContainerPort: 29006},
														{Name: "ici", ContainerPort: tpuICIPort},
														{Name: "metrics", ContainerPort: 8080},
														{ContainerPort: tpuMetricsPort},
													},
													Resources: corev1.ResourceRequirements{
														Limits: corev1.ResourceList{
															tpuResourceKey: resource.MustParse("4"),
														},
														Requests: corev1.ResourceList{
															tpuResourceKey: resource.MustParse("4"),
														},
													},
												},
											},
											NodeSelector: map[string]string{
												tpuAcceleratorSelectorKey: "tpu-v6e-slice",
												tpuTopologySelectorKey:    "2x2",
											},
											Tolerations: []corev1.Toleration{
												{
													Key:      tpuResourceKey,
													Operator: corev1.TolerationOpEqual,
													Value:    tpuTolerationValue,
													Effect:   corev1.TaintEffectNoSchedule,
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
		},
		{
			name: "cpu_resources_with_custom_ports",
			workUnit: &epb.WorkUnit{
				Executable: &epb.Executable{
					Type: &epb.Executable_Jobset{
						Jobset: &epb.JobSetExecutable{
							Jobs: []*epb.KubernetesJob{
								{
									Name: "pathways-head",
									Spec: &epb.ExecutableSpec{
										Resources: &epb.ResourceSet{
											Resources: &epb.ResourceSet_CpuResources{
												CpuResources: &epb.CpuResources{
													CpuMillicores: 2000,
													MemoryBytes:   4096,
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
			jobSet: &jobsetv1alpha2.JobSet{
				Spec: jobsetv1alpha2.JobSetSpec{
					ReplicatedJobs: []jobsetv1alpha2.ReplicatedJob{
						{
							Name: "pathways-head",
							Template: batchv1.JobTemplateSpec{
								Spec: batchv1.JobSpec{
									Template: corev1.PodTemplateSpec{
										Spec: corev1.PodSpec{
											Containers: []corev1.Container{
												{
													Ports: []corev1.ContainerPort{
														{Name: "proxy", ContainerPort: 29000},
														{Name: "rm-1", ContainerPort: 29001},
														{Name: "rm-2", ContainerPort: 29002},
													},
												},
											},
											NodeSelector: map[string]string{},
										},
									},
								},
							},
						},
					},
				},
			},
			wantJobSet: &jobsetv1alpha2.JobSet{
				Spec: jobsetv1alpha2.JobSetSpec{
					ReplicatedJobs: []jobsetv1alpha2.ReplicatedJob{
						{
							Name: "pathways-head",
							Template: batchv1.JobTemplateSpec{
								Spec: batchv1.JobSpec{
									Template: corev1.PodTemplateSpec{
										Spec: corev1.PodSpec{
											Containers: []corev1.Container{
												{
													Ports: []corev1.ContainerPort{
														{Name: "proxy", ContainerPort: 29000},
														{Name: "rm-1", ContainerPort: 29001},
														{Name: "rm-2", ContainerPort: 29002},
													},
													Resources: corev1.ResourceRequirements{
														Limits: corev1.ResourceList{
															corev1.ResourceMemory: resource.MustParse("4096"),
														},
														Requests: corev1.ResourceList{
															corev1.ResourceCPU:    resource.MustParse("2000m"),
															corev1.ResourceMemory: resource.MustParse("4096"),
														},
													},
												},
											},
											NodeSelector: map[string]string{},
										},
									},
								},
							},
						},
					},
				},
			},
		},
		{
			name: "multiple_replicated_jobs",
			workUnit: &epb.WorkUnit{
				Executable: &epb.Executable{
					Type: &epb.Executable_Jobset{
						Jobset: &epb.JobSetExecutable{
							Jobs: []*epb.KubernetesJob{
								{
									Name: "test-job-cpu",
									Spec: &epb.ExecutableSpec{
										Resources: &epb.ResourceSet{
											Resources: &epb.ResourceSet_CpuResources{
												CpuResources: &epb.CpuResources{
													CpuMillicores: 1000,
													MemoryBytes:   2048,
												},
											},
										},
									},
								},
								{
									Name: "test-job-gpu",
									Spec: &epb.ExecutableSpec{
										Resources: &epb.ResourceSet{
											Resources: &epb.ResourceSet_GpuResources{
												GpuResources: &epb.GpuResources{
													Type:  "T4",
													Count: 2,
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
			jobSet: &jobsetv1alpha2.JobSet{
				Spec: jobsetv1alpha2.JobSetSpec{
					ReplicatedJobs: []jobsetv1alpha2.ReplicatedJob{
						{
							Name: "test-job-cpu",
							Template: batchv1.JobTemplateSpec{
								Spec: batchv1.JobSpec{
									Template: corev1.PodTemplateSpec{
										Spec: corev1.PodSpec{
											Containers:   []corev1.Container{{}},
											NodeSelector: map[string]string{},
										},
									},
								},
							},
						},
						{
							Name: "test-job-gpu",
							Template: batchv1.JobTemplateSpec{
								Spec: batchv1.JobSpec{
									Template: corev1.PodTemplateSpec{
										Spec: corev1.PodSpec{
											Containers:   []corev1.Container{{}},
											NodeSelector: map[string]string{},
										},
									},
								},
							},
						},
					},
				},
			},
			wantJobSet: &jobsetv1alpha2.JobSet{
				Spec: jobsetv1alpha2.JobSetSpec{
					ReplicatedJobs: []jobsetv1alpha2.ReplicatedJob{
						{
							Name: "test-job-cpu",
							Template: batchv1.JobTemplateSpec{
								Spec: batchv1.JobSpec{
									Template: corev1.PodTemplateSpec{
										Spec: corev1.PodSpec{
											Containers: []corev1.Container{
												{
													Resources: corev1.ResourceRequirements{
														Limits: corev1.ResourceList{
															corev1.ResourceMemory: resource.MustParse("2048"),
														},
														Requests: corev1.ResourceList{
															corev1.ResourceCPU:    resource.MustParse("1000m"),
															corev1.ResourceMemory: resource.MustParse("2048"),
														},
													},
												},
											},
											NodeSelector: map[string]string{},
										},
									},
								},
							},
						},
						{
							Name:     "test-job-gpu",
							Replicas: 1,
							Template: batchv1.JobTemplateSpec{
								Spec: batchv1.JobSpec{
									Parallelism:    proto.Int32(1),
									Completions:    proto.Int32(1),
									CompletionMode: completionModePtr(batchv1.IndexedCompletion),
									Template: corev1.PodTemplateSpec{
										Spec: corev1.PodSpec{
											Containers: []corev1.Container{
												{
													Resources: corev1.ResourceRequirements{
														Limits: corev1.ResourceList{
															nvidiaGPUKey: resource.MustParse("2"),
														},
														// Two cards' worth of host resources, in the units the
														// converter builds them with. See TestApplyGPUResources.
														Requests: corev1.ResourceList{
															corev1.ResourceCPU:    resource.MustParse("2000m"),
															corev1.ResourceMemory: resource.MustParse("8589934592"),
														},
													},
												},
											},
											NodeSelector: map[string]string{
												nvidiaNodeSelectorKey: "nvidia-tesla-t4",
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
		{
			name: "ephemeral_storage",
			workUnit: &epb.WorkUnit{
				Executable: &epb.Executable{
					Type: &epb.Executable_Jobset{
						Jobset: &epb.JobSetExecutable{
							Jobs: []*epb.KubernetesJob{
								{
									Name: "test-job",
									Spec: &epb.ExecutableSpec{
										Resources: &epb.ResourceSet{
											Resources: &epb.ResourceSet_CpuResources{
												CpuResources: &epb.CpuResources{
													CpuMillicores: 500,
													MemoryBytes:   1024,
												},
											},
											EphemeralStorageBytes: 10737418240,
										},
									},
								},
							},
						},
					},
				},
			},
			jobSet: &jobsetv1alpha2.JobSet{
				Spec: jobsetv1alpha2.JobSetSpec{
					ReplicatedJobs: []jobsetv1alpha2.ReplicatedJob{
						{
							Name: "test-job",
							Template: batchv1.JobTemplateSpec{
								Spec: batchv1.JobSpec{
									Template: corev1.PodTemplateSpec{
										Spec: corev1.PodSpec{
											Containers:   []corev1.Container{{}},
											NodeSelector: map[string]string{},
										},
									},
								},
							},
						},
					},
				},
			},
			wantJobSet: &jobsetv1alpha2.JobSet{
				Spec: jobsetv1alpha2.JobSetSpec{
					ReplicatedJobs: []jobsetv1alpha2.ReplicatedJob{
						{
							Name: "test-job",
							Template: batchv1.JobTemplateSpec{
								Spec: batchv1.JobSpec{
									Template: corev1.PodTemplateSpec{
										Spec: corev1.PodSpec{
											Containers: []corev1.Container{
												{
													Resources: corev1.ResourceRequirements{
														Limits: corev1.ResourceList{
															corev1.ResourceMemory:           resource.MustParse("1024"),
															corev1.ResourceEphemeralStorage: resource.MustParse("10737418240"),
														},
														Requests: corev1.ResourceList{
															corev1.ResourceCPU:              resource.MustParse("500m"),
															corev1.ResourceMemory:           resource.MustParse("1024"),
															corev1.ResourceEphemeralStorage: resource.MustParse("10737418240"),
														},
													},
												},
											},
											NodeSelector: map[string]string{},
										},
									},
								},
							},
						},
					},
				},
			},
		},
		{
			name: "per_job_node_selector_cpu_only",
			workUnit: &epb.WorkUnit{
				Executable: &epb.Executable{
					Type: &epb.Executable_Jobset{
						Jobset: &epb.JobSetExecutable{
							Jobs: []*epb.KubernetesJob{
								{
									Name: "head",
									Spec: &epb.ExecutableSpec{
										Resources: &epb.ResourceSet{
											Resources: &epb.ResourceSet_CpuResources{
												CpuResources: &epb.CpuResources{
													CpuMillicores: 1000,
													MemoryBytes:   2048,
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
			jobSet: &jobsetv1alpha2.JobSet{
				Spec: jobsetv1alpha2.JobSetSpec{
					ReplicatedJobs: []jobsetv1alpha2.ReplicatedJob{
						{
							Name: "head",
							Template: batchv1.JobTemplateSpec{
								Spec: batchv1.JobSpec{
									Template: corev1.PodTemplateSpec{
										Spec: corev1.PodSpec{
											Containers: []corev1.Container{{}},
											NodeSelector: map[string]string{
												"cloud.google.com/gke-nodepool": "cpups",
											},
										},
									},
								},
							},
						},
					},
				},
			},
			wantJobSet: &jobsetv1alpha2.JobSet{
				Spec: jobsetv1alpha2.JobSetSpec{
					ReplicatedJobs: []jobsetv1alpha2.ReplicatedJob{
						{
							Name: "head",
							Template: batchv1.JobTemplateSpec{
								Spec: batchv1.JobSpec{
									Template: corev1.PodTemplateSpec{
										Spec: corev1.PodSpec{
											Containers: []corev1.Container{
												{
													Resources: corev1.ResourceRequirements{
														Limits: corev1.ResourceList{
															corev1.ResourceMemory: resource.MustParse("2048"),
														},
														Requests: corev1.ResourceList{
															corev1.ResourceCPU:    resource.MustParse("1000m"),
															corev1.ResourceMemory: resource.MustParse("2048"),
														},
													},
												},
											},
											NodeSelector: map[string]string{
												"cloud.google.com/gke-nodepool": "cpups",
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
		{
			name: "per_job_node_selector_gpu_merge",
			workUnit: &epb.WorkUnit{
				Executable: &epb.Executable{
					Type: &epb.Executable_Jobset{
						Jobset: &epb.JobSetExecutable{
							Jobs: []*epb.KubernetesJob{
								{
									Name: "worker",
									Spec: &epb.ExecutableSpec{
										Resources: &epb.ResourceSet{
											Resources: &epb.ResourceSet_GpuResources{
												GpuResources: &epb.GpuResources{
													Type:  "T4",
													Count: 1,
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
			jobSet: &jobsetv1alpha2.JobSet{
				Spec: jobsetv1alpha2.JobSetSpec{
					ReplicatedJobs: []jobsetv1alpha2.ReplicatedJob{
						{
							Name: "worker",
							Template: batchv1.JobTemplateSpec{
								Spec: batchv1.JobSpec{
									Template: corev1.PodTemplateSpec{
										Spec: corev1.PodSpec{
											Containers: []corev1.Container{{}},
											NodeSelector: map[string]string{
												"cloud.google.com/gke-nodepool": "gpu-pool",
											},
										},
									},
								},
							},
						},
					},
				},
			},
			wantJobSet: &jobsetv1alpha2.JobSet{
				Spec: jobsetv1alpha2.JobSetSpec{
					ReplicatedJobs: []jobsetv1alpha2.ReplicatedJob{
						{
							Name:     "worker",
							Replicas: 1,
							Template: batchv1.JobTemplateSpec{
								Spec: batchv1.JobSpec{
									Parallelism:    proto.Int32(1),
									Completions:    proto.Int32(1),
									CompletionMode: completionModePtr(batchv1.IndexedCompletion),
									Template: corev1.PodTemplateSpec{
										Spec: corev1.PodSpec{
											Containers: []corev1.Container{
												{
													Resources: corev1.ResourceRequirements{
														Limits: corev1.ResourceList{
															nvidiaGPUKey: resource.MustParse("1"),
														},
														Requests: corev1.ResourceList{
															corev1.ResourceCPU:    resource.MustParse("1000m"),
															corev1.ResourceMemory: resource.MustParse("4294967296"),
														},
													},
												},
											},
											NodeSelector: map[string]string{
												"cloud.google.com/gke-nodepool": "gpu-pool",
												nvidiaNodeSelectorKey:           "nvidia-tesla-t4",
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
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			gotJobSet, err := gke.InjectOnJobSetCreation(t.Context(), tc.workUnit, tc.jobSet)
			if err != nil {
				t.Fatalf("InjectOnJobSetCreation(%v, %v) returned an unexpected error: %v", tc.workUnit, tc.jobSet, err)
			}

			if diff := cmp.Diff(tc.wantJobSet, gotJobSet); diff != "" {
				t.Errorf("InjectOnJobSetCreation(%v, %v) returned an unexpected diff (-want +got): %v", tc.workUnit, tc.jobSet, diff)
			}
		})
	}
}

func TestGKEInjectOnExperimentCreation(t *testing.T) {
	gke := &GKE{}
	exp := &epb.Experiment{}
	gotExp, err := gke.InjectOnExperimentCreation(t.Context(), nil, exp)
	if err != nil {
		t.Fatalf("InjectOnExperimentCreation(%v) returned an unexpected error: %v", exp, err)
	}
	if diff := cmp.Diff(experimentURLs, gotExp.GetUrls(), protocmp.Transform()); diff != "" {
		t.Errorf("InjectOnExperimentCreation(%v) returned an unexpected diff (-want +got): %v", exp, diff)
	}
}

func TestGKEInjectOnWorkUnitCreation(t *testing.T) {
	gke := &GKE{}
	wu := &epb.WorkUnit{}
	gotWu, err := gke.InjectOnWorkUnitCreation(t.Context(), nil, wu)
	if err != nil {
		t.Fatalf("InjectOnWorkUnitCreation(%v) returned an unexpected error: %v", wu, err)
	}
	if diff := cmp.Diff(workUnitURLs, gotWu.GetUrls(), protocmp.Transform()); diff != "" {
		t.Errorf("InjectOnWorkUnitCreation(%v) returned an unexpected diff (-want +got): %v", wu, diff)
	}
}

func TestIsRegistered(t *testing.T) {
	if _, ok := executionEnvironments[name]; !ok {
		t.Errorf("Execution environment %q is not registered", name)
	}
}

func TestTPUNodesPerSlice(t *testing.T) {
	tests := []struct {
		name     string
		topology string
		want     int
		wantErr  bool
	}{
		{name: "single_chip", topology: "1x1", want: 1},
		{name: "single_host_2x2", topology: "2x2", want: 1},
		{name: "multi_host_2x4", topology: "2x4", want: 2},
		{name: "multi_host_2x2x4", topology: "2x2x4", want: 4},
		{name: "multi_host_4x4x4", topology: "4x4x4", want: 16},
		{name: "multi_host_4x4", topology: "4x4", want: 4},
		{name: "invalid_topology", topology: "abc", wantErr: true},
		{name: "zero_dimension", topology: "2x0", wantErr: true},
		{name: "negative_dimension", topology: "2x-2", wantErr: true},
		{name: "leading_zero_dimension", topology: "0x4", wantErr: true},
		{name: "leading_negative_dimension", topology: "-4x4", wantErr: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := tpuNodesPerSlice(tc.topology)
			if (err != nil) != tc.wantErr {
				t.Fatalf("tpuNodesPerSlice(%q) error = %v, wantErr %v", tc.topology, err, tc.wantErr)
			}
			if got != tc.want {
				t.Errorf("tpuNodesPerSlice(%q) = %d, want %d", tc.topology, got, tc.want)
			}
		})
	}
}

func TestUpdateTolerations(t *testing.T) {
	t.Run("nil podSpec does not panic", func(t *testing.T) {
		updateTolerations(nil)
	})

	t.Run("appends TPU toleration", func(t *testing.T) {
		podSpec := &corev1.PodSpec{}
		updateTolerations(podSpec)

		want := []corev1.Toleration{
			{
				Key:      tpuResourceKey,
				Operator: corev1.TolerationOpEqual,
				Value:    tpuTolerationValue,
				Effect:   corev1.TaintEffectNoSchedule,
			},
		}
		if diff := cmp.Diff(want, podSpec.Tolerations); diff != "" {
			t.Errorf("updateTolerations() returned unexpected tolerations diff (-want +got):\n%s", diff)
		}
	})
}

func TestUpdateNodeSelectorGPU(t *testing.T) {
	tests := []struct {
		name    string
		gpuType string
		want    string
		wantErr bool
	}{
		{name: "T4", gpuType: "T4", want: "nvidia-tesla-t4"},
		{name: "B200", gpuType: "B200", want: "nvidia-b200"},
		// GCP's G4 machine types. XManager's client enum spells the RTX PRO 6000
		// as B40_48TH, so that is the name that arrives here.
		{name: "RTX PRO 6000", gpuType: "B40_48TH", want: "nvidia-rtx-pro-6000"},
		{name: "unknown type is rejected", gpuType: "NOT_A_GPU", wantErr: true},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			resourceSet := &epb.ResourceSet{
				Resources: &epb.ResourceSet_GpuResources{
					GpuResources: &epb.GpuResources{Type: test.gpuType, Count: 1},
				},
			}
			podSpec := &corev1.PodSpec{}
			err := updateNodeSelector(resourceSet, podSpec)

			if test.wantErr {
				if err == nil {
					t.Fatalf("updateNodeSelector(%q) succeeded, want an error", test.gpuType)
				}
				return
			}
			if err != nil {
				t.Fatalf("updateNodeSelector(%q) returned an unexpected error: %v", test.gpuType, err)
			}
			if got := podSpec.NodeSelector[nvidiaNodeSelectorKey]; got != test.want {
				t.Errorf("updateNodeSelector(%q) set %s=%q, want %q", test.gpuType, nvidiaNodeSelectorKey, got, test.want)
			}
		})
	}
}

func completionModePtr(m batchv1.CompletionMode) *batchv1.CompletionMode {
	return &m
}

func TestInjectOnJobSetCreation_WithSidecars(t *testing.T) {
	gke := &GKE{}
	ctx := context.Background()
	workUnit := &epb.WorkUnit{
		Executable: &epb.Executable{
			Type: &epb.Executable_Jobset{
				Jobset: &epb.JobSetExecutable{
					Jobs: []*epb.KubernetesJob{
						{
							Name: "test-job",
							Spec: &epb.ExecutableSpec{
								Resources: &epb.ResourceSet{
									Resources: &epb.ResourceSet_CpuResources{
										CpuResources: &epb.CpuResources{
											CpuMillicores: 1000,
											MemoryBytes:   1024,
										},
									},
								},
							},
							Sidecars: []*epb.ExecutableSpec{
								{
									Resources: &epb.ResourceSet{
										Resources: &epb.ResourceSet_CpuResources{
											CpuResources: &epb.CpuResources{
												CpuMillicores: 200,
												MemoryBytes:   256,
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
	}

	jobSet := &jobsetv1alpha2.JobSet{
		Spec: jobsetv1alpha2.JobSetSpec{
			ReplicatedJobs: []jobsetv1alpha2.ReplicatedJob{
				{
					Name: "test-job",
					Template: batchv1.JobTemplateSpec{
						Spec: batchv1.JobSpec{
							Template: corev1.PodTemplateSpec{
								Spec: corev1.PodSpec{
									Containers: []corev1.Container{
										{Name: "primary"},
									},
									InitContainers: []corev1.Container{
										{Name: "sidecar"},
									},
								},
							},
						},
					},
				},
			},
		},
	}

	got, err := gke.InjectOnJobSetCreation(ctx, workUnit, jobSet)
	if err != nil {
		t.Fatalf("InjectOnJobSetCreation() error = %v", err)
	}

	containers := got.Spec.ReplicatedJobs[0].Template.Spec.Template.Spec.Containers
	if len(containers) != 1 {
		t.Fatalf("Expected 1 primary container, got %d", len(containers))
	}
	if containers[0].Resources.Requests[corev1.ResourceCPU] != resource.MustParse("1000m") {
		t.Errorf("Primary container CPU request mismatch: got %v", containers[0].Resources.Requests[corev1.ResourceCPU])
	}
	initContainers := got.Spec.ReplicatedJobs[0].Template.Spec.Template.Spec.InitContainers
	if len(initContainers) != 1 {
		t.Fatalf("Expected 1 sidecar init container, got %d", len(initContainers))
	}
	if initContainers[0].Resources.Requests[corev1.ResourceCPU] != resource.MustParse("200m") {
		t.Errorf("Sidecar container CPU request mismatch: got %v", initContainers[0].Resources.Requests[corev1.ResourceCPU])
	}
}

func TestInjectOnJobSetCreation_WithInitContainers(t *testing.T) {
	g := &GKE{}
	workUnit := &epb.WorkUnit{
		Executable: &epb.Executable{
			Type: &epb.Executable_Jobset{
				Jobset: &epb.JobSetExecutable{
					Jobs: []*epb.KubernetesJob{
						{
							Name: "job-1",
							Spec: &epb.ExecutableSpec{
								Resources: &epb.ResourceSet{
									Resources: &epb.ResourceSet_CpuResources{
										CpuResources: &epb.CpuResources{
											CpuMillicores: 1000,
											MemoryBytes:   1024,
										},
									},
								},
							},
							InitContainers: []*epb.ExecutableSpec{
								{
									Resources: &epb.ResourceSet{
										Resources: &epb.ResourceSet_CpuResources{
											CpuResources: &epb.CpuResources{
												CpuMillicores: 300,
												MemoryBytes:   512,
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
	}

	jobSet := &jobsetv1alpha2.JobSet{
		Spec: jobsetv1alpha2.JobSetSpec{
			ReplicatedJobs: []jobsetv1alpha2.ReplicatedJob{
				{
					Name: "job-1",
					Template: batchv1.JobTemplateSpec{
						Spec: batchv1.JobSpec{
							Template: corev1.PodTemplateSpec{
								Spec: corev1.PodSpec{
									Containers:     []corev1.Container{{Name: "main"}},
									InitContainers: []corev1.Container{{Name: "init"}},
								},
							},
						},
					},
				},
			},
		},
	}

	got, err := g.InjectOnJobSetCreation(context.Background(), workUnit, jobSet)
	if err != nil {
		t.Fatalf("InjectOnJobSetCreation() returned error: %v", err)
	}

	initContainers := got.Spec.ReplicatedJobs[0].Template.Spec.Template.Spec.InitContainers
	if len(initContainers) != 1 {
		t.Fatalf("Expected 1 init container, got %d", len(initContainers))
	}
	if initContainers[0].Resources.Requests[corev1.ResourceCPU] != resource.MustParse("300m") {
		t.Errorf("Init container CPU request mismatch: got %v", initContainers[0].Resources.Requests[corev1.ResourceCPU])
	}
}

func TestInjectOnJobSetCreation_WithInitContainersAndSidecars(t *testing.T) {
	g := &GKE{}
	workUnit := &epb.WorkUnit{
		Executable: &epb.Executable{
			Type: &epb.Executable_Jobset{
				Jobset: &epb.JobSetExecutable{
					Jobs: []*epb.KubernetesJob{
						{
							Name: "job-1",
							Spec: &epb.ExecutableSpec{
								Resources: &epb.ResourceSet{
									Resources: &epb.ResourceSet_CpuResources{
										CpuResources: &epb.CpuResources{
											CpuMillicores: 1000,
											MemoryBytes:   1024,
										},
									},
								},
							},
							InitContainers: []*epb.ExecutableSpec{
								{
									Resources: &epb.ResourceSet{
										Resources: &epb.ResourceSet_CpuResources{
											CpuResources: &epb.CpuResources{
												CpuMillicores: 300,
												MemoryBytes:   512,
											},
										},
									},
								},
								{
									Resources: &epb.ResourceSet{
										Resources: &epb.ResourceSet_CpuResources{
											CpuResources: &epb.CpuResources{
												CpuMillicores: 400,
												MemoryBytes:   1024,
											},
										},
									},
								},
							},
							Sidecars: []*epb.ExecutableSpec{
								{
									Resources: &epb.ResourceSet{
										Resources: &epb.ResourceSet_CpuResources{
											CpuResources: &epb.CpuResources{
												CpuMillicores: 200,
												MemoryBytes:   256,
											},
										},
									},
								},
								{
									Resources: &epb.ResourceSet{
										Resources: &epb.ResourceSet_CpuResources{
											CpuResources: &epb.CpuResources{
												CpuMillicores: 500,
												MemoryBytes:   2048,
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
	}

	jobSet := &jobsetv1alpha2.JobSet{
		Spec: jobsetv1alpha2.JobSetSpec{
			ReplicatedJobs: []jobsetv1alpha2.ReplicatedJob{
				{
					Name: "job-1",
					Template: batchv1.JobTemplateSpec{
						Spec: batchv1.JobSpec{
							Template: corev1.PodTemplateSpec{
								Spec: corev1.PodSpec{
									Containers: []corev1.Container{{Name: "main"}},
									InitContainers: []corev1.Container{
										{Name: "init-1"},
										{Name: "init-2"},
										{Name: "sidecar-1"},
										{Name: "sidecar-2"},
									},
								},
							},
						},
					},
				},
			},
		},
	}

	got, err := g.InjectOnJobSetCreation(context.Background(), workUnit, jobSet)
	if err != nil {
		t.Fatalf("InjectOnJobSetCreation() returned error: %v", err)
	}

	containers := got.Spec.ReplicatedJobs[0].Template.Spec.Template.Spec.Containers
	if len(containers) != 1 {
		t.Fatalf("Expected 1 primary container, got %d", len(containers))
	}
	if containers[0].Resources.Requests[corev1.ResourceCPU] != resource.MustParse("1000m") {
		t.Errorf("Primary container CPU request mismatch: got %v", containers[0].Resources.Requests[corev1.ResourceCPU])
	}
	if containers[0].Resources.Requests[corev1.ResourceMemory] != resource.MustParse("1024") {
		t.Errorf("Primary container Memory request mismatch: got %v", containers[0].Resources.Requests[corev1.ResourceMemory])
	}

	initContainers := got.Spec.ReplicatedJobs[0].Template.Spec.Template.Spec.InitContainers
	if len(initContainers) != 4 {
		t.Fatalf("Expected 4 init containers (2 init + 2 sidecars), got %d", len(initContainers))
	}

	expected := []struct {
		name       string
		wantCPU    string
		wantMemory string
	}{
		{name: "init-1", wantCPU: "300m", wantMemory: "512"},
		{name: "init-2", wantCPU: "400m", wantMemory: "1024"},
		{name: "sidecar-1", wantCPU: "200m", wantMemory: "256"},
		{name: "sidecar-2", wantCPU: "500m", wantMemory: "2048"},
	}

	for i, tt := range expected {
		if initContainers[i].Name != tt.name {
			t.Errorf("Init container %d name mismatch: want %s, got %s", i, tt.name, initContainers[i].Name)
		}
		if initContainers[i].Resources.Requests[corev1.ResourceCPU] != resource.MustParse(tt.wantCPU) {
			t.Errorf("Init container %d (%s) CPU request mismatch: want %s, got %v", i, tt.name, tt.wantCPU, initContainers[i].Resources.Requests[corev1.ResourceCPU])
		}
		if initContainers[i].Resources.Requests[corev1.ResourceMemory] != resource.MustParse(tt.wantMemory) {
			t.Errorf("Init container %d (%s) Memory request mismatch: want %s, got %v", i, tt.name, tt.wantMemory, initContainers[i].Resources.Requests[corev1.ResourceMemory])
		}
	}
}

func TestAnnotateContainersMismatchError(t *testing.T) {
	replicatedJob := &jobsetv1alpha2.ReplicatedJob{
		Template: batchv1.JobTemplateSpec{
			Spec: batchv1.JobSpec{
				Template: corev1.PodTemplateSpec{
					Spec: corev1.PodSpec{
						Containers: []corev1.Container{{Name: "c1"}},
					},
				},
			},
		},
	}
	job := &epb.KubernetesJob{
		Spec: &epb.ExecutableSpec{Name: "c1"},
		Sidecars: []*epb.ExecutableSpec{
			{Name: "sidecar1"},
		},
	}
	if err := annotateContainers(replicatedJob, job); err == nil {
		t.Errorf("expected error for mismatched container count (1) and spec count (2), got nil")
	}
}

func TestAnnotateContainersInitContainerErrorNoPartialMutation(t *testing.T) {
	replicatedJob := &jobsetv1alpha2.ReplicatedJob{
		Template: batchv1.JobTemplateSpec{
			Spec: batchv1.JobSpec{
				Template: corev1.PodTemplateSpec{
					Spec: corev1.PodSpec{
						Containers:     []corev1.Container{{Name: "c1"}},
						InitContainers: []corev1.Container{{Name: "init"}},
					},
				},
			},
		},
	}
	job := &epb.KubernetesJob{
		Spec: &epb.ExecutableSpec{
			Name: "c1",
			Resources: &epb.ResourceSet{
				Resources: &epb.ResourceSet_CpuResources{
					CpuResources: &epb.CpuResources{
						CpuMillicores: 1000,
						MemoryBytes:   1024,
					},
				},
			},
		},
		InitContainers: []*epb.ExecutableSpec{
			{Name: "init-1"},
			{Name: "init-2"}, // Mismatched init container count causes error
		},
	}

	err := annotateContainers(replicatedJob, job)
	if err == nil {
		t.Fatalf("expected error due to init container count mismatch, got nil")
	}
	if len(replicatedJob.Template.Spec.Template.Spec.Containers[0].Resources.Requests) != 0 {
		t.Errorf("expected main container resources to remain unmutated after init container error, got %v", replicatedJob.Template.Spec.Template.Spec.Containers[0].Resources.Requests)
	}
}

func TestInjectOnJobSetCreation_NetworkInjectionTPUMultiHost(t *testing.T) {
	g := &GKE{}
	workUnit := &epb.WorkUnit{
		Executable: &epb.Executable{
			Type: &epb.Executable_Jobset{
				Jobset: &epb.JobSetExecutable{
					Jobs: []*epb.KubernetesJob{
						{
							Name: "tpu-job",
							Spec: &epb.ExecutableSpec{
								Resources: &epb.ResourceSet{
									Resources: &epb.ResourceSet_TpuResources{
										TpuResources: &epb.TpuResources{
											Type:     "tpu-v5-lite-podslice",
											Topology: "2x4",
										},
									},
								},
							},
						},
					},
				},
			},
		},
	}
	jobSet := &jobsetv1alpha2.JobSet{
		ObjectMeta: metav1.ObjectMeta{Name: "test-jobset"},
		Spec: jobsetv1alpha2.JobSetSpec{
			ReplicatedJobs: []jobsetv1alpha2.ReplicatedJob{
				{
					Name: "tpu-job",
					Template: batchv1.JobTemplateSpec{
						Spec: batchv1.JobSpec{
							Template: corev1.PodTemplateSpec{
								Spec: corev1.PodSpec{
									Containers: []corev1.Container{{Name: "main"}},
								},
							},
						},
					},
				},
			},
		},
	}

	got, err := g.InjectOnJobSetCreation(context.Background(), workUnit, jobSet)
	if err != nil {
		t.Fatalf("InjectOnJobSetCreation() returned error: %v", err)
	}

	if got.Spec.Network == nil {
		t.Fatalf("Expected Spec.Network to be injected for multi-host TPU slice, got nil")
	}
	if got.Spec.Network.EnableDNSHostnames == nil || !*got.Spec.Network.EnableDNSHostnames {
		t.Errorf("Expected EnableDNSHostnames to be true, got %v", got.Spec.Network.EnableDNSHostnames)
	}
	if got.Spec.Network.Subdomain != "test-jobset" {
		t.Errorf("Subdomain mismatch: got %q, want %q", got.Spec.Network.Subdomain, "test-jobset")
	}
}

func TestInjectOnJobSetCreation_NetworkInjectionGPUMultiNode(t *testing.T) {
	g := &GKE{}
	workUnit := &epb.WorkUnit{
		Executable: &epb.Executable{
			Type: &epb.Executable_Jobset{
				Jobset: &epb.JobSetExecutable{
					Jobs: []*epb.KubernetesJob{
						{
							Name: "gpu-job",
							Spec: &epb.ExecutableSpec{
								Resources: &epb.ResourceSet{
									Resources: &epb.ResourceSet_GpuResources{
										GpuResources: &epb.GpuResources{
											Type:  "T4",
											Count: 8,
										},
									},
								},
							},
						},
					},
				},
			},
		},
	}
	jobSet := &jobsetv1alpha2.JobSet{
		ObjectMeta: metav1.ObjectMeta{Name: "test-gpu-jobset"},
		Spec: jobsetv1alpha2.JobSetSpec{
			ReplicatedJobs: []jobsetv1alpha2.ReplicatedJob{
				{
					Name:     "gpu-job",
					Replicas: 2,
					Template: batchv1.JobTemplateSpec{
						Spec: batchv1.JobSpec{
							Template: corev1.PodTemplateSpec{
								Spec: corev1.PodSpec{
									Containers: []corev1.Container{{Name: "main"}},
								},
							},
						},
					},
				},
			},
		},
	}

	got, err := g.InjectOnJobSetCreation(context.Background(), workUnit, jobSet)
	if err != nil {
		t.Fatalf("InjectOnJobSetCreation() returned error: %v", err)
	}

	if got.Spec.Network == nil {
		t.Fatalf("Expected Spec.Network to be injected for multi-node GPU job, got nil")
	}
	if got.Spec.Network.EnableDNSHostnames == nil || !*got.Spec.Network.EnableDNSHostnames {
		t.Errorf("Expected EnableDNSHostnames to be true, got %v", got.Spec.Network.EnableDNSHostnames)
	}
}

func TestInjectOnJobSetCreation_GCSFuseAnnotation(t *testing.T) {
	g := &GKE{}
	workUnit := &epb.WorkUnit{
		Executable: &epb.Executable{
			Type: &epb.Executable_Jobset{
				Jobset: &epb.JobSetExecutable{
					Jobs: []*epb.KubernetesJob{
						{
							Name: "job1",
							Spec: &epb.ExecutableSpec{
								Resources: &epb.ResourceSet{
									Resources: &epb.ResourceSet_CpuResources{
										CpuResources: &epb.CpuResources{
											CpuMillicores: 1000,
											MemoryBytes:   1024 * 1024 * 1024,
										},
									},
								},
								DockerImage: "gcr.io/test/image",
							},
							Volumes: []*epb.Volume{
								{
									Name: "gcs-bucket",
									Source: &epb.Volume_Gcs{
										Gcs: &epb.GcsVolumeSource{
											Bucket: "my-gcs-bucket",
										},
									},
								},
							},
						},
					},
				},
			},
		},
	}
	jobSet := &jobsetv1alpha2.JobSet{
		Spec: jobsetv1alpha2.JobSetSpec{
			ReplicatedJobs: []jobsetv1alpha2.ReplicatedJob{
				{
					Name: "job1",
					Template: batchv1.JobTemplateSpec{
						Spec: batchv1.JobSpec{
							Template: corev1.PodTemplateSpec{
								Spec: corev1.PodSpec{
									Containers: []corev1.Container{
										{
											Name:  "container1",
											Image: "gcr.io/test/image",
										},
									},
								},
							},
						},
					},
				},
			},
		},
	}

	got, err := g.InjectOnJobSetCreation(context.Background(), workUnit, jobSet)
	if err != nil {
		t.Fatalf("InjectOnJobSetCreation() returned error: %v", err)
	}

	annotations := got.Spec.ReplicatedJobs[0].Template.Spec.Template.ObjectMeta.Annotations
	if annotations["gke-gcsfuse/volumes"] != "true" {
		t.Errorf("Expected annotation 'gke-gcsfuse/volumes: true', got %v", annotations)
	}
}

func TestEnsureTpuContainerPorts(t *testing.T) {
	tests := []struct {
		name     string
		existing []corev1.ContainerPort
		want     []corev1.ContainerPort
	}{
		{
			name:     "empty_existing",
			existing: nil,
			want: []corev1.ContainerPort{
				{ContainerPort: tpuICIPort},
				{ContainerPort: tpuMetricsPort},
			},
		},
		{
			name: "has_ici_only",
			existing: []corev1.ContainerPort{
				{ContainerPort: tpuICIPort},
			},
			want: []corev1.ContainerPort{
				{ContainerPort: tpuICIPort},
				{ContainerPort: tpuMetricsPort},
			},
		},
		{
			name: "has_both_already",
			existing: []corev1.ContainerPort{
				{ContainerPort: tpuICIPort},
				{ContainerPort: tpuMetricsPort},
			},
			want: []corev1.ContainerPort{
				{ContainerPort: tpuICIPort},
				{ContainerPort: tpuMetricsPort},
			},
		},
		{
			name: "has_other_port",
			existing: []corev1.ContainerPort{
				{ContainerPort: 80},
			},
			want: []corev1.ContainerPort{
				{ContainerPort: 80},
				{ContainerPort: tpuICIPort},
				{ContainerPort: tpuMetricsPort},
			},
		},
		{
			name: "has_metrics_only",
			existing: []corev1.ContainerPort{
				{ContainerPort: tpuMetricsPort},
			},
			want: []corev1.ContainerPort{
				{ContainerPort: tpuMetricsPort},
				{ContainerPort: tpuICIPort},
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := ensureTpuContainerPorts(tc.existing)
			if diff := cmp.Diff(tc.want, got); diff != "" {
				t.Errorf("ensureTpuContainerPorts() mismatch (-want +got):\n%s", diff)
			}
		})
	}

	existingArr := make([]corev1.ContainerPort, 1, 5)
	existingArr[0] = corev1.ContainerPort{Name: "http", ContainerPort: 80}
	_ = ensureTpuContainerPorts(existingArr)
	fullArr := existingArr[:cap(existingArr)]
	if len(existingArr) != 1 || existingArr[0].ContainerPort != 80 || (cap(existingArr) > 1 && fullArr[1].ContainerPort != 0) {
		t.Errorf("ensureTpuContainerPorts mutated existing slice underlying array: %+v", fullArr)
	}
}

func TestInjectOnJobSetCreation_GPUMultiNode_WithCoordinator(t *testing.T) {
	g := &GKE{}
	workUnit := &epb.WorkUnit{
		Executable: &epb.Executable{
			Type: &epb.Executable_Jobset{
				Jobset: &epb.JobSetExecutable{
					Jobs: []*epb.KubernetesJob{
						{
							Name: "gpu-job",
							Spec: &epb.ExecutableSpec{
								Resources: &epb.ResourceSet{
									Resources: &epb.ResourceSet_GpuResources{
										GpuResources: &epb.GpuResources{
											Type:  "H100",
											Count: 8,
										},
									},
								},
							},
						},
					},
				},
			},
		},
	}

	jobSet := &jobsetv1alpha2.JobSet{
		ObjectMeta: metav1.ObjectMeta{Name: "test-jobset"},
		Spec: jobsetv1alpha2.JobSetSpec{
			Coordinator: &jobsetv1alpha2.Coordinator{
				ReplicatedJob: "gpu-job",
				JobIndex:      2,
				PodIndex:      0,
			},
			ReplicatedJobs: []jobsetv1alpha2.ReplicatedJob{
				{
					Name:     "gpu-job",
					Replicas: 4,
					Template: batchv1.JobTemplateSpec{
						Spec: batchv1.JobSpec{
							Template: corev1.PodTemplateSpec{
								Spec: corev1.PodSpec{
									Containers: []corev1.Container{
										{Name: "main", Image: "test-image"},
									},
								},
							},
						},
					},
				},
			},
		},
	}

	got, err := g.InjectOnJobSetCreation(context.Background(), workUnit, jobSet)
	if err != nil {
		t.Fatalf("InjectOnJobSetCreation() returned error: %v", err)
	}

	if got.Spec.Coordinator == nil {
		t.Fatalf("Expected Coordinator to be preserved, got nil")
	}
	if got.Spec.Coordinator.JobIndex != 0 {
		t.Errorf("Expected Coordinator.JobIndex to be 0, got %d", got.Spec.Coordinator.JobIndex)
	}
	if got.Spec.Coordinator.PodIndex != 2 {
		t.Errorf("Expected Coordinator.PodIndex to be 2, got %d", got.Spec.Coordinator.PodIndex)
	}
}

func TestApplyCPUResources(t *testing.T) {
	tests := []struct {
		name         string
		cpuRes       *epb.CpuResources
		reqs         *corev1.ResourceRequirements
		wantErr      bool
		wantCPU      string
		wantLimitCPU string
	}{
		{
			name:    "nil inputs",
			cpuRes:  nil,
			reqs:    nil,
			wantErr: false,
		},
		{
			name: "valid request and limit",
			cpuRes: &epb.CpuResources{
				CpuMillicores:      1000,
				LimitCpuMillicores: 2000,
				MemoryBytes:        1024,
			},
			reqs:         &corev1.ResourceRequirements{},
			wantErr:      false,
			wantCPU:      "1",
			wantLimitCPU: "2",
		},
		{
			name: "limit less than request error",
			cpuRes: &epb.CpuResources{
				CpuMillicores:      2000,
				LimitCpuMillicores: 1000,
				MemoryBytes:        1024,
			},
			reqs:    &corev1.ResourceRequirements{},
			wantErr: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := applyCPUResources(tc.cpuRes, tc.reqs)
			if (err != nil) != tc.wantErr {
				t.Fatalf("applyCPUResources() error = %v, wantErr %v", err, tc.wantErr)
			}
			if tc.wantErr || tc.reqs == nil {
				return
			}
			checkQuantityMatch(t, tc.reqs.Requests, corev1.ResourceCPU, tc.wantCPU)
			checkQuantityMatch(t, tc.reqs.Limits, corev1.ResourceCPU, tc.wantLimitCPU)
		})
	}
}

func TestApplyGPUResources(t *testing.T) {
	tests := []struct {
		name            string
		gpuRes          *epb.GpuResources
		wantGPU         string
		wantCPU         string
		wantMemoryBytes int64
	}{
		{
			name:            "one card",
			gpuRes:          &epb.GpuResources{Type: "T4", Count: 1},
			wantGPU:         "1",
			wantCPU:         "1",
			wantMemoryBytes: 4 << 30,
		},
		{
			name:            "host resources scale with the card count",
			gpuRes:          &epb.GpuResources{Type: "T4", Count: 4},
			wantGPU:         "4",
			wantCPU:         "4",
			wantMemoryBytes: 16 << 30,
		},
		{
			name: "a count validation would have rejected still gets a floor",
			// Validation requires a count of at least one. A pod with no request
			// at all is BestEffort and evicted first, so an unvalidated caller
			// gets one card's worth rather than nothing.
			gpuRes:          &epb.GpuResources{Type: "T4"},
			wantGPU:         "0",
			wantCPU:         "1",
			wantMemoryBytes: 4 << 30,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			reqs := &corev1.ResourceRequirements{}
			applyGPUResources(tc.gpuRes, reqs)

			checkQuantityMatch(t, reqs.Limits, nvidiaGPUKey, tc.wantGPU)
			checkQuantityMatch(t, reqs.Requests, corev1.ResourceCPU, tc.wantCPU)

			mem, ok := reqs.Requests[corev1.ResourceMemory]
			if !ok {
				t.Fatalf("memory request missing, want %d bytes", tc.wantMemoryBytes)
			}
			if got := mem.Value(); got != tc.wantMemoryBytes {
				t.Errorf("memory request = %d bytes, want %d", got, tc.wantMemoryBytes)
			}

			// A limit on either would cap a job at a figure it never chose, and
			// the memory one would be enforced by the OOM killer.
			if _, ok := reqs.Limits[corev1.ResourceCPU]; ok {
				t.Errorf("CPU limit was set, want none")
			}
			if _, ok := reqs.Limits[corev1.ResourceMemory]; ok {
				t.Errorf("memory limit was set, want none")
			}
		})
	}
}

func checkQuantityMatch(t *testing.T, list corev1.ResourceList, key corev1.ResourceName, want string) {
	t.Helper()
	if want == "" {
		return
	}
	q, ok := list[key]
	if !ok {
		t.Errorf("%s missing, want %q", key, want)
		return
	}
	if got := q.String(); got != want {
		t.Errorf("%s = %q, want %q", key, got, want)
	}
}
