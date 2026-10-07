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

// Package jwtkeyset converts standard RSA keys and PEM blocks into Tink JWT keyset handles.
package jwtkeyset

import (
	"bytes"
	"crypto"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"math/big"

	"github.com/tink-crypto/tink-go/v2/insecurecleartextkeyset"
	_ "github.com/tink-crypto/tink-go/v2/jwt"
	"github.com/tink-crypto/tink-go/v2/keyset"
	jwt_rsa_ssa_pkcs1_go_proto "github.com/tink-crypto/tink-go/v2/proto/jwt_rsa_ssa_pkcs1_go_proto"
	tink_go_proto "github.com/tink-crypto/tink-go/v2/proto/tink_go_proto"
	"google.golang.org/protobuf/proto"
)

// Options configures JWT key conversion parameters.
type Options struct {
	RSAAlgorithm string
}

// PrivateKeyToKeysetHandle converts an *rsa.PrivateKey into a Tink JWT signing keyset handle.
func PrivateKeyToKeysetHandle(privKey *rsa.PrivateKey, keyID *uint32, options Options) (*keyset.Handle, error) {
	if privKey == nil {
		return nil, errors.New("nil private key")
	}
	if len(privKey.Primes) < 2 {
		return nil, errors.New("private key must have at least 2 primes")
	}

	var algo jwt_rsa_ssa_pkcs1_go_proto.JwtRsaSsaPkcs1Algorithm
	switch options.RSAAlgorithm {
	case "RS256":
		algo = jwt_rsa_ssa_pkcs1_go_proto.JwtRsaSsaPkcs1Algorithm_RS256
	case "RS384":
		algo = jwt_rsa_ssa_pkcs1_go_proto.JwtRsaSsaPkcs1Algorithm_RS384
	case "RS512":
		algo = jwt_rsa_ssa_pkcs1_go_proto.JwtRsaSsaPkcs1Algorithm_RS512
	default:
		return nil, fmt.Errorf("unsupported algorithm: %s", options.RSAAlgorithm)
	}

	publicKey := &jwt_rsa_ssa_pkcs1_go_proto.JwtRsaSsaPkcs1PublicKey{
		Version:   0,
		Algorithm: algo,
		N:         privKey.N.Bytes(),
		E:         big.NewInt(int64(privKey.E)).Bytes(),
	}

	// Ensure Precomputed values are populated
	privKey.Precompute()

	privateKey := &jwt_rsa_ssa_pkcs1_go_proto.JwtRsaSsaPkcs1PrivateKey{
		Version:   0,
		PublicKey: publicKey,
		D:         privKey.D.Bytes(),
		P:         privKey.Primes[0].Bytes(),
		Q:         privKey.Primes[1].Bytes(),
		Dp:        privKey.Precomputed.Dp.Bytes(),
		Dq:        privKey.Precomputed.Dq.Bytes(),
		Crt:       privKey.Precomputed.Qinv.Bytes(),
	}

	serializedPrivateKey, err := proto.Marshal(privateKey)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal private key: %w", err)
	}

	keyData := &tink_go_proto.KeyData{
		TypeUrl:         "type.googleapis.com/google.crypto.tink.JwtRsaSsaPkcs1PrivateKey",
		Value:           serializedPrivateKey,
		KeyMaterialType: tink_go_proto.KeyData_ASYMMETRIC_PRIVATE,
	}

	actualKeyID := uint32(1)
	if keyID != nil {
		actualKeyID = *keyID
	}

	key := &tink_go_proto.Keyset_Key{
		KeyData:          keyData,
		Status:           tink_go_proto.KeyStatusType_ENABLED,
		KeyId:            actualKeyID,
		OutputPrefixType: tink_go_proto.OutputPrefixType_RAW,
	}

	keysetMsg := &tink_go_proto.Keyset{
		PrimaryKeyId: actualKeyID,
		Key:          []*tink_go_proto.Keyset_Key{key},
	}

	serializedKeyset, err := proto.Marshal(keysetMsg)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal keyset: %w", err)
	}

	reader := keyset.NewBinaryReader(bytes.NewReader(serializedKeyset))
	handle, err := insecurecleartextkeyset.Read(reader)
	if err != nil {
		return nil, fmt.Errorf("failed to read keyset handle: %w", err)
	}

	return handle, nil
}

// PublicKeysToJwtPublicKeyset converts a slice of RSA public keys into a Tink JWT verification keyset handle.
func PublicKeysToJwtPublicKeyset(keys []crypto.PublicKey, options *Options) (*keyset.Handle, error) {
	if len(keys) == 0 {
		return nil, errors.New("empty keys list")
	}

	var algo jwt_rsa_ssa_pkcs1_go_proto.JwtRsaSsaPkcs1Algorithm
	if options != nil {
		switch options.RSAAlgorithm {
		case "RS256":
			algo = jwt_rsa_ssa_pkcs1_go_proto.JwtRsaSsaPkcs1Algorithm_RS256
		case "RS384":
			algo = jwt_rsa_ssa_pkcs1_go_proto.JwtRsaSsaPkcs1Algorithm_RS384
		case "RS512":
			algo = jwt_rsa_ssa_pkcs1_go_proto.JwtRsaSsaPkcs1Algorithm_RS512
		default:
			return nil, fmt.Errorf("unsupported algorithm: %s", options.RSAAlgorithm)
		}
	} else {
		algo = jwt_rsa_ssa_pkcs1_go_proto.JwtRsaSsaPkcs1Algorithm_RS256
	}

	var keysetKeys []*tink_go_proto.Keyset_Key
	for idx, key := range keys {
		rsaPubKey, ok := key.(*rsa.PublicKey)
		if !ok || rsaPubKey == nil {
			return nil, fmt.Errorf("key %d is not *rsa.PublicKey", idx)
		}

		publicKeyMsg := &jwt_rsa_ssa_pkcs1_go_proto.JwtRsaSsaPkcs1PublicKey{
			Version:   0,
			Algorithm: algo,
			N:         rsaPubKey.N.Bytes(),
			E:         big.NewInt(int64(rsaPubKey.E)).Bytes(),
		}

		serializedPublicKey, err := proto.Marshal(publicKeyMsg)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal public key %d: %w", idx, err)
		}

		keyData := &tink_go_proto.KeyData{
			TypeUrl:         "type.googleapis.com/google.crypto.tink.JwtRsaSsaPkcs1PublicKey",
			Value:           serializedPublicKey,
			KeyMaterialType: tink_go_proto.KeyData_ASYMMETRIC_PUBLIC,
		}

		keyID := uint32(idx + 1)
		keysetKey := &tink_go_proto.Keyset_Key{
			KeyData:          keyData,
			Status:           tink_go_proto.KeyStatusType_ENABLED,
			KeyId:            keyID,
			OutputPrefixType: tink_go_proto.OutputPrefixType_RAW,
		}
		keysetKeys = append(keysetKeys, keysetKey)
	}

	keysetMsg := &tink_go_proto.Keyset{
		PrimaryKeyId: uint32(len(keys)),
		Key:          keysetKeys,
	}

	serializedKeyset, err := proto.Marshal(keysetMsg)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal keyset: %w", err)
	}

	reader := keyset.NewBinaryReader(bytes.NewReader(serializedKeyset))
	handle, err := insecurecleartextkeyset.Read(reader)
	if err != nil {
		return nil, fmt.Errorf("failed to read keyset handle: %w", err)
	}

	return handle, nil
}

// FromPrivatePEM parses a PEM-encoded PKCS#8 or PKCS#1 RSA private key into a Tink keyset handle.
func FromPrivatePEM(pemData []byte, options Options) (*keyset.Handle, error) {
	block, _ := pem.Decode(pemData)
	if block == nil {
		return nil, errors.New("failed to decode PEM block")
	}

	var parsedKey any
	var err error
	if block.Type == "RSA PRIVATE KEY" {
		parsedKey, err = x509.ParsePKCS1PrivateKey(block.Bytes)
	} else {
		parsedKey, err = x509.ParsePKCS8PrivateKey(block.Bytes)
	}
	if err != nil {
		return nil, fmt.Errorf("failed to parse private key: %w", err)
	}

	rsaPrivKey, ok := parsedKey.(*rsa.PrivateKey)
	if !ok {
		return nil, errors.New("key is not an RSA private key")
	}
	return PrivateKeyToKeysetHandle(rsaPrivKey, nil, options)
}

// FromPublicPEM parses a PEM-encoded X.509 certificate or PKIX RSA public key into a Tink keyset handle.
func FromPublicPEM(pemData []byte, options *Options) (*keyset.Handle, error) {
	block, _ := pem.Decode(pemData)
	if block == nil {
		return nil, errors.New("failed to decode PEM block")
	}

	var pubKey any
	if block.Type == "CERTIFICATE" {
		cert, err := x509.ParseCertificate(block.Bytes)
		if err != nil {
			return nil, fmt.Errorf("failed to parse certificate: %w", err)
		}
		pubKey = cert.PublicKey
	} else {
		parsed, err := x509.ParsePKIXPublicKey(block.Bytes)
		if err != nil {
			return nil, fmt.Errorf("failed to parse public key: %w", err)
		}
		pubKey = parsed
	}

	return PublicKeysToJwtPublicKeyset([]crypto.PublicKey{pubKey}, options)
}
