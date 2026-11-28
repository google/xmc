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
	"net/url"
	"strings"
	"testing"
)

func TestExperimentChartsURL(t *testing.T) {
	tests := []struct {
		name               string
		uiBaseURL          string
		mlflowExperimentID string
		xid                int64
		want               string
	}{
		{
			name:               "distinct uiBaseUrl with trailing slash",
			uiBaseURL:          "https://mlflow-ui.example.com/",
			mlflowExperimentID: "42",
			xid:                123,
			want:               "https://mlflow-ui.example.com/#/experiments/42?searchFilter=tags.%60xmc.experiment_id%60%20%3D%20%27123%27&compareRunsMode=CHART",
		},
		{
			name:               "multiple trailing slashes",
			uiBaseURL:          "http://localhost:5000///",
			mlflowExperimentID: "7",
			xid:                99,
			want:               "http://localhost:5000/#/experiments/7?searchFilter=tags.%60xmc.experiment_id%60%20%3D%20%2799%27&compareRunsMode=CHART",
		},
		{
			name:               "empty uiBaseURL",
			uiBaseURL:          "",
			mlflowExperimentID: "42",
			xid:                123,
			want:               "",
		},
		{
			name:               "empty experiment ID",
			uiBaseURL:          "https://mlflow.example.com",
			mlflowExperimentID: "",
			xid:                123,
			want:               "",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := ExperimentChartsURL(tc.uiBaseURL, tc.mlflowExperimentID, tc.xid)
			if got != tc.want {
				t.Errorf("ExperimentChartsURL() = %q, want %q", got, tc.want)
			}
			if got != "" {
				if strings.Contains(got, "+") {
					t.Errorf("ExperimentChartsURL() contains '+' instead of %%20: %q", got)
				}
				frag := strings.SplitN(got, "?", 2)[1]
				decoded, err := url.QueryUnescape(frag)
				if err != nil || !strings.Contains(decoded, "tags.`xmc.experiment_id` = '") {
					t.Errorf("decoded query %q (err=%v) did not preserve filter", decoded, err)
				}
			}
		})
	}
}

func TestWorkUnitRunURL(t *testing.T) {
	tests := []struct {
		name               string
		uiBaseURL          string
		mlflowExperimentID string
		runID              string
		want               string
	}{
		{
			name:               "standard with trailing slash",
			uiBaseURL:          "https://mlflow-ui.example.com/",
			mlflowExperimentID: "42",
			runID:              "abc-123",
			want:               "https://mlflow-ui.example.com/#/experiments/42/runs/abc-123",
		},
		{
			name:               "missing runID",
			uiBaseURL:          "https://mlflow-ui.example.com",
			mlflowExperimentID: "42",
			runID:              "",
			want:               "",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := WorkUnitRunURL(tc.uiBaseURL, tc.mlflowExperimentID, tc.runID); got != tc.want {
				t.Errorf("WorkUnitRunURL() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestXMCURLsAndRunDescription(t *testing.T) {
	expURL := XMCExperimentURL("http://localhost:8080/", 123)
	if expURL != "http://localhost:8080/experiments/123" {
		t.Errorf("XMCExperimentURL() = %q, want http://localhost:8080/experiments/123", expURL)
	}
	wuURL := XMCWorkUnitURL("http://localhost:8080/", 123, 4)
	if wuURL != "http://localhost:8080/experiments/123/workUnits/4" {
		t.Errorf("XMCWorkUnitURL() = %q, want http://localhost:8080/experiments/123/workUnits/4", wuURL)
	}
	if got := XMCExperimentURL("", 123); got != "" {
		t.Errorf("XMCExperimentURL(empty) = %q, want empty", got)
	}
	if got := XMCWorkUnitURL("", 123, 4); got != "" {
		t.Errorf("XMCWorkUnitURL(empty) = %q, want empty", got)
	}

	desc := RunDescription("sweep-title", expURL, wuURL)
	wantDesc := "### sweep-title\n\n- [XMC Experiment](http://localhost:8080/experiments/123)\n- [XMC Work Unit](http://localhost:8080/experiments/123/workUnits/4)"
	if desc != wantDesc {
		t.Errorf("RunDescription() = %q, want %q", desc, wantDesc)
	}

	parentDesc := RunDescription("sweep-title", expURL, "")
	wantParent := "### sweep-title\n\n- [XMC Experiment](http://localhost:8080/experiments/123)"
	if parentDesc != wantParent {
		t.Errorf("RunDescription(parent) = %q, want %q", parentDesc, wantParent)
	}

	noURLDesc := RunDescription("", "", "")
	if noURLDesc != "### XMC Experiment" {
		t.Errorf("RunDescription(empty) = %q, want ### XMC Experiment", noURLDesc)
	}
}
