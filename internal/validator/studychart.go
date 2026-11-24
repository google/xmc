// Copyright 2026 Google LLC
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
	"regexp"
	"strings"

	"xmanager_cloud/internal/grpcerr"
)

// studyChartIDPattern matches ASCII-digit chart IDs reserved for server-provisioned study charts.
var studyChartIDPattern = regexp.MustCompile(`^study-\d+-parallel-coordinates$`)

// StudyChartID returns the deterministic ID of the parallel coordinates chart for STUDY xid.
func StudyChartID(xid int64) string { return fmt.Sprintf("study-%d-parallel-coordinates", xid) }

// validateStudyChartID rejects a reserved study chart ID (including whitespace-padded lookalikes)
// unless it matches the canonical experiment-level parent (xid > 0, wid == 0).
func validateStudyChartID(chartID string, xid, wid int64) error {
	if !studyChartIDPattern.MatchString(strings.TrimSpace(chartID)) || (xid > 0 && wid == 0 && chartID == StudyChartID(xid)) {
		return nil
	}
	return fmt.Errorf("chart IDs of the form study-{xid}-parallel-coordinates are reserved for the parent experiments/{xid}: %w", grpcerr.ErrInvalidArgument)
}
