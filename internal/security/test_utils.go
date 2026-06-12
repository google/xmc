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
// Package securitytest provides test utilities for security.
package securitytest

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"fmt"
	"testing"

	"github.com/tink-crypto/tink-go/v2/jwt"
	"tink/go/insecuresecretdataaccess/insecuresecretdataaccess"
	"tink/integration/go/jwt/jwtkeyconverter"
)

var (
	testPrivKey *rsa.PrivateKey
	testPubKey  *rsa.PublicKey
)

func init() {
	var err error
	testPrivKey, err = rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		panic(fmt.Sprintf("Failed to generate RSA key pair: %v", err))
	}
	testPubKey = &testPrivKey.PublicKey
}

// MustCreateTestSigner creates a test signing key.
func MustCreateTestSigner(t *testing.T) func() (jwt.Signer, error) {
	t.Helper()

	options := jwtkeyconverter.Options{
		RSAAlgorithm: "RS256",
	}

	keysetHandle, err := jwtkeyconverter.PrivateKeyToKeysetHandle(
		testPrivKey,
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

	return func() (jwt.Signer, error) {
		return signer, nil
	}
}

// MustCreateTestVerifier creates a test verifier.
func MustCreateTestVerifier(t *testing.T) func() (jwt.Verifier, error) {
	t.Helper()
	publicKeysetHandle, err := jwtkeyconverter.PublicKeysToJwtPublicKeyset(
		[]crypto.PublicKey{testPubKey}, &jwtkeyconverter.Options{RSAAlgorithm: "RS256"})
	if err != nil {
		t.Fatalf("Failed to convert public key to JWT public keyset: %v", err)
	}

	verifier, err := jwt.NewVerifier(publicKeysetHandle)
	if err != nil {
		t.Fatalf("Failed to create JWT verifier: %v", err)
	}

	return func() (jwt.Verifier, error) {
		return verifier, nil
	}
}
