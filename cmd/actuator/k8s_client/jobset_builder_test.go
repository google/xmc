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

	"github.com/google/go-cmp/cmp"
	"google.golang.org/protobuf/proto"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/utils/ptr"
	jobsetv1alpha2 "sigs.k8s.io/jobset/api/jobset/v1alpha2"
	esspb "xmanager_cloud/experiment_state_server/proto/messages_go_proto"
	constants "xmanager_cloud/internal/kubernetes/common/constants"
	"xmanager_cloud/internal/prototest"
)

func envFieldRef(name, fieldPath string) corev1.EnvVar {
	return corev1.EnvVar{
		Name:      name,
		ValueFrom: &corev1.EnvVarSource{FieldRef: &corev1.ObjectFieldSelector{FieldPath: fieldPath}},
	}
}

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
							BackoffLimit:   proto.Int32(1),
							CompletionMode: ptr.To(batchv1.IndexedCompletion),
							Completions:    proto.Int32(1),
							PodFailurePolicy: &batchv1.PodFailurePolicy{
								Rules: []batchv1.PodFailurePolicyRule{
									{
										Action:          batchv1.PodFailurePolicyActionIgnore,
										OnPodConditions: []batchv1.PodFailurePolicyOnPodConditionsPattern{{Type: "DisruptionTarget"}},
									},
									{
										Action:      batchv1.PodFailurePolicyActionFailJob,
										OnExitCodes: &batchv1.PodFailurePolicyOnExitCodesRequirement{ContainerName: proto.String("job1"), Operator: "In", Values: []int32{50}},
									},
									{
										Action:      batchv1.PodFailurePolicyActionIgnore,
										OnExitCodes: &batchv1.PodFailurePolicyOnExitCodesRequirement{ContainerName: proto.String("job1"), Operator: "In", Values: []int32{42}},
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
												envFieldRef(constants.XmcEnvVarNamespace, "metadata.namespace"),
												envFieldRef(constants.XmcEnvVarQueueName, fmt.Sprintf("metadata.labels['%s']", kueueQueueLabel)),
												envFieldRef(constants.XmcEnvVarExperimentID, fmt.Sprintf("metadata.labels['%s']", constants.ExperimentIDLabel)),
												envFieldRef(constants.XmcEnvVarWorkUnitID, fmt.Sprintf("metadata.labels['%s']", constants.WorkUnitIDLabel)),
												envFieldRef(constants.XmcEnvVarExecutionID, fmt.Sprintf("metadata.labels['%s']", constants.ExecutionIDLabel)),
												envFieldRef(constants.XmcEnvVarAuthor, fmt.Sprintf("metadata.annotations['%s']", constants.AuthorAnnotation)),
												envFieldRef(constants.XmcEnvVarJobIndex, fmt.Sprintf("metadata.labels['%s']", constants.JobSetJobIndexLabel)),
												envFieldRef(constants.XmcEnvVarJobName, fmt.Sprintf("metadata.labels['%s']", constants.JobSetReplicatedJobNameLabel)),
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
												{Name: "ram-scratch", MountPath: "/dev/ram-scratch"},
												{Name: "disk-scratch", MountPath: "/dev/disk-scratch"},
												{Name: "dev-shm-scratch", MountPath: "/dev/shm"},
											},
											SecurityContext: &corev1.SecurityContext{
												AllowPrivilegeEscalation: proto.Bool(false),
												Capabilities:             &corev1.Capabilities{Drop: []corev1.Capability{"ALL"}},
												Privileged:               proto.Bool(false),
											},
										},
									},
									RestartPolicy: corev1.RestartPolicyNever,
									SecurityContext: &corev1.PodSecurityContext{
										RunAsGroup:     proto.Int64(1000),
										RunAsUser:      proto.Int64(1000),
										SeccompProfile: &corev1.SeccompProfile{Type: corev1.SeccompProfileTypeRuntimeDefault},
									},
									Volumes: []corev1.Volume{
										{Name: "ram-scratch", VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{Medium: corev1.StorageMediumMemory}}},
										{Name: "disk-scratch", VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{Medium: corev1.StorageMediumDefault}}},
										{Name: "dev-shm-scratch", VolumeSource: corev1.VolumeSource{EmptyDir: &corev1.EmptyDirVolumeSource{Medium: corev1.StorageMediumMemory}}},
									},
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
