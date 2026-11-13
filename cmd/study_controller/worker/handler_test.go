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

package worker

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"

	"xmanager_cloud/internal/logging"
)

func TestHandleTask(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		data        string
		wantSuccess bool
		wantLogs    []string
	}{
		{name: "valid tick is acknowledged", data: `{"experiment":"experiments/123"}`, wantSuccess: true,
			wantLogs: []string{"Received study reconcile tick", "worker_id=worker-1", "xid=123"}},
		{name: "malformed payload is dead-lettered", data: `not json`, wantLogs: []string{"Dropping invalid study reconcile tick", "payload_bytes=8"}},
		{name: "invalid experiment is dead-lettered", data: `{"experiment":"xids/123"}`, wantLogs: []string{"Dropping invalid study reconcile tick"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			var logs bytes.Buffer
			ctx := logging.WithLogger(context.Background(), slog.New(slog.NewTextHandler(&logs, &slog.HandlerOptions{Level: slog.LevelDebug})))

			got := handleTask(ctx, "worker-1", tc.data)

			if got.WasRunSuccessful != tc.wantSuccess || got.ShouldBeRetried {
				t.Errorf("handleTask(%q) = %+v, want WasRunSuccessful=%v ShouldBeRetried=false", tc.data, got, tc.wantSuccess)
			}
			if (got.Error == nil) != tc.wantSuccess {
				t.Errorf("handleTask(%q) error = %v, want error only on failure", tc.data, got.Error)
			}
			for _, want := range tc.wantLogs {
				if !strings.Contains(logs.String(), want) {
					t.Errorf("handleTask(%q) logs = %q, want them to contain %q", tc.data, logs.String(), want)
				}
			}
		})
	}
}
