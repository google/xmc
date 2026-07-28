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

package main

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/twmb/franz-go/pkg/kadm"
	"github.com/twmb/franz-go/pkg/kgo"
	"github.com/twmb/franz-go/pkg/sasl/plain"
	"github.com/twmb/franz-go/pkg/sasl/scram"
)

// TopologyQuerier abstracts Redpanda cluster metadata queries for topic validation.
type TopologyQuerier interface {
	ListTopics(ctx context.Context, topics ...string) (kadm.TopicDetails, error)
}

// ValidateTopicTopology asserts topic existence, partition count >= minPartitions, and replication factor >= minReplicas.
func ValidateTopicTopology(ctx context.Context, querier TopologyQuerier, topicName string, minPartitions, minReplicas int) error {
	if topicName == "" {
		return errors.New("topicName cannot be empty")
	}

	details, err := querier.ListTopics(ctx, topicName)
	if err != nil {
		return fmt.Errorf("failed to query topic metadata: %w", err)
	}

	detail, ok := details[topicName]
	if !ok {
		return fmt.Errorf("required topic %q does not exist in cluster", topicName)
	}

	if detail.Err != nil {
		return fmt.Errorf("topic %q metadata error: %w", topicName, detail.Err)
	}

	numPartitions := len(detail.Partitions)
	if numPartitions < minPartitions {
		return fmt.Errorf("topic %q has %d partitions, expected >= %d", topicName, numPartitions, minPartitions)
	}

	for pID, pDetail := range detail.Partitions {
		if pDetail.Err != nil {
			return fmt.Errorf("partition %d metadata error: %w", pID, pDetail.Err)
		}
		numReplicas := len(pDetail.Replicas)
		if numReplicas < minReplicas {
			return fmt.Errorf("partition %d of topic %q has %d replicas, expected >= %d", pID, topicName, numReplicas, minReplicas)
		}
	}

	return nil
}

// MetricProducer defines the interface for producing metric records asynchronously to Kafka/Redpanda.
type MetricProducer interface {
	Produce(ctx context.Context, rec *kgo.Record, promise func(*kgo.Record, error))
	Close()
}

// FormatCompositeKey formats the composite partitioning key (xid, work_unit_id).
func FormatCompositeKey(xid, workUnitID int64) []byte {
	buf := make([]byte, 0, 32)
	buf = strconv.AppendInt(buf, xid, 10)
	buf = append(buf, ':')
	buf = strconv.AppendInt(buf, workUnitID, 10)
	return buf
}

// BuildProducerOptions returns the kgo options required for metrics ingestion:
// - Partitioning: Key-based sticky key partitioner using 32-bit MurmurHash2
// - Compression: Zstd compression
// - Linger time: 15ms
// - Max batch size: 1MB
// - Required Acks: All (acks=all)
// - Disable Auto Topic Creation: UnknownTopicRetries(0)
// - Optional TLS / mTLS configuration with dynamic certificate reloading
func BuildProducerOptions(cfg *Config) ([]kgo.Opt, error) {
	opts := []kgo.Opt{
		kgo.SeedBrokers(cfg.RedpandaBrokers...),
		kgo.RecordPartitioner(kgo.StickyKeyPartitioner(nil)),
		kgo.ProducerBatchCompression(kgo.ZstdCompression()),
		kgo.ProducerLinger(15 * time.Millisecond),
		kgo.ProducerBatchMaxBytes(1 * 1024 * 1024),
		kgo.RequiredAcks(kgo.AllISRAcks()),
		kgo.UnknownTopicRetries(0),
	}

	if cfg.SASLEnabled {
		switch strings.ToUpper(cfg.SASLMechanism) {
		case "SCRAM-SHA-256":
			opts = append(opts, kgo.SASL(scram.Auth{User: cfg.SASLUser, Pass: cfg.SASLPassword}.AsSha256Mechanism()))
		case "SCRAM-SHA-512":
			opts = append(opts, kgo.SASL(scram.Auth{User: cfg.SASLUser, Pass: cfg.SASLPassword}.AsSha512Mechanism()))
		case "PLAIN":
			opts = append(opts, kgo.SASL(plain.Auth{User: cfg.SASLUser, Pass: cfg.SASLPassword}.AsMechanism()))
		}
	}

	if cfg.TLSEnabled {
		tlsConfig, _, err := BuildTLSConfig(cfg)
		if err != nil {
			return nil, fmt.Errorf("failed to build TLS config: %w", err)
		}
		if tlsConfig != nil {
			opts = append(opts, kgo.DialTLSConfig(tlsConfig))
		}
	}

	return opts, nil
}

// NewKafkaProducer constructs a kgo.Client with default producer configuration.
func NewKafkaProducer(cfg *Config) (*kgo.Client, error) {
	opts, err := BuildProducerOptions(cfg)
	if err != nil {
		return nil, err
	}
	return kgo.NewClient(opts...)
}

var congestionErrorSubstrings = []string{
	"queue limit",
	"queue full",
	"buffer full",
	"saturated",
	"paused",
	"unreachable",
	"connection refused",
	"broker outage",
	"broker produce error",
}

// IsCongestionError determines if a produce error is caused by queue saturation, broker pause, or broker outage.
func IsCongestionError(err error) bool {
	if err == nil {
		return false
	}

	if errors.Is(err, kgo.ErrClientClosed) {
		return true
	}

	msg := strings.ToLower(err.Error())
	for _, sub := range congestionErrorSubstrings {
		if strings.Contains(msg, sub) {
			return true
		}
	}
	return false
}
