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
	"testing"
	"time"
)

func TestNetworkHealthProber_ProbeTCP(t *testing.T) {
	prober := NewNetworkHealthProber(
		WithHealthDialer(&net.Dialer{}),
		WithHealthProbeTimeout(2*time.Second),
	)
	ctx := context.Background()

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
}

func TestNetworkHealthProber_DefaultProberTCP(t *testing.T) {
	p := NewNetworkHealthProber(
		nil,
		WithHealthDialer(nil),
		WithHealthProbeTimeout(0),
	)
	ctx := context.Background()

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

	if _, err := nilProber.Probe(ctx, "localhost:80", time.Second); err == nil {
		t.Error("expected error from nil NetworkHealthProber.Probe")
	}
	if _, err := nilProber.ProbeTCP(ctx, "localhost:80", time.Second); err == nil {
		t.Error("expected error from nil NetworkHealthProber.ProbeTCP")
	}

	var nilFake *FakeHealthProber
	nilFake.SetResult("http://localhost", nil) // should not panic
	if _, err := nilFake.Probe(ctx, "http://localhost", time.Second); err == nil {
		t.Error("expected error from nil FakeHealthProber.Probe")
	}
	if _, err := nilFake.ProbeTCP(ctx, "localhost:80", time.Second); err == nil {
		t.Error("expected error from nil FakeHealthProber.ProbeTCP")
	}
}

func TestFakeHealthProber(t *testing.T) {
	fake := NewFakeHealthProber()
	ctx := context.Background()

	// Default success
	res, err := fake.Probe(ctx, "target:8080", time.Second)
	if err != nil || !res.Healthy {
		t.Errorf("expected default healthy result, got res=%+v, err=%v", res, err)
	}

	// Preconfigured result
	fake.SetResult("target:8080", &HealthProbeResult{
		Healthy: false, StatusCode: 503, Message: "Service Unavailable",
	})
	res, err = fake.Probe(ctx, "target:8080", time.Second)
	if err != nil || res.Healthy || res.StatusCode != 503 {
		t.Errorf("expected preconfigured unhealthy result, got res=%+v, err=%v", res, err)
	}

	// Injected error
	fake.ProbeErr = errors.New("network unreachable")
	if _, err := fake.ProbeTCP(ctx, "target:8080", time.Second); err == nil {
		t.Error("expected injected probe error")
	}
}
