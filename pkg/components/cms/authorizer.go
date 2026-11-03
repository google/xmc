// Copyright 2026 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package cms

import (
	"context"
	"strings"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"xmanager_cloud/pkg/auth/principal"
)

var (
	_ CallerAuthorizer = (*Authorizer)(nil)
	_ CallerAuthorizer = disabledAuthorizer{}
)

// CallerAuthorizer authenticates and authorizes incoming ComponentManagerService gRPC requests.
type CallerAuthorizer interface {
	Authenticate(ctx context.Context) (string, error)
	AuthorizeMutating(ctx context.Context) (string, error)
}

// UserAuthenticator extracts and validates the caller's email and identity ID from a gRPC context.
type UserAuthenticator interface {
	AuthenticateUserFromContext(ctx context.Context) (string, string, error)
}

// AdminChecker checks whether a given email belongs to a platform administrator.
type AdminChecker interface {
	IsAdmin(ctx context.Context, email string) (bool, error)
}

// AuthorizerConfig configures a CMS CallerAuthorizer.
type AuthorizerConfig struct {
	Authenticator             UserAuthenticator
	AdminChecker              AdminChecker
	AuthorizedServiceAccounts []string
}

// Authorizer enforces authentication on all RPCs and restricts mutating RPCs to authorized
// controller service accounts or platform administrators.
type Authorizer struct {
	authn         UserAuthenticator
	adminChecker  AdminChecker
	authorizedSAs map[string]bool
}

// NewAuthorizer constructs an Authorizer from the provided configuration.
func NewAuthorizer(cfg AuthorizerConfig) *Authorizer {
	sas := make(map[string]bool, len(cfg.AuthorizedServiceAccounts))
	for _, sa := range cfg.AuthorizedServiceAccounts {
		if normalized := normalizeIdentity(sa); normalized != "" {
			sas[normalized] = true
		}
	}
	return &Authorizer{authn: cfg.Authenticator, adminChecker: cfg.AdminChecker, authorizedSAs: sas}
}

// Authenticate verifies caller identity and returns the normalized caller email.
func (a *Authorizer) Authenticate(ctx context.Context) (string, error) {
	if a == nil || a.authn == nil {
		return "", status.Error(codes.Unauthenticated, "CMS authorizer is not configured")
	}
	email, _, err := a.authn.AuthenticateUserFromContext(ctx)
	if err != nil {
		if status.Code(err) == codes.Unknown {
			return "", status.Errorf(codes.Unauthenticated, "authentication failed: %v", err)
		}
		return "", err
	}
	if normalized := normalizeIdentity(email); normalized != "" {
		return normalized, nil
	}
	return "", status.Error(codes.Unauthenticated, "authenticated caller identity is empty")
}

// AuthorizeMutating verifies that the caller is an authorized controller service account or admin.
func (a *Authorizer) AuthorizeMutating(ctx context.Context) (string, error) {
	if a == nil {
		return "", status.Error(codes.Unauthenticated, "CMS authorizer is not configured")
	}
	email, err := a.Authenticate(ctx)
	if err != nil || a.authorizedSAs[email] {
		return email, err
	}
	if a.adminChecker != nil {
		isAdmin, err := a.adminChecker.IsAdmin(ctx, email)
		if err != nil {
			return "", status.Errorf(codes.Internal, "failed to verify admin role for %q: %v", email, err)
		}
		if isAdmin {
			return email, nil
		}
	}
	return "", status.Errorf(codes.PermissionDenied, "caller %q is not authorized to mutate component state", email)
}

// normalizeIdentity returns email in canonical principal form (see principal.Normalize) with any
// IAP "accounts.google.com:" prefix removed.
func normalizeIdentity(email string) string {
	return strings.TrimPrefix(principal.Normalize(email), "accounts.google.com:")
}

type disabledAuthorizer struct{}

// NewDisabledAuthorizer returns a CallerAuthorizer that permits all requests when auth is disabled.
func NewDisabledAuthorizer() CallerAuthorizer { return disabledAuthorizer{} }

func (disabledAuthorizer) Authenticate(context.Context) (string, error) {
	return "anonymous@local", nil
}

func (disabledAuthorizer) AuthorizeMutating(context.Context) (string, error) {
	return "anonymous@local", nil
}
