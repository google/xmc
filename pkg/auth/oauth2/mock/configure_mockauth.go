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

import "xmanager_cloud/pkg/auth/oauth2"

// ConfigureIfEnabled installs the mock OAuth provider, ID token validator and IAM policy on a when
// MOCK_AUTH is set. Every binary calls it on the Authenticator it uses for both HTTP and gRPC
// admission, so all services admit the same principals under mock auth. Binaries built without
// the mockauth tag get a no-op.
func ConfigureIfEnabled(a *oauth2.Authenticator) {
	if a.Config.MockAuth {
		ConfigureMockAuth(a)
	}
}
