// Copyright 2025 Google LLC
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
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	xidgrpcpb "xmanager_cloud/api/protos/xid"
	commonhealth "xmanager_cloud/internal/common/health"
	"xmanager_cloud/internal/storage/spanner/migrations/version"
)

type fakeXidClient struct {
	xidgrpcpb.XidServiceClient
}

type fakeTaskQueueClient struct {
	PingErr error
}

func (f *fakeTaskQueueClient) PingRedis(ctx context.Context) error {
	return f.PingErr
}

type mockSpannerClient struct {
	pingErr    error
	versionVal int64
	dirty      bool
	versionErr error
}

func (m mockSpannerClient) Ping(ctx context.Context) error {
	return m.pingErr
}

func (m mockSpannerClient) QuerySchemaVersion(ctx context.Context) (int64, bool, error) {
	return m.versionVal, m.dirty, m.versionErr
}

func TestActiveHealthCheck(t *testing.T) {
	tests := []struct {
		name        string
		mockSpanner commonhealth.SpannerHealthClient
		setupTq     func() TaskQueueClient
		wantCode    int
		wantBody    string
	}{
		{
			name:        "redis_ping_error",
			mockSpanner: mockSpannerClient{versionVal: version.EssExpectedSchemaVersion()},
			setupTq: func() TaskQueueClient {
				return &fakeTaskQueueClient{PingErr: fmt.Errorf("fake redis error")}
			},
			wantCode: http.StatusInternalServerError,
			wantBody: "fake redis error",
		},
		{
			name:        "all_valid",
			mockSpanner: mockSpannerClient{versionVal: version.EssExpectedSchemaVersion()},
			setupTq: func() TaskQueueClient {
				return &fakeTaskQueueClient{}
			},
			wantCode: http.StatusOK,
			wantBody: "OK",
		},
		{
			name:        "nil_spanner",
			mockSpanner: nil,
			setupTq:     func() TaskQueueClient { return &fakeTaskQueueClient{} },
			wantCode:    http.StatusInternalServerError,
			wantBody:    "spanner: client is nil",
		},
		{
			name: "spanner_version_older",
			mockSpanner: mockSpannerClient{
				versionVal: version.EssExpectedSchemaVersion() - 1,
			},
			setupTq:  func() TaskQueueClient { return &fakeTaskQueueClient{} },
			wantCode: http.StatusInternalServerError,
			wantBody: "does not match expected version",
		},
		{
			name: "spanner_version_dirty",
			mockSpanner: mockSpannerClient{
				versionVal: version.EssExpectedSchemaVersion(),
				dirty:      true,
			},
			setupTq:  func() TaskQueueClient { return &fakeTaskQueueClient{} },
			wantCode: http.StatusInternalServerError,
			wantBody: "is in a dirty state",
		},
		{
			name: "spanner_query_error",
			mockSpanner: mockSpannerClient{
				versionErr: fmt.Errorf("failed to query SchemaMigrations: table not found"),
			},
			setupTq:  func() TaskQueueClient { return &fakeTaskQueueClient{} },
			wantCode: http.StatusInternalServerError,
			wantBody: "SchemaMigrations",
		},
		{
			name: "spanner_version_newer",
			mockSpanner: mockSpannerClient{
				versionVal: version.EssExpectedSchemaVersion() + 1,
			},
			setupTq:  func() TaskQueueClient { return &fakeTaskQueueClient{} },
			wantCode: http.StatusInternalServerError,
			wantBody: "does not match expected version",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var tqClient TaskQueueClient
			if tc.setupTq != nil {
				tqClient = tc.setupTq()
			}

			var xidClient xidgrpcpb.XidServiceClient = &fakeXidClient{}
			handler := activeHealthCheck(tc.mockSpanner, xidClient, tqClient)

			req := httptest.NewRequest("GET", "/readyz", nil)
			rr := httptest.NewRecorder()

			handler.ServeHTTP(rr, req)

			if rr.Code != tc.wantCode {
				t.Errorf("activeHealthCheck() status code = %v, want %v", rr.Code, tc.wantCode)
			}

			gotBody := rr.Body.String()
			if tc.wantBody != "" {
				if !strings.Contains(gotBody, tc.wantBody) {
					t.Errorf("activeHealthCheck() body = %q, want substring %q", gotBody, tc.wantBody)
				}
			}
		})
	}
}

func TestLivenessCheck(t *testing.T) {
	handler := livenessCheck()

	req := httptest.NewRequest("GET", "/healthz", nil)
	rr := httptest.NewRecorder()

	handler.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Errorf("livenessCheck() status code = %v, want %v", rr.Code, http.StatusOK)
	}

	gotBody := rr.Body.String()
	wantBody := "OK"
	if gotBody != wantBody {
		t.Errorf("livenessCheck() body = %q, want %q", gotBody, wantBody)
	}
}
