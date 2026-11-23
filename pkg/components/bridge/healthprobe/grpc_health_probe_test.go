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

package healthprobe_test

import (
	"context"
	"errors"
	"net"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/health"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/status"

	"xmanager_cloud/pkg/components/bridge/healthprobe"
	"xmanager_cloud/pkg/components/bridge/healthprobe/healthprobetest"
)

// stubHealthServer answers Health/Check with a fixed response or error.
type stubHealthServer struct {
	healthpb.UnimplementedHealthServer
	status healthpb.HealthCheckResponse_ServingStatus
	err    error
}

func (s *stubHealthServer) Check(context.Context, *healthpb.HealthCheckRequest) (*healthpb.HealthCheckResponse, error) {
	if s.err != nil {
		return nil, s.err
	}
	return &healthpb.HealthCheckResponse{Status: s.status}, nil
}

// startGRPCServer serves a gRPC server on network ("tcp" or "tcp6") loopback and returns its address.
// register may be nil, leaving the health service unimplemented.
func startGRPCServer(t *testing.T, network, host string, register func(*grpc.Server)) string {
	t.Helper()
	ln, err := net.Listen(network, net.JoinHostPort(host, "0"))
	if err != nil {
		t.Skipf("cannot listen on %s %s: %v", network, host, err)
	}
	srv := grpc.NewServer()
	if register != nil {
		register(srv)
	}
	go func() { _ = srv.Serve(ln) }()
	t.Cleanup(srv.Stop)
	return ln.Addr().String()
}

func healthServerWith(serving healthpb.HealthCheckResponse_ServingStatus) func(*grpc.Server) {
	return func(s *grpc.Server) {
		hs := health.NewServer()
		hs.SetServingStatus("", serving)
		healthpb.RegisterHealthServer(s, hs)
	}
}

func stubServer(stub *stubHealthServer) func(*grpc.Server) {
	return func(s *grpc.Server) { healthpb.RegisterHealthServer(s, stub) }
}

var ignoreLatency = cmpopts.IgnoreFields(healthprobe.HealthProbeResult{}, "Latency")

func TestNetworkHealthProber_ProbeGRPC_Verdicts(t *testing.T) {
	tests := []struct {
		name     string
		register func(*grpc.Server)
		want     *healthprobe.HealthProbeResult
	}{
		{
			name:     "SERVING is healthy",
			register: healthServerWith(healthpb.HealthCheckResponse_SERVING),
			want:     &healthprobe.HealthProbeResult{Healthy: true, Message: "gRPC health SERVING"},
		},
		{
			name:     "NOT_SERVING is unhealthy",
			register: healthServerWith(healthpb.HealthCheckResponse_NOT_SERVING),
			want:     &healthprobe.HealthProbeResult{Message: "gRPC health NOT_SERVING (unhealthy status)"},
		},
		{
			name:     "SERVICE_UNKNOWN is unhealthy",
			register: stubServer(&stubHealthServer{status: healthpb.HealthCheckResponse_SERVICE_UNKNOWN}),
			want:     &healthprobe.HealthProbeResult{Message: "gRPC health SERVICE_UNKNOWN (unhealthy status)"},
		},
		{
			name:     "UNKNOWN is unhealthy",
			register: stubServer(&stubHealthServer{status: healthpb.HealthCheckResponse_UNKNOWN}),
			want:     &healthprobe.HealthProbeResult{Message: "gRPC health UNKNOWN (unhealthy status)"},
		},
		{
			name:     "UNIMPLEMENTED falls back to reachable",
			register: nil,
			want:     &healthprobe.HealthProbeResult{Healthy: true, Message: healthprobe.GRPCHealthUnimplementedMessage},
		},
		{
			name:     "NotFound status error is unhealthy",
			register: stubServer(&stubHealthServer{err: status.Error(codes.NotFound, "unknown service")}),
			want:     &healthprobe.HealthProbeResult{Message: "gRPC health check failed: NotFound: unknown service"},
		},
		{
			name:     "Internal status error is unhealthy",
			register: stubServer(&stubHealthServer{err: status.Error(codes.Internal, "database unavailable")}),
			want:     &healthprobe.HealthProbeResult{Message: "gRPC health check failed: Internal: database unavailable"},
		},
	}
	prober := healthprobe.NewNetworkHealthProber()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			addr := startGRPCServer(t, "tcp", "127.0.0.1", tt.register)
			got, err := prober.ProbeGRPC(context.Background(), addr, 5*time.Second)
			if err != nil {
				t.Fatalf("ProbeGRPC() error = %v", err)
			}
			if diff := cmp.Diff(tt.want, got, ignoreLatency); diff != "" {
				t.Errorf("ProbeGRPC() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestNetworkHealthProber_ProbeGRPC_BoundsServerStatusMessage(t *testing.T) {
	long := strings.Repeat("é", 1000)
	addr := startGRPCServer(t, "tcp", "127.0.0.1", stubServer(&stubHealthServer{err: status.Error(codes.Internal, long)}))

	got, err := healthprobe.NewNetworkHealthProber().ProbeGRPC(context.Background(), addr, 5*time.Second)
	if err != nil {
		t.Fatalf("ProbeGRPC() error = %v", err)
	}
	if got.Healthy {
		t.Errorf("ProbeGRPC() Healthy = true, want false")
	}
	if !utf8.ValidString(got.Message) || !strings.HasSuffix(got.Message, "…") || len(got.Message) > 400 {
		t.Errorf("ProbeGRPC() Message = %q (%d bytes), want valid UTF-8, truncated with an ellipsis, at most 400 bytes", got.Message, len(got.Message))
	}
}

func TestNetworkHealthProber_ProbeGRPC_TransportFailures(t *testing.T) {
	// A listener that accepts connections but never speaks HTTP/2 makes Check wait for the deadline.
	silent, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.Listen() error = %v", err)
	}
	t.Cleanup(func() { _ = silent.Close() })
	closed, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.Listen() error = %v", err)
	}
	closedAddr := closed.Addr().String()
	_ = closed.Close()

	tests := []struct {
		name        string
		addr        string
		timeout     time.Duration
		wantMessage string
	}{
		{name: "timeout on a silent server", addr: silent.Addr().String(), timeout: 200 * time.Millisecond, wantMessage: "gRPC health check failed: DeadlineExceeded"},
		{name: "connection refused", addr: closedAddr, timeout: 2 * time.Second, wantMessage: "gRPC health check failed: Unavailable"},
	}
	prober := healthprobe.NewNetworkHealthProber()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			start := time.Now()
			got, err := prober.ProbeGRPC(context.Background(), tt.addr, tt.timeout)
			if err != nil {
				t.Fatalf("ProbeGRPC() error = %v, want unhealthy result", err)
			}
			if got.Healthy || !strings.HasPrefix(got.Message, tt.wantMessage) {
				t.Errorf("ProbeGRPC() = %+v, want unhealthy with message prefix %q", got, tt.wantMessage)
			}
			if elapsed := time.Since(start); elapsed > tt.timeout+2*time.Second {
				t.Errorf("ProbeGRPC() took %v, want it bounded by timeout %v", elapsed, tt.timeout)
			}
		})
	}
}

func TestNetworkHealthProber_ProbeGRPC_CanceledParentContext(t *testing.T) {
	addr := startGRPCServer(t, "tcp", "127.0.0.1", healthServerWith(healthpb.HealthCheckResponse_SERVING))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	if _, err := healthprobe.NewNetworkHealthProber().ProbeGRPC(ctx, addr, time.Second); !errors.Is(err, context.Canceled) {
		t.Errorf("ProbeGRPC() error = %v, want context.Canceled", err)
	}
}

func TestNetworkHealthProber_ProbeGRPC_AcceptedAddresses(t *testing.T) {
	addr4 := startGRPCServer(t, "tcp", "127.0.0.1", healthServerWith(healthpb.HealthCheckResponse_SERVING))
	tests := []struct {
		name    string
		address func(t *testing.T) string
	}{
		{name: "bare host:port", address: func(*testing.T) string { return addr4 }},
		{name: "grpc:// prefix", address: func(*testing.T) string { return "grpc://" + addr4 }},
		{name: "uppercase prefix", address: func(*testing.T) string { return "GRPC://" + addr4 }},
		{name: "surrounding whitespace", address: func(*testing.T) string { return " \tgrpc://" + addr4 + "\n" }},
		{name: "bracketed IPv6", address: func(t *testing.T) string {
			return "grpc://" + startGRPCServer(t, "tcp6", "::1", healthServerWith(healthpb.HealthCheckResponse_SERVING))
		}},
	}
	prober := healthprobe.NewNetworkHealthProber()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := prober.ProbeGRPC(context.Background(), tt.address(t), 5*time.Second)
			if err != nil || !got.Healthy {
				t.Errorf("ProbeGRPC() = %+v, %v, want healthy", got, err)
			}
		})
	}
}

func TestNetworkHealthProber_ProbeGRPC_RejectsMalformedAddresses(t *testing.T) {
	const secret = "s3cr3t-token"
	tests := []struct {
		name    string
		address string
		wantErr error
	}{
		{name: "empty", address: ""},
		{name: "whitespace only", address: " \t\n"},
		{name: "scheme only", address: "grpc://"},
		{name: "missing port", address: "grpc://vizier-service"},
		{name: "empty host", address: "grpc://:7077"},
		{name: "port zero", address: "grpc://vizier-service:0"},
		{name: "port out of range", address: "grpc://vizier-service:65536"},
		{name: "signed port", address: "grpc://vizier-service:+7077"},
		{name: "non-ASCII port digits", address: "grpc://vizier-service:７０７７"},
		{name: "unbracketed IPv6", address: "grpc://::1:7077"},
		{name: "named service path", address: "grpc://vizier-service:7077/vizier.VizierService"},
		{name: "query", address: "grpc://vizier-service:7077?service=x"},
		{name: "empty query", address: "grpc://vizier-service:7077?"},
		{name: "fragment", address: "grpc://vizier-service:7077#x"},
		{name: "user info with secret", address: "grpc://admin:" + secret + "@vizier-service:7077"},
		{name: "other scheme", address: "http://vizier-service:7077"},
		{name: "grpcs scheme", address: "grpcs://admin:" + secret + "@vizier-service:7077", wantErr: healthprobe.ErrGRPCSNotSupported},
		{name: "uppercase grpcs scheme", address: "GRPCS://vizier-service:7077", wantErr: healthprobe.ErrGRPCSNotSupported},
	}
	prober := healthprobe.NewNetworkHealthProber()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := prober.ProbeGRPC(context.Background(), tt.address, time.Second)
			if err == nil {
				t.Fatalf("ProbeGRPC(%q) error = nil, want error", tt.address)
			}
			if tt.wantErr != nil && !errors.Is(err, tt.wantErr) {
				t.Errorf("ProbeGRPC(%q) error = %v, want %v", tt.address, err, tt.wantErr)
			}
			if strings.Contains(err.Error(), secret) {
				t.Errorf("ProbeGRPC(%q) error %q leaks the address secret", tt.address, err)
			}
		})
	}
}

func TestNetworkHealthProber_Probe_RoutesGRPC(t *testing.T) {
	addr := startGRPCServer(t, "tcp", "127.0.0.1", healthServerWith(healthpb.HealthCheckResponse_NOT_SERVING))
	prober := healthprobe.NewNetworkHealthProber()
	ctx := context.Background()

	// NOT_SERVING proves the gRPC probe ran: a TCP probe of the same address would be healthy.
	got, err := prober.Probe(ctx, " Grpc://"+addr, time.Second*5)
	if err != nil || got.Healthy || got.Message != "gRPC health NOT_SERVING (unhealthy status)" {
		t.Errorf("Probe(grpc://) = %+v, %v, want the gRPC NOT_SERVING verdict", got, err)
	}
	if _, err := prober.Probe(ctx, "grpcs://"+addr, time.Second); !errors.Is(err, healthprobe.ErrGRPCSNotSupported) {
		t.Errorf("Probe(grpcs://) error = %v, want ErrGRPCSNotSupported", err)
	}
}

func TestNetworkHealthProber_ProbeGRPC_ZeroValueProberUsesDefaults(t *testing.T) {
	addr := startGRPCServer(t, "tcp", "127.0.0.1", healthServerWith(healthpb.HealthCheckResponse_SERVING))

	got, err := (&healthprobe.NetworkHealthProber{}).ProbeGRPC(context.Background(), addr, 0)
	if err != nil || !got.Healthy {
		t.Errorf("ProbeGRPC() = %+v, %v, want healthy with the default dialer and timeout", got, err)
	}
}

func TestProbeGRPC_NilReceivers(t *testing.T) {
	ctx := context.Background()
	var nilProber *healthprobe.NetworkHealthProber
	if _, err := nilProber.ProbeGRPC(ctx, "localhost:7077", time.Second); err == nil {
		t.Error("expected error from nil NetworkHealthProber.ProbeGRPC")
	}
	var nilFake *healthprobetest.FakeHealthProber
	if _, err := nilFake.ProbeGRPC(ctx, "grpc://localhost:7077", time.Second); err == nil {
		t.Error("expected error from nil FakeHealthProber.ProbeGRPC")
	}
}

func TestFakeHealthProber_ProbeGRPC(t *testing.T) {
	fake := healthprobetest.NewFakeHealthProber()
	want := &healthprobe.HealthProbeResult{Message: "gRPC health NOT_SERVING (unhealthy status)"}
	fake.SetResult("grpc://vizier:7077", want)

	got, err := fake.ProbeGRPC(context.Background(), "grpc://vizier:7077", time.Second)
	if err != nil {
		t.Fatalf("ProbeGRPC() error = %v", err)
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("ProbeGRPC() mismatch (-want +got):\n%s", diff)
	}
}
