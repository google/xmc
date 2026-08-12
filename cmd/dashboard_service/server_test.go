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
	"strings"
	"testing"
	"time"

	mpb "xmanager_cloud/dashboard_service/proto/api_go_proto"
	spb "xmanager_cloud/dashboard_service/proto/service_go_proto"
	"xmanager_cloud/pkg/auth/oauth2"
)

func TestCreateDashboard(t *testing.T) {
	tests := []struct {
		name     string
		req      *spb.CreateDashboardRequest
		wantName string
	}{
		{
			name: "create dashboard",
			req: &spb.CreateDashboardRequest{
				Dashboard: &mpb.Dashboard{Name: "dashboards/new", Title: "New Dashboard"},
			},
			wantName: "dashboards/new",
		},
	}

	s := newServer(LoadConfig(), nil, nil)
	ctx := context.Background()

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp, err := s.CreateDashboard(ctx, tt.req)
			if err != nil {
				t.Fatalf("CreateDashboard failed: %v", err)
			}
			if resp.GetName() != tt.wantName {
				t.Errorf("CreateDashboard().Name = %q, want %q", resp.GetName(), tt.wantName)
			}
		})
	}
}

func TestGetDashboard(t *testing.T) {
	tests := []struct {
		name     string
		req      *spb.GetDashboardRequest
		wantName string
	}{
		{
			name:     "get sample dashboard",
			req:      &spb.GetDashboardRequest{Name: "dashboards/123"},
			wantName: "dashboards/123",
		},
	}

	s := newServer(LoadConfig(), nil, nil)
	ctx := context.Background()

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp, err := s.GetDashboard(ctx, tt.req)
			if err != nil {
				t.Fatalf("GetDashboard failed: %v", err)
			}
			if resp.GetName() != tt.wantName {
				t.Errorf("GetDashboard().Name = %q, want %q", resp.GetName(), tt.wantName)
			}
		})
	}
}

func TestListDashboards(t *testing.T) {
	s := newServer(LoadConfig(), nil, nil)
	ctx := context.Background()

	resp, err := s.ListDashboards(ctx, &spb.ListDashboardsRequest{})
	if err != nil {
		t.Fatalf("ListDashboards failed: %v", err)
	}
	if len(resp.GetDashboards()) != 0 {
		t.Errorf("ListDashboards() returned %d dashboards, want 0", len(resp.GetDashboards()))
	}
}

func TestUpdateDashboard(t *testing.T) {
	tests := []struct {
		name     string
		req      *spb.UpdateDashboardRequest
		wantName string
	}{
		{
			name: "update dashboard",
			req: &spb.UpdateDashboardRequest{
				Dashboard: &mpb.Dashboard{Name: "dashboards/123", Title: "Updated Dashboard"},
			},
			wantName: "dashboards/123",
		},
	}

	s := newServer(LoadConfig(), nil, nil)
	ctx := context.Background()

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp, err := s.UpdateDashboard(ctx, tt.req)
			if err != nil {
				t.Fatalf("UpdateDashboard failed: %v", err)
			}
			if resp.GetName() != tt.wantName {
				t.Errorf("UpdateDashboard().Name = %q, want %q", resp.GetName(), tt.wantName)
			}
		})
	}
}

func TestDeleteDashboard(t *testing.T) {
	s := newServer(LoadConfig(), nil, nil)
	ctx := context.Background()

	_, err := s.DeleteDashboard(ctx, &spb.DeleteDashboardRequest{Name: "dashboards/123"})
	if err != nil {
		t.Fatalf("DeleteDashboard failed: %v", err)
	}
}

func TestCreateChart(t *testing.T) {
	tests := []struct {
		name     string
		req      *spb.CreateChartRequest
		wantName string
	}{
		{
			name: "create chart",
			req: &spb.CreateChartRequest{
				Chart: &mpb.Chart{Name: "dashboards/123/charts/new", Title: "New Chart"},
			},
			wantName: "dashboards/123/charts/new",
		},
	}

	s := newServer(LoadConfig(), nil, nil)
	ctx := context.Background()

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp, err := s.CreateChart(ctx, tt.req)
			if err != nil {
				t.Fatalf("CreateChart failed: %v", err)
			}
			if resp.GetName() != tt.wantName {
				t.Errorf("CreateChart().Name = %q, want %q", resp.GetName(), tt.wantName)
			}
		})
	}
}

func TestGetChart(t *testing.T) {
	tests := []struct {
		name     string
		req      *spb.GetChartRequest
		wantName string
	}{
		{
			name:     "get sample chart",
			req:      &spb.GetChartRequest{Name: "dashboards/123/charts/456"},
			wantName: "dashboards/123/charts/456",
		},
	}

	s := newServer(LoadConfig(), nil, nil)
	ctx := context.Background()

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp, err := s.GetChart(ctx, tt.req)
			if err != nil {
				t.Fatalf("GetChart failed: %v", err)
			}
			if resp.GetName() != tt.wantName {
				t.Errorf("GetChart().Name = %q, want %q", resp.GetName(), tt.wantName)
			}
		})
	}
}

func TestGetChartData(t *testing.T) {
	s := newServer(LoadConfig(), nil, nil)
	ctx := context.Background()

	resp, err := s.GetChartData(ctx, &spb.GetChartDataRequest{Name: "dashboards/123/charts/456"})
	if err != nil {
		t.Fatalf("GetChartData failed: %v", err)
	}
	if resp == nil {
		t.Error("GetChartData returned nil response")
	}
}

func TestListCharts(t *testing.T) {
	s := newServer(LoadConfig(), nil, nil)
	ctx := context.Background()

	resp, err := s.ListCharts(ctx, &spb.ListChartsRequest{Parent: "dashboards/123"})
	if err != nil {
		t.Fatalf("ListCharts failed: %v", err)
	}
	if len(resp.GetCharts()) == 0 {
		t.Error("ListCharts returned zero charts, expected at least 1")
	}
}

func TestUpdateChart(t *testing.T) {
	tests := []struct {
		name     string
		req      *spb.UpdateChartRequest
		wantName string
	}{
		{
			name: "update chart",
			req: &spb.UpdateChartRequest{
				Chart: &mpb.Chart{Name: "dashboards/123/charts/456", Title: "Updated Chart"},
			},
			wantName: "dashboards/123/charts/456",
		},
	}

	s := newServer(LoadConfig(), nil, nil)
	ctx := context.Background()

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			resp, err := s.UpdateChart(ctx, tt.req)
			if err != nil {
				t.Fatalf("UpdateChart failed: %v", err)
			}
			if resp.GetName() != tt.wantName {
				t.Errorf("UpdateChart().Name = %q, want %q", resp.GetName(), tt.wantName)
			}
		})
	}
}

func TestDeleteChart(t *testing.T) {
	s := newServer(LoadConfig(), nil, nil)
	ctx := context.Background()

	_, err := s.DeleteChart(ctx, &spb.DeleteChartRequest{Name: "dashboards/123/charts/456"})
	if err != nil {
		t.Fatalf("DeleteChart failed: %v", err)
	}
}

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
		name       string
		path       string
		method     string
		authHeader string
		cookie     *http.Cookie
		wantStatus int
		wantBody   string
	}{
		{
			name:       "healthz probe returns OK without auth",
			path:       "/healthz",
			method:     http.MethodGet,
			wantStatus: http.StatusOK,
			wantBody:   "OK",
		},
		{
			name:       "readyz probe returns OK without auth",
			path:       "/readyz",
			method:     http.MethodGet,
			wantStatus: http.StatusOK,
			wantBody:   "OK",
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

	handler := setupHTTPHandler(authenticator, dummyGWMux)

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
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

	handler := setupHTTPHandler(mockAuthenticator, dummyGWMux)
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
