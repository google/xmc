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

package healthprobe

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/status"
)

const (
	grpcScheme = "grpc://"
	// grpcsScheme is rejected explicitly: TLS gRPC probes are not supported.
	grpcsScheme = "grpcs://"
	// maxGRPCHealthResponseBytes bounds the Health/Check response, like the HTTP probe's body drain.
	maxGRPCHealthResponseBytes = 64 * 1024
	// maxGRPCStatusMessageBytes bounds the server-supplied status text copied into probe messages.
	maxGRPCStatusMessageBytes = 256

	// GRPCHealthUnimplementedMessage reports that the server answered Health/Check with
	// UNIMPLEMENTED: it speaks gRPC but does not serve grpc.health.v1, so the probe falls back
	// to transport reachability, which already proves more than a TCP dial.
	GRPCHealthUnimplementedMessage = "gRPC health service not implemented; server reachable (TCP-equivalent fallback)"
)

// ErrGRPCSNotSupported reports a grpcs:// probe target; only plaintext grpc:// is supported.
var ErrGRPCSNotSupported = errors.New("grpcs:// health probes are not supported; use grpc:// (plaintext) or https://")

// ProbeGRPC calls grpc.health.v1.Health/Check for the overall server (empty service name) over a
// plaintext connection to address (host:port, with or without the grpc:// prefix).
//
// SERVING is healthy. UNIMPLEMENTED is healthy with GRPCHealthUnimplementedMessage, because the
// server answered with a real gRPC status. Any other serving status, RPC error, or timeout is an
// unhealthy result. Cancellation of ctx itself returns ctx's error. Malformed addresses return an
// error that never echoes the address, so credentials in a misconfigured target are not logged.
func (p *NetworkHealthProber) ProbeGRPC(ctx context.Context, address string, timeout time.Duration) (*HealthProbeResult, error) {
	if p == nil {
		return nil, errors.New("network health prober is nil")
	}
	target, err := parseGRPCTarget(address)
	if err != nil {
		return nil, err
	}
	probeCtx, cancel := context.WithTimeout(ctx, p.effectiveTimeout(timeout))
	defer cancel()

	conn, err := grpc.NewClient("passthrough:///"+target,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithContextDialer(p.dialContext),
		grpc.WithDefaultCallOptions(grpc.MaxCallRecvMsgSize(maxGRPCHealthResponseBytes)),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to create gRPC probe client: %w", err)
	}
	defer conn.Close()

	start := time.Now()
	resp, err := healthpb.NewHealthClient(conn).Check(probeCtx, &healthpb.HealthCheckRequest{})
	latency := time.Since(start)
	if err != nil && ctx.Err() != nil {
		return nil, ctx.Err()
	}
	res := grpcHealthVerdict(resp, err)
	res.Latency = latency
	return res, nil
}

// dialContext dials addr over TCP with the prober's dialer, so gRPC and TCP probes share it.
func (p *NetworkHealthProber) dialContext(ctx context.Context, addr string) (net.Conn, error) {
	dialer := p.dialer
	if dialer == nil {
		dialer = &net.Dialer{}
	}
	return dialer.DialContext(ctx, "tcp", addr)
}

// grpcHealthVerdict maps a Health/Check outcome to a probe result without latency.
func grpcHealthVerdict(resp *healthpb.HealthCheckResponse, err error) *HealthProbeResult {
	if err != nil {
		st := status.Convert(err)
		if st.Code() == codes.Unimplemented {
			return &HealthProbeResult{Healthy: true, Message: GRPCHealthUnimplementedMessage}
		}
		return &HealthProbeResult{Message: fmt.Sprintf("gRPC health check failed: %s: %s", st.Code(), boundStatusMessage(st.Message()))}
	}
	if s := resp.GetStatus(); s != healthpb.HealthCheckResponse_SERVING {
		return &HealthProbeResult{Message: fmt.Sprintf("gRPC health %s (unhealthy status)", s)}
	}
	return &HealthProbeResult{Healthy: true, Message: "gRPC health SERVING"}
}

// boundStatusMessage truncates server-supplied status text to valid UTF-8 of bounded size.
func boundStatusMessage(msg string) string {
	if len(msg) <= maxGRPCStatusMessageBytes {
		return msg
	}
	return strings.ToValidUTF8(msg[:maxGRPCStatusMessageBytes], "") + "…"
}

// parseGRPCTarget validates address as [grpc://]host:port and returns host:port. Other schemes,
// paths, queries, fragments, and user info are rejected: the probe always checks the overall server.
func parseGRPCTarget(address string) (string, error) {
	trimmed := strings.TrimSpace(address)
	if trimmed == "" {
		return "", errors.New("gRPC probe address must not be empty")
	}
	if hasSchemePrefix(trimmed, grpcsScheme) {
		return "", ErrGRPCSNotSupported
	}
	if hasSchemePrefix(trimmed, grpcScheme) {
		trimmed = trimmed[len(grpcScheme):]
	}
	if strings.ContainsAny(trimmed, "/?#@") {
		return "", errors.New("invalid gRPC probe address: schemes other than grpc://, user info, paths, queries, and fragments are not supported, want grpc://host:port")
	}
	host, port, err := net.SplitHostPort(trimmed)
	if err != nil || host == "" || !validPort(port) {
		return "", errors.New("invalid gRPC probe address: want grpc://host:port with a port between 1 and 65535")
	}
	return trimmed, nil
}

// validPort reports whether port is a decimal TCP port between 1 and 65535.
func validPort(port string) bool {
	n, err := strconv.Atoi(port)
	return err == nil && n >= 1 && n <= 65535 && strconv.Itoa(n) == port
}

// hasSchemePrefix reports whether s starts with scheme, ignoring ASCII case.
func hasSchemePrefix(s, scheme string) bool {
	return len(s) >= len(scheme) && strings.EqualFold(s[:len(scheme)], scheme)
}
