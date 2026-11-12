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
// Package constants contains constants used by the actuator.
package constants

import "time"

const (
	// AuthorAnnotation is the annotation used to identify the author of a JobSet.
	AuthorAnnotation = "xmc.google.com/author"

	// LastEditLabel is a label to track the last edit time of a JobSet.
	LastEditLabel = "xmc.google.com/last-edit"

	// ExecutionIDLabel is the label used to identify the execution ID.
	ExecutionIDLabel = "xmc.google.com/execution-id"

	// WorkUnitIDLabel is the label used to identify the work unit ID.
	WorkUnitIDLabel = "xmc.google.com/work-unit-id"

	// ExperimentIDLabel is the label used to identify the experiment ID.
	ExperimentIDLabel = "xmc.google.com/experiment-id"

	// JobSetFinalizer is the finalizer used by XManager Cloud to manage JobSet lifecycle.
	JobSetFinalizer = "jobset.xmanager.cloud/finalizer"

	// ManagedByLabel is the label used to identify XManager Cloud managed resources.
	ManagedByLabel = "app.kubernetes.io/managed-by"

	// AutoscalerSafeToEvictAnnotation is the annotation used to mark a JobSet as safe to evict by the autoscaler.
	AutoscalerSafeToEvictAnnotation = "cluster-autoscaler.kubernetes.io/safe-to-evict"

	// XmcLabelValue is the value for ManagedByLabel for XManager Cloud resources.
	XmcLabelValue = "xmc"

	// XmcEnvVarManagedBy is an environment variable set for all XManager Cloud jobs. It is set to
	// true if the job is managed by XManager Cloud.
	XmcEnvVarManagedBy = "XMC_MANAGED_BY"

	// XmcEnvVarAuthor is an environment variable set for all XManager Cloud jobs. It is set to the
	// user who launched the experiment.
	XmcEnvVarAuthor = "XMC_AUTHOR"

	// XmcEnvVarNamespace is an environment variable set for all XManager Cloud jobs. It is set to the
	// namespace of the job.
	XmcEnvVarNamespace = "XMC_NAMESPACE"

	// XmcEnvVarQueueName is an environment variable set for all XManager Cloud jobs. It is set to the
	// Kueue queue of the job.
	XmcEnvVarQueueName = "XMC_QUEUE_NAME"

	// XmcEnvVarExperimentID is an environment variable set for all XManager Cloud jobs. It is set to the
	// experiment ID of the job.
	XmcEnvVarExperimentID = "XMC_EXPERIMENT_ID"

	// XmcEnvVarWorkUnitID is an environment variable set for all XManager Cloud jobs. It is set to the
	// work unit ID of the job.
	XmcEnvVarWorkUnitID = "XMC_WORK_UNIT_ID"

	// XmcEnvVarExecutionID is an environment variable set for all XManager Cloud jobs. It is set to the
	// execution ID of the job.
	XmcEnvVarExecutionID = "XMC_EXECUTION_ID"

	// XmcEnvVarManagedXprofGCSPath is the environment variable on the actuator that
	// provides the cluster-wide managed-xprof GCS bucket path. It is sourced from the
	// managed-xprof-gcs-config ConfigMap by the Helm chart, and is empty when managed
	// xprof is not configured for the cluster.
	XmcEnvVarManagedXprofGCSPath = "MANAGED_XPROF_GCS_PATH"

	// XmcEnvVarManagedXprofGCSObjectFolder is the environment variable containing the GCS object folder for managed profiling, which is per workunit.
	XmcEnvVarManagedXprofGCSObjectFolder = "XMC_MANAGED_XPROF_GCS_OBJECT_FOLDER"

	// XmcEnvVarManagedXprofDisplayName is the environment variable containing the display name for managed profiling's machine learning run.
	XmcEnvVarManagedXprofDisplayName = "XMC_MANAGED_XPROF_DISPLAY_NAME"

	// XmcEnvVarManagedXprofRunGroup is the environment variable containing the run group for managed profiling's machine learning run.
	XmcEnvVarManagedXprofRunGroup = "XMC_MANAGED_XPROF_RUN_GROUP"

	// MLRunCreationTimeFormat is the timestamp layout used for MLRun workload creation times.
	MLRunCreationTimeFormat = time.RFC3339Nano

	// MLRunIdentifierTimeFormat is the timestamp layout used in the raw MLRun workload identifier before hashing.
	MLRunIdentifierTimeFormat = "20060102-150405"

	// JobSetJobIndexLabel is the label set by JobSet for the job index.
	JobSetJobIndexLabel = "jobset.sigs.k8s.io/job-index"

	// JobSetReplicatedJobNameLabel is the label set by JobSet for the replicated job name.
	JobSetReplicatedJobNameLabel = "jobset.sigs.k8s.io/replicatedjob-name"

	// XmcEnvVarJobIndex is an environment variable set for all XManager Cloud jobs. It is set to the
	// job index.
	XmcEnvVarJobIndex = "XMC_JOB_INDEX"

	// XmcEnvVarJobName is an environment variable set for all XManager Cloud jobs. It is set to the
	// job name.
	XmcEnvVarJobName = "XMC_JOB_NAME"

	// XmcEnvVarMetricsEnabled is an environment variable set for all XManager Cloud jobs. It is set to
	// true if metrics collection is enabled.
	XmcEnvVarMetricsEnabled = "XMC_METRICS_ENABLED"

	// RedisStream is the name of the Redis stream used for task queueing.
	RedisStream = "xmc-tasks-stream"
	// RedisGroup is the name of the Redis consumer group.
	RedisGroup = "actuator-group"
	// RedisDLQ is the name of the Redis dead-letter stream.
	RedisDLQ = "xmc-dlq-stream"
	// RedisPauseStream is the name of the Redis stream used for broadcasting pause scheduling updates.
	RedisPauseStream = "xmc-pause-stream"
	// RedisResumeStream is the name of the Redis stream used for pause scheduling resumption triggers.
	RedisResumeStream = "xmc-scheduling-resume-stream"
	// RedisESSGroup is the name of the Redis consumer group for ESS resumption workers.
	RedisESSGroup = "ess-group"
	// ESSResumptionWorkerID is the prefix for the ID of the ESS resumption worker.
	ESSResumptionWorkerID = "ess-resumption-worker"
	// ESSResumptionCleanupWorkerID is the prefix for the ID of the ESS resumption cleanup worker.
	ESSResumptionCleanupWorkerID = "ess-resumption-cleanup"
	// ActuatorWorkerID is the prefix for the ID of the actuator worker.
	ActuatorWorkerID = "actuator-worker"
	// ActuatorCleanupWorkerID is the prefix for the ID of the Actuator cleanup worker.
	ActuatorCleanupWorkerID = "actuator-cleanup"
	// RedisStudyReconcileStream is the name of the Redis stream carrying study reconcile ticks
	// from the Experiment State Server to the Study Controller.
	RedisStudyReconcileStream = "xmc-study-reconcile-stream"
	// RedisStudyControllerGroup is the name of the Redis consumer group for Study Controller workers.
	RedisStudyControllerGroup = "study-controller-group"
	// RedisStudyReconcileDLQ is the name of the Redis dead-letter stream for study reconcile ticks.
	RedisStudyReconcileDLQ = "xmc-study-reconcile-dlq-stream"
	// StudyControllerWorkerID is the prefix for the ID of the Study Controller worker.
	StudyControllerWorkerID = "study-controller-worker"
	// StudyControllerCleanupWorkerID is the prefix for the ID of the Study Controller cleanup worker.
	StudyControllerCleanupWorkerID = "study-controller-cleanup"

	// RedisMaxRetries is the maximum number of retries for a Redis task.
	RedisMaxRetries = 5
	// RedisProcessTimeout is the timeout for processing a Redis task.
	RedisProcessTimeout = 2 * time.Minute
	// RedisAbandonedMessageTimeout is the timeout for abandoned Redis messages.
	RedisAbandonedMessageTimeout = 5 * time.Minute
	// RedisReadBatchSize is the batch size for reading Redis messages.
	RedisReadBatchSize = 1

	// TPUTopologySelectorKey is the node selector key for GKE TPU topology.
	TPUTopologySelectorKey = "cloud.google.com/gke-tpu-topology"

	// TPUAcceleratorSelectorKey is the node selector key for GKE TPU accelerator type.
	TPUAcceleratorSelectorKey = "cloud.google.com/gke-tpu-accelerator"

	// PathwaysHeadJobName is the default job name for the Pathways head job in a JobSet
	// (see https://github.com/google/pathways-job).
	PathwaysHeadJobName = "pathways-head"

	// DefaultOnFailureRetryLimit is the default backoff limit for jobs with RestartPolicy OnFailure.
	DefaultOnFailureRetryLimit = int32(100)

	// ResourceTypeCPU represents the standard CPU resource identifier.
	ResourceTypeCPU = "cpu"

	// ResourceTypeMemory represents the standard Memory resource identifier.
	ResourceTypeMemory = "memory"

	// ResourceTypeGPU represents the standard GPU resource identifier.
	ResourceTypeGPU = "gpu"

	// ResourceTypeTPU represents the standard TPU resource identifier.
	ResourceTypeTPU = "tpu"

	// NvidiaGPUResourceName is the Kubernetes extended resource name for NVIDIA GPUs.
	NvidiaGPUResourceName = "nvidia.com/gpu"

	// TPUResourceName is the Kubernetes extended resource name for Cloud TPUs.
	TPUResourceName = "google.com/tpu"

	// ComponentSnapshotsAnnotation is the annotation specifying locked component snapshots.
	ComponentSnapshotsAnnotation = "xmc.dev/component-snapshots"
	// ComponentDriftWarningAnnotation is the annotation applied to JobSets when component drift is detected.
	ComponentDriftWarningAnnotation = "xmc.dev/component-drift-warning"

	// Component custom resource metadata and defaults.
	ComponentCRDGroup          = "xmc.dev"
	ComponentCRDVersion        = "v1alpha1"
	ComponentCRDKind           = "Component"
	ComponentCRDResource       = "components"
	DefaultComponentsNamespace = "xmc"
)

// GKEAcceleratorForGPUType returns the GKE accelerator node label for an XManager GPU type
// (GpuResources.type).
func GKEAcceleratorForGPUType(gpuType string) (string, bool) {
	accelerator, ok := gpuTypeToGKEAccelerator[gpuType]
	return accelerator, ok
}

var gpuTypeToGKEAccelerator = map[string]string{
	"P4":         "nvidia-tesla-p4",
	"T4":         "nvidia-tesla-t4",
	"L4_24TH":    "nvidia-l4",
	"P100":       "nvidia-tesla-p100",
	"V100":       "nvidia-tesla-v100",
	"A100":       "nvidia-a100-40gb",
	"A100_80GIB": "nvidia-a100-80gb",
	"H100":       "nvidia-h100-80gb",
	// The NVIDIA H100 80GB Mega GPU, which backs GCP's A3 Mega (a3-megagpu-8g)
	// machine types. Because XManager's client enum only defines H100 (mapped
	// to nvidia-h100-80gb for a3-highgpu-8g), callers targeting A3 Mega pass
	// either the symbolic H100_MEGA key or the GKE accelerator label directly.
	"H100_MEGA":             "nvidia-h100-mega-80gb",
	"nvidia-h100-mega-80gb": "nvidia-h100-mega-80gb",
	"H200":                  "nvidia-h200-141gb",
	"B200":                  "nvidia-b200",
	"GB200":                 "nvidia-gb200",
	// The NVIDIA RTX PRO 6000 Blackwell Server Edition, which backs GCP's G4
	// machine types. XManager's client enum already carries this card as
	// B40_48TH and special-cases it in JobRequirements topology handling, but
	// no entry was ever added here, so requesting one is rejected outright by
	// updateNodeSelector.
	"B40_48TH": "nvidia-rtx-pro-6000",
}
