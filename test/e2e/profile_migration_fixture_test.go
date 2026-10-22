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

package e2e

import (
	"fmt"
	"strings"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// Pure fixture transformations and workload log parsing of profile_migration_test.go, kept apart so
// the hermetic kind_3p_harness_unit_test can cover them.

const (
	profileLabel = "xmc.dev/profile"
	// workloadOKMarker and workloadFailMarker prefix each iteration line of the continuous workloads.
	workloadOKMarker   = "WORKLOAD_OK"
	workloadFailMarker = "WORKLOAD_FAIL"
)

// profileOverlay is the test-only stable-2026b fixture; see testdata/profile_migration.
type profileOverlay struct {
	Profile       string            `json:"profile"`
	ImageTag      string            `json:"imageTag"`
	ChartVersions map[string]string `json:"chartVersions"`
}

// applyProfileOverlay returns copies of the rendered Component CRs with the profile label,
// spec.chartVersion and spec.version rewritten as the overlay fixture describes. It fails when the
// overlay has no chart version for a Component.
func applyProfileOverlay(objs []map[string]any, overlay profileOverlay) ([]map[string]any, error) {
	out := make([]map[string]any, 0, len(objs))
	for _, obj := range objs {
		u := (&unstructured.Unstructured{Object: obj}).DeepCopy()
		version, ok := overlay.ChartVersions[u.GetName()]
		if !ok {
			return nil, fmt.Errorf("profile overlay has no chartVersion for Component %q", u.GetName())
		}
		labels := u.GetLabels()
		if labels == nil {
			labels = make(map[string]string, 1)
		}
		labels[profileLabel] = overlay.Profile
		u.SetLabels(labels)
		for _, field := range []string{"chartVersion", "version"} {
			if err := unstructured.SetNestedField(u.Object, version, "spec", field); err != nil {
				return nil, fmt.Errorf("failed to set spec.%s of Component %q: %w", field, u.GetName(), err)
			}
		}
		out = append(out, u.Object)
	}
	return out, nil
}

// logTally counts the complete success and failure lines of a continuous workload.
type logTally struct{ ok, fail int }

// completeLogLines returns the newline-terminated lines of logs, dropping a trailing line that is
// still being written.
func completeLogLines(logs string) []string {
	lines := strings.Split(logs, "\n")
	return lines[:len(lines)-1]
}

// tallyWorkloadLogs counts the complete lines of logs prefixed by workloadOKMarker and
// workloadFailMarker.
func tallyWorkloadLogs(logs string) logTally {
	var tally logTally
	for _, line := range completeLogLines(logs) {
		switch {
		case strings.HasPrefix(line, workloadOKMarker):
			tally.ok++
		case strings.HasPrefix(line, workloadFailMarker):
			tally.fail++
		}
	}
	return tally
}

// workloadOKHosts returns, for each complete workloadOKMarker line of logs in order, the value of its
// host= field, or "" when the line has none. Index i matches the i-th success counted by
// tallyWorkloadLogs.
func workloadOKHosts(logs string) []string {
	var hosts []string
	for _, line := range completeLogLines(logs) {
		if !strings.HasPrefix(line, workloadOKMarker) {
			continue
		}
		hosts = append(hosts, okLineHost(line))
	}
	return hosts
}

// okLineHost returns the first whitespace-separated token after "host=" in line, or "".
func okLineHost(line string) string {
	_, after, found := strings.Cut(line, "host=")
	fields := strings.Fields(after)
	if !found || len(fields) == 0 {
		return ""
	}
	return fields[0]
}

// workloadFailureExcerpt returns the first maxLines failure lines of logs followed by its last
// maxLines complete lines, so failure messages stay short.
func workloadFailureExcerpt(logs string, maxLines int) string {
	lines := completeLogLines(logs)
	var failures []string
	for _, line := range lines {
		if strings.HasPrefix(line, workloadFailMarker) && len(failures) < maxLines {
			failures = append(failures, line)
		}
	}
	tail := lines[max(0, len(lines)-maxLines):]
	return fmt.Sprintf("first failures:\n%s\nlast lines:\n%s", strings.Join(failures, "\n"), strings.Join(tail, "\n"))
}
