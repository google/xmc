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

	"google.golang.org/protobuf/proto"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	jobsetv1alpha2 "sigs.k8s.io/jobset/api/jobset/v1alpha2"
	epb "xmanager_cloud/api/protos/experiment"
	"xmanager_cloud/internal/kubernetes/common/constants"
)

const (
	name        = "gke"
	description = "The execution environment for running experiments on GKE. See https://cloud.google.com/kubernetes-engine/docs/concepts/kubernetes-engine-overview for more details."

	baseURL = "https://console.cloud.google.com"

	xidPlaceholder         = "${XID}"
	widPlaceholder         = "${WID}"
	createdPlaceholder     = "${CREATED}"
	locationPlaceholder    = "${LOCATION}"
	namespacePlaceholder   = "${NAMESPACE}"
	clusterPlaceholder     = "${CLUSTER}"
	resourcePlaceholder    = "${RESOURCE_NAME}"
	projectPlaceholder     = "${PROJECT}"
	mgmtProjectPlaceholder = "${MGMT_PROJECT}"

	gcpExpDebug      = baseURL + "/logs/query;query=resource.type%3D%22k8s_container%22%0Aresource.labels.namespace_name%3D%22xmc%22%0Alabels.xid%3D%22" + xidPlaceholder + "%22;summaryFields=labels%252Fxid,labels%252Fwid,labels%252Feid,labels%252Fjobset:false:32:beginning;startTime=" + createdPlaceholder
	gkeDashboardURL  = baseURL + "/kubernetes/aiml/jobset/" + projectPlaceholder + "/GKE_CLUSTER/" + locationPlaceholder + "/" + clusterPlaceholder + "/" + namespacePlaceholder + "/" + resourcePlaceholder + "/overview?project=" + projectPlaceholder
	gkeWorkloadLogs  = baseURL + "/logs/query;query=resource.type%3D%22k8s_container%22%0A%20%20%20%20resource.labels.location%3D%22" + locationPlaceholder + "%22%0A%20%20%20%20resource.labels.namespace_name%3D%22" + namespacePlaceholder + "%22%0A%20%20%20%20resource.labels.cluster_name%3D%22" + clusterPlaceholder + "%22%0A%20%20%20%20labels.k8s-pod%2Fjobset_sigs_k8s_io%2Fjobset-name%3D%22" + resourcePlaceholder + "%22%20severity%3E%3DDEFAULT;storageScope=project;cursorTimestamp=;duration=PT30M?project=" + projectPlaceholder
	gkeWorkloadDebug = baseURL + "/logs/query;query=resource.type%3D%22k8s_container%22%0Aresource.labels.namespace_name%3D%22xmc%22%0Alabels.xid%3D%22" + xidPlaceholder + "%22%0Alabels.wid%3D%22" + widPlaceholder + "%22;summaryFields=labels%252Fxid,labels%252Fwid,labels%252Feid,labels%252Fjobset:false:32:beginning;startTime=" + createdPlaceholder + "?project=" + mgmtProjectPlaceholder

	nvidiaGPUKey          = constants.NvidiaGPUResourceName
	nvidiaNodeSelectorKey = "cloud.google.com/gke-accelerator"

	// Host resources a GPU job is given per card. `ResourceSet` holds its
	// resource kinds in a `oneof`, so a job that names GPUs cannot also name CPU
	// or memory, and it reaches the cluster asking for neither. These are floors
	// that stand in until the API can carry the real figures, sized to feed one
	// card rather than to size a trainer.
	defaultCPUMillicoresPerGPU = 1000
	defaultMemoryBytesPerGPU   = 4 << 30

	tpuResourceKey            = constants.TPUResourceName
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

func applyCPUResources(cpuRes *epb.CpuResources, reqs *corev1.ResourceRequirements) error {
	if cpuRes == nil || reqs == nil {
		return nil
	}
	if reqs.Requests == nil {
		reqs.Requests = make(corev1.ResourceList)
	}
	if reqs.Limits == nil {
		reqs.Limits = make(corev1.ResourceList)
	}

	cpuMillicores := cpuRes.GetCpuMillicores()
	limitCPU := cpuRes.GetLimitCpuMillicores()
	if limitCPU > 0 && limitCPU < cpuMillicores {
		return fmt.Errorf("CPU limit (%dm) cannot be less than CPU request (%dm)", limitCPU, cpuMillicores)
	}

	reqs.Requests[corev1.ResourceCPU] = resource.MustParse(fmt.Sprintf("%dm", cpuMillicores))
	if limitCPU > 0 {
		reqs.Limits[corev1.ResourceCPU] = resource.MustParse(fmt.Sprintf("%dm", limitCPU))
	}
	reqs.Limits[corev1.ResourceMemory] = resource.MustParse(fmt.Sprintf("%d", cpuRes.GetMemoryBytes()))
	reqs.Requests[corev1.ResourceMemory] = resource.MustParse(fmt.Sprintf("%d", cpuRes.GetMemoryBytes()))
	return nil
}

// applyGPUResources asks for the cards, and for the host resources to feed them.
//
// A pod that requests neither CPU nor memory is BestEffort, which the kubelet
// evicts first under memory pressure, so a job that outgrows its node is killed
// rather than scheduled somewhere it fits. It also reaches Kueue costing
// nothing, so quota is reserved against figures that bear no relation to what
// the pod goes on to use.
//
// Only requests are set. A limit would cap a job at a figure it never chose,
// and the memory one would be enforced by the OOM killer.
func applyGPUResources(gpuRes *epb.GpuResources, reqs *corev1.ResourceRequirements) {
	if gpuRes == nil || reqs == nil {
		return
	}
	if reqs.Requests == nil {
		reqs.Requests = make(corev1.ResourceList)
	}
	if reqs.Limits == nil {
		reqs.Limits = make(corev1.ResourceList)
	}

	count := gpuRes.GetCount()
	if count < 1 {
		// Validation rejects this, so it is reached only by a caller that skipped
		// it. One card's worth is a safer floor than none, and clamping before the
		// limit is set keeps the cards asked for and the host resources that feed
		// them describing the same job.
		count = 1
	}
	reqs.Limits[nvidiaGPUKey] = resource.MustParse(fmt.Sprintf("%d", count))
	reqs.Requests[corev1.ResourceCPU] = resource.MustParse(fmt.Sprintf("%dm", count*defaultCPUMillicoresPerGPU))
	reqs.Requests[corev1.ResourceMemory] = resource.MustParse(fmt.Sprintf("%d", count*defaultMemoryBytesPerGPU))
}

func applyTPUResources(tpuRes *epb.TpuResources, reqs *corev1.ResourceRequirements) error {
	chipsPerNode, err := tpuChipsPerNode(tpuRes.GetTopology())
	if err != nil {
		return err
	}
	chips := resource.MustParse(strconv.Itoa(chipsPerNode))
	reqs.Limits[tpuResourceKey] = chips
	reqs.Requests[tpuResourceKey] = chips
	return nil
}

func applyResourceSet(resourceSet *epb.ResourceSet, reqs *corev1.ResourceRequirements) error {
	if cpuRes := resourceSet.GetCpuResources(); cpuRes != nil {
		return applyCPUResources(cpuRes, reqs)
	}
	if gpuRes := resourceSet.GetGpuResources(); gpuRes != nil {
		applyGPUResources(gpuRes, reqs)
		return nil
	}
	if tpuRes := resourceSet.GetTpuResources(); tpuRes != nil {
		return applyTPUResources(tpuRes, reqs)
	}
	return nil
}

// resourceRequirements returns the resource requirements for the given resource set.
func resourceRequirements(resourceSet *epb.ResourceSet) (corev1.ResourceRequirements, error) {
	resourceRequirements := corev1.ResourceRequirements{
		Limits:   make(corev1.ResourceList),
		Requests: make(corev1.ResourceList),
	}

	if err := applyResourceSet(resourceSet, &resourceRequirements); err != nil {
		return resourceRequirements, err
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
		// TODO: Ironwood (TPU7x) requires cloud.google.com/placement-policy-name in the
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

	accelerator, ok := constants.GPUTypeToGKEAccelerator[gpuRes.GetType()]
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
		annotateGCSVolumes(replicatedJob, job)

		if err := updateNodeSelector(job.Spec.Resources, &replicatedJob.Template.Spec.Template.Spec); err != nil {
			return nil, err
		}

		if err := configureJobResources(jobSet, replicatedJob, job.Spec.Resources); err != nil {
			return nil, err
		}
	}

	return jobSet, nil
}

func configureJobResources(jobSet *jobsetv1alpha2.JobSet, replicatedJob *jobsetv1alpha2.ReplicatedJob, res *epb.ResourceSet) error {
	if res.GetTpuResources() != nil {
		return configureTPUReplicatedJob(jobSet, replicatedJob, res.GetTpuResources())
	}
	if res.GetGpuResources() != nil {
		configureGPUReplicatedJob(jobSet, replicatedJob)
	}
	return nil
}

func configureGPUReplicatedJob(jobSet *jobsetv1alpha2.JobSet, replicatedJob *jobsetv1alpha2.ReplicatedJob) {
	// For GPU jobs, Replicas specifies the number of pod replicas, defaulting to 1 if unset.
	replicas := replicatedJob.Replicas
	if replicas < 1 {
		replicas = 1
	}
	completionMode := batchv1.IndexedCompletion
	replicatedJob.Template.Spec.Parallelism = proto.Int32(replicas)
	replicatedJob.Template.Spec.Completions = proto.Int32(replicas)
	replicatedJob.Template.Spec.CompletionMode = &completionMode
	if replicas > 1 {
		ensureJobSetNetworkConfigured(jobSet)
	}
	collapseReplicasToPods(jobSet, replicatedJob)
}

// collapseReplicasToPods sets ReplicatedJob.Replicas to 1 and adjusts Coordinator indices if targeted.
func collapseReplicasToPods(jobSet *jobsetv1alpha2.JobSet, replicatedJob *jobsetv1alpha2.ReplicatedJob) {
	if replicatedJob.Replicas > 1 && jobSet.Spec.Coordinator != nil && jobSet.Spec.Coordinator.ReplicatedJob == replicatedJob.Name {
		jobSet.Spec.Coordinator.PodIndex = jobSet.Spec.Coordinator.JobIndex + jobSet.Spec.Coordinator.PodIndex
		jobSet.Spec.Coordinator.JobIndex = 0
	}
	replicatedJob.Replicas = 1
}

func configureTPUReplicatedJob(jobSet *jobsetv1alpha2.JobSet, replicatedJob *jobsetv1alpha2.ReplicatedJob, tpuRes *epb.TpuResources) error {
	if tpuRes == nil {
		return nil
	}
	updateTolerations(&replicatedJob.Template.Spec.Template.Spec)
	nodesPerSlice, err := tpuNodesPerSlice(tpuRes.GetTopology())
	if err != nil {
		return err
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
	return nil
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
func (g *GKE) InjectOnExperimentCreation(ctx context.Context, essServer epb.ExperimentStateServiceServer, experiment *epb.Experiment) (*epb.Experiment, error) {
	experiment.Urls = append(experiment.GetUrls(), experimentURLs...)
	return experiment, nil
}

// InjectOnWorkUnitCreation injects the environment on work unit creation.
func (g *GKE) InjectOnWorkUnitCreation(ctx context.Context, essServer epb.ExperimentStateServiceServer, workUnit *epb.WorkUnit) (*epb.WorkUnit, error) {
	workUnit.Urls = append(workUnit.GetUrls(), workUnitURLs...)
	return workUnit, nil
}

func ensurePortExists(existing []corev1.ContainerPort, port int32) []corev1.ContainerPort {
	for _, p := range existing {
		if p.ContainerPort == port {
			return existing
		}
	}
	result := make([]corev1.ContainerPort, len(existing)+1)
	copy(result, existing)
	result[len(existing)] = corev1.ContainerPort{ContainerPort: port}
	return result
}

func ensureTpuContainerPorts(existing []corev1.ContainerPort) []corev1.ContainerPort {
	existing = ensurePortExists(existing, tpuICIPort)
	existing = ensurePortExists(existing, tpuMetricsPort)
	return existing
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
			result[idx].Ports = ensureTpuContainerPorts(result[idx].Ports)
		}
	}
	return result, nil
}

func annotateContainers(replicatedJob *jobsetv1alpha2.ReplicatedJob, job *epb.KubernetesJob) error {
	containers, err := annotateContainerList(replicatedJob.Template.Spec.Template.Spec.Containers, []*epb.ExecutableSpec{job.Spec})
	if err != nil {
		return err
	}
	initSpecs := make([]*epb.ExecutableSpec, 0, len(job.InitContainers)+len(job.Sidecars))
	initSpecs = append(initSpecs, job.InitContainers...)
	initSpecs = append(initSpecs, job.Sidecars...)
	initContainers, err := annotateContainerList(replicatedJob.Template.Spec.Template.Spec.InitContainers, initSpecs)
	if err != nil {
		return err
	}
	replicatedJob.Template.Spec.Template.Spec.Containers = containers
	replicatedJob.Template.Spec.Template.Spec.InitContainers = initContainers
	return nil
}

const (
	gcsFuseAnnotationKey = "gke-gcsfuse/volumes"
	gcsFuseAnnotationVal = "true"
)

func annotateGCSVolumes(replicatedJob *jobsetv1alpha2.ReplicatedJob, job *epb.KubernetesJob) {
	hasGCS := false
	for _, v := range job.GetVolumes() {
		if v.GetGcs() != nil {
			hasGCS = true
			break
		}
	}
	if !hasGCS {
		for _, vol := range replicatedJob.Template.Spec.Template.Spec.Volumes {
			if vol.CSI != nil && vol.CSI.Driver == "gcs.csi.storage.gke.io" {
				hasGCS = true
				break
			}
		}
	}
	if !hasGCS {
		return
	}
	if replicatedJob.Template.Spec.Template.ObjectMeta.Annotations == nil {
		replicatedJob.Template.Spec.Template.ObjectMeta.Annotations = make(map[string]string)
	}
	replicatedJob.Template.Spec.Template.ObjectMeta.Annotations[gcsFuseAnnotationKey] = gcsFuseAnnotationVal
}

func init() {
	Register(&GKE{})
}
