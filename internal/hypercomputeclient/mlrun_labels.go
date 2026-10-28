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

package hypercomputeclient

import (
	"maps"
	"strconv"
)

const (
	// CreatedByLabel is the MLRun label key identifying the producer of a run.
	CreatedByLabel = "created_by"
	// CreatedByXManagerLabelValue marks an MLRun as owned by XManager. On a create
	// conflict, a run carrying created_by equal to this value is treated as our own
	// (idempotent success); a run without it is adopted (updated) with our fields.
	CreatedByXManagerLabelValue = "xmanager"
	// ExperimentIDLabel is the MLRun label key carrying the XManager experiment id.
	ExperimentIDLabel = "experiment_id"
	// WorkUnitIDLabel is the MLRun label key carrying the XManager work unit id.
	WorkUnitIDLabel = "workunit_id"
	// ListExistingSessionsOnlyLabel tells Diagon to scan the run's GCS folder for
	// profiles. It is applied via UpdateMLRun post-creation because setting it at
	// create time forces the run directly to COMPLETED stage, preventing
	// on-demand profiling.
	ListExistingSessionsOnlyLabel = "list_existing_sessions_only"
)

// BuildMLRunLabels returns the labels stamped on every MLRun XManager owns, so create
// and upsert apply an identical label set (including the created_by ownership marker).
func BuildMLRunLabels(xid, wid int64) map[string]string {
	return map[string]string{
		ExperimentIDLabel: strconv.FormatInt(xid, 10),
		WorkUnitIDLabel:   strconv.FormatInt(wid, 10),
		CreatedByLabel:    CreatedByXManagerLabelValue,
	}
}

// WithExistingSessionsOnlyLabel returns a copy of labels with
// ListExistingSessionsOnlyLabel set to "true".
func WithExistingSessionsOnlyLabel(labels map[string]string) map[string]string {
	updated := maps.Clone(labels)
	if updated == nil {
		updated = make(map[string]string, 1)
	}
	updated[ListExistingSessionsOnlyLabel] = "true"
	return updated
}

// MLRunHasLabel reports whether an MLRun resource carries the specified key-value label pair.
// Because a GET response decodes labels as map[string]any, it type-asserts defensively.
func MLRunHasLabel(mlRun map[string]any, key, value string) bool {
	labels, ok := mlRun["labels"].(map[string]any)
	if !ok {
		return false
	}
	v, ok := labels[key].(string)
	return ok && v == value
}

// MLRunOwnedByXManager reports whether an MLRun resource carries the
// created_by=xmanager label.
func MLRunOwnedByXManager(mlRun map[string]any) bool {
	return MLRunHasLabel(mlRun, CreatedByLabel, CreatedByXManagerLabelValue)
}

// MLRunAdoptionComplete reports whether an existing MLRun is owned by XManager and
// already carries ListExistingSessionsOnlyLabel, so no upsert is needed on a create
// conflict.
func MLRunAdoptionComplete(mlRun map[string]any) bool {
	return MLRunOwnedByXManager(mlRun) && MLRunHasLabel(mlRun, ListExistingSessionsOnlyLabel, "true")
}
