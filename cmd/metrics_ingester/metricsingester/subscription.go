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
	"fmt"

	"cloud.google.com/go/pubsub"
)

// OpenSubscription returns the configured subscription after asserting that it exists, so a
// misconfigured deployment fails at startup rather than by silently receiving nothing.
//
// Checking existence needs pubsub.subscriptions.get in addition to the pubsub.subscriber role.
func OpenSubscription(ctx context.Context, client *pubsub.Client, cfg *Config) (*pubsub.Subscription, error) {
	sub := client.Subscription(cfg.SubscriptionID)
	exists, err := sub.Exists(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to check pub/sub subscription %q: %w", cfg.SubscriptionID, err)
	}
	if !exists {
		return nil, fmt.Errorf("pub/sub subscription %q does not exist in project %q", cfg.SubscriptionID, cfg.ProjectID)
	}

	// Leave room for a second full batch to arrive while the first is being written. The library
	// default of 1000 outstanding messages would otherwise stall receiving whenever batch_size is at
	// or above it, since messages count as outstanding until acked.
	sub.ReceiveSettings.MaxOutstandingMessages = 2 * cfg.BatchSize
	return sub, nil
}
