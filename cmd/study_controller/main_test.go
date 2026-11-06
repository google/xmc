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
	"net"
	"net/http"
	"strconv"
	"testing"
	"time"
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

func TestRun_RejectsInvalidInput(t *testing.T) {
	busy, err := net.Listen("tcp", ":0")
	if err != nil {
		t.Fatalf("net.Listen() error = %v", err)
	}
	defer busy.Close()
	busyPort := strconv.Itoa(busy.Addr().(*net.TCPAddr).Port)

	tests := []struct {
		name string
		args []string
		want int
	}{
		{name: "unknown flag", args: []string{"-unknown"}, want: 2},
		{name: "invalid port", args: []string{"-port=http"}, want: 1},
		{name: "port already in use", args: []string{"-port=" + busyPort}, want: 1},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := run(context.Background(), tc.args); got != tc.want {
				t.Errorf("run(%v) = %d, want %d", tc.args, got, tc.want)
			}
		})
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
	go func() { done <- serve(ctx, lis, newHTTPHandler()) }()

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

	if err := serve(context.Background(), lis, newHTTPHandler()); err == nil {
		t.Error("serve() on a closed listener error = nil, want non-nil")
	}
}
