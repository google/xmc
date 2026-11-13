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
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"xmanager_cloud/cmd/rod_data_provider/cache"
)

func TestInitStorage_EmptyDBName(t *testing.T) {
	ctx := context.Background()

	storage, err := initStorage(ctx)
	if err == nil {
		t.Fatal("initStorage with empty database config succeeded, want error")
	}
	if storage != nil {
		t.Errorf("initStorage with empty database config returned non-nil storage: %v", storage)
	}
}

func TestSetupKueueReconcilers_NilCache(t *testing.T) {
	err := SetupKueueReconcilers(nil, nil, nil)
	if err == nil {
		t.Error("SetupKueueReconcilers(nil) expected error, got nil")
	}
}

func TestBuildHTTPServer(t *testing.T) {
	t.Setenv("JWT_SECRET", "test-secret-at-least-32-bytes-long!")
	ctx := context.Background()
	cfg := &Config{Port: "0", GRPCPort: "50051", ProjectID: "test-project"}
	kueueCache := cache.NewKueueCache()

	srv, err := buildHTTPServer(ctx, cfg, kueueCache, nil)
	if err != nil {
		t.Fatalf("buildHTTPServer failed: %v", err)
	}
	if srv == nil {
		t.Fatal("buildHTTPServer returned nil server")
	}

	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rec := httptest.NewRecorder()
	srv.Handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("/healthz status = %d, want %d", rec.Code, http.StatusOK)
	}
}
