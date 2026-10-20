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
	"testing"
	"time"

	"cloud.google.com/go/spanner"
	"xmanager_cloud/cmd/xid_service/config"
	"xmanager_cloud/internal/storage/spanner/emulator"
	oauth "xmanager_cloud/pkg/auth/oauth2"
)

func TestInitCLISessions(t *testing.T) {
	ctx := context.Background()
	_, essClient := emulator.Setup(ctx, t)
	if _, err := essClient.Apply(ctx, []*spanner.Mutation{spanner.Insert("CliSessions",
		[]string{"Jti", "Email", "IssuedAt", "ExpiresAt"},
		[]any{"active", "user@example.com", time.Now(), time.Now().Add(time.Hour)})}); err != nil {
		t.Fatalf("Apply() unexpected error: %v", err)
	}
	tests := []struct {
		name        string
		essDB       string
		wantRevoked bool
	}{
		{name: "ESS database checks the session table", essDB: essClient.DatabaseName(), wantRevoked: false},
		{name: "no ESS database rejects every CLI session", wantRevoked: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			authenticator := oauth.NewAuthenticator(&oauth.Config{})
			cleanup, err := initCLISessions(ctx, &config.Config{ESSSpannerDatabaseName: tt.essDB}, authenticator)
			if err != nil {
				t.Fatalf("initCLISessions() unexpected error: %v", err)
			}
			defer cleanup()
			got, err := authenticator.IsRevoked(ctx, "active")
			if err != nil {
				t.Fatalf("IsRevoked() unexpected error: %v", err)
			}
			if got != tt.wantRevoked {
				t.Errorf("IsRevoked() = %t, want %t", got, tt.wantRevoked)
			}
		})
	}
}

func TestInitCLISessionsRejectsSecondCheckerAndInvalidDatabase(t *testing.T) {
	ctx := context.Background()
	_, essClient := emulator.Setup(ctx, t)
	authenticator := oauth.NewAuthenticator(&oauth.Config{})
	cfg := &config.Config{ESSSpannerDatabaseName: essClient.DatabaseName()}
	cleanup, err := initCLISessions(ctx, cfg, authenticator)
	if err != nil {
		t.Fatalf("initCLISessions() unexpected error: %v", err)
	}
	defer cleanup()
	if _, err := initCLISessions(ctx, cfg, authenticator); err == nil {
		t.Error("second initCLISessions() error = nil, want non-nil")
	}
	if _, err := initCLISessions(ctx, &config.Config{ESSSpannerDatabaseName: "invalid-database-name"}, oauth.NewAuthenticator(&oauth.Config{})); err == nil {
		t.Error("initCLISessions(invalid database) error = nil, want non-nil")
	}
}
