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
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
)

func TestDeriveCLIKeys(t *testing.T) {
	// HKDF-SHA256 (RFC 5869) with an empty salt and a 32-byte output is
	// PRK = HMAC(0^32, secret), OKM = HMAC(PRK, info || 0x01).
	prk := hmacSHA256(make([]byte, sha256.Size), []byte(loginTestSecret))
	want := [][]byte{
		hmacSHA256(prk, append([]byte(cliLoginMACInfo), 0x01)),
		hmacSHA256(prk, append([]byte(cliManualConfirmationInfo), 0x01)),
	}

	ticketKey, confirmationKey, err := deriveCLIKeys([]byte(loginTestSecret))
	if err != nil {
		t.Fatalf("deriveCLIKeys() failed: %v", err)
	}
	if diff := cmp.Diff(want, [][]byte{ticketKey, confirmationKey}); diff != "" {
		t.Errorf("deriveCLIKeys() mismatch (-want +got):\n%s", diff)
	}
}

func TestCLILoginTicketRoundTrip(t *testing.T) {
	a, _, _ := newCLITestAuthenticator(t, baseLoginConfig())
	want := baseCLITicket("oauth-state")
	got, err := a.openCLILoginTicket(mustSealCLILoginTicket(t, a, want), time.Now())
	if err != nil {
		t.Fatalf("openCLILoginTicket() failed: %v", err)
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("openCLILoginTicket() mismatch (-want +got):\n%s", diff)
	}
}

func TestOpenCLILoginTicketRejects(t *testing.T) {
	a, _, _ := newCLITestAuthenticator(t, baseLoginConfig())
	sealed := mustSealCLILoginTicket(t, a, baseCLITicket("oauth-state"))
	payload, mac, _ := strings.Cut(sealed, ".")
	// signed seals an arbitrary payload with the authenticator's MAC key.
	signed := func(payload string) string {
		return payload + "." + base64.RawURLEncoding.EncodeToString(hmacSHA256(a.cliLoginKey, []byte(payload)))
	}
	// rawSecretMAC seals the genuine payload with the JWT secret instead of the HKDF sub-key.
	rawSecretMAC := func() string {
		return payload + "." + base64.RawURLEncoding.EncodeToString(hmacSHA256([]byte(loginTestSecret), []byte(payload)))
	}
	// confirmationKeyMAC seals the genuine payload with the manual confirmation sub-key.
	confirmationKeyMAC := func() string {
		return payload + "." + base64.RawURLEncoding.EncodeToString(hmacSHA256(a.cliConfirmationKey, []byte(payload)))
	}
	tampered := []byte(payload)
	tampered[5] ^= 0x01

	tests := []struct {
		name   string
		sealed string
		now    time.Time
		// expired reports whether the error must also match errExpiredCLILoginTicket.
		expired bool
	}{
		{name: "missing separator", sealed: payload, now: time.Now()},
		{name: "mac is not base64url", sealed: payload + ".!!!", now: time.Now()},
		{name: "tampered payload", sealed: string(tampered) + "." + mac, now: time.Now()},
		{name: "mac from another payload", sealed: payload + "." + strings.SplitN(signed("e30"), ".", 2)[1], now: time.Now()},
		{name: "mac keyed with the raw jwt secret", sealed: rawSecretMAC(), now: time.Now()},
		{name: "mac keyed with the confirmation key", sealed: confirmationKeyMAC(), now: time.Now()},
		{name: "payload is not base64url", sealed: signed("!!!"), now: time.Now()},
		{name: "payload is not json", sealed: signed(base64.RawURLEncoding.EncodeToString([]byte("not json"))), now: time.Now()},
		{name: "expired", sealed: sealed, now: time.Now().Add(cliLoginTicketTTL + time.Second), expired: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := a.openCLILoginTicket(tt.sealed, tt.now)
			if !errors.Is(err, errInvalidCLILoginTicket) {
				t.Errorf("openCLILoginTicket() error = %v, want %v", err, errInvalidCLILoginTicket)
			}
			if got := errors.Is(err, errExpiredCLILoginTicket); got != tt.expired {
				t.Errorf("errors.Is(%v, errExpiredCLILoginTicket) = %t, want %t", err, got, tt.expired)
			}
		})
	}
}

func TestCLILoginTicketBoundTo(t *testing.T) {
	ticket := baseCLITicket("oauth-state")
	tests := []struct {
		name       string
		oauthState string
		want       bool
	}{
		{name: "same round trip", oauthState: "oauth-state", want: true},
		{name: "another round trip", oauthState: "other-state", want: false},
		{name: "empty state", oauthState: "", want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ticket.boundTo(tt.oauthState); got != tt.want {
				t.Errorf("boundTo(%q) = %v, want %v", tt.oauthState, got, tt.want)
			}
		})
	}
}
