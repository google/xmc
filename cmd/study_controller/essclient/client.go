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

// Package essclient provides a gRPC client for xmc-study-controller to call the Experiment State
// Server using a Workload Identity Google ID token.
package essclient

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"

	"golang.org/x/oauth2"
	"google.golang.org/api/idtoken"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
	spb "xmanager_cloud/api/protos/experiment"
)

type tokenSourceFactory func(context.Context, string, ...idtoken.ClientOption) (oauth2.TokenSource, error)

// Client calls the Experiment State Server over plaintext in-cluster gRPC with Google ID token
// per-RPC credentials.
type Client struct {
	conn *grpc.ClientConn
	ess  spb.ExperimentStateServiceClient
}

// New dials addr over in-cluster h2c and attaches Google ID tokens minted for audience to every RPC.
// ctx governs the lifetime of the lazily initialized idtoken.TokenSource and must be process-scoped.
func New(ctx context.Context, addr, audience string) (*Client, error) {
	return newWithFactory(ctx, addr, audience, idtoken.NewTokenSource)
}

func newWithFactory(ctx context.Context, addr, audience string, factory tokenSourceFactory) (*Client, error) {
	addr = strings.TrimSpace(addr)
	if addr == "" {
		return nil, errors.New("ess address must not be empty")
	}
	creds, err := newIDTokenCredentials(ctx, audience, factory)
	if err != nil {
		return nil, err
	}
	conn, err := grpc.NewClient(
		addr,
		grpc.WithTransportCredentials(insecure.NewCredentials()),
		grpc.WithPerRPCCredentials(creds),
	)
	if err != nil {
		return nil, fmt.Errorf("create ESS gRPC client: %w", err)
	}
	return &Client{conn: conn, ess: spb.NewExperimentStateServiceClient(conn)}, nil
}

// GetExperiment fetches the named experiment ("experiments/{xid}") from ESS.
func (c *Client) GetExperiment(ctx context.Context, name string) (*spb.Experiment, error) {
	return c.ess.GetExperiment(ctx, &spb.GetExperimentRequest{Name: name})
}

// Close closes the underlying gRPC connection.
func (c *Client) Close() error {
	if c == nil || c.conn == nil {
		return nil
	}
	return c.conn.Close()
}

var _ credentials.PerRPCCredentials = (*idTokenCredentials)(nil)

type idTokenCredentials struct {
	baseCtx  context.Context
	audience string
	factory  tokenSourceFactory

	mu sync.Mutex
	ts oauth2.TokenSource
}

func newIDTokenCredentials(ctx context.Context, audience string, factory tokenSourceFactory) (*idTokenCredentials, error) {
	audience = strings.TrimSpace(audience)
	if audience == "" {
		return nil, errors.New("ess audience must not be empty")
	}
	if factory == nil {
		return nil, errors.New("id token source factory must not be nil")
	}
	if ctx == nil {
		ctx = context.Background()
	}
	return &idTokenCredentials{baseCtx: ctx, audience: audience, factory: factory}, nil
}

// GetRequestMetadata attaches a Google ID token as an Authorization Bearer header.
func (c *idTokenCredentials) GetRequestMetadata(context.Context, ...string) (map[string]string, error) {
	source, err := c.tokenSource()
	if err != nil {
		return nil, fmt.Errorf("obtain ESS id token source: %w", err)
	}
	tok, err := source.Token()
	if err != nil {
		return nil, fmt.Errorf("obtain ESS id token: %w", err)
	}
	if tok == nil || tok.AccessToken == "" {
		return nil, errors.New("obtain ESS id token: empty access token")
	}
	return map[string]string{"authorization": "Bearer " + tok.AccessToken}, nil
}

func (c *idTokenCredentials) tokenSource() (oauth2.TokenSource, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.ts != nil {
		return c.ts, nil
	}
	source, err := c.factory(c.baseCtx, c.audience)
	if err != nil {
		return nil, err
	}
	c.ts = source
	return c.ts, nil
}

// RequireTransportSecurity returns false to support cluster-internal plaintext h2c connections.
func (c *idTokenCredentials) RequireTransportSecurity() bool { return false }
