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
	"strings"
	"testing"
	"time"
)

// fakePinger returns err from Ping and records the deadline of the context it was called with.
type fakePinger struct {
	err         error
	deadline    time.Time
	hasDeadline bool
}

func (p *fakePinger) Ping(ctx context.Context) error {
	p.deadline, p.hasDeadline = ctx.Deadline()
	return p.err
}

func TestNewHTTPHandler(t *testing.T) {
	t.Parallel()

	unreachable := errors.New("dial tcp 10.0.0.3:6379: connect: connection refused")
	tests := []struct {
		name         string
		method       string
		path         string
		pingErr      error
		wantStatus   int
		wantContains string
	}{
		{name: "liveness", method: http.MethodGet, path: "/healthz", wantStatus: http.StatusOK, wantContains: "OK"},
		{name: "liveness ignores Redis outage", method: http.MethodGet, path: "/healthz", pingErr: unreachable, wantStatus: http.StatusOK, wantContains: "OK"},
		{name: "readiness with Redis reachable", method: http.MethodGet, path: "/readyz", wantStatus: http.StatusOK, wantContains: "OK"},
		{name: "readiness with Redis unreachable", method: http.MethodGet, path: "/readyz", pingErr: unreachable, wantStatus: http.StatusServiceUnavailable, wantContains: "Service Unavailable"},
		{name: "readiness with Redis ping timeout", method: http.MethodGet, path: "/readyz", pingErr: context.DeadlineExceeded, wantStatus: http.StatusServiceUnavailable, wantContains: "Service Unavailable"},
		{name: "metrics", method: http.MethodGet, path: "/metrics", wantStatus: http.StatusOK, wantContains: "go_goroutines"},
		{name: "unknown path", method: http.MethodGet, path: "/unknown", wantStatus: http.StatusNotFound},
		{name: "non-GET liveness", method: http.MethodPost, path: "/healthz", wantStatus: http.StatusMethodNotAllowed},
		{name: "non-GET readiness", method: http.MethodPost, path: "/readyz", wantStatus: http.StatusMethodNotAllowed},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			handler := newHTTPHandler(&fakePinger{err: tc.pingErr})
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, httptest.NewRequest(tc.method, tc.path, nil))
			if rec.Code != tc.wantStatus {
				t.Errorf("%s %s status = %d, want %d", tc.method, tc.path, rec.Code, tc.wantStatus)
			}
			if !strings.Contains(rec.Body.String(), tc.wantContains) {
				t.Errorf("%s %s body = %q, want it to contain %q", tc.method, tc.path, rec.Body.String(), tc.wantContains)
			}
			if tc.pingErr != nil && strings.Contains(rec.Body.String(), tc.pingErr.Error()) {
				t.Errorf("%s %s body = %q, want it not to expose the ping error", tc.method, tc.path, rec.Body.String())
			}
		})
	}
}

func TestReadinessCheck_BoundsPingWithTimeout(t *testing.T) {
	t.Parallel()
	redis := &fakePinger{}
	start := time.Now()

	readinessCheck(redis).ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/readyz", nil))
	end := time.Now()

	if !redis.hasDeadline {
		t.Fatal("Ping context has no deadline, want one bounded by readinessPingTimeout")
	}
	earliest, latest := start.Add(readinessPingTimeout), end.Add(readinessPingTimeout)
	if redis.deadline.Before(earliest) || redis.deadline.After(latest) {
		t.Errorf("Ping context deadline = %v, want within [%v, %v] (readinessPingTimeout after the request)", redis.deadline, earliest, latest)
	}
}
