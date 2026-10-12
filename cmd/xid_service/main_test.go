// Copyright 2025 Google LLC
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
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/reflection"
	"xmanager_cloud/cmd/xid_service/config"
	"xmanager_cloud/cmd/xid_service/server"
	"xmanager_cloud/internal/auth"
	"xmanager_cloud/internal/storage"
	apipb "xmanager_cloud/xid_service/proto/api_go_proto"
)

type dummyAuthorizer struct {
	auth.AuthorizerInterface
}

func TestGRPCServer_RegistrationAndGracefulStop(t *testing.T) {
	t.Parallel()

	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.Listen failed: %v", err)
	}
	defer lis.Close()

	s := grpc.NewServer(grpc.Creds(insecure.NewCredentials()))

	srv, err := server.New(server.Params{
		Config:     config.Config{},
		Authorizer: &dummyAuthorizer{},
		Storage:    &storage.MockStorage{},
	})
	if err != nil {
		t.Fatalf("server.New failed: %v", err)
	}

	apipb.RegisterXidServiceServer(s, srv)
	reflection.Register(s)
	healthServer := RegisterHealthServer(s)
	if healthServer == nil {
		t.Fatal("RegisterHealthServer returned nil")
	}

	serveErr := make(chan error, 1)
	go func() {
		serveErr <- s.Serve(lis)
	}()

	// Verify server accepted connection
	conn, err := grpc.NewClient(lis.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatalf("grpc.NewClient failed: %v", err)
	}
	defer conn.Close()

	// Stop gracefully
	s.GracefulStop()

	select {
	case err := <-serveErr:
		if err != nil && err != grpc.ErrServerStopped {
			t.Errorf("s.Serve returned unexpected error: %v", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("s.GracefulStop() timed out")
	}
}

func TestHealthMonitoring_Integration(t *testing.T) {
	t.Parallel()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	s := grpc.NewServer()
	healthServer := RegisterHealthServer(s)

	mockClient := &mockSpannerHealthClient{
		version: 1,
		dirty:   false,
	}

	go StartHealthMonitor(ctx, mockClient, healthServer)

	// Allow initial health check to process
	time.Sleep(100 * time.Millisecond)
	cancel()
}
