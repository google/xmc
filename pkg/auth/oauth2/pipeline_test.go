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

package oauth2

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"cloud.google.com/go/iam/apiv1/iampb"
)

func TestIamPolicyCachingAndClientReuse(t *testing.T) {
	cfg := &Config{
		GCPProjectID:  "test-project-id",
		TargetIAMRole: "roles/viewer",
	}

	a := NewAuthenticator(cfg)

	// Set up a mock fetcher to count calls
	var fetchCount int64
	mockPolicy := &iampb.Policy{
		Bindings: []*iampb.Binding{
			{
				Role:    "roles/viewer",
				Members: []string{"user:allowed@company.com"},
			},
		},
	}

	a.PolicyFetcher = func(ctx context.Context) (*iampb.Policy, error) {
		atomic.AddInt64(&fetchCount, 1)
		return mockPolicy, nil
	}

	ctx := context.Background()

	// 1. Initial lookup - should fetch from mock API
	authorized, err := a.IsUserAuthorizedViaIAM(ctx, "allowed@company.com")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !authorized {
		t.Errorf("expected user to be authorized")
	}

	if count := atomic.LoadInt64(&fetchCount); count != 1 {
		t.Errorf("expected PolicyFetcher to be called once, got %d", count)
	}

	// 2. Immediate second lookup - should use cached results, fetchCount should remain 1
	authorized, err = a.IsUserAuthorizedViaIAM(ctx, "allowed@company.com")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !authorized {
		t.Errorf("expected user to be authorized")
	}

	if count := atomic.LoadInt64(&fetchCount); count != 1 {
		t.Errorf("expected PolicyFetcher to still be called once (cached), got %d", count)
	}

	// 3. Different user lookup - should still use cached policy (fetchCount remains 1)
	unauthorized, err := a.IsUserAuthorizedViaIAM(ctx, "unauthorized@company.com")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if unauthorized {
		t.Errorf("expected user to be unauthorized")
	}

	if count := atomic.LoadInt64(&fetchCount); count != 1 {
		t.Errorf("expected PolicyFetcher to still be called once (cached for project-wide policy), got %d", count)
	}

	// 4. Force expiration of cache and lookup - should fetch again (fetchCount becomes 2)
	a.mu.Lock()
	a.policyCache.expiry = time.Now().Add(-1 * time.Second)
	a.mu.Unlock()

	authorized, err = a.IsUserAuthorizedViaIAM(ctx, "allowed@company.com")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !authorized {
		t.Errorf("expected user to be authorized")
	}

	if count := atomic.LoadInt64(&fetchCount); count != 2 {
		t.Errorf("expected PolicyFetcher to be called twice after cache expired, got %d", count)
	}

	// Test Close() does not error when projectsClient is nil (not initialized because policyFetcher was mocked)
	if err := a.Close(); err != nil {
		t.Errorf("unexpected error on Close: %v", err)
	}
}

func TestIsUserAuthorizedForRoleViaIAM(t *testing.T) {
	cfg := &Config{
		GCPProjectID:  "test-project-id",
		TargetIAMRole: "roles/viewer",
		AdminIAMRole:  "roles/owner",
	}

	a := NewAuthenticator(cfg)

	mockPolicy := &iampb.Policy{
		Bindings: []*iampb.Binding{
			{
				Role:    "roles/viewer",
				Members: []string{"user:viewer@company.com"},
			},
			{
				Role:    "roles/owner",
				Members: []string{"user:owner@company.com"},
			},
		},
	}

	a.PolicyFetcher = func(ctx context.Context) (*iampb.Policy, error) {
		return mockPolicy, nil
	}

	ctx := context.Background()

	tests := []struct {
		name    string
		email   string
		role    string
		want    bool
		wantErr bool
	}{
		{
			name:    "viewer email has viewer role",
			email:   "viewer@company.com",
			role:    "roles/viewer",
			want:    true,
			wantErr: false,
		},
		{
			name:    "viewer email with uppercase letters has viewer role",
			email:   "Viewer@Company.com",
			role:    "roles/viewer",
			want:    true,
			wantErr: false,
		},
		{
			name:    "viewer email with whitespace and uppercase has viewer role",
			email:   "  Viewer@Company.com  ",
			role:    "roles/viewer",
			want:    true,
			wantErr: false,
		},
		{
			name:    "viewer email does not have owner role",
			email:   "viewer@company.com",
			role:    "roles/owner",
			want:    false,
			wantErr: false,
		},
		{
			name:    "owner email has owner role",
			email:   "owner@company.com",
			role:    "roles/owner",
			want:    true,
			wantErr: false,
		},
		{
			name:    "owner email does not have viewer role",
			email:   "owner@company.com",
			role:    "roles/viewer",
			want:    false,
			wantErr: false,
		},
		{
			name:    "empty role returns false",
			email:   "owner@company.com",
			role:    "",
			want:    false,
			wantErr: false,
		},
		{
			name:    "whitespace role returns false",
			email:   "owner@company.com",
			role:    "   ",
			want:    false,
			wantErr: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := a.IsUserAuthorizedForRoleViaIAM(ctx, tt.email, tt.role)
			if (err != nil) != tt.wantErr {
				t.Errorf("IsUserAuthorizedForRoleViaIAM() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if got != tt.want {
				t.Errorf("IsUserAuthorizedForRoleViaIAM() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestIsAdmin(t *testing.T) {
	mockPolicy := &iampb.Policy{
		Bindings: []*iampb.Binding{
			{
				Role:    "roles/owner",
				Members: []string{"user:admin@company.com"},
			},
		},
	}

	// DisableAuthentication is covered by TestDisableAuthentication* in mockauth_{dev,prod}_test.go.

	t.Run("returns error when AdminIAMRole is not configured", func(t *testing.T) {
		a := NewAuthenticator(&Config{AdminIAMRole: ""})
		_, err := a.IsAdmin(context.Background(), "admin@company.com")
		if !errors.Is(err, ErrAdminRoleNotConfigured) {
			t.Errorf("IsAdmin() error = %v, want %v", err, ErrAdminRoleNotConfigured)
		}
	})

	t.Run("evaluates role when AdminIAMRole configured", func(t *testing.T) {
		a := NewAuthenticator(&Config{GCPProjectID: "test-proj", AdminIAMRole: "roles/owner"})
		a.PolicyFetcher = func(ctx context.Context) (*iampb.Policy, error) {
			return mockPolicy, nil
		}

		gotAdmin, err := a.IsAdmin(context.Background(), "admin@company.com")
		if err != nil || !gotAdmin {
			t.Errorf("IsAdmin(admin) = (%v, %v), want (true, nil)", gotAdmin, err)
		}

		gotNonAdmin, err := a.IsAdmin(context.Background(), "user@company.com")
		if err != nil || gotNonAdmin {
			t.Errorf("IsAdmin(user) = (%v, %v), want (false, nil)", gotNonAdmin, err)
		}
	})
}

func TestEmailDomain(t *testing.T) {
	tests := []struct {
		name  string
		email string
		want  string
	}{
		{name: "address", email: "user@mycompany.com", want: "mycompany.com"},
		{name: "no at sign", email: "mycompany.com", want: ""},
		{name: "two at signs", email: "a@b@mycompany.com", want: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := emailDomain(tt.email); got != tt.want {
				t.Errorf("emailDomain(%q) = %q, want %q", tt.email, got, tt.want)
			}
		})
	}
}

func TestIsAllowedDomain(t *testing.T) {
	a := NewAuthenticator(&Config{AllowedDomains: []string{" MyCompany.com ", "other.org"}})
	tests := []struct {
		name   string
		domain string
		want   bool
	}{
		{name: "allowlisted domain", domain: "mycompany.com", want: true},
		{name: "case and spaces are ignored", domain: " MYCOMPANY.com", want: true},
		{name: "second allowlisted domain", domain: "other.org", want: true},
		{name: "other domain", domain: "example.com", want: false},
		{name: "subdomain", domain: "eng.mycompany.com", want: false},
		{name: "empty", domain: "", want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := a.isAllowedDomain(tt.domain); got != tt.want {
				t.Errorf("isAllowedDomain(%q) = %v, want %v", tt.domain, got, tt.want)
			}
		})
	}
}
