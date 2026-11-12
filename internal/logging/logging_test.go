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
package logging

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type logEntry map[string]any

func testHandler(w io.Writer, level slog.Level, projectID string) slog.Handler {
	return slog.NewJSONHandler(w, &slog.HandlerOptions{
		Level:     level,
		AddSource: true,
		ReplaceAttr: func(_ []string, a slog.Attr) slog.Attr {
			if a.Key == slog.LevelKey {
				a.Key = keySeverity
			}
			if a.Key == slog.MessageKey {
				a.Key = keyMessage
			}
			if a.Key == slog.TimeKey {
				a.Key = keyTimestamp
				if a.Value.Kind() == slog.KindTime {
					a.Value = slog.StringValue(a.Value.Time().Format(time.RFC3339))
				}
			}
			if a.Key == slog.SourceKey {
				a.Key = keySourceLocation
				source, _ := a.Value.Any().(*slog.Source)
				if source != nil {
					source.File = filepath.Base(source.File)
					source.Function = strings.TrimPrefix(source.Function, "xmanager_cloud/")
				}
			}
			if a.Key == keyTrace && projectID != "" {
				a.Key = keyTraceGCP
				a.Value = slog.StringValue(fmt.Sprintf("projects/%s/traces/%s", projectID, a.Value.String()))
			}
			return a
		},
	})
}

func TestGCPLogger(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(testHandler(&buf, slog.LevelDebug, "test-project"))
	ctx := WithLogger(context.Background(), logger)
	ctx = WithLabels(ctx, slog.String("label1", "value1"))
	ctx = WithJobSetName(ctx, "xm-job-123-1", nil)
	ctx = WithExperimentName(ctx, "experiments/123")

	Info(ctx, "test message", slog.String("key", "value"), slog.String(keyTrace, "trace-id"))

	var entry logEntry
	if err := json.Unmarshal(buf.Bytes(), &entry); err != nil {
		t.Fatalf("Failed to unmarshal log entry: %v", err)
	}
	if entry[keySeverity] != "INFO" {
		t.Errorf("Expected severity %q, got %q", "INFO", entry[keySeverity])
	}
	if entry[keyMessage] != "test message" {
		t.Errorf("Expected message %q, got %q", "test message", entry[keyMessage])
	}
	if entry["key"] != "value" {
		t.Errorf("Expected key %q, got %q", "value", entry["key"])
	}
	traceVal, ok := entry[keyTraceGCP].(string)
	if !ok || !strings.Contains(traceVal, "projects/test-project/traces/trace-id") {
		t.Errorf("Expected trace containing %q, got %q", "projects/test-project/traces/trace-id", entry[keyTraceGCP])
	}
	labels, ok := entry[keyLabelsGCP].(map[string]any)
	if !ok {
		t.Fatalf("Labels not found or not a map")
	}
	if labels["label1"] != "value1" {
		t.Errorf("Expected label1 %q, got %q", "value1", labels["label1"])
	}
	if labels[keyJobSet] != "xm-job-123-1" {
		t.Errorf("Expected job_set %q, got %q", "xm-job-123-1", labels[keyJobSet])
	}
	if labels[keyWID] != "1" {
		t.Errorf("Expected wid %q, got %q", "1", labels[keyWID])
	}
	if labels[keyXID] != "123" {
		t.Errorf("Expected xid %q, got %q", "123", labels[keyXID])
	}
	sourceLocation, ok := entry[keySourceLocation].(map[string]any)
	if !ok {
		t.Fatalf("Source location not found or not a map")
	}
	if sourceLocation["file"] != "logging_test.go" {
		t.Errorf("Expected source file %q, got %q", "logging_test.go", sourceLocation["file"])
	}
	if function, ok := sourceLocation["function"].(string); !ok || !strings.Contains(function, "TestGCPLogger") {
		t.Errorf("Expected source function containing %q, got %q", "TestGCPLogger", sourceLocation["function"])
	}
	if line, ok := sourceLocation["line"].(float64); !ok || line <= 0 {
		t.Errorf("Expected positive source line, got %v", sourceLocation["line"])
	}
}

func TestWithLabels(t *testing.T) {
	ctx := context.Background()
	ctx = WithLabels(ctx, slog.String("a", "1"))
	ctx = WithLabels(ctx, slog.String("b", "2"), slog.String("c", "3"))

	labels := getLabelsFromContext(ctx)

	expected := labelsMap{"a": "1", "b": "2", "c": "3"}
	if len(labels) != len(expected) {
		t.Errorf("Expected %d labels, got %d", len(expected), len(labels))
	}
	for k, v := range expected {
		if labels[k] != v {
			t.Errorf("Expected label %s=%s, got %s", k, v, labels[k])
		}
	}
}

func TestAdditiveLabels(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(testHandler(&buf, slog.LevelDebug, ""))
	ctx := WithLogger(context.Background(), logger)

	ctx = WithJobSetName(ctx, "xm-job-456-1", nil)
	Info(ctx, "first")
	buf.Reset()
	ctx = WithExperimentName(ctx, "experiments/123")
	Info(ctx, "second")

	var entry logEntry
	if err := json.Unmarshal(buf.Bytes(), &entry); err != nil {
		t.Fatalf("Failed to unmarshal log entry: %v", err)
	}
	labels, ok := entry[keyLabelsGCP].(map[string]any)
	if !ok {
		t.Fatalf("Labels not found or not a map")
	}
	if labels[keyJobSet] != "xm-job-456-1" {
		t.Errorf("Expected label %s=%s, got %s", keyJobSet, "xm-job-456-1", labels[keyJobSet])
	}
	if labels[keyWID] != "1" {
		t.Errorf("Expected label %s=%s, got %s", keyWID, "1", labels[keyWID])
	}
	if labels[keyXID] != "123" {
		t.Errorf("Expected label %s=%s, got %s", keyXID, "123", labels[keyXID])
	}
}

func TestFromContext(t *testing.T) {
	logger := slog.New(testHandler(&bytes.Buffer{}, slog.LevelInfo, ""))
	ctx := WithLogger(context.Background(), logger)
	if FromContext(ctx) != logger {
		t.Errorf("Expected logger from context")
	}
	if FromContext(context.Background()) == logger {
		t.Errorf("Did not expect logger from context")
	}
}

func TestWithValues(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(testHandler(&buf, slog.LevelDebug, ""))
	ctx := WithLogger(context.Background(), logger)

	ctx = WithValues(ctx, "key1", "value1")
	Info(ctx, "message")

	if !strings.Contains(buf.String(), `"key1":"value1"`) {
		t.Errorf("Expected log to contain key1:value1, got %s", buf.String())
	}
}

func TestDisabledLogLevel(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(testHandler(&buf, slog.LevelInfo, ""))
	ctx := WithLogger(context.Background(), logger)

	Debug(ctx, "test message")

	if buf.Len() > 0 {
		t.Errorf("Expected empty buffer for disabled log level, got %s", buf.String())
	}
}

func TestErrorMessage(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(testHandler(&buf, slog.LevelDebug, ""))
	ctx := WithLogger(context.Background(), logger)

	Info(ctx, "message", "error", "some error")

	var entry logEntry
	if err := json.Unmarshal(buf.Bytes(), &entry); err != nil {
		t.Fatalf("Failed to unmarshal log entry: %v", err)
	}

	if want := "message: some error"; entry[keyMessage] != want {
		t.Errorf("Expected message %q, got %q", want, entry[keyMessage])
	}
	if want := "some error"; entry["error"] != want {
		t.Errorf("Expected error field %q, got %q", want, entry["error"])
	}
}

func TestStripContext(t *testing.T) {
	otherKey := &contextKey{"otherKey"}
	ctx := context.Background()
	ctx = WithLogger(ctx, slog.Default())
	ctx = context.WithValue(ctx, traceIDKey, "trace-id")
	ctx = withLabelsInContext(ctx, labelsMap{"label1": "value1"})
	ctx = context.WithValue(ctx, otherKey, "otherValue")

	strippedCtx := Strip(ctx)

	if val := strippedCtx.Value(loggerKey); val != nil {
		t.Errorf("Strip() did not remove loggerKey, got %v", val)
	}
	if val := strippedCtx.Value(traceIDKey); val != nil {
		t.Errorf("Strip() did not remove traceIDKey, got %v", val)
	}
	if val := strippedCtx.Value(labelsKey); val != nil {
		t.Errorf("Strip() did not remove labelsKey, got %v", val)
	}
	if val := strippedCtx.Value(otherKey); val == nil {
		t.Errorf("Strip() removed otherKey, got nil")
	}
}
