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
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const (
	defaultHTTPTimeout    = 5 * time.Second
	maxResponseBodyBytes  = 1 << 20 // 1 MiB
	maxErrorMessageLength = 256
)

// Client communicates with an MLflow 3.x tracking server over its REST API.
type Client struct {
	baseURL    string
	httpClient *http.Client
	creds      Credentials
}

// NewClient constructs an MLflow REST client from cfg.
func NewClient(cfg *Config, httpClient *http.Client) (*Client, error) {
	if cfg == nil || strings.TrimSpace(cfg.TrackingURI) == "" {
		return nil, fmt.Errorf("mlflow tracking URI is required")
	}
	if httpClient == nil {
		httpClient = &http.Client{Timeout: defaultHTTPTimeout}
	}
	return &Client{
		baseURL:    strings.TrimRight(cfg.TrackingURI, "/"),
		httpClient: httpClient,
		creds:      cfg.creds,
	}, nil
}

type apiError struct {
	StatusCode int
	ErrorCode  string `json:"error_code"`
	Message    string `json:"message"`
}

func (e *apiError) Error() string {
	msg := e.Message
	if len(msg) > maxErrorMessageLength {
		msg = msg[:maxErrorMessageLength] + "..."
	}
	if e.ErrorCode != "" {
		return fmt.Sprintf("mlflow API error (%d %s): %s", e.StatusCode, e.ErrorCode, msg)
	}
	return fmt.Sprintf("mlflow API error (%d): %s", e.StatusCode, msg)
}

type experimentResponse struct {
	Experiment struct {
		ExperimentID string `json:"experiment_id"`
	} `json:"experiment"`
}

type createExperimentResponse struct {
	ExperimentID string `json:"experiment_id"`
}

type runTag struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

type runInfo struct {
	RunID  string `json:"run_id"`
	Status string `json:"status"`
}

type runData struct {
	Tags []runTag `json:"tags"`
}

type mlflowRun struct {
	Info runInfo `json:"info"`
	Data runData `json:"data"`
}

type createRunResponse struct {
	Run mlflowRun `json:"run"`
}

type searchRunsResponse struct {
	Runs []mlflowRun `json:"runs"`
}

// EnsureExperiment returns the MLflow experiment ID for title, creating it if absent.
func (c *Client) EnsureExperiment(ctx context.Context, title string) (string, error) {
	if strings.TrimSpace(title) == "" {
		return "", fmt.Errorf("experiment title cannot be empty")
	}
	id, err := c.getExperimentByName(ctx, title)
	if err == nil {
		return id, nil
	}
	var ae *apiError
	if !errors.As(err, &ae) || (ae.StatusCode != http.StatusNotFound && ae.ErrorCode != "RESOURCE_DOES_NOT_EXIST") {
		return "", err
	}
	id, err = c.createExperiment(ctx, title)
	if err == nil {
		return id, nil
	}
	if errors.As(err, &ae) && ae.ErrorCode == "RESOURCE_ALREADY_EXISTS" {
		return c.getExperimentByName(ctx, title)
	}
	return "", err
}

// RunLinks holds XMC metadata and URLs attached as tags and description to MLflow runs.
type RunLinks struct {
	ExperimentTitle string
	ExperimentURL   string
	WorkUnitURL     string
}

// EnsureParentRun returns the MLflow parent run ID for xid, creating it if absent
// and ensuring XMC link tags and description are set.
func (c *Client) EnsureParentRun(ctx context.Context, experimentID string, xid int64, links RunLinks) (string, error) {
	runName := fmt.Sprintf("xmc-%d", xid)
	filter := fmt.Sprintf("tags.`xmc.experiment_id` = '%d' AND tags.`mlflow.runName` = '%s'", xid, runName)
	runs, err := c.searchRuns(ctx, experimentID, filter)
	if err != nil {
		return "", err
	}
	desiredTags := append([]runTag{
		{Key: "xmc.experiment_id", Value: strconv.FormatInt(xid, 10)},
	}, buildLinkTags(links, false)...)
	for _, r := range runs {
		if _, hasParent := findTag(r.Data.Tags, "mlflow.parentRunId"); !hasParent && r.Info.RunID != "" {
			if err := c.syncRunTags(ctx, r.Info.RunID, r.Data.Tags, desiredTags); err != nil {
				return "", err
			}
			return r.Info.RunID, nil
		}
	}
	return c.createRun(ctx, experimentID, runName, desiredTags)
}

// EnsureChildRun returns the MLflow child run ID for (xid, wid), creating or reusing it,
// ensuring XMC link tags and description are set, and setting its status to SCHEDULED.
func (c *Client) EnsureChildRun(ctx context.Context, experimentID, parentRunID string, xid, wid, eid int64, links RunLinks) (string, error) {
	filter := fmt.Sprintf("tags.`xmc.experiment_id` = '%d' AND tags.`xmc.work_unit_id` = '%d'", xid, wid)
	runs, err := c.searchRuns(ctx, experimentID, filter)
	if err != nil {
		return "", err
	}
	eidStr := strconv.FormatInt(eid, 10)
	linkTags := buildLinkTags(links, true)
	var runID, curStatus string
	for _, r := range runs {
		if r.Info.RunID != "" {
			runID = r.Info.RunID
			curStatus = r.Info.Status
			desired := append([]runTag{{Key: "xmc.execution_id", Value: eidStr}}, linkTags...)
			if err := c.syncRunTags(ctx, runID, r.Data.Tags, desired); err != nil {
				return "", err
			}
			break
		}
	}
	if runID == "" {
		tags := []runTag{
			{Key: "mlflow.parentRunId", Value: parentRunID},
			{Key: "xmc.experiment_id", Value: strconv.FormatInt(xid, 10)},
			{Key: "xmc.work_unit_id", Value: strconv.FormatInt(wid, 10)},
			{Key: "xmc.execution_id", Value: eidStr},
		}
		runID, err = c.createRun(ctx, experimentID, fmt.Sprintf("wu-%d", wid), append(tags, linkTags...))
		if err != nil {
			return "", err
		}
	}
	if curStatus != "SCHEDULED" {
		if err := c.updateRunStatus(ctx, runID, "SCHEDULED"); err != nil {
			return "", err
		}
	}
	return runID, nil
}

func buildLinkTags(links RunLinks, includeWorkUnit bool) []runTag {
	var tags []runTag
	if links.ExperimentURL != "" {
		tags = append(tags, runTag{Key: "xmc.experiment_url", Value: links.ExperimentURL})
	}
	wuURL := ""
	if includeWorkUnit && links.WorkUnitURL != "" {
		wuURL = links.WorkUnitURL
		tags = append(tags, runTag{Key: "xmc.work_unit_url", Value: wuURL})
	}
	if desc := RunDescription(links.ExperimentTitle, links.ExperimentURL, wuURL); desc != "" {
		tags = append(tags, runTag{Key: "mlflow.note.content", Value: desc})
	}
	return tags
}

func (c *Client) syncRunTags(ctx context.Context, runID string, existing, desired []runTag) error {
	for _, want := range desired {
		if cur, ok := findTag(existing, want.Key); !ok || cur != want.Value {
			if err := c.setTag(ctx, runID, want.Key, want.Value); err != nil {
				return err
			}
		}
	}
	return nil
}

func (c *Client) getExperimentByName(ctx context.Context, title string) (string, error) {
	path := "/api/2.0/mlflow/experiments/get-by-name?experiment_name=" + url.QueryEscape(title)
	var resp experimentResponse
	if err := c.doJSON(ctx, http.MethodGet, path, nil, &resp); err != nil {
		return "", err
	}
	if resp.Experiment.ExperimentID == "" {
		return "", fmt.Errorf("mlflow get-by-name returned empty experiment_id")
	}
	return resp.Experiment.ExperimentID, nil
}

func (c *Client) createExperiment(ctx context.Context, title string) (string, error) {
	var resp createExperimentResponse
	err := c.doJSON(ctx, http.MethodPost, "/api/2.0/mlflow/experiments/create", map[string]string{"name": title}, &resp)
	if err != nil {
		return "", err
	}
	if resp.ExperimentID == "" {
		return "", fmt.Errorf("mlflow experiments/create returned empty experiment_id")
	}
	return resp.ExperimentID, nil
}

func (c *Client) searchRuns(ctx context.Context, experimentID, filter string) ([]mlflowRun, error) {
	body := map[string]any{
		"experiment_ids": []string{experimentID},
		"filter":         filter,
		"order_by":       []string{"attribute.start_time ASC"},
		"max_results":    20,
	}
	var resp searchRunsResponse
	if err := c.doJSON(ctx, http.MethodPost, "/api/2.0/mlflow/runs/search", body, &resp); err != nil {
		return nil, err
	}
	return resp.Runs, nil
}

func (c *Client) createRun(ctx context.Context, experimentID, runName string, tags []runTag) (string, error) {
	body := map[string]any{
		"experiment_id": experimentID,
		"run_name":      runName,
		"tags":          tags,
	}
	var resp createRunResponse
	if err := c.doJSON(ctx, http.MethodPost, "/api/2.0/mlflow/runs/create", body, &resp); err != nil {
		return "", err
	}
	if resp.Run.Info.RunID == "" {
		return "", fmt.Errorf("mlflow runs/create returned empty run_id")
	}
	return resp.Run.Info.RunID, nil
}

func (c *Client) setTag(ctx context.Context, runID, key, value string) error {
	body := map[string]string{"run_id": runID, "key": key, "value": value}
	return c.doJSON(ctx, http.MethodPost, "/api/2.0/mlflow/runs/set-tag", body, nil)
}

func (c *Client) updateRunStatus(ctx context.Context, runID, status string) error {
	// Omit end_time when transitioning to SCHEDULED so MLflow does not reject or close the run.
	body := map[string]string{"run_id": runID, "status": status}
	return c.doJSON(ctx, http.MethodPost, "/api/2.0/mlflow/runs/update", body, nil)
}

func (c *Client) doJSON(ctx context.Context, method, path string, reqBody any, out any) error {
	var bodyReader io.Reader
	if reqBody != nil {
		raw, err := json.Marshal(reqBody)
		if err != nil {
			return fmt.Errorf("failed to marshal mlflow request: %w", err)
		}
		bodyReader = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, bodyReader)
	if err != nil {
		return fmt.Errorf("failed to create mlflow request: %w", err)
	}
	if reqBody != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	c.applyAuth(req)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("mlflow request %s %s failed: %w", method, cleanPath(path), err)
	}
	defer resp.Body.Close()

	rawResp, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBodyBytes))
	if err != nil {
		return fmt.Errorf("failed to read mlflow response: %w", err)
	}
	if resp.StatusCode >= 400 {
		ae := &apiError{StatusCode: resp.StatusCode}
		_ = json.Unmarshal(rawResp, ae)
		if ae.Message == "" && len(rawResp) > 0 {
			const maxRawErrorBytes = 512
			msg := strings.TrimSpace(string(rawResp))
			if len(msg) > maxRawErrorBytes {
				msg = msg[:maxRawErrorBytes] + "..."
			}
			ae.Message = msg
		}
		return ae
	}
	if out != nil && len(rawResp) > 0 {
		if err := json.Unmarshal(rawResp, out); err != nil {
			return fmt.Errorf("failed to decode mlflow response: %w", err)
		}
	}
	return nil
}

func (c *Client) applyAuth(req *http.Request) {
	if c.creds.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.creds.token)
		return
	}
	if c.creds.username != "" || c.creds.password != "" {
		req.SetBasicAuth(c.creds.username, c.creds.password)
	}
}

func cleanPath(p string) string {
	if idx := strings.IndexByte(p, '?'); idx >= 0 {
		return p[:idx]
	}
	return p
}

func findTag(tags []runTag, key string) (string, bool) {
	for _, t := range tags {
		if t.Key == key {
			return t.Value, true
		}
	}
	return "", false
}
