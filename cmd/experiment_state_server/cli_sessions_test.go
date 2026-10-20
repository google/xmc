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

	"cloud.google.com/go/spanner"
	"xmanager_cloud/internal/storage/spanner/emulator"
	oauth "xmanager_cloud/pkg/auth/oauth2"
)

// unusedSessionStore is a CLISessionStore that is only installed, never called.
type unusedSessionStore struct{}

func (unusedSessionStore) Create(context.Context, oauth.CLISession) error { return nil }

func TestEnableCLISessions(t *testing.T) {
	ctx := context.Background()
	_, essClient := emulator.Setup(ctx, t)
	tests := []struct {
		name          string
		spannerClient *spanner.Client
		calls         int
		wantInstalled bool
	}{
		{name: "spanner client installs the session store", spannerClient: essClient, calls: 1, wantInstalled: true},
		{name: "a second call keeps the first store", spannerClient: essClient, calls: 2, wantInstalled: true},
		{name: "missing spanner client installs nothing", calls: 1, wantInstalled: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			oauthAuth := oauth.NewAuthenticator(&oauth.Config{})
			for range tt.calls {
				enableCLISessions(ctx, oauthAuth, tt.spannerClient)
			}
			// EnableCLISessions refuses to replace an installed store.
			installed := oauthAuth.EnableCLISessions(unusedSessionStore{}) != nil
			if installed != tt.wantInstalled {
				t.Errorf("session store installed = %t, want %t", installed, tt.wantInstalled)
			}
		})
	}
}
