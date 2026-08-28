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
	"testing"

	"xmanager_cloud/cmd/ingestion_gateway/ingestiongateway"
)

type stubProducer struct{}

func (s *stubProducer) Produce(ctx context.Context, record *ingestiongateway.MetricRecord, promise func(*ingestiongateway.MetricRecord, error)) {
	if promise != nil {
		promise(record, nil)
	}
}

func (s *stubProducer) Close() {}

func TestStartHTTPServerWithProducer(t *testing.T) {
	mp := &stubProducer{}
	srv := startHTTPServer(context.Background(), "0", "xmc-metrics", nil, mp)
	defer srv.Close()

	if srv == nil {
		t.Fatalf("expected non-nil server from startHTTPServer")
	}
}
