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

// Package hypercomputeclient provides a client for the Google Cloud
// Hypercompute Cluster ML Run service.
//
// Forked from:
// (internal URL);l=71
//
// Reason: Diagon API is alpha so it doesn't have auto-generated gcloud golang client library.
package hypercomputeclient

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"math/rand"
	"net/http"
	"net/url"
	"strings"
	"time"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
	"xmanager_cloud/logging/logging"
)

const (
	defaultLocation        = "us-central1"
	defaultPollingInterval = time.Second
	defaultTimeout         = 5 * time.Minute
	maxPollingDelay        = 60 * time.Second
)

// ControlPlaneClient communicates with the Google Cloud Hypercompute Cluster
// ML Run service.
type ControlPlaneClient struct {
	projectID   string
	location    string
	baseURL     string
	mlRunsPath  string
	tokenSource oauth2.TokenSource
	httpClient  *http.Client
}

// NewControlPlaneClient creates a new ControlPlaneClient.
//
// environment must be one of "autopush", "staging", or "prod".
// location defaults to "us-central1" when empty.
func NewControlPlaneClient(ctx context.Context, projectID, environment, location string) (*ControlPlaneClient, error) {
	cfg, err := resolveEndpoint(projectID, environment, location)
	if err != nil {
		return nil, err
	}

	ts, err := google.DefaultTokenSource(ctx, "https://www.googleapis.com/auth/cloud-platform")
	if err != nil {
		return nil, fmt.Errorf("get default token source: %w", err)
	}

	return &ControlPlaneClient{
		projectID:   projectID,
		location:    cfg.location,
		baseURL:     cfg.baseURL,
		mlRunsPath:  cfg.mlRunsPath,
		tokenSource: ts,
		httpClient:  &http.Client{Timeout: 30 * time.Second},
	}, nil
}

// endpointConfig holds the endpoint URLs and effective location derived from
// the target environment.
type endpointConfig struct {
	location   string
	baseURL    string
	mlRunsPath string
}

// resolveEndpoint validates the environment and derives the base URL, ML runs
// path, and effective location. It is separated from NewControlPlaneClient so
// that URL construction and validation can be unit-tested without acquiring
// credentials.
func resolveEndpoint(projectID, environment, location string) (endpointConfig, error) {
	if projectID == "" {
		// An empty projectID would build malformed paths like "projects//locations".
		return endpointConfig{}, fmt.Errorf("projectID cannot be empty")
	}
	if environment != "autopush" && environment != "staging" && environment != "prod" {
		return endpointConfig{}, fmt.Errorf("invalid environment %q; must be one of %q, %q, or %q", environment, "autopush", "staging", "prod")
	}

	if location == "" {
		location = defaultLocation
	}

	baseURL := "https://hypercomputecluster.googleapis.com/v1alpha"
	if environment != "prod" {
		baseURL = fmt.Sprintf("https://%s-hypercomputecluster.sandbox.googleapis.com/v1alpha", environment)
	}

	mlRunsPath := fmt.Sprintf("%s/projects/%s/locations/%s/machineLearningRuns", baseURL, projectID, location)
	return endpointConfig{location: location, baseURL: baseURL, mlRunsPath: mlRunsPath}, nil
}

func (c *ControlPlaneClient) authHeader(ctx context.Context) (string, error) {
	token, err := c.tokenSource.Token()
	if err != nil {
		return "", fmt.Errorf("get token: %w", err)
	}
	return "Bearer " + token.AccessToken, nil
}

// APIError is returned by do for a non-2xx HTTP response. It carries the HTTP
// status code so callers can branch on it (e.g. http.StatusConflict) instead of
// matching on the error string.
type APIError struct {
	Method     string
	URL        string
	StatusCode int
	Body       string
}

// Error preserves the original human-readable format.
func (e *APIError) Error() string {
	return fmt.Sprintf("http %s %s: status %d: %s", e.Method, e.URL, e.StatusCode, e.Body)
}

// CodeAlreadyExists is the google.rpc.Code (mirrors gRPC codes.AlreadyExists = 6)
// the control plane embeds in a long-running-operation error when the target
// resource already exists.
const CodeAlreadyExists = 6

// OperationError is returned when a long-running operation completes with an error.
// It carries the numeric google.rpc.Code so callers can branch on it (e.g.
// CodeAlreadyExists) instead of matching on the error string.
type OperationError struct {
	Name    string
	Code    int
	Message string
	// Raw is the operation's original error payload, retained so the error string
	// stays identical to the pre-refactor format.
	Raw any
}

// Error preserves the original human-readable format.
func (e *OperationError) Error() string {
	return fmt.Sprintf("operation %s failed: %v", e.Name, e.Raw)
}

// newOperationError builds an OperationError from an operation's raw error payload,
// extracting the numeric google.rpc.Code and message when present. JSON numbers
// decode as float64, so the code is converted accordingly.
func newOperationError(name string, errField any) *OperationError {
	oe := &OperationError{Name: name, Raw: errField}
	if m, ok := errField.(map[string]any); ok {
		if code, ok := m["code"].(float64); ok {
			oe.Code = int(code)
		}
		if msg, ok := m["message"].(string); ok {
			oe.Message = msg
		}
	}
	return oe
}

func (c *ControlPlaneClient) do(ctx context.Context, method, rawURL string, params url.Values, body any) (map[string]any, error) {
	var reqBody io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("marshal request body: %w", err)
		}
		reqBody = bytes.NewReader(b)
	}

	if len(params) > 0 {
		rawURL = rawURL + "?" + params.Encode()
	}

	req, err := http.NewRequestWithContext(ctx, method, rawURL, reqBody)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}

	auth, err := c.authHeader(ctx)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", auth)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("http %s %s: %w", method, rawURL, err)
	}
	defer resp.Body.Close()

	respBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read response body: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, &APIError{Method: method, URL: rawURL, StatusCode: resp.StatusCode, Body: string(respBytes)}
	}

	// An empty body is a valid response (e.g. HTTP 204); treat it as a nil
	// result instead of attempting to JSON-decode it and erroring.
	if len(respBytes) == 0 {
		return nil, nil
	}

	var result map[string]any
	if err := json.Unmarshal(respBytes, &result); err != nil {
		return nil, fmt.Errorf("decode response: %w", err)
	}
	return result, nil
}

// GetOperation retrieves a long-running operation by name.
func (c *ControlPlaneClient) GetOperation(ctx context.Context, operationName string) (map[string]any, error) {
	opURL := fmt.Sprintf("%s/%s", c.baseURL, operationName)
	logging.Debug(ctx, "GetOperation", "url", opURL)

	result, err := c.do(ctx, http.MethodGet, opURL, nil, nil)
	if err != nil {
		logging.Error(ctx, "GetOperation failed", "url", opURL, "error", err)
		return nil, err
	}
	logging.Debug(ctx, "GetOperation response", "operation", result)
	return result, nil
}

// WaitForOperationOptions configures WaitForOperation.
type WaitForOperationOptions struct {
	PollingInterval time.Duration // default: 1s
	Timeout         time.Duration // default: 5m
}

// WaitForOperation polls until the operation completes or the deadline is reached.
func (c *ControlPlaneClient) WaitForOperation(ctx context.Context, operationName string, opts *WaitForOperationOptions) (map[string]any, error) {
	pollingInterval := defaultPollingInterval
	timeout := defaultTimeout
	if opts != nil {
		if opts.PollingInterval > 0 {
			pollingInterval = opts.PollingInterval
		}
		if opts.Timeout > 0 {
			timeout = opts.Timeout
		}
	}

	// Derive a context with the timeout so downstream GetOperation calls
	// inherit the deadline instead of relying on a manual wall-clock check.
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	delay := pollingInterval.Seconds()

	for {
		op, err := c.GetOperation(ctx, operationName)
		if err != nil {
			// Re-raise any error to fail fast (matches Python HTTPError re-raise).
			return nil, err
		}
		if op == nil {
			// GetOperation returns a nil map for an empty body; fail fast with a
			// clear error instead of treating it as "not done" and polling until
			// the deadline.
			return nil, fmt.Errorf("operation %q returned empty response", operationName)
		}

		if done, _ := op["done"].(bool); done {
			if errField, ok := op["error"]; ok && errField != nil {
				return nil, newOperationError(operationName, errField)
			}
			return op, nil
		}

		jitter := delay * (0.5 + rand.Float64()*0.5)
		timer := time.NewTimer(time.Duration(jitter * float64(time.Second)))
		select {
		case <-ctx.Done():
			timer.Stop()
			if ctx.Err() == context.DeadlineExceeded {
				return nil, fmt.Errorf("timed out waiting for operation %s to complete: %w", operationName, ctx.Err())
			}
			return nil, ctx.Err()
		case <-timer.C:
		}
		delay = math.Min(delay*2, maxPollingDelay.Seconds())
	}
}

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
func (c *ControlPlaneClient) CreateMLRun(
	ctx context.Context,
	name, displayName, runPhase string,
	opts *CreateMLRunOptions,
) (map[string]any, error) {
	payload := map[string]any{
		"name":        name,
		"displayName": displayName,
		"runPhase":    runPhase,
	}

	if opts != nil {
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

	params := url.Values{"machine_learning_run_id": {SanitizeIdentifier(name)}}

	logging.Debug(ctx, "CreateMLRun request", "url", c.mlRunsPath, "params", params, "payload", payload)

	result, err := c.do(ctx, http.MethodPost, c.mlRunsPath, params, payload)
	if err != nil {
		logging.Error(ctx, "CreateMLRun failed", "error", err)
		return nil, err
	}
	logging.Debug(ctx, "CreateMLRun response", "result", result)

	return c.resolveOperation(ctx, result)
}

// GetMLRun retrieves an existing ML run by name.
func (c *ControlPlaneClient) GetMLRun(ctx context.Context, name string) (map[string]any, error) {
	runURL := fmt.Sprintf("%s/%s", c.mlRunsPath, name)
	logging.Debug(ctx, "GetMLRun request", "url", runURL)

	result, err := c.do(ctx, http.MethodGet, runURL, nil, nil)
	if err != nil {
		logging.Error(ctx, "GetMLRun failed", "name", name, "error", err)
		return nil, err
	}
	logging.Debug(ctx, "GetMLRun response", "result", result)
	return result, nil
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

// UpdateMLRun updates an existing ML run. It fetches the current state, applies
// the requested changes, and PATCHes the resource with update_mask=*.
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

	needUpdate := false
	if opts != nil {
		needUpdate = opts.Force
		if opts.DisplayName != "" && payload["displayName"] != opts.DisplayName {
			payload["displayName"] = opts.DisplayName
			needUpdate = true
		}
		if opts.RunPhase != "" && payload["runPhase"] != opts.RunPhase {
			payload["runPhase"] = opts.RunPhase
			needUpdate = true
		}
		if opts.RunGroup != "" && payload["runSet"] != opts.RunGroup {
			payload["runSet"] = opts.RunGroup
			needUpdate = true
		}
		if len(opts.Labels) > 0 {
			payload["labels"] = opts.Labels
			needUpdate = true
		}
		if len(opts.Artifacts) > 0 {
			payload["artifacts"] = opts.Artifacts
			needUpdate = true
		}
		if opts.Orchestrator != "" {
			payload["orchestrator"] = opts.Orchestrator
			if opts.Orchestrator == "GKE" && opts.WorkloadDetails != nil {
				payload["workloadDetails"] = map[string]any{"gke": opts.WorkloadDetails}
			}
			needUpdate = true
		}
	}

	if !needUpdate {
		return payload, nil
	}

	// Remove output-only fields.
	for _, field := range []string{"createTime", "updateTime", "endTime"} {
		delete(payload, field)
	}
	// orchestrator and workloadDetails are mutable, but they are seeded from the GET
	// above and update_mask=* only touches the fields present in the payload. When
	// this call is not explicitly updating the orchestrator, drop them so the PATCH
	// stays scoped to the fields we intend to change and leaves the existing values
	// untouched.
	if opts == nil || opts.Orchestrator == "" {
		delete(payload, "orchestrator")
		delete(payload, "workloadDetails")
	}

	runURL := fmt.Sprintf("%s/%s", c.mlRunsPath, name)
	params := url.Values{"update_mask": {"*"}}

	logging.Debug(ctx, "UpdateMLRun request", "url", runURL, "params", params, "payload", payload)

	result, err := c.do(ctx, http.MethodPatch, runURL, params, payload)
	if err != nil {
		logging.Error(ctx, "UpdateMLRun failed", "name", name, "error", err)
		return nil, err
	}
	return c.resolveOperation(ctx, result)
}

// resolveOperation blocks until the long-running operation completes, then
// returns the final MLRun resource or a formatted error if it failed.
func (c *ControlPlaneClient) resolveOperation(ctx context.Context, result map[string]any) (map[string]any, error) {
	if result == nil {
		return nil, fmt.Errorf("operation result is nil")
	}

	var operation map[string]any
	var err error
	if done, _ := result["done"].(bool); !done {
		opName, _ := result["name"].(string)
		if opName == "" {
			return nil, fmt.Errorf("operation name is empty in pending operation result")
		}
		operation, err = c.WaitForOperation(ctx, opName, nil)
		if err != nil {
			return nil, err
		}
	} else {
		operation = result
	}

	logging.Info(ctx, "Operation complete", "operation", operation)

	if errField, ok := operation["error"]; ok && errField != nil {
		opName, _ := operation["name"].(string)
		return nil, newOperationError(opName, errField)
	}

	if response, ok := operation["response"].(map[string]any); ok {
		return response, nil
	}

	// Fall back: fetch the ML run using target from operation metadata.
	metadata, _ := operation["metadata"].(map[string]any)
	target, _ := metadata["target"].(string)
	if target == "" {
		return nil, fmt.Errorf("no response or target in operation %v", operation["name"])
	}
	parts := strings.Split(target, "/")
	mlRunName := parts[len(parts)-1]
	return c.GetMLRun(ctx, mlRunName)
}
