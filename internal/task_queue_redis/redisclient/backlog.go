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
	"fmt"
	"strings"

	"xmanager_cloud/internal/logging"

	"github.com/redis/go-redis/v9"
)

// streamInspector is the subset of Redis stream commands required for backlog inspection.
type streamInspector interface {
	XInfoGroups(ctx context.Context, key string) *redis.XInfoGroupsCmd
	XRangeN(ctx context.Context, stream, start, stop string, count int64) *redis.XMessageSliceCmd
}

// BacklogLength implements Publisher.BacklogLength for the client's stream.
func (p *PublisherClient) BacklogLength(ctx context.Context, group string, maxThreshold int) (int64, error) {
	return streamBacklogLength(ctx, p.Client, p.StreamName, group, maxThreshold)
}

// streamBacklogLength reads pending and last-delivered-id from XINFO GROUPS, then counts
// undelivered messages via XRANGE. If pending alone reaches maxThreshold, XRANGE is skipped;
// otherwise XRANGE reads only the (maxThreshold - pending) + 1 entries needed to decide.
func streamBacklogLength(ctx context.Context, client streamInspector, stream, group string, maxThreshold int) (int64, error) {
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
			// The stream was deleted after XINFO GROUPS; its pending entries went with it.
			return 0, nil
		}
		return 0, fmt.Errorf("failed to query lag via xrange for stream %s: %w", stream, err)
	}

	return targetGroup.Pending + undeliveredCount(msgs, targetGroup.LastDeliveredID), nil
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
