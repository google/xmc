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

// MetricsProducer is an alias for MetricProducer for backward compatibility.
type MetricsProducer = MetricProducer

// FormatCompositeKey formats the composite partitioning key (xid, work_unit_id).
func FormatCompositeKey(xid, workUnitID int64) []byte {
	return []byte(fmt.Sprintf("%d:%d", xid, workUnitID))
}

// CompositePartitionKey returns the composite partitioning key byte slice (alias).
func CompositePartitionKey(xid, workUnitID int64) []byte {
	return FormatCompositeKey(xid, workUnitID)
}

// MurmurHash2 computes the 32-bit MurmurHash2 hash of key bytes (Kafka variant).
func MurmurHash2(data []byte) uint32 {
	const seed uint32 = 0x9747b28c
	const m uint32 = 0x5bd1e995
	const r = 24

	length := len(data)
	h := seed ^ uint32(length)

	for length >= 4 {
		k := uint32(data[0]) | uint32(data[1])<<8 | uint32(data[2])<<16 | uint32(data[3])<<24
		k *= m
		k ^= k >> r
		k *= m

		h *= m
		h ^= k

		data = data[4:]
		length -= 4
	}

	switch length {
	case 3:
		h ^= uint32(data[2]) << 16
		fallthrough
	case 2:
		h ^= uint32(data[1]) << 8
		fallthrough
	case 1:
		h ^= uint32(data[0])
		h *= m
	}

	h ^= h >> 13
	h *= m
	h ^= h >> 15

	return h & 0x7fffffff
}

// BuildProducerOptions returns the kgo options required for metrics ingestion:
// - Partitioning: Key-based sticky key partitioner using 32-bit MurmurHash2
// - Compression: Zstd compression
// - Linger time: 15ms
// - Max batch size: 1MB
// - Required Acks: All (acks=all)
// - Disable Auto Topic Creation: UnknownTopicRetries(0) & DisableAutoTopicCreation(true)
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

// NewProducerOptions builds kgo options required for metrics ingestion (alias).
func NewProducerOptions(cfg *Config) []kgo.Opt {
	return BuildProducerOptions(cfg)
}

// buildKgoOptions builds kgo options required for metrics ingestion (alias for backward compatibility).
func buildKgoOptions(cfg *Config) []kgo.Opt {
	return BuildProducerOptions(cfg)
}

// NewKafkaProducer constructs a kgo.Client with default producer configuration.
func NewKafkaProducer(cfg *Config) (*kgo.Client, error) {
	opts := BuildProducerOptions(cfg)
	return kgo.NewClient(opts...)
}
