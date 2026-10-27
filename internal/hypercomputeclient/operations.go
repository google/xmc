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
	"fmt"
	"math"
	"math/rand"
	"net/http"
	"strings"
	"time"

	"xmanager_cloud/internal/logging"
)

const (
	defaultPollingInterval = time.Second
	defaultTimeout         = 5 * time.Minute
	maxPollingDelay        = 60 * time.Second
	// CodeAlreadyExists is the google.rpc.Code (mirrors gRPC codes.AlreadyExists = 6)
	// the control plane embeds in a long-running-operation error when the target
	// resource already exists.
	CodeAlreadyExists = 6
)

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
// extracting the numeric google.rpc.Code and message when present.
func newOperationError(name string, errField any) *OperationError {
	oe := &OperationError{Name: name, Raw: errField}
	if m, ok := errField.(map[string]any); ok {
		switch v := m["code"].(type) {
		case float64:
			oe.Code = int(v)
		case int:
			oe.Code = v
		case int64:
			oe.Code = int(v)
		}
		oe.Message, _ = m["message"].(string)
	}
	return oe
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

func resolveWaitOptions(opts *WaitForOperationOptions) (time.Duration, time.Duration) {
	pollingInterval, timeout := defaultPollingInterval, defaultTimeout
	if opts != nil && opts.PollingInterval > 0 {
		pollingInterval = opts.PollingInterval
	}
	if opts != nil && opts.Timeout > 0 {
		timeout = opts.Timeout
	}
	return pollingInterval, timeout
}

func waitPollBackoff(ctx context.Context, operationName string, delay float64) (float64, error) {
	jitter := delay * (0.5 + rand.Float64()*0.5)
	timer := time.NewTimer(time.Duration(jitter * float64(time.Second)))
	defer timer.Stop()
	select {
	case <-ctx.Done():
		if ctx.Err() == context.DeadlineExceeded {
			return 0, fmt.Errorf("timed out waiting for operation %s to complete: %w", operationName, ctx.Err())
		}
		return 0, ctx.Err()
	case <-timer.C:
		return math.Min(delay*2, maxPollingDelay.Seconds()), nil
	}
}

// WaitForOperation polls until the operation completes or the deadline is reached.
func (c *ControlPlaneClient) WaitForOperation(ctx context.Context, operationName string, opts *WaitForOperationOptions) (map[string]any, error) {
	pollingInterval, timeout := resolveWaitOptions(opts)
	// Derive a context with the timeout so downstream GetOperation calls inherit the deadline.
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
			// Fail fast on empty body instead of treating it as "not done" until the deadline.
			return nil, fmt.Errorf("operation %q returned empty response", operationName)
		}
		if done, _ := op["done"].(bool); done {
			if errField, ok := op["error"]; ok && errField != nil {
				return nil, newOperationError(operationName, errField)
			}
			return op, nil
		}
		if delay, err = waitPollBackoff(ctx, operationName, delay); err != nil {
			return nil, err
		}
	}
}

// resolveOperation blocks until the long-running operation completes, then
// returns the final MLRun resource or a formatted error if it failed.
func (c *ControlPlaneClient) resolveOperation(ctx context.Context, result map[string]any) (map[string]any, error) {
	if result == nil {
		return nil, fmt.Errorf("operation result is nil")
	}
	operation := result
	if done, _ := result["done"].(bool); !done {
		opName, _ := result["name"].(string)
		if opName == "" {
			return nil, fmt.Errorf("operation name is empty in pending operation result")
		}
		var err error
		if operation, err = c.WaitForOperation(ctx, opName, nil); err != nil {
			return nil, err
		}
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
	parts := strings.Split(strings.TrimSuffix(target, "/"), "/")
	return c.GetMLRun(ctx, parts[len(parts)-1])
}
