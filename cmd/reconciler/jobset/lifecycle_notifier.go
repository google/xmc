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

package jobset

import (
	"context"
	"fmt"

	"google.golang.org/protobuf/encoding/protojson"
	taskqueuepb "xmanager_cloud/api/protos/task_queue"
	"xmanager_cloud/cmd/reconciler/shared"
)

// RedisPublisher defines the Redis client interface for adding messages to a stream.
type RedisPublisher interface {
	AddMessage(ctx context.Context, data string, failedAttempts string) error
}

// RedisLifecycleNotifier publishes ArtifactLifecycleEvents to a Redis stream.
type RedisLifecycleNotifier struct {
	publisher RedisPublisher
}

// NewRedisLifecycleNotifier creates a new RedisLifecycleNotifier backed by publisher.
func NewRedisLifecycleNotifier(publisher RedisPublisher) *RedisLifecycleNotifier {
	return &RedisLifecycleNotifier{publisher: publisher}
}

// NotifyLifecycleEvent publishes event as protojson to the Redis stream.
func (n *RedisLifecycleNotifier) NotifyLifecycleEvent(ctx context.Context, event *taskqueuepb.ArtifactLifecycleEvent) error {
	if n == nil || n.publisher == nil || event == nil {
		return nil
	}
	data, err := protojson.Marshal(event)
	if err != nil {
		return fmt.Errorf("failed to marshal ArtifactLifecycleEvent: %w", err)
	}
	return n.publisher.AddMessage(ctx, string(data), "0")
}

var _ shared.LifecycleNotifier = (*RedisLifecycleNotifier)(nil)
