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
