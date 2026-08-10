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

func (r *RealRedpandaQuerier) Ping(ctx context.Context) (int, time.Duration, error) {
	start := time.Now()
	brokers, err := r.client.ListBrokers(ctx)
	return len(brokers), time.Since(start), err
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

	var pingLatStr string
	var brokerCount int
	if !skipSmoke {
		bCount, lat, err := querier.Ping(ctx)
		if err != nil {
			return RedpandaReport{
				Status: StatusFailed,
				Reason: "Failed connecting or listing Redpanda brokers",
				Error:  fmt.Sprintf("ping failed: %v", err),
			}
		}
		brokerCount = bCount
		pingLatStr = lat.String()
	}

	topics, err := querier.ListTopics(ctx, topic)
	if err != nil {
		return RedpandaReport{
			Status:      StatusFailed,
			Connected:   brokerCount > 0,
			BrokerCount: brokerCount,
			PingLatency: pingLatStr,
			Reason:      fmt.Sprintf("Failed listing metadata for topic %q", topic),
			Error:       fmt.Sprintf("failed listing topics: %v", err),
		}
	}

	topicDetail, exists := topics[topic]
	if !exists {
		return RedpandaReport{
			Status:      StatusFailed,
			Connected:   true,
			BrokerCount: brokerCount,
			PingLatency: pingLatStr,
			Reason:      fmt.Sprintf("Required metrics topic %q does not exist", topic),
			Error:       fmt.Sprintf("topic %q not found", topic),
		}
	}

	if topicDetail.Err != nil {
		return RedpandaReport{
			Status:      StatusFailed,
			Connected:   true,
			BrokerCount: brokerCount,
			PingLatency: pingLatStr,
			Reason:      fmt.Sprintf("Topic %q reports error", topic),
			Error:       topicDetail.Err.Error(),
		}
	}

	numPartitions := len(topicDetail.Partitions)
	var statusReasons []string
	status := StatusHealthy

	endOffsets, err := querier.ListEndOffsets(ctx, topic)
	if err != nil {
		status = StatusDegraded
		statusReasons = append(statusReasons, fmt.Sprintf("failed listing partition end offsets for topic %q: %v", topic, err))
	} else {
		for p, o := range endOffsets[topic] {
			if o.Err != nil {
				status = StatusDegraded
				statusReasons = append(statusReasons, fmt.Sprintf("partition %d end offset error: %v", p, o.Err))
			}
		}
	}

	groupOffsets, err := querier.FetchOffsets(ctx, group)
	if err != nil {
		status = StatusDegraded
		statusReasons = append(statusReasons, fmt.Sprintf("failed fetching consumer group offsets for %q: %v", group, err))
	} else {
		for p, o := range groupOffsets[topic] {
			if o.Err != nil {
				status = StatusDegraded
				statusReasons = append(statusReasons, fmt.Sprintf("partition %d committed offset error: %v", p, o.Err))
			}
		}
	}

	lagRep := CalculateConsumerLag(endOffsets, groupOffsets, topic, group)

	var reason string
	if len(statusReasons) > 0 {
		reason = strings.Join(statusReasons, "; ")
	}

	return RedpandaReport{
		Status:          status,
		Connected:       true,
		BrokerCount:     brokerCount,
		TopicPartitions: numPartitions,
		Lag:             lagRep,
		PingLatency:     pingLatStr,
		Reason:          reason,
	}
}
