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

package main

import (
	"context"
	"testing"

	"xmanager_cloud/cmd/experiment_state_server/config"
	oauth "xmanager_cloud/pkg/auth/oauth2"
)

func TestDisableAuthenticationBypassesCMSAuthWithMockAuthTag(t *testing.T) {
	ctx := context.Background()
	oauthCfg := &oauth.Config{DisableAuthentication: true}

	authz := buildCMSAuthorizer(&config.Config{InvokerSA: "bridge@proj.iam.gserviceaccount.com"}, oauthCfg, nil, nil)
	if _, err := authz.AuthorizeMutating(ctx); err != nil {
		t.Errorf("AuthorizeMutating() error = %v, want nil", err)
	}
	admitted, err := cmsAdmission("xmc-wl-sa@proj.iam.gserviceaccount.com", oauth.NewAuthenticator(oauthCfg)).IsUserAuthorized(ctx, "anyone@example.com")
	if err != nil || !admitted {
		t.Errorf("IsUserAuthorized() = (%v, %v), want (true, nil)", admitted, err)
	}
}
