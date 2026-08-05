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

package ingestiongateway

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/twmb/franz-go/pkg/kgo"
)

type certPaths struct {
	caCertFile     string
	caKeyFile      string
	serverCertFile string
	serverKeyFile  string
	clientCertFile string
	clientKeyFile  string
}

func generateCertificates(t *testing.T, dir string) *certPaths {
	t.Helper()

	// 1. Generate CA Certificate
	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("failed to generate CA key: %v", err)
	}

	caTemplate := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject: pkix.Name{
			CommonName: "Test CA",
		},
		NotBefore:             time.Now().Add(-1 * time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		IsCA:                  true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageCRLSign,
		BasicConstraintsValid: true,
	}

	caCertBytes, err := x509.CreateCertificate(rand.Reader, caTemplate, caTemplate, &caKey.PublicKey, caKey)
	if err != nil {
		t.Fatalf("failed to create CA cert: %v", err)
	}

	caCertPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caCertBytes})
	caKeyBytes, err := x509.MarshalECPrivateKey(caKey)
	if err != nil {
		t.Fatalf("failed to marshal CA key: %v", err)
	}
	caKeyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: caKeyBytes})

	caCertFile := filepath.Join(dir, "ca.crt")
	caKeyFile := filepath.Join(dir, "ca.key")
	if err := os.WriteFile(caCertFile, caCertPEM, 0600); err != nil {
		t.Fatalf("failed to write CA cert: %v", err)
	}
	if err := os.WriteFile(caKeyFile, caKeyPEM, 0600); err != nil {
		t.Fatalf("failed to write CA key: %v", err)
	}

	// 2. Generate Server Certificate
	serverKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("failed to generate server key: %v", err)
	}

	serverTemplate := &x509.Certificate{
		SerialNumber: big.NewInt(2),
		Subject: pkix.Name{
			CommonName: "localhost",
		},
		IPAddresses: []net.IP{net.ParseIP("127.0.0.1")},
		DNSNames:    []string{"localhost"},
		NotBefore:   time.Now().Add(-1 * time.Hour),
		NotAfter:    time.Now().Add(24 * time.Hour),
		KeyUsage:    x509.KeyUsageDigitalSignature,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}

	serverCertBytes, err := x509.CreateCertificate(rand.Reader, serverTemplate, caTemplate, &serverKey.PublicKey, caKey)
	if err != nil {
		t.Fatalf("failed to create server cert: %v", err)
	}

	serverCertPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: serverCertBytes})
	serverKeyBytes, err := x509.MarshalECPrivateKey(serverKey)
	if err != nil {
		t.Fatalf("failed to marshal server key: %v", err)
	}
	serverKeyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: serverKeyBytes})

	serverCertFile := filepath.Join(dir, "server.crt")
	serverKeyFile := filepath.Join(dir, "server.key")
	if err := os.WriteFile(serverCertFile, serverCertPEM, 0600); err != nil {
		t.Fatalf("failed to write server cert: %v", err)
	}
	if err := os.WriteFile(serverKeyFile, serverKeyPEM, 0600); err != nil {
		t.Fatalf("failed to write server key: %v", err)
	}

	// 3. Generate Client Certificate
	clientCertFile, clientKeyFile := generateClientCert(t, dir, caTemplate, caKey, "Test Client v1", "client.crt", "client.key")

	return &certPaths{
		caCertFile:     caCertFile,
		caKeyFile:      caKeyFile,
		serverCertFile: serverCertFile,
		serverKeyFile:  serverKeyFile,
		clientCertFile: clientCertFile,
		clientKeyFile:  clientKeyFile,
	}
}

func generateClientCert(t *testing.T, dir string, caTemplate *x509.Certificate, caKey *ecdsa.PrivateKey, commonName, certName, keyName string) (string, string) {
	t.Helper()
	clientKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("failed to generate client key: %v", err)
	}

	clientTemplate := &x509.Certificate{
		SerialNumber: big.NewInt(time.Now().UnixNano()),
		Subject: pkix.Name{
			CommonName: commonName,
		},
		NotBefore:   time.Now().Add(-1 * time.Hour),
		NotAfter:    time.Now().Add(24 * time.Hour),
		KeyUsage:    x509.KeyUsageDigitalSignature,
		ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth},
	}

	clientCertBytes, err := x509.CreateCertificate(rand.Reader, clientTemplate, caTemplate, &clientKey.PublicKey, caKey)
	if err != nil {
		t.Fatalf("failed to create client cert: %v", err)
	}

	clientCertPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: clientCertBytes})
	clientKeyBytes, err := x509.MarshalECPrivateKey(clientKey)
	if err != nil {
		t.Fatalf("failed to marshal client key: %v", err)
	}
	clientKeyPEM := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: clientKeyBytes})

	certPath := filepath.Join(dir, certName)
	keyPath := filepath.Join(dir, keyName)
	if err := os.WriteFile(certPath, clientCertPEM, 0600); err != nil {
		t.Fatalf("failed to write client cert: %v", err)
	}
	if err := os.WriteFile(keyPath, clientKeyPEM, 0600); err != nil {
		t.Fatalf("failed to write client key: %v", err)
	}

	return certPath, keyPath
}

func TestBuildTLSConfig_Disabled(t *testing.T) {
	cfg := &Config{TLSEnabled: false}
	tlsCfg, reloader, err := BuildTLSConfig(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if tlsCfg != nil || reloader != nil {
		t.Fatalf("expected nil tlsCfg and reloader when TLSEnabled is false")
	}
}

func TestBuildTLSConfig_BasicTLS(t *testing.T) {
	tempDir := t.TempDir()
	certs := generateCertificates(t, tempDir)

	cfg := &Config{
		TLSEnabled:            true,
		TLSCaFile:             certs.caCertFile,
		TLSInsecureSkipVerify: true,
	}

	tlsCfg, reloader, err := BuildTLSConfig(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if tlsCfg == nil {
		t.Fatalf("expected non-nil tls.Config")
	}
	if reloader != nil {
		t.Fatalf("expected nil reloader when cert/key files are omitted")
	}
	if !tlsCfg.InsecureSkipVerify {
		t.Errorf("expected InsecureSkipVerify to be true")
	}
	if tlsCfg.RootCAs == nil {
		t.Errorf("expected RootCAs to be populated")
	}
}

func TestBuildTLSConfig_mTLS(t *testing.T) {
	tempDir := t.TempDir()
	certs := generateCertificates(t, tempDir)

	cfg := &Config{
		TLSEnabled:  true,
		TLSCaFile:   certs.caCertFile,
		TLSCertFile: certs.clientCertFile,
		TLSKeyFile:  certs.clientKeyFile,
	}

	tlsCfg, reloader, err := BuildTLSConfig(cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if tlsCfg == nil || reloader == nil {
		t.Fatalf("expected non-nil tlsCfg and reloader")
	}
	if tlsCfg.GetClientCertificate == nil {
		t.Fatalf("expected GetClientCertificate callback to be set")
	}

	cert, err := tlsCfg.GetClientCertificate(nil)
	if err != nil {
		t.Fatalf("failed to get client certificate via callback: %v", err)
	}
	if cert == nil || len(cert.Certificate) == 0 {
		t.Fatalf("returned certificate is empty")
	}
}

func TestBuildTLSConfig_InvalidFiles(t *testing.T) {
	cfg := &Config{
		TLSEnabled: true,
		TLSCaFile:  "/nonexistent/ca.crt",
	}
	_, _, err := BuildTLSConfig(cfg)
	if err == nil {
		t.Fatalf("expected error for nonexistent CA file")
	}

	cfg = &Config{
		TLSEnabled:  true,
		TLSCertFile: "/nonexistent/client.crt",
		TLSKeyFile:  "/nonexistent/client.key",
	}
	_, _, err = BuildTLSConfig(cfg)
	if err == nil {
		t.Fatalf("expected error for nonexistent client cert/key files")
	}
}

func TestBuildProducerOptions_WithTLSAndSASL(t *testing.T) {
	tempDir := t.TempDir()
	certs := generateCertificates(t, tempDir)

	cfg := &Config{
		RedpandaBrokers:       []string{"localhost:9092"},
		SASLEnabled:           true,
		SASLMechanism:         "SCRAM-SHA-256",
		SASLUser:              "user",
		SASLPassword:          "pass",
		TLSEnabled:            true,
		TLSCaFile:             certs.caCertFile,
		TLSCertFile:           certs.clientCertFile,
		TLSKeyFile:            certs.clientKeyFile,
		TLSInsecureSkipVerify: true,
	}

	opts, err := BuildProducerOptions(cfg, nil)
	if err != nil {
		t.Fatalf("failed to build producer options: %v", err)
	}
	if len(opts) == 0 {
		t.Fatalf("expected non-empty options")
	}

	client, err := kgo.NewClient(opts...)
	if err != nil {
		t.Fatalf("failed to create kgo client with TLS and SASL options: %v", err)
	}
	client.Close()
}

func TestDynamicCertReloading_mTLSHandshake(t *testing.T) {
	tempDir := t.TempDir()
	certs := generateCertificates(t, tempDir)

	// Set up TLS server demanding client certificate verification
	caCertBytes, err := os.ReadFile(certs.caCertFile)
	if err != nil {
		t.Fatalf("failed to read CA file: %v", err)
	}
	clientCertPool := x509.NewCertPool()
	clientCertPool.AppendCertsFromPEM(caCertBytes)

	serverCert, err := tls.LoadX509KeyPair(certs.serverCertFile, certs.serverKeyFile)
	if err != nil {
		t.Fatalf("failed to load server keypair: %v", err)
	}

	serverTLSConfig := &tls.Config{
		Certificates: []tls.Certificate{serverCert},
		ClientAuth:   tls.RequireAndVerifyClientCert,
		ClientCAs:    clientCertPool,
	}

	listener, err := tls.Listen("tcp", "127.0.0.1:0", serverTLSConfig)
	if err != nil {
		t.Fatalf("failed to start TLS listener: %v", err)
	}
	defer listener.Close()

	// Server goroutine accepting connections
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				tlsConn, ok := c.(*tls.Conn)
				if !ok {
					return
				}
				if err := tlsConn.Handshake(); err != nil {
					return
				}
				state := tlsConn.ConnectionState()
				if len(state.PeerCertificates) > 0 {
					c.Write([]byte(state.PeerCertificates[0].Subject.CommonName))
				} else {
					c.Write([]byte("NO_CERT"))
				}
			}(conn)
		}
	}()

	// Build client configuration using gateway tls.go BuildTLSConfig
	clientCfg := &Config{
		TLSEnabled:            true,
		TLSCaFile:             certs.caCertFile,
		TLSCertFile:           certs.clientCertFile,
		TLSKeyFile:            certs.clientKeyFile,
		TLSInsecureSkipVerify: true,
	}

	clientTLSConfig, _, err := BuildTLSConfig(clientCfg)
	if err != nil {
		t.Fatalf("failed to build client TLS config: %v", err)
	}

	// 1. Perform initial mTLS handshake
	conn1, err := tls.Dial("tcp", listener.Addr().String(), clientTLSConfig)
	if err != nil {
		t.Fatalf("initial mTLS handshake failed: %v", err)
	}
	buf1 := make([]byte, 64)
	n1, err := conn1.Read(buf1)
	if err != nil {
		t.Fatalf("failed to read from conn1: %v", err)
	}
	resp1 := string(buf1[:n1])
	if resp1 != "Test Client v1" {
		t.Fatalf("expected 'Test Client v1', got %q", resp1)
	}

	// 2. Rotate client cert on disk with new Certificate Common Name
	caKeyBytes, err := os.ReadFile(certs.caKeyFile)
	if err != nil {
		t.Fatalf("failed to read CA key: %v", err)
	}
	caBlock, _ := pem.Decode(caKeyBytes)
	caKey, err := x509.ParseECPrivateKey(caBlock.Bytes)
	if err != nil {
		t.Fatalf("failed to parse CA key: %v", err)
	}

	caCertBlock, _ := pem.Decode(caCertBytes)
	caTemplate, err := x509.ParseCertificate(caCertBlock.Bytes)
	if err != nil {
		t.Fatalf("failed to parse CA cert: %v", err)
	}

	// Update cert file on disk
	time.Sleep(10 * time.Millisecond) // Ensure file mtime changes
	generateClientCert(t, tempDir, caTemplate, caKey, "Test Client v2 Rotated", "client.crt", "client.key")

	// 3. Keep conn1 open (active connections are not dropped)
	if _, err := conn1.Write([]byte("ping")); err != nil {
		t.Fatalf("existing active connection dropped after file rotation: %v", err)
	}
	conn1.Close()

	// 4. Perform second mTLS handshake (must dynamically reload new cert from disk without restart)
	conn2, err := tls.Dial("tcp", listener.Addr().String(), clientTLSConfig)
	if err != nil {
		t.Fatalf("mTLS handshake after cert rotation failed: %v", err)
	}
	defer conn2.Close()
	buf2 := make([]byte, 64)
	n2, err := conn2.Read(buf2)
	if err != nil {
		t.Fatalf("failed to read from conn2: %v", err)
	}
	resp2 := string(buf2[:n2])
	if resp2 != "Test Client v2 Rotated" {
		t.Fatalf("expected 'Test Client v2 Rotated', got %q", resp2)
	}
}
