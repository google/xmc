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
	"errors"
	"fmt"
	"os"
	"strings"
	"sync"
	"time"

	"golang.org/x/oauth2"
	"google.golang.org/api/idtoken"
	"google.golang.org/grpc/credentials"

	"xmanager_cloud/internal/logging"
	tq "xmanager_cloud/internal/task_queue"
	xmcoauth "xmanager_cloud/pkg/auth/oauth2"
)

var _ credentials.PerRPCCredentials = (*BearerTokenCredentials)(nil)

// TokenSourceFunc fetches a Bearer token for an outgoing CMS gRPC request.
type TokenSourceFunc func(ctx context.Context) (string, error)

type idTokenSourceFactory func(context.Context, string, ...idtoken.ClientOption) (oauth2.TokenSource, error)

// BearerTokenCredentials implements credentials.PerRPCCredentials using a TokenSourceFunc.
type BearerTokenCredentials struct{ tokenFn TokenSourceFunc }

// NewBearerTokenCredentials constructs PerRPCCredentials backed by tokenFn.
func NewBearerTokenCredentials(tokenFn TokenSourceFunc) credentials.PerRPCCredentials {
	return &BearerTokenCredentials{tokenFn: tokenFn}
}

// GetRequestMetadata attaches the Authorization Bearer header to outgoing gRPC metadata.
func (c *BearerTokenCredentials) GetRequestMetadata(ctx context.Context, _ ...string) (map[string]string, error) {
	if c == nil || c.tokenFn == nil {
		return nil, nil
	}
	tok, err := c.tokenFn(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to obtain CMS bearer token: %w", err)
	}
	if tok == "" {
		return nil, nil
	}
	return map[string]string{"authorization": "Bearer " + tok}, nil
}

// RequireTransportSecurity returns false to support cluster-internal gRPC connections.
func (c *BearerTokenCredentials) RequireTransportSecurity() bool { return false }

// NewCMSTokenCredentials resolves PerRPCCredentials for authenticating xmc-bridge to CMS. It fails
// when the environment requests a non-production auth mode that this binary was not built with.
func NewCMSTokenCredentials(ctx context.Context) (credentials.PerRPCCredentials, error) {
	return newCMSTokenCredentialsWithFactory(ctx, idtoken.NewTokenSource)
}

func newCMSTokenCredentialsWithFactory(ctx context.Context, newIDTokenSource idTokenSourceFactory) (credentials.PerRPCCredentials, error) {
	if os.Getenv("DISABLE_AUTHENTICATION") == "true" {
		return NewBearerTokenCredentials(nil), nil
	}
	oauthCfg, err := xmcoauth.LoadConfig()
	if errors.Is(err, xmcoauth.ErrDevAuthNotCompiledIn) {
		return nil, err
	}
	if err != nil {
		logging.Warn(ctx, "Failed to load OAuth config for CMS credentials; falling back to Google ID token", "error", err)
	}
	if oauthCfg != nil && oauthCfg.IsNonProdAuth() {
		if fn := buildNonProdTokenFunc(oauthCfg); fn != nil {
			return NewBearerTokenCredentials(fn), nil
		}
	}
	aud := resolveCMSAudience(oauthCfg)
	if aud == "placeholder-grpc-audience" {
		logging.Warn(ctx, "No gRPC audience configured for CMS credentials; using placeholder. Production calls will likely fail.")
	}
	return NewBearerTokenCredentials(buildGoogleIDTokenFunc(ctx, aud, newIDTokenSource)), nil
}

func buildNonProdTokenFunc(oauthCfg *xmcoauth.Config) TokenSourceFunc {
	if !oauthCfg.MockAuth && oauthCfg.TokenURL != "" && oauthCfg.ClientID != "" && oauthCfg.Username != "" && oauthCfg.Password != "" {
		provider := tq.NewOidcTokenProvider(tq.OidcTokenProviderConfig{
			TokenURL: oauthCfg.TokenURL, ClientID: oauthCfg.ClientID, ClientSecret: oauthCfg.ClientSecret,
			Username: oauthCfg.Username, Password: oauthCfg.Password, Scope: "openid email profile",
		})
		return func(reqCtx context.Context) (string, error) {
			reqCtx, cancel := context.WithTimeout(reqCtx, 10*time.Second)
			defer cancel()
			return provider.GenerateToken(reqCtx, "", oauthCfg.ClientID)
		}
	}
	if oauthCfg.JWTSecret == "" {
		return nil
	}
	email := strings.TrimSpace(oauthCfg.Username)
	if email == "" {
		email = "admin@xmc.local"
	}
	secret := []byte(oauthCfg.JWTSecret)
	return func(context.Context) (string, error) {
		return xmcoauth.GenerateSessionToken(email, "XMC Bridge", "", time.Hour, secret)
	}
}

func buildGoogleIDTokenFunc(ctx context.Context, audience string, newIDTokenSource idTokenSourceFactory) TokenSourceFunc {
	var (
		mu sync.Mutex
		ts oauth2.TokenSource
	)
	return func(context.Context) (string, error) {
		mu.Lock()
		if ts == nil {
			source, err := newIDTokenSource(ctx, audience)
			if err != nil {
				mu.Unlock()
				return "", err
			}
			ts = source
		}
		source := ts
		mu.Unlock()
		tok, err := source.Token()
		if err != nil {
			return "", err
		}
		return tok.AccessToken, nil
	}
}

func resolveCMSAudience(oauthCfg *xmcoauth.Config) string {
	var cfgClientID string
	if oauthCfg != nil {
		cfgClientID = oauthCfg.ClientID
	}
	for _, candidate := range []string{os.Getenv("IAP_EXPECTED_GRPC_AUDIENCE"), cfgClientID, os.Getenv("CLIENT_ID")} {
		if trimmed := strings.TrimSpace(candidate); trimmed != "" {
			return trimmed
		}
	}
	return "placeholder-grpc-audience"
}
