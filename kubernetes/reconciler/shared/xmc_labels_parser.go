// Package shared contains shared utilities for the XManager Cloud actuator reconciler.
package shared

import (
	"errors"
	"fmt"
	"strconv"

	"xmanager_cloud/kubernetes/common/constants"
)

// ErrMissingXMCLabels is returned when an object is missing required XMC labels.
var ErrMissingXMCLabels = errors.New("object is missing required labels")

// ParseXMCLabels parses XMC labels from a map.
func ParseXMCLabels(labels map[string]string) (xid int64, wid int64, eid int64, err error) {
	if labels == nil {
		return 0, 0, 0, ErrMissingXMCLabels
	}

	xidStr := labels[constants.ExperimentIDLabel]
	widStr := labels[constants.WorkUnitIDLabel]
	eidStr := labels[constants.ExecutionIDLabel]

	hasXID := xidStr != ""
	hasWID := widStr != ""
	hasEID := eidStr != ""

	if !hasXID || !hasWID || !hasEID {
		return 0, 0, 0, ErrMissingXMCLabels
	}

	xid, err = strconv.ParseInt(xidStr, 10, 64)
	if err != nil {
		return 0, 0, 0, fmt.Errorf("invalid %s label value %q: %w", constants.ExperimentIDLabel, xidStr, err)
	}

	wid, err = strconv.ParseInt(widStr, 10, 64)
	if err != nil {
		return 0, 0, 0, fmt.Errorf("invalid %s label value %q: %w", constants.WorkUnitIDLabel, widStr, err)
	}

	eid, err = strconv.ParseInt(eidStr, 10, 64)
	if err != nil {
		return 0, 0, 0, fmt.Errorf("invalid %s label value %q: %w", constants.ExecutionIDLabel, eidStr, err)
	}

	return xid, wid, eid, nil
}
