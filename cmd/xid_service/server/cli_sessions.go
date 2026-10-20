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

package server

import (
	"context"
	"fmt"

	"cloud.google.com/go/spanner"
	"google.golang.org/api/option"
	"xmanager_cloud/cmd/xid_service/config"
	"xmanager_cloud/internal/logging"
	"xmanager_cloud/internal/storage/spanner/clisession"
	oauth "xmanager_cloud/pkg/auth/oauth2"
)

// initCLISessions makes authenticator reject revoked CLI sessions, reading the CliSessions table of
// the ESS database, where ESS records them. Callers reach XID directly as well as through ESS, so
// XID checks revocation itself. Without an ESS database CLI tokens are rejected. It returns the
// cleanup of the ESS client.
func initCLISessions(ctx context.Context, cfg *config.Config, authenticator *oauth.Authenticator) (func() error, error) {
	if cfg.ESSSpannerDatabaseName == "" {
		logging.Warn(ctx, "ESS Spanner database is not configured: CLI session tokens will be rejected")
		return func() error { return nil }, nil
	}
	client, err := spanner.NewClient(ctx, cfg.ESSSpannerDatabaseName, option.WithQuotaProject(cfg.QuotaProjectID))
	if err != nil {
		return nil, fmt.Errorf("failed to create ESS spanner client: %w", err)
	}
	sessions, err := clisession.NewRepository(client)
	if err == nil {
		err = authenticator.CheckCLISessions(sessions)
	}
	if err != nil {
		client.Close()
		return nil, fmt.Errorf("failed to enable CLI session checks: %w", err)
	}
	return func() error {
		client.Close()
		return nil
	}, nil
}
