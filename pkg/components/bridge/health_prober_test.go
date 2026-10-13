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
	"strings"
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
		withProbeDialControl(allowAllProbeDials),
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
	t.Run("whitespace trimmed URL", func(t *testing.T) {
		res, err := prober.ProbeHTTP(ctx, "  "+ts.URL+"/health  \n\t", time.Second)
		if err != nil || !res.Healthy || res.StatusCode != http.StatusOK {
			t.Fatalf("expected healthy response from whitespace-padded URL: res=%+v, err=%v", res, err)
		}
	})
	t.Run("body read error preserves status code", func(t *testing.T) {
		client := &http.Client{
			Transport: roundTripFunc(func(req *http.Request) (*http.Response, error) {
				return &http.Response{
					StatusCode: http.StatusTeapot,
					Body:       &errReadCloser{err: errors.New("simulated body read error")},
					Header:     make(http.Header),
				}, nil
			}),
		}
		p := NewNetworkHealthProber(WithHealthHTTPClient(client))
		res, err := p.ProbeHTTP(ctx, "http://example.com/health", time.Second)
		if err != nil {
			t.Fatalf("unexpected probe error: %v", err)
		}
		if res.Healthy {
			t.Fatalf("expected unhealthy probe result on body read failure: %+v", res)
		}
		if res.StatusCode != http.StatusTeapot {
			t.Errorf("expected StatusCode %d, got %d", http.StatusTeapot, res.StatusCode)
		}
		if !strings.Contains(res.Message, "HTTP body read failed") {
			t.Errorf("expected body read error message, got %q", res.Message)
		}
	})
}

type errReadCloser struct {
	err error
}

func (e *errReadCloser) Read(p []byte) (n int, err error) {
	return 0, e.err
}

func (e *errReadCloser) Close() error {
	return nil
}

type roundTripFunc func(req *http.Request) (*http.Response, error)

func (f roundTripFunc) RoundTrip(req *http.Request) (*http.Response, error) {
	return f(req)
}

func TestNetworkHealthProber_DefaultProberBlocksLoopback(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) }))
	defer ts.Close()

	p := NewNetworkHealthProber(
		nil,
		WithHealthHTTPClient(nil),
		WithHealthDialer(nil),
		WithHealthProbeTimeout(0),
		withProbeDialControl(nil),
	)
	ctx := context.Background()

	resHTTP, err := p.ProbeHTTP(ctx, ts.URL, 0)
	if err != nil || resHTTP.Healthy || !strings.Contains(resHTTP.Message, errBlockedProbeAddress.Error()) {
		t.Errorf("expected default ProbeHTTP to block loopback: res=%+v, err=%v", resHTTP, err)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to listen: %v", err)
	}
	defer ln.Close()

	resTCP, err := p.ProbeTCP(ctx, ln.Addr().String(), 0)
	if err != nil || resTCP.Healthy || !strings.Contains(resTCP.Message, errBlockedProbeAddress.Error()) {
		t.Errorf("expected default ProbeTCP to block loopback: res=%+v, err=%v", resTCP, err)
	}
}

func TestNetworkHealthProber_UninitializedProberRefusesToDial(t *testing.T) {
	p := &NetworkHealthProber{}
	ctx := context.Background()
	if _, err := p.ProbeHTTP(ctx, "http://example.com", time.Second); !errors.Is(err, errNetworkHealthProberUninitialized) {
		t.Errorf("ProbeHTTP() error = %v, want %v", err, errNetworkHealthProberUninitialized)
	}
	if _, err := p.ProbeTCP(ctx, "example.com:80", time.Second); !errors.Is(err, errNetworkHealthProberUninitialized) {
		t.Errorf("ProbeTCP() error = %v, want %v", err, errNetworkHealthProberUninitialized)
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
