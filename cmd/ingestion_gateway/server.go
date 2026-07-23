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
	"net"
	"net/http"
	"time"
)

const (
	// Default timeouts and payload constraints.
	DefaultReadTimeout       = 5 * time.Second
	DefaultWriteTimeout      = 10 * time.Second
	DefaultIdleTimeout       = 30 * time.Second
	DefaultReadHeaderTimeout = 5 * time.Second
	DefaultMaxHeaderBytes    = 1 * 1024 * 1024  // 1MB
	DefaultMaxBodyBytes      = 10 * 1024 * 1024 // 10MB
)

// ServerConfig holds HTTP server timeout and payload limit configurations.
type ServerConfig struct {
	Addr              string
	ReadTimeout       time.Duration
	WriteTimeout      time.Duration
	IdleTimeout       time.Duration
	ReadHeaderTimeout time.Duration
	MaxHeaderBytes    int
	MaxBodyBytes      int64
}

// DefaultServerConfig creates baseline server settings for a given port.
func DefaultServerConfig(port string) ServerConfig {
	return ServerConfig{
		Addr:              net.JoinHostPort("", port),
		ReadTimeout:       DefaultReadTimeout,
		WriteTimeout:      DefaultWriteTimeout,
		IdleTimeout:       DefaultIdleTimeout,
		ReadHeaderTimeout: DefaultReadHeaderTimeout,
		MaxHeaderBytes:    DefaultMaxHeaderBytes,
		MaxBodyBytes:      DefaultMaxBodyBytes,
	}
}

// MaxBytesMiddleware wraps an http.Handler to limit the request body using http.MaxBytesReader.
func MaxBytesMiddleware(maxBytes int64, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Body != nil {
			r.Body = http.MaxBytesReader(w, r.Body, maxBytes)
		}
		next.ServeHTTP(w, r)
	})
}

// healthzHandler handles health check endpoints, accepting GET and HEAD requests.
func healthzHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		http.Error(w, "Method Not Allowed", http.StatusMethodNotAllowed)
		return
	}
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("OK"))
}

// NewHTTPServer constructs an http.Server with tuned timeouts and body limits.
func NewHTTPServer(cfg ServerConfig) *http.Server {
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", healthzHandler)
	mux.HandleFunc("/readyz", healthzHandler)
	mux.HandleFunc("/api/v1/metrics", MetricsIngestionHandler)

	handler := MaxBytesMiddleware(cfg.MaxBodyBytes, mux)

	return &http.Server{
		Addr:              cfg.Addr,
		Handler:           handler,
		ReadTimeout:       cfg.ReadTimeout,
		WriteTimeout:      cfg.WriteTimeout,
		IdleTimeout:       cfg.IdleTimeout,
		ReadHeaderTimeout: cfg.ReadHeaderTimeout,
		MaxHeaderBytes:    cfg.MaxHeaderBytes,
	}
}
