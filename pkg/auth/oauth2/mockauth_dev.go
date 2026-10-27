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

//go:build mockauth

package oauth2

import (
	"context"
	"fmt"
	"net/http"

	"cloud.google.com/go/iam/apiv1/iampb"
	xoauth2 "golang.org/x/oauth2"
	"google.golang.org/api/idtoken"
)

// mockAuthCompiledIn reports that this binary was built with the mockauth tag, so LoadConfig
// accepts MOCK_AUTH.
const mockAuthCompiledIn = true

// devOnlyJWTSecret is a well-known, non-secret value used to sign session tokens
// only in local/dev mode (MOCK_AUTH or DISABLE_AUTHENTICATION). It must never be
// used when authentication is enabled: when JWT_SECRET is unset in that case,
// LoadConfig fails fast instead of silently signing tokens with this known value.
// It is compiled only into binaries built with the mockauth tag.
const devOnlyJWTSecret = "default-jwt-secret-for-local-dev-only-12345"

// MockOAuthProvider implements OAuthProvider with hardcoded credentials for local dev/testing.
type MockOAuthProvider struct {
	MockEmail string
	MockName  string
	MockPic   string
}

// AuthCodeURL returns the mock callback URL.
func (m *MockOAuthProvider) AuthCodeURL(state string, r *http.Request, opts ...xoauth2.AuthCodeOption) string {
	return fmt.Sprintf("/api/callback?code=mock_code_123&state=%s", state)
}

// ExchangeAndGetUserInfo returns mock user details directly without performing external HTTP network calls.
func (m *MockOAuthProvider) ExchangeAndGetUserInfo(ctx context.Context, r *http.Request, code string) (*GoogleUserInfo, error) {
	return &GoogleUserInfo{
		Email:         m.MockEmail,
		EmailVerified: true,
		Name:          m.MockName,
		Picture:       m.MockPic,
	}, nil
}

// configureMockIfEnabled installs the mock OAuth provider, ID token validator and IAM policy on a
// when MOCK_AUTH is set in binaries built with the mockauth tag.
func configureMockIfEnabled(a *Authenticator) {
	if a.Config != nil && a.Config.MockAuth {
		ConfigureMockAuth(a)
	}
}

// ConfigureMockAuth configures the authenticator with mock implementations for local dev/testing.
func ConfigureMockAuth(a *Authenticator) {
	a.OAuthProvider = &MockOAuthProvider{
		MockEmail: "mock-user@mycompany.com",
		MockName:  "Mock User",
		MockPic:   "https://example.com/mock-avatar.png",
	}
	a.IDTokenValidator = func(ctx context.Context, idToken string, audience string) (*idtoken.Payload, error) {
		if idToken == "mock-sa-token" {
			return &idtoken.Payload{
				Issuer:   "google-service-account",
				Audience: audience,
				Claims: map[string]interface{}{
					"email":   "service-account@mycompany.com",
					"name":    "Mock Service Account",
					"picture": "https://example.com/sa-avatar.png",
				},
			}, nil
		}
		return nil, fmt.Errorf("invalid token: %s", idToken)
	}
	a.PolicyFetcher = func(ctx context.Context) (*iampb.Policy, error) {
		adminRole := a.Config.AdminIAMRole
		if adminRole == "" {
			adminRole = "roles/owner"
		}
		return &iampb.Policy{
			Bindings: []*iampb.Binding{
				{
					Role:    a.Config.TargetIAMRole,
					Members: []string{"user:mock-admin@mycompany.com", "user:mock-sa-user@mycompany.com", "serviceaccount:service-account@mycompany.com"},
				},
				{
					Role:    adminRole,
					Members: []string{"user:mock-admin@mycompany.com"},
				},
			},
		}, nil
	}
}
