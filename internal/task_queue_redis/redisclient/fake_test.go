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

package redisclient

import (
	"context"
	"errors"
	"testing"
)

func TestFakeClient_ExtendClaim(t *testing.T) {
	ctx := context.Background()
	fake := NewFakeClient()

	if fake.ExtendClaimCalls != 0 {
		t.Errorf("initial ExtendClaimCalls = %d, want 0", fake.ExtendClaimCalls)
	}

	if err := fake.ExtendClaim(ctx); err != nil {
		t.Fatalf("ExtendClaim() unexpected error: %v", err)
	}
	if fake.ExtendClaimCalls != 1 {
		t.Errorf("ExtendClaimCalls = %d, want 1", fake.ExtendClaimCalls)
	}

	wantErr := errors.New("simulated error")
	fake.ExtendClaimError = wantErr

	if err := fake.ExtendClaim(ctx); !errors.Is(err, wantErr) {
		t.Fatalf("ExtendClaim() error = %v, want %v", err, wantErr)
	}
	if fake.ExtendClaimCalls != 2 {
		t.Errorf("ExtendClaimCalls = %d, want 2", fake.ExtendClaimCalls)
	}
}

func TestFakeClient_AddMessages(t *testing.T) {
	fake := NewFakeClient()
	errBoom := errors.New("boom")
	fake.AddMessagesErrors = []error{nil, errBoom}

	errs := fake.AddMessages(context.Background(), []string{"a", "b", "c"})

	if fake.AddMessagesCalls != 1 {
		t.Errorf("AddMessagesCalls = %d, want 1", fake.AddMessagesCalls)
	}
	if len(errs) != 3 || errs[0] != nil || !errors.Is(errs[1], errBoom) || errs[2] != nil {
		t.Errorf("AddMessages() = %v, want [nil boom nil]", errs)
	}
	if len(fake.Messages) != 2 || fake.Messages[0] != "a" || fake.Messages[1] != "c" {
		t.Errorf("Messages = %v, want [a c]", fake.Messages)
	}
}
