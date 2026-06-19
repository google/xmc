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
	"fmt"

	corev1 "golang/k8s_io/api/v/v0_23/core/v1/v1"
	"golang/k8s_io/apimachinery/v/v0_23/pkg/api/resource/resource"
	jobsetv1alpha2 "sigs.k8s.io/jobset/api/jobset/v1alpha2"
	spb "xmanager_cloud/experiment_state_server/proto/api_go_grpc_proto"
	epb "xmanager_cloud/experiment_state_server/proto/messages_go_proto"
)

const (
	name        = "gke"
	description = "The execution environment for running experiments on GKE. See https://cloud.google.com/kubernetes-engine/docs/concepts/kubernetes-engine-overview for more details."

	baseURL = "https://console.cloud.google.com"

	xidPlaceholder       = "${XID}"
	widPlaceholder       = "${WID}"
	createdPlaceholder   = "${CREATED}"
	locationPlaceholder  = "${LOCATION}"
	namespacePlaceholder = "${NAMESPACE}"
	clusterPlaceholder   = "${CLUSTER}"
	resourcePlaceholder  = "${RESOURCE_NAME}"
	projectPlaceholder   = "${PROJECT}"

	gcpExpDebug      = baseURL + "/logs/query;query=resource.type%3D%22k8s_container%22%0Aresource.labels.namespace_name%3D%22xmc%22%0Alabels.xid%3D%22" + xidPlaceholder + "%22;summaryFields=labels%252Fxid,labels%252Fwid,labels%252Feid,labels%252Fjobset:false:32:beginning;startTime=" + createdPlaceholder
	gkeDashboardURL  = baseURL + "/kubernetes/aiml/jobset/" + projectPlaceholder + "/GKE_CLUSTER/" + locationPlaceholder + "/" + clusterPlaceholder + "/" + namespacePlaceholder + "/" + resourcePlaceholder + "/overview?project=" + projectPlaceholder
	gkeWorkloadLogs  = baseURL + "/logs/query;query=resource.type%3D%22k8s_container%22%0A%20%20%20%20resource.labels.location%3D%22" + locationPlaceholder + "%22%0A%20%20%20%20resource.labels.namespace_name%3D%22" + namespacePlaceholder + "%22%0A%20%20%20%20resource.labels.cluster_name%3D%22" + clusterPlaceholder + "%22%0A%20%20%20%20labels.k8s-pod%2Fjobset_sigs_k8s_io%2Fjobset-name%3D%22" + resourcePlaceholder + "%22%20severity%3E%3DDEFAULT;storageScope=project;cursorTimestamp=;duration=PT30M?project=" + projectPlaceholder
	gkeWorkloadDebug = baseURL + "/logs/query;query=resource.type%3D%22k8s_container%22%0Aresource.labels.namespace_name%3D%22xmc%22%0Alabels.xid%3D%22" + xidPlaceholder + "%22%0Alabels.wid%3D%22" + widPlaceholder + "%22;summaryFields=labels%252Fxid,labels%252Fwid,labels%252Feid,labels%252Fjobset:false:32:beginning;startTime=" + createdPlaceholder + "?project=" + projectPlaceholder

	nvidiaGPUKey          = "nvidia.com/gpu"
	nvidiaNodeSelectorKey = "cloud.google.com/gke-accelerator"
)

var (
	experimentURLs = []*epb.Url{
		{
			Url:         gcpExpDebug,
			Icon:        "plumbing",
			DisplayName: "Debug Experiment",
			Priority:    debugPriority,
		},
	}
	workUnitURLs = []*epb.Url{
		{
			Url:         gkeDashboardURL,
			Icon:        "cloud",
			DisplayName: "GKE Dashboard",
			Priority:    dashboardPriority,
		},
		{
			Url:         gkeWorkloadLogs,
			Icon:        "bug_report",
			DisplayName: "Cloud Logs",
			Priority:    dashboardPriority,
		},
		{
			Url:         gkeWorkloadDebug,
			Icon:        "plumbing",
			DisplayName: "Debug Work Unit",
			Priority:    debugPriority,
		},
	}

	gpuTypeToGKEAccelerator = map[string]string{
		"P4":         "nvidia-tesla-p4",
		"T4":         "nvidia-tesla-t4",
		"L4_24TH":    "nvidia-l4",
		"P100":       "nvidia-tesla-p100",
		"V100":       "nvidia-tesla-v100",
		"A100":       "nvidia-a100-40gb",
		"A100_80GIB": "nvidia-a100-80gb",
		"H100":       "nvidia-h100-80gb",
		"H200":       "nvidia-h200-141gb",
		"B200":       "nvidia-b200",
		"GB200":      "nvidia-gb200",
	}
)

// GKE is an execution environment for GKE.
type GKE struct{}

// Name returns the name of the execution environment.
func (g *GKE) Name() string {
	return name
}

// Description returns a description of the execution environment.
func (g *GKE) Description() string {
	return description
}

// IsCompatibleWithWorkUnit returns whether the execution environment is compatible with the given work unit.
// It returns a reason if the environment is not compatible.
func (g *GKE) IsCompatibleWithWorkUnit(workUnit *epb.WorkUnit) (bool, string) {
	// Since this is GKE, we should support everything at this point.
	return true, ""
}

// resourceRequirements returns the resource requirements for the given resource set.
func resourceRequirements(resourceSet *epb.ResourceSet) (corev1.ResourceRequirements, error) {
	resourceRequirements := corev1.ResourceRequirements{
		Limits:   make(corev1.ResourceList),
		Requests: make(corev1.ResourceList),
	}

	if cpuRes := resourceSet.GetCpuResources(); cpuRes != nil {
		resourceRequirements.Requests[corev1.ResourceCPU] = resource.MustParse(fmt.Sprintf("%dm", cpuRes.GetCpuMillicores()))
		resourceRequirements.Limits[corev1.ResourceMemory] = resource.MustParse(fmt.Sprintf("%d", cpuRes.GetMemoryBytes()))
		resourceRequirements.Requests[corev1.ResourceMemory] = resource.MustParse(fmt.Sprintf("%d", cpuRes.GetMemoryBytes()))
	} else if gpuRes := resourceSet.GetGpuResources(); gpuRes != nil {
		resourceRequirements.Limits[nvidiaGPUKey] = resource.MustParse(fmt.Sprintf("%d", gpuRes.GetCount()))
	} else if tpuRes := resourceSet.GetTpuResources(); tpuRes != nil {
		// TODO - Implement TPU support.
		return resourceRequirements, fmt.Errorf("TPU support is not implemented yet")
	}
	return resourceRequirements, nil
}

// updateNodeSelector updates the node selector for the given resource set.
func updateNodeSelector(resourceSet *epb.ResourceSet, podSpec *corev1.PodSpec) error {
	if tpuRes := resourceSet.GetTpuResources(); tpuRes != nil {
		// TODO - Implement TPU support.
		return fmt.Errorf("TPU support is not implemented yet")
	}

	gpuRes := resourceSet.GetGpuResources()
	if gpuRes == nil {
		return nil
	}

	if podSpec.NodeSelector == nil {
		podSpec.NodeSelector = make(map[string]string)
	}

	accelerator, ok := gpuTypeToGKEAccelerator[gpuRes.GetType()]
	if !ok {
		return fmt.Errorf("unsupported GPU type: %q", gpuRes.GetType())
	}
	podSpec.NodeSelector[nvidiaNodeSelectorKey] = accelerator
	return nil
}

// InjectOnJobSetCreation annotates the given job requirements with the execution environment's
// specific resources.
func (g *GKE) InjectOnJobSetCreation(ctx context.Context, workUnit *epb.WorkUnit, jobSet *jobsetv1alpha2.JobSet) (*jobsetv1alpha2.JobSet, error) {
	if workUnit.GetExecutable().GetJobset() == nil {
		return nil, fmt.Errorf("work unit does not contain a jobset")
	}

	jobSet = jobSet.DeepCopy()
	replicatedJobsMap := make(map[string]*jobsetv1alpha2.ReplicatedJob)
	for i := range jobSet.Spec.ReplicatedJobs {
		replicatedJobsMap[jobSet.Spec.ReplicatedJobs[i].Name] = &jobSet.Spec.ReplicatedJobs[i]
	}

	for _, job := range workUnit.GetExecutable().GetJobset().GetJobs() {
		replicatedJob, exists := replicatedJobsMap[job.Name]
		if !exists {
			return nil, fmt.Errorf("job %s not found in jobset", job.Name)
		}

		if job.Spec.Resources == nil {
			return nil, fmt.Errorf("no resources found to annotate for job %s", job.Name)
		}

		resourceRequirements, err := resourceRequirements(job.Spec.Resources)
		if err != nil {
			return nil, err
		}

		if len(replicatedJob.Template.Spec.Template.Spec.Containers) == 0 {
			return nil, fmt.Errorf("no containers found to annotate")
		}
		replicatedJob.Template.Spec.Template.Spec.Containers[0].Resources = resourceRequirements

		if err := updateNodeSelector(job.Spec.Resources, &replicatedJob.Template.Spec.Template.Spec); err != nil {
			return nil, err
		}
	}

	return jobSet, nil
}

// InjectOnExperimentCreation injects the environment on experiment creation.
func (g *GKE) InjectOnExperimentCreation(ctx context.Context, essServer spb.ExperimentStateServiceServer, experiment *epb.Experiment) (*epb.Experiment, error) {
	experiment.Urls = append(experiment.GetUrls(), experimentURLs...)
	return experiment, nil
}

// InjectOnWorkUnitCreation injects the environment on work unit creation.
func (g *GKE) InjectOnWorkUnitCreation(ctx context.Context, essServer spb.ExperimentStateServiceServer, workUnit *epb.WorkUnit) (*epb.WorkUnit, error) {
	workUnit.Urls = append(workUnit.GetUrls(), workUnitURLs...)
	return workUnit, nil
}

func init() {
	Register(&GKE{})
}
