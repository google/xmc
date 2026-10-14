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

package k8sclient

import (
	"fmt"
	"testing"

	"github.com/google/go-cmp/cmp"
	"google.golang.org/protobuf/proto"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/utils/ptr"
	jobsetv1alpha2 "sigs.k8s.io/jobset/api/jobset/v1alpha2"
	esspb "xmanager_cloud/api/protos/experiment"
	constants "xmanager_cloud/internal/kubernetes/common/constants"
	"xmanager_cloud/internal/prototest"
)

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

func TestConstructJobSet_CustomEnvVars(t *testing.T) {
	ctx, client, _ := setupTestClient(true /* createNewNamespaces */, &alwaysPassingPreconditionsValidator{})
	workUnit := prototest.Modify(baseCPUWorkUnit, func(wu *esspb.WorkUnit) {
		job := wu.Executable.GetJobset().Jobs[0]
		job.Spec.EnvVars = map[string]string{"env_vars_key": "env_vars_val"}
		job.Spec.Env = []*esspb.EnvVar{
			{Name: "literal_env", Source: &esspb.EnvVar_Value{Value: "literal_val"}},
			nil, // nil entries should be safely ignored.
			{Name: "", Source: &esspb.EnvVar_Value{Value: "empty_name_val"}},
			{
				Name: "field_ref_env",
				Source: &esspb.EnvVar_ValueFrom{
					ValueFrom: &esspb.EnvVarSource{
						Reference: &esspb.EnvVarSource_FieldRef{
							FieldRef: &esspb.FieldRef{FieldPath: "status.podIP", ApiVersion: "v1"},
						},
					},
				},
			},
			{Name: "nil_value_from_env", Source: &esspb.EnvVar_ValueFrom{ValueFrom: nil}},
			{Name: "nil_field_ref_env", Source: &esspb.EnvVar_ValueFrom{ValueFrom: &esspb.EnvVarSource{Reference: nil}}},
			{
				Name: "nil_inner_field_ref_env",
				Source: &esspb.EnvVar_ValueFrom{
					ValueFrom: &esspb.EnvVarSource{Reference: &esspb.EnvVarSource_FieldRef{FieldRef: nil}},
				},
			},
			{
				Name: "secret_ref_env_opt",
				Source: &esspb.EnvVar_ValueFrom{
					ValueFrom: &esspb.EnvVarSource{
						Reference: &esspb.EnvVarSource_SecretKeyRef{
							SecretKeyRef: &esspb.SecretKeyRef{Name: "my-secret", Key: "my-key", Optional: ptr.To(true)},
						},
					},
				},
			},
			{
				Name: "secret_ref_env_req",
				Source: &esspb.EnvVar_ValueFrom{
					ValueFrom: &esspb.EnvVarSource{
						Reference: &esspb.EnvVarSource_SecretKeyRef{
							SecretKeyRef: &esspb.SecretKeyRef{Name: "other-secret", Key: "other-key", Optional: nil},
						},
					},
				},
			},
			{
				Name: "nil_inner_secret_key_ref_env",
				Source: &esspb.EnvVar_ValueFrom{
					ValueFrom: &esspb.EnvVarSource{Reference: &esspb.EnvVarSource_SecretKeyRef{SecretKeyRef: nil}},
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
				FieldRef: &corev1.ObjectFieldSelector{FieldPath: "status.podIP", APIVersion: "v1"},
			},
		},
		{
			Name: "secret_ref_env_opt",
			ValueFrom: &corev1.EnvVarSource{
				SecretKeyRef: &corev1.SecretKeySelector{
					LocalObjectReference: corev1.LocalObjectReference{Name: "my-secret"},
					Key:                  "my-key",
					Optional:             ptr.To(true),
				},
			},
		},
		{
			Name: "secret_ref_env_req",
			ValueFrom: &corev1.EnvVarSource{
				SecretKeyRef: &corev1.SecretKeySelector{
					LocalObjectReference: corev1.LocalObjectReference{Name: "other-secret"},
					Key:                  "other-key",
					Optional:             nil,
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
			env.Name == constants.XmcEnvVarJobIndex ||
			env.Name == constants.XmcEnvVarJobName {
			continue
		}
		gotEnv = append(gotEnv, env)
	}

	if diff := cmp.Diff(wantEnv, gotEnv); diff != "" {
		t.Errorf("container.Env returned diff (-want +got):\n%s", diff)
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
	workUnit := prototest.Modify(baseCPUWorkUnit, func(wu *esspb.WorkUnit) {
		spec := wu.Executable.GetJobset().Jobs[0].Spec
		spec.Args = map[string]string{"z_flag": "z_val", "a_flag": "a_val"}
		spec.ArgsList = []string{"positional_arg", "bare_key=val", "--gnu_flag=val"}
	})

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

func TestConstructJobSet_PrivilegedSecurityContext(t *testing.T) {
	tests := []struct {
		name                string
		allowPrivileged     bool
		specPrivileged      *bool
		wantPrivileged      bool
		wantAllowEscalation bool
	}{
		{
			name:                "Flag enabled and spec privileged true",
			allowPrivileged:     true,
			specPrivileged:      proto.Bool(true),
			wantPrivileged:      true,
			wantAllowEscalation: true,
		},
		{
			name:                "Flag disabled and spec privileged true",
			allowPrivileged:     false,
			specPrivileged:      proto.Bool(true),
			wantPrivileged:      false,
			wantAllowEscalation: false,
		},
		{
			name:                "Flag enabled and spec privileged false",
			allowPrivileged:     true,
			specPrivileged:      proto.Bool(false),
			wantPrivileged:      false,
			wantAllowEscalation: false,
		},
		{
			name:                "Flag enabled and spec privileged nil",
			allowPrivileged:     true,
			specPrivileged:      nil,
			wantPrivileged:      false,
			wantAllowEscalation: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, client, _ := setupTestClient(true /* createNewNamespaces */, &alwaysPassingPreconditionsValidator{})
			client.RuntimeConfig.AllowPrivilegedContainers = tt.allowPrivileged

			workUnit := proto.Clone(baseCPUWorkUnit).(*esspb.WorkUnit)
			workUnit.GetExecutable().GetJobset().GetJobs()[0].GetSpec().Privileged = tt.specPrivileged

			got, err := client.constructJobSet(ctx, workUnit, testTarget, "test-author", constants.XmcLabelValue, testTime, Priorities{})
			if err != nil {
				t.Fatalf("constructJobSet() returned unexpected error: %v", err)
			}

			podSpec := got.Spec.ReplicatedJobs[0].Template.Spec.Template.Spec
			containerSC := podSpec.Containers[0].SecurityContext
			if containerSC == nil {
				t.Fatal("Expected Container SecurityContext to be set, but it was nil")
			}
			if *containerSC.Privileged != tt.wantPrivileged {
				t.Errorf("Privileged = %v, want %v", *containerSC.Privileged, tt.wantPrivileged)
			}
			if *containerSC.AllowPrivilegeEscalation != tt.wantAllowEscalation {
				t.Errorf("AllowPrivilegeEscalation = %v, want %v", *containerSC.AllowPrivilegeEscalation, tt.wantAllowEscalation)
			}
		})
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

func TestInjectManagedXprofGCSEnvVars_AllContainers(t *testing.T) {
	const (
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
		object        string
		wantInjected  bool
	}{
		{
			name:          "injects into every container",
			numContainers: 3,
			object:        wantObject,
			wantInjected:  true,
		},
		{
			name:          "single container still injected",
			numContainers: 1,
			object:        wantObject,
			wantInjected:  true,
		},
		{
			name:          "empty object is a no-op",
			numContainers: 2,
			wantInjected:  false,
		},
		{
			name:          "no containers is a safe no-op",
			numContainers: 0,
			object:        wantObject,
			wantInjected:  false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rj := newReplicatedJob(tc.numContainers)
			injectManagedXprofGCSEnvVars(rj, tc.object)

			for _, containers := range [][]corev1.Container{
				rj.Template.Spec.Template.Spec.Containers,
				rj.Template.Spec.Template.Spec.InitContainers,
			} {
				for i := range containers {
					envMap := make(map[string]string)
					for _, ev := range containers[i].Env {
						envMap[ev.Name] = ev.Value
					}
					gotObject, hasObject := envMap[constants.XmcEnvVarManagedXprofGCSObjectFolder]
					if tc.wantInjected {
						if gotObject != tc.object {
							t.Errorf("container[%d] %s = %q, want %q", i, constants.XmcEnvVarManagedXprofGCSObjectFolder, gotObject, tc.object)
						}
					} else if hasObject {
						t.Errorf("container[%d] unexpectedly got managed-xprof env vars: %v", i, envMap)
					}
				}
			}
		})
	}
}

func TestConstructJobSet_NativeSidecarInitContainers(t *testing.T) {
	ctx, client, _ := setupTestClient(true /* createNewNamespaces */, &alwaysPassingPreconditionsValidator{})

	workUnit := prototest.Modify(baseCPUWorkUnit, func(wu *esspb.WorkUnit) {
		job := wu.Executable.GetJobset().Jobs[0]
		job.Spec.RestartPolicy = proto.String("Always")
		job.InitContainers = []*esspb.ExecutableSpec{
			{
				DockerImage: "gcr.io/foo/regular-init:latest",
			},
			{
				DockerImage:   "gcr.io/foo/native-sidecar-init:latest",
				RestartPolicy: proto.String("Always"),
			},
		}
		job.Sidecars = []*esspb.ExecutableSpec{
			{
				DockerImage: "gcr.io/foo/legacy-sidecar:latest",
			},
		}
	})

	got, err := client.constructJobSet(ctx, workUnit, testTarget, "test-author", testNamespace, testTime, Priorities{})
	if err != nil {
		t.Fatalf("constructJobSet() returned unexpected error: %v", err)
	}

	if len(got.Spec.ReplicatedJobs) != 1 {
		t.Fatalf("Expected 1 replicated job, got %d", len(got.Spec.ReplicatedJobs))
	}

	initContainers := got.Spec.ReplicatedJobs[0].Template.Spec.Template.Spec.InitContainers
	if len(initContainers) != 3 {
		t.Fatalf("Expected 3 init containers, got %d", len(initContainers))
	}

	// 1. Regular init container (RestartPolicy should be nil)
	if initContainers[0].RestartPolicy != nil {
		t.Errorf("initContainers[0].RestartPolicy = %v, want nil", *initContainers[0].RestartPolicy)
	}

	// 2. Native sidecar init container with RestartPolicy: Always
	if initContainers[1].RestartPolicy == nil || *initContainers[1].RestartPolicy != corev1.ContainerRestartPolicyAlways {
		t.Errorf("initContainers[1].RestartPolicy = %v, want Always", initContainers[1].RestartPolicy)
	}

	// 3. Legacy sidecar container (RestartPolicy should be Always)
	if initContainers[2].RestartPolicy == nil || *initContainers[2].RestartPolicy != corev1.ContainerRestartPolicyAlways {
		t.Errorf("initContainers[2].RestartPolicy = %v, want Always", initContainers[2].RestartPolicy)
	}

	// Verify primary container does not have RestartPolicy set
	primaryContainers := got.Spec.ReplicatedJobs[0].Template.Spec.Template.Spec.Containers
	if len(primaryContainers) != 1 {
		t.Fatalf("Expected 1 primary container, got %d", len(primaryContainers))
	}
	if primaryContainers[0].RestartPolicy != nil {
		t.Errorf("primaryContainers[0].RestartPolicy = %v, want nil", *primaryContainers[0].RestartPolicy)
	}
}
