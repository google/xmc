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

package client

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/redis/go-redis/v9"
	"xmanager_cloud/kubernetes/common/constants"
	"xmanager_cloud/logging/logging"
	"xmanager_cloud/task_queue_redis/redisclient"
)

// RedisStreamClient defines the subset of Redis stream commands required for backlog inspection.
type RedisStreamClient interface {
	XInfoGroups(ctx context.Context, key string) *redis.XInfoGroupsCmd
	XRangeN(ctx context.Context, stream, start, stop string, count int64) *redis.XMessageSliceCmd
}

// Compile-time guarantee that the production publisher supports backlog inspection.
// Without it, Client.CalculateBacklog would silently report an empty backlog.
var _ RedisStreamClient = (*redisclient.PublisherClient)(nil)

// CalculateStreamBacklog calculates the total backlog (pending + lag) on a Redis stream for a consumer group.
// It reads pending and last-delivered-id from XINFO GROUPS, then counts undelivered messages via XRANGE.
// The result is capped at maxThreshold: if pending alone reaches it, XRANGE is skipped; otherwise XRANGE
// reads only the (maxThreshold - pending) + 1 entries needed to decide, avoiding unbounded stream reads.
func CalculateStreamBacklog(ctx context.Context, client RedisStreamClient, stream, group string, maxThreshold int) (int64, error) {
	if client == nil {
		logging.Warn(ctx, "No Redis stream client configured; reporting empty backlog", "stream", stream)
		return 0, nil
	}
	if maxThreshold <= 0 {
		return 0, fmt.Errorf("max backlog threshold must be positive, got %d", maxThreshold)
	}

	groups, err := client.XInfoGroups(ctx, stream).Result()
	if err != nil {
		if isMissingStreamErr(err) {
			return 0, nil
		}
		return 0, fmt.Errorf("failed to query consumer groups for stream %s: %w", stream, err)
	}

	targetGroup := findGroup(groups, group)
	if targetGroup == nil {
		logging.Warn(ctx, "Consumer group not found on stream; reporting empty backlog", "stream", stream, "group", group)
		return 0, nil
	}

	requiredLag := int64(maxThreshold) - targetGroup.Pending
	if requiredLag <= 0 {
		return targetGroup.Pending, nil
	}

	start := targetGroup.LastDeliveredID
	if start == "" {
		start = "-"
	}

	// +1 because XRANGE's start bound is inclusive and may return the last delivered message itself.
	msgs, err := client.XRangeN(ctx, stream, start, "+", requiredLag+1).Result()
	if err != nil {
		if isMissingStreamErr(err) {
			return targetGroup.Pending, nil
		}
		return 0, fmt.Errorf("failed to query lag via xrange for stream %s: %w", stream, err)
	}

	return targetGroup.Pending + undeliveredCount(msgs, targetGroup.LastDeliveredID), nil
}

// CalculateBacklog calculates the backlog on the actuator task stream.
func (c *Client) CalculateBacklog(ctx context.Context, maxThreshold int) (int64, error) {
	if c == nil || c.redisClient == nil {
		logging.Warn(ctx, "Task queue Redis client not configured; reporting empty backlog")
		return 0, nil
	}
	rClient, ok := c.redisClient.(RedisStreamClient)
	if !ok {
		logging.Warn(ctx, "Task queue Redis client does not support stream inspection; reporting empty backlog")
		return 0, nil
	}
	return CalculateStreamBacklog(ctx, rClient, constants.RedisStream, constants.RedisGroup, maxThreshold)
}

// isMissingStreamErr reports whether err indicates the stream key does not exist.
// XINFO GROUPS replies "ERR no such key" for a missing stream rather than a nil reply.
func isMissingStreamErr(err error) bool {
	return errors.Is(err, redis.Nil) || strings.Contains(err.Error(), "no such key")
}

func findGroup(groups []redis.XInfoGroup, name string) *redis.XInfoGroup {
	for i := range groups {
		if groups[i].Name == name {
			return &groups[i]
		}
	}
	return nil
}

// undeliveredCount excludes the last delivered message, which XRANGE includes because its start bound is inclusive.
func undeliveredCount(msgs []redis.XMessage, lastDeliveredID string) int64 {
	if len(msgs) > 0 && msgs[0].ID == lastDeliveredID {
		return int64(len(msgs) - 1)
	}
	return int64(len(msgs))
}
