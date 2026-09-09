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

package auth

import (
	"context"
	"testing"
	"time"
)

func TestNewAuthorizer(t *testing.T) {
	ctx := context.Background()

	t.Run("Valid parameters", func(t *testing.T) {
		auth, err := NewAuthorizer(ctx, nil, 10*time.Minute, 1*time.Minute, []string{"aud1", "aud2"}, "https://issuer.com", nil)
		if err != nil {
			t.Fatalf("Expected no error, got %v", err)
		}
		if auth == nil {
			t.Fatal("Expected Authorizer, got nil")
		}
	})

	t.Run("Empty IAP issuer", func(t *testing.T) {
		_, err := NewAuthorizer(ctx, nil, 10*time.Minute, 1*time.Minute, []string{"aud1"}, "", nil)
		if err == nil {
			t.Error("Expected error for empty IAP issuer, got nil")
		}
	})

	t.Run("Empty IAP audience", func(t *testing.T) {
		_, err := NewAuthorizer(ctx, nil, 10*time.Minute, 1*time.Minute, []string{"aud1", ""}, "https://issuer.com", nil)
		if err == nil {
			t.Error("Expected error for empty IAP audience, got nil")
		}
	})
}
