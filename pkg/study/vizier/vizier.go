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

// Package vizier resolves the live vizier Component CR endpoint and manages the Vizier gRPC client.
package vizier

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"net"
	"strconv"
	"strings"
	"time"
	"unicode"

	"google.golang.org/grpc"
	"google.golang.org/grpc/backoff"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/status"
	"xmanager_cloud/internal/logging"
	"xmanager_cloud/pkg/components/crreader"
)

const vizierComponentName = "vizier"

// Client holds a single shared plaintext gRPC connection to the in-cluster Vizier service.
type Client struct{ conn *grpc.ClientConn }

// Dial creates a shared plaintext gRPC ClientConn to target (host:port) with bounded reconnect backoff.
func Dial(target string) (*Client, error) { return dial(target) }

func dial(target string, extraOpts ...grpc.DialOption) (*Client, error) {
	if _, err := validateEndpoint(target); err != nil {
		return nil, err
	}
	conn, err := grpc.NewClient("dns:///"+target, append([]grpc.DialOption{
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithConnectParams(grpc.ConnectParams{Backoff: backoff.Config{BaseDelay: 250 * time.Millisecond, Multiplier: 1.6, Jitter: 0.2, MaxDelay: 5 * time.Second}}),
	}, extraOpts...)...)
	if err != nil {
		return nil, fmt.Errorf("failed to create vizier gRPC client: %w", err)
	}
	return &Client{conn: conn}, nil
}

// Ping verifies Vizier reachability via grpc.health.v1.Health/Check (SERVING or Unimplemented).
func (c *Client) Ping(ctx context.Context) error {
	if c == nil || c.conn == nil {
		return errors.New("vizier client is not initialized")
	}
	resp, err := healthpb.NewHealthClient(c.conn).Check(ctx, &healthpb.HealthCheckRequest{}, grpc.MaxCallRecvMsgSize(64*1024))
	if st := status.Convert(err); err != nil {
		if st.Code() == codes.Unimplemented {
			return nil
		}
		msg := strings.ToValidUTF8(st.Message(), "")
		if len(msg) > 256 {
			msg = strings.ToValidUTF8(msg[:256], "") + "..."
		}
		return status.Errorf(st.Code(), "vizier health check failed: %s", msg)
	}
	if resp.GetStatus() != healthpb.HealthCheckResponse_SERVING {
		return fmt.Errorf("vizier health status %s is not serving", resp.GetStatus())
	}
	return nil
}

// Close closes the underlying gRPC ClientConn.
func (c *Client) Close() error {
	if c == nil || c.conn == nil {
		return nil
	}
	return c.conn.Close()
}

// WaitForEndpoint polls the live vizier Component CR until enabled with a valid host:port endpoint.
func WaitForEndpoint(ctx context.Context, getter crreader.Getter, namespace string) (string, error) {
	return waitForEndpoint(ctx, getter, namespace, 500*time.Millisecond, 10*time.Second)
}

func waitForEndpoint(ctx context.Context, getter crreader.Getter, namespace string, initialBackoff, maxBackoff time.Duration) (string, error) {
	if getter == nil {
		return "", errors.New("component getter cannot be nil")
	}
	ns, delay := crreader.ResolveNamespace(namespace), initialBackoff
	for {
		endpoint, err := resolveEndpoint(ctx, getter, ns)
		if err == nil {
			logging.Info(ctx, "Resolved Vizier endpoint from Component CR", "namespace", ns, "component", vizierComponentName, "endpoint", endpoint)
			return endpoint, nil
		}
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		logging.Warn(ctx, "Waiting for valid Vizier Component CR endpoint", "namespace", ns, "component", vizierComponentName, "error", err, "retry_in", delay)
		timer := time.NewTimer(time.Duration(float64(delay) * (0.8 + 0.4*rand.Float64())))
		select {
		case <-ctx.Done():
			timer.Stop()
			return "", ctx.Err()
		case <-timer.C:
		}
		delay = min(delay*2, maxBackoff)
	}
}

func resolveEndpoint(ctx context.Context, getter crreader.Getter, ns string) (string, error) {
	reqCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	comp, err := getter.GetComponent(reqCtx, ns, vizierComponentName)
	if err != nil {
		return "", fmt.Errorf("failed to get component %s/%s: %w", ns, vizierComponentName, err)
	}
	if crreader.IsDisabled(comp) {
		return "", fmt.Errorf("component %s/%s is disabled", ns, vizierComponentName)
	}
	params, err := crreader.Parameters(comp)
	if err != nil {
		return "", fmt.Errorf("failed to parse parameters for %s/%s: %w", ns, vizierComponentName, err)
	}
	return validateEndpoint(params["endpoint"])
}

func validateEndpoint(raw string) (string, error) {
	if raw == "" {
		return "", errors.New("vizier endpoint is required")
	}
	for _, r := range raw {
		if r > 0x7f || unicode.IsSpace(r) || unicode.IsControl(r) || strings.ContainsRune("/?#@\\%", r) {
			return "", errors.New("invalid vizier endpoint: must be ASCII host:port without scheme, credentials, path, or whitespace")
		}
	}
	host, port, err := net.SplitHostPort(raw)
	validHost := (strings.Contains(host, ":") && net.ParseIP(host) != nil) || (!strings.Contains(host, ":") && strings.IndexFunc(host, func(r rune) bool {
		return (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') && (r < '0' || r > '9') && r != '.' && r != '-'
	}) < 0)
	if err != nil || host == "" || strings.HasPrefix(raw, "[") != strings.Contains(host, ":") || !validHost {
		return "", errors.New("invalid vizier endpoint: must be host:port, with IPv6 hosts in brackets")
	}
	if n, err := strconv.ParseUint(port, 10, 64); err != nil || n < 1 || n > 65535 || strconv.FormatUint(n, 10) != port {
		return "", errors.New("invalid vizier endpoint: port must be a canonical integer between 1 and 65535")
	}
	return raw, nil
}
