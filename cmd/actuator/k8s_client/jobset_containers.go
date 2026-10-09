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
	"sort"
	"strconv"
	"strings"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/utils/ptr"
	jobsetv1alpha2 "sigs.k8s.io/jobset/api/jobset/v1alpha2"

	esspb "xmanager_cloud/experiment_state_server/proto/messages_go_proto"
	constants "xmanager_cloud/kubernetes/common/constants"
	"xmanager_cloud/kubernetes/common/jobsetutil"
)

// injectManagedXprofGCSEnvVars appends the per-work-unit managed-xprof GCS object
// folder env var to every container of the replicated job so all workloads in the
// pod know where to write xprof output, not just the primary container. No-op when
// no destination was resolved.
func injectManagedXprofGCSEnvVars(rj *jobsetv1alpha2.ReplicatedJob, xprofObjectPath string) {
	if xprofObjectPath == "" {
		return
	}
	injectManagedXprofGCSEnvVarsToContainers(rj.Template.Spec.Template.Spec.Containers, xprofObjectPath)
	injectManagedXprofGCSEnvVarsToContainers(rj.Template.Spec.Template.Spec.InitContainers, xprofObjectPath)
}

// injectManagedXprofGCSEnvVarsToContainers appends the per-work-unit managed-xprof
// GCS object folder env var to each container in the provided slice in place.
func injectManagedXprofGCSEnvVarsToContainers(containers []corev1.Container, xprofObjectPath string) {
	for i := range containers {
		// Index into the slice so appends write back to the JobSet in place.
		container := &containers[i]
		if xprofObjectPath != "" {
			container.Env = append(container.Env, corev1.EnvVar{Name: constants.XmcEnvVarManagedXprofGCSObjectFolder, Value: xprofObjectPath})
		}
	}
}

func (c *Client) buildInitContainersForJob(job *esspb.KubernetesJob) []corev1.Container {
	total := len(job.GetInitContainers()) + len(job.GetSidecars())
	if total == 0 {
		return nil
	}
	initContainers := make([]corev1.Container, 0, total)
	for i, initContainer := range job.GetInitContainers() {
		name := jobsetutil.InitContainerName(job, initContainer, i)
		container := c.buildContainer(name, initContainer)
		if strings.EqualFold(initContainer.GetRestartPolicy(), "Always") {
			restartPolicyAlways := corev1.ContainerRestartPolicyAlways
			container.RestartPolicy = &restartPolicyAlways
		}
		initContainers = append(initContainers, container)
	}
	restartPolicyAlways := corev1.ContainerRestartPolicyAlways
	for i, sidecar := range job.GetSidecars() {
		name := jobsetutil.SidecarContainerName(job, sidecar, i)
		container := c.buildContainer(name, sidecar)
		container.RestartPolicy = &restartPolicyAlways
		initContainers = append(initContainers, container)
	}
	return initContainers
}

func (c *Client) buildContainersForJob(job *esspb.KubernetesJob) []corev1.Container {
	primaryName := jobsetutil.PrimaryContainerName(job)
	return []corev1.Container{
		c.buildContainer(primaryName, job.GetSpec()),
	}
}

func buildContainerPorts(spec *esspb.ExecutableSpec) []corev1.ContainerPort {
	if len(spec.GetPorts()) == 0 {
		return nil
	}
	ports := make([]corev1.ContainerPort, 0, len(spec.GetPorts()))
	for _, p := range spec.GetPorts() {
		var protocol corev1.Protocol
		switch p.GetProtocol() {
		case esspb.ContainerPort_UDP:
			protocol = corev1.ProtocolUDP
		case esspb.ContainerPort_SCTP:
			protocol = corev1.ProtocolSCTP
		default:
			protocol = corev1.ProtocolTCP
		}
		ports = append(ports, corev1.ContainerPort{
			Name:          p.GetName(),
			ContainerPort: p.GetContainerPort(),
			Protocol:      protocol,
			HostPort:      p.GetHostPort(),
		})
	}
	return ports
}

func buildContainerArgs(spec *esspb.ExecutableSpec) []string {
	var args []string
	specArgs := spec.GetArgs()
	if len(specArgs) > 0 {
		keys := make([]string, 0, len(specArgs))
		for k := range specArgs {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			args = append(args, fmt.Sprintf("--%s=%s", k, specArgs[k]))
		}
	}
	return append(args, spec.GetArgsList()...)
}

func (c *Client) buildContainer(containerName string, spec *esspb.ExecutableSpec) corev1.Container {
	sc := defaultSecurityContext()
	if c != nil && c.RuntimeConfig.AllowPrivilegedContainers && spec.GetPrivileged() {
		sc.Privileged = ptr.To(true)
		sc.AllowPrivilegeEscalation = ptr.To(true)
	}

	return corev1.Container{
		Name:                     containerName,
		Image:                    spec.GetDockerImage(),
		ImagePullPolicy:          corev1.PullAlways,
		TerminationMessagePath:   "/dev/termination-log",
		TerminationMessagePolicy: corev1.TerminationMessageReadFile,
		Command:                  spec.GetCommand(),
		Args:                     buildContainerArgs(spec),
		Env:                      c.generateEnvVarsForSpec(spec),
		Ports:                    buildContainerPorts(spec),
		SecurityContext:          sc,
		VolumeMounts:             generateVolumeMountsForSpec(spec),
	}
}

func parseEnvVarSource(src *esspb.EnvVarSource) *corev1.EnvVarSource {
	if src == nil {
		return nil
	}
	switch ref := src.GetReference().(type) {
	case *esspb.EnvVarSource_FieldRef:
		if ref.FieldRef == nil {
			return nil
		}
		return &corev1.EnvVarSource{
			FieldRef: &corev1.ObjectFieldSelector{
				FieldPath:  ref.FieldRef.FieldPath,
				APIVersion: ref.FieldRef.ApiVersion,
			},
		}
	case *esspb.EnvVarSource_SecretKeyRef:
		if ref.SecretKeyRef == nil {
			return nil
		}
		return &corev1.EnvVarSource{
			SecretKeyRef: &corev1.SecretKeySelector{
				LocalObjectReference: corev1.LocalObjectReference{
					Name: ref.SecretKeyRef.Name,
				},
				Key:      ref.SecretKeyRef.Key,
				Optional: ref.SecretKeyRef.Optional,
			},
		}
	default:
		return nil
	}
}

func appendSpecEnvVars(envVars []corev1.EnvVar, spec *esspb.ExecutableSpec) []corev1.EnvVar {
	specEnvVars := spec.GetEnvVars()
	if len(specEnvVars) > 0 {
		keys := make([]string, 0, len(specEnvVars))
		for k := range specEnvVars {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			envVars = append(envVars, corev1.EnvVar{Name: k, Value: specEnvVars[k]})
		}
	}
	for _, ev := range spec.GetEnv() {
		if ev == nil || ev.Name == "" {
			continue
		}
		k8sEnvVar := corev1.EnvVar{Name: ev.Name}
		switch src := ev.GetSource().(type) {
		case *esspb.EnvVar_Value:
			k8sEnvVar.Value = src.Value
			envVars = append(envVars, k8sEnvVar)
		case *esspb.EnvVar_ValueFrom:
			if k8sSource := parseEnvVarSource(src.ValueFrom); k8sSource != nil {
				k8sEnvVar.ValueFrom = k8sSource
				envVars = append(envVars, k8sEnvVar)
			}
		}
	}
	return envVars
}

func downwardAPIFieldRefEnvVars() []corev1.EnvVar {
	envVarFieldRefs := []struct {
		name      string
		fieldPath string
	}{
		{constants.XmcEnvVarNamespace, "metadata.namespace"},
		{constants.XmcEnvVarQueueName, fmt.Sprintf("metadata.labels['%s']", kueueQueueLabel)},
		{constants.XmcEnvVarExperimentID, fmt.Sprintf("metadata.labels['%s']", constants.ExperimentIDLabel)},
		{constants.XmcEnvVarWorkUnitID, fmt.Sprintf("metadata.labels['%s']", constants.WorkUnitIDLabel)},
		{constants.XmcEnvVarExecutionID, fmt.Sprintf("metadata.labels['%s']", constants.ExecutionIDLabel)},
		{constants.XmcEnvVarAuthor, fmt.Sprintf("metadata.annotations['%s']", constants.AuthorAnnotation)},
		{constants.XmcEnvVarJobIndex, fmt.Sprintf("metadata.labels['%s']", constants.JobSetJobIndexLabel)},
		{constants.XmcEnvVarJobName, fmt.Sprintf("metadata.labels['%s']", constants.JobSetReplicatedJobNameLabel)},
	}

	envVars := make([]corev1.EnvVar, 0, len(envVarFieldRefs))
	for _, ev := range envVarFieldRefs {
		envVars = append(envVars, corev1.EnvVar{
			Name: ev.name,
			ValueFrom: &corev1.EnvVarSource{
				FieldRef: &corev1.ObjectFieldSelector{
					FieldPath: ev.fieldPath,
				},
			},
		})
	}
	return envVars
}

func (c *Client) generateEnvVarsForSpec(spec *esspb.ExecutableSpec) []corev1.EnvVar {
	envVars := appendSpecEnvVars(nil, spec)
	metricsEnabled := false
	if c != nil {
		metricsEnabled = c.RuntimeConfig.MetricsEnabled
	}
	envVars = append(envVars, corev1.EnvVar{Name: constants.XmcEnvVarManagedBy, Value: "true"})
	envVars = append(envVars, corev1.EnvVar{Name: constants.XmcEnvVarMetricsEnabled, Value: strconv.FormatBool(metricsEnabled)})
	return append(envVars, downwardAPIFieldRefEnvVars()...)
}

// defaultSecurityContext returns the default security context for XManager Cloud jobs.
// To the inevitable engineer tinkering with this, please read the security documentation for K8s
// thoroughly before changing this. Unless you're absolutely sure what you're doing, please don't change this.
func defaultSecurityContext() *corev1.SecurityContext {
	return &corev1.SecurityContext{
		AllowPrivilegeEscalation: ptr.To(false),
		Capabilities: &corev1.Capabilities{
			Drop: []corev1.Capability{
				"ALL",
			},
		},
		Privileged: ptr.To(false),
	}
}

// defaultPodSecurityContext returns the default pod security context for XManager Cloud jobs.
// To the inevitable engineer tinkering with this, please read the security documentation for K8s
// thoroughly before changing this. Unless you're absolutely sure what you're doing, please don't change this.
func defaultPodSecurityContext() *corev1.PodSecurityContext {
	return &corev1.PodSecurityContext{
		RunAsGroup: ptr.To(int64(1000)),
		RunAsUser:  ptr.To(int64(1000)),
		SeccompProfile: &corev1.SeccompProfile{
			Type: corev1.SeccompProfileTypeRuntimeDefault,
		},
	}
}
