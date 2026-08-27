// Copyright 2026 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package bridge

import (
	"context"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestNetworkHealthProber_ProbeHTTP_And_TCP(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/health" {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("OK"))
			return
		}
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer ts.Close()

	prober := NewNetworkHealthProber(
		WithHealthHTTPClient(ts.Client()),
		WithHealthDialer(&net.Dialer{}),
		WithHealthProbeTimeout(2*time.Second),
	)
	ctx := context.Background()

	t.Run("HTTP healthy and unhealthy", func(t *testing.T) {
		res, err := prober.ProbeHTTP(ctx, ts.URL+"/health", 2*time.Second)
		if err != nil || !res.Healthy || res.StatusCode != http.StatusOK {
			t.Fatalf("expected healthy HTTP probe: res=%+v, err=%v", res, err)
		}
		resErr, err := prober.ProbeHTTP(ctx, ts.URL+"/error", 2*time.Second)
		if err != nil || resErr.Healthy || resErr.StatusCode != http.StatusInternalServerError {
			t.Fatalf("expected unhealthy HTTP probe: res=%+v, err=%v", resErr, err)
		}
	})

	t.Run("HTTP error paths", func(t *testing.T) {
		if _, err := prober.ProbeHTTP(ctx, "", 0); err == nil {
			t.Error("expected error on empty URL")
		}
		if _, err := prober.ProbeHTTP(ctx, "://bad", 0); err == nil {
			t.Error("expected error on invalid URL")
		}
		canceledCtx, cancel := context.WithCancel(ctx)
		cancel()
		if _, err := prober.ProbeHTTP(canceledCtx, ts.URL+"/health", time.Second); err == nil {
			t.Error("expected error on canceled context")
		}
		resFail, err := prober.ProbeHTTP(ctx, "http://127.0.0.1:1", time.Second)
		if err != nil || resFail.Healthy {
			t.Errorf("expected failed HTTP probe: res=%+v, err=%v", resFail, err)
		}
	})

	t.Run("TCP healthy and error paths", func(t *testing.T) {
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatalf("failed to listen: %v", err)
		}
		defer ln.Close()

		res, err := prober.ProbeTCP(ctx, ln.Addr().String(), 2*time.Second)
		if err != nil || !res.Healthy {
			t.Fatalf("expected healthy TCP probe: res=%+v, err=%v", res, err)
		}
		resPrefix, err := prober.Probe(ctx, "tcp://"+ln.Addr().String(), 2*time.Second)
		if err != nil || !resPrefix.Healthy {
			t.Fatalf("expected healthy TCP probe with prefix: res=%+v, err=%v", resPrefix, err)
		}
		resUpperTCP, err := prober.Probe(ctx, "TCP://"+ln.Addr().String(), 2*time.Second)
		if err != nil || !resUpperTCP.Healthy {
			t.Fatalf("expected healthy uppercase TCP probe with prefix: res=%+v, err=%v", resUpperTCP, err)
		}
		resProbeTCPUpper, err := prober.ProbeTCP(ctx, "TCP://"+ln.Addr().String(), 2*time.Second)
		if err != nil || !resProbeTCPUpper.Healthy {
			t.Fatalf("expected healthy uppercase ProbeTCP with prefix: res=%+v, err=%v", resProbeTCPUpper, err)
		}
		if _, err := prober.ProbeTCP(ctx, "", 0); err == nil {
			t.Error("expected error on empty address")
		}
		if _, err := prober.ProbeTCP(ctx, "invalid-no-port", 0); err == nil {
			t.Error("expected error on invalid address")
		}
		canceledCtx, cancel := context.WithCancel(ctx)
		cancel()
		if _, err := prober.ProbeTCP(canceledCtx, "127.0.0.1:8080", time.Second); err == nil {
			t.Error("expected error on canceled context")
		}
		resFail, err := prober.ProbeTCP(ctx, "127.0.0.1:1", time.Second)
		if err != nil || resFail.Healthy {
			t.Errorf("expected failed TCP probe: res=%+v, err=%v", resFail, err)
		}
	})

	t.Run("Probe protocol routing", func(t *testing.T) {
		resHTTP, err := prober.Probe(ctx, ts.URL+"/health", time.Second)
		if err != nil || !resHTTP.Healthy {
			t.Errorf("Probe() HTTP routing failed: res=%+v, err=%v", resHTTP, err)
		}
		resUpperHTTP, err := prober.Probe(ctx, "HTTP://"+ts.Listener.Addr().String()+"/health", time.Second)
		if err != nil || !resUpperHTTP.Healthy {
			t.Errorf("Probe() uppercase HTTP routing failed: res=%+v, err=%v", resUpperHTTP, err)
		}
		resTCP, err := prober.Probe(ctx, "127.0.0.1:1", time.Second)
		if err != nil || resTCP.Healthy {
			t.Errorf("Probe() TCP routing failed: res=%+v, err=%v", resTCP, err)
		}
	})
	t.Run("bounded body drain", func(t *testing.T) {
		largePayload := make([]byte, 128*1024)
		tsLarge := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(largePayload)
		}))
		defer tsLarge.Close()

		res, err := prober.ProbeHTTP(ctx, tsLarge.URL, 2*time.Second)
		if err != nil || !res.Healthy || res.StatusCode != http.StatusOK {
			t.Fatalf("expected healthy bounded body drain probe: res=%+v, err=%v", res, err)
		}
	})
}

func TestNetworkHealthProber_DefaultProberHTTP_TCP(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) }))
	defer ts.Close()

	p := NewNetworkHealthProber(
		nil,
		WithHealthHTTPClient(nil),
		WithHealthDialer(nil),
		WithHealthProbeTimeout(0),
	)
	ctx := context.Background()

	resHTTP, err := p.ProbeHTTP(ctx, ts.URL, 0)
	if err != nil || !resHTTP.Healthy {
		t.Errorf("expected healthy default ProbeHTTP: res=%+v, err=%v", resHTTP, err)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to listen: %v", err)
	}
	defer ln.Close()

	resTCP, err := p.ProbeTCP(ctx, ln.Addr().String(), 0)
	if err != nil || !resTCP.Healthy {
		t.Errorf("expected healthy default ProbeTCP: res=%+v, err=%v", resTCP, err)
	}
}

func TestNetworkHealthProber_NilReceiverSafety(t *testing.T) {
	var nilProber *NetworkHealthProber
	ctx := context.Background()

	if _, err := nilProber.Probe(ctx, "http://localhost", time.Second); err == nil {
		t.Error("expected error from nil NetworkHealthProber.Probe")
	}
	if _, err := nilProber.ProbeHTTP(ctx, "http://localhost", time.Second); err == nil {
		t.Error("expected error from nil NetworkHealthProber.ProbeHTTP")
	}
	if _, err := nilProber.ProbeTCP(ctx, "localhost:80", time.Second); err == nil {
		t.Error("expected error from nil NetworkHealthProber.ProbeTCP")
	}

	var nilFake *FakeHealthProber
	nilFake.SetResult("http://localhost", nil) // should not panic
	if _, err := nilFake.Probe(ctx, "http://localhost", time.Second); err == nil {
		t.Error("expected error from nil FakeHealthProber.Probe")
	}
	if _, err := nilFake.ProbeHTTP(ctx, "http://localhost", time.Second); err == nil {
		t.Error("expected error from nil FakeHealthProber.ProbeHTTP")
	}
	if _, err := nilFake.ProbeTCP(ctx, "localhost:80", time.Second); err == nil {
		t.Error("expected error from nil FakeHealthProber.ProbeTCP")
	}
}

func TestParseCoordinates(t *testing.T) {
	tests := []struct {
		name      string
		endpoint  string
		defaultNS string
		wantHost  string
		wantPort  uint32
		wantSvc   string
		wantNS    string
		wantProto string
	}{
		{
			name:      "empty endpoint",
			endpoint:  "",
			defaultNS: "default",
		},
		{
			name:      "whitespace endpoint",
			endpoint:  "   ",
			defaultNS: "default",
		},
		{
			name:      "full k8s service URL",
			endpoint:  "http://ray-head.xmc.svc.cluster.local:8265",
			defaultNS: "default",
			wantHost:  "ray-head.xmc.svc.cluster.local",
			wantPort:  8265,
			wantSvc:   "ray-head",
			wantNS:    "xmc",
			wantProto: "http",
		},
		{
			name:      "https endpoint default port",
			endpoint:  "https://secure-service.prod",
			defaultNS: "prod",
			wantHost:  "secure-service.prod",
			wantPort:  443,
			wantSvc:   "secure-service",
			wantNS:    "prod",
			wantProto: "https",
		},
		{
			name:      "host:port without scheme",
			endpoint:  "clickhouse.db:8123",
			defaultNS: "default",
			wantHost:  "clickhouse.db",
			wantPort:  8123,
			wantSvc:   "clickhouse",
			wantNS:    "db",
			wantProto: "http",
		},
		{
			name:      "ip:port endpoint",
			endpoint:  "10.0.0.1:9090",
			defaultNS: "fallback-ns",
			wantHost:  "10.0.0.1",
			wantPort:  9090,
			wantSvc:   "",
			wantNS:    "fallback-ns",
			wantProto: "http",
		},
		{
			name:      "bare ip endpoint without port",
			endpoint:  "10.0.0.1",
			defaultNS: "fallback-ns",
			wantHost:  "10.0.0.1",
			wantPort:  80,
			wantSvc:   "",
			wantNS:    "fallback-ns",
			wantProto: "http",
		},
		{
			name:      "short service name",
			endpoint:  "my-service",
			defaultNS: "custom-ns",
			wantHost:  "my-service",
			wantPort:  80,
			wantSvc:   "my-service",
			wantNS:    "custom-ns",
			wantProto: "http",
		},
		{
			name:      "ipv6 endpoint without port",
			endpoint:  "http://[::1]",
			defaultNS: "default",
			wantHost:  "::1",
			wantPort:  80,
			wantSvc:   "",
			wantNS:    "default",
			wantProto: "http",
		},
		{
			name:      "bare ipv6 bracketed with port",
			endpoint:  "[::1]:9090",
			defaultNS: "default",
			wantHost:  "::1",
			wantPort:  9090,
			wantSvc:   "",
			wantNS:    "default",
			wantProto: "http",
		},
		{
			name:      "bare ipv6 without brackets",
			endpoint:  "::1",
			defaultNS: "default",
			wantHost:  "::1",
			wantPort:  80,
			wantSvc:   "",
			wantNS:    "default",
			wantProto: "http",
		},
		{
			name:      "svc namespace name in standard DNS",
			endpoint:  "http://clickhouse.svc.svc.cluster.local:8123",
			defaultNS: "default",
			wantHost:  "clickhouse.svc.svc.cluster.local",
			wantPort:  8123,
			wantSvc:   "clickhouse",
			wantNS:    "svc",
			wantProto: "http",
		},
		{
			name:      "trailing dot absolute DNS",
			endpoint:  "http://my-service.prod.svc.cluster.local.:8080",
			defaultNS: "default",
			wantHost:  "my-service.prod.svc.cluster.local.",
			wantPort:  8080,
			wantSvc:   "my-service",
			wantNS:    "prod",
			wantProto: "http",
		},
		{
			name:      "endpoint with path in URL",
			endpoint:  "http://my-service.prod.svc.cluster.local:8080/healthz?verbose=true",
			defaultNS: "default",
			wantHost:  "my-service.prod.svc.cluster.local",
			wantPort:  8080,
			wantSvc:   "my-service",
			wantNS:    "prod",
			wantProto: "http",
		},
		{
			name:      "endpoint with path without scheme",
			endpoint:  "my-service:8080/metrics",
			defaultNS: "metrics-ns",
			wantHost:  "my-service",
			wantPort:  8080,
			wantSvc:   "my-service",
			wantNS:    "metrics-ns",
			wantProto: "http",
		},
		{
			name:      "grpc scheme endpoint",
			endpoint:  "grpc://my-service.xmc.svc.cluster.local:50051",
			defaultNS: "default",
			wantHost:  "my-service.xmc.svc.cluster.local",
			wantPort:  50051,
			wantSvc:   "my-service",
			wantNS:    "xmc",
			wantProto: "grpc",
		},
		{
			name:      "out-of-range port without scheme",
			endpoint:  "clickhouse.db:70000",
			defaultNS: "default",
			wantHost:  "clickhouse.db",
			wantPort:  0,
			wantSvc:   "clickhouse",
			wantNS:    "db",
			wantProto: "http",
		},
		{
			name:      "out-of-range port with http scheme",
			endpoint:  "http://clickhouse.db:99999",
			defaultNS: "default",
			wantHost:  "clickhouse.db",
			wantPort:  0,
			wantSvc:   "clickhouse",
			wantNS:    "db",
			wantProto: "http",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			coords := ParseCoordinates(tt.endpoint, tt.defaultNS)
			if tt.endpoint == "" || tt.endpoint == "   " {
				if coords != nil {
					t.Errorf("expected nil coordinates for empty endpoint, got %+v", coords)
				}
				return
			}
			if coords == nil {
				t.Fatalf("unexpected nil coordinates for endpoint %q", tt.endpoint)
			}
			if coords.Host != tt.wantHost || coords.Port != tt.wantPort ||
				coords.ServiceName != tt.wantSvc || coords.Namespace != tt.wantNS || coords.Scheme != tt.wantProto {
				t.Errorf("ParseCoordinates(%q) = %+v, want host=%s port=%d svc=%s ns=%s scheme=%s",
					tt.endpoint, coords, tt.wantHost, tt.wantPort, tt.wantSvc, tt.wantNS, tt.wantProto)
			}
		})
	}
}

func TestFakeHealthProber(t *testing.T) {
	fake := NewFakeHealthProber()
	ctx := context.Background()

	// Default success
	res, err := fake.Probe(ctx, "http://target:8080", time.Second)
	if err != nil || !res.Healthy {
		t.Errorf("expected default healthy result, got res=%+v, err=%v", res, err)
	}

	// Preconfigured result
	fake.SetResult("http://target:8080", &HealthProbeResult{
		Healthy: false, StatusCode: 503, Message: "Service Unavailable",
	})
	res, err = fake.ProbeHTTP(ctx, "http://target:8080", time.Second)
	if err != nil || res.Healthy || res.StatusCode != 503 {
		t.Errorf("expected preconfigured unhealthy result, got res=%+v, err=%v", res, err)
	}

	// Injected error
	fake.ProbeErr = errors.New("network unreachable")
	if _, err := fake.ProbeTCP(ctx, "target:8080", time.Second); err == nil {
		t.Error("expected injected probe error")
	}
}
