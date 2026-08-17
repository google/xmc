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
	t.Run("default fallbacks when env is empty", func(t *testing.T) {
		cfg := LoadConfig()
		if cfg == nil {
			t.Fatal("LoadConfig() returned nil")
		}
		if cfg.Port != "8080" {
			t.Errorf("Expected default Port '8080', got %q", cfg.Port)
		}
		if cfg.GRPCPort != "50051" {
			t.Errorf("Expected default GRPCPort '50051', got %q", cfg.GRPCPort)
		}
		if cfg.XidServiceAddr != "localhost:8082" {
			t.Errorf("Expected default XidServiceAddr 'localhost:8082', got %q", cfg.XidServiceAddr)
		}
	})

	t.Run("environment variable fallbacks", func(t *testing.T) {
		t.Setenv("PORT", "9090")
		t.Setenv("GRPC_PORT", "60051")
		t.Setenv("GOOGLE_CLOUD_PROJECT", "custom-proj")
		t.Setenv("XID_SERVICE_ADDR", "custom-xid:9999")

		cfg := LoadConfig()
		if cfg.Port != "9090" {
			t.Errorf("Expected Port '9090', got %q", cfg.Port)
		}
		if cfg.GRPCPort != "60051" {
			t.Errorf("Expected GRPCPort '60051', got %q", cfg.GRPCPort)
		}
		if cfg.ProjectID != "custom-proj" {
			t.Errorf("Expected ProjectID 'custom-proj', got %q", cfg.ProjectID)
		}
		if cfg.XidServiceAddr != "custom-xid:9999" {
			t.Errorf("Expected XidServiceAddr 'custom-xid:9999', got %q", cfg.XidServiceAddr)
		}
	})
}

func TestGetSpannerDatabaseName(t *testing.T) {
	t.Run("returns env variable when flag is empty", func(t *testing.T) {
		t.Setenv("SPANNER_DATABASE_NAME", "projects/p/instances/i/databases/d")
		if got := getSpannerDatabaseName(); got != "projects/p/instances/i/databases/d" {
			t.Errorf("getSpannerDatabaseName() = %q, want 'projects/p/instances/i/databases/d'", got)
		}
	})
}
