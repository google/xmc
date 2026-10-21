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

	"xmanager_cloud/internal/logging"
	"xmanager_cloud/internal/storage"
	oauth "xmanager_cloud/pkg/auth/oauth2"
)

// registerCLILogin serves the browser-based CLI login endpoints. They are not registered under
// mock or disabled authentication, which would let anyone mint CLI tokens, nor without storage.
func registerCLILogin(ctx context.Context, httpMux *http.ServeMux, oauthAuth *oauth.Authenticator, oauthCfg *oauth.Config, store storage.Storage) {
	if oauthCfg.MockAuth || oauthCfg.DisableAuthentication {
		logging.Warn(ctx, "CLI login disabled under non-production auth", "mock_auth", oauthCfg.MockAuth, "disable_authentication", oauthCfg.DisableAuthentication)
		return
	}
	if store == nil {
		logging.Warn(ctx, "CLI login disabled: no storage")
		return
	}
	if err := oauthAuth.EnableCLILogin(store.CLILoginCodes()); err != nil {
		logging.Warn(ctx, "CLI login disabled", "error", err)
		return
	}
	httpMux.HandleFunc("/api/cli/login", oauthAuth.HandleCLILogin)
	httpMux.HandleFunc("/api/cli/token", oauthAuth.HandleCLIToken)
	httpMux.HandleFunc("/api/cli/success", oauthAuth.HandleCLISuccess)
	logging.Info(ctx, "CLI login endpoints registered")
}
