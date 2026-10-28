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

package ingestiongateway

import (
	"context"
	"errors"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/twmb/franz-go/pkg/kadm"
	"github.com/twmb/franz-go/pkg/kerr"
	"github.com/twmb/franz-go/pkg/kgo"
)

type mockTopologyQuerier struct {
	details kadm.TopicDetails
	err     error
}

func (m *mockTopologyQuerier) ListTopics(ctx context.Context, topics ...string) (kadm.TopicDetails, error) {
	if m.err != nil {
		return nil, m.err
	}
	return m.details, nil
}

type sequenceTopologyQuerier struct {
	responses []mockTopologyQuerier
	calls     int
}

func (s *sequenceTopologyQuerier) ListTopics(ctx context.Context, topics ...string) (kadm.TopicDetails, error) {
	idx := min(s.calls, len(s.responses)-1)
	s.calls++
	return s.responses[idx].ListTopics(ctx, topics...)
}

func createMockPartitions(numPartitions, replicasPerPartition int) kadm.PartitionDetails {
	parts := make(kadm.PartitionDetails)
	for i := 0; i < numPartitions; i++ {
		replicas := make([]int32, replicasPerPartition)
		for r := 0; r < replicasPerPartition; r++ {
			replicas[r] = int32(r + 1)
		}
		parts[int32(i)] = kadm.PartitionDetail{
			Partition: int32(i),
			Leader:    1,
			Replicas:  replicas,
			ISR:       replicas,
		}
	}
	return parts
}

func TestValidateTopicTopology(t *testing.T) {
	tests := []struct {
		name          string
		querier       *mockTopologyQuerier
		topicName     string
		minPartitions int
		minReplicas   int
		wantErr       bool
	}{
		{
			name: "Valid topology assertions pass",
			querier: &mockTopologyQuerier{
				details: kadm.TopicDetails{
					"xmc-metrics": kadm.TopicDetail{
						Topic:      "xmc-metrics",
						Partitions: createMockPartitions(128, 3),
					},
				},
			},
			topicName:     "xmc-metrics",
			minPartitions: 128,
			minReplicas:   3,
			wantErr:       false,
		},
		{
			name:          "Empty topic name fails topology validation",
			querier:       &mockTopologyQuerier{details: kadm.TopicDetails{}},
			topicName:     "",
			minPartitions: 1,
			minReplicas:   1,
			wantErr:       true,
		},
		{
			name: "ListTopics error fails topology validation",
			querier: &mockTopologyQuerier{
				err: errors.New("connection failed"),
			},
			topicName:     "xmc-metrics",
			minPartitions: 128,
			minReplicas:   3,
			wantErr:       true,
		},
		{
			name: "Missing topic fails topology validation",
			querier: &mockTopologyQuerier{
				details: kadm.TopicDetails{},
			},
			topicName:     "xmc-metrics",
			minPartitions: 128,
			minReplicas:   3,
			wantErr:       true,
		},
		{
			name: "Topic metadata error fails validation",
			querier: &mockTopologyQuerier{
				details: kadm.TopicDetails{
					"xmc-metrics": kadm.TopicDetail{
						Topic: "xmc-metrics",
						Err:   errors.New("topic authorization error"),
					},
				},
			},
			topicName:     "xmc-metrics",
			minPartitions: 128,
			minReplicas:   3,
			wantErr:       true,
		},
		{
			name: "Insufficient partitions fails topology validation",
			querier: &mockTopologyQuerier{
				details: kadm.TopicDetails{
					"xmc-metrics": kadm.TopicDetail{
						Topic:      "xmc-metrics",
						Partitions: createMockPartitions(64, 3),
					},
				},
			},
			topicName:     "xmc-metrics",
			minPartitions: 128,
			minReplicas:   3,
			wantErr:       true,
		},
		{
			name: "Insufficient replicas fails topology validation",
			querier: &mockTopologyQuerier{
				details: kadm.TopicDetails{
					"xmc-metrics": kadm.TopicDetail{
						Topic:      "xmc-metrics",
						Partitions: createMockPartitions(128, 2),
					},
				},
			},
			topicName:     "xmc-metrics",
			minPartitions: 128,
			minReplicas:   3,
			wantErr:       true,
		},
		{
			name: "Partition detail error fails topology validation",
			querier: &mockTopologyQuerier{
				details: kadm.TopicDetails{
					"xmc-metrics": kadm.TopicDetail{
						Topic: "xmc-metrics",
						Partitions: kadm.PartitionDetails{
							0: kadm.PartitionDetail{
								Partition: 0,
								Err:       errors.New("partition broker offline"),
							},
						},
					},
				},
			},
			topicName:     "xmc-metrics",
			minPartitions: 1,
			minReplicas:   1,
			wantErr:       true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateTopicTopology(context.Background(), tt.querier, tt.topicName, tt.minPartitions, tt.minReplicas)
			if (err != nil) != tt.wantErr {
				t.Errorf("ValidateTopicTopology() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestValidateTopicTopologyWithRetry(t *testing.T) {
	validDetails := kadm.TopicDetails{
		"xmc-metrics": kadm.TopicDetail{
			Topic:      "xmc-metrics",
			Partitions: createMockPartitions(128, 1),
		},
	}

	tests := []struct {
		name      string
		querier   *sequenceTopologyQuerier
		topicName string
		timeout   time.Duration
		interval  time.Duration
		wantCalls int
		wantErr   bool
	}{
		{
			name: "Succeeds immediately on first attempt",
			querier: &sequenceTopologyQuerier{
				responses: []mockTopologyQuerier{
					{details: validDetails},
				},
			},
			topicName: "xmc-metrics",
			timeout:   100 * time.Millisecond,
			interval:  5 * time.Millisecond,
			wantCalls: 1,
			wantErr:   false,
		},
		{
			name: "Succeeds after transient missing topic and broker error",
			querier: &sequenceTopologyQuerier{
				responses: []mockTopologyQuerier{
					{err: errors.New("broker starting")},
					{details: kadm.TopicDetails{}},
					{details: validDetails},
				},
			},
			topicName: "xmc-metrics",
			timeout:   200 * time.Millisecond,
			interval:  5 * time.Millisecond,
			wantCalls: 3,
			wantErr:   false,
		},
		{
			name: "Fails immediately on empty topic name without polling",
			querier: &sequenceTopologyQuerier{
				responses: []mockTopologyQuerier{
					{details: validDetails},
				},
			},
			topicName: "",
			timeout:   50 * time.Millisecond,
			interval:  5 * time.Millisecond,
			wantCalls: 0,
			wantErr:   true,
		},
		{
			name: "Fails when retry timeout expires",
			querier: &sequenceTopologyQuerier{
				responses: []mockTopologyQuerier{
					{details: kadm.TopicDetails{}},
				},
			},
			topicName: "xmc-metrics",
			timeout:   25 * time.Millisecond,
			interval:  10 * time.Millisecond,
			wantErr:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateTopicTopologyWithRetry(
				context.Background(),
				tt.querier,
				tt.topicName,
				128,
				1,
				tt.timeout,
				tt.interval,
			)
			if (err != nil) != tt.wantErr {
				t.Fatalf("ValidateTopicTopologyWithRetry() error = %v, wantErr %v", err, tt.wantErr)
			}
			if tt.wantCalls > 0 && tt.querier.calls != tt.wantCalls {
				t.Errorf("querier calls = %d, want %d", tt.querier.calls, tt.wantCalls)
			}
		})
	}
}

func TestBuildProducerOptions(t *testing.T) {
	cfg := &Config{
		RedpandaBrokers: []string{"localhost:9092", "localhost:9093"},
		TopicName:       "xmc-metrics",
		SASLEnabled:     true,
		SASLMechanism:   "SCRAM-SHA-256",
		SASLUser:        "user",
		SASLPassword:    "pass",
	}

	opts, err := BuildProducerOptions(cfg, nil)
	if err != nil {
		t.Fatalf("unexpected error building producer options: %v", err)
	}
	if len(opts) == 0 {
		t.Fatalf("expected non-empty kgo options")
	}

	client, err := kgo.NewClient(opts...)
	if err != nil {
		t.Fatalf("failed to construct kgo.Client with options: %v", err)
	}
	defer client.Close()
}

func TestBuildProducerOptions_WithMetrics(t *testing.T) {
	cfg := &Config{
		RedpandaBrokers: []string{"localhost:9092"},
		TopicName:       "xmc-metrics",
	}
	metrics := NewGatewayMetrics(nil)

	opts, err := BuildProducerOptions(cfg, metrics)
	if err != nil {
		t.Fatalf("unexpected error building producer options with metrics: %v", err)
	}

	client, err := kgo.NewClient(opts...)
	if err != nil {
		t.Fatalf("failed to construct kgo.Client with metrics hooks: %v", err)
	}
	defer client.Close()
}

func TestNewKafkaProducer(t *testing.T) {
	cfg := &Config{
		RedpandaBrokers: []string{"127.0.0.1:9092"},
		TopicName:       "xmc-metrics",
	}

	producer, err := NewKafkaProducer(cfg, nil)
	if err != nil {
		t.Fatalf("failed to construct Kafka producer: %v", err)
	}
	if producer == nil {
		t.Fatalf("expected non-nil KafkaProducer")
	}
	producer.Close()
}

func TestBuildProducerOptions_AllSASLMechanisms(t *testing.T) {
	tests := []struct {
		name        string
		saslEnabled bool
		mechanism   string
		wantErr     bool
	}{
		{name: "Disabled SASL", saslEnabled: false, mechanism: ""},
		{name: "SCRAM-SHA-256", saslEnabled: true, mechanism: "SCRAM-SHA-256"},
		{name: "SCRAM-SHA-512", saslEnabled: true, mechanism: "SCRAM-SHA-512"},
		{name: "PLAIN", saslEnabled: true, mechanism: "PLAIN"},
		// An unsupported mechanism must fail loudly. Building options without SASL attached would
		// hand back a client that connects unauthenticated.
		{name: "Unknown Mechanism", saslEnabled: true, mechanism: "UNSUPPORTED", wantErr: true},
		{name: "Empty Mechanism", saslEnabled: true, mechanism: "", wantErr: true},
		// SASLEnabled is what gates the switch, so a bad mechanism is ignored while SASL is off.
		{name: "Unknown Mechanism With SASL Disabled", saslEnabled: false, mechanism: "UNSUPPORTED"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := &Config{
				RedpandaBrokers: []string{"localhost:9092"},
				SASLEnabled:     tt.saslEnabled,
				SASLMechanism:   tt.mechanism,
				SASLUser:        "testuser",
				SASLPassword:    "testpass",
			}
			opts, err := BuildProducerOptions(cfg, nil)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected an error for mechanism %q, got none", tt.mechanism)
				}
				if !strings.Contains(err.Error(), "unsupported sasl_mechanism") {
					t.Errorf("expected an unsupported sasl_mechanism error, got %v", err)
				}
				if opts != nil {
					t.Errorf("expected nil options alongside the error, got %d", len(opts))
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error building producer options for %s: %v", tt.name, err)
			}
			if len(opts) == 0 {
				t.Fatalf("expected options for %s", tt.name)
			}
			client, err := kgo.NewClient(opts...)
			if err != nil {
				t.Fatalf("failed to construct kgo.Client for %s: %v", tt.name, err)
			}
			client.Close()
		})
	}
}

func TestNewKafkaProducer_EmptyBrokers(t *testing.T) {
	cfg := &Config{
		RedpandaBrokers: nil,
	}
	producer, err := NewKafkaProducer(cfg, nil)
	if err != nil {
		t.Logf("NewKafkaProducer returned error for empty brokers as expected: %v", err)
	} else if producer != nil {
		producer.Close()
	}
}

// TestKafkaProducer_ProduceClassifiesError asserts the adapter hands the handler a *PublishError
// rather than a raw franz-go error, so the HTTP mapping stays backend-neutral.
//
// An empty topic is rejected by franz-go before it buffers or dials anything, which makes this
// deterministic without a broker.
func TestKafkaProducer_ProduceClassifiesError(t *testing.T) {
	cfg := &Config{
		RedpandaBrokers: []string{"127.0.0.1:9092"},
		TopicName:       "xmc-metrics",
	}
	producer, err := NewKafkaProducer(cfg, nil)
	if err != nil {
		t.Fatalf("failed to construct Kafka producer: %v", err)
	}
	defer producer.Close()

	rec := &MetricRecord{Key: []byte("1:1"), Value: []byte("v")}
	done := make(chan error, 1)
	producer.Produce(context.Background(), rec, func(gotRec *MetricRecord, err error) {
		if gotRec != rec {
			t.Errorf("expected the promise to receive the original record")
		}
		done <- err
	})

	select {
	case gotErr := <-done:
		if gotErr == nil {
			t.Fatalf("expected producing a record with no topic to fail")
		}
		var pubErr *PublishError
		if !errors.As(gotErr, &pubErr) {
			t.Fatalf("expected a *PublishError, got %T: %v", gotErr, gotErr)
		}
		if pubErr.Kind != PublishErrorUnknown {
			t.Errorf("expected kind %v, got %v", PublishErrorUnknown, pubErr.Kind)
		}
	case <-time.After(5 * time.Second):
		t.Fatalf("timed out waiting for the produce promise")
	}
}

func TestClassifyKafkaError(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		wantKind PublishErrorKind
		wantNil  bool
	}{
		{name: "nil error stays nil", err: nil, wantNil: true},
		{name: "MessageTooLarge is a client fault", err: kerr.MessageTooLarge, wantKind: PublishErrorClient},
		{name: "ThrottlingQuotaExceeded is congestion", err: kerr.ThrottlingQuotaExceeded, wantKind: PublishErrorCongestion},
		{name: "client closed is congestion", err: kgo.ErrClientClosed, wantKind: PublishErrorCongestion},
		{name: "unrelated error is unknown", err: errors.New("invalid proto format"), wantKind: PublishErrorUnknown},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := classifyKafkaError(tt.err)
			if tt.wantNil {
				if got != nil {
					t.Fatalf("classifyKafkaError(nil) = %v, want nil", got)
				}
				return
			}
			var pubErr *PublishError
			if !errors.As(got, &pubErr) {
				t.Fatalf("expected a *PublishError, got %T", got)
			}
			if pubErr.Kind != tt.wantKind {
				t.Errorf("classifyKafkaError(%v) kind = %v, want %v", tt.err, pubErr.Kind, tt.wantKind)
			}
		})
	}
}

func TestIsCongestionError(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{name: "nil error", err: nil, want: false},
		{name: "kgo ErrClientClosed", err: kgo.ErrClientClosed, want: true},
		{name: "kerr ThrottlingQuotaExceeded (Code 89)", err: kerr.ThrottlingQuotaExceeded, want: true},
		{name: "kerr NotEnoughReplicas (Code 19)", err: kerr.NotEnoughReplicas, want: true},
		{name: "kerr LeaderNotAvailable (Code 5)", err: kerr.LeaderNotAvailable, want: true},
		{name: "kerr RequestTimedOut (Code 7)", err: kerr.RequestTimedOut, want: true},
		{name: "kerr BrokerNotAvailable (Code 8)", err: kerr.BrokerNotAvailable, want: true},
		{name: "kerr KafkaStorageError (Code 56)", err: kerr.KafkaStorageError, want: true},
		{name: "net OpError dial failure", err: &net.OpError{Op: "dial", Net: "tcp", Err: errors.New("connection refused")}, want: true},
		{name: "queue limit exceeded string fallback", err: errors.New("franz-go: queue limit reached"), want: true},
		{name: "broker outage string fallback", err: errors.New("broker outage: connection refused"), want: true},
		{name: "unrelated error", err: errors.New("invalid proto format"), want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsCongestionError(tt.err); got != tt.want {
				t.Errorf("IsCongestionError(%v) = %v, want %v", tt.err, got, tt.want)
			}
		})
	}
}

func TestIsClientProduceError(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{name: "nil error", err: nil, want: false},
		{name: "kerr MessageTooLarge (Code 10)", err: kerr.MessageTooLarge, want: true},
		{name: "kerr RecordListTooLarge (Code 18)", err: kerr.RecordListTooLarge, want: true},
		{name: "kerr InvalidRecord (Code 87)", err: kerr.InvalidRecord, want: true},
		{name: "kerr InvalidTimestamp (Code 32)", err: kerr.InvalidTimestamp, want: true},
		{name: "kerr RequestTimedOut (Code 7)", err: kerr.RequestTimedOut, want: false},
		{name: "unrelated error", err: errors.New("some other error"), want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsClientProduceError(tt.err); got != tt.want {
				t.Errorf("IsClientProduceError(%v) = %v, want %v", tt.err, got, tt.want)
			}
		})
	}
}
