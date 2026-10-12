// Copyright 2026 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//      http://www.apache.org/licenses/LICENSE-2.0
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
	"testing"

	"cloud.google.com/go/iam/apiv1/iampb"
	"github.com/google/xmc/pkg/auth/oauth2"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type testAuthorizer struct {
	email    string
	userID   string
	authnErr error
}

func (m *testAuthorizer) AuthenticateUserFromContext(ctx context.Context) (string, string, error) {
	if m.authnErr != nil {
		return "", "", m.authnErr
	}
	return m.email, m.userID, nil
}

func newTestAuthenticator(adminRole string, disableAuth bool, policy *iampb.Policy, fetchErr error) *oauth2.Authenticator {
	a := oauth2.NewAuthenticator(&oauth2.Config{
		GCPProjectID:          "test-project",
		AdminIAMRole:          adminRole,
		DisableAuthentication: disableAuth,
	})
	a.PolicyFetcher = func(ctx context.Context) (*iampb.Policy, error) {
		if fetchErr != nil {
			return nil, fetchErr
		}
		return policy, nil
	}
	return a
}

func TestAdminRoleGuard_HasAdminRole(t *testing.T) {
	adminPolicy := &iampb.Policy{
		Bindings: []*iampb.Binding{
			{
				Role:    "roles/owner",
				Members: []string{"user:admin@company.com"},
			},
		},
	}

	tests := []struct {
		name        string
		params      AdminRoleGuardParams
		wantEmail   string
		wantHasRole bool
		wantErr     bool
	}{
		{
			name: "user has admin IAM role",
			params: AdminRoleGuardParams{
				Authorizer:    &testAuthorizer{email: "admin@company.com", userID: "123"},
				Authenticator: newTestAuthenticator("roles/owner", false, adminPolicy, nil),
			},
			wantEmail:   "admin@company.com",
			wantHasRole: true,
			wantErr:     false,
		},
		{
			name: "user lacks admin IAM role",
			params: AdminRoleGuardParams{
				Authorizer:    &testAuthorizer{email: "viewer@company.com", userID: "456"},
				Authenticator: newTestAuthenticator("roles/owner", false, adminPolicy, nil),
			},
			wantEmail:   "viewer@company.com",
			wantHasRole: false,
			wantErr:     false,
		},
		{
			name: "disable authentication grants admin role",
			params: AdminRoleGuardParams{
				Authorizer:    &testAuthorizer{email: "dev@company.com", userID: "789"},
				Authenticator: newTestAuthenticator("roles/owner", true, nil, nil),
			},
			wantEmail:   "dev@company.com",
			wantHasRole: true,
			wantErr:     false,
		},
		{
			name: "IAM policy fetch error returns error",
			params: AdminRoleGuardParams{
				Authorizer:    &testAuthorizer{email: "admin@company.com", userID: "123"},
				Authenticator: newTestAuthenticator("roles/owner", false, nil, errors.New("IAM API error")),
			},
			wantEmail:   "admin@company.com",
			wantHasRole: false,
			wantErr:     true,
		},
		{
			name: "unauthenticated context returns error",
			params: AdminRoleGuardParams{
				Authorizer:    &testAuthorizer{authnErr: status.Errorf(codes.Unauthenticated, "invalid token")},
				Authenticator: newTestAuthenticator("roles/owner", false, adminPolicy, nil),
			},
			wantEmail:   "",
			wantHasRole: false,
			wantErr:     true,
		},
		{
			name: "nil authorizer returns error",
			params: AdminRoleGuardParams{
				Authorizer:    nil,
				Authenticator: newTestAuthenticator("roles/owner", false, adminPolicy, nil),
			},
			wantEmail:   "",
			wantHasRole: false,
			wantErr:     true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			guard := NewAdminRoleGuard(tt.params)
			email, hasRole, err := guard.HasAdminRole(context.Background())
			if (err != nil) != tt.wantErr {
				t.Fatalf("HasAdminRole() error = %v, wantErr %v", err, tt.wantErr)
			}
			if email != tt.wantEmail {
				t.Errorf("HasAdminRole() email = %q, want %q", email, tt.wantEmail)
			}
			if hasRole != tt.wantHasRole {
				t.Errorf("HasAdminRole() hasRole = %v, want %v", hasRole, tt.wantHasRole)
			}
		})
	}
}

func TestAdminRoleGuard_CheckAdminRole(t *testing.T) {
	adminPolicy := &iampb.Policy{
		Bindings: []*iampb.Binding{
			{
				Role:    "roles/owner",
				Members: []string{"user:admin@company.com"},
			},
		},
	}

	tests := []struct {
		name     string
		params   AdminRoleGuardParams
		wantCode codes.Code
	}{
		{
			name: "authorized admin returns OK",
			params: AdminRoleGuardParams{
				Authorizer:    &testAuthorizer{email: "admin@company.com", userID: "123"},
				Authenticator: newTestAuthenticator("roles/owner", false, adminPolicy, nil),
			},
			wantCode: codes.OK,
		},
		{
			name: "unauthorized user returns PermissionDenied",
			params: AdminRoleGuardParams{
				Authorizer:    &testAuthorizer{email: "viewer@company.com", userID: "456"},
				Authenticator: newTestAuthenticator("roles/owner", false, adminPolicy, nil),
			},
			wantCode: codes.PermissionDenied,
		},
		{
			name: "unauthenticated caller returns Unauthenticated",
			params: AdminRoleGuardParams{
				Authorizer:    &testAuthorizer{authnErr: status.Errorf(codes.Unauthenticated, "token missing")},
				Authenticator: newTestAuthenticator("roles/owner", false, adminPolicy, nil),
			},
			wantCode: codes.Unauthenticated,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			guard := NewAdminRoleGuard(tt.params)
			err := guard.CheckAdminRole(context.Background())
			if status.Code(err) != tt.wantCode {
				t.Errorf("CheckAdminRole() status code = %v, want %v (err: %v)", status.Code(err), tt.wantCode, err)
			}
		})
	}
}
