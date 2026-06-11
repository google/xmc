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
// Package keyreloader provides a reloader for private, and public keys.
// This is used to implement hot-reload for the gRPC server's TLS credentials,
// and for keys used for signing, and validating other operations.
package keyreloader

import (
	"context"
	"crypto"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"os"
	"sync"
	"time"

	"github.com/tink-crypto/tink-go/v2/jwt"
	"tink/go/insecuresecretdataaccess/insecuresecretdataaccess"
	"tink/integration/go/jwt/jwtkeyconverter"
	"xmanager_cloud/logging/logging"
)

// KeyPairReloader manages a cached certificate and reloads it when the file changes.
type KeyPairReloader struct {
	certPath   string
	keyPath    string
	cachedCert *tls.Certificate
	lastMod    time.Time
	mu         sync.RWMutex
}

// NewKeyPairReloader creates a new reloader.
func NewKeyPairReloader(certPath, keyPath string) (*KeyPairReloader, error) {
	reloader := &KeyPairReloader{
		certPath: certPath,
		keyPath:  keyPath,
	}
	// Attempt initial load
	if err := reloader.reload(); err != nil {
		return nil, err
	}
	return reloader, nil
}

// reload checks if the file has changed and reloads the cert if needed.
func (k *KeyPairReloader) reload() error {
	certInfo, err := os.Stat(k.certPath)
	if err != nil {
		return err
	}
	keyInfo, err := os.Stat(k.keyPath)
	if err != nil {
		return err
	}

	latestModTime := certInfo.ModTime()
	if keyInfo.ModTime().After(latestModTime) {
		latestModTime = keyInfo.ModTime()
	}

	k.mu.RLock()
	needsReload := latestModTime.After(k.lastMod)
	k.mu.RUnlock()

	if !needsReload {
		return nil
	}

	// 3. Load the new keypair
	newCert, err := tls.LoadX509KeyPair(k.certPath, k.keyPath)
	if err != nil {
		return err
	}

	// 4. Update the cache (thread-safe)
	k.mu.Lock()
	defer k.mu.Unlock()
	// Re-check latestModTime against k.lastMod because another goroutine
	// might have reloaded the certificate since we released the read lock.
	if latestModTime.After(k.lastMod) {
		k.cachedCert = &newCert
		k.lastMod = latestModTime
	}

	return nil
}

// Certificate is the callback we pass to tls.Config
func (k *KeyPairReloader) Certificate(hello *tls.ClientHelloInfo) (*tls.Certificate, error) {
	err := k.reload()
	if err != nil {
		ctx := context.Background()
		logging.Warn(ctx, "Failed to reload keypair", "error", err)
	}

	k.mu.RLock()
	defer k.mu.RUnlock()
	return k.cachedCert, nil
}

// KeyReloader manages a cached key and reloads it when the file changes.
type KeyReloader[T any, K any] struct {
	keyPath   string
	cachedKey T
	// converter is a function that converts a PEM block, and the recent keys to a key of type T.
	// The recentKeys are used for key rotation.
	// The function returns the new validator or signer, and the recent keys, with the new key
	// appended to the end, to be used for the next key rotation.
	converter     func(*pem.Block, []K, int) (T, []K, error)
	recentKeys    []K
	maxRecentKeys int // The maximum number of recent keys to store.
	lastMod       time.Time
	mu            sync.RWMutex
}

// SignerReloader is a KeyReloader for jwt.Signer.
type SignerReloader struct {
	// The SignerReloader doesn't need to maintain a history of recent keys,
	// so `any` is used as the type for the (unused) recent key list in the
	// underlying KeyReloader.
	*KeyReloader[jwt.Signer, any]
}

// VerifierReloader is a KeyReloader for jwt.Verifier.
type VerifierReloader struct {
	*KeyReloader[jwt.Verifier, x509.Certificate]
}

// NewSignerReloader creates a new Signer reloader with the key in a keyPath.
func NewSignerReloader(keyPath string) (*SignerReloader, error) {
	reloader := &KeyReloader[jwt.Signer, any]{
		keyPath:   keyPath,
		converter: convertToSigner,
	}
	// Attempt initial load
	if _, err := reloader.reload(); err != nil {
		return nil, err
	}
	return &SignerReloader{reloader}, nil
}

// NewVerifierReloader creates a new Verifier reloader with the key in a keyPath.
func NewVerifierReloader(keyPath string, maxRecentKeys int) (*VerifierReloader, error) {
	reloader := &KeyReloader[jwt.Verifier, x509.Certificate]{
		keyPath:       keyPath,
		converter:     convertToVerifier,
		maxRecentKeys: maxRecentKeys,
	}
	// Attempt initial load
	if _, err := reloader.reload(); err != nil {
		return nil, err
	}
	return &VerifierReloader{reloader}, nil
}

// reload returns the cached key, reloading it from the key file if the file has
// been modified.
func (k *KeyReloader[T, K]) reload() (T, error) {
	keyInfo, err := os.Stat(k.keyPath)
	if err != nil {
		var zero T
		return zero, err
	}

	latestModTime := keyInfo.ModTime()

	k.mu.RLock()
	needsReload := latestModTime.After(k.lastMod)
	k.mu.RUnlock()

	if needsReload {
		// Load the new key
		keyData, err := os.ReadFile(k.keyPath)
		if err != nil {
			ctx := context.Background()
			logging.Warn(ctx, "Failed to reload key", "error", err)
			var zero T
			return zero, err
		}

		block, _ := pem.Decode(keyData)
		if block == nil {
			err := errors.New("failed to decode PEM block from key file")
			ctx := context.Background()
			logging.Warn(ctx, "Failed to decode PEM block from key file", "error", err)
			var zero T
			return zero, err
		}

		k.mu.RLock()
		// Create a copy of recentKeys to pass to the converter,
		// ensuring the slice isn't modified while being read.
		keysToConvert := make([]K, len(k.recentKeys))
		copy(keysToConvert, k.recentKeys)
		k.mu.RUnlock()

		key, newRecentKeys, err := k.converter(block, keysToConvert, k.maxRecentKeys)
		if err != nil {
			var zero T
			return zero, err
		}

		// Update the cache (thread-safe)
		k.mu.Lock()
		// Re-check latestModTime against k.lastMod because another goroutine
		// might have reloaded the key since we released the read lock.
		if latestModTime.After(k.lastMod) {
			k.cachedKey = key
			k.recentKeys = newRecentKeys
			k.lastMod = latestModTime
		}
		k.mu.Unlock()
	}

	k.mu.RLock()
	defer k.mu.RUnlock()
	key := k.cachedKey
	// Check if the interface value is nil. This works for jwt.Signer and jwt.Verifier.
	if any(key) == nil {
		var zero T
		return zero, errors.New("invalid cached key")
	}
	return key, nil
}

// convertToSigner converts a PEM block to a jwt.Signer.
// The second and third parameters are unused, and nil is returned as the
// second return value because the signer only needs to use the current key to
// sign tokens. This contrasts with the verifier, which needs to retain old
// keys for verification during key rotation.
func convertToSigner(block *pem.Block, _ []any, _ int) (jwt.Signer, []any, error) {
	key, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, nil, errors.New("failed to parse cached signing key")
	}

	// The key must be an RSA private key for JWT signing.
	rsaPrivateKey, ok := key.(*rsa.PrivateKey)
	if !ok {
		return nil, nil, errors.New("signing key is not an RSA private key")
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
		return nil, nil, fmt.Errorf("failed to convert private key to keyset handle: %w", err)
	}

	signer, err := jwt.NewSigner(keysetHandle)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create jwt.Signer: %w", err)
	}

	return signer, nil, nil
}

// convertToVerifier converts a PEM block to a jwt.Verifier.
func convertToVerifier(block *pem.Block, recentCerts []x509.Certificate, maxRecentKeys int) (jwt.Verifier, []x509.Certificate, error) {
	cert, err := x509.ParseCertificate(block.Bytes)
	if err != nil {
		return nil, nil, errors.New("failed to parse cached validation key")
	}

	combinedCerts := append(recentCerts, *cert)
	newRecentCerts := validCerts(combinedCerts)

	if len(newRecentCerts) > maxRecentKeys {
		newRecentCerts = newRecentCerts[len(newRecentCerts)-maxRecentKeys:]
	}

	publicKeysetHandle, err := jwtkeyconverter.PublicKeysToJwtPublicKeyset(
		publicKeys(newRecentCerts), &jwtkeyconverter.Options{RSAAlgorithm: "RS256"})
	if err != nil {
		return nil, nil, fmt.Errorf("failed to convert public key to JWT public keyset: %w", err)
	}

	verifier, err := jwt.NewVerifier(publicKeysetHandle)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to create JWT verifier: %w", err)
	}

	return verifier, newRecentCerts, nil
}

func validCerts(certs []x509.Certificate) []x509.Certificate {
	var newCerts []x509.Certificate
	now := time.Now()
	for _, cert := range certs {
		if now.After(cert.NotBefore) && now.Before(cert.NotAfter) {
			newCerts = append(newCerts, cert)
		}
	}
	return newCerts
}

func publicKeys(certs []x509.Certificate) []crypto.PublicKey {
	keys := make([]crypto.PublicKey, len(certs))
	for i, cert := range certs {
		keys[i] = cert.PublicKey
	}
	return keys
}

// Signer returns a JWT signer for the cached key, reloading it if it has changed.
func (k *SignerReloader) Signer() (jwt.Signer, error) {
	return k.reload()
}

// Verifier returns a JWT verifier for the cached key, reloading it if it has changed.
func (k *VerifierReloader) Verifier() (jwt.Verifier, error) {
	return k.reload()
}
