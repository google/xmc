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
	"testing"
	"time"

	"cloud.google.com/go/spanner"
	"xmanager_cloud/internal/storage/spanner/emulator"
	oauth "xmanager_cloud/pkg/auth/oauth2"
)

func TestEnableCLISessions(t *testing.T) {
	ctx := context.Background()
	_, essClient := emulator.Setup(ctx, t)
	if _, err := essClient.Apply(ctx, []*spanner.Mutation{spanner.Insert("CliSessions",
		[]string{"Jti", "Email", "IssuedAt", "ExpiresAt"},
		[]any{"active", "user@example.com", time.Now(), time.Now().Add(time.Hour)})}); err != nil {
		t.Fatalf("Apply() unexpected error: %v", err)
	}
	tests := []struct {
		name          string
		spannerClient *spanner.Client
		calls         int
		wantRevoked   bool
	}{
		{name: "spanner client checks the session table", spannerClient: essClient, calls: 1, wantRevoked: false},
		{name: "a second call keeps the first store", spannerClient: essClient, calls: 2, wantRevoked: false},
		{name: "missing spanner client rejects every CLI session", calls: 1, wantRevoked: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			oauthAuth := oauth.NewAuthenticator(&oauth.Config{})
			for range tt.calls {
				enableCLISessions(ctx, oauthAuth, tt.spannerClient)
			}
			got, err := oauthAuth.IsRevoked(ctx, "active")
			if err != nil {
				t.Fatalf("IsRevoked() unexpected error: %v", err)
			}
			if got != tt.wantRevoked {
				t.Errorf("IsRevoked() = %t, want %t", got, tt.wantRevoked)
			}
		})
	}
}
