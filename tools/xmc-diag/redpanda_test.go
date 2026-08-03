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
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/twmb/franz-go/pkg/kadm"
)

func TestCalculateConsumerLag(t *testing.T) {
	topic := "xmc-metrics"
	group := "xmc-ingester"

	endOffsets := kadm.ListedOffsets{
		topic: {
			1: kadm.ListedOffset{Topic: topic, Partition: 1, Offset: 2500},
			0: kadm.ListedOffset{Topic: topic, Partition: 0, Offset: 1000},
		},
	}

	groupOffsets := kadm.OffsetResponses{
		topic: {
			0: kadm.OffsetResponse{Offset: kadm.Offset{Topic: topic, Partition: 0, At: 800}},
			1: kadm.OffsetResponse{Offset: kadm.Offset{Topic: topic, Partition: 1, At: 2000}},
		},
	}

	lagRep := CalculateConsumerLag(endOffsets, groupOffsets, topic, group)
	if lagRep.TotalLag != 700 {
		t.Errorf("TotalLag = %d, want 700 (200 + 500)", lagRep.TotalLag)
	}
	if len(lagRep.Partitions) != 2 {
		t.Fatalf("Partitions len = %d, want 2", len(lagRep.Partitions))
	}
	if lagRep.Partitions[0].Partition != 0 || lagRep.Partitions[1].Partition != 1 {
		t.Errorf("Partitions not sorted deterministically: %v", lagRep.Partitions)
	}
}

func TestCheckRedpanda_Healthy(t *testing.T) {
	ctx := context.Background()
	topic := "xmc-metrics"
	group := "xmc-ingester"

	mock := &MockRedpandaQuerier{
		pingLat: 5 * time.Millisecond,
		topicDetails: kadm.TopicDetails{
			topic: kadm.TopicDetail{
				Topic: topic,
				Partitions: kadm.PartitionDetails{
					0: kadm.PartitionDetail{Partition: 0, Leader: 1, Replicas: []int32{1}},
				},
			},
		},
		endOffsets: kadm.ListedOffsets{
			topic: {
				0: kadm.ListedOffset{Topic: topic, Partition: 0, Offset: 500},
			},
		},
		offsetResp: kadm.OffsetResponses{
			topic: {
				0: kadm.OffsetResponse{Offset: kadm.Offset{Topic: topic, Partition: 0, At: 500}},
			},
		},
	}

	rep := CheckRedpanda(ctx, mock, topic, group, false)
	if rep.Status != StatusHealthy {
		t.Errorf("Status = %q, want %q", rep.Status, StatusHealthy)
	}
	if !rep.Connected {
		t.Errorf("Connected = false, want true")
	}
	if rep.TopicPartitions != 1 {
		t.Errorf("TopicPartitions = %d, want 1", rep.TopicPartitions)
	}
	if rep.Lag.TotalLag != 0 {
		t.Errorf("TotalLag = %d, want 0", rep.Lag.TotalLag)
	}
}

func TestCheckRedpanda_DegradedReason(t *testing.T) {
	ctx := context.Background()
	topic := "xmc-metrics"
	group := "xmc-ingester"

	mock := &MockRedpandaQuerier{
		pingLat: 5 * time.Millisecond,
		topicDetails: kadm.TopicDetails{
			topic: kadm.TopicDetail{
				Topic: topic,
				Partitions: kadm.PartitionDetails{
					0: kadm.PartitionDetail{Partition: 0, Leader: 1, Replicas: []int32{1}},
				},
			},
		},
		endOffsets: kadm.ListedOffsets{
			topic: {
				0: kadm.ListedOffset{Topic: topic, Partition: 0, Offset: 500},
			},
		},
		offsetErr: errors.New("group offsets fetch error"),
	}

	rep := CheckRedpanda(ctx, mock, topic, group, false)
	if rep.Status != StatusDegraded {
		t.Errorf("Status = %q, want %q", rep.Status, StatusDegraded)
	}
	if !strings.Contains(rep.Reason, "failed fetching consumer group offsets for \"xmc-ingester\"") {
		t.Errorf("Reason = %q, expected group offset error reason", rep.Reason)
	}
}
