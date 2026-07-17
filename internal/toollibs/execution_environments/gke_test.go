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
	tests := []struct {
		name       string
		workUnit   *epb.WorkUnit
		jobSet     *jobsetv1alpha2.JobSet
		wantJobSet *jobsetv1alpha2.JobSet
	}{
		{
			name: "cpu_resources",
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
														Requests: corev1.ResourceList{},
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
														Requests: corev1.ResourceList{},
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
	if len(containers) != 2 {
		t.Fatalf("Expected 2 containers, got %d", len(containers))
	}
	if containers[0].Resources.Requests[corev1.ResourceCPU] != resource.MustParse("1000m") {
		t.Errorf("Primary container CPU request mismatch: got %v", containers[0].Resources.Requests[corev1.ResourceCPU])
	}
	if containers[1].Resources.Requests[corev1.ResourceCPU] != resource.MustParse("200m") {
		t.Errorf("Sidecar container CPU request mismatch: got %v", containers[1].Resources.Requests[corev1.ResourceCPU])
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
