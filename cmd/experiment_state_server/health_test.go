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

	"golang/cloud_google_com/go/spanner/v/v1/spanner"
	"xmanager_cloud/storage/spanner/emulator/emulator"
	xidgrpcpb "xmanager_cloud/xid_service/proto/api_go_grpc_proto"
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

func TestActiveHealthCheck(t *testing.T) {
	validSpanner := func(t *testing.T) *spanner.Client {
		t.Helper()
		sc, _, _ := emulator.SetupWithEmulator(context.Background(), t)
		return sc
	}

	tests := []struct {
		name         string
		setupSpanner func(t *testing.T) *spanner.Client
		setupTq      func() TaskQueueClient
		wantCode     int
		wantBody     string
	}{
		{
			name:         "redis_ping_error",
			setupSpanner: validSpanner,
			setupTq: func() TaskQueueClient {
				return &fakeTaskQueueClient{PingErr: fmt.Errorf("fake redis error")}
			},
			wantCode: http.StatusInternalServerError,
			wantBody: "fake redis error",
		},
		{
			name:         "all_valid",
			setupSpanner: validSpanner,
			setupTq: func() TaskQueueClient {
				return &fakeTaskQueueClient{}
			},
			wantCode: http.StatusOK,
			wantBody: "OK",
		},
		{
			name:         "nil_spanner",
			setupTq:      func() TaskQueueClient { return &fakeTaskQueueClient{} },
			setupSpanner: func(t *testing.T) *spanner.Client { return nil },
			wantCode:     http.StatusInternalServerError,
			wantBody:     "spanner client is nil",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			var spClient *spanner.Client
			if tc.setupSpanner != nil {
				spClient = tc.setupSpanner(t)
			}

			var tqClient TaskQueueClient
			if tc.setupTq != nil {
				tqClient = tc.setupTq()
			}

			var xidClient xidgrpcpb.XidServiceClient = &fakeXidClient{}
			handler := activeHealthCheck(spClient, xidClient, tqClient)

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
