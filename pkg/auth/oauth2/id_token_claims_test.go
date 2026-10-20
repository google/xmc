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
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/go-cmp/cmp"
	"golang.org/x/oauth2"
	"xmanager_cloud/pkg/auth/jwks"
)

const (
	entraIDTokenIssuer = "https://login.microsoftonline.com/9188040d-6c67-4c5b-b112-36a304b66dad/v2.0"
	entraLoginEmail    = "jdoe@contoso.com"
)

// fakeIDTokenVerifier returns claims for the ID token "valid-id-token" and records the audiences
// it was asked to accept.
type fakeIDTokenVerifier struct {
	claims    *jwks.Claims
	audiences []string
}

func (f *fakeIDTokenVerifier) ValidateClaimsAnyAudience(_ context.Context, tokenStr string, audiences []string) (*jwks.Claims, error) {
	f.audiences = audiences
	if tokenStr != "valid-id-token" {
		return nil, jwks.ErrInvalidToken
	}
	return f.claims, nil
}

// entraIDTokenClaims are the verified claims of an Entra ID sign-in ID token.
func entraIDTokenClaims() *jwks.Claims {
	return &jwks.Claims{
		PreferredUsername: "Jane.Doe@contoso.com",
		UPN:               "JDoe@Contoso.com",
		RegisteredClaims:  jwt.RegisteredClaims{IssuedAt: jwt.NewNumericDate(time.Now())},
	}
}

// entraLoginConfig is a single-tenant Microsoft Entra ID deployment that admits contoso.com by
// `upn`.
func entraLoginConfig() *Config {
	cfg := baseLoginConfig()
	cfg.ClientID = "entra-client"
	cfg.AuthURL = "https://login.microsoftonline.com/contoso.onmicrosoft.com/oauth2/v2.0/authorize"
	cfg.AllowedDomains = []string{"contoso.com"}
	cfg.EmailVerification = EmailVerificationTrustIssuer
	cfg.EmailClaim = EmailClaimUPN
	cfg.IDTokenJWKSURL = "https://login.microsoftonline.com/contoso.onmicrosoft.com/discovery/v2.0/keys"
	cfg.IDTokenIssuer = entraIDTokenIssuer
	return cfg
}

func TestNewIDTokenClaimsVerifier(t *testing.T) {
	if v := newIDTokenClaimsVerifier(baseLoginConfig()); v != nil {
		t.Errorf("newIDTokenClaimsVerifier() = %v for the email claim, want nil", v)
	}
	if _, ok := newIDTokenClaimsVerifier(entraLoginConfig()).(*jwks.Validator); !ok {
		t.Error("newIDTokenClaimsVerifier() is not a *jwks.Validator for the upn claim")
	}
}

func TestApplyIDTokenClaims(t *testing.T) {
	withIssuedAt := func(iat *jwt.NumericDate) *jwks.Claims {
		c := entraIDTokenClaims()
		c.IssuedAt = iat
		return c
	}
	idToken := (&oauth2.Token{AccessToken: "a"}).WithExtra(map[string]any{"id_token": "valid-id-token"})
	tests := []struct {
		name     string
		cfg      func(*Config)
		token    *oauth2.Token
		claims   *jwks.Claims
		verifier bool
		want     *GoogleUserInfo
		wantErr  bool
	}{
		{name: "email claim ignores the ID token", cfg: func(c *Config) { c.EmailClaim = EmailClaimEmail }, token: &oauth2.Token{}, want: &GoogleUserInfo{UPN: "userinfo@contoso.com"}},
		{name: "upn from the ID token", token: idToken, claims: entraIDTokenClaims(), verifier: true, want: &GoogleUserInfo{PreferredUsername: "Jane.Doe@contoso.com", UPN: "JDoe@Contoso.com"}},
		{name: "no id_token", token: &oauth2.Token{AccessToken: "a"}, verifier: true, wantErr: true},
		{name: "no verifier", token: idToken, wantErr: true},
		{name: "invalid ID token", token: (&oauth2.Token{}).WithExtra(map[string]any{"id_token": "forged"}), verifier: true, wantErr: true},
		{name: "missing iat", token: idToken, claims: withIssuedAt(nil), verifier: true, wantErr: true},
		{name: "future iat", token: idToken, claims: withIssuedAt(jwt.NewNumericDate(time.Now().Add(time.Hour))), verifier: true, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := entraLoginConfig()
			if tt.cfg != nil {
				tt.cfg(cfg)
			}
			a := NewAuthenticator(cfg)
			verifier := &fakeIDTokenVerifier{claims: tt.claims}
			a.IDTokenClaimsVerifier = nil
			if tt.verifier {
				a.IDTokenClaimsVerifier = verifier
			}
			info := &GoogleUserInfo{UPN: "userinfo@contoso.com"}
			err := a.applyIDTokenClaims(context.Background(), tt.token, info)
			if gotErr := err != nil; gotErr != tt.wantErr {
				t.Fatalf("applyIDTokenClaims() error = %v, wantErr %v", err, tt.wantErr)
			}
			if tt.wantErr {
				return
			}
			if diff := cmp.Diff(tt.want, info); diff != "" {
				t.Errorf("applyIDTokenClaims() userinfo mismatch (-want +got):\n%s", diff)
			}
			if tt.verifier && !cmp.Equal(verifier.audiences, []string{"entra-client"}) {
				t.Errorf("ID token audiences = %v, want [entra-client]", verifier.audiences)
			}
		})
	}
}

// TestExchangeAndGetUserInfoIDTokenClaims signs in through a fake Entra ID token endpoint: the
// identity comes from the verified ID token, never from userinfo.
func TestExchangeAndGetUserInfoIDTokenClaims(t *testing.T) {
	idp := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/token":
			_, _ = w.Write([]byte(`{"access_token": "token-xyz", "token_type": "Bearer", "id_token": "valid-id-token"}`))
		case "/token-without-id-token":
			_, _ = w.Write([]byte(`{"access_token": "token-xyz", "token_type": "Bearer"}`))
		case "/userinfo":
			_, _ = w.Write([]byte(`{"name": "Jane Doe", "upn": "attacker@contoso.com", "email": "attacker@contoso.com"}`))
		}
	}))
	defer idp.Close()
	signIn := func(tokenPath string) (loginIdentity, error) {
		cfg := entraLoginConfig()
		cfg.TokenURL, cfg.APIURL = idp.URL+tokenPath, idp.URL+"/userinfo"
		a := NewAuthenticator(cfg)
		a.IDTokenClaimsVerifier = &fakeIDTokenVerifier{claims: entraIDTokenClaims()}
		info, err := a.OAuthProvider.ExchangeAndGetUserInfo(context.Background(), httptest.NewRequest(http.MethodGet, "/api/callback", nil), "code")
		if err != nil {
			return loginIdentity{}, err
		}
		return a.verifyUserInfo(info)
	}

	identity, err := signIn("/token")
	if err != nil {
		t.Fatalf("sign-in failed: %v", err)
	}
	if want := (loginIdentity{email: entraLoginEmail, name: "Jane Doe"}); identity != want {
		t.Errorf("identity = %+v, want %+v", identity, want)
	}
	if _, err := signIn("/token-without-id-token"); err == nil {
		t.Error("sign-in succeeded without an ID token, want error")
	}
}

func TestValidateIDTokenSource(t *testing.T) {
	tests := []struct {
		name    string
		cfg     func(*Config)
		wantErr bool
	}{
		{name: "tenant issuer"},
		{name: "no JWKS URL", cfg: func(c *Config) { c.IDTokenJWKSURL = "" }, wantErr: true},
		{name: "no issuer", cfg: func(c *Config) { c.IDTokenIssuer = "" }, wantErr: true},
		{name: "IAP default issuer", cfg: func(c *Config) { c.IDTokenIssuer = "https://iap.googleapis.com" }, wantErr: true},
		{name: "multi-tenant issuer", cfg: func(c *Config) { c.IDTokenIssuer = "https://login.microsoftonline.com/common/v2.0" }, wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := entraLoginConfig()
			if tt.cfg != nil {
				tt.cfg(cfg)
			}
			if err := validateIDTokenSource(cfg); (err != nil) != tt.wantErr {
				t.Errorf("validateIDTokenSource() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}
