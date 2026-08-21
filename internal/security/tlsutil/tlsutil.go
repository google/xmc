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

// Package tlsutil provides gRPC TLS credentials initialization utilities.
package tlsutil

import (
	"context"
	"crypto/tls"
	"fmt"
	"xmanager_cloud/internal/security/keyreloader"
	"xmanager_cloud/logging/logging"

	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/credentials/insecure"
)

// NewServerCredentials returns server transport credentials.
// It initializes a hot-reloading key reloader if certFile and keyFile are provided,
// falling back to insecure plaintext credentials if they are empty.
func NewServerCredentials(ctx context.Context, certFile, keyFile string) (credentials.TransportCredentials, error) {
	if (certFile != "") != (keyFile != "") {
		return nil, fmt.Errorf("both certFile and keyFile must be specified, or both must be empty")
	}
	if certFile != "" {
		reloader, err := keyreloader.NewKeyPairReloader(certFile, keyFile)
		if err != nil {
			return nil, fmt.Errorf("failed to load initial TLS certs: %w", err)
		}
		tlsConfig := &tls.Config{
			GetCertificate: reloader.GetCertificate,
		}
		return credentials.NewTLS(tlsConfig), nil
	}
	logging.Warn(ctx, "gRPC server is starting with insecure plaintext credentials")
	return insecure.NewCredentials(), nil
}

// NewClientCredentials returns client transport credentials.
// If certFile and keyFile are provided, it returns TLS credentials that skip verification
// (useful for proxying loopback traffic), falling back to insecure plaintext credentials if empty.
func NewClientCredentials(ctx context.Context, certFile, keyFile string) (credentials.TransportCredentials, error) {
	if (certFile != "") != (keyFile != "") {
		return nil, fmt.Errorf("both certFile and keyFile must be specified, or both must be empty")
	}
	if certFile != "" {
		return credentials.NewTLS(&tls.Config{InsecureSkipVerify: true}), nil
	}
	logging.Warn(ctx, "gRPC client transport is starting with insecure plaintext credentials")
	return insecure.NewCredentials(), nil
}
