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

package oauth2

import (
	"strings"

	"github.com/grpc-ecosystem/grpc-gateway/v2/runtime"
)

// IncomingHeaderMatcher matches custom Google IAP identity and OAuth headers,
// forwarding them through the gRPC-Gateway proxy metadata.
func IncomingHeaderMatcher(key string) (string, bool) {
	switch strings.ToLower(key) {
	case "x-goog-iap-jwt-assertion", "x-goog-user-email", "x-goog-authenticated-user-email", "x-goog-authenticated-user-id":
		return key, true
	default:
		return runtime.DefaultHeaderMatcher(key)
	}
}
