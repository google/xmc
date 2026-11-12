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
	"strconv"
	"strings"

	"google.golang.org/protobuf/reflect/protoreflect"
	essmsgpb "xmanager_cloud/api/protos/experiment"
	xidmessagespb "xmanager_cloud/api/protos/xid"
	"xmanager_cloud/internal/grpcerr"
	"xmanager_cloud/internal/kubernetes/common/jobsetutil"
)

const (
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
	// maxBatchCreateWorkUnits is the maximum number of requests in a BatchCreateWorkUnitsRequest.
	maxBatchCreateWorkUnits = 500
)

var (
	// jobNameRegex matches valid Kubernetes job names (RFC 1123 DNS Label).
	jobNameRegex = regexp.MustCompile(`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`)
	// labelNameRegex matches valid Kubernetes label values and label name segments.
	labelNameRegex = regexp.MustCompile(`^[a-zA-Z0-9]([a-zA-Z0-9-_.]*[a-zA-Z0-9])?$`)
	// dnsSubdomainRegex matches valid DNS subdomain names (RFC 1123).
	dnsSubdomainRegex = regexp.MustCompile(`^[a-z0-9]([-a-z0-9]*[a-z0-9])?(\.[a-z0-9]([-a-z0-9]*[a-z0-9])?)*$`)
)

func validateRoles(roles []*xidmessagespb.XidAcls_Role) error {
	for _, role := range roles {
		if role.GetPrincipal() == nil {
			return fmt.Errorf("role cannot be nil: %w", grpcerr.ErrInvalidArgument)
		}
	}
	return nil
}

// ValidateCreateXidRequest validates the given CreateXidEntryRequest.
func ValidateCreateXidRequest(in *xidmessagespb.CreateXidEntryRequest) error {
	if in == nil {
		return fmt.Errorf("request cannot be nil: %w", grpcerr.ErrInvalidArgument)
	}
	if in.GetXidEntry() == nil {
		return fmt.Errorf("xid_entry cannot be nil: %w", grpcerr.ErrInvalidArgument)
	}
	if in.GetXidEntry().GetName() != "" {
		return fmt.Errorf("xid_entry.name must be empty for CreateXidEntryRequest: %w", grpcerr.ErrInvalidArgument)
	}
	if in.GetXidEntry().GetOrigin() == nil {
		return fmt.Errorf("xid_entry.origin cannot be nil: %w", grpcerr.ErrInvalidArgument)
	}
	acls := in.GetXidEntry().GetAcls()
	if parentXID := in.GetXidEntry().GetParentXid(); parentXID != "" {
		if _, err := ValidateXIDName(parentXID); err != nil {
			return fmt.Errorf("xid_entry.parent_xid must be in the format xids/{xid}: %w", err)
		}
	} else if err := validateRootACLs(acls); err != nil {
		return err
	}
	return validateACLRoles(acls)
}

// validateACLRoles rejects ACLs containing a role without a principal in any role list. Nil ACLs
// are valid.
func validateACLRoles(acls *xidmessagespb.XidAcls) error {
	for _, roles := range [][]*xidmessagespb.XidAcls_Role{acls.GetOwners(), acls.GetWriters(), acls.GetReaders()} {
		if err := validateRoles(roles); err != nil {
			return err
		}
	}
	return nil
}

func validateRootACLs(acls *xidmessagespb.XidAcls) error {
	if acls == nil {
		return fmt.Errorf("xid_entry.acls cannot be nil: %w", grpcerr.ErrInvalidArgument)
	}
	if len(acls.GetOwners()) == 0 {
		return fmt.Errorf("xid_entry.acls.owners cannot be empty for an XID with no parent: %w", grpcerr.ErrInvalidArgument)
	}
	return nil
}

// ValidateCreateExperimentRequest validates the given CreateExperimentRequest.
func ValidateCreateExperimentRequest(in *essmsgpb.CreateExperimentRequest) error {
	if in == nil {
		return fmt.Errorf("request cannot be nil: %w", grpcerr.ErrInvalidArgument)
	}
	if in.GetExperiment() == nil {
		return fmt.Errorf("experiment cannot be nil: %w", grpcerr.ErrInvalidArgument)
	}
	exp := in.GetExperiment()
	if exp.GetName() != "" {
		return fmt.Errorf("experiment.name must be empty for CreateExperimentRequest: %w", grpcerr.ErrInvalidArgument)
	}
	if exp.GetCreator() != "" {
		return fmt.Errorf("experiment.creator must be empty for CreateExperimentRequest: %w", grpcerr.ErrInvalidArgument)
	}
	if strings.TrimSpace(exp.GetTitle()) == "" {
		return fmt.Errorf("experiment.title must be set and not only contain whitespace: %w", grpcerr.ErrInvalidArgument)
	}
	if exp.GetCreateTime() != nil {
		return fmt.Errorf("experiment.create_time must be empty for CreateExperimentRequest: %w", grpcerr.ErrInvalidArgument)
	}
	if exp.GetUpdateTime() != nil {
		return fmt.Errorf("experiment.update_time must be empty for CreateExperimentRequest: %w", grpcerr.ErrInvalidArgument)
	}
	if exp.GetStatus() != nil {
		return fmt.Errorf("experiment.status must be empty for CreateExperimentRequest: %w", grpcerr.ErrInvalidArgument)
	}
	// experiment_type is immutable after creation, so an unknown value must not be persisted.
	if expType := exp.GetExperimentType(); !isKnownEnumValue(expType) {
		return fmt.Errorf("experiment.experiment_type %d is not a known type: %w", expType, grpcerr.ErrInvalidArgument)
	}
	// Only studies carry a study config; UNSPECIFIED is stored as STANDARD.
	if exp.GetExperimentType() != essmsgpb.Experiment_STUDY && exp.GetStudyConfig() != nil {
		return fmt.Errorf("experiment.study_config must be unset unless experiment.experiment_type is STUDY: %w", grpcerr.ErrInvalidArgument)
	}
	if err := validateExperimentStudyConfig(exp); err != nil {
		return err
	}
	if err := validateExperimentACLs(exp.GetAcls()); err != nil {
		return err
	}
	return validateExperimentMetadata(exp)
}

// isKnownEnumValue reports whether e is a value declared by its enum. Values unknown to this server
// must not be persisted in fields that cannot be updated later.
func isKnownEnumValue(e protoreflect.Enum) bool {
	return e.Descriptor().Values().ByNumber(e.Number()) != nil
}

// validateExperimentACLs rejects malformed ACLs on experiment creation. Unset or empty ACLs are
// valid because FillExperimentDefaultACLs grants the creator OWNER. Otherwise, every role must name
// a principal and, as for any root XID, at least one owner must be listed.
func validateExperimentACLs(acls *xidmessagespb.XidAcls) error {
	if err := validateACLRoles(acls); err != nil {
		return fmt.Errorf("experiment.acls: %w", err)
	}
	if hasACLPrincipals(acls) && len(acls.GetOwners()) == 0 {
		return fmt.Errorf("experiment.acls.owners cannot be empty when writers or readers are set: %w", grpcerr.ErrInvalidArgument)
	}
	return nil
}

func validateExperimentMetadata(exp *essmsgpb.Experiment) error {
	return validateMetadataLimits("experiment", exp.GetTags(), exp.GetNotes(), exp.GetUrls())
}

// validateMetadataLimits enforces the size limits shared by experiment and work unit tags, notes,
// and URLs. The resource prefixes the field names in error messages, e.g. "experiment.tags".
func validateMetadataLimits(resource string, tags []string, notes string, urls []*essmsgpb.Url) error {
	if len(tags) > maxTags {
		return fmt.Errorf("%s.tags cannot contain more than %d tags: %w", resource, maxTags, grpcerr.ErrInvalidArgument)
	}
	for _, tag := range tags {
		if len(tag) > maxTagLen {
			return fmt.Errorf("%s.tags cannot contain tags longer than %d characters: %w", resource, maxTagLen, grpcerr.ErrInvalidArgument)
		}
	}
	if len(notes) > maxNotesLen {
		return fmt.Errorf("%s.notes cannot be longer than %d characters: %w", resource, maxNotesLen, grpcerr.ErrInvalidArgument)
	}
	if len(urls) > maxURLs {
		return fmt.Errorf("%s.urls cannot contain more than %d urls: %w", resource, maxURLs, grpcerr.ErrInvalidArgument)
	}
	for _, url := range urls {
		if len(url.GetUrl()) > maxURLLen {
			return fmt.Errorf("%s.urls cannot contain urls longer than %d characters: %w", resource, maxURLLen, grpcerr.ErrInvalidArgument)
		}
	}
	return nil
}

// immutableExperimentPaths are the Experiment fields that only CreateExperiment sets.
var immutableExperimentPaths = []string{"experiment_type", "study_config"}

// ValidateUpdateExperimentRequest validates updates in UpdateExperimentRequest.
func ValidateUpdateExperimentRequest(req *essmsgpb.UpdateExperimentRequest) error {
	for _, path := range req.GetUpdateMask().GetPaths() {
		if isImmutableExperimentPath(path) {
			return fmt.Errorf("experiment field %q cannot be updated after creation: %w", path, grpcerr.ErrInvalidArgument)
		}
		if path == "title" {
			if strings.TrimSpace(req.GetExperiment().GetTitle()) == "" {
				return fmt.Errorf("experiment title cannot be empty or contain only whitespaces: %w", grpcerr.ErrInvalidArgument)
			}
		}
	}
	return nil
}

// isImmutableExperimentPath reports whether the update mask path names an immutable Experiment
// field or one of its subfields.
func isImmutableExperimentPath(path string) bool {
	for _, field := range immutableExperimentPaths {
		if path == field || strings.HasPrefix(path, field+".") {
			return true
		}
	}
	return false
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

// FillExperimentDefaultACLs grants the creator OWNER on an experiment whose ACLs are unset or list
// no principals in any role. ACLs that list at least one principal are returned unchanged, so the
// server never rewrites access that the caller specified explicitly. Like FillExperimentNullValues,
// it modifies exp in place and returns it.
func FillExperimentDefaultACLs(exp *essmsgpb.Experiment, creator string) *essmsgpb.Experiment {
	if hasACLPrincipals(exp.GetAcls()) {
		return exp
	}
	exp.Acls = &xidmessagespb.XidAcls{
		Owners: []*xidmessagespb.XidAcls_Role{{Principal: &xidmessagespb.XidAcls_Role_User{User: creator}}},
	}
	return exp
}

// hasACLPrincipals reports whether the ACLs list at least one role in any role list. Callers must
// reject roles without a principal first (see validateACLRoles).
func hasACLPrincipals(acls *xidmessagespb.XidAcls) bool {
	return len(acls.GetOwners())+len(acls.GetWriters())+len(acls.GetReaders()) > 0
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

// isManagedXprofArtifact reports whether the artifact carries a ManagedXprof
// payload. ManagedXprof artifacts are enriched with their MLRun URL on the ESS
// read path, so unlike other artifacts they carry no URL at creation time.
func isManagedXprofArtifact(art *essmsgpb.Artifact) bool {
	return art.GetPayload().GetManagedXprof() != nil
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
	// A non-nil Url avoids nil-pointer dereferences in downstream Spanner
	// mutations, which read Url subfields unconditionally. ManagedXprof artifacts
	// legitimately carry no URL at creation, so this guard covers them too.
	if art.Url == nil {
		art.Url = &essmsgpb.Url{}
	}
	return art
}

// DefaultManagedXprofMetadata populates fallback values for DisplayName and RunGroup
// when they are empty or contain only whitespace. DisplayName defaults to
// "<expTitle>_<wid>" (or "<expTitle>" if not attached to a work unit), and RunGroup
// defaults to "<expTitle>".
func DefaultManagedXprofMetadata(mx *essmsgpb.ManagedXprofArtifact, expTitle string, wid int64) {
	if mx == nil {
		return
	}
	cleanedExp := strings.TrimSpace(expTitle)
	if strings.TrimSpace(mx.GetDisplayName()) == "" {
		if wid > 0 {
			mx.DisplayName = fmt.Sprintf("%s_%d", cleanedExp, wid)
		} else {
			mx.DisplayName = cleanedExp
		}
	}
	if strings.TrimSpace(mx.GetRunGroup()) == "" {
		mx.RunGroup = cleanedExp
	}
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
func ValidateCreateWorkUnitRequest(in *essmsgpb.CreateWorkUnitRequest) error {
	if in == nil {
		return fmt.Errorf("request cannot be nil: %w", grpcerr.ErrInvalidArgument)
	}
	if _, err := ValidateExperimentName(in.GetParent()); err != nil {
		return fmt.Errorf("parent must be in the format experiments/{xid}: %w", err)
	}
	if in.GetWorkUnit() == nil {
		return fmt.Errorf("work_unit cannot be nil: %w", grpcerr.ErrInvalidArgument)
	}
	wu := in.GetWorkUnit()
	if wu.GetName() != "" {
		return fmt.Errorf("work_unit.name must be empty for CreateWorkUnitRequest: %w", grpcerr.ErrInvalidArgument)
	}
	if wu.GetCreateTime() != nil {
		return fmt.Errorf("work_unit.create_time must be empty for CreateWorkUnitRequest: %w", grpcerr.ErrInvalidArgument)
	}
	if wu.GetUpdateTime() != nil {
		return fmt.Errorf("work_unit.update_time must be empty for CreateWorkUnitRequest: %w", grpcerr.ErrInvalidArgument)
	}
	if wu.GetCurrentWorkUnitExecution() != nil {
		return fmt.Errorf("work_unit.current_work_unit_execution must be empty for CreateWorkUnitRequest: %w", grpcerr.ErrInvalidArgument)
	}
	if err := validateWorkUnitMetadata(wu); err != nil {
		return err
	}
	if wu.GetExecutable() == nil {
		return fmt.Errorf("work_unit.executable cannot be nil: %w", grpcerr.ErrInvalidArgument)
	}
	if wu.GetWorkUnitType() == essmsgpb.WorkUnit_WORK_UNIT_TYPE_UNSPECIFIED {
		return fmt.Errorf("work_unit.work_unit_type cannot be empty: %w", grpcerr.ErrInvalidArgument)
	}
	return validateJobSetExecutable(wu.GetExecutable())
}

// ValidateBatchCreateWorkUnitsRequest validates the given BatchCreateWorkUnitsRequest and each of
// its requests, and returns the XID of its parent. The parent of each request must be empty or the
// batch parent, and work_unit_id must be unset because the server assigns the IDs.
func ValidateBatchCreateWorkUnitsRequest(in *essmsgpb.BatchCreateWorkUnitsRequest) (int64, error) {
	xid, err := ValidateExperimentName(in.GetParent())
	if err != nil {
		return 0, fmt.Errorf("parent must be in the format experiments/{xid}: %v: %w", err, grpcerr.ErrInvalidArgument)
	}
	if n := len(in.GetRequests()); n == 0 || n > maxBatchCreateWorkUnits {
		return 0, fmt.Errorf("requests must contain between 1 and %d requests, got %d: %w", maxBatchCreateWorkUnits, n, grpcerr.ErrInvalidArgument)
	}
	for i, req := range in.GetRequests() {
		if err := validateBatchedCreateWorkUnitRequest(in.GetParent(), req); err != nil {
			return 0, fmt.Errorf("requests[%d]: %w", i, err)
		}
	}
	return xid, nil
}

// validateBatchedCreateWorkUnitRequest validates one request of a batch with parent.
func validateBatchedCreateWorkUnitRequest(parent string, req *essmsgpb.CreateWorkUnitRequest) error {
	if req.GetParent() != "" && req.GetParent() != parent {
		return fmt.Errorf("parent must be empty or %q: %w", parent, grpcerr.ErrInvalidArgument)
	}
	if req.GetWorkUnitId() != 0 {
		return fmt.Errorf("work_unit_id must be unset, the server assigns work unit IDs: %w", grpcerr.ErrInvalidArgument)
	}
	return ValidateCreateWorkUnitRequest(&essmsgpb.CreateWorkUnitRequest{Parent: parent, WorkUnit: req.GetWorkUnit()})
}

func validateWorkUnitMetadata(wu *essmsgpb.WorkUnit) error {
	return validateMetadataLimits("work_unit", wu.GetTags(), wu.GetNotes(), wu.GetUrls())
}

func validateJobSetExecutable(exec *essmsgpb.Executable) error {
	if exec.GetSchedulingConstraints() == nil {
		return fmt.Errorf("work_unit.executable.scheduling_constraints cannot be nil: %w", grpcerr.ErrInvalidArgument)
	}
	if exec.GetJobset() == nil {
		return fmt.Errorf("work_unit.executable.jobset cannot be nil: %w", grpcerr.ErrInvalidArgument)
	}
	jobset := exec.GetJobset()
	if err := validateJobSetNetwork(jobset.GetNetwork()); err != nil {
		return err
	}
	if err := validateJobSetSuccessPolicy(jobset.GetSuccessPolicy(), jobset.GetJobs()); err != nil {
		return err
	}
	if len(jobset.Jobs) < 1 {
		return fmt.Errorf("work_unit.executable.jobset.jobs cannot contain less than 1 job: %w", grpcerr.ErrInvalidArgument)
	}
	if len(jobset.Jobs) > 8 {
		return fmt.Errorf("work_unit.executable.jobset.jobs cannot contain more than 8 jobs: %w", grpcerr.ErrInvalidArgument)
	}
	if err := validateJobSetJobs(jobset); err != nil {
		return err
	}
	if queueName := exec.GetSchedulingConstraints().GetQueueName(); queueName == "" {
		return fmt.Errorf("work_unit.executable.scheduling_constraints.queue_name cannot be empty: %w", grpcerr.ErrInvalidArgument)
	} else if len(queueName) > maxQueueNameLen {
		return fmt.Errorf("work_unit.executable.scheduling_constraints.queue_name cannot be longer than %d characters: %w", maxQueueNameLen, grpcerr.ErrInvalidArgument)
	}
	return nil
}

func validateJobSetJobs(jobset *essmsgpb.JobSetExecutable) error {
	jobNames := make(map[string]bool)
	jobMap := make(map[string]*essmsgpb.KubernetesJob)
	for _, job := range jobset.Jobs {
		if err := validateJobSpecAndMetadata(job, jobNames); err != nil {
			return err
		}
		jobNames[job.Name] = true
		jobMap[job.Name] = job
	}
	return validateJobSetCoordinator(jobset.GetCoordinator(), jobMap)
}

func validateJobSpecAndMetadata(job *essmsgpb.KubernetesJob, jobNames map[string]bool) error {
	if job.GetReplicas() < 1 {
		return fmt.Errorf("work_unit.executable.jobset.jobs.replicas must be greater than 0: %w", grpcerr.ErrInvalidArgument)
	}
	if job.Spec == nil {
		return fmt.Errorf("work_unit.executable.jobset.jobs.spec cannot be nil: %w", grpcerr.ErrInvalidArgument)
	}
	if job.Name == "" {
		return fmt.Errorf("work_unit.executable.jobset.jobs.name cannot be empty: %w", grpcerr.ErrInvalidArgument)
	}
	if err := validateDNSLabel(job.Name, "work_unit.executable.jobset.jobs.name"); err != nil {
		return err
	}
	if jobNames[job.Name] {
		return fmt.Errorf("work_unit.executable.jobset.jobs.name must be unique: %w", grpcerr.ErrInvalidArgument)
	}
	if err := validateJobDependencyOrder(job.GetDependencyOrder(), job.Name, jobNames); err != nil {
		return err
	}
	validVols, err := validateJobVolumes(job.GetVolumes(), "work_unit.executable.jobset.jobs.volumes")
	if err != nil {
		return err
	}
	if err := validateExecutableSpec(job.Spec, "work_unit.executable.jobset.jobs.spec", validVols); err != nil {
		return err
	}
	for i, sidecar := range job.GetSidecars() {
		prefix := fmt.Sprintf("work_unit.executable.jobset.jobs.sidecars[%d]", i)
		if err := validateExecutableSpec(sidecar, prefix, validVols); err != nil {
			return err
		}
	}
	for i, initContainer := range job.GetInitContainers() {
		prefix := fmt.Sprintf("work_unit.executable.jobset.jobs.init_containers[%d]", i)
		if err := validateExecutableSpec(initContainer, prefix, validVols); err != nil {
			return err
		}
	}
	if err := validateContainerNamesUnique(job); err != nil {
		return err
	}
	if err := validateNodeSelector(job); err != nil {
		return err
	}
	return validateJobSecurityAndDNS(job)
}

func validateJobSecurityAndDNS(job *essmsgpb.KubernetesJob) error {
	if sa := job.GetServiceAccount(); sa != "" {
		if len(sa) > 253 {
			return fmt.Errorf("work_unit.executable.jobset.jobs.service_account cannot be longer than 253 characters: %w", grpcerr.ErrInvalidArgument)
		}
		if !dnsSubdomainRegex.MatchString(sa) {
			return fmt.Errorf("work_unit.executable.jobset.jobs.service_account %q must be a valid DNS subdomain (RFC 1123): %w", sa, grpcerr.ErrInvalidArgument)
		}
	}
	if pcn := job.GetPriorityClassName(); pcn != "" {
		if len(pcn) > 253 {
			return fmt.Errorf("work_unit.executable.jobset.jobs.priority_class_name cannot be longer than 253 characters: %w", grpcerr.ErrInvalidArgument)
		}
		if !dnsSubdomainRegex.MatchString(pcn) {
			return fmt.Errorf("work_unit.executable.jobset.jobs.priority_class_name %q must be a valid DNS subdomain (RFC 1123): %w", pcn, grpcerr.ErrInvalidArgument)
		}
	}
	if dp := job.GetDnsPolicy(); dp != "" {
		switch dp {
		case "ClusterFirst", "ClusterFirstWithHostNet", "Default", "None":
		default:
			return fmt.Errorf("work_unit.executable.jobset.jobs.dns_policy %q must be one of ClusterFirst, ClusterFirstWithHostNet, Default, or None: %w", dp, grpcerr.ErrInvalidArgument)
		}
	}
	return nil
}

func validateJobDependencyOrder(dep *essmsgpb.KubernetesJob_DependencyOrder, jobName string, jobNames map[string]bool) error {
	if dep == nil {
		return nil
	}
	if dep.GetJobName() == "" {
		return fmt.Errorf("work_unit.executable.jobset.jobs.dependency_order.job_name cannot be empty: %w", grpcerr.ErrInvalidArgument)
	}
	if dep.GetJobName() == jobName {
		return fmt.Errorf("work_unit.executable.jobset.jobs.dependency_order.job_name cannot be the same as the job name: %w", grpcerr.ErrInvalidArgument)
	}
	if !jobNames[dep.GetJobName()] {
		return fmt.Errorf("work_unit.executable.jobset.jobs.dependency_order.job_name %q must be defined before the dependent job: %w", dep.GetJobName(), grpcerr.ErrInvalidArgument)
	}
	if dep.GetJobDependencyState() == essmsgpb.KubernetesJob_DependencyOrder_JOB_DEPENDENCY_STATE_UNSPECIFIED {
		return fmt.Errorf("work_unit.executable.jobset.jobs.dependency_order.job_dependency_state cannot be unspecified: %w", grpcerr.ErrInvalidArgument)
	}
	return nil
}

// ValidateCreateArtifactRequest validates the given CreateArtifactRequest.
func ValidateCreateArtifactRequest(in *essmsgpb.CreateArtifactRequest) error {
	if in == nil {
		return fmt.Errorf("request cannot be nil: %w", grpcerr.ErrInvalidArgument)
	}

	_, _, err := ValidateArtifactParent(in.GetParent())
	if err != nil {
		return fmt.Errorf("parent must be in the format experiments/{xid} or experiments/{xid}/workUnits/{wid}: %w", err)
	}

	if in.GetArtifact() == nil {
		return fmt.Errorf("artifact cannot be nil: %w", grpcerr.ErrInvalidArgument)
	}

	art := in.GetArtifact()
	if art.GetName() != "" || art.GetParent() != "" || art.GetCreateTime() != nil || art.GetUpdateTime() != nil {
		return fmt.Errorf("artifact name, parent, create_time, and update_time must be empty for CreateArtifactRequest: %w", grpcerr.ErrInvalidArgument)
	}
	return validateArtifactMetadata(art)
}

func validateArtifactMetadata(art *essmsgpb.Artifact) error {
	if !isManagedXprofArtifact(art) && art.GetUrl().GetUrl() == "" {
		return fmt.Errorf("artifact.url.url must be set: %w", grpcerr.ErrInvalidArgument)
	}
	if len(art.GetUrl().GetUrl()) > maxURLLen {
		return fmt.Errorf("artifact.url.url cannot be longer than %d characters: %w", maxURLLen, grpcerr.ErrInvalidArgument)
	}
	// Artifacts carry a single URL (validated above) rather than a URL list.
	if err := validateMetadataLimits("artifact", art.GetTags(), art.GetNotes(), nil); err != nil {
		return err
	}
	if info := art.GetAdditionalInfo(); info != nil {
		if info.GetContent() == nil {
			return fmt.Errorf("artifact.additional_info.content must be set when additional_info is provided: %w", grpcerr.ErrInvalidArgument)
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
		return fmt.Errorf("artifact.payload.type must be set when payload is provided: %w", grpcerr.ErrInvalidArgument)
	}
	switch p := payload.Type.(type) {
	case *essmsgpb.ArtifactPayload_ManagedXprof:
		// DisplayName and RunGroup are optional on creation; when left empty, ESS
		// populates them with default values derived from the parent experiment title
		// and work unit ID.
	case *essmsgpb.ArtifactPayload_CodeSource:
		if p.CodeSource.GetTitle() == "" {
			return fmt.Errorf("artifact.payload.code_source.title must be set: %w", grpcerr.ErrInvalidArgument)
		}
		if p.CodeSource.GetCodeBlock() == "" {
			return fmt.Errorf("artifact.payload.code_source.code_block must be set: %w", grpcerr.ErrInvalidArgument)
		}
	case *essmsgpb.ArtifactPayload_Text:
		if p.Text.GetTitle() == "" {
			return fmt.Errorf("artifact.payload.text.title must be set: %w", grpcerr.ErrInvalidArgument)
		}
		if p.Text.GetText() == "" {
			return fmt.Errorf("artifact.payload.text.text must be set: %w", grpcerr.ErrInvalidArgument)
		}
	case *essmsgpb.ArtifactPayload_Url:
		if p.Url.GetUrl() == "" {
			return fmt.Errorf("artifact.payload.url.url must be set: %w", grpcerr.ErrInvalidArgument)
		}
	case *essmsgpb.ArtifactPayload_Chart:
		if len(p.Chart.GetMetricNames()) == 0 {
			return fmt.Errorf("artifact.payload.chart.metric_names must not be empty: %w", grpcerr.ErrInvalidArgument)
		}
	}
	return nil
}

func validateExecutableSpec(spec *essmsgpb.ExecutableSpec, prefix string, validVolumes ...map[string]bool) error {
	if spec == nil {
		return fmt.Errorf("%s cannot be nil: %w", prefix, grpcerr.ErrInvalidArgument)
	}
	if name := spec.GetName(); name != "" {
		if err := validateDNSLabel(name, prefix+".name"); err != nil {
			return err
		}
	}
	if spec.GetDockerImage() == "" {
		return fmt.Errorf("%s.docker_image cannot be empty: %w", prefix, grpcerr.ErrInvalidArgument)
	}
	if spec.GetResources() == nil || spec.GetResources().GetResources() == nil {
		return fmt.Errorf("%s.resources.resources cannot be nil: %w", prefix, grpcerr.ErrInvalidArgument)
	}
	var volMap map[string]bool
	if len(validVolumes) > 0 {
		volMap = validVolumes[0]
	}
	if err := validateVolumeMounts(spec.GetVolumeMounts(), prefix, volMap); err != nil {
		return err
	}
	return validateResourceSet(spec.GetResources(), prefix+".resources")
}

func validateResourceSet(res *essmsgpb.ResourceSet, prefix string) error {
	if cpu := res.GetCpuResources(); cpu != nil {
		if cpu.GetCpuMillicores() < 1 {
			return fmt.Errorf("%s.cpu_resources.cpu_millicores must be greater than 0: %w", prefix, grpcerr.ErrInvalidArgument)
		}
		if cpu.GetMemoryBytes() < 1 {
			return fmt.Errorf("%s.cpu_resources.memory_bytes must be greater than 0: %w", prefix, grpcerr.ErrInvalidArgument)
		}
	}
	if gpu := res.GetGpuResources(); gpu != nil {
		if gpu.GetType() == "" {
			return fmt.Errorf("%s.gpu_resources.type cannot be empty: %w", prefix, grpcerr.ErrInvalidArgument)
		}
		if gpu.GetCount() < 1 {
			return fmt.Errorf("%s.gpu_resources.count must be greater than 0: %w", prefix, grpcerr.ErrInvalidArgument)
		}
	}
	if tpu := res.GetTpuResources(); tpu != nil {
		if tpu.GetType() == "" {
			return fmt.Errorf("%s.tpu_resources.type cannot be empty: %w", prefix, grpcerr.ErrInvalidArgument)
		}
		if err := validateTPUTopology(tpu.GetTopology(), prefix); err != nil {
			return err
		}
	}
	return nil
}

// validateTPUTopology verifies that a TPU topology string has valid positive integer dimensions.
func validateTPUTopology(topology string, prefix string) error {
	if topology == "" {
		return fmt.Errorf("%s.tpu_resources.topology cannot be empty: %w", prefix, grpcerr.ErrInvalidArgument)
	}
	dims := strings.Split(topology, "x")
	for _, dim := range dims {
		val, err := strconv.Atoi(dim)
		if err != nil || val <= 0 {
			return fmt.Errorf("%s.tpu_resources.topology %q is invalid (each dimension must be a positive integer): %w", prefix, topology, grpcerr.ErrInvalidArgument)
		}
	}
	return nil
}

// validateNodeSelector validates all key-value pairs in a Kubernetes job's node_selector map.
func validateNodeSelector(job *essmsgpb.KubernetesJob) error {
	for key, val := range job.GetNodeSelector() {
		if err := validateNodeSelectorLabel(key, val); err != nil {
			return err
		}
	}
	return nil
}

func validateNodeSelectorLabel(key, val string) error {
	if key == "" {
		return fmt.Errorf("work_unit.executable.jobset.jobs.node_selector key cannot be empty: %w", grpcerr.ErrInvalidArgument)
	}
	if len(key) > 316 {
		return fmt.Errorf("work_unit.executable.jobset.jobs.node_selector key %q exceeds maximum 316 characters: %w", key, grpcerr.ErrInvalidArgument)
	}
	if len(val) > 63 {
		return fmt.Errorf("work_unit.executable.jobset.jobs.node_selector value %q for key %q exceeds 63 characters: %w", val, key, grpcerr.ErrInvalidArgument)
	}
	if val != "" && !labelNameRegex.MatchString(val) {
		return fmt.Errorf("work_unit.executable.jobset.jobs.node_selector value %q for key %q must be a valid Kubernetes label value: %w", val, key, grpcerr.ErrInvalidArgument)
	}

	parts := strings.Split(key, "/")
	if len(parts) > 2 {
		return fmt.Errorf("work_unit.executable.jobset.jobs.node_selector key %q cannot contain more than one slash: %w", key, grpcerr.ErrInvalidArgument)
	}

	name := key
	if len(parts) == 2 {
		prefix := parts[0]
		name = parts[1]
		if prefix == "" {
			return fmt.Errorf("work_unit.executable.jobset.jobs.node_selector key %q prefix cannot be empty: %w", key, grpcerr.ErrInvalidArgument)
		}
		if len(prefix) > 253 {
			return fmt.Errorf("work_unit.executable.jobset.jobs.node_selector key %q prefix exceeds 253 characters: %w", key, grpcerr.ErrInvalidArgument)
		}
		if !dnsSubdomainRegex.MatchString(prefix) {
			return fmt.Errorf("work_unit.executable.jobset.jobs.node_selector key %q prefix must be a valid DNS subdomain: %w", key, grpcerr.ErrInvalidArgument)
		}
	}

	if name == "" {
		return fmt.Errorf("work_unit.executable.jobset.jobs.node_selector key %q name segment cannot be empty: %w", key, grpcerr.ErrInvalidArgument)
	}
	if len(name) > 63 {
		return fmt.Errorf("work_unit.executable.jobset.jobs.node_selector key %q name segment %q exceeds 63 characters: %w", key, name, grpcerr.ErrInvalidArgument)
	}
	if !labelNameRegex.MatchString(name) {
		return fmt.Errorf("work_unit.executable.jobset.jobs.node_selector key %q name segment %q must be a valid Kubernetes label name: %w", key, name, grpcerr.ErrInvalidArgument)
	}
	return nil
}

func validateDNSLabel(name, desc string) error {
	if len(name) > 63 {
		return fmt.Errorf("%s %q cannot be longer than 63 characters: %w", desc, name, grpcerr.ErrInvalidArgument)
	}
	if !jobNameRegex.MatchString(name) {
		return fmt.Errorf("%s %q must be a valid DNS label (RFC 1123): %w", desc, name, grpcerr.ErrInvalidArgument)
	}
	return nil
}

func validateJobSetNetwork(net *essmsgpb.JobSetExecutable_Network) error {
	if net == nil {
		return nil
	}
	if sub := net.GetSubdomain(); sub != "" {
		if err := validateDNSLabel(sub, "work_unit.executable.jobset.network.subdomain"); err != nil {
			return err
		}
	}
	return nil
}

func validateJobSetSuccessPolicy(policy *essmsgpb.JobSetExecutable_SuccessPolicy, jobs []*essmsgpb.KubernetesJob) error {
	if policy == nil {
		return nil
	}
	jobNames := make(map[string]bool, len(jobs))
	for _, j := range jobs {
		jobNames[j.GetName()] = true
	}
	for i, target := range policy.GetTargetReplicatedJobs() {
		if target == "" {
			return fmt.Errorf("work_unit.executable.jobset.success_policy.target_replicated_jobs[%d] cannot be empty: %w", i, grpcerr.ErrInvalidArgument)
		}
		if !jobNames[target] {
			return fmt.Errorf("work_unit.executable.jobset.success_policy.target_replicated_jobs[%d] %q does not match any job in work_unit.executable.jobset.jobs: %w", i, target, grpcerr.ErrInvalidArgument)
		}
	}
	return nil
}

func validateJobSetCoordinator(coord *essmsgpb.JobSetExecutable_Coordinator, jobs map[string]*essmsgpb.KubernetesJob) error {
	if coord == nil {
		return nil
	}
	if coord.GetReplicatedJob() == "" {
		return fmt.Errorf("work_unit.executable.jobset.coordinator.replicated_job cannot be empty: %w", grpcerr.ErrInvalidArgument)
	}
	job, ok := jobs[coord.GetReplicatedJob()]
	if !ok {
		return fmt.Errorf("work_unit.executable.jobset.coordinator.replicated_job %q must match a valid job name in the JobSet: %w", coord.GetReplicatedJob(), grpcerr.ErrInvalidArgument)
	}
	if coord.GetJobIndex() < 0 {
		return fmt.Errorf("work_unit.executable.jobset.coordinator.job_index cannot be negative: %w", grpcerr.ErrInvalidArgument)
	}
	if coord.GetJobIndex() >= job.GetReplicas() {
		return fmt.Errorf("work_unit.executable.jobset.coordinator.job_index %d must be less than replicas (%d) for job %q: %w", coord.GetJobIndex(), job.GetReplicas(), job.GetName(), grpcerr.ErrInvalidArgument)
	}
	if coord.GetPodIndex() < 0 {
		return fmt.Errorf("work_unit.executable.jobset.coordinator.pod_index cannot be negative: %w", grpcerr.ErrInvalidArgument)
	}
	return nil
}

func validateContainerName(names map[string]bool, name, containerType string) error {
	if err := validateDNSLabel(name, fmt.Sprintf("work_unit.executable.jobset.jobs %s container name", containerType)); err != nil {
		return err
	}
	if names[name] {
		return fmt.Errorf("work_unit.executable.jobset.jobs %s container name %q must be unique within the pod: %w", containerType, name, grpcerr.ErrInvalidArgument)
	}
	return nil
}

func validateContainerNamesUnique(job *essmsgpb.KubernetesJob) error {
	names := make(map[string]bool)
	names[jobsetutil.PrimaryContainerName(job)] = true

	for i, sidecar := range job.GetSidecars() {
		name := jobsetutil.SidecarContainerName(job, sidecar, i)
		if err := validateContainerName(names, name, "sidecar"); err != nil {
			return err
		}
		names[name] = true
	}
	for i, initContainer := range job.GetInitContainers() {
		name := jobsetutil.InitContainerName(job, initContainer, i)
		if err := validateContainerName(names, name, "init"); err != nil {
			return err
		}
		names[name] = true
	}
	return nil
}
