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
	"testing"
	"time"
)

const (
	cliTestPort      = 51234
	cliTestState     = "cli-state_123"
	cliTestVerifier  = "dBjftJeZ4CVP-mB92K27uhbUJU1p1r_wW1gFWFOEjXk"
	cliTestChallenge = "E9Melhoa2OwvFrEMTJguCHaoeK1t8URWbuGJSstw-cM"
)

func newCLITestAuthenticator(t *testing.T, cfg *Config) (*Authenticator, *stubOAuthProvider) {
	t.Helper()
	a, provider := newLoginTestAuthenticator(cfg)
	if err := a.EnableCLILogin(); err != nil {
		t.Fatalf("EnableCLILogin() failed: %v", err)
	}
	return a, provider
}

func baseCLITicket(oauthState string) cliLoginTicket {
	return cliLoginTicket{
		Port:           cliTestPort,
		State:          cliTestState,
		CodeChallenge:  cliTestChallenge,
		Mode:           cliLoginModeLoopback,
		OAuthStateHash: hashOAuthState(oauthState),
		ExpiresAt:      time.Now().Add(cliLoginTicketTTL).Unix(),
	}
}

func withFailingRandom(t *testing.T) {
	t.Helper()
	orig := randReader
	randReader = errorReader{}
	t.Cleanup(func() { randReader = orig })
}
