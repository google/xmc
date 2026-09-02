// Copyright 2025 Google LLC
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
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"golang/prometheus/client/prometheus/prometheus"
)

// newTestRegistry returns a registry exposing a single counter with the given
// name so tests can assert which registries a handler serves.
func newTestRegistry(t *testing.T, metricName string) *prometheus.Registry {
	t.Helper()
	reg := prometheus.NewRegistry()
	counter := prometheus.NewCounter(prometheus.CounterOpts{Name: metricName, Help: "test metric"})
	reg.MustRegister(counter)
	counter.Inc()
	return reg
}

func TestNewMetricsHandler(t *testing.T) {
	regA := newTestRegistry(t, "test_metric_a")
	regB := newTestRegistry(t, "test_metric_b")

	tests := []struct {
		name       string
		gatherers  []prometheus.Gatherer
		wantFound  []string
		wantAbsent []string
	}{
		{
			name:       "no gatherers exposes nothing",
			gatherers:  nil,
			wantAbsent: []string{"test_metric_a", "test_metric_b"},
		},
		{
			name:       "single gatherer exposes only its metrics",
			gatherers:  []prometheus.Gatherer{regA},
			wantFound:  []string{"test_metric_a"},
			wantAbsent: []string{"test_metric_b"},
		},
		{
			name:      "merged gatherers expose both registries",
			gatherers: []prometheus.Gatherer{regA, regB},
			wantFound: []string{"test_metric_a", "test_metric_b"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			handler := newMetricsHandler(tc.gatherers...)
			req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
			rec := httptest.NewRecorder()

			handler.ServeHTTP(rec, req)

			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want %d", rec.Code, http.StatusOK)
			}
			body := rec.Body.String()
			for _, want := range tc.wantFound {
				if !strings.Contains(body, want) {
					t.Errorf("response body missing expected metric %q", want)
				}
			}
			for _, absent := range tc.wantAbsent {
				if strings.Contains(body, absent) {
					t.Errorf("response body unexpectedly contains metric %q", absent)
				}
			}
		})
	}
}
