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
	"strconv"
	"testing"

	"github.com/google/go-cmp/cmp"
	"google.golang.org/protobuf/proto"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
	jobsetv1alpha2 "sigs.k8s.io/jobset/api/jobset/v1alpha2"
	esspb "xmanager_cloud/experiment_state_server/proto/messages_go_proto"
	"xmanager_cloud/internal/prototest"
	constants "xmanager_cloud/kubernetes/common/constants"
)

func TestConstructJobSet(t *testing.T) {
	ctx, client, _ := setupTestClient(true /* createNewNamespaces */, &alwaysPassingPreconditionsValidator{})
	workUnit := prototest.Modify(baseCPUWorkUnit, func(wu *esspb.WorkUnit) {
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
							BackoffLimit:            proto.Int32(1),
							CompletionMode:          ptr.To(batchv1.IndexedCompletion),
							Completions:             proto.Int32(1),
							PodFailurePolicy:        podFailurePolicy("job1"),
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
											Env:                      client.generateEnvVarsForSpec(workUnit.Executable.GetJobset().Jobs[0].GetSpec()),
											Resources: corev1.ResourceRequirements{
												Limits: corev1.ResourceList{
													corev1.ResourceMemory: resource.MustParse("1024"),
												},
												Requests: corev1.ResourceList{
													corev1.ResourceCPU:    resource.MustParse("100m"),
													corev1.ResourceMemory: resource.MustParse("1024"),
												},
											},
											VolumeMounts:    generateVolumeMountsForSpec(workUnit.Executable.GetJobset().Jobs[0].GetSpec()),
											SecurityContext: defaultSecurityContext(),
										},
									},
									RestartPolicy:   corev1.RestartPolicyNever,
									SecurityContext: defaultPodSecurityContext(),
									Volumes:         generateVolumesForJob(workUnit.Executable.GetJobset().Jobs[0]),
								},
							},
						},
					},
				},
			},
			Network: &jobsetv1alpha2.Network{
				EnableDNSHostnames:       ptr.To(true),
				Subdomain:                "xmc-operation-123-456",
				PublishNotReadyAddresses: ptr.To(true),
			},
			Suspend: ptr.To(false),
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
		workUnit := prototest.Modify(baseCPUWorkUnit, func(wu *esspb.WorkUnit) {
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
	workUnit := prototest.Modify(baseCPUWorkUnit, func(wu *esspb.WorkUnit) {
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
	podLabels := rj.Template.Spec.Template.ObjectMeta.Labels

	expectedLabels := map[string]string{
		"kueue.x-k8s.io/podset":     "pathways-head",
		"custom.label/key":          "value",
		constants.ExperimentIDLabel: "123",
		constants.WorkUnitIDLabel:   "456",
		constants.ExecutionIDLabel:  "1",
		"kueue.x-k8s.io/queue-name": testNamespace,
	}
	for labelKey, wantValue := range expectedLabels {
		if got := rj.Template.ObjectMeta.Labels[labelKey]; got != wantValue {
			t.Errorf("JobTemplateSpec label %s = %q, want %q", labelKey, got, wantValue)
		}
		if got := podLabels[labelKey]; got != wantValue {
			t.Errorf("PodTemplateSpec label %s = %q, want %q", labelKey, got, wantValue)
		}
	}
}

func TestConstructJobSet_PerJobNodeSelector(t *testing.T) {
	ctx, client, _ := setupTestClient(true /* createNewNamespaces */, &alwaysPassingPreconditionsValidator{})
	workUnit := prototest.Modify(baseCPUWorkUnit, func(wu *esspb.WorkUnit) {
		head := wu.Executable.GetJobset().Jobs[0]
		head.Name = "head"
		head.NodeSelector = map[string]string{"cloud.google.com/gke-nodepool": "cpups"}

		worker := proto.Clone(head).(*esspb.KubernetesJob)
		worker.Name = "worker"
		worker.Replicas = 2
		worker.Spec.Resources = &esspb.ResourceSet{
			Resources: &esspb.ResourceSet_GpuResources{
				GpuResources: &esspb.GpuResources{Type: "T4", Count: 1},
			},
		}
		worker.NodeSelector = map[string]string{"cloud.google.com/gke-nodepool": "gpu-pool"}
		wu.Executable.GetJobset().Jobs = []*esspb.KubernetesJob{head, worker}
	})

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

func TestConstructJobSet_TTLSecondsAfterFinished(t *testing.T) {
	ctx, client, _ := setupTestClient(true /* createNewNamespaces */, &alwaysPassingPreconditionsValidator{})
	got, err := client.constructJobSet(ctx, baseCPUWorkUnit, testTarget, "test-author", constants.XmcLabelValue, testTime, Priorities{})
	if err != nil {
		t.Fatalf("constructJobSet() returned unexpected error: %v", err)
	}

	if len(got.Spec.ReplicatedJobs) != 1 {
		t.Fatalf("Expected 1 replicated job, got %d", len(got.Spec.ReplicatedJobs))
	}

	if got.Spec.TTLSecondsAfterFinished == nil || *got.Spec.TTLSecondsAfterFinished != 604800 {
		t.Errorf("Expected JobSet TTLSecondsAfterFinished to be 604800, got %v", got.Spec.TTLSecondsAfterFinished)
	}

	jobSpec := got.Spec.ReplicatedJobs[0].Template.Spec
	if jobSpec.TTLSecondsAfterFinished == nil || *jobSpec.TTLSecondsAfterFinished != 604800 {
		t.Errorf("Expected ReplicatedJob TTLSecondsAfterFinished to be 604800, got %v", jobSpec.TTLSecondsAfterFinished)
	}
}

func TestConstructJobSet_LeaderWorker_Pathways(t *testing.T) {
	ctx, client, _ := setupTestClient(true /* createNewNamespaces */, &alwaysPassingPreconditionsValidator{})
	workUnit := prototest.Modify(baseCPUWorkUnit, func(wu *esspb.WorkUnit) {
		head := wu.Executable.GetJobset().Jobs[0]
		head.Name = constants.PathwaysHeadJobName
		head.Spec.Name = "head-container"
		head.Spec.DockerImage = "head-image"
		head.RestartPolicy = proto.String("Never")

		worker := proto.Clone(head).(*esspb.KubernetesJob)
		worker.Name = "worker"
		worker.Spec.Name = "worker-container"
		worker.Spec.DockerImage = "worker-image"
		worker.RestartPolicy = proto.String("OnFailure")
		worker.RetryLimit = 100
		wu.Executable.GetJobset().Jobs = []*esspb.KubernetesJob{head, worker}
	})

	got, err := client.constructJobSet(ctx, workUnit, testTarget, "test-author", constants.XmcLabelValue, testTime, Priorities{})
	if err != nil {
		t.Fatalf("constructJobSet() returned unexpected error: %v", err)
	}

	if len(got.Spec.ReplicatedJobs) != 2 {
		t.Fatalf("Expected 2 replicated jobs, got %d", len(got.Spec.ReplicatedJobs))
	}

	head := got.Spec.ReplicatedJobs[0]
	if head.Name != constants.PathwaysHeadJobName {
		t.Errorf("Head job name: got %q, want %q", head.Name, constants.PathwaysHeadJobName)
	}
	if head.Template.Spec.Template.Spec.RestartPolicy != corev1.RestartPolicyNever {
		t.Errorf("Head RestartPolicy: got %v, want %v", head.Template.Spec.Template.Spec.RestartPolicy, corev1.RestartPolicyNever)
	}
	if head.Template.Spec.PodFailurePolicy == nil {
		t.Error("Head PodFailurePolicy: expected non-nil, got nil")
	}

	worker := got.Spec.ReplicatedJobs[1]
	if worker.Name != "worker" {
		t.Errorf("Worker job name: got %q, want %q", worker.Name, "worker")
	}
	if worker.Template.Spec.Template.Spec.RestartPolicy != corev1.RestartPolicyOnFailure {
		t.Errorf("Worker RestartPolicy: got %v, want %v", worker.Template.Spec.Template.Spec.RestartPolicy, corev1.RestartPolicyOnFailure)
	}
	if worker.Template.Spec.PodFailurePolicy != nil {
		t.Errorf("Worker PodFailurePolicy: expected nil, got %+v", worker.Template.Spec.PodFailurePolicy)
	}
	if worker.Template.Spec.BackoffLimit == nil || *worker.Template.Spec.BackoffLimit != 100 {
		t.Errorf("Worker BackoffLimit: got %v, want 100", worker.Template.Spec.BackoffLimit)
	}
}

func TestConstructJobSet_ActiveDeadlineSeconds(t *testing.T) {
	ctx, client, _ := setupTestClient(true /* createNewNamespaces */, &alwaysPassingPreconditionsValidator{})
	workUnit := prototest.Modify(baseCPUWorkUnit, func(wu *esspb.WorkUnit) {
		wu.Executable.GetJobset().Jobs[0].ActiveDeadlineSeconds = 3600
	})
	got, err := client.constructJobSet(ctx, workUnit, testTarget, "test-author", constants.XmcLabelValue, testTime, Priorities{})
	if err != nil {
		t.Fatalf("constructJobSet() returned unexpected error: %v", err)
	}
	if diff := cmp.Diff(ptr.To(int64(3600)), got.Spec.ReplicatedJobs[0].Template.Spec.ActiveDeadlineSeconds); diff != "" {
		t.Errorf("ActiveDeadlineSeconds mismatch (-want +got):\n%s", diff)
	}
}

func TestConstructJobSet_TerminationGracePeriodSeconds(t *testing.T) {
	tests := []struct {
		name string
		val  *int64
	}{
		{name: "positive grace period", val: proto.Int64(120)},
		{name: "zero grace period", val: proto.Int64(0)},
		{name: "nil grace period", val: nil},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, client, _ := setupTestClient(true /* createNewNamespaces */, &alwaysPassingPreconditionsValidator{})
			workUnit := prototest.Modify(baseCPUWorkUnit, func(wu *esspb.WorkUnit) {
				wu.Executable.GetJobset().Jobs[0].TerminationGracePeriodSeconds = tt.val
			})
			got, err := client.constructJobSet(ctx, workUnit, testTarget, "test-author", constants.XmcLabelValue, testTime, Priorities{})
			if err != nil {
				t.Fatalf("constructJobSet() returned unexpected error: %v", err)
			}
			gotPodSpec := got.Spec.ReplicatedJobs[0].Template.Spec.Template.Spec
			if diff := cmp.Diff(tt.val, gotPodSpec.TerminationGracePeriodSeconds); diff != "" {
				t.Errorf("TerminationGracePeriodSeconds mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestConstructJobSet_NoJobset(t *testing.T) {
	ctx, client, _ := setupTestClient(true /* createNewNamespaces */, &alwaysPassingPreconditionsValidator{})
	workUnit := &esspb.WorkUnit{Name: "workUnit1", Executable: &esspb.Executable{}}

	if _, err := client.constructJobSet(ctx, workUnit, testTarget, "test-author", constants.XmcLabelValue, testTime, Priorities{}); err == nil {
		t.Errorf("constructJobSet() with no jobset succeeded, want error")
	}
}

func TestConstructJobSet_NoExecutable(t *testing.T) {
	ctx, client, _ := setupTestClient(true /* createNewNamespaces */, &alwaysPassingPreconditionsValidator{})
	workUnit := &esspb.WorkUnit{Name: "workUnit1", Executable: nil}

	if _, err := client.constructJobSet(ctx, workUnit, testTarget, "test-author", constants.XmcLabelValue, testTime, Priorities{}); err == nil {
		t.Errorf("constructJobSet() with no executable succeeded, want error")
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

			got, err := client.constructJobSet(ctx, baseCPUWorkUnit, testTarget, "test-author", constants.XmcLabelValue, testTime, Priorities{})
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
	workUnit := prototest.Modify(baseCPUWorkUnit, func(wu *esspb.WorkUnit) {
		jobset := wu.Executable.GetJobset()
		jobset.Annotations = map[string]string{
			"custom.company.com/release": "v1.2.3",
			"helm.sh/hook":               "pre-install",
			constants.AuthorAnnotation:   "malicious-author-override",
		}
		jobset.Labels = map[string]string{
			"custom.company.com/team":   "ml-platform",
			"env":                       "production",
			constants.ExperimentIDLabel: "99999",
			kueueQueueLabel:             "malicious-queue-override",
		}
	})

	got, err := client.constructJobSet(ctx, workUnit, testTarget, "test-author", testNamespace, testTime, Priorities{})
	if err != nil {
		t.Fatalf("constructJobSet() returned unexpected error: %v", err)
	}

	for k, want := range map[string]string{
		"custom.company.com/release": "v1.2.3",
		"helm.sh/hook":               "pre-install",
		constants.AuthorAnnotation:   "test-author",
	} {
		if got.Annotations[k] != want {
			t.Errorf("Annotations[%q] = %q, want %q", k, got.Annotations[k], want)
		}
	}
	for k, want := range map[string]string{
		"custom.company.com/team":   "ml-platform",
		"env":                       "production",
		constants.ExperimentIDLabel: "123",
		kueueQueueLabel:             testNamespace,
	} {
		if got.Labels[k] != want {
			t.Errorf("Labels[%q] = %q, want %q", k, got.Labels[k], want)
		}
	}

	if len(got.Spec.ReplicatedJobs) != 1 {
		t.Fatalf("Expected 1 replicated job, got %d", len(got.Spec.ReplicatedJobs))
	}
	jobAnnotations := got.Spec.ReplicatedJobs[0].Template.ObjectMeta.Annotations
	for _, k := range []string{"custom.company.com/release", "helm.sh/hook"} {
		if _, ok := jobAnnotations[k]; ok {
			t.Errorf("ReplicatedJob unexpectedly received top-level annotation %q", k)
		}
	}
	jobLabels := got.Spec.ReplicatedJobs[0].Template.ObjectMeta.Labels
	for _, k := range []string{"custom.company.com/team", "env"} {
		if _, ok := jobLabels[k]; ok {
			t.Errorf("ReplicatedJob unexpectedly received top-level label %q", k)
		}
	}
}

func TestConstructJobSet_KubernetesJobAnnotationsPropagation(t *testing.T) {
	ctx, client, _ := setupTestClient(true /* createNewNamespaces */, &alwaysPassingPreconditionsValidator{})
	wantAnnotations := map[string]string{
		"alpha.jobset.sigs.k8s.io/exclusive-topology": "kubernetes.io/hostname",
		"custom.job.annotation/key":                   "custom-value",
	}
	workUnit := prototest.Modify(baseCPUWorkUnit, func(wu *esspb.WorkUnit) {
		wu.Executable.GetJobset().Jobs[0].Annotations = wantAnnotations
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
	for k, want := range wantAnnotations {
		if gotVal := jobTemplateAnnotations[k]; gotVal != want {
			t.Errorf("JobTemplateSpec.Annotations[%q] = %q, want %q", k, gotVal, want)
		}
		if gotVal := podTemplateAnnotations[k]; gotVal != want {
			t.Errorf("PodTemplateSpec.Annotations[%q] = %q, want %q", k, gotVal, want)
		}
	}

	jobTemplateAnnotations["test-mutation-key"] = "mutated"
	if _, ok := podTemplateAnnotations["test-mutation-key"]; ok {
		t.Errorf("PodTemplateSpec.Annotations shared map pointer with JobTemplateSpec.Annotations")
	}
}
