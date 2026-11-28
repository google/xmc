// Copyright 2026 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package mlflow

import (
	"fmt"
	"net/url"
	"strings"
)

const (
	// ExperimentURLDisplayName is the XMC Experiment Url display_name for the MLflow charts link.
	ExperimentURLDisplayName = "MLflow charts"
	// WorkUnitURLDisplayName is the XMC WorkUnit Url display_name for the MLflow run link.
	WorkUnitURLDisplayName = "MLflow run"
	// ExperimentURLIcon is the Material icon name for the Experiment MLflow link.
	ExperimentURLIcon = "analytics"
	// WorkUnitURLIcon is the Material icon name for the WorkUnit MLflow link.
	WorkUnitURLIcon = "timeline"
)

// ExperimentChartsURL builds the MLflow experiment chart URL filtered to xid.
// Spaces in searchFilter are percent-encoded as %20 so both URLSearchParams and
// decodeURIComponent in MLflow's hash router decode the filter cleanly.
func ExperimentChartsURL(uiBaseURL, mlflowExperimentID string, xid int64) string {
	base := strings.TrimRight(strings.TrimSpace(uiBaseURL), "/")
	if base == "" || mlflowExperimentID == "" {
		return ""
	}
	filter := fmt.Sprintf("tags.`xmc.experiment_id` = '%d'", xid)
	encodedFilter := strings.ReplaceAll(url.QueryEscape(filter), "+", "%20")
	return fmt.Sprintf("%s/#/experiments/%s?searchFilter=%s&compareRunsMode=CHART", base, url.PathEscape(mlflowExperimentID), encodedFilter)
}

// WorkUnitRunURL builds the MLflow run page URL for a work unit's child run.
func WorkUnitRunURL(uiBaseURL, mlflowExperimentID, runID string) string {
	base := strings.TrimRight(strings.TrimSpace(uiBaseURL), "/")
	if base == "" || mlflowExperimentID == "" || runID == "" {
		return ""
	}
	return fmt.Sprintf("%s/#/experiments/%s/runs/%s", base, url.PathEscape(mlflowExperimentID), url.PathEscape(runID))
}

// XMCExperimentURL builds the browser-facing XMC experiment page URL.
func XMCExperimentURL(frontendURL string, xid int64) string {
	base := strings.TrimRight(strings.TrimSpace(frontendURL), "/")
	if base == "" || xid <= 0 {
		return ""
	}
	return fmt.Sprintf("%s/experiments/%d", base, xid)
}

// XMCWorkUnitURL builds the browser-facing XMC work unit page URL.
func XMCWorkUnitURL(frontendURL string, xid, wid int64) string {
	base := strings.TrimRight(strings.TrimSpace(frontendURL), "/")
	if base == "" || xid <= 0 || wid <= 0 {
		return ""
	}
	return fmt.Sprintf("%s/experiments/%d/workUnits/%d", base, xid, wid)
}

// RunDescription formats the Markdown run description (mlflow.note.content)
// with the experiment title and links back to the XMC experiment and work unit pages.
func RunDescription(title, experimentURL, workUnitURL string) string {
	heading := strings.TrimSpace(title)
	if heading == "" {
		heading = "XMC Experiment"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "### %s", heading)
	if experimentURL != "" {
		fmt.Fprintf(&b, "\n\n- [XMC Experiment](%s)", experimentURL)
	}
	if workUnitURL != "" {
		if experimentURL == "" {
			b.WriteString("\n")
		}
		fmt.Fprintf(&b, "\n- [XMC Work Unit](%s)", workUnitURL)
	}
	return b.String()
}
