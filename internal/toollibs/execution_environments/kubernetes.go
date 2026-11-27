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
	"fmt"
	"slices"
	"text/template"

	"google.golang.org/protobuf/proto"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	jobsetv1alpha2 "sigs.k8s.io/jobset/api/jobset/v1alpha2"
	epb "xmanager_cloud/api/protos/experiment"
	"xmanager_cloud/internal/kubernetes/common/constants"
)

const (
	kubernetesName        = "kubernetes"
	kubernetesDescription = "The execution environment for standard Kubernetes clusters using JobSet and Kueue. Cluster-specific accelerator schedulers, supplemental device groups, and metadata templates are configured by the cluster administrator."
)

// Kubernetes is a vendor-neutral execution environment for standard Kubernetes clusters.
type Kubernetes struct {
	placement *compiledPlacementConfig
}

// NewKubernetes constructs a Kubernetes execution environment with the given placement profile.
func NewKubernetes(cfg PlacementConfig) (*Kubernetes, error) {
	compiled, err := compilePlacementConfig(cfg)
	if err != nil {
		return nil, err
	}
	return &Kubernetes{placement: compiled}, nil
}

// Name returns the name of the execution environment.
func (k *Kubernetes) Name() string {
	return kubernetesName
}

// Description returns a description of the execution environment.
func (k *Kubernetes) Description() string {
	return kubernetesDescription
}

// IsCompatibleWithWorkUnit validates that the work unit's resource topology is structurally valid.
func (k *Kubernetes) IsCompatibleWithWorkUnit(workUnit *epb.WorkUnit) (bool, string) {
	return (&GKE{}).IsCompatibleWithWorkUnit(workUnit)
}

// InjectOnJobSetCreation configures container resources, multi-node networking, and
// any cluster-administrator accelerator placement defaults on the JobSet.
func (k *Kubernetes) InjectOnJobSetCreation(ctx context.Context, workUnit *epb.WorkUnit, jobSet *jobsetv1alpha2.JobSet) (*jobsetv1alpha2.JobSet, error) {
	if workUnit.GetExecutable().GetJobset() == nil {
		return nil, fmt.Errorf("work unit does not contain a jobset")
	}

	jobSet = jobSet.DeepCopy()
	replicatedJobsMap := make(map[string]*jobsetv1alpha2.ReplicatedJob, len(jobSet.Spec.ReplicatedJobs))
	for i := range jobSet.Spec.ReplicatedJobs {
		replicatedJobsMap[jobSet.Spec.ReplicatedJobs[i].Name] = &jobSet.Spec.ReplicatedJobs[i]
	}

	for _, job := range workUnit.GetExecutable().GetJobset().GetJobs() {
		replicatedJob, exists := replicatedJobsMap[job.GetName()]
		if !exists {
			return nil, fmt.Errorf("job %s not found in jobset", job.GetName())
		}
		if err := k.configureKubernetesJob(jobSet, replicatedJob, job); err != nil {
			return nil, err
		}
	}
	return jobSet, nil
}

func (k *Kubernetes) configureKubernetesJob(jobSet *jobsetv1alpha2.JobSet, replicatedJob *jobsetv1alpha2.ReplicatedJob, job *epb.KubernetesJob) error {
	resources := job.GetSpec().GetResources()
	if resources == nil {
		return fmt.Errorf("no resources found to annotate for job %s", job.GetName())
	}
	if len(replicatedJob.Template.Spec.Template.Spec.Containers) == 0 {
		return fmt.Errorf("no containers found to annotate")
	}
	if err := annotateContainers(replicatedJob, job); err != nil {
		return err
	}

	switch {
	case resources.GetGpuResources() != nil:
		return k.configureGPUJob(jobSet, replicatedJob, job.GetName(), resources.GetGpuResources())
	case resources.GetTpuResources() != nil:
		return k.configureTPUJob(jobSet, replicatedJob, job.GetName(), resources.GetTpuResources())
	default:
		return nil
	}
}

func (k *Kubernetes) configureGPUJob(jobSet *jobsetv1alpha2.JobSet, replicatedJob *jobsetv1alpha2.ReplicatedJob, jobName string, gpuRes *epb.GpuResources) error {
	ensureExistsToleration(&replicatedJob.Template.Spec.Template.Spec, nvidiaGPUKey)
	configureGPUReplicatedJob(jobSet, replicatedJob)
	if k.placement == nil {
		return nil
	}
	vals := buildTemplateValues(jobSet, jobName, gpuRes.GetType(), "", gpuRes.GetCount())
	return applyAcceleratorPlacement(jobSet, replicatedJob, "gpu", k.placement.gpu, vals)
}

func (k *Kubernetes) configureTPUJob(jobSet *jobsetv1alpha2.JobSet, replicatedJob *jobsetv1alpha2.ReplicatedJob, jobName string, tpuRes *epb.TpuResources) error {
	hostCount, err := tpuNodesPerSlice(tpuRes.GetTopology())
	if err != nil {
		return err
	}
	ensureExistsToleration(&replicatedJob.Template.Spec.Template.Spec, tpuResourceKey)

	completionMode := batchv1.IndexedCompletion
	replicatedJob.Template.Spec.Parallelism = proto.Int32(int32(hostCount))
	replicatedJob.Template.Spec.Completions = proto.Int32(int32(hostCount))
	replicatedJob.Template.Spec.CompletionMode = &completionMode
	if hostCount > 1 {
		ensureJobSetNetworkConfigured(jobSet)
	}

	if k.placement == nil {
		return nil
	}
	vals := buildTemplateValues(jobSet, jobName, tpuRes.GetType(), tpuRes.GetTopology(), 0)
	return applyAcceleratorPlacement(jobSet, replicatedJob, "tpu", k.placement.tpu, vals)
}

func ensureExistsToleration(podSpec *corev1.PodSpec, key string) {
	for _, tol := range podSpec.Tolerations {
		if tol.Key == key && tol.Operator == corev1.TolerationOpExists && tol.Effect == corev1.TaintEffectNoSchedule {
			return
		}
	}
	podSpec.Tolerations = append(podSpec.Tolerations, corev1.Toleration{
		Key:      key,
		Operator: corev1.TolerationOpExists,
		Effect:   corev1.TaintEffectNoSchedule,
	})
}

func buildTemplateValues(jobSet *jobsetv1alpha2.JobSet, jobName, accType, topology string, count int64) TemplateValues {
	return TemplateValues{
		JobSetName:      jobSet.Name,
		Namespace:       jobSet.Namespace,
		JobName:         jobName,
		XID:             jobSet.Labels[constants.ExperimentIDLabel],
		WID:             jobSet.Labels[constants.WorkUnitIDLabel],
		EID:             jobSet.Labels[constants.ExecutionIDLabel],
		AcceleratorType: accType,
		Topology:        topology,
		Count:           count,
	}
}

func applyAcceleratorPlacement(jobSet *jobsetv1alpha2.JobSet, replicatedJob *jobsetv1alpha2.ReplicatedJob, scope string, p compiledAcceleratorPlacement, vals TemplateValues) error {
	podTemplate := &replicatedJob.Template.Spec.Template
	if p.schedulerName != "" && podTemplate.Spec.SchedulerName == "" {
		podTemplate.Spec.SchedulerName = p.schedulerName
	}
	appendSupplementalGroups(&podTemplate.Spec, p.supplementalGroups)

	if err := renderAndMerge(&podTemplate.Spec.NodeSelector, p.nodeSelector, vals, true, scope+".nodeSelector"); err != nil {
		return err
	}
	if err := renderAndMerge(&podTemplate.Labels, p.podLabels, vals, true, scope+".podLabels"); err != nil {
		return err
	}
	if err := renderAndMerge(&podTemplate.Annotations, p.podAnnotations, vals, false, scope+".podAnnotations"); err != nil {
		return err
	}
	if err := renderAndMerge(&jobSet.Labels, p.jobSetLabels, vals, true, scope+".jobSetLabels"); err != nil {
		return err
	}
	return renderAndMerge(&jobSet.Annotations, p.jobSetAnnotations, vals, false, scope+".jobSetAnnotations")
}

func appendSupplementalGroups(podSpec *corev1.PodSpec, groups []int64) {
	if len(groups) == 0 {
		return
	}
	if podSpec.SecurityContext == nil {
		podSpec.SecurityContext = &corev1.PodSecurityContext{}
	}
	for _, gid := range groups {
		if !slices.Contains(podSpec.SecurityContext.SupplementalGroups, gid) {
			podSpec.SecurityContext.SupplementalGroups = append(podSpec.SecurityContext.SupplementalGroups, gid)
		}
	}
}

func renderAndMerge(dst *map[string]string, templates map[string]*template.Template, vals TemplateValues, isLabel bool, field string) error {
	rendered, err := renderTemplateMap(templates, vals, isLabel, field)
	if err != nil || len(rendered) == 0 {
		return err
	}
	if *dst == nil {
		*dst = make(map[string]string, len(rendered))
	}
	for k, v := range rendered {
		if (*dst)[k] == "" {
			(*dst)[k] = v
		}
	}
	return nil
}

// InjectOnExperimentCreation adds no vendor-specific URLs.
func (k *Kubernetes) InjectOnExperimentCreation(ctx context.Context, essServer epb.ExperimentStateServiceServer, experiment *epb.Experiment) (*epb.Experiment, error) {
	return experiment, nil
}

// InjectOnWorkUnitCreation adds no vendor-specific URLs.
func (k *Kubernetes) InjectOnWorkUnitCreation(ctx context.Context, essServer epb.ExperimentStateServiceServer, workUnit *epb.WorkUnit) (*epb.WorkUnit, error) {
	return workUnit, nil
}

func init() {
	Register(&Kubernetes{})
}
