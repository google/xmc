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
	"net/http/httptest"
	"testing"

	"github.com/google/go-cmp/cmp"
	"xmanager_cloud/internal/storage"
	"xmanager_cloud/internal/storage/spanner"
	"xmanager_cloud/internal/storage/spanner/emulator"
	oauth "xmanager_cloud/pkg/auth/oauth2"
)

var cliLoginPaths = []string{"/api/cli/login"}

// registeredPatterns returns the mux pattern serving each CLI login path, or "" when none does.
func registeredPatterns(mux *http.ServeMux) []string {
	patterns := make([]string, len(cliLoginPaths))
	for i, path := range cliLoginPaths {
		_, patterns[i] = mux.Handler(httptest.NewRequest(http.MethodGet, path, nil))
	}
	return patterns
}

func TestRegisterCLILogin(t *testing.T) {
	ctx := context.Background()
	_, essClient := emulator.Setup(ctx, t)
	spannerStorage, err := spanner.NewStorage(essClient)
	if err != nil {
		t.Fatalf("spanner.NewStorage() failed: %v", err)
	}
	none := make([]string, len(cliLoginPaths))
	tests := []struct {
		name  string
		cfg   oauth.Config
		store storage.Storage
		want  []string
	}{
		{
			name:  "production auth with storage registers the endpoints",
			cfg:   oauth.Config{JWTSecret: "cli-login-test-secret-0123456789abcdef"},
			store: spannerStorage,
			want:  cliLoginPaths,
		},
		{
			name:  "mock auth skips the endpoints",
			cfg:   oauth.Config{MockAuth: true},
			store: spannerStorage,
			want:  none,
		},
		{
			name:  "disabled auth skips the endpoints",
			cfg:   oauth.Config{DisableAuthentication: true},
			store: spannerStorage,
			want:  none,
		},
		{
			name: "missing storage skips the endpoints",
			cfg:  oauth.Config{JWTSecret: "cli-login-test-secret-0123456789abcdef"},
			want: none,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mux := http.NewServeMux()
			registerCLILogin(ctx, mux, oauth.NewAuthenticator(&tt.cfg), &tt.cfg, tt.store)
			if diff := cmp.Diff(tt.want, registeredPatterns(mux)); diff != "" {
				t.Errorf("registered patterns mismatch (-want +got):\n%s", diff)
			}
		})
	}
}
