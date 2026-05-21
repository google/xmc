// Copyright 2025 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//      http://www.apache.org/licenses/LICENSE-2.0
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

	// RedisStream is the name of the Redis stream used for task queueing.
	RedisStream = "xmc-tasks-stream"
	// RedisGroup is the name of the Redis consumer group.
	RedisGroup = "actuator-group"
	// RedisDLQ is the name of the Redis dead-letter stream.
	RedisDLQ = "xmc-dlq-stream"
	// ActuatorWorkerID is the prefix for the ID of the actuator worker.
	ActuatorWorkerID = "actuator-worker"
	// ActuatorCleanupWorkerID is the prefix for the ID of the Actuator cleanup worker.
	ActuatorCleanupWorkerID = "actuator-cleanup"

	// RedisMaxRetries is the maximum number of retries for a Redis task.
	RedisMaxRetries = 5
	// RedisProcessTimeout is the timeout for processing a Redis task.
	RedisProcessTimeout = 2 * time.Minute
	// RedisAbandonedMessageTimeout is the timeout for abandoned Redis messages.
	RedisAbandonedMessageTimeout = 5 * time.Minute
	// RedisReadBatchSize is the batch size for reading Redis messages.
	RedisReadBatchSize = 1
)
