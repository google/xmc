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

package mock

import (
	"testing"

	"xmanager_cloud/pkg/auth/oauth2"
)

func TestConfigureIfEnabled(t *testing.T) {
	tests := []struct {
		name     string
		mockAuth bool
		wantMock bool
	}{
		{name: "mock auth enabled", mockAuth: true, wantMock: true},
		{name: "mock auth disabled", mockAuth: false, wantMock: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := oauth2.NewAuthenticator(&oauth2.Config{MockAuth: tt.mockAuth})
			ConfigureIfEnabled(a)
			_, gotMock := a.OAuthProvider.(*MockOAuthProvider)
			if gotMock != tt.wantMock {
				t.Errorf("ConfigureIfEnabled() installed mock provider = %v, want %v", gotMock, tt.wantMock)
			}
		})
	}
}
