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
	"os"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/grpc-ecosystem/grpc-gateway/v2/runtime"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"

	"xmanager_cloud/cmd/dashboard_service/server"
	"xmanager_cloud/internal/dashboard/dataengine"
	"xmanager_cloud/pkg/auth/oauth2"
)

func TestSetupHTTPHandler(t *testing.T) {
	jwtSecret := "test-secret-12345678901234567890"
	authenticator := oauth2.NewAuthenticator(&oauth2.Config{
		JWTSecret:     jwtSecret,
		AllowedEmails: []string{"user@example.com"},
	})

	validToken, err := oauth2.GenerateSessionToken("user@example.com", "User", "", time.Hour, []byte(jwtSecret))
	if err != nil {
		t.Fatalf("GenerateSessionToken failed: %v", err)
	}

	dummyGWMux := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"dashboards": []}`))
	})

	tests := []struct {
		name           string
		path           string
		method         string
		authHeader     string
		cookie         *http.Cookie
		readinessCheck func(context.Context) error
		wantStatus     int
		wantBody       string
	}{
		{
			name:       "healthz probe returns OK without auth",
			path:       "/healthz",
			method:     http.MethodGet,
			wantStatus: http.StatusOK,
			wantBody:   "OK",
		},
		{
			name:       "readyz probe returns OK without auth and nil check",
			path:       "/readyz",
			method:     http.MethodGet,
			wantStatus: http.StatusOK,
			wantBody:   "OK",
		},
		{
			name:   "readyz probe returns OK with healthy check",
			path:   "/readyz",
			method: http.MethodGet,
			readinessCheck: func(ctx context.Context) error {
				return nil
			},
			wantStatus: http.StatusOK,
			wantBody:   "OK",
		},
		{
			name:   "readyz probe returns 503 with failing check",
			path:   "/readyz",
			method: http.MethodGet,
			readinessCheck: func(ctx context.Context) error {
				return errors.New("spanner connection refused")
			},
			wantStatus: http.StatusServiceUnavailable,
			wantBody:   "spanner connection refused",
		},
		{
			name:       "root fallback probe returns OK without auth",
			path:       "/",
			method:     http.MethodGet,
			wantStatus: http.StatusOK,
			wantBody:   "Dashboard service OK",
		},
		{
			name:       "unauthenticated request to API endpoint is rejected with 401",
			path:       "/v1/dashboards",
			method:     http.MethodGet,
			wantStatus: http.StatusUnauthorized,
		},
		{
			name:       "authenticated request with Bearer token passes to gateway",
			path:       "/v1/dashboards",
			method:     http.MethodGet,
			authHeader: "Bearer " + validToken,
			wantStatus: http.StatusOK,
			wantBody:   `{"dashboards": []}`,
		},
		{
			name:       "authenticated request with session cookie passes to gateway",
			path:       "/v1/dashboards",
			method:     http.MethodGet,
			cookie:     &http.Cookie{Name: "session_token", Value: validToken},
			wantStatus: http.StatusOK,
			wantBody:   `{"dashboards": []}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			handler := setupHTTPHandler(authenticator, dummyGWMux, tt.readinessCheck)

			req := httptest.NewRequest(tt.method, tt.path, nil)
			if tt.authHeader != "" {
				req.Header.Set("Authorization", tt.authHeader)
			}
			if tt.cookie != nil {
				req.AddCookie(tt.cookie)
			}
			rec := httptest.NewRecorder()

			handler.ServeHTTP(rec, req)

			if rec.Code != tt.wantStatus {
				t.Errorf("GET %s status = %d, want %d", tt.path, rec.Code, tt.wantStatus)
			}
			if tt.wantBody != "" && !strings.Contains(rec.Body.String(), tt.wantBody) {
				t.Errorf("GET %s body = %q, want containing %q", tt.path, rec.Body.String(), tt.wantBody)
			}
		})
	}
}

func TestSetupHTTPHandlerSecurityHeaders(t *testing.T) {
	mockAuthenticator := oauth2.NewAuthenticator(&oauth2.Config{
		MockAuth: true,
	})
	dummyGWMux := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	handler := setupHTTPHandler(mockAuthenticator, dummyGWMux, nil)
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Errorf("Expected X-Content-Type-Options: nosniff, got %q", rec.Header().Get("X-Content-Type-Options"))
	}
	if rec.Header().Get("X-Frame-Options") != "DENY" {
		t.Errorf("Expected X-Frame-Options: DENY, got %q", rec.Header().Get("X-Frame-Options"))
	}
}

func TestIncomingHeaderMatcher_Propagation(t *testing.T) {
	mux := runtime.NewServeMux(runtime.WithIncomingHeaderMatcher(oauth2.IncomingHeaderMatcher))

	req := httptest.NewRequest(http.MethodGet, "/v1/dashboards", nil)
	req.Header.Set("x-goog-user-email", "test-user@google.com")
	req.Header.Set("x-goog-iap-jwt-assertion", "jwt-test-assertion")
	req.Header.Set("x-custom-unmatched-header", "drop-me")

	ctx, err := runtime.AnnotateIncomingContext(context.Background(), mux, req, "TestEndpoint")
	if err != nil {
		t.Fatalf("AnnotateIncomingContext failed: %v", err)
	}

	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		t.Fatal("Expected metadata in context, got none")
	}

	if emails := md.Get("x-goog-user-email"); len(emails) != 1 || emails[0] != "test-user@google.com" {
		t.Errorf("Expected x-goog-user-email = %q, got %v", "test-user@google.com", emails)
	}

	if jwts := md.Get("x-goog-iap-jwt-assertion"); len(jwts) != 1 || jwts[0] != "jwt-test-assertion" {
		t.Errorf("Expected x-goog-iap-jwt-assertion = %q, got %v", "jwt-test-assertion", jwts)
	}

	if dropped := md.Get("x-custom-unmatched-header"); len(dropped) > 0 {
		t.Errorf("Expected x-custom-unmatched-header to be dropped, got %v", dropped)
	}
}

func TestInitDataEngine_GracefulFallback(t *testing.T) {
	// In test environments without ClickHouse running, initDataEngine should gracefully
	// return nil without panicking or failing fatally.
	ctx := context.Background()
	_ = initDataEngine(ctx)
}

func TestChartDataEngine_NilEngineStaysNilInterface(t *testing.T) {
	// A nil *DataEngine must not become a non-nil interface holding a nil pointer: the server's
	// "data engine is not configured" guard compares against nil, and would let the request through.
	// The variable is typed explicitly so that the comparison is the one the server actually makes.
	var got server.ChartDataEngine = chartDataEngine(nil)

	if got != nil {
		t.Errorf("chartDataEngine(nil) = %v, want a nil interface", got)
	}
}

func TestChartDataEngine_WrapsEngine(t *testing.T) {
	engine := &dataengine.DataEngine{}

	got := chartDataEngine(engine)

	if got != engine {
		t.Errorf("chartDataEngine(engine) = %v, want the engine itself", got)
	}
}

func TestInitXidClient(t *testing.T) {
	client, conn, err := initXidClient("localhost:8082")
	if err != nil {
		t.Fatalf("initXidClient failed: %v", err)
	}
	if client == nil {
		t.Fatal("expected non-nil XidServiceClient")
	}
	if conn != nil {
		_ = conn.Close()
	}
}

func TestGracefulShutdown_Signal(t *testing.T) {
	httpServer := &http.Server{
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		}),
	}
	grpcServer := grpc.NewServer()

	quit := make(chan os.Signal, 1)
	errChan := make(chan error, 2)

	// Send signal immediately to trigger shutdown
	quit <- syscall.SIGTERM

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	done := make(chan struct{})
	go func() {
		gracefulShutdown(ctx, httpServer, grpcServer, quit, errChan)
		close(done)
	}()

	select {
	case <-done:
		// Succeeded cleanly
	case <-time.After(3 * time.Second):
		t.Fatal("gracefulShutdown timed out on signal")
	}
}

func TestGracefulShutdown_ServerError(t *testing.T) {
	httpServer := &http.Server{
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		}),
	}
	grpcServer := grpc.NewServer()

	quit := make(chan os.Signal, 1)
	errChan := make(chan error, 2)

	// Send error to trigger shutdown
	errChan <- errors.New("simulated fatal server error")

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	done := make(chan struct{})
	go func() {
		gracefulShutdown(ctx, httpServer, grpcServer, quit, errChan)
		close(done)
	}()

	select {
	case <-done:
		// Succeeded cleanly
	case <-time.After(3 * time.Second):
		t.Fatal("gracefulShutdown timed out on error")
	}
}

func TestInitSpannerClient_EmptyDatabaseName(t *testing.T) {
	ctx := context.Background()
	_, err := initSpannerClient(ctx, "", "proj")
	if err == nil {
		t.Fatal("expected error for empty database name, got nil")
	}
}

func TestInitRepository_NilClient(t *testing.T) {
	ctx := context.Background()
	_, err := initRepository(ctx, nil)
	if err == nil {
		t.Fatal("expected error for nil spanner client, got nil")
	}
}
