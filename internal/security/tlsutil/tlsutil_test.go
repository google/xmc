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

package tlsutil

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func generateTestCertAndKey(t *testing.T, dir string) (string, string) {
	t.Helper()
	certPath := filepath.Join(dir, "cert.pem")
	keyPath := filepath.Join(dir, "key.pem")

	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("failed to generate private key: %v", err)
	}

	template := x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject: pkix.Name{
			Organization: []string{"Test Org"},
		},
		NotBefore:             time.Now(),
		NotAfter:              time.Now().Add(time.Hour),
		KeyUsage:              x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
	}

	derBytes, err := x509.CreateCertificate(rand.Reader, &template, &template, &priv.PublicKey, priv)
	if err != nil {
		t.Fatalf("failed to create certificate: %v", err)
	}

	certOut, err := os.Create(certPath)
	if err != nil {
		t.Fatalf("failed to create cert file: %v", err)
	}
	if err := pem.Encode(certOut, &pem.Block{Type: "CERTIFICATE", Bytes: derBytes}); err != nil {
		t.Fatalf("failed to encode cert: %v", err)
	}
	_ = certOut.Close()

	keyOut, err := os.OpenFile(keyPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0600)
	if err != nil {
		t.Fatalf("failed to create key file: %v", err)
	}
	privBytes, err := x509.MarshalPKCS8PrivateKey(priv)
	if err != nil {
		t.Fatalf("failed to marshal private key: %v", err)
	}
	if err := pem.Encode(keyOut, &pem.Block{Type: "PRIVATE KEY", Bytes: privBytes}); err != nil {
		t.Fatalf("failed to encode private key: %v", err)
	}
	_ = keyOut.Close()

	return certPath, keyPath
}

func TestNewServerCredentials(t *testing.T) {
	dir := t.TempDir()
	certPath, keyPath := generateTestCertAndKey(t, dir)

	tests := []struct {
		name      string
		ctx       context.Context
		certFile  string
		keyFile   string
		wantErr   bool
		wantCreds bool
	}{
		{
			name:      "both empty falls back to plaintext insecure credentials",
			ctx:       t.Context(),
			certFile:  "",
			keyFile:   "",
			wantErr:   false,
			wantCreds: true,
		},
		{
			name:      "nil context defaults to background context safely without panic",
			ctx:       nil,
			certFile:  "",
			keyFile:   "",
			wantErr:   false,
			wantCreds: true,
		},
		{
			name:      "only certFile specified returns error",
			ctx:       t.Context(),
			certFile:  certPath,
			keyFile:   "",
			wantErr:   true,
			wantCreds: false,
		},
		{
			name:      "only keyFile specified returns error",
			ctx:       t.Context(),
			certFile:  "",
			keyFile:   keyPath,
			wantErr:   true,
			wantCreds: false,
		},
		{
			name:      "valid cert and key files return TLS credentials",
			ctx:       t.Context(),
			certFile:  certPath,
			keyFile:   keyPath,
			wantErr:   false,
			wantCreds: true,
		},
		{
			name:      "non-existent cert files return error",
			ctx:       t.Context(),
			certFile:  filepath.Join(dir, "missing_cert.pem"),
			keyFile:   filepath.Join(dir, "missing_key.pem"),
			wantErr:   true,
			wantCreds: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			creds, err := NewServerCredentials(tt.ctx, tt.certFile, tt.keyFile)
			if (err != nil) != tt.wantErr {
				t.Fatalf("NewServerCredentials() error = %v, wantErr %v", err, tt.wantErr)
			}
			if (creds != nil) != tt.wantCreds {
				t.Fatalf("NewServerCredentials() creds = %v, wantCreds %v", creds, tt.wantCreds)
			}
		})
	}
}

func TestNewClientCredentials(t *testing.T) {
	dir := t.TempDir()
	certPath, keyPath := generateTestCertAndKey(t, dir)

	tests := []struct {
		name      string
		ctx       context.Context
		certFile  string
		keyFile   string
		wantErr   bool
		wantCreds bool
	}{
		{
			name:      "both empty falls back to plaintext insecure credentials",
			ctx:       t.Context(),
			certFile:  "",
			keyFile:   "",
			wantErr:   false,
			wantCreds: true,
		},
		{
			name:      "nil context defaults to background context safely without panic",
			ctx:       nil,
			certFile:  "",
			keyFile:   "",
			wantErr:   false,
			wantCreds: true,
		},
		{
			name:      "only certFile specified returns error",
			ctx:       t.Context(),
			certFile:  certPath,
			keyFile:   "",
			wantErr:   true,
			wantCreds: false,
		},
		{
			name:      "only keyFile specified returns error",
			ctx:       t.Context(),
			certFile:  "",
			keyFile:   keyPath,
			wantErr:   true,
			wantCreds: false,
		},
		{
			name:      "valid cert and key files return TLS credentials",
			ctx:       t.Context(),
			certFile:  certPath,
			keyFile:   keyPath,
			wantErr:   false,
			wantCreds: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			creds, err := NewClientCredentials(tt.ctx, tt.certFile, tt.keyFile)
			if (err != nil) != tt.wantErr {
				t.Fatalf("NewClientCredentials() error = %v, wantErr %v", err, tt.wantErr)
			}
			if (creds != nil) != tt.wantCreds {
				t.Fatalf("NewClientCredentials() creds = %v, wantCreds %v", creds, tt.wantCreds)
			}
		})
	}
}
