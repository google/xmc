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
	"bufio"
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
	"google.golang.org/grpc"
	"xmanager_cloud/cmd/study_controller/config"
	"xmanager_cloud/cmd/study_controller/worker"
	"xmanager_cloud/internal/logging"
	"xmanager_cloud/internal/task_queue_redis/redisclient"
	"xmanager_cloud/pkg/components/crreader"
	"xmanager_cloud/pkg/components/crreader/crreadertest"
	"xmanager_cloud/pkg/study/vizier"

	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// waitHTTPStatus polls GET path at addr until it returns want or the deadline passes.
func waitHTTPStatus(t *testing.T, addr, path string, want int) {
	t.Helper()
	client := &http.Client{Timeout: 3 * time.Second}
	deadline := time.Now().Add(10 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := client.Get("http://" + addr + path)
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == want {
				return
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("GET %s at %s did not return %d before deadline", path, addr, want)
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
	const (
		redisAddr   = "-redis_addr=redis-service:6379"
		essAddr     = "-ess_addr=grpc-service:50051"
		essAudience = "-ess_audience=ess-aud.apps.googleusercontent.com"
	)

	tests := []struct {
		name string
		args []string
		want int
	}{
		{name: "unknown flag", args: []string{"-unknown"}, want: 2},
		{name: "invalid port", args: []string{"-port=http", redisAddr, essAddr, essAudience}, want: 1},
		// Validation fails before Listen, so the default port is never bound.
		{name: "missing redis address", args: []string{"-port=" + config.DefaultPort, essAddr, essAudience}, want: 1},
		{name: "malformed redis address", args: []string{"-port=" + config.DefaultPort, "-redis_addr=redis-service", essAddr, essAudience}, want: 1},
		{name: "missing ess address", args: []string{"-port=" + config.DefaultPort, redisAddr, essAudience}, want: 1},
		{name: "malformed ess address", args: []string{"-port=" + config.DefaultPort, redisAddr, "-ess_addr=grpc-service", essAudience}, want: 1},
		{name: "missing ess audience", args: []string{"-port=" + config.DefaultPort, redisAddr, essAddr}, want: 1},
		{name: "port already in use", args: []string{"-port=" + busyPort, redisAddr, essAddr, essAudience}, want: 1},
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
	args := []string{
		"-port=" + freePort(t),
		"-redis_addr=127.0.0.1:" + freePort(t),
		"-ess_addr=127.0.0.1:" + freePort(t),
		"-ess_audience=ess-aud.apps.googleusercontent.com",
	}

	if got := run(ctx, args); got != 0 {
		t.Errorf("run(%v) with a cancelled context = %d, want 0", args, got)
	}
}

type noopRedisClient struct{ redisclient.Client }

func (noopRedisClient) Close() error { return nil }

func TestServeWithClient_RejectsInvalidClients(t *testing.T) {
	nonLockClient := worker.NewRedisConsumer("127.0.0.1:" + freePort(t))
	_ = nonLockClient.Close(context.Background())
	nonLockClient.Client = noopRedisClient{}

	tests := []struct {
		name   string
		client *redisclient.ConsumerClient
	}{
		{name: "nil consumer client", client: nil},
		{name: "non-locking underlying client", client: nonLockClient},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			lis, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatalf("net.Listen() error = %v", err)
			}
			defer lis.Close()
			if err := serveWithClient(context.Background(), lis, tc.client, nil, "xmc", crreader.NewK8sGetter); err == nil {
				t.Errorf("serveWithClient(%+v) error = nil, want non-nil", tc.client)
			}
		})
	}
}

func TestReadiness_UnreachableRedis(t *testing.T) {
	client := worker.NewRedisConsumer("127.0.0.1:" + freePort(t))
	defer client.Close(context.Background())
	rec := httptest.NewRecorder()

	newHTTPHandler(client, &fakePinger{}).ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/readyz", nil))

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
	handler := newHTTPHandler(client, &fakePinger{})
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
	handler, err := worker.NewRedisHandler(client, nil)
	if err != nil {
		t.Fatalf("NewRedisHandler() error = %v", err)
	}
	consumer := worker.Start(ctx, client, handler)
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
	go func() { done <- serve(ctx, lis, newHTTPHandler(&fakePinger{}, &fakePinger{})) }()

	waitHTTPStatus(t, lis.Addr().String(), "/healthz", http.StatusOK)
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

	if err := serve(context.Background(), lis, newHTTPHandler(&fakePinger{}, &fakePinger{})); err == nil {
		t.Error("serve() on a closed listener error = nil, want non-nil")
	}
}

func TestServeWithClient_ServesHealthzWhileWaitingForVizierAndCancelsCleanly(t *testing.T) {
	lis := mustListen(t)
	client := worker.NewRedisConsumer(startPongRedisServer(t, nil))
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	t.Cleanup(func() { cancel(); <-done })
	go func() {
		defer close(done)
		done <- serveWithClient(ctx, lis, client, nil, "xmc", func() (crreader.Getter, error) { return &crreadertest.FakeGetter{}, nil })
	}()
	addr := lis.Addr().String()
	waitHTTPStatus(t, addr, "/healthz", http.StatusOK)
	waitHTTPStatus(t, addr, "/readyz", http.StatusServiceUnavailable)
	cancel()
	if err := <-done; err != nil {
		t.Errorf("serveWithClient() = %v, want nil", err)
	}
}

type atomicGetter struct {
	mu    sync.Mutex
	inner crreadertest.FakeGetter
}

func (g *atomicGetter) GetComponent(ctx context.Context, ns, name string) (*unstructured.Unstructured, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.inner.GetComponent(ctx, ns, name)
}

func mustListen(t *testing.T) net.Listener {
	t.Helper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.Listen() = %v", err)
	}
	return lis
}

func startPongRedisServer(t *testing.T, consumerCmds *atomic.Int32) string {
	t.Helper()
	lis := mustListen(t)
	var mu sync.Mutex
	var conns []net.Conn
	var wg sync.WaitGroup
	var closed bool
	t.Cleanup(func() {
		_ = lis.Close()
		mu.Lock()
		closed = true
		for _, c := range conns {
			_ = c.Close()
		}
		mu.Unlock()
		wg.Wait()
	})
	wg.Add(1)
	go func() {
		defer wg.Done()
		for conn, err := lis.Accept(); err == nil; conn, err = lis.Accept() {
			mu.Lock()
			if closed {
				mu.Unlock()
				_ = conn.Close()
				return
			}
			conns = append(conns, conn)
			mu.Unlock()
			wg.Add(1)
			go func(c net.Conn) { defer wg.Done(); serveFakeRedis(c, consumerCmds) }(conn)
		}
	}()
	return lis.Addr().String()
}

func serveFakeRedis(c net.Conn, consumerCmds *atomic.Int32) {
	defer c.Close()
	r := bufio.NewReader(c)
	replies := map[string]string{"HELLO": "-ERR unknown command\r\n", "CLIENT": "+OK\r\n", "XGROUP": "+OK\r\n", "XREADGROUP": "*-1\r\n", "XAUTOCLAIM": "*2\r\n$3\r\n0-0\r\n*0\r\n"}
	for line, err := r.ReadString('\n'); err == nil; line, err = r.ReadString('\n') {
		if !strings.HasPrefix(line, "*") {
			continue
		}
		n, err := strconv.Atoi(strings.TrimSpace(line[1:]))
		if err != nil || n < 1 {
			return
		}
		var cmd string
		for i := 0; i < n; i++ {
			hdr, err := r.ReadString('\n')
			sz, err2 := strconv.Atoi(strings.TrimSpace(strings.TrimPrefix(hdr, "$")))
			if err != nil || !strings.HasPrefix(hdr, "$") || err2 != nil || sz < 0 {
				return
			}
			buf := make([]byte, sz+2)
			if _, err := io.ReadFull(r, buf); err != nil {
				return
			}
			if i == 0 {
				cmd = strings.ToUpper(string(buf[:sz]))
			}
		}
		if consumerCmds != nil && (cmd == "XGROUP" || cmd == "XREADGROUP" || cmd == "XAUTOCLAIM") {
			consumerCmds.Add(1)
		}
		reply, ok := replies[cmd]
		if !ok {
			reply = "+PONG\r\n"
		}
		_, _ = c.Write([]byte(reply))
	}
}

func startStubVizierServer(t *testing.T) string {
	t.Helper()
	lis := mustListen(t)
	srv := grpc.NewServer()
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(func() { srv.Stop(); _ = lis.Close() })
	return lis.Addr().String()
}

func TestServeWithClient_LatchesVizierOnceComponentAppears(t *testing.T) {
	lis := mustListen(t)
	var consumerCmds atomic.Int32
	client := worker.NewRedisConsumer(startPongRedisServer(t, &consumerCmds))
	vizierAddr := startStubVizierServer(t)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	getter := &atomicGetter{}
	var gotVizier atomic.Pointer[vizier.Client]
	done := make(chan error, 1)
	t.Cleanup(func() { cancel(); <-done })
	go func() {
		defer close(done)
		done <- serveWithClient(ctx, lis, client, func(vc *vizier.Client) (worker.ReconcileFunc, error) {
			gotVizier.Store(vc)
			return nil, nil
		}, "xmc-custom", func() (crreader.Getter, error) { return getter, nil })
	}()

	addr := lis.Addr().String()
	waitHTTPStatus(t, addr, "/healthz", http.StatusOK)
	waitHTTPStatus(t, addr, "/readyz", http.StatusServiceUnavailable)
	if n := consumerCmds.Load(); n != 0 {
		t.Fatalf("consumer commands before Vizier latch = %d, want 0", n)
	}

	getter.mu.Lock()
	getter.inner.Components = map[string]*unstructured.Unstructured{"xmc-custom/vizier": {Object: map[string]any{
		"apiVersion": "xmc.dev/v1alpha1", "kind": "Component", "metadata": map[string]any{"name": "vizier", "namespace": "xmc-custom"},
		"spec": map[string]any{"enabled": true, "parameters": map[string]any{"endpoint": vizierAddr}},
	}}}
	getter.mu.Unlock()
	waitHTTPStatus(t, addr, "/readyz", http.StatusOK)
	for d := time.Now().Add(5 * time.Second); consumerCmds.Load() == 0 && time.Now().Before(d); {
		time.Sleep(10 * time.Millisecond)
	}
	if consumerCmds.Load() == 0 {
		t.Error("expected Redis consumer commands after Vizier latch, got 0")
	}
	cancel()
	if err := <-done; err != nil {
		t.Errorf("serveWithClient() after Vizier latch = %v, want nil", err)
	}
	if gotVizier.Load() == nil {
		t.Error("newReconciler did not receive latched *vizier.Client")
	}
}

func TestServeWithClient_ReturnsReconcilerError(t *testing.T) {
	lis := mustListen(t)
	client := worker.NewRedisConsumer("127.0.0.1:" + freePort(t))
	vizierAddr := startStubVizierServer(t)
	getter := &crreadertest.FakeGetter{Components: map[string]*unstructured.Unstructured{
		"xmc/vizier": {Object: map[string]any{
			"apiVersion": "xmc.dev/v1alpha1", "kind": "Component",
			"metadata": map[string]any{"name": "vizier", "namespace": "xmc"},
			"spec":     map[string]any{"enabled": true, "parameters": map[string]any{"endpoint": vizierAddr}},
		}},
	}}
	wantErr := errors.New("reconciler init failed")
	err := serveWithClient(context.Background(), lis, client, func(*vizier.Client) (worker.ReconcileFunc, error) {
		return nil, wantErr
	}, "xmc", func() (crreader.Getter, error) { return getter, nil })
	if !errors.Is(err, wantErr) {
		t.Fatalf("serveWithClient() error = %v, want %v", err, wantErr)
	}
	if pingErr := client.Ping(context.Background()); !errors.Is(pingErr, redis.ErrClosed) {
		t.Errorf("client.Ping() after stopServing = %v, want redis.ErrClosed", pingErr)
	}
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, "http://"+lis.Addr().String()+"/healthz", nil)
	if resp, getErr := (&http.Client{Timeout: time.Second}).Do(req); getErr == nil {
		resp.Body.Close()
		t.Errorf("GET /healthz after stopServing = %d, want error", resp.StatusCode)
	}
}

func TestServeWithClient_AbortsResolveOnHTTPFailure(t *testing.T) {
	lis := mustListen(t)
	lis.Close()
	if err := serveWithClient(context.Background(), lis, worker.NewRedisConsumer("127.0.0.1:"+freePort(t)), nil, "xmc", func() (crreader.Getter, error) { return &crreadertest.FakeGetter{}, nil }); err == nil {
		t.Fatal("serveWithClient() with closed listener = nil, want non-nil")
	}
}

func TestServeWithClient_ReturnsGetterError(t *testing.T) {
	for _, fn := range []func() (crreader.Getter, error){nil, func() (crreader.Getter, error) { return nil, errors.New("k8s config unavailable") }} {
		if err := serveWithClient(context.Background(), mustListen(t), worker.NewRedisConsumer("127.0.0.1:"+freePort(t)), nil, "xmc", fn); err == nil {
			t.Fatal("serveWithClient() = nil, want non-nil")
		}
	}
}
