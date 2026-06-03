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
	"context"
	"sync/atomic"
	"testing"
	"time"

	"cloud.google.com/go/iam/apiv1/iampb"
)

func TestIamPolicyCachingAndClientReuse(t *testing.T) {
	cfg := &Config{
		GCPProjectID:  "test-project-id",
		TargetIAMRole: "roles/viewer",
	}

	a := NewAuthenticator(cfg)

	// Set up a mock fetcher to count calls
	var fetchCount int64
	mockPolicy := &iampb.Policy{
		Bindings: []*iampb.Binding{
			{
				Role:    "roles/viewer",
				Members: []string{"user:allowed@company.com"},
			},
		},
	}

	a.policyFetcher = func(ctx context.Context) (*iampb.Policy, error) {
		atomic.AddInt64(&fetchCount, 1)
		return mockPolicy, nil
	}

	ctx := context.Background()

	// 1. Initial lookup - should fetch from mock API
	authorized, err := a.IsUserAuthorizedViaIAM(ctx, "allowed@company.com")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !authorized {
		t.Errorf("expected user to be authorized")
	}

	if count := atomic.LoadInt64(&fetchCount); count != 1 {
		t.Errorf("expected policyFetcher to be called once, got %d", count)
	}

	// 2. Immediate second lookup - should use cached results, fetchCount should remain 1
	authorized, err = a.IsUserAuthorizedViaIAM(ctx, "allowed@company.com")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !authorized {
		t.Errorf("expected user to be authorized")
	}

	if count := atomic.LoadInt64(&fetchCount); count != 1 {
		t.Errorf("expected policyFetcher to still be called once (cached), got %d", count)
	}

	// 3. Different user lookup - should still use cached policy (fetchCount remains 1)
	unauthorized, err := a.IsUserAuthorizedViaIAM(ctx, "unauthorized@company.com")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if unauthorized {
		t.Errorf("expected user to be unauthorized")
	}

	if count := atomic.LoadInt64(&fetchCount); count != 1 {
		t.Errorf("expected policyFetcher to still be called once (cached for project-wide policy), got %d", count)
	}

	// 4. Force expiration of cache and lookup - should fetch again (fetchCount becomes 2)
	a.mu.Lock()
	a.policyCache.expiry = time.Now().Add(-1 * time.Second)
	a.mu.Unlock()

	authorized, err = a.IsUserAuthorizedViaIAM(ctx, "allowed@company.com")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !authorized {
		t.Errorf("expected user to be authorized")
	}

	if count := atomic.LoadInt64(&fetchCount); count != 2 {
		t.Errorf("expected policyFetcher to be called twice after cache expired, got %d", count)
	}

	// Test Close() does not error when projectsClient is nil (not initialized because policyFetcher was mocked)
	if err := a.Close(); err != nil {
		t.Errorf("unexpected error on Close: %v", err)
	}
}
