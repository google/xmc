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
	"crypto/hmac"
	"crypto/sha256"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/go-cmp/cmp"
)

func TestDeriveCLILoginMACKey(t *testing.T) {
	// HKDF-SHA256 (RFC 5869) with an empty salt and a 32-byte output is
	// PRK = HMAC(0^32, secret), OKM = HMAC(PRK, info || 0x01).
	hmacSHA256 := func(key, data []byte) []byte {
		mac := hmac.New(sha256.New, key)
		mac.Write(data)
		return mac.Sum(nil)
	}
	prk := hmacSHA256(make([]byte, sha256.Size), []byte(loginTestSecret))
	want := hmacSHA256(prk, append([]byte(cliLoginMACInfo), 0x01))

	got, err := deriveCLILoginMACKey([]byte(loginTestSecret))
	if err != nil {
		t.Fatalf("deriveCLILoginMACKey() failed: %v", err)
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("deriveCLILoginMACKey() mismatch (-want +got):\n%s", diff)
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
	ticket := baseCLITicket("oauth-state")
	sealed := mustSealCLILoginTicket(t, a, ticket)
	parts := strings.Split(sealed, ".")
	otherParts := strings.Split(mustSealCLILoginTicket(t, a, baseCLITicket("other-state")), ".")
	rawSecretToken, err := sealSigned([]byte(loginTestSecret), ticket, ticket.ExpiresAt)
	if err != nil {
		t.Fatalf("sealSigned() failed: %v", err)
	}
	noneToken, err := jwt.NewWithClaims(jwt.SigningMethodNone, signedClaims[cliLoginTicket]{
		Payload:          ticket,
		RegisteredClaims: jwt.RegisteredClaims{ExpiresAt: jwt.NewNumericDate(time.Unix(ticket.ExpiresAt, 0))},
	}).SignedString(jwt.UnsafeAllowNoneSignatureType)
	if err != nil {
		t.Fatalf("SignedString(none) failed: %v", err)
	}
	tampered := []byte(parts[1])
	tampered[5] ^= 0x01

	tests := []struct {
		name   string
		sealed string
		now    time.Time
		// expired reports whether the error must also match errExpiredCLILoginTicket.
		expired bool
	}{
		{name: "malformed token", sealed: parts[0] + "." + parts[1], now: time.Now()},
		{name: "signature is not base64url", sealed: parts[0] + "." + parts[1] + ".!!!", now: time.Now()},
		{name: "tampered payload", sealed: parts[0] + "." + string(tampered) + "." + parts[2], now: time.Now()},
		{name: "signature from another ticket", sealed: parts[0] + "." + parts[1] + "." + otherParts[2], now: time.Now()},
		{name: "signed with the raw jwt secret", sealed: rawSecretToken, now: time.Now()},
		{name: "alg none rejected", sealed: noneToken, now: time.Now()},
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
