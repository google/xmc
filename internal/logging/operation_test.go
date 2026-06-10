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
	"log/slog"
	"strings"
	"testing"

	"golang/grpc/metadata/metadata"
)

func TestInjectOperationIDProvided(t *testing.T) {
	ctx := InjectOperation(context.Background(), "op1")
	id, ok := OperationID(ctx)
	if !ok || id != "op1" {
		t.Errorf("InjectOperation with specific id: got %q, want %q", id, "op1")
	}
}

func TestInjectOperationIDFromTraceID(t *testing.T) {
	ctx := context.WithValue(context.Background(), traceIDKey, "trace1")
	ctx = InjectOperation(ctx, "")
	id, ok := OperationID(ctx)
	if !ok || id != "trace1" {
		t.Errorf("InjectOperation with traceID in context: got %q, want %q", id, "trace1")
	}
}

func TestInjectOperationIDFromIncomingMetadata(t *testing.T) {
	md := metadata.Pairs(requestIDHeader, "req1")
	ctx := metadata.NewIncomingContext(context.Background(), md)
	ctx = InjectOperation(ctx, "")
	id, ok := OperationID(ctx)
	if !ok || id != "req1" {
		t.Errorf("InjectOperation with incoming metadata: got %q, want %q", id, "req1")
	}
}

func TestInjectOperationIDGenerated(t *testing.T) {
	ctx := InjectOperation(context.Background(), "")
	id, ok := OperationID(ctx)
	if !ok || id == "" {
		t.Errorf("InjectOperation with no source should generate id: got %q, ok=%v", id, ok)
	}
}

func TestInjectOperationAddsToOutgoingMetadata(t *testing.T) {
	ctx := InjectOperation(context.Background(), "op2")
	md, ok := metadata.FromOutgoingContext(ctx)
	if !ok {
		t.Fatalf("No outgoing metadata found in context")
	}
	vals := md.Get(requestIDHeader)
	if len(vals) == 0 || vals[0] != "op2" {
		t.Errorf("InjectOperation should add to outgoing metadata: got %q, want %q", vals, "op2")
	}
}

func TestInjectOperationAddsOperationAndTraceToLog(t *testing.T) {
	var buf bytes.Buffer
	logger := slog.New(testHandler(&buf, slog.LevelDebug, "test-project"))
	ctx := WithLogger(context.Background(), logger)

	ctx = InjectOperation(ctx, "op3")
	Info(ctx, "message")

	var entry logEntry
	if err := json.Unmarshal(buf.Bytes(), &entry); err != nil {
		t.Fatalf("Failed to unmarshal log entry: %v", err)
	}

	op, ok := entry[keyOperationGCP].(map[string]any)
	if !ok {
		t.Fatalf("%q field not found in log or not a map", keyOperationGCP)
	}
	if op["id"] != "op3" {
		t.Errorf("Expected operation id %q in log, got %q", "op3", op["id"])
	}

	traceVal, ok := entry[keyTraceGCP].(string)
	if !ok || !strings.Contains(traceVal, "projects/test-project/traces/op3") {
		t.Errorf("Expected trace containing %q in log, got %v", "projects/test-project/traces/op3", entry[keyTraceGCP])
	}
}

func TestOperationIDNotFound(t *testing.T) {
	_, ok := OperationID(context.Background())
	if ok {
		t.Errorf("OperationID should not be found in empty context")
	}
}
