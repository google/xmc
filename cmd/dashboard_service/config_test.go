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

package main

import (
	"testing"
)

func TestLoadConfig(t *testing.T) {
	t.Run("returns valid config struct", func(t *testing.T) {
		cfg := LoadConfig()
		if cfg == nil {
			t.Fatal("LoadConfig() returned nil")
		}
		if cfg.Port == "" {
			t.Error("Expected non-empty Port")
		}
		if cfg.GRPCPort == "" {
			t.Error("Expected non-empty GRPCPort")
		}
		if cfg.XidServiceAddr == "" {
			t.Error("Expected non-empty XidServiceAddr")
		}
	})

	t.Run("loads component flags from environment", func(t *testing.T) {
		t.Cleanup(loadEnvConfig)
		t.Setenv("components_namespace", "custom-ns")
		t.Setenv("METRICS_STORE", "bigquery")

		loadEnvConfig()

		if cfg.ComponentsNamespace != "custom-ns" {
			t.Errorf("cfg.ComponentsNamespace = %q, want %q", cfg.ComponentsNamespace, "custom-ns")
		}
		if got := LoadConfig().MetricsStore; got != "bigquery" {
			t.Errorf("LoadConfig().MetricsStore = %q, want %q", got, "bigquery")
		}
	})
}
