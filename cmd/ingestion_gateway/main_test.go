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
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/twmb/franz-go/pkg/kadm"
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

func TestHealthAndReadinessHandlers(t *testing.T) {
	server := startHTTPServer(context.Background(), "0") // 0 binds ephemeral port
	defer server.Close()

	if server.ReadHeaderTimeout == 0 {
		t.Errorf("server.ReadHeaderTimeout = 0, want > 0")
	}

	endpoints := []string{"/healthz", "/readyz"}
	for _, ep := range endpoints {
		t.Run(ep, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, ep, nil)
			w := httptest.NewRecorder()

			server.Handler.ServeHTTP(w, req)

			if w.Code != http.StatusOK {
				t.Errorf("%s returned status %d, want %d", ep, w.Code, http.StatusOK)
			}
			if body := w.Body.String(); body != "OK" {
				t.Errorf("%s returned body %q, want %q", ep, body, "OK")
			}
		})
	}
}

func TestBuildKgoOptions(t *testing.T) {
	tests := []struct {
		name string
		cfg  Config
	}{
		{
			name: "Default config without SASL",
			cfg: Config{
				RedpandaBrokers: []string{"localhost:9092"},
				SASLEnabled:     false,
			},
		},
		{
			name: "SASL enabled SCRAM-SHA-256",
			cfg: Config{
				RedpandaBrokers: []string{"localhost:9092"},
				SASLEnabled:     true,
				SASLMechanism:   "SCRAM-SHA-256",
				SASLUser:        "user",
				SASLPassword:    "pass",
			},
		},
		{
			name: "SASL enabled PLAIN",
			cfg: Config{
				RedpandaBrokers: []string{"localhost:9092"},
				SASLEnabled:     true,
				SASLMechanism:   "PLAIN",
				SASLUser:        "user",
				SASLPassword:    "pass",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			opts := BuildProducerOptions(&tt.cfg)
			if len(opts) == 0 {
				t.Errorf("BuildProducerOptions() returned empty options slice")
			}
		})
	}
}

func TestStartHTTPServerWithProducer(t *testing.T) {
	mp := NewMockProducer()
	srv := startHTTPServer(context.Background(), "0", mp)
	defer srv.Close()

	if srv == nil {
		t.Fatalf("expected non-nil server from startHTTPServer")
	}
}
