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

package main

import (
	"context"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/twmb/franz-go/pkg/kadm"
)

// RealRedpandaQuerier wraps kadm.Client.
type RealRedpandaQuerier struct {
	client *kadm.Client
}

func (r *RealRedpandaQuerier) Ping(ctx context.Context) (RedpandaPingResult, error) {
	start := time.Now()
	brokers, err := r.client.ListBrokers(ctx)
	return RedpandaPingResult{
		BrokerCount: len(brokers),
		Latency:     time.Since(start),
	}, err
}

func (r *RealRedpandaQuerier) ListTopics(ctx context.Context, topics ...string) (kadm.TopicDetails, error) {
	return r.client.ListTopics(ctx, topics...)
}

func (r *RealRedpandaQuerier) ListEndOffsets(ctx context.Context, topics ...string) (kadm.ListedOffsets, error) {
	return r.client.ListEndOffsets(ctx, topics...)
}

func (r *RealRedpandaQuerier) FetchOffsets(ctx context.Context, group string) (kadm.OffsetResponses, error) {
	return r.client.FetchOffsets(ctx, group)
}

// CalculateConsumerLag computes partition lag and total lag from kadm topic details and consumer group offsets.
func CalculateConsumerLag(endOffsets kadm.ListedOffsets, groupOffsets kadm.OffsetResponses, topic, group string) LagReport {
	report := LagReport{
		ConsumerGroup: group,
		Topic:         topic,
		Partitions:    make([]PartitionLag, 0),
	}

	topEnds, ok := endOffsets[topic]
	if !ok {
		return report
	}

	topCommitted, ok := groupOffsets[topic]

	for pID, endOffset := range topEnds {
		if endOffset.Err != nil {
			continue
		}
		highWater := endOffset.Offset

		var committed int64
		if ok {
			if cOffset, exists := topCommitted[pID]; exists && cOffset.Err == nil {
				if cOffset.Offset.At >= 0 {
					committed = cOffset.Offset.At
				}
			}
		}

		lag := highWater - committed
		if lag < 0 {
			lag = 0
		}

		report.TotalLag += lag
		report.Partitions = append(report.Partitions, PartitionLag{
			Partition: pID,
			HighWater: highWater,
			Committed: committed,
			Lag:       lag,
		})
	}

	sort.Slice(report.Partitions, func(i, j int) bool {
		return report.Partitions[i].Partition < report.Partitions[j].Partition
	})

	return report
}

func inspectTopicPartitions(ctx context.Context, querier RedpandaQuerier, topic string, report *RedpandaReport) (int, bool) {
	topics, err := querier.ListTopics(ctx, topic)
	if err != nil {
		report.Status = StatusFailed
		report.Connected = report.BrokerCount > 0
		report.Reason = fmt.Sprintf("Failed listing metadata for topic %q", topic)
		report.Error = fmt.Sprintf("failed listing topics: %v", err)
		return 0, false
	}
	report.Connected = true

	topicDetail, exists := topics[topic]
	if !exists {
		report.Status = StatusFailed
		report.Reason = fmt.Sprintf("Required metrics topic %q does not exist", topic)
		report.Error = fmt.Sprintf("topic %q not found", topic)
		return 0, false
	}
	if topicDetail.Err != nil {
		report.Status = StatusFailed
		report.Reason = fmt.Sprintf("Topic %q reports error", topic)
		report.Error = topicDetail.Err.Error()
		return 0, false
	}
	return len(topicDetail.Partitions), true
}

func inspectEndOffsets(ctx context.Context, querier RedpandaQuerier, topic string) (kadm.ListedOffsets, []string) {
	endOffsets, err := querier.ListEndOffsets(ctx, topic)
	if err != nil {
		return nil, []string{fmt.Sprintf("failed listing partition end offsets for topic %q: %v", topic, err)}
	}
	var reasons []string
	for p, o := range endOffsets[topic] {
		if o.Err != nil {
			reasons = append(reasons, fmt.Sprintf("partition %d end offset error: %v", p, o.Err))
		}
	}
	return endOffsets, reasons
}

func inspectGroupOffsets(ctx context.Context, querier RedpandaQuerier, topic, group string) (kadm.OffsetResponses, []string) {
	groupOffsets, err := querier.FetchOffsets(ctx, group)
	if err != nil {
		return nil, []string{fmt.Sprintf("failed fetching consumer group offsets for %q: %v", group, err)}
	}
	var reasons []string
	for p, o := range groupOffsets[topic] {
		if o.Err != nil {
			reasons = append(reasons, fmt.Sprintf("partition %d committed offset error: %v", p, o.Err))
		}
	}
	return groupOffsets, reasons
}

// CheckRedpanda performs Redpanda diagnostics using querier.
func CheckRedpanda(ctx context.Context, querier RedpandaQuerier, connErr error, topic, group string, skipSmoke bool) RedpandaReport {
	if connErr != nil {
		return RedpandaReport{
			Status: StatusFailed,
			Reason: "Failed initializing Redpanda client",
			Error:  connErr.Error(),
		}
	}
	if querier == nil {
		return RedpandaReport{
			Status: StatusFailed,
			Reason: "Redpanda client uninitialized or connection disabled",
			Error:  "redpanda querier not initialized",
		}
	}

	report := RedpandaReport{Status: StatusHealthy}
	if !skipSmoke {
		pingRes, err := querier.Ping(ctx)
		if err != nil {
			return RedpandaReport{
				Status: StatusFailed,
				Reason: "Failed connecting or listing Redpanda brokers",
				Error:  fmt.Sprintf("ping failed: %v", err),
			}
		}
		report.BrokerCount = pingRes.BrokerCount
		report.PingLatency = pingRes.Latency.String()
	}

	numPartitions, ok := inspectTopicPartitions(ctx, querier, topic, &report)
	if !ok {
		return report
	}
	report.TopicPartitions = numPartitions

	endOffsets, endReasons := inspectEndOffsets(ctx, querier, topic)
	groupOffsets, groupReasons := inspectGroupOffsets(ctx, querier, topic, group)
	statusReasons := append(endReasons, groupReasons...)
	if len(statusReasons) > 0 {
		report.Status = StatusDegraded
		report.Reason = strings.Join(statusReasons, "; ")
	}
	report.Lag = CalculateConsumerLag(endOffsets, groupOffsets, topic, group)
	return report
}
