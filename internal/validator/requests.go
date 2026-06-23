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
// Package validator provides request validation for the XManager Cloud services.
package validator

import (
	"fmt"
	"regexp"
	"strings"

	"task/go/status"
	essapipb "xmanager_cloud/experiment_state_server/proto/api_go_proto"
	essmsgpb "xmanager_cloud/experiment_state_server/proto/messages_go_proto"
	xidapipb "xmanager_cloud/xid_service/proto/api_go_proto"
	xidmessagespb "xmanager_cloud/xid_service/proto/messages_go_proto"
)

const (
	// defaultPageSize is the default page size for list requests.
	defaultPageSize = 10
	// maxQueueNameLen is the maximum allowed length of a queue name.
	maxQueueNameLen = 253
	// These are just some arbitrary limits for sanity. We don't expect users to hit these limits,
	// and if they are, they're probably doing something weird or wrong anyways.
	// maxTags defines the maximum number of tags an experiment can have.
	maxTags = 1000
	// maxTagLen defines the maximum character length of a tag string.
	maxTagLen = 51200 // 50 KiB
	// maxNotesLen defines the maximum character length of the notes field.
	maxNotesLen = 51200 // 50 KiB
	// maxURLs defines the maximum number of URLs an experiment can have.
	maxURLs = 1000
	// maxURLLen defines the maximum character length of a URL string.
	maxURLLen = 51200 // 50 KiB
)

var (
	// jobNameRegex matches valid Kubernetes job names (RFC 1123 DNS Label).
	jobNameRegex = regexp.MustCompile(`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`)
)

func validateRoles(roles []*xidmessagespb.XidAcls_Role) error {
	for _, role := range roles {
		if role.GetPrincipal() == nil {
			return fmt.Errorf("role cannot be nil: %w", status.ErrInvalidArgument)
		}
	}
	return nil
}

// ValidateCreateXidRequest validates the given CreateXidEntryRequest.
func ValidateCreateXidRequest(in *xidapipb.CreateXidEntryRequest) error {
	if in == nil {
		return fmt.Errorf("request cannot be nil: %w", status.ErrInvalidArgument)
	}
	if in.GetXidEntry() == nil {
		return fmt.Errorf("xid_entry cannot be nil: %w", status.ErrInvalidArgument)
	}
	if in.GetXidEntry().GetName() != "" {
		return fmt.Errorf("xid_entry.name must be empty for CreateXidEntryRequest: %w", status.ErrInvalidArgument)
	}
	if in.GetXidEntry().GetOrigin() == nil {
		return fmt.Errorf("xid_entry.origin cannot be nil: %w", status.ErrInvalidArgument)
	}
	acls := in.GetXidEntry().GetAcls()
	if in.GetXidEntry().GetParentXid() != "" {
		_, err := ValidateXIDName(in.GetXidEntry().GetParentXid())
		if err != nil {
			return fmt.Errorf("xid_entry.parent_xid must be in the format xids/{xid}: %w", err)
		}
	} else {
		if in.GetXidEntry().GetAcls() == nil {
			return fmt.Errorf("xid_entry.acls cannot be nil: %w", status.ErrInvalidArgument)
		}
		if len(acls.GetOwners()) == 0 {
			return fmt.Errorf("xid_entry.acls.owners cannot be empty for an XID with no parent: %w", status.ErrInvalidArgument)
		}
	}
	if acls != nil {
		if err := validateRoles(acls.GetOwners()); err != nil {
			return err
		}
		if err := validateRoles(acls.GetWriters()); err != nil {
			return err
		}
		if err := validateRoles(acls.GetReaders()); err != nil {
			return err
		}
	}
	return nil
}

// ValidateCreateExperimentRequest validates the given CreateExperimentRequest.
func ValidateCreateExperimentRequest(in *essapipb.CreateExperimentRequest) error {
	if in == nil {
		return fmt.Errorf("request cannot be nil: %w", status.ErrInvalidArgument)
	}
	if in.GetExperiment() == nil {
		return fmt.Errorf("experiment cannot be nil: %w", status.ErrInvalidArgument)
	}
	exp := in.GetExperiment()
	if exp.GetName() != "" {
		return fmt.Errorf("experiment.name must be empty for CreateExperimentRequest: %w", status.ErrInvalidArgument)
	}
	if exp.GetCreator() != "" {
		return fmt.Errorf("experiment.creator must be empty for CreateExperimentRequest: %w", status.ErrInvalidArgument)
	}
	if strings.TrimSpace(exp.GetTitle()) == "" {
		return fmt.Errorf("experiment.title must be set and not only contain whitespace: %w", status.ErrInvalidArgument)
	}
	if exp.GetCreateTime() != nil {
		return fmt.Errorf("experiment.create_time must be empty for CreateExperimentRequest: %w", status.ErrInvalidArgument)
	}
	if exp.GetUpdateTime() != nil {
		return fmt.Errorf("experiment.update_time must be empty for CreateExperimentRequest: %w", status.ErrInvalidArgument)
	}
	if exp.GetStatus() != nil {
		return fmt.Errorf("experiment.status must be empty for CreateExperimentRequest: %w", status.ErrInvalidArgument)
	}
	if len(exp.GetTags()) > maxTags {
		return fmt.Errorf("experiment.tags cannot contain more than %d tags: %w", maxTags, status.ErrInvalidArgument)
	}
	for _, tag := range exp.GetTags() {
		if len(tag) > maxTagLen {
			return fmt.Errorf("experiment.tags cannot contain tags longer than %d characters: %w", maxTagLen, status.ErrInvalidArgument)
		}
	}
	if len(exp.GetNotes()) > maxNotesLen {
		return fmt.Errorf("experiment.notes cannot be longer than %d characters: %w", maxNotesLen, status.ErrInvalidArgument)
	}
	if len(exp.GetUrls()) > maxURLs {
		return fmt.Errorf("experiment.urls cannot contain more than %d urls: %w", maxURLs, status.ErrInvalidArgument)
	}
	for _, url := range exp.GetUrls() {
		if len(url.GetUrl()) > maxURLLen {
			return fmt.Errorf("experiment.urls cannot contain urls longer than %d characters: %w", maxURLLen, status.ErrInvalidArgument)
		}
	}
	if exp.Acls == nil {
		return fmt.Errorf("experiment.acls cannot be nil: %w", status.ErrInvalidArgument)
	}
	return nil
}

// ValidateUpdateExperimentRequest validates updates in UpdateExperimentRequest.
func ValidateUpdateExperimentRequest(req *essapipb.UpdateExperimentRequest) error {
	for _, path := range req.GetUpdateMask().GetPaths() {
		if path == "title" {
			if strings.TrimSpace(req.GetExperiment().GetTitle()) == "" {
				return fmt.Errorf("experiment title cannot be empty or contain only whitespaces: %w", status.ErrInvalidArgument)
			}
		}
	}
	return nil
}

// FillExperimentNullValues fills in any null values in the Experiment proto with default values.
// This is necessary for fields that are optional in the proto definition, but are actually
// required in the database schema.
func FillExperimentNullValues(exp *essmsgpb.Experiment) *essmsgpb.Experiment {
	if exp.GetUrls() == nil {
		exp.Urls = []*essmsgpb.Url{}
	}
	if exp.GetTags() == nil {
		exp.Tags = []string{}
	}
	if exp.Priority == essmsgpb.Priority_PRIORITY_UNSPECIFIED {
		exp.Priority = essmsgpb.Priority_LOW
	}
	return exp
}

// FillWorkUnitNullValues fills in any null values in the WorkUnit proto with default values.
// This is necessary for fields that are optional in the proto definition, but are actually
// required in the database schema.
func FillWorkUnitNullValues(wu *essmsgpb.WorkUnit) *essmsgpb.WorkUnit {
	if wu.GetUrls() == nil {
		wu.Urls = []*essmsgpb.Url{}
	}
	if wu.GetTags() == nil {
		wu.Tags = []string{}
	}
	if wu.Priority == essmsgpb.Priority_PRIORITY_UNSPECIFIED {
		wu.Priority = essmsgpb.Priority_LOW
	}
	return wu
}

// FillArtifactNullValues fills in any null values in the Artifact proto with default values.
// This is necessary for fields that are optional in the proto definition, but are actually
// required in the database schema.
func FillArtifactNullValues(art *essmsgpb.Artifact) *essmsgpb.Artifact {
	if art.GetTags() == nil {
		art.Tags = []string{}
	}
	if art.Priority == 0 && art.Url != nil && art.Url.Priority != 0 {
		art.Priority = art.Url.Priority
	} else if art.Priority != 0 && art.Url != nil && art.Url.Priority == 0 {
		art.Url.Priority = art.Priority
	}
	return art
}

// FillXidEntryNullValues fills in any null values in the XidEntry proto with default values.
// This is necessary for fields that are optional in the proto definition, but are actually
// required in the database schema.
func FillXidEntryNullValues(xidEntry *xidmessagespb.XidEntry) *xidmessagespb.XidEntry {
	if xidEntry.GetAcls() == nil {
		xidEntry.Acls = &xidmessagespb.XidAcls{}
	}
	return xidEntry
}

// ValidateCreateWorkUnitRequest validates the given CreateWorkUnitRequest.
func ValidateCreateWorkUnitRequest(in *essapipb.CreateWorkUnitRequest) error {
	if in == nil {
		return fmt.Errorf("request cannot be nil: %w", status.ErrInvalidArgument)
	}
	_, err := ValidateExperimentName(in.GetParent())
	if err != nil {
		return fmt.Errorf("parent must be in the format experiments/{xid}: %w", err)
	}
	if in.GetWorkUnit() == nil {
		return fmt.Errorf("work_unit cannot be nil: %w", status.ErrInvalidArgument)
	}
	wu := in.GetWorkUnit()
	if wu.GetName() != "" {
		return fmt.Errorf("work_unit.name must be empty for CreateWorkUnitRequest: %w", status.ErrInvalidArgument)
	}
	if wu.GetCreateTime() != nil {
		return fmt.Errorf("work_unit.create_time must be empty for CreateWorkUnitRequest: %w", status.ErrInvalidArgument)
	}
	if wu.GetUpdateTime() != nil {
		return fmt.Errorf("work_unit.update_time must be empty for CreateWorkUnitRequest: %w", status.ErrInvalidArgument)
	}
	if wu.GetCurrentWorkUnitExecution() != nil {
		return fmt.Errorf("work_unit.current_work_unit_execution must be empty for CreateWorkUnitRequest: %w", status.ErrInvalidArgument)
	}
	if len(wu.GetTags()) > maxTags {
		return fmt.Errorf("work_unit.tags cannot contain more than %d tags: %w", maxTags, status.ErrInvalidArgument)
	}
	for _, tag := range wu.GetTags() {
		if len(tag) > maxTagLen {
			return fmt.Errorf("work_unit.tags cannot contain tags longer than %d characters: %w", maxTagLen, status.ErrInvalidArgument)
		}
	}
	if len(wu.GetNotes()) > maxNotesLen {
		return fmt.Errorf("work_unit.notes cannot be longer than %d characters: %w", maxNotesLen, status.ErrInvalidArgument)
	}
	if len(wu.GetUrls()) > maxURLs {
		return fmt.Errorf("work_unit.urls cannot contain more than %d urls: %w", maxURLs, status.ErrInvalidArgument)
	}
	for _, url := range wu.GetUrls() {
		if len(url.GetUrl()) > maxURLLen {
			return fmt.Errorf("work_unit.urls cannot contain urls longer than %d characters: %w", maxURLLen, status.ErrInvalidArgument)
		}
	}
	if wu.GetExecutable() == nil {
		return fmt.Errorf("work_unit.executable cannot be nil: %w", status.ErrInvalidArgument)
	}
	if wu.GetWorkUnitType() == essmsgpb.WorkUnit_WORK_UNIT_TYPE_UNSPECIFIED {
		return fmt.Errorf("work_unit.work_unit_type cannot be empty: %w", status.ErrInvalidArgument)
	}
	if wu.GetExecutable().GetSchedulingConstraints() == nil {
		return fmt.Errorf("work_unit.executable.scheduling_constraints cannot be nil: %w", status.ErrInvalidArgument)
	}
	if wu.GetExecutable().GetJobset() == nil {
		return fmt.Errorf("work_unit.executable.jobset cannot be nil: %w", status.ErrInvalidArgument)
	}
	// This is a limitation of Kueue; see
	// https://github.com/kubernetes-sigs/kueue/blob/bacde9cdbc87bef550bb559c5b81c8c7d001c58a/apis/kueue/v1beta1/workload_types.go#L42
	if len(wu.GetExecutable().GetJobset().Jobs) < 1 {
		return fmt.Errorf("work_unit.executable.jobset.jobs cannot contain less than 1 job: %w", status.ErrInvalidArgument)
	}
	if len(wu.GetExecutable().GetJobset().Jobs) > 8 {
		return fmt.Errorf("work_unit.executable.jobset.jobs cannot contain more than 8 jobs: %w", status.ErrInvalidArgument)
	}
	jobNames := make(map[string]bool)
	for _, job := range wu.GetExecutable().GetJobset().Jobs {
		if job.GetReplicas() < 1 {
			return fmt.Errorf("work_unit.executable.jobset.jobs.replicas must be greater than 0: %w", status.ErrInvalidArgument)
		}
		if job.Spec == nil {
			return fmt.Errorf("work_unit.executable.jobset.jobs.spec cannot be nil: %w", status.ErrInvalidArgument)
		}
		if job.Name == "" {
			return fmt.Errorf("work_unit.executable.jobset.jobs.name cannot be empty: %w", status.ErrInvalidArgument)
		}
		if len(job.Name) > 63 {
			return fmt.Errorf("work_unit.executable.jobset.jobs.name cannot be longer than 63 characters: %w", status.ErrInvalidArgument)
		}
		if !jobNameRegex.MatchString(job.Name) {
			return fmt.Errorf("work_unit.executable.jobset.jobs.name must be a valid DNS label (RFC 1123): %w", status.ErrInvalidArgument)
		}
		if jobNames[job.Name] {
			return fmt.Errorf("work_unit.executable.jobset.jobs.name must be unique: %w", status.ErrInvalidArgument)
		}
		if dep := job.GetDependencyOrder(); dep != nil {
			if dep.GetJobName() == "" {
				return fmt.Errorf("work_unit.executable.jobset.jobs.dependency_order.job_name cannot be empty: %w", status.ErrInvalidArgument)
			}
			if dep.GetJobName() == job.Name {
				return fmt.Errorf("work_unit.executable.jobset.jobs.dependency_order.job_name cannot be the same as the job name: %w", status.ErrInvalidArgument)
			}
			if !jobNames[dep.GetJobName()] {
				return fmt.Errorf("work_unit.executable.jobset.jobs.dependency_order.job_name %q must be defined before the dependent job: %w", dep.GetJobName(), status.ErrInvalidArgument)
			}
			if dep.GetJobDependencyState() == essmsgpb.KubernetesJob_DependencyOrder_JOB_DEPENDENCY_STATE_UNSPECIFIED {
				return fmt.Errorf("work_unit.executable.jobset.jobs.dependency_order.job_dependency_state cannot be unspecified: %w", status.ErrInvalidArgument)
			}
		}
		jobNames[job.Name] = true

		if job.Spec.DockerImage == "" {
			return fmt.Errorf("work_unit.executable.jobset.jobs.spec.docker_image cannot be empty: %w", status.ErrInvalidArgument)
		}
		if job.Spec.Resources == nil {
			return fmt.Errorf("work_unit.executable.jobset.jobs.spec.resources cannot be nil: %w", status.ErrInvalidArgument)
		}
		if job.Spec.Resources.GetResources() == nil {
			return fmt.Errorf("work_unit.executable.jobset.jobs.spec.resources.resources cannot be nil: %w", status.ErrInvalidArgument)
		}
		if job.Spec.Resources.GetCpuResources() != nil {
			if job.Spec.Resources.GetCpuResources().GetCpuMillicores() < 1 {
				return fmt.Errorf("work_unit.executable.jobset.jobs.spec.resources.cpu_resources.cpu_millicores must be greater than 0: %w", status.ErrInvalidArgument)
			}
			if job.Spec.Resources.GetCpuResources().GetMemoryBytes() < 1 {
				return fmt.Errorf("work_unit.executable.jobset.jobs.spec.resources.cpu_resources.memory_bytes must be greater than 0: %w", status.ErrInvalidArgument)
			}
		}
		if job.Spec.Resources.GetGpuResources() != nil {
			if job.Spec.Resources.GetGpuResources().GetType() == "" {
				return fmt.Errorf("work_unit.executable.jobset.jobs.spec.resources.gpu_resources.type cannot be empty: %w", status.ErrInvalidArgument)
			}
			if job.Spec.Resources.GetGpuResources().GetCount() < 1 {
				return fmt.Errorf("work_unit.executable.jobset.jobs.spec.resources.gpu_resources.count must be greater than 0: %w", status.ErrInvalidArgument)
			}
		}
		if job.Spec.Resources.GetTpuResources() != nil {
			if job.Spec.Resources.GetTpuResources().GetType() == "" {
				return fmt.Errorf("work_unit.executable.jobset.jobs.spec.resources.tpu_resources.type cannot be empty: %w", status.ErrInvalidArgument)
			}
			if job.Spec.Resources.GetTpuResources().GetTopology() == "" {
				return fmt.Errorf("work_unit.executable.jobset.jobs.spec.resources.tpu_resources.topology cannot be empty: %w", status.ErrInvalidArgument)
			}
		}
	}
	if queueName := wu.GetExecutable().GetSchedulingConstraints().GetQueueName(); queueName == "" {
		return fmt.Errorf("work_unit.executable.scheduling_constraints.queue_name cannot be empty: %w", status.ErrInvalidArgument)
	} else if len(queueName) > maxQueueNameLen {
		return fmt.Errorf("work_unit.executable.scheduling_constraints.queue_name cannot be longer than %d characters: %w", maxQueueNameLen, status.ErrInvalidArgument)
	}
	return nil
}

// ValidateCreateArtifactRequest validates the given CreateArtifactRequest.
func ValidateCreateArtifactRequest(in *essapipb.CreateArtifactRequest) error {
	if in == nil {
		return fmt.Errorf("request cannot be nil: %w", status.ErrInvalidArgument)
	}

	_, _, err := ValidateArtifactParent(in.GetParent())
	if err != nil {
		return fmt.Errorf("parent must be in the format experiments/{xid} or experiments/{xid}/workUnits/{wid}: %w", err)
	}

	if in.GetArtifact() == nil {
		return fmt.Errorf("artifact cannot be nil: %w", status.ErrInvalidArgument)
	}

	art := in.GetArtifact()
	if art.GetName() != "" {
		return fmt.Errorf("artifact.name must be empty for CreateArtifactRequest: %w", status.ErrInvalidArgument)
	}
	if art.GetParent() != "" {
		return fmt.Errorf("artifact.parent must be empty for CreateArtifactRequest: %w", status.ErrInvalidArgument)
	}
	if art.GetCreateTime() != nil {
		return fmt.Errorf("artifact.create_time must be empty for CreateArtifactRequest: %w", status.ErrInvalidArgument)
	}
	if art.GetUpdateTime() != nil {
		return fmt.Errorf("artifact.update_time must be empty for CreateArtifactRequest: %w", status.ErrInvalidArgument)
	}
	if art.GetUrl().GetUrl() == "" {
		return fmt.Errorf("artifact.url.url must be set: %w", status.ErrInvalidArgument)
	}
	if len(art.GetUrl().GetUrl()) > maxURLLen {
		return fmt.Errorf("artifact.url.url cannot be longer than %d characters: %w", maxURLLen, status.ErrInvalidArgument)
	}
	if len(art.GetTags()) > maxTags {
		return fmt.Errorf("artifact.tags cannot contain more than %d tags: %w", maxTags, status.ErrInvalidArgument)
	}
	for _, tag := range art.GetTags() {
		if len(tag) > maxTagLen {
			return fmt.Errorf("artifact.tags cannot contain tags longer than %d characters: %w", maxTagLen, status.ErrInvalidArgument)
		}
	}
	if len(art.GetNotes()) > maxNotesLen {
		return fmt.Errorf("artifact.notes cannot be longer than %d characters: %w", maxNotesLen, status.ErrInvalidArgument)
	}
	if info := art.GetAdditionalInfo(); info != nil {
		if info.GetContent() == nil {
			return fmt.Errorf("artifact.additional_info.content must be set when additional_info is provided: %w", status.ErrInvalidArgument)
		}
	}
	if payload := art.GetPayload(); payload != nil {
		if err := validatePayload(payload); err != nil {
			return err
		}
	}

	return nil
}

func validatePayload(payload *essmsgpb.ArtifactPayload) error {
	if payload.GetType() == nil {
		return fmt.Errorf("artifact.payload.type must be set when payload is provided: %w", status.ErrInvalidArgument)
	}
	switch p := payload.Type.(type) {
	case *essmsgpb.ArtifactPayload_ManagedXprof:
		if p.ManagedXprof.GetDisplayName() == "" {
			return fmt.Errorf("artifact.payload.managed_xprof.display_name must be set: %w", status.ErrInvalidArgument)
		}
		if p.ManagedXprof.GetRunGroup() == "" {
			return fmt.Errorf("artifact.payload.managed_xprof.run_group must be set: %w", status.ErrInvalidArgument)
		}
	case *essmsgpb.ArtifactPayload_CodeSource:
		if p.CodeSource.GetTitle() == "" {
			return fmt.Errorf("artifact.payload.code_source.title must be set: %w", status.ErrInvalidArgument)
		}
		if p.CodeSource.GetCodeBlock() == "" {
			return fmt.Errorf("artifact.payload.code_source.code_block must be set: %w", status.ErrInvalidArgument)
		}
	case *essmsgpb.ArtifactPayload_Text:
		if p.Text.GetTitle() == "" {
			return fmt.Errorf("artifact.payload.text.title must be set: %w", status.ErrInvalidArgument)
		}
		if p.Text.GetText() == "" {
			return fmt.Errorf("artifact.payload.text.text must be set: %w", status.ErrInvalidArgument)
		}
	case *essmsgpb.ArtifactPayload_Url:
		if p.Url.GetUrl() == "" {
			return fmt.Errorf("artifact.payload.url.url must be set: %w", status.ErrInvalidArgument)
		}
	}
	return nil
}
