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
	"net/http"

	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// newHTTPHandler returns the mux serving the liveness, readiness and Prometheus metrics endpoints.
//
// Readiness only reports that the process is up: the Study Controller has no dependencies to
// check yet.
func newHTTPHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", writeOK)
	mux.HandleFunc("GET /readyz", writeOK)
	mux.Handle("GET /metrics", promhttp.Handler())
	return mux
}

// writeOK responds 200 OK with body "OK"; it backs the liveness and readiness probes.
func writeOK(w http.ResponseWriter, _ *http.Request) {
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("OK"))
}
