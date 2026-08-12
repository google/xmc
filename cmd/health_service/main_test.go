// Copyright 2026 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//      http://www.apache.org/licenses/LICENSE-2.0
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
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"golang/github_com/grpc_ecosystem/grpc_gateway/v/v2/runtime/runtime"
	"golang/grpc/credentials/insecure/insecure"
	"golang/grpc/grpc"
	gwpb "xmanager_cloud/health_service/proto/api_go_grpc_gateway_proto"
	spb "xmanager_cloud/health_service/proto/api_go_grpc_proto"
	"xmanager_cloud/pkg/auth/oauth2"
	"xmanager_cloud/pkg/auth/oauth2/mock"
)

func TestRequireAdminRoleMiddleware(t *testing.T) {
	// 1. Setup mock oauth2 Config
	oauthCfg := &oauth2.Config{
		MockAuth:              false,
		DisableAuthentication: false,
		AllowedDomains:        []string{"mycompany.com"},
		GCPProjectID:          "test-project-id",
		TargetIAMRole:         "roles/viewer",
		AdminIAMRole:          "roles/owner",
		JWTSecret:             "default-jwt-secret-for-local-dev-only-12345",
		SecureCookie:          false,
	}

	authenticator := oauth2.NewAuthenticator(oauthCfg)
	mock.ConfigureMockAuth(authenticator)

	// 2. Setup mock gRPC server
	// Spin up a real local gRPC server with mock server implementation to serve gRPC-Gateway requests.
	lis, err := netListen()
	if err != nil {
		t.Fatalf("Failed to listen: %v", err)
	}
	defer lis.Close()

	checker := &mockHealthChecker{}
	cfg := &Config{ConfigDir: t.TempDir()}
	grpcServer := grpc.NewServer(grpc.Creds(insecure.NewCredentials()))
	spb.RegisterHealthServiceServer(grpcServer, newServer(cfg, checker, authenticator, &mockAuthorizer{}))
	go func() {
		_ = grpcServer.Serve(lis)
	}()
	defer grpcServer.GracefulStop()

	// 3. Setup gRPC Gateway
	ctx := context.Background()
	gwmux := runtime.NewServeMux(runtime.WithIncomingHeaderMatcher(oauth2.IncomingHeaderMatcher))
	dialOpts := []grpc.DialOption{
		grpc.WithTransportCredentials(insecure.NewCredentials()),
	}
	err = gwpb.RegisterHealthServiceHandlerFromEndpoint(ctx, gwmux, lis.Addr().String(), dialOpts)
	if err != nil {
		t.Fatalf("Failed to register gateway: %v", err)
	}

	// Wrap with Authenticate
	authenticatedGwmux := authenticator.Authenticate(gwmux)

	// Setup Server Mux
	mux := http.NewServeMux()
	mux.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte("OK"))
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" {
			w.WriteHeader(http.StatusOK)
			w.Write([]byte("OK"))
			return
		}
		authenticatedGwmux.ServeHTTP(w, r)
	})

	testServer := httptest.NewServer(mux)
	defer testServer.Close()

	// Helper to generate a valid signed mock session token
	generateToken := func(email string) string {
		tokenString, _ := oauth2.GenerateSessionToken(email, "Test User", "", 1*time.Hour, []byte(oauthCfg.JWTSecret))
		return tokenString
	}

	tests := []struct {
		name           string
		path           string
		token          string
		expectedStatus int
	}{
		{
			name:           "healthz bypasses auth",
			path:           "/healthz",
			token:          "",
			expectedStatus: http.StatusOK,
		},
		{
			name:           "unauthenticated request to health status is 401",
			path:           "/v1/health/status",
			token:          "",
			expectedStatus: http.StatusUnauthorized,
		},
		{
			name:           "viewer (non-admin) request to health status is 403",
			path:           "/v1/health/status",
			token:          generateToken("mock-sa-user@mycompany.com"), // viewer role in mock PolicyFetcher
			expectedStatus: http.StatusForbidden,
		},
		{
			name:           "admin request to health status is 200",
			path:           "/v1/health/status",
			token:          generateToken("mock-admin@mycompany.com"), // owner role in mock PolicyFetcher
			expectedStatus: http.StatusOK,
		},
		{
			name:           "unauthenticated request to health configs is 401",
			path:           "/v1/health/configs",
			token:          "",
			expectedStatus: http.StatusUnauthorized,
		},
		{
			name:           "viewer (non-admin) request to health configs is 403",
			path:           "/v1/health/configs",
			token:          generateToken("mock-sa-user@mycompany.com"),
			expectedStatus: http.StatusForbidden,
		},
		{
			name:           "admin request to health configs is 200",
			path:           "/v1/health/configs",
			token:          generateToken("mock-admin@mycompany.com"),
			expectedStatus: http.StatusOK,
		},
		{
			name:           "viewer request to userinfo is 200",
			path:           "/v1/health/userinfo",
			token:          generateToken("mock-sa-user@mycompany.com"),
			expectedStatus: http.StatusOK,
		},
		{
			name:           "admin request to userinfo is 200",
			path:           "/v1/health/userinfo",
			token:          generateToken("mock-admin@mycompany.com"),
			expectedStatus: http.StatusOK,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			req, _ := http.NewRequest("GET", testServer.URL+tt.path, nil)
			if tt.token != "" {
				req.Header.Set("Authorization", "Bearer "+tt.token)
			}
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatalf("Request failed: %v", err)
			}
			defer resp.Body.Close()

			if resp.StatusCode != tt.expectedStatus {
				t.Errorf("Expected status %d, got %d", tt.expectedStatus, resp.StatusCode)
			}
		})
	}
}

// netListen is a helper to get a free local port for the gRPC server.
func netListen() (net.Listener, error) {
	return net.Listen("tcp", "127.0.0.1:0")
}
