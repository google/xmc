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
package validator

import (
	"fmt"
	"strconv"
	"strings"

	"go/log"
	"task/go/status"
)

// validateStringID checks if a given ID string is valid, and returns the ID if it is valid.
func validateStringID(ID string) (int64, error) {
	intID, err := strconv.ParseInt(ID, 10, 64)
	if err != nil || intID <= 0 {
		log.Warningf("ID must be a positive integer: %s, error: %v", ID, err)
		return 0, fmt.Errorf("ID must be a positive integer: %w (%v)", status.ErrInvalidArgument, err)
	}

	return intID, nil
}

// ValidateWorkUnitNameAllowZeroWID checks if a given work unit name is valid, allowing the WID to be 0.
// It ensures that the name is in the correct format and that the XID is valid.
// It returns the integer XID and WID if it is valid.
// Valid work unit names are in the format "experiments/{xid}/workUnits/{wid}".
func ValidateWorkUnitNameAllowZeroWID(name string) (xid int64, wid int64, err error) {
	ids, err := validateResourceName(name, []string{"experiments", "workUnits"}, "experiments/{xid}/workUnits/{wid}", true)
	if err != nil {
		return 0, 0, err
	}
	return ids[0], ids[1], nil
}

// validateStringIDAllowZero checks if a given ID string is valid, allowing 0.
func validateStringIDAllowZero(ID string) (int64, error) {
	intID, err := strconv.ParseInt(ID, 10, 64)
	if err != nil || intID < 0 {
		log.Warningf("ID must be a non-negative integer: %s, error: %v", ID, err)
		return 0, fmt.Errorf("ID must be a non-negative integer: %w (%v)", status.ErrInvalidArgument, err)
	}

	return intID, nil
}

// validateResourceName checks if a given name conforms to a pattern of resource name parts.
// It expects a name in format "resource1/id1/resource2/id2/..."
// 'resources' contains resource parts, e.g. ["experiments", "workUnits"] for "experiments/{xid}/workUnits/{wid}".
// It returns parsed IDs in order of appearance.
// If allowZeroLastID is true, the last ID is allowed to be 0.
func validateResourceName(name string, resources []string, format string, allowZeroLastID bool) ([]int64, error) {
	parts := strings.Split(name, "/")
	if len(parts) != 2*len(resources) {
		return nil, fmt.Errorf("resource name must be in the format '%s': %w", format, status.ErrInvalidArgument)
	}

	var ids []int64
	for i, resource := range resources {
		if parts[2*i] != resource {
			return nil, fmt.Errorf("resource name must be in the format '%s': %w", format, status.ErrInvalidArgument)
		}

		var id int64
		var err error
		if allowZeroLastID && i == len(resources)-1 {
			id, err = validateStringIDAllowZero(parts[2*i+1])
		} else {
			id, err = validateStringID(parts[2*i+1])
		}

		if err != nil {
			return nil, fmt.Errorf("invalid ID in resource name: %w", err)
		}
		ids = append(ids, id)
	}
	return ids, nil
}

// ValidateXIDName checks if a given XID name is valid.
// It ensures that the name is in the correct format and that the XID is valid.
// It returns the integer XID if it is valid.
// Valid XID names are in the format "xids/{xid}".
func ValidateXIDName(name string) (int64, error) {
	ids, err := validateResourceName(name, []string{"xids"}, "xids/{xid}", false)
	if err != nil {
		return 0, err
	}
	return ids[0], nil
}

// ValidateExperimentName checks if a given experiment name is valid.
// It ensures that the name is in the correct format and that the XID is valid.
// It returns the integer XID if it is valid.
// Valid experiment names are in the format "experiments/{xid}".
func ValidateExperimentName(name string) (int64, error) {
	ids, err := validateResourceName(name, []string{"experiments"}, "experiments/{xid}", false)
	if err != nil {
		return 0, err
	}
	return ids[0], nil
}

// ValidateWorkUnitName checks if a given work unit name is valid.
// It ensures that the name is in the correct format and that the XID and WID are valid.
// It returns the integer XID and WID if it is valid.
// Valid work unit names are in the format "experiments/{xid}/workUnits/{wid}".
func ValidateWorkUnitName(name string) (xid int64, wid int64, err error) {
	ids, err := validateResourceName(name, []string{"experiments", "workUnits"}, "experiments/{xid}/workUnits/{wid}", false)
	if err != nil {
		return 0, 0, err
	}
	return ids[0], ids[1], nil
}

// ValidateWorkUnitExecutionName checks if a given work unit execution name is valid.
// It ensures that the name is in the correct format and that the XID, WID, and EID are valid.
// Valid work unit execution names are in the format "experiments/{xid}/workUnits/{wid}/executions/{eid}".
//
//nolint:revive // Why: Parses 3-level hierarchical AIP resource path into (xid, wid, eid, err) without introducing artificial tuple structs.
func ValidateWorkUnitExecutionName(name string) (xid int64, wid int64, eid int64, err error) {
	ids, err := validateResourceName(name, []string{"experiments", "workUnits", "executions"}, "experiments/{xid}/workUnits/{wid}/executions/{eid}", false)
	if err != nil {
		return 0, 0, 0, err
	}
	return ids[0], ids[1], ids[2], nil
}

// ValidateStatusMessageName checks if a given status message name is valid.
// It ensures that the name is in the correct format and that the XID, WID (if present), and MessageID are valid.
// It returns the integer XID, WID (or 0 if not present), and MessageID if it is valid.
// Valid status message names are in the format "experiments/{xid}/statusMessages/{message_id}"
// or "experiments/{xid}/workUnits/{wid}/statusMessages/{message_id}".
//
//nolint:revive // Why: Parses 3-level hierarchical AIP resource path into (xid, wid, messageID, err) without introducing artificial tuple structs.
func ValidateStatusMessageName(name string) (xid int64, wid int64, messageID int64, err error) {
	// Try work unit pattern first (longer)
	ids, err := validateResourceName(name, []string{"experiments", "workUnits", "statusMessages"}, "experiments/{xid}/workUnits/{wid}/statusMessages/{message_id}", false)
	if err == nil {
		return ids[0], ids[1], ids[2], nil
	}

	// Try experiment pattern
	ids, err = validateResourceName(name, []string{"experiments", "statusMessages"}, "experiments/{xid}/statusMessages/{message_id}", false)
	if err == nil {
		return ids[0], 0, ids[1], nil
	}

	return 0, 0, 0, fmt.Errorf("status message name must be in the format 'experiments/{xid}/statusMessages/{message_id}' or 'experiments/{xid}/workUnits/{wid}/statusMessages/{message_id}': %w", status.ErrInvalidArgument)
}

// ValidateArtifactName checks if a given artifact name is valid.
// It ensures that the name is in the correct format and that the AID is valid.
// It returns the integer AID if it is valid.
// Valid artifact names are in the format "artifacts/{aid}".
func ValidateArtifactName(name string) (int64, error) {
	ids, err := validateResourceName(name, []string{"artifacts"}, "artifacts/{aid}", false)
	if err != nil {
		return 0, err
	}
	return ids[0], nil
}

// ValidateArtifactParent checks if a given artifact parent is valid.
// It ensures that the parent is in the correct format and that the XID and WID are valid.
// It returns the integer XID and WID if it is valid.
// Valid artifact parents are in the format "experiments/{xid}" or "experiments/{xid}/workUnits/{wid}".
func ValidateArtifactParent(parent string) (xid int64, wid int64, err error) {
	return validateExperimentOrWorkUnitParent(parent)
}

// ValidateStatusMessageParent checks if a given status message parent is valid.
// It ensures that the parent is in the correct format and that the XID and WID are valid.
// It returns the integer XID and WID if it is valid.
// Valid status message parents are in the format "experiments/{xid}" or "experiments/{xid}/workUnits/{wid}".
func ValidateStatusMessageParent(parent string) (xid int64, wid int64, err error) {
	return validateExperimentOrWorkUnitParent(parent)
}

// ValidateOperationName checks if a given operation name is valid.
// It ensures that the name is in the correct format and that the XID and WID are valid.
// It returns the integer XID, WID, and the operation UUID string if the name is valid.
// Valid operation names are in the format "experiments/{xid}/workUnits/{wid}/operations/{uuid}".
//
//nolint:revive // Why: Parses 3-level hierarchical AIP resource path into (xid, wid, oidUUID, err) without introducing artificial tuple structs.
func ValidateOperationName(name string) (xid int64, wid int64, oidUUID string, err error) {
	const format = "experiments/{xid}/workUnits/{wid}/operations/{uuid}"
	const separator = "/operations/"
	index := strings.LastIndex(name, separator)
	if index == -1 {
		log.Warningf("Invalid resource name format: got %q, want '%s'", name, format)
		return 0, 0, "", fmt.Errorf("resource name must be in the format '%s': %w", format, status.ErrInvalidArgument)
	}
	workUnitName := name[:index]
	uuid := name[index+len(separator):]

	xid, wid, err = ValidateWorkUnitName(workUnitName)
	if err != nil {
		return 0, 0, "", err
	}

	if uuid == "" {
		log.Warningf("Invalid resource name format: got %q, want '%s'. UUID part is empty.", name, format)
		return 0, 0, "", fmt.Errorf("resource name must be in the format '%s', UUID cannot be empty: %w", format, status.ErrInvalidArgument)
	}

	return xid, wid, uuid, nil
}

// ConstructXIDName constructs the name of a XID in the format "xids/{xid}".
func ConstructXIDName(xid int64) string {
	return fmt.Sprintf("xids/%d", xid)
}

// ConstructExperimentName constructs the name of an experiment in the format "experiments/{xid}".
func ConstructExperimentName(xid int64) string {
	return fmt.Sprintf("experiments/%d", xid)
}

// ConstructWorkUnitName constructs the name of a work unit in the format "experiments/{xid}/workUnits/{wid}".
func ConstructWorkUnitName(xid int64, wid int64) string {
	return fmt.Sprintf("experiments/%d/workUnits/%d", xid, wid)
}

// ConstructWorkUnitExecutionName constructs the name of a work unit execution in the format "experiments/{xid}/workUnits/{wid}/executions/{execution_id}".
func ConstructWorkUnitExecutionName(xid int64, wid int64, eid int64) string {
	return fmt.Sprintf("experiments/%d/workUnits/%d/executions/%d", xid, wid, eid)
}

// ConstructOperationName constructs the name of an operation in the format "experiments/{xid}/workUnits/{wid}/operations/{uuid}".
func ConstructOperationName(xid int64, wid int64, oid string) string {
	return fmt.Sprintf("experiments/%d/workUnits/%d/operations/%s", xid, wid, oid)
}

// ConstructArtifactName constructs the name of an artifact in the format "artifacts/{aid}".
func ConstructArtifactName(aid int64) string {
	return fmt.Sprintf("artifacts/%d", aid)
}

// ResourceType represents the type of the resource.
type ResourceType int

const (
	// UnknownResource represents an unknown resource type.
	UnknownResource ResourceType = iota
	// XIDResource represents a XID resource.
	XIDResource
	// ExperimentResource represents an experiment resource.
	ExperimentResource
	// WorkUnitResource represents a work unit resource.
	WorkUnitResource
	// WorkUnitExecutionResource represents a work unit execution resource.
	WorkUnitExecutionResource
	// ArtifactResource represents an artifact resource.
	ArtifactResource
	// OperationResource represents an operation resource.
	OperationResource
	// DashboardResource represents a dashboard resource.
	DashboardResource
)

// ValidateDashboardName checks if a given dashboard name is valid.
// It ensures that the name is in the correct format "dashboards/{dashboard_id}".
// It returns the dashboard ID string if it is valid.
func ValidateDashboardName(name string) (string, error) {
	parts := strings.Split(name, "/")
	if len(parts) != 2 || parts[0] != "dashboards" || parts[1] == "" {
		return "", fmt.Errorf("dashboard name must be in the format 'dashboards/{dashboard_id}': %w", status.ErrInvalidArgument)
	}
	return parts[1], nil
}

// ConstructDashboardName constructs the name of a dashboard in the format "dashboards/{dashboard_id}".
func ConstructDashboardName(dashboardID string) string {
	return fmt.Sprintf("dashboards/%s", dashboardID)
}

// GetResourceType identifies the type of the given resource name.
func GetResourceType(name string) ResourceType {
	if _, err := ValidateXIDName(name); err == nil {
		return XIDResource
	}
	if _, err := ValidateExperimentName(name); err == nil {
		return ExperimentResource
	}
	if _, _, err := ValidateWorkUnitName(name); err == nil {
		return WorkUnitResource
	}
	if _, _, _, err := ValidateWorkUnitExecutionName(name); err == nil {
		return WorkUnitExecutionResource
	}
	if _, err := ValidateArtifactName(name); err == nil {
		return ArtifactResource
	}
	if _, _, _, err := ValidateOperationName(name); err == nil {
		return OperationResource
	}
	if _, err := ValidateDashboardName(name); err == nil {
		return DashboardResource
	}
	return UnknownResource
}

func validateExperimentOrWorkUnitParent(parent string) (int64, int64, error) {
	resourceType := GetResourceType(parent)
	if resourceType == ExperimentResource {
		xid, err := ValidateExperimentName(parent)
		if err != nil {
			return 0, 0, fmt.Errorf("failed to validate experiment name %q: %w", parent, err)
		}
		return xid, 0, nil
	} else if resourceType == WorkUnitResource {
		xid, wid, err := ValidateWorkUnitName(parent)
		if err != nil {
			return 0, 0, fmt.Errorf("failed to validate work unit name %q: %w", parent, err)
		}
		return xid, wid, nil
	}
	return 0, 0, fmt.Errorf("parent must be in the format experiments/{xid} or experiments/{xid}/workUnits/{wid}: %w", status.ErrInvalidArgument)
}
