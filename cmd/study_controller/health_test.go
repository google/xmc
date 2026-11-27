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
		name          string
		method        string
		path          string
		pingErr       error
		vizierErr     error
		nilRedis      bool
		unresolvedViz bool
		typedNil      bool
		unconfigured  bool
		wantStatus    int
		wantContains  string
	}{
		{name: "liveness", method: http.MethodGet, path: "/healthz", wantStatus: http.StatusOK, wantContains: "OK"},
		{name: "liveness ignores Redis and Vizier outage", method: http.MethodGet, path: "/healthz", pingErr: unreachable, vizierErr: unreachable, wantStatus: http.StatusOK, wantContains: "OK"},
		{name: "readiness with Redis and Vizier reachable", method: http.MethodGet, path: "/readyz", wantStatus: http.StatusOK, wantContains: "OK"},
		{name: "readiness with nil redis", method: http.MethodGet, path: "/readyz", nilRedis: true, wantStatus: http.StatusServiceUnavailable, wantContains: "Service Unavailable"},
		{name: "readiness with Redis unreachable", method: http.MethodGet, path: "/readyz", pingErr: unreachable, wantStatus: http.StatusServiceUnavailable, wantContains: "Service Unavailable"},
		{name: "readiness with Redis ping timeout", method: http.MethodGet, path: "/readyz", pingErr: context.DeadlineExceeded, wantStatus: http.StatusServiceUnavailable, wantContains: "Service Unavailable"},
		{name: "readiness with Vizier unresolved", method: http.MethodGet, path: "/readyz", unresolvedViz: true, wantStatus: http.StatusServiceUnavailable, wantContains: "Service Unavailable"},
		{name: "readiness with typed-nil vizierProbe", method: http.MethodGet, path: "/readyz", typedNil: true, wantStatus: http.StatusServiceUnavailable, wantContains: "Service Unavailable"},
		{name: "readiness with nil vizier interface", method: http.MethodGet, path: "/readyz", unconfigured: true, wantStatus: http.StatusServiceUnavailable, wantContains: "Service Unavailable"},
		{name: "readiness with Vizier unreachable", method: http.MethodGet, path: "/readyz", vizierErr: unreachable, wantStatus: http.StatusServiceUnavailable, wantContains: "Service Unavailable"},
		{name: "readiness with Vizier ping timeout", method: http.MethodGet, path: "/readyz", vizierErr: context.DeadlineExceeded, wantStatus: http.StatusServiceUnavailable, wantContains: "Service Unavailable"},
		{name: "metrics exposes runtime stats", method: http.MethodGet, path: "/metrics", wantStatus: http.StatusOK, wantContains: "go_goroutines"},
		{name: "metrics exposes reconcile ticks counter", method: http.MethodGet, path: "/metrics", wantStatus: http.StatusOK, wantContains: "xmc_study_controller_reconcile_ticks_total"},
		{name: "unknown path", method: http.MethodGet, path: "/unknown", wantStatus: http.StatusNotFound},
		{name: "non-GET liveness", method: http.MethodPost, path: "/healthz", wantStatus: http.StatusMethodNotAllowed},
		{name: "non-GET readiness", method: http.MethodPost, path: "/readyz", wantStatus: http.StatusMethodNotAllowed},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			vp := &vizierProbe{}
			vp.Set(&fakePinger{err: tc.vizierErr})
			vp.Set(nil)
			(*vizierProbe)(nil).Set(&fakePinger{})
			var redis pinger = &fakePinger{err: tc.pingErr}
			if tc.nilRedis {
				redis = nil
			}
			var vizier pinger = vp
			switch {
			case tc.unresolvedViz:
				vizier = &vizierProbe{}
			case tc.typedNil:
				vizier = (*vizierProbe)(nil)
			case tc.unconfigured:
				vizier = nil
			}
			handler := newHTTPHandler(redis, vizier)
			rec := httptest.NewRecorder()
			handler.ServeHTTP(rec, httptest.NewRequest(tc.method, tc.path, nil))
			if rec.Code != tc.wantStatus {
				t.Errorf("%s %s status = %d, want %d", tc.method, tc.path, rec.Code, tc.wantStatus)
			}
			if !strings.Contains(rec.Body.String(), tc.wantContains) {
				t.Errorf("%s %s body = %q, want it to contain %q", tc.method, tc.path, rec.Body.String(), tc.wantContains)
			}
			for _, depErr := range []error{tc.pingErr, tc.vizierErr} {
				if depErr != nil && strings.Contains(rec.Body.String(), depErr.Error()) {
					t.Errorf("%s %s body = %q, want it not to expose error %q", tc.method, tc.path, rec.Body.String(), depErr)
				}
			}
		})
	}
}

func TestReadinessCheck_BoundsPingWithTimeout(t *testing.T) {
	t.Parallel()
	redis := &fakePinger{}
	vizier := &fakePinger{}
	start := time.Now()

	readinessCheck(redis, vizier).ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/readyz", nil))
	end := time.Now()

	for name, p := range map[string]*fakePinger{"redis": redis, "vizier": vizier} {
		if !p.hasDeadline {
			t.Fatalf("%s Ping context has no deadline, want one bounded by readinessPingTimeout", name)
		}
		earliest, latest := start.Add(readinessPingTimeout), end.Add(readinessPingTimeout)
		if p.deadline.Before(earliest) || p.deadline.After(latest) {
			t.Errorf("%s Ping context deadline = %v, want within [%v, %v] (readinessPingTimeout after the request)", name, p.deadline, earliest, latest)
		}
	}
}
