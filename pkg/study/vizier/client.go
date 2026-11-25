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

package vizier

import (
	"context"
	"errors"
	"fmt"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/backoff"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials/insecure"
	healthpb "google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/status"
)

const (
	// maxReconnectBackoff bounds gRPC reconnect delay so recovery from transient Vizier outages
	// completes promptly without custom reconnect loops.
	maxReconnectBackoff = 5 * time.Second
	maxHealthRespBytes  = 64 * 1024
)

// Client holds a single shared plaintext gRPC connection to the in-cluster Vizier service.
type Client struct {
	conn *grpc.ClientConn
}

// Dial creates a shared plaintext gRPC ClientConn to target (host:port) with bounded reconnect backoff.
func Dial(target string, extraOpts ...grpc.DialOption) (*Client, error) {
	if _, err := validateEndpoint(target); err != nil {
		return nil, err
	}
	opts := []grpc.DialOption{
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithConnectParams(grpc.ConnectParams{
			Backoff: backoff.Config{
				BaseDelay:  250 * time.Millisecond,
				Multiplier: 1.6,
				Jitter:     0.2,
				MaxDelay:   maxReconnectBackoff,
			},
		}),
		grpc.WithDefaultCallOptions(grpc.MaxCallRecvMsgSize(maxHealthRespBytes)),
	}
	opts = append(opts, extraOpts...)
	conn, err := grpc.NewClient("passthrough:///"+target, opts...)
	if err != nil {
		return nil, fmt.Errorf("failed to create vizier gRPC client: %w", err)
	}
	return &Client{conn: conn}, nil
}

// Ping verifies Vizier gRPC reachability via grpc.health.v1.Health/Check. Both SERVING and
// codes.Unimplemented (returned by OSS google-vizier DefaultVizierServer) are treated as reachable.
func (c *Client) Ping(ctx context.Context) error {
	if c == nil || c.conn == nil {
		return errors.New("vizier client is not initialized")
	}
	resp, err := healthpb.NewHealthClient(c.conn).Check(ctx, &healthpb.HealthCheckRequest{Service: ""})
	if err != nil {
		if status.Code(err) == codes.Unimplemented {
			return nil
		}
		return fmt.Errorf("vizier health check failed: %w", err)
	}
	if s := resp.GetStatus(); s != healthpb.HealthCheckResponse_SERVING {
		return fmt.Errorf("vizier health status %s is not serving", s)
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
