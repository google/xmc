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

	"cloud.google.com/go/spanner"
	"xmanager_cloud/internal/logging"
	"xmanager_cloud/internal/storage/spanner/clisession"
	oauth "xmanager_cloud/pkg/auth/oauth2"
)

// enableCLISessions records the CLI tokens oauthAuth mints in the CliSessions table. It must run
// before the servers start. Without a Spanner client no CLI tokens are minted.
func enableCLISessions(ctx context.Context, oauthAuth *oauth.Authenticator, spannerClient *spanner.Client) {
	sessions, err := clisession.NewRepository(spannerClient)
	if err != nil {
		logging.Warn(ctx, "CLI sessions disabled: no CLI tokens will be issued", "error", err)
		return
	}
	if err := oauthAuth.EnableCLISessions(sessions); err != nil {
		logging.Warn(ctx, "CLI sessions disabled: no CLI tokens will be issued", "error", err)
	}
}
