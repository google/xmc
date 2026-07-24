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
	"fmt"
	"strings"
	"time"

	"github.com/twmb/franz-go/pkg/kgo"
	"github.com/twmb/franz-go/pkg/sasl/plain"
	"github.com/twmb/franz-go/pkg/sasl/scram"
)

// MetricProducer defines the interface for producing metric records asynchronously to Kafka/Redpanda.
type MetricProducer interface {
	Produce(ctx context.Context, rec *kgo.Record, promise func(*kgo.Record, error))
	Close()
}

// FormatCompositeKey formats the composite partitioning key (xid, work_unit_id).
func FormatCompositeKey(xid, workUnitID int64) []byte {
	return []byte(fmt.Sprintf("%d:%d", xid, workUnitID))
}

// BuildProducerOptions returns the kgo options required for metrics ingestion:
// - Partitioning: Key-based sticky key partitioner using 32-bit MurmurHash2
// - Compression: Zstd compression
// - Linger time: 15ms
// - Max batch size: 1MB
// - Required Acks: All (acks=all)
// - Disable Auto Topic Creation: UnknownTopicRetries(0)
func BuildProducerOptions(cfg *Config) []kgo.Opt {
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

	return opts
}

// NewKafkaProducer constructs a kgo.Client with default producer configuration.
func NewKafkaProducer(cfg *Config) (*kgo.Client, error) {
	opts := BuildProducerOptions(cfg)
	return kgo.NewClient(opts...)
}
