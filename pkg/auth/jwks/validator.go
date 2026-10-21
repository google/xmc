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
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"golang.org/x/sync/singleflight"
	"xmanager_cloud/internal/logging"
)

// GoogleOAuth2CertsURL is the standard public Google OAuth2 / OIDC JWKS certificate endpoint.
const GoogleOAuth2CertsURL = "https://www.googleapis.com/oauth2/v3/certs"

var (
	ErrEmptyToken             = errors.New("auth token is empty")
	ErrInvalidToken           = errors.New("invalid auth token")
	ErrKeyNotFound            = errors.New("signing key not found in JWKS")
	ErrUnsupportedKeyType     = errors.New("unsupported key type in JWKS")
	ErrAudienceMismatch       = errors.New("token audience mismatch")
	ErrIssuerMismatch         = errors.New("token issuer mismatch")
	ErrServiceAccountMismatch = errors.New("token service account mismatch")
)

type (
	// JWK represents a single JSON Web Key in a JWKS key set.
	JWK struct{ Kty, Alg, Use, Kid, N, E string }

	// JWKS represents a JSON Web Key Set structure.
	JWKS struct {
		Keys []JWK `json:"keys"`
	}

	// ValidatorConfig holds configuration for the JWKS token validator.
	ValidatorConfig struct {
		JWKSURL, ExpectedIssuer, ExpectedServiceAccount string
		AllowedAudiences                                []string
		AllowedServiceAccounts                          []string
		HTTPClient                                      *http.Client
		// CacheTTL specifies how long successfully fetched JWKS keys are cached (default: 1 hour).
		CacheTTL time.Duration
		// MinRefreshInterval specifies the minimum cooldown interval between outbound
		// refresh attempts for unknown key IDs (kid) or following failed refreshes,
		// mitigating cache stampedes and key flooding attacks (default: 1 minute).
		MinRefreshInterval time.Duration
	}

	// Validator validates OIDC tokens against a standard JWKS endpoint.
	Validator struct {
		cfg         ValidatorConfig
		httpClient  *http.Client
		keys        map[string]*rsa.PublicKey
		lastFetch   time.Time
		lastAttempt time.Time
		mu          sync.RWMutex
		sfGroup     singleflight.Group
	}
)

// NewValidator creates a new JWKS Validator.
func NewValidator(cfg ValidatorConfig) *Validator {
	if cfg.HTTPClient == nil {
		cfg.HTTPClient = &http.Client{Timeout: 10 * time.Second}
	}
	if cfg.CacheTTL == 0 {
		cfg.CacheTTL = 1 * time.Hour
	}
	if cfg.MinRefreshInterval == 0 {
		cfg.MinRefreshInterval = 1 * time.Minute
	}
	return &Validator{
		cfg:        cfg,
		httpClient: cfg.HTTPClient,
		keys:       make(map[string]*rsa.PublicKey),
	}
}

// DecodeJWKToRSAPublicKey converts an RSA JWK into an *rsa.PublicKey.
func DecodeJWKToRSAPublicKey(key JWK) (*rsa.PublicKey, error) {
	if key.Kty != "RSA" {
		return nil, fmt.Errorf("%w: %s", ErrUnsupportedKeyType, key.Kty)
	}
	nBytes, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(key.N, "="))
	if err != nil {
		return nil, fmt.Errorf("failed to decode modulus: %w", err)
	}
	eBytes, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(key.E, "="))
	if err != nil {
		return nil, fmt.Errorf("failed to decode exponent: %w", err)
	}
	if len(eBytes) > 4 {
		return nil, fmt.Errorf("exponent too large: %d bytes", len(eBytes))
	}
	exp := int(new(big.Int).SetBytes(eBytes).Int64())
	n := new(big.Int).SetBytes(nBytes)
	if n.Sign() <= 0 {
		return nil, fmt.Errorf("invalid public modulus")
	}
	if exp < 3 {
		return nil, fmt.Errorf("invalid public exponent: must be >= 3")
	}
	return &rsa.PublicKey{N: n, E: exp}, nil
}

func (v *Validator) fetchKeys(ctx context.Context) (map[string]*rsa.PublicKey, error) {
	if v.cfg.JWKSURL == "" {
		return nil, fmt.Errorf("JWKS URL is not configured")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, v.cfg.JWKSURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create JWKS request: %w", err)
	}
	resp, err := v.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch JWKS from %s: %w", v.cfg.JWKSURL, err)
	}
	defer resp.Body.Close()
	limitReader := io.LimitReader(resp.Body, 1024*1024)
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(limitReader)
		return nil, fmt.Errorf("JWKS request failed with status %d: %s", resp.StatusCode, string(body))
	}
	var keySet JWKS
	if err := json.NewDecoder(limitReader).Decode(&keySet); err != nil {
		return nil, fmt.Errorf("failed to decode JWKS response: %w", err)
	}
	newKeys := make(map[string]*rsa.PublicKey, len(keySet.Keys))
	for _, k := range keySet.Keys {
		if pubKey, err := DecodeJWKToRSAPublicKey(k); err == nil {
			newKeys[k.Kid] = pubKey
		} else {
			logging.Warn(ctx, "Skipping unsupported JWK key", "kid", k.Kid, "error", err)
		}
	}
	return newKeys, nil
}

func (v *Validator) refreshKeysSingleflight(ctx context.Context) (map[string]*rsa.PublicKey, error) {
	res, err, _ := v.sfGroup.Do("fetchKeys", func() (any, error) {
		fetchTimeout := v.httpClient.Timeout
		if fetchTimeout <= 0 {
			fetchTimeout = 15 * time.Second
		}
		fetchCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), fetchTimeout)
		defer cancel()

		newKeys, fetchErr := v.fetchKeys(fetchCtx)
		v.mu.Lock()
		v.lastAttempt = time.Now()
		if fetchErr == nil {
			v.keys = newKeys
			v.lastFetch = time.Now()
		}
		v.mu.Unlock()
		if fetchErr != nil {
			return nil, fetchErr
		}
		return newKeys, nil
	})
	if err != nil {
		return nil, err
	}
	newKeys, ok := res.(map[string]*rsa.PublicKey)
	if !ok {
		return nil, errors.New("unexpected key map type from singleflight fetch")
	}
	return newKeys, nil
}

func (v *Validator) getKey(ctx context.Context, kid string) (*rsa.PublicKey, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	v.mu.RLock()
	key, exists := v.findKeyLocked(kid)
	lastFetch := v.lastFetch
	lastAttempt := v.lastAttempt
	v.mu.RUnlock()

	minInterval := min(v.cfg.MinRefreshInterval, v.cfg.CacheTTL)
	if exists && !lastFetch.IsZero() && time.Since(lastFetch) < v.cfg.CacheTTL {
		return key, nil
	}
	if !lastAttempt.IsZero() && time.Since(lastAttempt) < minInterval {
		if exists {
			return key, nil
		}
		return nil, fmt.Errorf("%w: %q", ErrKeyNotFound, kid)
	}

	newKeys, err := v.refreshKeysSingleflight(ctx)
	if err != nil {
		v.mu.RLock()
		staleKey, staleExists := v.findKeyLocked(kid)
		v.mu.RUnlock()
		if staleExists {
			logging.Warn(ctx, "JWKS refresh failed, using cached key", "error", err)
			return staleKey, nil
		}
		return nil, err
	}

	if pubKey, ok := newKeys[kid]; ok {
		return pubKey, nil
	}
	if kid == "" && len(newKeys) == 1 {
		for _, k := range newKeys {
			return k, nil
		}
	}
	return nil, fmt.Errorf("%w: %q", ErrKeyNotFound, kid)
}

func (v *Validator) findKeyLocked(kid string) (*rsa.PublicKey, bool) {
	if key, exists := v.keys[kid]; exists {
		return key, true
	}
	if kid == "" && len(v.keys) == 1 {
		for _, k := range v.keys {
			return k, true
		}
	}
	return nil, false
}

// Claims represents the OIDC token claims required for validation.
type Claims struct {
	Email string `json:"email"`
	// EmailVerified is the raw email_verified claim, or nil when the token omits it. It is kept
	// untyped because identity providers disagree on whether it is a JSON boolean or a string.
	EmailVerified any `json:"email_verified,omitempty"`
	jwt.RegisteredClaims
}

// ValidateClaimsAnyAudience verifies the cryptographic signature, expiration, issuer, service account,
// and checks that the token's audience matches at least one of the allowed audiences.
func (v *Validator) ValidateClaimsAnyAudience(ctx context.Context, tokenStr string, audiences []string) (*Claims, error) {
	if strings.TrimSpace(tokenStr) == "" {
		return nil, ErrEmptyToken
	}
	token, err := jwt.ParseWithClaims(tokenStr, &Claims{}, func(t *jwt.Token) (any, error) {
		if _, ok := t.Method.(*jwt.SigningMethodRSA); !ok || t.Method.Alg() != jwt.SigningMethodRS256.Alg() {
			return nil, fmt.Errorf("unexpected signing method: %v, expected RS256", t.Header["alg"])
		}
		kid, _ := t.Header["kid"].(string)
		return v.getKey(ctx, kid)
	})
	if err != nil {
		return nil, fmt.Errorf("%w: %w", ErrInvalidToken, err)
	}
	claims, ok := token.Claims.(*Claims)
	if !ok || !token.Valid {
		return nil, ErrInvalidToken
	}
	if claims.ExpiresAt == nil {
		return nil, fmt.Errorf("%w: missing required exp claim", ErrInvalidToken)
	}
	if err := v.validateAudiences(claims, audiences); err != nil {
		return nil, err
	}
	if err := v.validateIssuer(claims); err != nil {
		return nil, err
	}
	return claims, v.validateServiceAccount(claims)
}

// ValidateClaims verifies the token and returns parsed claims for the single given audience.
func (v *Validator) ValidateClaims(ctx context.Context, tokenStr, audience string) (*Claims, error) {
	if audience == "" {
		return v.ValidateClaimsAnyAudience(ctx, tokenStr, nil)
	}
	return v.ValidateClaimsAnyAudience(ctx, tokenStr, []string{audience})
}

// Validate verifies the token signature, audience, and claims against configured requirements.
func (v *Validator) Validate(ctx context.Context, tokenStr, audience string) error {
	_, err := v.ValidateClaims(ctx, tokenStr, audience)
	return err
}

func (v *Validator) validateAudiences(claims *Claims, audiences []string) error {
	allowed := append(slices.Clone(audiences), v.cfg.AllowedAudiences...)
	if len(allowed) == 0 {
		return fmt.Errorf("%w: no allowed audiences configured for validation", ErrAudienceMismatch)
	}
	for _, aud := range claims.Audience {
		if aud != "" && slices.Contains(allowed, aud) {
			return nil
		}
	}
	return fmt.Errorf("%w: expected one of %v, got %v", ErrAudienceMismatch, allowed, claims.Audience)
}

func (v *Validator) validateIssuer(claims *Claims) error {
	if v.cfg.ExpectedIssuer != "" && claims.Issuer != v.cfg.ExpectedIssuer {
		return fmt.Errorf("%w: expected %q, got %q", ErrIssuerMismatch, v.cfg.ExpectedIssuer, claims.Issuer)
	}
	return nil
}

func (v *Validator) validateServiceAccount(claims *Claims) error {
	var allowed []string
	if v.cfg.ExpectedServiceAccount != "" {
		allowed = append(allowed, v.cfg.ExpectedServiceAccount)
	}
	allowed = append(allowed, v.cfg.AllowedServiceAccounts...)
	if len(allowed) == 0 {
		return nil
	}
	for _, sa := range allowed {
		if sa != "" && (claims.Email == sa || claims.Subject == sa) {
			return nil
		}
	}
	return fmt.Errorf("%w: expected one of %v, got email=%q, sub=%q", ErrServiceAccountMismatch, allowed, claims.Email, claims.Subject)
}
