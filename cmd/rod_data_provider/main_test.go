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
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"xmanager_cloud/cmd/rod_data_provider/cache"
	commonhealth "xmanager_cloud/internal/common/health"
)

type fakeReadinessChecker struct {
	err error
}

func (f *fakeReadinessChecker) CheckReady(_ context.Context) error {
	return f.err
}

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

	srv, err := buildHTTPServer(ctx, cfg, kueueCache, nil, &fakeReadinessChecker{})
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

func TestBuildHTTPServer_Readyz(t *testing.T) {
	t.Setenv("JWT_SECRET", "test-secret-at-least-32-bytes-long!")
	ctx := context.Background()
	cfg := &Config{Port: "0", GRPCPort: "50051", ProjectID: "test-project"}
	kueueCache := cache.NewKueueCache()

	tests := []struct {
		name           string
		db             commonhealth.ReadinessChecker
		wantStatus     int
		wantBodySubstr string
	}{
		{
			name:           "ready when database schema version aligns",
			db:             &fakeReadinessChecker{err: nil},
			wantStatus:     http.StatusOK,
			wantBodySubstr: "OK",
		},
		{
			name:           "unhealthy when database schema version mismatches",
			db:             &fakeReadinessChecker{err: errors.New("database schema version mismatch: expected 3, got 2")},
			wantStatus:     http.StatusServiceUnavailable,
			wantBodySubstr: "database schema version mismatch: expected 3, got 2",
		},
		{
			name:           "unhealthy when database readiness checker is nil",
			db:             nil,
			wantStatus:     http.StatusServiceUnavailable,
			wantBodySubstr: "database readiness checker is not initialized",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			srv, err := buildHTTPServer(ctx, cfg, kueueCache, nil, tt.db)
			if err != nil {
				t.Fatalf("buildHTTPServer failed: %v", err)
			}

			req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
			rec := httptest.NewRecorder()
			srv.Handler.ServeHTTP(rec, req)

			if rec.Code != tt.wantStatus {
				t.Errorf("/readyz status = %d, want %d (body: %q)", rec.Code, tt.wantStatus, rec.Body.String())
			}
			if !strings.Contains(rec.Body.String(), tt.wantBodySubstr) {
				t.Errorf("/readyz body = %q, want substring %q", rec.Body.String(), tt.wantBodySubstr)
			}
		})
	}
}
