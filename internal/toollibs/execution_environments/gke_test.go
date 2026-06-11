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
	"testing"

	"golang/cmp/cmp"
	batchv1 "golang/k8s_io/api/v/v0_23/batch/v1/v1"
	corev1 "golang/k8s_io/api/v/v0_23/core/v1/v1"
	"golang/k8s_io/apimachinery/v/v0_23/pkg/api/resource/resource"
	"golang/protobuf/v2/testing/protocmp/protocmp"
	epb "xmanager_cloud/experiment_state_server/proto/messages_go_proto"
	jobsetv1alpha2 "xmanager_cloud/kubernetes/api/v1alpha2/v1alpha2"
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
	if ok, reason := gke.IsCompatibleWithWorkUnit(nil); !ok {
		t.Errorf("IsCompatibleWithWorkUnit(nil) = false (reason: %s), want true", reason)
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
							Name: "test-job",
							Template: batchv1.JobTemplateSpec{
								Spec: batchv1.JobSpec{
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
							Name: "test-job-gpu",
							Template: batchv1.JobTemplateSpec{
								Spec: batchv1.JobSpec{
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
