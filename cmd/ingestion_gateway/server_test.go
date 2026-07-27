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
	"bytes"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestNewHTTPServerConfiguration(t *testing.T) {
	cfg := DefaultServerConfig("8080")
	server := NewHTTPServer(cfg)

	if server.ReadTimeout != 5*time.Second {
		t.Errorf("ReadTimeout = %v, want %v", server.ReadTimeout, 5*time.Second)
	}
	if server.WriteTimeout != 10*time.Second {
		t.Errorf("WriteTimeout = %v, want %v", server.WriteTimeout, 10*time.Second)
	}
	if server.IdleTimeout != 30*time.Second {
		t.Errorf("IdleTimeout = %v, want %v", server.IdleTimeout, 30*time.Second)
	}
	if server.ReadHeaderTimeout != 5*time.Second {
		t.Errorf("ReadHeaderTimeout = %v, want %v", server.ReadHeaderTimeout, 5*time.Second)
	}
	if server.MaxHeaderBytes != 1*1024*1024 {
		t.Errorf("MaxHeaderBytes = %v, want %v", server.MaxHeaderBytes, 1*1024*1024)
	}
}

func TestHealthAndReadinessEndpoints(t *testing.T) {
	cfg := DefaultServerConfig("0")
	server := NewHTTPServer(cfg)

	endpoints := []string{"/healthz", "/readyz"}
	for _, ep := range endpoints {
		t.Run(ep, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, ep, nil)
			w := httptest.NewRecorder()

			server.Handler.ServeHTTP(w, req)

			if w.Code != http.StatusOK {
				t.Errorf("%s returned status %d, want %d", ep, w.Code, http.StatusOK)
			}
			if body := w.Body.String(); body != "OK" {
				t.Errorf("%s returned body %q, want %q", ep, body, "OK")
			}
		})
	}

	t.Run("Disallow non-GET/HEAD methods on health check", func(t *testing.T) {
		req := httptest.NewRequest(http.MethodPost, "/healthz", nil)
		w := httptest.NewRecorder()

		server.Handler.ServeHTTP(w, req)

		if w.Code != http.StatusMethodNotAllowed {
			t.Errorf("POST /healthz status = %d, want %d", w.Code, http.StatusMethodNotAllowed)
		}
	})
}

func TestPayloadSizeLimitRejection(t *testing.T) {
	testHandler := MaxBytesMiddleware(10*1024*1024, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Body != nil {
			buf := make([]byte, 4096)
			for {
				_, err := r.Body.Read(buf)
				if err != nil {
					if err.Error() == "http: request body too large" {
						http.Error(w, "request body too large", http.StatusRequestEntityTooLarge)
						return
					}
					break
				}
			}
		}
		w.WriteHeader(http.StatusOK)
	}))

	tests := []struct {
		name        string
		payloadSize int
		wantStatus  int
	}{
		{
			name:        "Payload under 10MB limit accepted",
			payloadSize: 1 * 1024 * 1024, // 1MB
			wantStatus:  http.StatusOK,
		},
		{
			name:        "Payload exactly at 10MB limit accepted",
			payloadSize: 10 * 1024 * 1024, // 10MB
			wantStatus:  http.StatusOK,
		},
		{
			name:        "Payload over 10MB limit rejected",
			payloadSize: 10*1024*1024 + 1024, // 10MB + 1KB
			wantStatus:  http.StatusRequestEntityTooLarge,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			body := bytes.NewBuffer(make([]byte, tt.payloadSize))
			req := httptest.NewRequest(http.MethodPost, "/", body)
			w := httptest.NewRecorder()

			testHandler.ServeHTTP(w, req)

			if w.Code != tt.wantStatus {
				t.Errorf("ServeHTTP() status = %d, want %d", w.Code, tt.wantStatus)
			}
		})
	}
}

func TestHTTPServerLiveSocket(t *testing.T) {
	cfg := DefaultServerConfig("0")
	server := NewHTTPServer(cfg)
	ts := httptest.NewServer(server.Handler)
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/healthz")
	if err != nil {
		t.Fatalf("http.Get error: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("GET /healthz status = %d, want %d", resp.StatusCode, http.StatusOK)
	}

	testHandler := MaxBytesMiddleware(10*1024*1024, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Body != nil {
			buf := make([]byte, 4096)
			for {
				_, err := r.Body.Read(buf)
				if err != nil {
					if err.Error() == "http: request body too large" {
						http.Error(w, "request body too large", http.StatusRequestEntityTooLarge)
						return
					}
					break
				}
			}
		}
		w.WriteHeader(http.StatusOK)
	}))
	tsLimit := httptest.NewServer(testHandler)
	defer tsLimit.Close()

	smallBody := bytes.NewBuffer(make([]byte, 1*1024*1024))
	respSmall, err := http.Post(tsLimit.URL, "application/octet-stream", smallBody)
	if err != nil {
		t.Fatalf("http.Post small body error: %v", err)
	}
	defer respSmall.Body.Close()
	if respSmall.StatusCode != http.StatusOK {
		t.Errorf("POST small body status = %d, want %d", respSmall.StatusCode, http.StatusOK)
	}

	largeBody := bytes.NewBuffer(make([]byte, 10*1024*1024+1024))
	respLarge, err := http.Post(tsLimit.URL, "application/octet-stream", largeBody)
	if err != nil {
		t.Fatalf("http.Post large body error: %v", err)
	}
	defer respLarge.Body.Close()
	if respLarge.StatusCode != http.StatusRequestEntityTooLarge {
		t.Errorf("POST large body status = %d, want %d", respLarge.StatusCode, http.StatusRequestEntityTooLarge)
	}
}

func TestHealthzHandler_DisallowedMethods(t *testing.T) {
	disallowedMethods := []string{http.MethodPost, http.MethodPut, http.MethodDelete, http.MethodPatch}
	endpoints := []string{"/healthz", "/readyz"}

	cfg := DefaultServerConfig("0")
	server := NewHTTPServer(cfg)

	for _, ep := range endpoints {
		for _, method := range disallowedMethods {
			t.Run(method+" "+ep, func(t *testing.T) {
				req := httptest.NewRequest(method, ep, nil)
				w := httptest.NewRecorder()
				server.Handler.ServeHTTP(w, req)
				if w.Code != http.StatusMethodNotAllowed {
					t.Errorf("%s %s status = %d, want %d", method, ep, w.Code, http.StatusMethodNotAllowed)
				}
			})
		}
	}
}

func TestDefaultServerConfig_Defaults(t *testing.T) {
	cfg := DefaultServerConfig("9090")
	if cfg.Addr != ":9090" {
		t.Errorf("cfg.Addr = %q, want :9090", cfg.Addr)
	}
	if cfg.ReadTimeout != DefaultReadTimeout {
		t.Errorf("cfg.ReadTimeout = %v, want %v", cfg.ReadTimeout, DefaultReadTimeout)
	}
	if cfg.WriteTimeout != DefaultWriteTimeout {
		t.Errorf("cfg.WriteTimeout = %v, want %v", cfg.WriteTimeout, DefaultWriteTimeout)
	}
	if cfg.IdleTimeout != DefaultIdleTimeout {
		t.Errorf("cfg.IdleTimeout = %v, want %v", cfg.IdleTimeout, DefaultIdleTimeout)
	}
	if cfg.ReadHeaderTimeout != DefaultReadHeaderTimeout {
		t.Errorf("cfg.ReadHeaderTimeout = %v, want %v", cfg.ReadHeaderTimeout, DefaultReadHeaderTimeout)
	}
	if cfg.MaxHeaderBytes != DefaultMaxHeaderBytes {
		t.Errorf("cfg.MaxHeaderBytes = %v, want %v", cfg.MaxHeaderBytes, DefaultMaxHeaderBytes)
	}
	if cfg.MaxBodyBytes != DefaultMaxBodyBytes {
		t.Errorf("cfg.MaxBodyBytes = %v, want %v", cfg.MaxBodyBytes, DefaultMaxBodyBytes)
	}
}

func TestNewHTTPServer_CustomConfig(t *testing.T) {
	mp := NewMockProducer()
	cfg := ServerConfig{
		Addr:              "127.0.0.1:8080",
		ReadTimeout:       1 * time.Second,
		WriteTimeout:      2 * time.Second,
		IdleTimeout:       3 * time.Second,
		ReadHeaderTimeout: 4 * time.Second,
		MaxHeaderBytes:    512 * 1024,
		MaxBodyBytes:      5 * 1024 * 1024,
		Producer:          mp,
		TopicName:         "custom-topic",
	}

	server := NewHTTPServer(cfg)
	if server.Addr != "127.0.0.1:8080" {
		t.Errorf("server.Addr = %q, want %q", server.Addr, "127.0.0.1:8080")
	}
	if server.ReadTimeout != 1*time.Second {
		t.Errorf("server.ReadTimeout = %v, want %v", server.ReadTimeout, 1*time.Second)
	}
	if server.WriteTimeout != 2*time.Second {
		t.Errorf("server.WriteTimeout = %v, want %v", server.WriteTimeout, 2*time.Second)
	}
	if server.IdleTimeout != 3*time.Second {
		t.Errorf("server.IdleTimeout = %v, want %v", server.IdleTimeout, 3*time.Second)
	}
	if server.ReadHeaderTimeout != 4*time.Second {
		t.Errorf("server.ReadHeaderTimeout = %v, want %v", server.ReadHeaderTimeout, 4*time.Second)
	}
	if server.MaxHeaderBytes != 512*1024 {
		t.Errorf("server.MaxHeaderBytes = %v, want %v", server.MaxHeaderBytes, 512*1024)
	}
}
