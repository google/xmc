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

package metricsingester

import (
	"context"
	"strings"
	"testing"
)

func TestOpenSubscriptionConfiguresExistingSubscription(t *testing.T) {
	ctx := context.Background()
	_, client, _ := newFakeSubscription(ctx, t)
	cfg := &Config{ProjectID: testProjectID, SubscriptionID: testSubscriptionID, BatchSize: 1500}

	sub, err := OpenSubscription(ctx, client, cfg)
	if err != nil {
		t.Fatalf("OpenSubscription() error = %v", err)
	}
	if got := sub.ID(); got != testSubscriptionID {
		t.Errorf("subscription ID = %q, want %q", got, testSubscriptionID)
	}
	if got := sub.ReceiveSettings.MaxOutstandingMessages; got != 3000 {
		t.Errorf("MaxOutstandingMessages = %d, want 3000", got)
	}
}

func TestOpenSubscriptionRejectsMissingSubscription(t *testing.T) {
	ctx := context.Background()
	_, client, _ := newFakeSubscription(ctx, t)
	cfg := &Config{ProjectID: testProjectID, SubscriptionID: "missing", BatchSize: 1}

	_, err := OpenSubscription(ctx, client, cfg)
	if err == nil || !strings.Contains(err.Error(), `subscription "missing" does not exist`) {
		t.Errorf("OpenSubscription() error = %v, want a missing-subscription error", err)
	}
}

func TestOpenSubscriptionWrapsLookupFailure(t *testing.T) {
	ctx := context.Background()
	_, client, _ := newFakeSubscription(ctx, t)
	cancelled, cancel := context.WithCancel(ctx)
	cancel()

	_, err := OpenSubscription(cancelled, client, &Config{SubscriptionID: testSubscriptionID, BatchSize: 1})
	if err == nil || !strings.Contains(err.Error(), "failed to check pub/sub subscription") {
		t.Errorf("OpenSubscription() error = %v, want a lookup failure", err)
	}
}
