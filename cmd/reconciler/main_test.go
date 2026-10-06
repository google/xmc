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
package main

import (
	"context"
	"testing"
	"time"
)

// TestNewMLRunCreatorReturnsNilInterfaceOnError guards against the typed-nil
// interface pitfall: when client construction fails, newMLRunCreator must yield
// a genuine nil MLRunCreator interface. Otherwise a non-nil interface wrapping a
// nil *ControlPlaneClient would defeat the reconciler's nil check and panic on
// CreateMLRun.
func TestNewMLRunCreatorReturnsNilInterfaceOnError(t *testing.T) {
	tests := []struct {
		name        string
		environment string
	}{
		{name: "invalid environment", environment: "not-a-real-env"},
		{name: "empty environment", environment: ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// An invalid environment makes NewControlPlaneClient fail before it
			// needs ambient credentials, exercising the failure path hermetically.
			got := newMLRunCreator(context.Background(), "test-project", tc.environment, "us-central1")
			if got != nil {
				t.Errorf("newMLRunCreator() = %v, want nil interface on init failure", got)
			}
		})
	}
}

func TestLoadConfig(t *testing.T) {
	cfg := Load()
	if cfg.MetricsPort != 8082 {
		t.Errorf("Load().MetricsPort = %d, want 8082", cfg.MetricsPort)
	}
	if cfg.ReconciliationTimeout != 2*time.Minute {
		t.Errorf("Load().ReconciliationTimeout = %v, want %v", cfg.ReconciliationTimeout, 2*time.Minute)
	}
}
