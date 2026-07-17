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
	"strconv"
	"strings"

	batchv1 "golang/k8s_io/api/v/v0_23/batch/v1/v1"
	corev1 "golang/k8s_io/api/v/v0_23/core/v1/v1"
	"golang/k8s_io/apimachinery/v/v0_23/pkg/api/resource/resource"
	"golang/protobuf/v2/proto/proto"
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

	tpuResourceKey            = "google.com/tpu"
	tpuTolerationValue        = "present"
	tpuAcceleratorSelectorKey = "cloud.google.com/gke-tpu-accelerator"
	tpuTopologySelectorKey    = "cloud.google.com/gke-tpu-topology"

	// TPU generations v3-v6e all use 4 chips/node for multi-host.
	tpuMultiHostChipsPerNode = 4

	tpuICIPort     = 8471
	tpuMetricsPort = 8431

	exclusiveTopologyAnnotation = "alpha.jobset.sigs.k8s.io/exclusive-topology"
	exclusiveTopologyValue      = "cloud.google.com/gke-nodepool"
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
// While GKE supports CPU, GPU, and TPU compute resources, this verifies that any requested TPU topology is structurally valid.
func (g *GKE) IsCompatibleWithWorkUnit(workUnit *epb.WorkUnit) (bool, string) {
	if workUnit == nil || workUnit.GetExecutable().GetJobset() == nil {
		return true, ""
	}
	for _, job := range workUnit.GetExecutable().GetJobset().GetJobs() {
		tpuRes := job.GetSpec().GetResources().GetTpuResources()
		if tpuRes == nil {
			continue
		}
		if _, err := tpuNodesPerSlice(tpuRes.GetTopology()); err != nil {
			return false, fmt.Sprintf("invalid TPU topology %q in job %q: %v", tpuRes.GetTopology(), job.GetName(), err)
		}
	}
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
		chipsPerNode, err := tpuChipsPerNode(tpuRes.GetTopology())
		if err != nil {
			return resourceRequirements, err
		}
		chips := resource.MustParse(strconv.Itoa(chipsPerNode))
		resourceRequirements.Limits[tpuResourceKey] = chips
		resourceRequirements.Requests[tpuResourceKey] = chips
	}

	if storageBytes := resourceSet.GetEphemeralStorageBytes(); storageBytes > 0 {
		storage := resource.MustParse(fmt.Sprintf("%d", storageBytes))
		resourceRequirements.Requests[corev1.ResourceEphemeralStorage] = storage
		resourceRequirements.Limits[corev1.ResourceEphemeralStorage] = storage
	}

	return resourceRequirements, nil
}

// updateNodeSelector updates the node selector for the given resource set.
func updateNodeSelector(resourceSet *epb.ResourceSet, podSpec *corev1.PodSpec) error {
	if tpuRes := resourceSet.GetTpuResources(); tpuRes != nil {
		if podSpec.NodeSelector == nil {
			podSpec.NodeSelector = make(map[string]string)
		}
		podSpec.NodeSelector[tpuAcceleratorSelectorKey] = tpuRes.GetType()
		podSpec.NodeSelector[tpuTopologySelectorKey] = tpuRes.GetTopology()
		// TODO(b/470040109) - Ironwood (TPU7x) requires cloud.google.com/placement-policy-name in the
		// nodeSelector. The value is a user-created workload policy name, which needs to be plumbed
		// through the TpuResources proto.
		return nil
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

// updateTolerations adds necessary TPU tolerations to the pod spec.
func updateTolerations(podSpec *corev1.PodSpec) {
	if podSpec == nil {
		return
	}
	toleration := corev1.Toleration{
		Key:      tpuResourceKey,
		Operator: corev1.TolerationOpEqual,
		Value:    tpuTolerationValue,
		Effect:   corev1.TaintEffectNoSchedule,
	}
	podSpec.Tolerations = append(podSpec.Tolerations, toleration)
}

// ensureJobSetNetworkConfigured guarantees stable DNS resolution for multi-node workloads.
func ensureJobSetNetworkConfigured(jobSet *jobsetv1alpha2.JobSet) {
	if jobSet.Spec.Network != nil {
		return
	}
	jobSet.Spec.Network = &jobsetv1alpha2.Network{
		EnableDNSHostnames:       proto.Bool(true),
		Subdomain:                jobSet.Name,
		PublishNotReadyAddresses: proto.Bool(true),
	}
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

		if len(replicatedJob.Template.Spec.Template.Spec.Containers) == 0 {
			return nil, fmt.Errorf("no containers found to annotate")
		}
		if err := annotateContainers(replicatedJob, job); err != nil {
			return nil, err
		}

		if err := updateNodeSelector(job.Spec.Resources, &replicatedJob.Template.Spec.Template.Spec); err != nil {
			return nil, err
		}

		if tpuRes := job.Spec.Resources.GetTpuResources(); tpuRes != nil {
			updateTolerations(&replicatedJob.Template.Spec.Template.Spec)
			nodesPerSlice, err := tpuNodesPerSlice(tpuRes.GetTopology())
			if err != nil {
				return nil, err
			}
			nodes := int32(nodesPerSlice)
			completionMode := batchv1.IndexedCompletion
			replicatedJob.Template.Spec.Parallelism = proto.Int32(nodes)
			replicatedJob.Template.Spec.Completions = proto.Int32(nodes)
			replicatedJob.Template.Spec.CompletionMode = &completionMode

			if nodesPerSlice > 1 {
				if jobSet.Annotations == nil {
					jobSet.Annotations = make(map[string]string)
				}
				jobSet.Annotations[exclusiveTopologyAnnotation] = exclusiveTopologyValue
				ensureJobSetNetworkConfigured(jobSet)
			}
		} else if job.Spec.Resources.GetGpuResources() != nil {
			// For GPU jobs, we use Replicas to specify the number of nodes, defaulting to 1 if unset.
			nodes := replicatedJob.Replicas
			if nodes < 1 {
				nodes = 1
			}
			completionMode := batchv1.IndexedCompletion
			replicatedJob.Template.Spec.Parallelism = proto.Int32(nodes)
			replicatedJob.Template.Spec.Completions = proto.Int32(nodes)
			replicatedJob.Template.Spec.CompletionMode = &completionMode
			if nodes > 1 {
				ensureJobSetNetworkConfigured(jobSet)
			}
			// For indexed completion, Replicas must be 1.
			collapseReplicasToPods(jobSet, replicatedJob)
		}
	}

	return jobSet, nil
}

// collapseReplicasToPods sets ReplicatedJob.Replicas to 1 and adjusts Coordinator indices if targeted.
func collapseReplicasToPods(jobSet *jobsetv1alpha2.JobSet, replicatedJob *jobsetv1alpha2.ReplicatedJob) {
	if replicatedJob.Replicas > 1 && jobSet.Spec.Coordinator != nil && jobSet.Spec.Coordinator.ReplicatedJob == replicatedJob.Name {
		jobSet.Spec.Coordinator.PodIndex = jobSet.Spec.Coordinator.JobIndex + jobSet.Spec.Coordinator.PodIndex
		jobSet.Spec.Coordinator.JobIndex = 0
	}
	replicatedJob.Replicas = 1
}

func parseTotalChips(topology string) (int, error) {
	if topology == "" {
		return 0, fmt.Errorf("invalid TPU topology: %q", topology)
	}
	dims := strings.Split(topology, "x")
	totalChips := 1
	for _, d := range dims {
		v, err := strconv.Atoi(d)
		if err != nil {
			return 0, fmt.Errorf("invalid TPU topology dimension %q in %q: %w", d, topology, err)
		}
		if v <= 0 {
			return 0, fmt.Errorf("invalid TPU topology dimension %d in %q: must be greater than 0", v, topology)
		}
		totalChips *= v
	}
	return totalChips, nil
}

func tpuChipsPerNode(topology string) (int, error) {
	totalChips, err := parseTotalChips(topology)
	if err != nil {
		return 0, err
	}
	if totalChips <= tpuMultiHostChipsPerNode {
		return totalChips, nil
	}
	return tpuMultiHostChipsPerNode, nil
}

func tpuNodesPerSlice(topology string) (int, error) {
	totalChips, err := parseTotalChips(topology)
	if err != nil {
		return 0, err
	}
	chipsPerNode, err := tpuChipsPerNode(topology)
	if err != nil {
		return 0, err
	}
	return totalChips / chipsPerNode, nil
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

func annotateContainerList(containers []corev1.Container, specs []*epb.ExecutableSpec) ([]corev1.Container, error) {
	if len(containers) != len(specs) {
		return nil, fmt.Errorf("number of containers in replicated job (%d) does not match number of specs (%d)", len(containers), len(specs))
	}
	if len(containers) == 0 {
		return containers, nil
	}
	result := make([]corev1.Container, len(containers))
	copy(result, containers)
	for idx := range result {
		if idx >= len(specs) || specs[idx].GetResources() == nil {
			continue
		}
		reqs, err := resourceRequirements(specs[idx].GetResources())
		if err != nil {
			return nil, err
		}
		result[idx].Resources = reqs
		if specs[idx].GetResources().GetTpuResources() != nil {
			result[idx].Ports = append(result[idx].Ports,
				corev1.ContainerPort{ContainerPort: tpuICIPort},
				corev1.ContainerPort{ContainerPort: tpuMetricsPort},
			)
		}
	}
	return result, nil
}

func annotateContainers(replicatedJob *jobsetv1alpha2.ReplicatedJob, job *epb.KubernetesJob) error {
	specs := []*epb.ExecutableSpec{job.Spec}
	specs = append(specs, job.Sidecars...)
	containers, err := annotateContainerList(replicatedJob.Template.Spec.Template.Spec.Containers, specs)
	if err != nil {
		return err
	}
	initContainers, err := annotateContainerList(replicatedJob.Template.Spec.Template.Spec.InitContainers, job.InitContainers)
	if err != nil {
		return err
	}
	replicatedJob.Template.Spec.Template.Spec.Containers = containers
	replicatedJob.Template.Spec.Template.Spec.InitContainers = initContainers
	return nil
}

func init() {
	Register(&GKE{})
}
