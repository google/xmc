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
	"testing"

	"k8s.io/client-go/kubernetes"
	kubernetesfake "k8s.io/client-go/kubernetes/fake"
	"xmanager_cloud/common/health"
	"xmanager_cloud/internal/storage/spanner/migrations/version"
	"xmanager_cloud/task_queue_redis/redisclient"
)

type mockK8sHealthClient struct {
	clientset kubernetes.Interface
}

func (m *mockK8sHealthClient) Clientset() kubernetes.Interface {
	return m.clientset
}

type mockSpannerHealthClient struct {
	version int64
	dirty   bool
	err     error
}

func (m *mockSpannerHealthClient) QuerySchemaVersion(ctx context.Context) (int64, bool, error) {
	if ctx.Err() != nil {
		return 0, false, ctx.Err()
	}
	return m.version, m.dirty, m.err
}

type mockRedisConsumer struct {
	redisclient.Consumer
	pingErr error
}

func (m *mockRedisConsumer) Ping(ctx context.Context) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	return m.pingErr
}

func (m *mockRedisConsumer) Close(ctx context.Context) error {
	return nil
}

func TestActiveHealthCheck(t *testing.T) {
	fakeClientset := kubernetesfake.NewSimpleClientset()
	validK8s := &mockK8sHealthClient{clientset: fakeClientset}
	validRedis := &mockRedisConsumer{pingErr: nil}
	validSpanner := &mockSpannerHealthClient{
		version: version.EssExpectedSchemaVersion(),
		dirty:   false,
		err:     nil,
	}

	testCases := []struct {
		name       string
		k8s        k8sHealthClient
		spClient   health.SpannerHealthClient
		redis      redisclient.Consumer
		wantStatus int
	}{
		{
			name:       "all_healthy_without_spanner",
			k8s:        validK8s,
			spClient:   nil,
			redis:      validRedis,
			wantStatus: http.StatusOK,
		},
		{
			name:       "all_healthy_with_spanner",
			k8s:        validK8s,
			spClient:   validSpanner,
			redis:      validRedis,
			wantStatus: http.StatusOK,
		},
		{
			name:       "spanner_query_error",
			k8s:        validK8s,
			spClient:   &mockSpannerHealthClient{err: fmt.Errorf("spanner unreachable")},
			redis:      validRedis,
			wantStatus: http.StatusInternalServerError,
		},
		{
			name:       "spanner_dirty_migration",
			k8s:        validK8s,
			spClient:   &mockSpannerHealthClient{version: version.EssExpectedSchemaVersion(), dirty: true},
			redis:      validRedis,
			wantStatus: http.StatusInternalServerError,
		},
		{
			name:       "nil_k8s_client",
			k8s:        nil,
			spClient:   nil,
			redis:      validRedis,
			wantStatus: http.StatusInternalServerError,
		},
		{
			name:       "k8s_clientset_nil",
			k8s:        &mockK8sHealthClient{clientset: nil},
			spClient:   nil,
			redis:      validRedis,
			wantStatus: http.StatusInternalServerError,
		},
		{
			name:       "nil_redis_client",
			k8s:        validK8s,
			spClient:   nil,
			redis:      nil,
			wantStatus: http.StatusInternalServerError,
		},
		{
			name:       "redis_ping_error",
			k8s:        validK8s,
			spClient:   nil,
			redis:      &mockRedisConsumer{pingErr: fmt.Errorf("connection refused")},
			wantStatus: http.StatusInternalServerError,
		},
		{
			name:       "redis_context_canceled",
			k8s:        validK8s,
			spClient:   nil,
			redis:      &mockRedisConsumer{pingErr: context.Canceled},
			wantStatus: http.StatusInternalServerError,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			handler := activeHealthCheck(tc.k8s, tc.spClient, tc.redis)
			req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
			rec := httptest.NewRecorder()

			handler.ServeHTTP(rec, req)

			if rec.Code != tc.wantStatus {
				t.Errorf("activeHealthCheck status = %d, want %d, body: %s", rec.Code, tc.wantStatus, rec.Body.String())
			}
		})
	}
}

func TestActiveHealthCheck_RequestContextCanceled(t *testing.T) {
	fakeClientset := kubernetesfake.NewSimpleClientset()
	validK8s := &mockK8sHealthClient{clientset: fakeClientset}
	validRedis := &mockRedisConsumer{pingErr: nil}

	handler := activeHealthCheck(validK8s, nil, validRedis)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	req := httptest.NewRequest(http.MethodGet, "/readyz", nil).WithContext(ctx)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("activeHealthCheck with canceled request context status = %d, want %d", rec.Code, http.StatusInternalServerError)
	}
}

func TestLivenessCheck(t *testing.T) {
	handler := livenessCheck()
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("livenessCheck status = %d, want %d", rec.Code, http.StatusOK)
	}
	if rec.Body.String() != "ok" {
		t.Errorf("livenessCheck body = %q, want 'ok'", rec.Body.String())
	}
}
