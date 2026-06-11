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
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"golang.org/x/oauth2"
)

// OAuthProvider abstracts the external authentication provider interactions.
type OAuthProvider interface {
	// AuthCodeURL returns the authentication redirect URL for the given state.
	AuthCodeURL(state string, r *http.Request) string
	// ExchangeAndGetUserInfo performs token exchange and fetches the user identity.
	ExchangeAndGetUserInfo(ctx context.Context, r *http.Request, code string) (*GoogleUserInfo, error)
}

// GoogleOAuthProvider implements OAuthProvider using Google's production endpoints.
type GoogleOAuthProvider struct {
	Authenticator *Authenticator
}

// AuthCodeURL returns the authentication redirect URL for the given state.
func (g *GoogleOAuthProvider) AuthCodeURL(state string, r *http.Request) string {
	oauthConfig := g.Authenticator.getDynamicOAuthConfig(r)
	return oauthConfig.AuthCodeURL(state, oauth2.AccessTypeOffline)
}

// ExchangeAndGetUserInfo performs token exchange and fetches the user identity from Google userinfo API.
func (g *GoogleOAuthProvider) ExchangeAndGetUserInfo(ctx context.Context, r *http.Request, code string) (*GoogleUserInfo, error) {
	oauthConfig := g.Authenticator.getDynamicOAuthConfig(r)
	token, err := oauthConfig.Exchange(ctx, code)
	if err != nil {
		return nil, fmt.Errorf("Token exchange failed: %w", err)
	}

	client := oauthConfig.Client(ctx, token)
	resp, err := client.Get(g.Authenticator.Config.APIURL)
	if err != nil {
		return nil, fmt.Errorf("Failed to fetch user info: %w", err)
	}
	defer resp.Body.Close()

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("Failed to read user info response: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("User info call failed with status %d: %s", resp.StatusCode, string(bodyBytes))
	}

	var userInfo GoogleUserInfo
	if err := json.Unmarshal(bodyBytes, &userInfo); err != nil {
		return nil, fmt.Errorf("Failed to decode user info details: %w", err)
	}

	return &userInfo, nil
}
