// Copyright 2025 Google LLC
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
package keyreloader

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"os"
	"path/filepath"
	"testing"
	"time"

	"tink/go/insecuresecretdataaccess/insecuresecretdataaccess"
	"github.com/tink-crypto/tink-go/v2/jwt"
	"tink/integration/go/jwt/jwtkeyconverter"
)

func generateCertAndKey(certPath, keyPath string) error {
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return fmt.Errorf("failed to generate private key: %w", err)
	}

	template := x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject: pkix.Name{
			Organization: []string{"Acme Co"},
		},
		NotBefore: time.Now(),
		NotAfter:  time.Now().Add(time.Hour),

		KeyUsage:              x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
	}

	derBytes, err := x509.CreateCertificate(rand.Reader, &template, &template, &priv.PublicKey, priv)
	if err != nil {
		return fmt.Errorf("failed to create certificate: %w", err)
	}

	certOut, err := os.Create(certPath)
	if err != nil {
		return fmt.Errorf("failed to open %s for writing: %w", certPath, err)
	}
	if err := pem.Encode(certOut, &pem.Block{Type: "CERTIFICATE", Bytes: derBytes}); err != nil {
		return fmt.Errorf("failed to write data to %s: %w", certPath, err)
	}
	if err := certOut.Close(); err != nil {
		return fmt.Errorf("error closing %s: %w", certPath, err)
	}

	keyOut, err := os.OpenFile(keyPath, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0600)
	if err != nil {
		return fmt.Errorf("failed to open %s for writing: %w", keyPath, err)
	}
	privBytes, err := x509.MarshalPKCS8PrivateKey(priv)
	if err != nil {
		return fmt.Errorf("unable to marshal private key: %w", err)
	}
	if err := pem.Encode(keyOut, &pem.Block{Type: "PRIVATE KEY", Bytes: privBytes}); err != nil {
		return fmt.Errorf("failed to write data to %s: %w", keyPath, err)
	}
	if err := keyOut.Close(); err != nil {
		return fmt.Errorf("error closing %s: %w", keyPath, err)
	}
	return nil
}

func TestKeyPairReloader(t *testing.T) {
	dir := t.TempDir()
	certPath := filepath.Join(dir, "cert.pem")
	keyPath := filepath.Join(dir, "key.pem")

	if err := generateCertAndKey(certPath, keyPath); err != nil {
		t.Fatalf("Failed to generate cert and key: %v", err)
	}

	reloader, err := NewKeyPairReloader(certPath, keyPath)
	if err != nil {
		t.Fatalf("NewKeyPairReloader() failed: %v", err)
	}

	cert1, err := reloader.Certificate(nil)
	if err != nil {
		t.Fatalf("reloader.Certificate() failed: %v", err)
	}
	if cert1 == nil {
		t.Fatal("reloader.Certificate() returned nil certificate")
	}

	// Wait a bit to ensure file mod time is different
	time.Sleep(10 * time.Millisecond)

	// Overwrite certificate with new one
	if err := generateCertAndKey(certPath, keyPath); err != nil {
		t.Fatalf("Failed to generate new cert and key: %v", err)
	}

	cert2, err := reloader.Certificate(nil)
	if err != nil {
		t.Fatalf("reloader.Certificate() failed after file update: %v", err)
	}
	if cert2 == nil {
		t.Fatal("reloader.Certificate() returned nil certificate after file update")
	}

	if cert1 == cert2 {
		t.Errorf("Certificate was not reloaded after file update")
	}
}

func generatePKCS8Key(t *testing.T) []byte {
	t.Helper()
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("failed to generate private key: %v", err)
	}
	privBytes, err := x509.MarshalPKCS8PrivateKey(priv)
	if err != nil {
		t.Fatalf("unable to marshal private key: %v", err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: privBytes})
}

func generateCertAndSigner(t *testing.T, notBefore time.Time, notAfter time.Time) ([]byte, crypto.Signer) {
	t.Helper()
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("failed to generate private key: %v", err)
	}

	template := x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject: pkix.Name{
			Organization: []string{"Acme Co"},
		},
		NotBefore: notBefore,
		NotAfter:  notAfter,

		KeyUsage:              x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
	}

	derBytes, err := x509.CreateCertificate(rand.Reader, &template, &template, &priv.PublicKey, priv)
	if err != nil {
		t.Fatalf("failed to create certificate: %v", err)
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: derBytes})
	return certPEM, priv
}

func createJWTSigner(t *testing.T, priv crypto.Signer) jwt.Signer {
	t.Helper()
	rsaPrivateKey, ok := priv.(*rsa.PrivateKey)
	if !ok {
		t.Fatal("private key is not *rsa.PrivateKey")
	}
	options := jwtkeyconverter.Options{
		RSAAlgorithm: "RS256",
	}
	keysetHandle, err := jwtkeyconverter.PrivateKeyToKeysetHandle(
		rsaPrivateKey,
		nil,
		options,
		insecuresecretdataaccess.Token{},
	)
	if err != nil {
		t.Fatalf("failed to convert private key to keyset handle: %v", err)
	}
	signer, err := jwt.NewSigner(keysetHandle)
	if err != nil {
		t.Fatalf("failed to create jwt.Signer: %v", err)
	}
	return signer
}

func TestKeyReloader_Signer(t *testing.T) {
	dir := t.TempDir()
	keyPath := filepath.Join(dir, "key.txt")
	keyContent1 := generatePKCS8Key(t)
	keyContent2 := generatePKCS8Key(t)

	if err := os.WriteFile(keyPath, keyContent1, 0644); err != nil {
		t.Fatalf("Failed to write key file: %v", err)
	}

	reloader, err := NewSignerReloader(keyPath)
	if err != nil {
		t.Fatalf("NewSignerReloader() failed: %v", err)
	}

	s1, err := reloader.Signer()
	if err != nil {
		t.Fatalf("reloader.Signer() failed: %v", err)
	}
	if s1 == nil {
		t.Fatal("reloader.Signer() returned nil signer")
	}
	reloader.mu.RLock()
	lastMod1 := reloader.lastMod
	reloader.mu.RUnlock()

	// Wait a bit to ensure file mod time is different
	time.Sleep(10 * time.Millisecond)

	if err := os.WriteFile(keyPath, keyContent2, 0644); err != nil {
		t.Fatalf("Failed to write updated key file: %v", err)
	}

	s2, err := reloader.Signer()
	if err != nil {
		t.Fatalf("reloader.Signer() failed: %v", err)
	}
	if s2 == nil {
		t.Fatal("reloader.Signer() returned nil signer")
	}
	reloader.mu.RLock()
	lastMod2 := reloader.lastMod
	reloader.mu.RUnlock()

	rawJWT, err := jwt.NewRawJWT(&jwt.RawJWTOptions{WithoutExpiration: true})
	if err != nil {
		t.Fatalf("jwt.NewRawJWT() failed: %v", err)
	}
	token1, err := s1.SignAndEncode(rawJWT)
	if err != nil {
		t.Fatalf("s1.SignAndEncode() failed: %v", err)
	}
	token2, err := s2.SignAndEncode(rawJWT)
	if err != nil {
		t.Fatalf("s2.SignAndEncode() failed: %v", err)
	}

	if token1 == token2 {
		t.Errorf("Tokens are the same, expected different tokens after key reload")
	}

	if !lastMod2.After(lastMod1) {
		t.Errorf("Key was not reloaded after file update")
	}
}

func TestKeyReloader_Verifier(t *testing.T) {
	dir := t.TempDir()
	keyPath := filepath.Join(dir, "key.txt")
	now := time.Now()
	certContent1, signer1 := generateCertAndSigner(t, now.Add(-2*time.Minute), now.Add(3*time.Minute))
	certContent2, signer2 := generateCertAndSigner(t, now.Add(-1*time.Minute), now.Add(4*time.Minute))
	certContent3, signer3 := generateCertAndSigner(t, now, now.Add(5*time.Minute))

	if err := os.WriteFile(keyPath, certContent1, 0644); err != nil {
		t.Fatalf("Failed to write key file: %v", err)
	}

	reloader, err := NewVerifierReloader(keyPath, 2)
	if err != nil {
		t.Fatalf("NewVerifierReloader() failed: %v", err)
	}

	v1, err := reloader.Verifier()
	if err != nil {
		t.Fatalf("reloader.Verifier() failed: %v", err)
	}
	if v1 == nil {
		t.Fatal("reloader.Verifier() returned nil verifier")
	}
	reloader.mu.RLock()
	lastMod1 := reloader.lastMod
	reloader.mu.RUnlock()

	// Wait a bit to ensure file mod time is different
	time.Sleep(10 * time.Millisecond)

	if err := os.WriteFile(keyPath, certContent2, 0644); err != nil {
		t.Fatalf("Failed to write updated key file: %v", err)
	}

	v2, err := reloader.Verifier()
	if err != nil {
		t.Fatalf("reloader.Verifier() failed: %v", err)
	}
	if v2 == nil {
		t.Fatal("reloader.Verifier() returned nil verifier")
	}
	reloader.mu.RLock()
	lastMod2 := reloader.lastMod
	reloader.mu.RUnlock()

	jwtSigner1 := createJWTSigner(t, signer1)
	jwtSigner2 := createJWTSigner(t, signer2)
	jwtSigner3 := createJWTSigner(t, signer3)

	aud := "test"
	rawJWT, err := jwt.NewRawJWT(&jwt.RawJWTOptions{Audience: &aud, WithoutExpiration: true})
	if err != nil {
		t.Fatalf("jwt.NewRawJWT() failed: %v", err)
	}
	token1, err := jwtSigner1.SignAndEncode(rawJWT)
	if err != nil {
		t.Fatalf("jwtSigner1.SignAndEncode() failed: %v", err)
	}
	token2, err := jwtSigner2.SignAndEncode(rawJWT)
	if err != nil {
		t.Fatalf("jwtSigner2.SignAndEncode() failed: %v", err)
	}
	token3, err := jwtSigner3.SignAndEncode(rawJWT)
	if err != nil {
		t.Fatalf("jwtSigner3.SignAndEncode() failed: %v", err)
	}

	validator, err := jwt.NewValidator(&jwt.ValidatorOpts{ExpectedAudience: &aud, AllowMissingExpiration: true})
	if err != nil {
		t.Fatalf("jwt.NewValidator() failed: %v", err)
	}

	// Verify token1 with v1 (should pass)
	if _, err := v1.VerifyAndDecode(token1, validator); err != nil {
		t.Errorf("v1.VerifyAndDecode(token1) failed: %v", err)
	}
	// Verify token1 with v2 (should pass)
	// Keys rotated, v1 key is still valid
	if _, err := v2.VerifyAndDecode(token1, validator); err != nil {
		t.Errorf("v2.VerifyAndDecode(token1) unexpectedly failed: %v", err)
	}
	// Verify token2 with v2 (should pass)
	if _, err := v2.VerifyAndDecode(token2, validator); err != nil {
		t.Errorf("v2.VerifyAndDecode(token2) failed: %v", err)
	}
	// Verify token2 with v1 (should fail)
	if _, err := v1.VerifyAndDecode(token2, validator); err == nil {
		t.Error("v1.VerifyAndDecode(token2) unexpectedly succeeded")
	}

	if !lastMod2.After(lastMod1) {
		t.Errorf("Key was not reloaded after file update")
	}

	// Wait a bit to ensure file mod time is different
	time.Sleep(10 * time.Millisecond)

	if err := os.WriteFile(keyPath, certContent3, 0644); err != nil {
		t.Fatalf("Failed to write updated key file: %v", err)
	}

	v3, err := reloader.Verifier()
	if err != nil {
		t.Fatalf("reloader.Verifier() failed: %v", err)
	}
	if v3 == nil {
		t.Fatal("reloader.Verifier() returned nil verifier")
	}
	reloader.mu.RLock()
	lastMod3 := reloader.lastMod
	reloader.mu.RUnlock()

	// Verify token3 with v3 (should pass)
	if _, err := v3.VerifyAndDecode(token3, validator); err != nil {
		t.Errorf("v3.VerifyAndDecode(token3) failed: %v", err)
	}

	// Verify token2 with v3 (should pass)
	// v3 has the previous key, so it should pass
	if _, err := v3.VerifyAndDecode(token2, validator); err != nil {
		t.Errorf("v3.VerifyAndDecode(token2) failed: %v", err)
	}

	// Verify token1 with v3 (should fail)
	// token1 is signed with the first key, which is trimmed
	if _, err := v3.VerifyAndDecode(token1, validator); err == nil {
		t.Errorf("v3.VerifyAndDecode(token1) unexpectedly succeeded")
	}

	// Verify token3 with v2 (should fail)
	// v2 doesn't have the new key, so it should fail
	if _, err := v2.VerifyAndDecode(token3, validator); err == nil {
		t.Error("v2.VerifyAndDecode(token3) unexpectedly succeeded")
	}

	// Verify token3 with v1 (should fail)
	// v1 doesn't have the new key, so it should fail
	if _, err := v1.VerifyAndDecode(token3, validator); err == nil {
		t.Error("v1.VerifyAndDecode(token3) unexpectedly succeeded")
	}

	if !lastMod3.After(lastMod2) {
		t.Errorf("Key was not reloaded after file update")
	}
}
