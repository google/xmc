// Copyright 2026 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package bridge

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"golang.org/x/oauth2"
	"google.golang.org/api/idtoken"
	"google.golang.org/grpc/credentials"

	xmcoauth "xmanager_cloud/pkg/auth/oauth2"
)

type staticTokenSource struct {
	token string
	err   error
}

func (s staticTokenSource) Token() (*oauth2.Token, error) {
	if s.err != nil {
		return nil, s.err
	}
	return &oauth2.Token{AccessToken: s.token}, nil
}

func TestBearerTokenCredentials(t *testing.T) {
	ctx := context.Background()
	if NewBearerTokenCredentials(nil).RequireTransportSecurity() {
		t.Error("RequireTransportSecurity() = true, want false")
	}

	tests := []struct {
		name    string
		fn      TokenSourceFunc
		wantHdr string
		wantErr bool
	}{
		{name: "nil tokenFn", fn: nil, wantHdr: ""},
		{name: "empty token", fn: func(context.Context) (string, error) { return "", nil }, wantHdr: ""},
		{name: "valid token", fn: func(context.Context) (string, error) { return "my-id-token", nil }, wantHdr: "Bearer my-id-token"},
		{name: "tokenFn error", fn: func(context.Context) (string, error) { return "", errors.New("boom") }, wantErr: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			md, err := NewBearerTokenCredentials(tc.fn).GetRequestMetadata(ctx)
			if (err != nil) != tc.wantErr {
				t.Fatalf("GetRequestMetadata() err = %v, wantErr %v", err, tc.wantErr)
			}
			if got := md["authorization"]; got != tc.wantHdr {
				t.Errorf("authorization = %q, want %q", got, tc.wantHdr)
			}
		})
	}
}

// mustCMSTokenCredentials resolves CMS credentials with factory, failing the test on error.
func mustCMSTokenCredentials(t *testing.T, factory idTokenSourceFactory) credentials.PerRPCCredentials {
	t.Helper()
	creds, err := newCMSTokenCredentialsWithFactory(context.Background(), factory)
	if err != nil {
		t.Fatalf("newCMSTokenCredentialsWithFactory() error = %v", err)
	}
	return creds
}

func TestNewCMSTokenCredentials(t *testing.T) {
	ctx := context.Background()

	t.Run("DISABLE_AUTHENTICATION=true returns no-op credentials", func(t *testing.T) {
		t.Setenv("DISABLE_AUTHENTICATION", "true")
		md, err := mustCMSTokenCredentials(t, idtoken.NewTokenSource).GetRequestMetadata(ctx)
		if err != nil || md != nil {
			t.Fatalf("GetRequestMetadata() = (%v, %v), want (nil, nil)", md, err)
		}
	})

	t.Run("non-prod OIDC token provider fetches ID token", func(t *testing.T) {
		ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_ = json.NewEncoder(w).Encode(map[string]any{"id_token": "dex-oidc-token", "expires_in": 3600})
		}))
		defer ts.Close()

		t.Setenv("DISABLE_AUTHENTICATION", "false")
		t.Setenv("MOCK_AUTH", "false")
		t.Setenv("AUTH_URL", ts.URL+"/auth")
		t.Setenv("TOKEN_URL", ts.URL+"/token")
		t.Setenv("CLIENT_ID", "xmc-local")
		t.Setenv("CLIENT_SECRET", "secret")
		t.Setenv("JWT_SECRET", "0123456789abcdef0123456789abcdef")
		t.Setenv("OIDC_USERNAME", "admin@xmc.local")
		t.Setenv("OIDC_PASSWORD", "password")

		md, err := mustCMSTokenCredentials(t, idtoken.NewTokenSource).GetRequestMetadata(ctx)
		if err != nil || md["authorization"] != "Bearer dex-oidc-token" {
			t.Fatalf("GetRequestMetadata() = (%v, %v), want Bearer dex-oidc-token", md, err)
		}
	})

	t.Run("non-prod without OIDC_USERNAME/OIDC_PASSWORD falls back to session token", func(t *testing.T) {
		t.Setenv("DISABLE_AUTHENTICATION", "false")
		t.Setenv("MOCK_AUTH", "false")
		t.Setenv("AUTH_URL", "http://dex.dex.svc.cluster.local:5556/dex/auth")
		t.Setenv("TOKEN_URL", "http://dex.dex.svc.cluster.local:5556/dex/token")
		t.Setenv("CLIENT_ID", "xmc-client")
		t.Setenv("JWT_SECRET", "0123456789abcdef0123456789abcdef")
		t.Setenv("OIDC_USERNAME", "")
		t.Setenv("OIDC_PASSWORD", "")

		md, err := mustCMSTokenCredentials(t, idtoken.NewTokenSource).GetRequestMetadata(ctx)
		if err != nil || !strings.HasPrefix(md["authorization"], "Bearer ") {
			t.Fatalf("GetRequestMetadata() = (%v, %v), expected Bearer token", md, err)
		}
	})

	t.Run("production mode uses Google ID token source with configured GRPC audience", func(t *testing.T) {
		t.Setenv("DISABLE_AUTHENTICATION", "false")
		t.Setenv("MOCK_AUTH", "false")
		t.Setenv("AUTH_URL", "")
		t.Setenv("JWT_SECRET", "0123456789abcdef0123456789abcdef")
		t.Setenv("IAP_EXPECTED_GRPC_AUDIENCE", "https://ess-grpc-audience")

		var gotAudience string
		factory := func(_ context.Context, audience string, _ ...idtoken.ClientOption) (oauth2.TokenSource, error) {
			gotAudience = audience
			return staticTokenSource{token: "gcp-wi-id-token"}, nil
		}
		md, err := mustCMSTokenCredentials(t, factory).GetRequestMetadata(ctx)
		if err != nil || gotAudience != "https://ess-grpc-audience" || md["authorization"] != "Bearer gcp-wi-id-token" {
			t.Fatalf("got (%v, %q, %v), want Bearer gcp-wi-id-token & https://ess-grpc-audience", md, gotAudience, err)
		}
	})

	t.Run("production mode falls back to CLIENT_ID and retries transient factory error", func(t *testing.T) {
		t.Setenv("DISABLE_AUTHENTICATION", "false")
		t.Setenv("MOCK_AUTH", "false")
		t.Setenv("AUTH_URL", "")
		t.Setenv("JWT_SECRET", "0123456789abcdef0123456789abcdef")
		t.Setenv("IAP_EXPECTED_GRPC_AUDIENCE", "")
		t.Setenv("CLIENT_ID", "fallback-client-id")

		var calls int
		var gotAudience string
		factory := func(_ context.Context, audience string, _ ...idtoken.ClientOption) (oauth2.TokenSource, error) {
			calls++
			gotAudience = audience
			if calls == 1 {
				return nil, errors.New("adc unavailable")
			}
			return staticTokenSource{token: "recovered-token"}, nil
		}
		creds := mustCMSTokenCredentials(t, factory)
		if _, err := creds.GetRequestMetadata(ctx); err == nil {
			t.Fatal("expected error on first call when ID token source factory fails")
		}
		md, err := creds.GetRequestMetadata(ctx)
		if err != nil || md["authorization"] != "Bearer recovered-token" || gotAudience != "fallback-client-id" {
			t.Fatalf("retry got (%v, %q, %v), want Bearer recovered-token & fallback-client-id", md, gotAudience, err)
		}
	})
}

// TestBuildNonProdTokenFuncMockAuth covers the mock auth branch directly: binaries without the
// mockauth tag cannot reach it through LoadConfig, which rejects MOCK_AUTH.
func TestBuildNonProdTokenFuncMockAuth(t *testing.T) {
	fn := buildNonProdTokenFunc(&xmcoauth.Config{
		MockAuth: true, TokenURL: "https://unused.example/token", ClientID: "xmc-client",
		Username: "admin@xmc.local", Password: "password", JWTSecret: "0123456789abcdef0123456789abcdef",
	})
	if fn == nil {
		t.Fatal("buildNonProdTokenFunc() = nil, want a session token minter")
	}
	tok, err := fn(context.Background())
	if err != nil || tok == "" {
		t.Fatalf("token func = (%q, %v), want a non-empty session token", tok, err)
	}
}
