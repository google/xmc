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
package jobset

import (
	"context"
	"fmt"
	"net/url"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	fmpb "protobuf/field_mask_go_proto"
	timestamppb "protobuf/timestamp_go_proto"
	spb "xmanager_cloud/experiment_state_server/proto/messages_go_proto"
	"xmanager_cloud/kubernetes/common/jobsetutil"
	"xmanager_cloud/logging/logging"
	storagepkg "xmanager_cloud/storage/storage"

	v1alpha2 "sigs.k8s.io/jobset/api/jobset/v1alpha2"
)

func calculateTotalReplicas(jobSet *v1alpha2.JobSet) int32 {
	var totalReplicas int32
	for _, rJob := range jobSet.Spec.ReplicatedJobs {
		totalReplicas += rJob.Replicas
	}
	return totalReplicas
}

// JobTotals holds aggregate replica statistics across all jobs in a JobSet.
type JobTotals struct {
	Succeeded int32
	Failed    int32
	Active    int32
	Ready     int32
	Replicas  int32
}

func calculateJobTotals(jobSet *v1alpha2.JobSet) JobTotals {
	var totals JobTotals
	for _, rjs := range jobSet.Status.ReplicatedJobsStatus {
		totals.Succeeded += rjs.Succeeded
		totals.Failed += rjs.Failed
		totals.Active += rjs.Active
		totals.Ready += rjs.Ready
	}
	totals.Replicas = calculateTotalReplicas(jobSet)
	return totals
}

func generateStructuredMessage(ctx context.Context, jobSet *v1alpha2.JobSet, state spb.WorkUnitStatus_WorkUnitState, project, location, clusterName, podErrorMsg, startupWarningMsg string) *spb.StructuredMessage {
	ctx = logging.WithJobSetName(ctx, jobSet.Name, jobSet.Labels)

	totals := calculateJobTotals(jobSet)
	msg, msgSeverity, msgSource := buildStructuredMessageContent(jobSet, totals, state, podErrorMsg, startupWarningMsg)
	debugLink := buildDebugLink(ctx, jobSet, state, project, location, clusterName)

	logging.Debug(ctx, fmt.Sprintf("Structured status message: %q, debugLink: %q", msg, debugLink), "structuredMessage", msg, "debugLink", debugLink)

	return &spb.StructuredMessage{
		UserFacingText: msg,
		UserFacingUrl: &spb.Url{
			Url: debugLink,
		},
		Severity:   msgSeverity,
		Source:     msgSource,
		CreateTime: timestamppb.New(time.Now()),
	}
}

func buildStructuredMessageContent(jobSet *v1alpha2.JobSet, totals JobTotals, state spb.WorkUnitStatus_WorkUnitState, podErrorMsg, startupWarningMsg string) (string, spb.StructuredMessage_Severity, spb.StructuredMessage_Source) {
	totalSucceeded, totalFailed, totalActive, totalReplicas := totals.Succeeded, totals.Failed, totals.Active, totals.Replicas
	var msg string
	msgSeverity := spb.StructuredMessage_DEBUG
	msgSource := spb.StructuredMessage_KUBERNETES

	switch state {
	case spb.WorkUnitStatus_FAILED:
		msg = "Work unit failed."
		if podErrorMsg != "" {
			msg = podErrorMsg
		} else if condition := jobsetutil.FindConditionWithType(jobSet, v1alpha2.JobSetFailed); condition != nil {
			msg = fmt.Sprintf("Work unit failed: %s (Reason: %s)", condition.Message, condition.Reason)
		}
		msgSeverity = spb.StructuredMessage_ERROR
	case spb.WorkUnitStatus_COMPLETED:
		msg = "Work unit completed successfully."
		if condition := jobsetutil.FindConditionWithType(jobSet, v1alpha2.JobSetCompleted); condition != nil && condition.Message != "" {
			msg = fmt.Sprintf("Work unit completed successfully with message: %s", condition.Message)
		}
	case spb.WorkUnitStatus_RUNNING, spb.WorkUnitStatus_STARTING:
		msg, msgSeverity = buildRunningOrStartingMessage(totals, state, startupWarningMsg)
	case spb.WorkUnitStatus_PARTIALLY_FAILED:
		msg = fmt.Sprintf("Work unit is partially failed. %d failed, %d active, %d succeeded out of %d job(s).", totalFailed, totalActive, totalSucceeded, totalReplicas)
		msgSeverity = spb.StructuredMessage_WARNING
	case spb.WorkUnitStatus_SCHEDULING:
		msg = "Work unit is waiting for resources from the scheduler."
		if condition := jobsetutil.FindConditionWithType(jobSet, v1alpha2.JobSetStartupPolicyInProgress); condition != nil && condition.Message != "" {
			msg = fmt.Sprintf("Work unit is scheduling jobs: %s", condition.Message)
		}
		msgSource = spb.StructuredMessage_SCHEDULER
	case spb.WorkUnitStatus_STOPPED:
		msg = "Work unit has been fully stopped."
	case spb.WorkUnitStatus_WORK_UNIT_STATE_UNSPECIFIED:
		msg = "Work unit state is unspecified. This should not happen."
	default:
		msg = fmt.Sprintf("Work unit state: %s", state.String())
	}
	return msg, msgSeverity, msgSource
}

func buildRunningOrStartingMessage(totals JobTotals, state spb.WorkUnitStatus_WorkUnitState, startupWarningMsg string) (string, spb.StructuredMessage_Severity) {
	if state == spb.WorkUnitStatus_RUNNING {
		if totals.Ready == totals.Active {
			return fmt.Sprintf("Work unit is running. %d/%d job(s) active and ready.", totals.Active, totals.Replicas), spb.StructuredMessage_DEBUG
		}
		return fmt.Sprintf("Work unit is running. %d/%d job(s) active, %d ready.", totals.Active, totals.Replicas, totals.Ready), spb.StructuredMessage_DEBUG
	}
	msg := fmt.Sprintf("Work unit is starting. %d/%d job(s) active, %d ready.", totals.Active, totals.Replicas, totals.Ready)
	if startupWarningMsg != "" {
		return startupWarningMsg, spb.StructuredMessage_WARNING
	}
	return msg, spb.StructuredMessage_DEBUG
}

func buildDebugLink(ctx context.Context, jobSet *v1alpha2.JobSet, state spb.WorkUnitStatus_WorkUnitState, project, location, clusterName string) string {
	if state != spb.WorkUnitStatus_FAILED && state != spb.WorkUnitStatus_PARTIALLY_FAILED {
		return ""
	}
	if project == "" || location == "" || clusterName == "" {
		logging.Warn(ctx, "Cannot build debug link for JobSet", "namespace", jobSet.Namespace, "jobSet", jobSet.Name, "project", project, "location", location, "clusterName", clusterName)
		return ""
	}
	return fmt.Sprintf(
		"https://console.cloud.google.com/kubernetes/aiml/jobset/%s/GKE_CLUSTER/%s/%s/%s/%s/overview?project=%s",
		url.PathEscape(project),
		url.PathEscape(location),
		url.PathEscape(clusterName),
		url.PathEscape(jobSet.Namespace),
		url.PathEscape(jobSet.Name),
		url.QueryEscape(project),
	)
}

// ToWorkUnitState converts a JobSet status to a WorkUnitState enum value.
func ToWorkUnitState(jobSet *v1alpha2.JobSet) spb.WorkUnitStatus_WorkUnitState {
	// 1. Terminal Conditions (Highest Priority)
	if jobsetutil.HasCondition(jobSet, v1alpha2.JobSetCompleted, metav1.ConditionTrue) {
		return spb.WorkUnitStatus_COMPLETED
	}
	if jobsetutil.HasCondition(jobSet, v1alpha2.JobSetFailed, metav1.ConditionTrue) {
		return spb.WorkUnitStatus_FAILED
	}
	if jobSet.GetDeletionTimestamp() != nil {
		return spb.WorkUnitStatus_STOPPED
	}

	// 2. Initial / Queued Check
	isStatusEmpty := len(jobSet.Status.Conditions) == 0 && len(jobSet.Status.ReplicatedJobsStatus) == 0
	if isStatusEmpty {
		return spb.WorkUnitStatus_SCHEDULING
	}

	totals := calculateJobTotals(jobSet)
	totalSucceeded, totalFailed, totalActive, totalReady := totals.Succeeded, totals.Failed, totals.Active, totals.Ready

	// 3. "Alive" Check (Priority: High)
	// If any pods are Ready, the WorkUnit is serving/alive. This prevents the "Active=0 vs Ready=1" flicker.
	if totalReady > 0 {
		return spb.WorkUnitStatus_RUNNING
	}

	// 4. Activity Check (Priority: Medium)
	// If we have active pods (working/retrying), we are running/starting.
	// We check this BEFORE `Failed` to ensure retrying jobs don't look broken.
	if totalActive > 0 {
		if totalSucceeded > 0 {
			return spb.WorkUnitStatus_RUNNING
		}
		return spb.WorkUnitStatus_STARTING
	}

	// 5. Inactive Checks (Active=0, Ready=0)

	// CRITICAL ORDERING:
	// If the job stopped(Active=0), we MUST check for failures first.
	// If all target replicated jobs succeeded, ignore non-target worker failures during teardown.
	if totalFailed > 0 && !targetSuccessPolicySatisfied(jobSet) {
		return spb.WorkUnitStatus_PARTIALLY_FAILED
	}

	// "Success Gap": The job stopped, had NO failures (or target success policy satisfied), but has successes.
	// This is the only safe time to return RUNNING based on Success count alone.
	if totalSucceeded > 0 {
		return spb.WorkUnitStatus_RUNNING
	}

	// 6. Fallback (Active=0, Ready=0, Failed=0, Succeeded=0)
	return spb.WorkUnitStatus_SCHEDULING
}

// targetSuccessPolicySatisfied returns true if the JobSet defines TargetReplicatedJobs in its
// SuccessPolicy and the target replicated jobs have succeeded according to SuccessPolicy.Operator
// (OperatorAll requires all target replicated jobs to achieve Succeeded >= Replicas, whereas
// OperatorAny requires at least one target replicated job to achieve Succeeded >= Replicas).
func targetSuccessPolicySatisfied(jobSet *v1alpha2.JobSet) bool {
	if jobSet.Spec.SuccessPolicy == nil || len(jobSet.Spec.SuccessPolicy.TargetReplicatedJobs) == 0 {
		return false
	}
	expectedReplicas := make(map[string]int32, len(jobSet.Spec.ReplicatedJobs))
	for _, rj := range jobSet.Spec.ReplicatedJobs {
		expectedReplicas[rj.Name] = rj.Replicas
	}
	succeededJobs := make(map[string]bool, len(jobSet.Status.ReplicatedJobsStatus))
	for _, rjs := range jobSet.Status.ReplicatedJobsStatus {
		expected, ok := expectedReplicas[rjs.Name]
		if ok && expected > 0 && rjs.Succeeded >= expected {
			succeededJobs[rjs.Name] = true
		}
	}
	if jobSet.Spec.SuccessPolicy.Operator == v1alpha2.OperatorAny {
		for _, target := range jobSet.Spec.SuccessPolicy.TargetReplicatedJobs {
			if succeededJobs[target] {
				return true
			}
		}
		return false
	}
	for _, target := range jobSet.Spec.SuccessPolicy.TargetReplicatedJobs {
		if !succeededJobs[target] {
			return false
		}
	}
	return true
}

// ConvertJobSetToWorkUnitExecutionUpdate converts a JobSet to a WorkUnitExecutionUpdate.
func ConvertJobSetToWorkUnitExecutionUpdate(ctx context.Context, jobSet *v1alpha2.JobSet, wid int64, eid int64, project, location, clusterName, podErrorMsg, startupWarningMsg string) (*storagepkg.WorkUnitExecutionUpdate, error) {
	ctx = logging.WithJobSetName(ctx, jobSet.Name, jobSet.Labels)

	var state spb.WorkUnitStatus_WorkUnitState
	if podErrorMsg != "" {
		state = spb.WorkUnitStatus_FAILED
	} else {
		state = ToWorkUnitState(jobSet)
	}

	// We intentionally log the state so that the logs are clear.
	logging.Debug(ctx, fmt.Sprintf("JobSet state: %s", state.String()), "state", state.String(), "namespace", jobSet.ObjectMeta.Namespace)
	time := time.Now()
	metadata := &spb.WorkUnitExecutionMetadata{
		ManagementCluster: &spb.ClusterInfo{
			Project:     project,
			Location:    location,
			ClusterName: clusterName,
		},
		Type:           spb.WorkUnitExecutionMetadata_JOB_SET,
		ResourceName:   jobSet.ObjectMeta.Name,
		Namespace:      jobSet.ObjectMeta.Namespace,
		ServiceAccount: getServiceAccountName(jobSet),
	}

	statusMessage := generateStructuredMessage(ctx, jobSet, state, project, location, clusterName, podErrorMsg, startupWarningMsg)

	// If we are about to propagate an error+ message, defer to any existing message. This will
	// prevent us from continually clobbering log monitor error messages with job state messages.
	deferToExistingMessage := false
	if statusMessage.GetSeverity() >= spb.StructuredMessage_ERROR {
		deferToExistingMessage = true
	}

	return &storagepkg.WorkUnitExecutionUpdate{
		WID:                    wid,
		EID:                    eid,
		DeferToExistingMessage: deferToExistingMessage,
		WorkUnitExecution: &spb.WorkUnitExecution{
			Status: &spb.WorkUnitStatus{
				State: state,
				// This update time is required for the state update to be correctly propagated. The storage
				// layer will reject the update if it's older than the current state update time, or zero.
				StateUpdateTime: timestamppb.New(time),
				SystemMessage:   statusMessage,
				DetailedStatus: &spb.ExecutableStatus{
					Kind: &spb.ExecutableStatus_JobsetStatus{
						JobsetStatus: convertK8sJobSetStatusToProto(jobSet.Status),
					},
				},
			},
			Metadata: metadata,
		},
		UpdateMask: &fmpb.FieldMask{
			Paths: []string{
				"status.state",
				"status.detailed_status",
				"status.system_message",
				"metadata.management_cluster",
				"metadata.type",
				"metadata.resource_name",
				"metadata.namespace",
				"metadata.service_account",
			},
		},
	}, nil
}

func convertK8sJobSetStatusToProto(k8sStatus v1alpha2.JobSetStatus) *spb.JobSetStatus {
	jsStatus := &spb.JobSetStatus{
		Restarts: int64(k8sStatus.Restarts),
	}
	for _, condition := range k8sStatus.Conditions {
		jsStatus.Conditions = append(jsStatus.Conditions, convertK8sConditionToProto(condition))
	}
	for _, rjs := range k8sStatus.ReplicatedJobsStatus {
		jsStatus.ReplicatedJobsStatus = append(jsStatus.ReplicatedJobsStatus, convertK8sReplicatedJobStatusToProto(rjs))
	}
	return jsStatus
}

func convertK8sConditionToProto(k8sCond metav1.Condition) *spb.Condition {
	return &spb.Condition{
		Type:       k8sCond.Type,
		Status:     string(k8sCond.Status),
		UpdateTime: timestamppb.New(k8sCond.LastTransitionTime.Time),
		Reason:     k8sCond.Reason,
		Message:    k8sCond.Message,
	}
}

func getServiceAccountName(jobSet *v1alpha2.JobSet) string {
	if jobSet != nil && len(jobSet.Spec.ReplicatedJobs) > 0 {
		// Assuming the ServiceAccountName is consistent across all ReplicatedJobs if specified.
		return jobSet.Spec.ReplicatedJobs[0].Template.Spec.Template.Spec.ServiceAccountName
	}
	return ""
}

// createStoppedUpdate creates a WorkUnitExecutionUpdate with state STOPPED.
func createStoppedUpdate(wid int64, eid int64) *storagepkg.WorkUnitExecutionUpdate {
	time := time.Now()
	return &storagepkg.WorkUnitExecutionUpdate{
		WID: wid,
		EID: eid,
		WorkUnitExecution: &spb.WorkUnitExecution{
			Status: &spb.WorkUnitStatus{
				State:           spb.WorkUnitStatus_STOPPED,
				StateUpdateTime: timestamppb.New(time),
				SystemMessage: &spb.StructuredMessage{
					UserFacingText: "Work unit has been fully stopped.",
					Severity:       spb.StructuredMessage_DEBUG,
					Source:         spb.StructuredMessage_KUBERNETES,
					CreateTime:     timestamppb.New(time),
				},
			},
		},
		UpdateMask: &fmpb.FieldMask{
			Paths: []string{
				"status.state",
				"status.system_message",
			},
		},
	}
}

func convertK8sReplicatedJobStatusToProto(k8sRJS v1alpha2.ReplicatedJobStatus) *spb.ReplicatedJobStatus {
	return &spb.ReplicatedJobStatus{
		Name:      k8sRJS.Name,
		Ready:     int64(k8sRJS.Ready),
		Succeeded: int64(k8sRJS.Succeeded),
		Active:    int64(k8sRJS.Active),
		Failed:    int64(k8sRJS.Failed),
	}
}

// setMLRunURL records the MLRun URL on the update's execution metadata and
// marks the field in the update mask. It instantiates the metadata message when
// absent so a not-yet-initialized metadata does not silently drop the URL. It is
// a no-op when there is no URL or no execution to update.
func setMLRunURL(update *storagepkg.WorkUnitExecutionUpdate, mlRunURL string) {
	if mlRunURL == "" || update == nil || update.WorkUnitExecution == nil {
		return
	}
	if update.WorkUnitExecution.Metadata == nil {
		update.WorkUnitExecution.Metadata = &spb.WorkUnitExecutionMetadata{}
	}
	update.WorkUnitExecution.Metadata.MlrunUrl = mlRunURL
	if update.UpdateMask != nil {
		update.UpdateMask.Paths = append(update.UpdateMask.Paths, "metadata.mlrun_url")
	}
}
