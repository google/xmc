// Copyright 2026 Google LLC
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

// Package principal canonicalizes and validates the email and domain principals that XMC
// authenticates and authorizes. It depends only on the standard library so that every auth
// package, including pkg/auth/oauth2, can share one definition of principal equality.
package principal

import (
	"fmt"
	"net/mail"
	"regexp"
	"strings"
)

// plainEmailPattern is the conservative set of email addresses accepted as authenticated
// identities. It covers Google account, Workspace (including apostrophes) and service account
// emails, and is restricted to ASCII so that principals compare with ASCII-only case folding.
var plainEmailPattern = regexp.MustCompile(`^[A-Za-z0-9._%+'-]+@[A-Za-z0-9-]+(\.[A-Za-z0-9-]+)+$`)

// ValidatePlainEmail rejects emails that are not plain addresses (no display name, comment,
// quoting or surrounding whitespace) within plainEmailPattern.
func ValidatePlainEmail(email string) error {
	addr, err := mail.ParseAddress(email)
	if err != nil {
		return fmt.Errorf("invalid email address: %w", err)
	}
	if addr.Name != "" || addr.Address != email {
		return fmt.Errorf("email %q is not a plain address", email)
	}
	if !plainEmailPattern.MatchString(email) {
		return fmt.Errorf("email %q contains unsupported characters", email)
	}
	return nil
}

// CanonicalEmail returns the canonical form of an authenticated email: the address with ASCII
// letters lowercased. Emails that are not plain ASCII addresses are rejected.
func CanonicalEmail(email string) (string, error) {
	if err := ValidatePlainEmail(email); err != nil {
		return "", err
	}
	return asciiLower(email), nil
}

// Normalize returns the canonical form of a principal (user, service account, group or domain):
// surrounding whitespace removed and ASCII letters lowercased. Email addresses are
// case-insensitive, so principals must be compared in this form. Non-ASCII characters are left
// unchanged so that Unicode case folding (e.g. U+212A KELVIN SIGN to "k") cannot make distinct
// principals match.
func Normalize(principal string) string {
	return asciiLower(strings.TrimSpace(principal))
}

// asciiLower lowercases the ASCII letters A-Z in s and leaves every other rune unchanged.
func asciiLower(s string) string {
	return strings.Map(func(r rune) rune {
		if 'A' <= r && r <= 'Z' {
			return r + ('a' - 'A')
		}
		return r
	}, s)
}
