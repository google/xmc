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
	"crypto/rand"
	"crypto/rsa"
	"math/big"
	"testing"

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

func TestPrivateKeyAndPublicKeys_RoundTrip(t *testing.T) {
	rsaKey := mustGenerateRSAKey(t)

	for _, alg := range []string{"RS256", "RS384", "RS512"} {
		t.Run(alg, func(t *testing.T) {
			privHandle, err := PrivateKeyToKeysetHandle(rsaKey, nil, Options{RSAAlgorithm: alg})
			if err != nil {
				t.Fatalf("PrivateKeyToKeysetHandle() error = %v", err)
			}
			pubHandle, err := PublicKeysToJwtPublicKeyset([]crypto.PublicKey{&rsaKey.PublicKey}, &Options{RSAAlgorithm: alg})
			if err != nil {
				t.Fatalf("PublicKeysToJwtPublicKeyset() error = %v", err)
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
			privKey: &rsa.PrivateKey{PublicKey: rsa.PublicKey{N: big.NewInt(15), E: 65537}, D: big.NewInt(3), Primes: []*big.Int{big.NewInt(3)}},
			opts:    Options{RSAAlgorithm: "RS256"},
		},
		{
			name:    "nil prime element",
			privKey: &rsa.PrivateKey{PublicKey: rsa.PublicKey{N: big.NewInt(15), E: 65537}, D: big.NewInt(3), Primes: []*big.Int{big.NewInt(3), nil}},
			opts:    Options{RSAAlgorithm: "RS256"},
		},
		{
			name:    "invalid precomputed values (non-coprime primes)",
			privKey: &rsa.PrivateKey{PublicKey: rsa.PublicKey{N: big.NewInt(9), E: 3}, D: big.NewInt(3), Primes: []*big.Int{big.NewInt(3), big.NewInt(3)}},
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
		{
			name: "RSA key with nil N",
			keys: []crypto.PublicKey{&rsa.PublicKey{N: nil, E: 65537}},
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
