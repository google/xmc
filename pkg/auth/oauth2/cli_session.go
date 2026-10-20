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

package oauth2

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"time"

	"xmanager_cloud/internal/logging"
)

// ErrCLISessionRevoked reports a CLI session token whose session is revoked, unknown, or cannot be
// checked because no CLISessionChecker is configured.
var ErrCLISessionRevoked = errors.New("cli session is revoked")

// CLISession is the server-side record of an issued CLI session token.
type CLISession struct {
	// JTI is the token's jti claim.
	JTI string
	// Email is the normalized email the token was issued to.
	Email string
	// IssuedAt and ExpiresAt are the token's iat and exp claims.
	IssuedAt  time.Time
	ExpiresAt time.Time
}

// CLISessionChecker reports whether CLI sessions are revoked. Every service that accepts CLI
// session tokens needs one.
type CLISessionChecker interface {
	// IsRevoked reports whether the session with the given jti is revoked. A jti with no session
	// record counts as revoked: every issued token is recorded before it is returned.
	IsRevoked(ctx context.Context, jti string) (bool, error)
}

// CLISessionStore records and revokes CLI sessions. Only the service that issues CLI tokens needs
// one.
type CLISessionStore interface {
	CLISessionChecker
	// Create records a newly issued session.
	Create(ctx context.Context, session CLISession) error
}

// EnableCLISessions records every CLI token minted by this Authenticator in store and rejects
// revoked CLI tokens using store. It is an initialization step, like EnableCLILogin: call it once
// before the Authenticator serves requests.
func (a *Authenticator) EnableCLISessions(store CLISessionStore) error {
	if store == nil {
		return errors.New("cli session store must not be nil")
	}
	if err := a.CheckCLISessions(store); err != nil {
		return err
	}
	a.cliSessionStore = store
	return nil
}

// CheckCLISessions makes this Authenticator reject revoked CLI tokens, looking sessions up through
// checker behind a cliSessionCache. It is an initialization step: call it once before the
// Authenticator serves requests. Without it every CLI token is rejected.
func (a *Authenticator) CheckCLISessions(checker CLISessionChecker) error {
	if checker == nil {
		return errors.New("cli session checker must not be nil")
	}
	if a.cliSessions != nil {
		return errors.New("cli session checks are already enabled")
	}
	a.cliSessions = newCLISessionCache(checker)
	return nil
}

var _ CLISessionChecker = (*Authenticator)(nil)

// IsRevoked implements CLISessionChecker with the checker installed by CheckCLISessions, so gRPC
// authorizers can share this Authenticator's cache. Without a checker every session counts as
// revoked.
func (a *Authenticator) IsRevoked(ctx context.Context, jti string) (bool, error) {
	if a.cliSessions == nil {
		return true, nil
	}
	return a.cliSessions.IsRevoked(ctx, jti)
}

// recordCLISession stores the session of a CLI token about to be issued.
func (a *Authenticator) recordCLISession(ctx context.Context, session CLISession) error {
	if a.cliSessionStore == nil {
		return errors.New("cli session store is not configured")
	}
	if err := a.cliSessionStore.Create(ctx, session); err != nil {
		return fmt.Errorf("failed to record cli session %s: %w", session.JTI, err)
	}
	return nil
}

// VerifyCLISession rejects claims of a revoked CLI session token. Claims of other session tokens
// pass. It returns an error wrapping ErrCLISessionRevoked when the session is revoked or checker is
// nil, and any other error when the lookup fails: callers must fail closed on both.
func VerifyCLISession(ctx context.Context, checker CLISessionChecker, claims *SessionClaims) error {
	if !slices.Contains(claims.Audience, CLIAudience) {
		return nil
	}
	if checker == nil {
		return fmt.Errorf("%w: no session checker for %s", ErrCLISessionRevoked, claims.ID)
	}
	revoked, err := checker.IsRevoked(ctx, claims.ID)
	if err != nil {
		return fmt.Errorf("failed to check cli session %s: %w", claims.ID, err)
	}
	if revoked {
		return fmt.Errorf("%w: %s", ErrCLISessionRevoked, claims.ID)
	}
	return nil
}

// allowCLISession runs VerifyCLISession for the HTTP middleware and writes the error response when
// it fails: 401 for a revoked session, 503 when the session cannot be looked up.
func (a *Authenticator) allowCLISession(w http.ResponseWriter, r *http.Request, claims *SessionClaims) bool {
	err := VerifyCLISession(r.Context(), a, claims)
	if err == nil {
		return true
	}
	if errors.Is(err, ErrCLISessionRevoked) {
		logging.Warn(r.Context(), "[Middleware Auth] Rejected revoked CLI session", "error", err)
		http.Error(w, "Unauthorized: Session token is invalid or missing", http.StatusUnauthorized)
		return false
	}
	logging.Error(r.Context(), "[Middleware Auth] CLI session check failed", "error", err)
	http.Error(w, "Service unavailable: session check failed", http.StatusServiceUnavailable)
	return false
}
