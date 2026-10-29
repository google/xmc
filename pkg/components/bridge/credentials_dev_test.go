// Copyright 2026 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

//go:build mockauth

package bridge

import (
	"context"
	"testing"
)

func TestNewCMSTokenCredentialsDisableAuthenticationWithMockAuthTag(t *testing.T) {
	t.Setenv("DISABLE_AUTHENTICATION", "true")
	t.Setenv("MOCK_AUTH", "false")
	ctx := context.Background()

	creds, err := NewCMSTokenCredentials(ctx)
	if err != nil {
		t.Fatalf("NewCMSTokenCredentials() error = %v, want nil", err)
	}
	md, err := creds.GetRequestMetadata(ctx)
	if err != nil || md != nil {
		t.Fatalf("GetRequestMetadata() = (%v, %v), want no-op credentials (nil, nil)", md, err)
	}
}
