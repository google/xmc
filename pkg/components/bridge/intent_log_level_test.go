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

package bridge

import (
	"context"
	"log/slog"
	"sync"
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	dynamicfake "k8s.io/client-go/dynamic/fake"

	componentspb "xmanager_cloud/api/protos/components/v1alpha1"
	"xmanager_cloud/internal/logging"
)

const (
	appliedIntentMessage   = "Applied component intent to CMS"
	attachFinalizerMessage = "Attaching deletion protection finalizer"
)

// loggedRecord is the level and attributes of one handled log message.
type loggedRecord struct {
	level slog.Level
	attrs map[string]any
}

// recordingHandler is a slog.Handler that records the level and attributes of every message it
// handles, keyed by message.
type recordingHandler struct {
	mu      sync.Mutex
	records map[string][]loggedRecord
}

func (h *recordingHandler) Enabled(context.Context, slog.Level) bool { return true }

func (h *recordingHandler) Handle(_ context.Context, r slog.Record) error {
	attrs := make(map[string]any)
	r.Attrs(func(a slog.Attr) bool {
		attrs[a.Key] = a.Value.Any()
		return true
	})
	h.mu.Lock()
	defer h.mu.Unlock()
	h.records[r.Message] = append(h.records[r.Message], loggedRecord{level: r.Level, attrs: attrs})
	return nil
}

func (h *recordingHandler) WithAttrs([]slog.Attr) slog.Handler { return h }
func (h *recordingHandler) WithGroup(string) slog.Handler      { return h }

// recordLogs returns a context whose logger records into the returned handler.
func recordLogs() (context.Context, *recordingHandler) {
	h := &recordingHandler{records: make(map[string][]loggedRecord)}
	return logging.WithLogger(context.Background(), slog.New(h)), h
}

// singleRecord returns the only record logged with message, failing the test otherwise.
func (h *recordingHandler) singleRecord(t *testing.T, message string) loggedRecord {
	t.Helper()
	recs := h.records[message]
	if len(recs) != 1 {
		t.Fatalf("%q logged %d times, want 1", message, len(recs))
	}
	return recs[0]
}

func TestReconcileComponent_AppliedIntentLogLevel(t *testing.T) {
	tests := []struct {
		name string
		// reported is status.componentGeneration; committed is the generation CMS returns.
		reported, committed int64
		wantLevel           slog.Level
	}{
		{name: "first apply without a reported generation", reported: 0, committed: 1, wantLevel: slog.LevelInfo},
		{name: "unchanged generation re-applied every pass", reported: 4, committed: 4, wantLevel: slog.LevelDebug},
		{name: "newly committed generation", reported: 4, committed: 5, wantLevel: slog.LevelInfo},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r, err := NewReconciler(&mockCMS{componentGen: tt.committed}, nil, nil)
			if err != nil {
				t.Fatalf("NewReconciler() error = %v", err)
			}
			ctx, logs := recordLogs()
			in := sourcedRedpandaInput("uid-a", time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC), 7)
			in.ComponentGeneration = tt.reported
			if _, err := r.ReconcileComponent(ctx, in); err != nil {
				t.Fatalf("ReconcileComponent() error = %v", err)
			}
			got := logs.singleRecord(t, appliedIntentMessage)
			if got.level != tt.wantLevel {
				t.Errorf("%q level = %v, want %v", appliedIntentMessage, got.level, tt.wantLevel)
			}
			if prev := got.attrs["previousComponentGeneration"]; prev != tt.reported {
				t.Errorf("%q previousComponentGeneration = %v, want %d", appliedIntentMessage, prev, tt.reported)
			}
		})
	}
}

// TestReconcileAndPersist_LogsFinalizerAttachOnce checks that one pass over a new custom resource
// logs the finalizer attachment once, from the update that attaches it.
func TestReconcileAndPersist_LogsFinalizerAttachOnce(t *testing.T) {
	r, err := NewReconciler(&mockCMS{}, nil, nil)
	if err != nil {
		t.Fatalf("NewReconciler() error = %v", err)
	}
	item := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "xmc.dev/v1alpha1",
		"kind":       "Component",
		"metadata":   map[string]any{"name": "redpanda", "namespace": "xmc"},
		"spec":       map[string]any{"type": componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_3P_WRAPPER.String(), "enabled": false},
	}}
	client := dynamicfake.NewSimpleDynamicClient(newTestScheme(), item.DeepCopy())
	ctx, logs := recordLogs()
	res, err := r.Reconcile(ctx, item)
	if err != nil {
		t.Fatalf("Reconcile() error = %v", err)
	}
	if !res.AddFinalizer {
		t.Fatal("Reconcile() AddFinalizer = false, want true for a new custom resource")
	}
	if _, err := SyncComponentFinalizers(ctx, client, "xmc", item, res); err != nil {
		t.Fatalf("SyncComponentFinalizers() error = %v", err)
	}
	if got := len(logs.records[attachFinalizerMessage]); got != 1 {
		t.Errorf("%q logged %d times, want 1", attachFinalizerMessage, got)
	}
}
