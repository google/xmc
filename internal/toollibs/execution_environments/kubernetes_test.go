// Copyright 2026 Google LLC
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
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/testing/protocmp"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
	jobsetv1alpha2 "sigs.k8s.io/jobset/api/jobset/v1alpha2"
	epb "xmanager_cloud/api/protos/experiment"
	"xmanager_cloud/internal/kubernetes/common/constants"
	"xmanager_cloud/internal/prototest"
)

func baseKubernetesGPUWorkUnit() *epb.WorkUnit {
	return &epb.WorkUnit{
		Executable: &epb.Executable{
			Type: &epb.Executable_Jobset{
				Jobset: &epb.JobSetExecutable{
					Jobs: []*epb.KubernetesJob{{
						Name:     "worker",
						Replicas: 2,
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
					}},
				},
			},
		},
	}
}

func baseKubernetesJobSet(name string, replicas int32) *jobsetv1alpha2.JobSet {
	return &jobsetv1alpha2.JobSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: "team-queue",
			Labels: map[string]string{
				constants.ExperimentIDLabel: "123",
				constants.WorkUnitIDLabel:   "456",
				constants.ExecutionIDLabel:  "1",
			},
		},
		Spec: jobsetv1alpha2.JobSetSpec{
			ReplicatedJobs: []jobsetv1alpha2.ReplicatedJob{{
				Name:     "worker",
				Replicas: replicas,
				Template: batchv1.JobTemplateSpec{
					Spec: batchv1.JobSpec{
						Template: corev1.PodTemplateSpec{
							Spec: corev1.PodSpec{
								SecurityContext: &corev1.PodSecurityContext{
									RunAsUser:          ptr.To(int64(1000)),
									RunAsGroup:         ptr.To(int64(1000)),
									SupplementalGroups: []int64{1000},
								},
								Containers: []corev1.Container{{Name: "worker"}},
							},
						},
					},
				},
			}},
		},
	}
}

func TestKubernetesIsRegistered(t *testing.T) {
	env, ok := Lookup("kubernetes")
	if !ok {
		t.Fatalf("kubernetes execution environment is not registered")
	}
	if env.Name() != "kubernetes" {
		t.Errorf("Name() = %q, want %q", env.Name(), "kubernetes")
	}
	if env.Description() == "" {
		t.Errorf("Description() must not be empty")
	}
}

func TestKubernetesCompatibilityAndCreationHooks(t *testing.T) {
	env, err := NewKubernetes(PlacementConfig{})
	if err != nil {
		t.Fatalf("NewKubernetes() unexpected error: %v", err)
	}

	if ok, reason := env.IsCompatibleWithWorkUnit(baseKubernetesGPUWorkUnit()); !ok {
		t.Errorf("IsCompatibleWithWorkUnit(valid) = false (%q), want true", reason)
	}
	invalidWU := prototest.Modify(baseKubernetesGPUWorkUnit(), func(w *epb.WorkUnit) {
		w.GetExecutable().GetJobset().GetJobs()[0].Spec.Resources = &epb.ResourceSet{
			Resources: &epb.ResourceSet_TpuResources{
				TpuResources: &epb.TpuResources{Type: "v5p", Topology: "2x0"},
			},
		}
	})
	if ok, _ := env.IsCompatibleWithWorkUnit(invalidWU); ok {
		t.Errorf("IsCompatibleWithWorkUnit(invalid topology) = true, want false")
	}

	exp := &epb.Experiment{Name: "experiments/1"}
	gotExp, err := env.InjectOnExperimentCreation(context.Background(), nil, exp)
	if err != nil {
		t.Fatalf("InjectOnExperimentCreation() error: %v", err)
	}
	if diff := cmp.Diff(exp, gotExp, protocmp.Transform()); diff != "" {
		t.Errorf("InjectOnExperimentCreation() modified experiment (-want +got):\n%s", diff)
	}

	wu := baseKubernetesGPUWorkUnit()
	gotWU, err := env.InjectOnWorkUnitCreation(context.Background(), nil, wu)
	if err != nil {
		t.Fatalf("InjectOnWorkUnitCreation() error: %v", err)
	}
	if diff := cmp.Diff(wu, gotWU, protocmp.Transform()); diff != "" {
		t.Errorf("InjectOnWorkUnitCreation() modified work unit (-want +got):\n%s", diff)
	}
}

func TestKubernetesInjectOnJobSetCreationGPUMultiNodeWithPlacement(t *testing.T) {
	cfg := PlacementConfig{
		GPU: AcceleratorPlacement{
			SchedulerName:      "gang-scheduler",
			SupplementalGroups: []int64{2000},
			ExclusivePlacement: true,
			NodeSelector: map[string]string{
				"accelerator.example.com/gpu-class": "{{ .AcceleratorType }}",
			},
			PodLabels: map[string]string{
				"accelerator.example.com/count": "{{ .Count }}",
			},
			PodAnnotations: map[string]string{
				"accelerator.example.com/job": "{{ .JobName }}",
			},
			JobSetLabels: map[string]string{
				"orchestration.example.com/group": "{{ truncHash 24 .JobSetName }}",
			},
			JobSetAnnotations: map[string]string{
				"orchestration.example.com/coords": "{{ .XID }}-{{ .WID }}-{{ .EID }}",
			},
		},
	}
	env, err := NewKubernetes(cfg)
	if err != nil {
		t.Fatalf("NewKubernetes() error: %v", err)
	}

	got, err := env.InjectOnJobSetCreation(context.Background(), baseKubernetesGPUWorkUnit(), baseKubernetesJobSet("xmc-operation-123-456", 2))
	if err != nil {
		t.Fatalf("InjectOnJobSetCreation() error: %v", err)
	}

	if _, hasGKEExcl := got.Annotations[exclusiveTopologyAnnotation]; hasGKEExcl {
		t.Errorf("unexpected GKE exclusive-topology annotation on kubernetes JobSet: %v", got.Annotations)
	}
	wantJobSetLabels := map[string]string{
		constants.ExperimentIDLabel:       "123",
		constants.WorkUnitIDLabel:         "456",
		constants.ExecutionIDLabel:        "1",
		"orchestration.example.com/group": "xmc-operation-123-456",
	}
	if diff := cmp.Diff(wantJobSetLabels, got.Labels); diff != "" {
		t.Errorf("JobSet.Labels diff (-want +got):\n%s", diff)
	}
	wantJobSetAnnotations := map[string]string{
		"orchestration.example.com/coords": "123-456-1",
	}
	if diff := cmp.Diff(wantJobSetAnnotations, got.Annotations); diff != "" {
		t.Errorf("JobSet.Annotations diff (-want +got):\n%s", diff)
	}

	rj := got.Spec.ReplicatedJobs[0]
	if rj.Replicas != 1 || rj.Template.Spec.Parallelism == nil || *rj.Template.Spec.Parallelism != 2 || *rj.Template.Spec.Completions != 2 {
		t.Errorf("Replicas/Parallelism/Completions = %d/%v/%v, want 1/2/2", rj.Replicas, rj.Template.Spec.Parallelism, rj.Template.Spec.Completions)
	}
	if rj.Template.Spec.CompletionMode == nil || *rj.Template.Spec.CompletionMode != batchv1.IndexedCompletion {
		t.Errorf("CompletionMode = %v, want Indexed", rj.Template.Spec.CompletionMode)
	}

	pod := rj.Template.Spec.Template
	for k := range pod.Spec.NodeSelector {
		if strings.HasPrefix(k, "cloud.google.com/") {
			t.Errorf("unexpected GKE nodeSelector %q in %v", k, pod.Spec.NodeSelector)
		}
	}
	if diff := cmp.Diff(map[string]string{"accelerator.example.com/gpu-class": "H100"}, pod.Spec.NodeSelector); diff != "" {
		t.Errorf("Pod.Spec.NodeSelector diff (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff(map[string]string{"accelerator.example.com/count": "8"}, pod.Labels); diff != "" {
		t.Errorf("Pod.Labels diff (-want +got):\n%s", diff)
	}
	if pod.Spec.SchedulerName != "gang-scheduler" {
		t.Errorf("SchedulerName = %q, want %q", pod.Spec.SchedulerName, "gang-scheduler")
	}
	if diff := cmp.Diff([]int64{1000, 2000}, pod.Spec.SecurityContext.SupplementalGroups); diff != "" {
		t.Errorf("SupplementalGroups diff (-want +got):\n%s", diff)
	}
	wantTolerations := []corev1.Toleration{{
		Key:      nvidiaGPUKey,
		Operator: corev1.TolerationOpExists,
		Effect:   corev1.TaintEffectNoSchedule,
	}}
	if diff := cmp.Diff(wantTolerations, pod.Spec.Tolerations); diff != "" {
		t.Errorf("Tolerations diff (-want +got):\n%s", diff)
	}
	wantGPUQty := resource.MustParse("8")
	if gotQty := pod.Spec.Containers[0].Resources.Limits[corev1.ResourceName(nvidiaGPUKey)]; !gotQty.Equal(wantGPUQty) {
		t.Errorf("GPU limit = %s, want %s", gotQty.String(), wantGPUQty.String())
	}
	wantNetwork := &jobsetv1alpha2.Network{
		EnableDNSHostnames:       proto.Bool(true),
		Subdomain:                "xmc-operation-123-456",
		PublishNotReadyAddresses: proto.Bool(true),
	}
	if diff := cmp.Diff(wantNetwork, got.Spec.Network); diff != "" {
		t.Errorf("JobSet.Spec.Network diff (-want +got):\n%s", diff)
	}
}

func TestKubernetesPreservesExistingUserMetadataAndScheduler(t *testing.T) {
	cfg := PlacementConfig{
		GPU: AcceleratorPlacement{
			SchedulerName:      "gang-scheduler",
			SupplementalGroups: []int64{1000, 2000},
			PodLabels:          map[string]string{"accelerator.example.com/model": "{{ .AcceleratorType }}"},
			JobSetLabels:       map[string]string{"orchestration.example.com/group": "{{ .JobSetName }}"},
		},
	}
	env, err := NewKubernetes(cfg)
	if err != nil {
		t.Fatalf("NewKubernetes() error: %v", err)
	}

	wu := prototest.Modify(baseKubernetesGPUWorkUnit(), func(w *epb.WorkUnit) {
		w.GetExecutable().GetJobset().GetJobs()[0].Replicas = 1
	})
	js := baseKubernetesJobSet("xmc-operation-123-456", 1)
	js.Labels["orchestration.example.com/group"] = "custom-user-group"
	js.Spec.ReplicatedJobs[0].Template.Spec.Template.Spec.SchedulerName = "explicit-scheduler"
	js.Spec.ReplicatedJobs[0].Template.Spec.Template.Spec.SecurityContext = nil
	js.Spec.ReplicatedJobs[0].Template.Spec.Template.Spec.Tolerations = []corev1.Toleration{{
		Key:      nvidiaGPUKey,
		Operator: corev1.TolerationOpExists,
		Effect:   corev1.TaintEffectNoSchedule,
	}}

	got, err := env.InjectOnJobSetCreation(context.Background(), wu, js)
	if err != nil {
		t.Fatalf("InjectOnJobSetCreation() error: %v", err)
	}

	if got.Labels["orchestration.example.com/group"] != "custom-user-group" {
		t.Errorf("JobSet label = %q, want user-supplied %q", got.Labels["orchestration.example.com/group"], "custom-user-group")
	}
	rj := got.Spec.ReplicatedJobs[0]
	if *rj.Template.Spec.Parallelism != 1 {
		t.Errorf("single-replica Parallelism = %d, want 1", *rj.Template.Spec.Parallelism)
	}
	if got.Spec.Network != nil {
		t.Errorf("single-node JobSet should not force Network, got %+v", got.Spec.Network)
	}
	podSpec := rj.Template.Spec.Template.Spec
	if podSpec.SchedulerName != "explicit-scheduler" {
		t.Errorf("SchedulerName = %q, want %q", podSpec.SchedulerName, "explicit-scheduler")
	}
	if len(podSpec.Tolerations) != 1 {
		t.Errorf("expected existing Exists toleration to be deduplicated, got %v", podSpec.Tolerations)
	}
	if diff := cmp.Diff([]int64{1000, 2000}, podSpec.SecurityContext.SupplementalGroups); diff != "" {
		t.Errorf("SupplementalGroups diff (-want +got):\n%s", diff)
	}
}

func TestKubernetesCPUAndTPUResourceClasses(t *testing.T) {
	cfg := PlacementConfig{
		GPU: AcceleratorPlacement{
			SchedulerName: "gpu-only-scheduler",
			JobSetLabels:  map[string]string{"orchestration.example.com/gpu-group": "{{ .JobSetName }}"},
		},
		TPU: AcceleratorPlacement{
			PodLabels: map[string]string{
				"accelerator.example.com/model":    "{{ .AcceleratorType }}",
				"accelerator.example.com/topology": "{{ .Topology }}",
			},
			SupplementalGroups: []int64{3000},
		},
	}
	env, err := NewKubernetes(cfg)
	if err != nil {
		t.Fatalf("NewKubernetes() error: %v", err)
	}

	t.Run("cpu_only_receives_no_accelerator_placement", func(t *testing.T) {
		wu := prototest.Modify(baseKubernetesGPUWorkUnit(), func(w *epb.WorkUnit) {
			job := w.GetExecutable().GetJobset().GetJobs()[0]
			job.Replicas = 1
			job.Spec.Resources = &epb.ResourceSet{
				Resources: &epb.ResourceSet_CpuResources{
					CpuResources: &epb.CpuResources{CpuMillicores: 1000, MemoryBytes: 2048},
				},
			}
		})
		got, err := env.InjectOnJobSetCreation(context.Background(), wu, baseKubernetesJobSet("xmc-operation-123-456", 1))
		if err != nil {
			t.Fatalf("InjectOnJobSetCreation() error: %v", err)
		}
		if _, ok := got.Labels["orchestration.example.com/gpu-group"]; ok {
			t.Errorf("CPU JobSet must not receive accelerator jobSetLabels, got %v", got.Labels)
		}
		podSpec := got.Spec.ReplicatedJobs[0].Template.Spec.Template.Spec
		if podSpec.SchedulerName != "" {
			t.Errorf("CPU pod SchedulerName = %q, want empty", podSpec.SchedulerName)
		}
		if diff := cmp.Diff([]int64{1000}, podSpec.SecurityContext.SupplementalGroups); diff != "" {
			t.Errorf("CPU SupplementalGroups diff (-want +got):\n%s", diff)
		}
	})

	t.Run("tpu_multi_host_applies_tpu_profile_and_topology_parallelism", func(t *testing.T) {
		wu := prototest.Modify(baseKubernetesGPUWorkUnit(), func(w *epb.WorkUnit) {
			job := w.GetExecutable().GetJobset().GetJobs()[0]
			job.Replicas = 1
			job.Spec.Resources = &epb.ResourceSet{
				Resources: &epb.ResourceSet_TpuResources{
					TpuResources: &epb.TpuResources{Type: "v5p", Topology: "2x4"},
				},
			}
		})
		got, err := env.InjectOnJobSetCreation(context.Background(), wu, baseKubernetesJobSet("xmc-operation-123-456", 1))
		if err != nil {
			t.Fatalf("InjectOnJobSetCreation() error: %v", err)
		}
		rj := got.Spec.ReplicatedJobs[0]
		if *rj.Template.Spec.Parallelism != 2 || *rj.Template.Spec.Completions != 2 {
			t.Errorf("Parallelism/Completions = %d/%d, want 2/2", *rj.Template.Spec.Parallelism, *rj.Template.Spec.Completions)
		}
		pod := rj.Template.Spec.Template
		wantLabels := map[string]string{
			"accelerator.example.com/model":    "v5p",
			"accelerator.example.com/topology": "2x4",
		}
		if diff := cmp.Diff(wantLabels, pod.Labels); diff != "" {
			t.Errorf("Pod.Labels diff (-want +got):\n%s", diff)
		}
		if diff := cmp.Diff([]int64{1000, 3000}, pod.Spec.SecurityContext.SupplementalGroups); diff != "" {
			t.Errorf("SupplementalGroups diff (-want +got):\n%s", diff)
		}
	})
}

func TestKubernetesInjectOnJobSetCreationErrors(t *testing.T) {
	env := &Kubernetes{}
	tests := []struct {
		name    string
		wu      *epb.WorkUnit
		js      *jobsetv1alpha2.JobSet
		wantErr string
	}{
		{
			name:    "missing_jobset_executable",
			wu:      &epb.WorkUnit{},
			js:      baseKubernetesJobSet("xmc-operation-1-1", 1),
			wantErr: "does not contain a jobset",
		},
		{
			name:    "job_missing_from_k8s_jobset",
			wu:      baseKubernetesGPUWorkUnit(),
			js:      &jobsetv1alpha2.JobSet{},
			wantErr: "not found in jobset",
		},
		{
			name: "missing_resources",
			wu: prototest.Modify(baseKubernetesGPUWorkUnit(), func(w *epb.WorkUnit) {
				w.GetExecutable().GetJobset().GetJobs()[0].Spec.Resources = nil
			}),
			js:      baseKubernetesJobSet("xmc-operation-1-1", 1),
			wantErr: "no resources found",
		},
		{
			name: "missing_containers",
			wu:   baseKubernetesGPUWorkUnit(),
			js: func() *jobsetv1alpha2.JobSet {
				js := baseKubernetesJobSet("xmc-operation-1-1", 1)
				js.Spec.ReplicatedJobs[0].Template.Spec.Template.Spec.Containers = nil
				return js
			}(),
			wantErr: "no containers found",
		},
		{
			name: "invalid_topology",
			wu: prototest.Modify(baseKubernetesGPUWorkUnit(), func(w *epb.WorkUnit) {
				w.GetExecutable().GetJobset().GetJobs()[0].Spec.Resources = &epb.ResourceSet{
					Resources: &epb.ResourceSet_TpuResources{
						TpuResources: &epb.TpuResources{Type: "v5p", Topology: "bad"},
					},
				}
			}),
			js:      baseKubernetesJobSet("xmc-operation-1-1", 1),
			wantErr: "invalid TPU topology",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := env.InjectOnJobSetCreation(context.Background(), tc.wu, tc.js)
			if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
				t.Fatalf("InjectOnJobSetCreation() error = %v, want substring %q", err, tc.wantErr)
			}
		})
	}
}

func TestKubernetesZeroPlacementAndRuntimeRenderErrors(t *testing.T) {
	zeroEnv := &Kubernetes{}
	if _, err := zeroEnv.InjectOnJobSetCreation(context.Background(), baseKubernetesGPUWorkUnit(), baseKubernetesJobSet("xmc-operation-1-1", 2)); err != nil {
		t.Fatalf("zero-config GPU InjectOnJobSetCreation() error: %v", err)
	}
	tpuWU := prototest.Modify(baseKubernetesGPUWorkUnit(), func(w *epb.WorkUnit) {
		job := w.GetExecutable().GetJobset().GetJobs()[0]
		job.Replicas = 1
		job.Spec.Resources = &epb.ResourceSet{
			Resources: &epb.ResourceSet_TpuResources{
				TpuResources: &epb.TpuResources{Type: "v5p", Topology: "2x2"},
			},
		}
	})
	if _, err := zeroEnv.InjectOnJobSetCreation(context.Background(), tpuWU, baseKubernetesJobSet("xmc-operation-1-1", 1)); err != nil {
		t.Fatalf("zero-config TPU InjectOnJobSetCreation() error: %v", err)
	}

	invalidRuntimeTypeWU := prototest.Modify(baseKubernetesGPUWorkUnit(), func(w *epb.WorkUnit) {
		w.GetExecutable().GetJobset().GetJobs()[0].Spec.Resources.GetGpuResources().Type = "invalid/type/value"
	})
	for _, tc := range []struct {
		name string
		cfg  PlacementConfig
	}{
		{
			name: "nodeSelector_render_error",
			cfg:  PlacementConfig{GPU: AcceleratorPlacement{NodeSelector: map[string]string{"example.com/k": "{{ .AcceleratorType }}"}}},
		},
		{
			name: "podLabels_render_error",
			cfg:  PlacementConfig{GPU: AcceleratorPlacement{PodLabels: map[string]string{"example.com/k": "{{ .AcceleratorType }}"}}},
		},
		{
			name: "jobSetLabels_render_error",
			cfg:  PlacementConfig{GPU: AcceleratorPlacement{JobSetLabels: map[string]string{"example.com/k": "{{ .AcceleratorType }}"}}},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			env, err := NewKubernetes(tc.cfg)
			if err != nil {
				t.Fatalf("NewKubernetes() error: %v", err)
			}
			if _, err := env.InjectOnJobSetCreation(context.Background(), invalidRuntimeTypeWU, baseKubernetesJobSet("xmc-operation-1-1", 2)); err == nil {
				t.Fatalf("InjectOnJobSetCreation() succeeded for invalid runtime label value, want error")
			}
		})
	}
}
