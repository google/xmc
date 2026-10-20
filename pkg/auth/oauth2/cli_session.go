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
	"time"
)

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

// CLISessionStore records CLI sessions. Only the service that issues CLI tokens needs one.
type CLISessionStore interface {
	// Create records a newly issued session.
	Create(ctx context.Context, session CLISession) error
}

// EnableCLISessions records every CLI token minted by this Authenticator in store. It is an
// initialization step, like EnableCLILogin: call it once before the Authenticator serves requests.
func (a *Authenticator) EnableCLISessions(store CLISessionStore) error {
	if store == nil {
		return errors.New("cli session store must not be nil")
	}
	if a.cliSessionStore != nil {
		return errors.New("cli sessions are already enabled")
	}
	a.cliSessionStore = store
	return nil
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
