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
)

// errNotAdmitted reports that a signed-in user may not use this deployment, either because the
// identity provider did not vouch for the email or because admission denied it.
var errNotAdmitted = errors.New("user is not admitted")

// loginIdentity is the identity of a user who completed the IdP round trip.
type loginIdentity struct {
	// email is trimmed and lowercased.
	email   string
	name    string
	picture string
}

// admitLogin verifies the userinfo returned by the identity provider and runs deployment
// admission. It returns an error wrapping errNotAdmitted when the user is rejected, and any other
// error when admission could not be evaluated.
func (a *Authenticator) admitLogin(ctx context.Context, info *GoogleUserInfo) (loginIdentity, error) {
	identity, err := a.verifyUserInfo(info)
	if err != nil {
		return identity, err
	}
	authorized, err := a.IsUserAuthorized(ctx, identity.email)
	if err != nil {
		return identity, fmt.Errorf("authorization pipeline check failed: %w", err)
	}
	if !authorized {
		return identity, fmt.Errorf("%w: not authorized for this deployment", errNotAdmitted)
	}
	return identity, nil
}

// verifyUserInfo requires an email address in the configured claim (Config.EmailClaim) and, for
// the `email` claim, that the identity provider vouches for it (Config.EmailVerification). When
// Google admits the user through the domain allowlist, it also requires a Workspace hosted domain
// (`hd`) that is itself allowlisted. This refuses consumer Google accounts registered with a
// corporate address (they carry no `hd`) while accepting users whose primary Workspace domain
// differs from an allowlisted alias or secondary email domain.
func (a *Authenticator) verifyUserInfo(info *GoogleUserInfo) (loginIdentity, error) {
	if info == nil {
		return loginIdentity{}, fmt.Errorf("%w: no userinfo", errNotAdmitted)
	}
	identity := loginIdentity{name: info.Name, picture: info.Picture}
	email, ok := a.Config.EmailClaim.Address(IdentityClaims{Email: info.Email, PreferredUsername: info.PreferredUsername, UPN: info.UPN})
	if !ok {
		return identity, fmt.Errorf("%w: no email address in the %q claim", errNotAdmitted, a.Config.EmailClaim)
	}
	identity.email = email
	if a.Config.EmailClaim.isEmail() && !EmailVerifiedAccepted(info.EmailVerified, a.Config.requiresVerifiedEmail()) {
		return identity, fmt.Errorf("%w: email is not verified", errNotAdmitted)
	}
	if a.isAllowedDomain(emailDomain(identity.email)) && a.usesGoogleSignIn() && !a.isAllowedDomain(info.HostedDomain) {
		return identity, fmt.Errorf("%w: hosted domain %q is not an allowed domain", errNotAdmitted, info.HostedDomain)
	}
	return identity, nil
}

// usesGoogleSignIn reports whether the identity provider is Google, the only one that sets `hd`.
// IsNonProdAuth is true exactly when the configured identity provider is not Google (for example
// Dex or another OIDC provider) or when authentication is mocked or disabled.
func (a *Authenticator) usesGoogleSignIn() bool {
	return !a.Config.IsNonProdAuth()
}
