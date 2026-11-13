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
	"context"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"xmanager_cloud/cmd/study_controller/config"
	"xmanager_cloud/cmd/study_controller/worker"
	"xmanager_cloud/internal/logging"
)

// waitHealthy polls GET /healthz at addr until it returns 200 OK or the deadline passes.
func waitHealthy(t *testing.T, addr string) {
	t.Helper()
	client := &http.Client{Timeout: time.Second}
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := client.Get("http://" + addr + "/healthz")
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("GET /healthz at %s did not return 200 OK before the deadline", addr)
}

// freePort returns a TCP port that was free when the function returned.
func freePort(t *testing.T) string {
	t.Helper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.Listen() error = %v", err)
	}
	defer lis.Close()
	return strconv.Itoa(lis.Addr().(*net.TCPAddr).Port)
}

func TestRun_RejectsInvalidInput(t *testing.T) {
	busy, err := net.Listen("tcp", ":0")
	if err != nil {
		t.Fatalf("net.Listen() error = %v", err)
	}
	defer busy.Close()
	busyPort := strconv.Itoa(busy.Addr().(*net.TCPAddr).Port)
	const redisAddr = "-redis_addr=redis-service:6379"

	tests := []struct {
		name string
		args []string
		want int
	}{
		{name: "unknown flag", args: []string{"-unknown"}, want: 2},
		{name: "invalid port", args: []string{"-port=http", redisAddr}, want: 1},
		// Validation fails before Listen, so the default port is never bound.
		{name: "missing redis address", args: []string{"-port=" + config.DefaultPort}, want: 1},
		{name: "malformed redis address", args: []string{"-port=" + config.DefaultPort, "-redis_addr=redis-service"}, want: 1},
		{name: "port already in use", args: []string{"-port=" + busyPort, redisAddr}, want: 1},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := run(context.Background(), tc.args); got != tc.want {
				t.Errorf("run(%v) = %d, want %d", tc.args, got, tc.want)
			}
		})
	}
}

func TestRun_CancelledContextExitsZero(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	args := []string{"-port=" + freePort(t), "-redis_addr=127.0.0.1:" + freePort(t)}

	if got := run(ctx, args); got != 0 {
		t.Errorf("run(%v) with a cancelled context = %d, want 0", args, got)
	}
}

func TestReadiness_UnreachableRedis(t *testing.T) {
	client := worker.NewRedisConsumer("127.0.0.1:" + freePort(t))
	defer client.Close(context.Background())
	rec := httptest.NewRecorder()

	newHTTPHandler(client).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/readyz", nil))

	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("GET /readyz with Redis on a closed port status = %d, want %d", rec.Code, http.StatusServiceUnavailable)
	}
}

// startSilentServer returns the address of a TCP server that accepts connections and never
// replies, like a hung Redis server.
func startSilentServer(t *testing.T) string {
	t.Helper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.Listen() error = %v", err)
	}
	var mu sync.Mutex
	var conns []net.Conn
	t.Cleanup(func() {
		lis.Close()
		mu.Lock()
		defer mu.Unlock()
		for _, c := range conns {
			c.Close()
		}
	})
	go func() {
		for {
			conn, err := lis.Accept()
			if err != nil {
				return
			}
			mu.Lock()
			conns = append(conns, conn)
			mu.Unlock()
		}
	}()
	return lis.Addr().String()
}

// TestReadiness_HungRedisFailsBeforeProbeTimeout uses the production Redis consumer client,
// including its default retries, to check that /readyz answers 503 within readinessPingTimeout,
// before the 3s readiness probe timeout, when Redis accepts connections but never replies.
func TestReadiness_HungRedisFailsBeforeProbeTimeout(t *testing.T) {
	client := worker.NewRedisConsumer(startSilentServer(t))
	defer client.Close(context.Background())
	handler := newHTTPHandler(client)
	rec := httptest.NewRecorder()

	start := time.Now()
	handler.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/readyz", nil))
	elapsed := time.Since(start)

	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("GET /readyz with a hung Redis status = %d, want %d", rec.Code, http.StatusServiceUnavailable)
	}
	if limit := readinessPingTimeout + 500*time.Millisecond; elapsed > limit {
		t.Errorf("GET /readyz with a hung Redis took %v, want at most %v", elapsed, limit)
	}
}

func TestStopConsumer_LogsCloseFailure(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	client := worker.NewRedisConsumer("127.0.0.1:" + freePort(t))
	consumer := worker.Start(ctx, client)
	// Closing the client first makes the consumer's own close fail.
	if err := client.Close(context.Background()); err != nil {
		t.Fatalf("client.Close() error = %v", err)
	}
	var logs bytes.Buffer
	logCtx := logging.WithLogger(context.Background(), slog.New(slog.NewTextHandler(&logs, nil)))

	stopConsumer(logCtx, consumer)

	if !strings.Contains(logs.String(), "Failed to close the study reconcile consumer") {
		t.Errorf("stopConsumer() with a closed client logs = %q, want a close failure warning", logs.String())
	}
}

func TestServe_ServesUntilContextDone(t *testing.T) {
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.Listen() error = %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- serve(ctx, lis, newHTTPHandler(&fakePinger{})) }()

	waitHealthy(t, lis.Addr().String())
	cancel()

	select {
	case err := <-done:
		if err != nil {
			t.Errorf("serve() after graceful shutdown error = %v, want nil", err)
		}
	case <-time.After(shutdownTimeout + 5*time.Second):
		t.Fatal("serve() did not return after its context was cancelled")
	}
}

func TestServe_ReturnsListenerError(t *testing.T) {
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.Listen() error = %v", err)
	}
	lis.Close()

	if err := serve(context.Background(), lis, newHTTPHandler(&fakePinger{})); err == nil {
		t.Error("serve() on a closed listener error = nil, want non-nil")
	}
}
