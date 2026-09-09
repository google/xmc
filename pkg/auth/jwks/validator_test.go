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

package jwks

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

func generateTestRSAKey(t *testing.T) (*rsa.PrivateKey, JWK) {
	t.Helper()
	privKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("failed to generate RSA key: %v", err)
	}

	nBytes := privKey.PublicKey.N.Bytes()
	eBytes := []byte{1, 0, 1} // 65537

	jwk := JWK{
		Kty: "RSA",
		Alg: "RS256",
		Use: "sig",
		Kid: "test-key-id-1",
		N:   base64.RawURLEncoding.EncodeToString(nBytes),
		E:   base64.RawURLEncoding.EncodeToString(eBytes),
	}

	return privKey, jwk
}

func mintTestToken(t *testing.T, privKey *rsa.PrivateKey, kid string, claims *Claims) string {
	t.Helper()
	token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	if kid != "" {
		token.Header["kid"] = kid
	}
	signed, err := token.SignedString(privKey)
	if err != nil {
		t.Fatalf("failed to sign token: %v", err)
	}
	return signed
}

func TestJWKSValidator(t *testing.T) {
	ctx := context.Background()
	privKey, jwk := generateTestRSAKey(t)
	otherPrivKey, _ := generateTestRSAKey(t)

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(JWKS{
			Keys: []JWK{jwk},
		})
	}))
	defer server.Close()

	validator := NewValidator(ValidatorConfig{
		JWKSURL:                server.URL,
		ExpectedIssuer:         "https://issuer.example.com",
		ExpectedServiceAccount: "service-account@project.iam.gserviceaccount.com",
		HTTPClient:             server.Client(),
	})

	validClaims := func() *Claims {
		now := time.Now()
		return &Claims{
			Email: "service-account@project.iam.gserviceaccount.com",
			RegisteredClaims: jwt.RegisteredClaims{
				Issuer:    "https://issuer.example.com",
				Subject:   "service-account@project.iam.gserviceaccount.com",
				Audience:  jwt.ClaimStrings{"https://actuator.api.example.com/actuate"},
				ExpiresAt: jwt.NewNumericDate(now.Add(1 * time.Hour)),
				IssuedAt:  jwt.NewNumericDate(now),
				NotBefore: jwt.NewNumericDate(now),
			},
		}
	}

	tests := []struct {
		name        string
		token       func() string
		audience    string
		expectError error
	}{
		{
			name: "ValidToken",
			token: func() string {
				return mintTestToken(t, privKey, jwk.Kid, validClaims())
			},
			audience:    "https://actuator.api.example.com/actuate",
			expectError: nil,
		},
		{
			name: "EmptyToken",
			token: func() string {
				return ""
			},
			audience:    "https://actuator.api.example.com/actuate",
			expectError: ErrEmptyToken,
		},
		{
			name: "WhitespaceToken",
			token: func() string {
				return "   "
			},
			audience:    "https://actuator.api.example.com/actuate",
			expectError: ErrEmptyToken,
		},
		{
			name: "ExpiredToken",
			token: func() string {
				c := validClaims()
				c.ExpiresAt = jwt.NewNumericDate(time.Now().Add(-10 * time.Minute))
				return mintTestToken(t, privKey, jwk.Kid, c)
			},
			audience:    "https://actuator.api.example.com/actuate",
			expectError: ErrInvalidToken,
		},
		{
			name: "InvalidSignature",
			token: func() string {
				return mintTestToken(t, otherPrivKey, jwk.Kid, validClaims())
			},
			audience:    "https://actuator.api.example.com/actuate",
			expectError: ErrInvalidToken,
		},
		{
			name: "UnknownKeyID",
			token: func() string {
				return mintTestToken(t, privKey, "unknown-kid", validClaims())
			},
			audience:    "https://actuator.api.example.com/actuate",
			expectError: ErrInvalidToken,
		},
		{
			name: "AudienceMismatch",
			token: func() string {
				return mintTestToken(t, privKey, jwk.Kid, validClaims())
			},
			audience:    "https://different.audience.com",
			expectError: ErrAudienceMismatch,
		},
		{
			name: "IssuerMismatch",
			token: func() string {
				c := validClaims()
				c.Issuer = "https://wrong.issuer.com"
				return mintTestToken(t, privKey, jwk.Kid, c)
			},
			audience:    "https://actuator.api.example.com/actuate",
			expectError: ErrIssuerMismatch,
		},
		{
			name: "ServiceAccountMismatch",
			token: func() string {
				c := validClaims()
				c.Email = "attacker@other.com"
				c.Subject = "attacker@other.com"
				return mintTestToken(t, privKey, jwk.Kid, c)
			},
			audience:    "https://actuator.api.example.com/actuate",
			expectError: ErrServiceAccountMismatch,
		},
		{
			name: "MissingExpClaim",
			token: func() string {
				c := validClaims()
				c.ExpiresAt = nil
				return mintTestToken(t, privKey, jwk.Kid, c)
			},
			audience:    "https://actuator.api.example.com/actuate",
			expectError: ErrInvalidToken,
		},
		{
			name: "UnsupportedAlgorithmRS384",
			token: func() string {
				token := jwt.NewWithClaims(jwt.SigningMethodRS384, validClaims())
				token.Header["kid"] = jwk.Kid
				signed, err := token.SignedString(privKey)
				if err != nil {
					t.Fatalf("failed to sign: %v", err)
				}
				return signed
			},
			audience:    "https://actuator.api.example.com/actuate",
			expectError: ErrInvalidToken,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tokenStr := tc.token()
			err := validator.Validate(ctx, tokenStr, tc.audience)
			if tc.expectError != nil {
				if err == nil {
					t.Fatalf("expected error %v, got nil", tc.expectError)
				}
				if !errors.Is(err, tc.expectError) && !strings.Contains(err.Error(), tc.expectError.Error()) {
					t.Fatalf("expected error containing %v, got %v", tc.expectError, err)
				}
			} else if err != nil {
				t.Fatalf("expected no error, got: %v", err)
			}
		})
	}

	t.Run("ValidateClaimsReturnsParsedClaims", func(t *testing.T) {
		tokenStr := mintTestToken(t, privKey, jwk.Kid, validClaims())
		claims, err := validator.ValidateClaims(ctx, tokenStr, "https://actuator.api.example.com/actuate")
		if err != nil {
			t.Fatalf("expected no error, got: %v", err)
		}
		if claims == nil || claims.Email != "service-account@project.iam.gserviceaccount.com" {
			t.Fatalf("unexpected claims: %+v", claims)
		}
	})

	t.Run("NoAudiencesConfiguredFailsClosed", func(t *testing.T) {
		valNoAud := NewValidator(ValidatorConfig{
			JWKSURL:    server.URL,
			HTTPClient: server.Client(),
		})
		tokenStr := mintTestToken(t, privKey, jwk.Kid, validClaims())
		err := valNoAud.Validate(ctx, tokenStr, "")
		if !errors.Is(err, ErrAudienceMismatch) {
			t.Fatalf("expected ErrAudienceMismatch, got %v", err)
		}
	})
}

func TestDecodeJWKToRSAPublicKeyErrors(t *testing.T) {
	t.Run("UnsupportedKeyType", func(t *testing.T) {
		_, err := DecodeJWKToRSAPublicKey(JWK{Kty: "EC"})
		if !errors.Is(err, ErrUnsupportedKeyType) {
			t.Errorf("expected ErrUnsupportedKeyType, got %v", err)
		}
	})

	t.Run("InvalidModulusBase64", func(t *testing.T) {
		_, err := DecodeJWKToRSAPublicKey(JWK{Kty: "RSA", N: "??invalid??", E: "AQAB"})
		if err == nil {
			t.Errorf("expected error for invalid modulus base64, got nil")
		}
	})

	t.Run("EmptyModulus", func(t *testing.T) {
		_, err := DecodeJWKToRSAPublicKey(JWK{Kty: "RSA", N: "", E: "AQAB"})
		if err == nil || !strings.Contains(err.Error(), "invalid public modulus") {
			t.Errorf("expected invalid public modulus error, got %v", err)
		}
	})

	t.Run("InvalidExponentBase64", func(t *testing.T) {
		_, err := DecodeJWKToRSAPublicKey(JWK{Kty: "RSA", N: "AQAB", E: "??invalid??"})
		if err == nil {
			t.Errorf("expected error for invalid exponent base64, got nil")
		}
	})

	t.Run("ExponentTooLarge", func(t *testing.T) {
		_, err := DecodeJWKToRSAPublicKey(JWK{Kty: "RSA", N: "AQAB", E: base64.RawURLEncoding.EncodeToString([]byte{1, 2, 3, 4, 5})})
		if err == nil || !strings.Contains(err.Error(), "exponent too large") {
			t.Errorf("expected exponent too large error, got %v", err)
		}
	})

	t.Run("ExponentLessThan3", func(t *testing.T) {
		_, err := DecodeJWKToRSAPublicKey(JWK{Kty: "RSA", N: "AQAB", E: base64.RawURLEncoding.EncodeToString([]byte{1})})
		if err == nil || !strings.Contains(err.Error(), "invalid public exponent: must be >= 3") {
			t.Errorf("expected invalid public exponent error, got %v", err)
		}
		_, err2 := DecodeJWKToRSAPublicKey(JWK{Kty: "RSA", N: "AQAB", E: base64.RawURLEncoding.EncodeToString([]byte{2})})
		if err2 == nil || !strings.Contains(err2.Error(), "invalid public exponent: must be >= 3") {
			t.Errorf("expected invalid public exponent error for exp=2, got %v", err2)
		}
	})
}

func TestJWKSCachedFallbackOnRefreshError(t *testing.T) {
	ctx := context.Background()
	privKey, jwk := generateTestRSAKey(t)

	failRequests := false
	fetchCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fetchCount++
		if failRequests {
			http.Error(w, "internal server error", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(JWKS{
			Keys: []JWK{jwk},
		})
	}))
	defer server.Close()

	validator := NewValidator(ValidatorConfig{
		JWKSURL:          server.URL,
		AllowedAudiences: []string{"https://actuator.api.example.com/actuate"},
		HTTPClient:       server.Client(),
		CacheTTL:         100 * time.Millisecond,
	})

	tokenStr := mintTestToken(t, privKey, jwk.Kid, &Claims{
		RegisteredClaims: jwt.RegisteredClaims{
			Audience:  jwt.ClaimStrings{"https://actuator.api.example.com/actuate"},
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(1 * time.Hour)),
		},
	})

	// Initial successful fetch.
	if err := validator.Validate(ctx, tokenStr, "https://actuator.api.example.com/actuate"); err != nil {
		t.Fatalf("expected initial validation to succeed, got %v", err)
	}
	if fetchCount != 1 {
		t.Fatalf("expected 1 fetch, got %d", fetchCount)
	}

	// Wait for cache TTL to expire.
	time.Sleep(150 * time.Millisecond)

	// Now fail backend requests.
	failRequests = true

	// Should fallback to cached key and apply cooldown.
	if err := validator.Validate(ctx, tokenStr, "https://actuator.api.example.com/actuate"); err != nil {
		t.Fatalf("expected cached fallback to succeed, got %v", err)
	}
	if fetchCount != 2 {
		t.Fatalf("expected 2 fetches, got %d", fetchCount)
	}

	// Immediate next request should hit cooldown without making another HTTP request.
	if err := validator.Validate(ctx, tokenStr, "https://actuator.api.example.com/actuate"); err != nil {
		t.Fatalf("expected validation during cooldown to succeed, got %v", err)
	}
	if fetchCount != 2 {
		t.Fatalf("expected still 2 fetches due to cooldown, got %d", fetchCount)
	}
}

func TestJWKSMissingKeyCooldownAndSingleflight(t *testing.T) {
	ctx := context.Background()
	privKey, jwk := generateTestRSAKey(t)

	var fetchCount int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&fetchCount, 1)
		time.Sleep(30 * time.Millisecond)
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(JWKS{
			Keys: []JWK{jwk},
		})
	}))
	defer server.Close()

	validator := NewValidator(ValidatorConfig{
		JWKSURL:            server.URL,
		AllowedAudiences:   []string{"https://actuator.api.example.com/actuate"},
		HTTPClient:         server.Client(),
		CacheTTL:           1 * time.Hour,
		MinRefreshInterval: 500 * time.Millisecond,
	})

	testClaims := &Claims{
		Email: "user@example.com",
		RegisteredClaims: jwt.RegisteredClaims{
			Audience:  jwt.ClaimStrings{"https://actuator.api.example.com/actuate"},
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
		},
	}

	// Concurrent requests with non-existent kid should collapse via singleflight into 1 fetch.
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			tokenStr := mintTestToken(t, privKey, fmt.Sprintf("unknown-kid-%d", idx), testClaims)
			_ = validator.Validate(ctx, tokenStr, "https://actuator.api.example.com/actuate")
		}(i)
	}
	wg.Wait()

	if count := atomic.LoadInt32(&fetchCount); count != 1 {
		t.Fatalf("expected 1 fetch due to singleflight deduplication, got %d", count)
	}

	// Immediate next request for another unknown kid should fail fast due to MinRefreshInterval cooldown.
	tokenStr := mintTestToken(t, privKey, "another-unknown-kid", testClaims)
	err := validator.Validate(ctx, tokenStr, "https://actuator.api.example.com/actuate")
	if err == nil || !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("expected ErrInvalidToken for unknown kid, got %v", err)
	}
	if count := atomic.LoadInt32(&fetchCount); count != 1 {
		t.Fatalf("expected fetchCount to remain 1 due to missing key cooldown, got %d", count)
	}
}

func TestJWKSSingleflightContextCancellation(t *testing.T) {
	privKey, jwk := generateTestRSAKey(t)

	var fetchCount int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&fetchCount, 1)
		time.Sleep(50 * time.Millisecond) // Simulate slow network request
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(JWKS{
			Keys: []JWK{jwk},
		})
	}))
	defer server.Close()

	validator := NewValidator(ValidatorConfig{
		JWKSURL:          server.URL,
		AllowedAudiences: []string{"https://actuator.api.example.com/actuate"},
		HTTPClient:       server.Client(),
		CacheTTL:         1 * time.Hour,
	})

	testClaims := &Claims{
		Email: "user@example.com",
		RegisteredClaims: jwt.RegisteredClaims{
			Audience:  jwt.ClaimStrings{"https://actuator.api.example.com/actuate"},
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
		},
	}
	tokenStr := mintTestToken(t, privKey, jwk.Kid, testClaims)

	// Caller 1 starts fetch with a context that cancels after 10ms.
	ctx1, cancel1 := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel1()

	// Caller 2 starts concurrently with a generous context.
	ctx2 := context.Background()

	var err2 error
	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()
		_ = validator.Validate(ctx1, tokenStr, "https://actuator.api.example.com/actuate")
	}()

	go func() {
		defer wg.Done()
		time.Sleep(5 * time.Millisecond) // Ensure caller 1 starts first
		err2 = validator.Validate(ctx2, tokenStr, "https://actuator.api.example.com/actuate")
	}()

	wg.Wait()

	// Caller 2 must succeed despite Caller 1's context cancellation.
	if err2 != nil {
		t.Fatalf("expected caller 2 to succeed despite caller 1 cancellation, got %v", err2)
	}
	if count := atomic.LoadInt32(&fetchCount); count != 1 {
		t.Fatalf("expected 1 fetch, got %d", count)
	}
}

func TestJWKSCooldownOnFailedRefreshWithLargeCacheTTL(t *testing.T) {
	ctx := context.Background()
	privKey, _ := generateTestRSAKey(t)

	var fetchCount int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&fetchCount, 1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer server.Close()

	validator := NewValidator(ValidatorConfig{
		JWKSURL:            server.URL,
		AllowedAudiences:   []string{"https://actuator.api.example.com/actuate"},
		HTTPClient:         server.Client(),
		CacheTTL:           1 * time.Hour,
		MinRefreshInterval: 200 * time.Millisecond,
	})

	testClaims := &Claims{
		Email: "user@example.com",
		RegisteredClaims: jwt.RegisteredClaims{
			Audience:  jwt.ClaimStrings{"https://actuator.api.example.com/actuate"},
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
		},
	}
	tokenStr := mintTestToken(t, privKey, "unknown-kid", testClaims)

	// First attempt fails against backend.
	err1 := validator.Validate(ctx, tokenStr, "https://actuator.api.example.com/actuate")
	if err1 == nil {
		t.Fatal("expected error on 500 server response")
	}
	if count := atomic.LoadInt32(&fetchCount); count != 1 {
		t.Fatalf("expected 1 fetch, got %d", count)
	}

	// Immediate next attempt should fail fast via cooldown without querying the backend again.
	err2 := validator.Validate(ctx, tokenStr, "https://actuator.api.example.com/actuate")
	if err2 == nil || !errors.Is(err2, ErrInvalidToken) {
		t.Fatalf("expected ErrInvalidToken on cooldown, got %v", err2)
	}
	if count := atomic.LoadInt32(&fetchCount); count != 1 {
		t.Fatalf("expected fetchCount to remain 1 due to failure cooldown, got %d", count)
	}
}

func TestJWKSPreflightContextCancellation(t *testing.T) {
	privKey, jwk := generateTestRSAKey(t)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(JWKS{
			Keys: []JWK{jwk},
		})
	}))
	defer server.Close()

	validator := NewValidator(ValidatorConfig{
		JWKSURL:    server.URL,
		HTTPClient: server.Client(),
	})

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Pre-canceled context

	testClaims := &Claims{
		Email: "user@example.com",
		RegisteredClaims: jwt.RegisteredClaims{
			Audience:  jwt.ClaimStrings{"https://actuator.api.example.com/actuate"},
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
		},
	}
	tokenStr := mintTestToken(t, privKey, jwk.Kid, testClaims)

	err := validator.Validate(ctx, tokenStr, "https://actuator.api.example.com/actuate")
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
}
