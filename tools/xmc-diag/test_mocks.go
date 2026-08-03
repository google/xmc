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
	"time"

	"github.com/twmb/franz-go/pkg/kadm"
)

type MockClickHouseQuerier struct {
	pingErr        error
	version        string
	versionErr     error
	missingTables  []string
	checkTablesErr error
	latency        time.Duration
	latencyErr     error
}

func (m *MockClickHouseQuerier) Ping(ctx context.Context) error {
	return m.pingErr
}

func (m *MockClickHouseQuerier) SelectVersion(ctx context.Context) (string, error) {
	return m.version, m.versionErr
}

func (m *MockClickHouseQuerier) CheckTablesExist(ctx context.Context, tables []string) ([]string, error) {
	return m.missingTables, m.checkTablesErr
}

func (m *MockClickHouseQuerier) MeasureLatency(ctx context.Context) (time.Duration, error) {
	return m.latency, m.latencyErr
}

type MockRedpandaQuerier struct {
	pingLat      time.Duration
	pingErr      error
	topicDetails kadm.TopicDetails
	topicErr     error
	endOffsets   kadm.ListedOffsets
	endErr       error
	offsetResp   kadm.OffsetResponses
	offsetErr    error
}

func (m *MockRedpandaQuerier) Ping(ctx context.Context) (time.Duration, error) {
	return m.pingLat, m.pingErr
}

func (m *MockRedpandaQuerier) ListTopics(ctx context.Context, topics ...string) (kadm.TopicDetails, error) {
	return m.topicDetails, m.topicErr
}

func (m *MockRedpandaQuerier) ListEndOffsets(ctx context.Context, topics ...string) (kadm.ListedOffsets, error) {
	return m.endOffsets, m.endErr
}

func (m *MockRedpandaQuerier) FetchOffsets(ctx context.Context, group string) (kadm.OffsetResponses, error) {
	return m.offsetResp, m.offsetErr
}
