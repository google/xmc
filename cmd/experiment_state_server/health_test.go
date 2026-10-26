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
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	xidgrpcpb "xmanager_cloud/api/protos/xid"
	"xmanager_cloud/cmd/experiment_state_server/config"
	commonhealth "xmanager_cloud/internal/common/health"
	oauth "xmanager_cloud/pkg/auth/oauth2"
)

type fakeXidClient struct {
	xidgrpcpb.XidServiceClient
}

type fakeTaskQueueClient struct {
	PingErr error
}

func (f *fakeTaskQueueClient) PingRedis(ctx context.Context) error {
	return f.PingErr
}

type fakeReadinessChecker struct {
	err error
}

func (f fakeReadinessChecker) CheckReady(ctx context.Context) error {
	return f.err
}

func TestActiveHealthCheck(t *testing.T) {
	tests := []struct {
		name     string
		mockDB   commonhealth.ReadinessChecker
		setupTq  func() TaskQueueClient
		wantCode int
		wantBody string
	}{
		{
			name:   "redis_ping_error",
			mockDB: fakeReadinessChecker{},
			setupTq: func() TaskQueueClient {
				return &fakeTaskQueueClient{PingErr: fmt.Errorf("fake redis error")}
			},
			wantCode: http.StatusInternalServerError,
			wantBody: "fake redis error",
		},
		{
			name:   "all_valid",
			mockDB: fakeReadinessChecker{},
			setupTq: func() TaskQueueClient {
				return &fakeTaskQueueClient{}
			},
			wantCode: http.StatusOK,
			wantBody: "OK",
		},
		{
			name:     "nil_database",
			mockDB:   nil,
			setupTq:  func() TaskQueueClient { return &fakeTaskQueueClient{} },
			wantCode: http.StatusInternalServerError,
			wantBody: "database: client is nil",
		},
		{
			name: "database_not_ready",
			mockDB: fakeReadinessChecker{
				err: fmt.Errorf("schema version 1 does not match expected version 2"),
			},
			setupTq:  func() TaskQueueClient { return &fakeTaskQueueClient{} },
			wantCode: http.StatusInternalServerError,
			wantBody: "does not match expected version",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var tqClient TaskQueueClient
			if tc.setupTq != nil {
				tqClient = tc.setupTq()
			}

			var xidClient xidgrpcpb.XidServiceClient = &fakeXidClient{}
			handler := activeHealthCheck(tc.mockDB, xidClient, tqClient)

			req := httptest.NewRequest("GET", "/readyz", nil)
			rr := httptest.NewRecorder()

			handler.ServeHTTP(rr, req)

			if rr.Code != tc.wantCode {
				t.Errorf("activeHealthCheck() status code = %v, want %v", rr.Code, tc.wantCode)
			}

			gotBody := rr.Body.String()
			if tc.wantBody != "" {
				if !strings.Contains(gotBody, tc.wantBody) {
					t.Errorf("activeHealthCheck() body = %q, want substring %q", gotBody, tc.wantBody)
				}
			}
		})
	}
}

func TestLivenessCheck(t *testing.T) {
	handler := livenessCheck()

	req := httptest.NewRequest("GET", "/healthz", nil)
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("livenessCheck() status code = %v, want %v", rr.Code, http.StatusOK)
	}

	gotBody := rr.Body.String()
	wantBody := "OK"
	if gotBody != wantBody {
		t.Errorf("livenessCheck() body = %q, want %q", gotBody, wantBody)
	}
}

type stubUserAuth struct{ email string }

func (s stubUserAuth) AuthenticateUserFromContext(context.Context) (string, string, error) {
	return s.email, "uid-1", nil
}

type stubAdminChecker struct {
	isAdmin bool
	err     error
}

func (s stubAdminChecker) IsAdmin(context.Context, string) (bool, error) { return s.isAdmin, s.err }

func TestBuildCMSAuthorizer(t *testing.T) {
	cfg := &config.Config{InvokerSA: "bridge@proj.iam.gserviceaccount.com"}
	ctx := context.Background()

	t.Setenv("DISABLE_AUTHENTICATION", "true")
	if _, err := buildCMSAuthorizer(cfg, &oauth.Config{}, nil, nil).AuthorizeMutating(ctx); err != nil {
		t.Fatalf("AuthorizeMutating() with DISABLE_AUTHENTICATION=true error = %v", err)
	}

	t.Setenv("DISABLE_AUTHENTICATION", "false")
	nonProdAuth := buildCMSAuthorizer(cfg, &oauth.Config{AuthURL: "https://dex/auth"}, stubUserAuth{email: "viewer@xmc.local"}, stubAdminChecker{err: fmt.Errorf("crm down")})
	if _, err := nonProdAuth.AuthorizeMutating(ctx); err == nil {
		t.Fatal("AuthorizeMutating() expected PermissionDenied error, got nil")
	}

	prodAuth := buildCMSAuthorizer(cfg, &oauth.Config{AdminIAMRole: "roles/owner"}, stubUserAuth{email: "admin@example.com"}, stubAdminChecker{isAdmin: true})
	if _, err := prodAuth.AuthorizeMutating(ctx); err != nil {
		t.Fatalf("AuthorizeMutating() unexpected error = %v", err)
	}
}

func TestCMSAdmission(t *testing.T) {
	const invokerSA = "xmc-wl-sa@proj.iam.gserviceaccount.com"
	tests := []struct {
		name     string
		oauthCfg *oauth.Config
		email    string
		want     bool
	}{
		{name: "admits the bridge service account", oauthCfg: &oauth.Config{AllowedEmails: []string{"alice@example.com"}}, email: "XMC-WL-SA@proj.iam.gserviceaccount.com", want: true},
		{name: "admits deployment principals", oauthCfg: &oauth.Config{AllowedEmails: []string{"alice@example.com"}}, email: "alice@example.com", want: true},
		{name: "rejects everyone else", oauthCfg: &oauth.Config{AllowedEmails: []string{"alice@example.com"}}, email: "mallory@example.com", want: false},
		{name: "disabled authentication admits all", oauthCfg: &oauth.Config{DisableAuthentication: true}, email: "anyone@example.com", want: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			admission := cmsAdmission(invokerSA, oauth.NewAuthenticator(tt.oauthCfg))
			got, err := admission.IsUserAuthorized(context.Background(), tt.email)
			if err != nil {
				t.Fatalf("IsUserAuthorized(%q) error = %v", tt.email, err)
			}
			if got != tt.want {
				t.Errorf("IsUserAuthorized(%q) = %v, want %v", tt.email, got, tt.want)
			}
		})
	}
}
