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

package hypercomputeclient

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"

	"xmanager_cloud/internal/logging"
)

// GKEWorkloadDetails holds GKE-specific workload information.
type GKEWorkloadDetails struct {
	ID             string            `json:"id"`
	Kind           string            `json:"kind"`
	Cluster        string            `json:"cluster"`
	Namespace      string            `json:"namespace"`
	ParentWorkload string            `json:"parentWorkload,omitempty"`
	Labels         map[string]string `json:"labels,omitempty"`
	CreateTime     string            `json:"createTime,omitempty"`
}

// CreateMLRunOptions holds optional parameters for CreateMLRun.
type CreateMLRunOptions struct {
	Configs         map[string]any
	Tools           []map[string]any
	Artifacts       map[string]string
	RunGroup        string
	Labels          map[string]string
	Orchestrator    string
	WorkloadDetails *GKEWorkloadDetails
}

// CreateMLRun creates a new ML run and waits for the operation to complete.
func (c *ControlPlaneClient) CreateMLRun(ctx context.Context, name, displayName, runPhase string, opts *CreateMLRunOptions) (map[string]any, error) {
	payload := map[string]any{"name": name, "displayName": displayName, "runPhase": runPhase}
	applyMLRunCreateOptions(payload, opts)
	params := url.Values{"machine_learning_run_id": {SanitizeIdentifier(name)}}
	logging.Debug(ctx, "CreateMLRun request", "url", c.mlRunsPath, "params", params, "payload", payload)
	result, err := c.do(ctx, http.MethodPost, c.mlRunsPath, params, payload)
	if err != nil {
		var apiErr *APIError
		if errors.As(err, &apiErr) && apiErr.StatusCode == http.StatusConflict {
			logging.Debug(ctx, "CreateMLRun returned conflict (already exists)", "error", err)
		} else {
			logging.Error(ctx, "CreateMLRun failed", "error", err)
		}
		return nil, err
	}
	logging.Debug(ctx, "CreateMLRun response", "result", result)
	return c.resolveOperation(ctx, result)
}

// GetMLRun retrieves an existing ML run by name.
func (c *ControlPlaneClient) GetMLRun(ctx context.Context, name string) (map[string]any, error) {
	runURL := fmt.Sprintf("%s/%s", c.mlRunsPath, url.PathEscape(name))
	logging.Debug(ctx, "GetMLRun request", "url", runURL)
	result, err := c.do(ctx, http.MethodGet, runURL, nil, nil)
	if err != nil {
		logging.Error(ctx, "GetMLRun failed", "name", name, "error", err)
		return nil, err
	}
	logging.Debug(ctx, "GetMLRun response", "result", result)
	return result, nil
}

func applyMLRunCreateOptions(payload map[string]any, opts *CreateMLRunOptions) {
	if opts == nil {
		return
	}
	if len(opts.Configs) > 0 {
		payload["configs"] = opts.Configs
	}
	if len(opts.Artifacts) > 0 {
		payload["artifacts"] = opts.Artifacts
	}
	if opts.RunGroup != "" {
		payload["runSet"] = opts.RunGroup
	}
	if len(opts.Labels) > 0 {
		payload["labels"] = opts.Labels
	}
	if len(opts.Tools) > 0 {
		payload["tools"] = opts.Tools
	}
	if opts.Orchestrator != "" {
		payload["orchestrator"] = opts.Orchestrator
		if opts.Orchestrator == "GKE" && opts.WorkloadDetails != nil {
			payload["workloadDetails"] = map[string]any{"gke": opts.WorkloadDetails}
		}
	}
}

// UpdateMLRunOptions holds optional parameters for UpdateMLRun.
//
// Unlike CreateMLRunOptions, it intentionally omits Configs and Tools: in the
// XMC context these are set once at creation time and are never updated
// afterwards, so they are excluded here to keep the update contract explicit
// for callers.
type UpdateMLRunOptions struct {
	// Force triggers an update even when no fields have changed.
	Force           bool
	RunPhase        string
	DisplayName     string
	RunGroup        string
	Labels          map[string]string
	Artifacts       map[string]string
	Orchestrator    string
	WorkloadDetails *GKEWorkloadDetails
}

// mergeStringMap merges updates into a nested string map in payload under field.
// It initializes the nested map if absent or of an unexpected type, and reports
// whether any values were added or modified.
func mergeStringMap(payload map[string]any, field string, updates map[string]string) bool {
	if len(updates) == 0 {
		return false
	}
	nested, ok := payload[field].(map[string]any)
	if !ok || nested == nil {
		nested = make(map[string]any, len(updates))
		payload[field] = nested
	}
	var changed bool
	for k, v := range updates {
		if nested[k] != v {
			nested[k] = v
			changed = true
		}
	}
	return changed
}

func applyMLRunUpdateOptions(payload map[string]any, opts *UpdateMLRunOptions) bool {
	if opts == nil {
		return false
	}
	needUpdate := opts.Force
	for k, v := range map[string]string{"displayName": opts.DisplayName, "runPhase": opts.RunPhase, "runSet": opts.RunGroup} {
		if curr, _ := payload[k].(string); v != "" && curr != v {
			payload[k] = v
			needUpdate = true
		}
	}
	if mergeStringMap(payload, "labels", opts.Labels) {
		needUpdate = true
	}
	if mergeStringMap(payload, "artifacts", opts.Artifacts) {
		needUpdate = true
	}
	if opts.Orchestrator != "" {
		payload["orchestrator"] = opts.Orchestrator
		if opts.Orchestrator == "GKE" && opts.WorkloadDetails != nil {
			payload["workloadDetails"] = map[string]any{"gke": opts.WorkloadDetails}
		}
		needUpdate = true
	}
	return needUpdate
}

// UpdateMLRun updates an existing ML run via a read-modify-write against the full
// resource: it fetches the current state, applies the requested changes, and
// re-sends the whole resource with update_mask=* (the only mask the API accepts).
func (c *ControlPlaneClient) UpdateMLRun(ctx context.Context, name string, opts *UpdateMLRunOptions) (map[string]any, error) {
	payload, err := c.GetMLRun(ctx, name)
	if err != nil {
		return nil, err
	}
	if payload == nil {
		// A nil payload (e.g. an empty response body) would panic on the map
		// writes below ("assignment to entry in nil map").
		return nil, fmt.Errorf("update MLRun %q: GET returned empty payload", name)
	}
	if !applyMLRunUpdateOptions(payload, opts) {
		return payload, nil
	}
	// Remove output-only fields.
	for _, field := range []string{"createTime", "updateTime", "endTime"} {
		delete(payload, field)
	}
	// The current backend only accepts update_mask=*|workload_details, so it uses * here. TODO: update to per field masks once backend supports it.
	runURL := fmt.Sprintf("%s/%s", c.mlRunsPath, url.PathEscape(name))
	params := url.Values{"update_mask": {"*"}}
	logging.Debug(ctx, "UpdateMLRun request", "url", runURL, "params", params, "payload", payload)
	result, err := c.do(ctx, http.MethodPatch, runURL, params, payload)
	if err != nil {
		logging.Error(ctx, "UpdateMLRun failed", "name", name, "error", err)
		return nil, err
	}
	return c.resolveOperation(ctx, result)
}
