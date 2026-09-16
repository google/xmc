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

package resumption

import (
	"encoding/json"
	"fmt"
	"strings"
)

const (
	// defaultBatchSize is the fallback batch size fetched and replayed per iteration.
	defaultBatchSize = 100
	// defaultMaxBacklog is the fallback maximum unprocessed tasks in Redis before replay is paused.
	defaultMaxBacklog = 500
)

// Request represents the parsed resumption parameters dispatched to the resumption worker.
type Request struct {
	ResumptionRunID     string `json:"resumption_run_id"`
	BatchSize           int    `json:"batch_size"`
	MaxBacklogThreshold int    `json:"max_backlog_threshold"`
}

// ParseTriggerPayload parses the trigger JSON string into a Request with resolved defaults.
func ParseTriggerPayload(data string) (Request, error) {
	trimmed := strings.TrimSpace(data)
	if trimmed == "" {
		return Request{}, fmt.Errorf("trigger payload cannot be empty")
	}

	var req Request
	if err := json.Unmarshal([]byte(trimmed), &req); err != nil {
		return Request{}, fmt.Errorf("failed to unmarshal trigger payload: %w", err)
	}
	if req.ResumptionRunID == "" {
		return Request{}, fmt.Errorf("missing required resumption_run_id in trigger payload")
	}

	if req.BatchSize <= 0 {
		req.BatchSize = defaultBatchSize
	}
	if req.MaxBacklogThreshold <= 0 {
		req.MaxBacklogThreshold = defaultMaxBacklog
	}

	return req, nil
}
