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
	"net/http"
	"net/url"
	"time"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
)

const (
	defaultLocation = "us-central1"
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

func (c *ControlPlaneClient) buildHTTPRequest(ctx context.Context, method, rawURL string, body any) (*http.Request, error) {
	var reqBody io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("marshal request body: %w", err)
		}
		reqBody = bytes.NewReader(b)
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
	return req, nil
}

func (c *ControlPlaneClient) do(ctx context.Context, method, rawURL string, params url.Values, body any) (map[string]any, error) {
	if len(params) > 0 {
		rawURL = rawURL + "?" + params.Encode()
	}
	req, err := c.buildHTTPRequest(ctx, method, rawURL, body)
	if err != nil {
		return nil, err
	}

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
