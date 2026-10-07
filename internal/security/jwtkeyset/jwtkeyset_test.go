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

package jwtkeyset

import (
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"testing"
	"time"

	"github.com/tink-crypto/tink-go/v2/jwt"
)

func mustGenerateRSAKey(t *testing.T) *rsa.PrivateKey {
	t.Helper()
	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("rsa.GenerateKey() failed: %v", err)
	}
	return priv
}

func mustEncodePKCS8PrivatePEM(t *testing.T, key any) []byte {
	t.Helper()
	der, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatalf("x509.MarshalPKCS8PrivateKey() failed: %v", err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der})
}

func mustEncodePKIXPublicPEM(t *testing.T, pub any) []byte {
	t.Helper()
	der, err := x509.MarshalPKIXPublicKey(pub)
	if err != nil {
		t.Fatalf("x509.MarshalPKIXPublicKey() failed: %v", err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der})
}

func mustEncodeCertPEM(t *testing.T, priv *rsa.PrivateKey) []byte {
	t.Helper()
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "test"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &priv.PublicKey, priv)
	if err != nil {
		t.Fatalf("x509.CreateCertificate() failed: %v", err)
	}
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
}

func TestFromPrivatePEM_And_FromPublicPEM_RoundTrip(t *testing.T) {
	rsaKey := mustGenerateRSAKey(t)
	pkcs8PEM := mustEncodePKCS8PrivatePEM(t, rsaKey)
	pkcs1PEM := pem.EncodeToMemory(&pem.Block{
		Type:  "RSA PRIVATE KEY",
		Bytes: x509.MarshalPKCS1PrivateKey(rsaKey),
	})
	pubPEM := mustEncodePKIXPublicPEM(t, &rsaKey.PublicKey)
	pkcs1PubPEM := pem.EncodeToMemory(&pem.Block{
		Type:  "RSA PUBLIC KEY",
		Bytes: x509.MarshalPKCS1PublicKey(&rsaKey.PublicKey),
	})
	certPEM := mustEncodeCertPEM(t, rsaKey)

	tests := []struct {
		name    string
		privPEM []byte
		pubPEM  []byte
		alg     string
	}{
		{
			name:    "PKCS8 and PKIX RS256",
			privPEM: pkcs8PEM,
			pubPEM:  pubPEM,
			alg:     "RS256",
		},
		{
			name:    "PKCS1 and PKCS1 Public RS256",
			privPEM: pkcs1PEM,
			pubPEM:  pkcs1PubPEM,
			alg:     "RS256",
		},
		{
			name:    "PKCS1 and Certificate RS384",
			privPEM: pkcs1PEM,
			pubPEM:  certPEM,
			alg:     "RS384",
		},
		{
			name:    "PKCS8 and Certificate RS512",
			privPEM: pkcs8PEM,
			pubPEM:  certPEM,
			alg:     "RS512",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			privHandle, err := FromPrivatePEM(tt.privPEM, Options{RSAAlgorithm: tt.alg})
			if err != nil {
				t.Fatalf("FromPrivatePEM() error = %v", err)
			}
			pubHandle, err := FromPublicPEM(tt.pubPEM, &Options{RSAAlgorithm: tt.alg})
			if err != nil {
				t.Fatalf("FromPublicPEM() error = %v", err)
			}

			signer, err := jwt.NewSigner(privHandle)
			if err != nil {
				t.Fatalf("jwt.NewSigner() error = %v", err)
			}
			verifier, err := jwt.NewVerifier(pubHandle)
			if err != nil {
				t.Fatalf("jwt.NewVerifier() error = %v", err)
			}

			aud := "test-audience"
			subject := "user@example.com"
			rawJWT, err := jwt.NewRawJWT(&jwt.RawJWTOptions{
				Audience:          &aud,
				Subject:           &subject,
				WithoutExpiration: true,
			})
			if err != nil {
				t.Fatalf("jwt.NewRawJWT() error = %v", err)
			}
			compact, err := signer.SignAndEncode(rawJWT)
			if err != nil {
				t.Fatalf("signer.SignAndEncode() error = %v", err)
			}

			validator, err := jwt.NewValidator(&jwt.ValidatorOpts{
				ExpectedAudience:       &aud,
				AllowMissingExpiration: true,
			})
			if err != nil {
				t.Fatalf("jwt.NewValidator() error = %v", err)
			}
			verifiedJWT, err := verifier.VerifyAndDecode(compact, validator)
			if err != nil {
				t.Fatalf("verifier.VerifyAndDecode() error = %v", err)
			}
			gotSub, err := verifiedJWT.Subject()
			if err != nil || gotSub != subject {
				t.Errorf("verifiedJWT.Subject() = %q, %v; want %q, nil", gotSub, err, subject)
			}
		})
	}
}

func TestFromPrivatePEM_Errors(t *testing.T) {
	ecKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("ecdsa.GenerateKey() failed: %v", err)
	}
	ecPEM := mustEncodePKCS8PrivatePEM(t, ecKey)

	tests := []struct {
		name    string
		pemData []byte
		opts    Options
	}{
		{
			name:    "invalid PEM",
			pemData: []byte("not-a-pem"),
			opts:    Options{RSAAlgorithm: "RS256"},
		},
		{
			name:    "corrupted PKCS8 bytes",
			pemData: pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: []byte("bad")}),
			opts:    Options{RSAAlgorithm: "RS256"},
		},
		{
			name:    "non-RSA private key",
			pemData: ecPEM,
			opts:    Options{RSAAlgorithm: "RS256"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := FromPrivatePEM(tt.pemData, tt.opts); err == nil {
				t.Errorf("FromPrivatePEM() succeeded, want error")
			}
		})
	}
}

func TestFromPublicPEM_Errors(t *testing.T) {
	ecKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("ecdsa.GenerateKey() failed: %v", err)
	}
	ecPubPEM := mustEncodePKIXPublicPEM(t, &ecKey.PublicKey)

	tests := []struct {
		name    string
		pemData []byte
		opts    *Options
	}{
		{
			name:    "invalid PEM",
			pemData: []byte("not-a-pem"),
			opts:    nil,
		},
		{
			name:    "corrupted certificate bytes",
			pemData: pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: []byte("bad")}),
			opts:    nil,
		},
		{
			name:    "corrupted RSA public key bytes",
			pemData: pem.EncodeToMemory(&pem.Block{Type: "RSA PUBLIC KEY", Bytes: []byte("bad")}),
			opts:    nil,
		},
		{
			name:    "corrupted public key bytes",
			pemData: pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: []byte("bad")}),
			opts:    nil,
		},
		{
			name:    "non-RSA public key",
			pemData: ecPubPEM,
			opts:    nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := FromPublicPEM(tt.pemData, tt.opts); err == nil {
				t.Errorf("FromPublicPEM() succeeded, want error")
			}
		})
	}
}

func TestPrivateKeyToKeysetHandle_EdgeCases(t *testing.T) {
	validKey := mustGenerateRSAKey(t)
	customKeyID := uint32(7)

	if _, err := PrivateKeyToKeysetHandle(validKey, &customKeyID, Options{RSAAlgorithm: "RS256"}); err != nil {
		t.Fatalf("PrivateKeyToKeysetHandle() with custom keyID failed: %v", err)
	}

	tests := []struct {
		name    string
		privKey *rsa.PrivateKey
		opts    Options
	}{
		{
			name:    "nil private key",
			privKey: nil,
			opts:    Options{RSAAlgorithm: "RS256"},
		},
		{
			name:    "fewer than 2 primes",
			privKey: &rsa.PrivateKey{Primes: []*big.Int{big.NewInt(3)}},
			opts:    Options{RSAAlgorithm: "RS256"},
		},
		{
			name:    "unsupported algorithm",
			privKey: validKey,
			opts:    Options{RSAAlgorithm: "ES256"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := PrivateKeyToKeysetHandle(tt.privKey, nil, tt.opts); err == nil {
				t.Errorf("PrivateKeyToKeysetHandle() succeeded, want error")
			}
		})
	}
}

func TestPublicKeysToJwtPublicKeyset_EdgeCases(t *testing.T) {
	validKey := mustGenerateRSAKey(t)

	// Default options (nil) should default to RS256.
	if _, err := PublicKeysToJwtPublicKeyset([]crypto.PublicKey{&validKey.PublicKey}, nil); err != nil {
		t.Fatalf("PublicKeysToJwtPublicKeyset() with nil options failed: %v", err)
	}

	tests := []struct {
		name string
		keys []crypto.PublicKey
		opts *Options
	}{
		{
			name: "empty keys slice",
			keys: nil,
			opts: nil,
		},
		{
			name: "unsupported algorithm",
			keys: []crypto.PublicKey{&validKey.PublicKey},
			opts: &Options{RSAAlgorithm: "PS256"},
		},
		{
			name: "non-RSA key in slice",
			keys: []crypto.PublicKey{"not-an-rsa-key"},
			opts: nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := PublicKeysToJwtPublicKeyset(tt.keys, tt.opts); err == nil {
				t.Errorf("PublicKeysToJwtPublicKeyset() succeeded, want error")
			}
		})
	}
}
