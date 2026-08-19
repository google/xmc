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

package main

import (
	"context"

	"xmanager_cloud/logging/logging"
	"xmanager_cloud/pkg/auth/oauth2"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// UserAuthenticator extracts caller identity from context.
type UserAuthenticator interface {
	AuthenticateUserFromContext(ctx context.Context) (string, string, error)
}

// AdminRoleGuard defines the interface for authenticating callers and verifying admin permissions.
type AdminRoleGuard interface {
	// CheckAdminRole ensures the caller in context has the administrator role.
	CheckAdminRole(ctx context.Context) error

	// HasAdminRole determines whether the caller has the administrator role and returns their email.
	HasAdminRole(ctx context.Context) (email string, hasRole bool, err error)
}

// AdminRoleGuardParams contains the initialization parameters for AdminRoleGuard.
type AdminRoleGuardParams struct {
	Authorizer    UserAuthenticator
	Authenticator *oauth2.Authenticator
}

// adminRoleGuard provides the real implementation of AdminRoleGuard.
type adminRoleGuard struct {
	authorizer    UserAuthenticator
	authenticator *oauth2.Authenticator
}

// NewAdminRoleGuard creates a new instance of AdminRoleGuard.
func NewAdminRoleGuard(params AdminRoleGuardParams) AdminRoleGuard {
	return &adminRoleGuard{
		authorizer:    params.Authorizer,
		authenticator: params.Authenticator,
	}
}

// CheckAdminRole verifies if the incoming request context has the administrator role.
func (g *adminRoleGuard) CheckAdminRole(ctx context.Context) error {
	_, hasRole, err := g.HasAdminRole(ctx)
	if err != nil {
		return err
	}
	if !hasRole {
		return status.Errorf(codes.PermissionDenied, "forbidden: user lacks admin role")
	}
	return nil
}

// HasAdminRole determines whether the caller has the administrator role and returns their email.
func (g *adminRoleGuard) HasAdminRole(ctx context.Context) (string, bool, error) {
	if g.authorizer == nil || g.authenticator == nil {
		return "", false, status.Errorf(codes.Internal, "authorizer or authenticator not configured")
	}

	email, _, err := g.authorizer.AuthenticateUserFromContext(ctx)
	if err != nil {
		return "", false, err
	}

	hasRole, err := g.authenticator.IsAdmin(ctx, email)
	if err != nil {
		logging.Error(ctx, "[Admin Role Guard] Admin check failed", "error", err, "user", email)
		return email, false, status.Errorf(codes.Internal, "internal authorization check failed")
	}

	return email, hasRole, nil
}
