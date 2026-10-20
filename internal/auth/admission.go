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

package auth

import (
	"context"
	"fmt"
	"reflect"

	"xmanager_cloud/internal/grpcerr"
	"xmanager_cloud/internal/logging"
)

// AdmissionChecker decides whether an authenticated principal may use this deployment at all,
// before any per-experiment ACL is consulted. *oauth2.Authenticator satisfies it with the same
// email allowlist, domain allowlist and project IAM role pipeline the HTTP middleware runs, so
// gRPC and HTTP admit exactly the same principals.
type AdmissionChecker interface {
	IsUserAuthorized(ctx context.Context, email string) (bool, error)
}

// UserAuthorizer is the subset of *oauth2.Authenticator consumed by DeploymentAdmission.
type UserAuthorizer interface {
	IsUserAuthorized(ctx context.Context, email string) (bool, error)
}

// DeploymentAdmission returns the admission checker a binary's gRPC authorizer should use for its
// HTTP authenticator. It returns nil for a nil authorizer (including a typed nil pointer), which
// NewAuthorizer rejects.
func DeploymentAdmission(authz UserAuthorizer) AdmissionChecker {
	if authz == nil {
		return nil
	}
	if v := reflect.ValueOf(authz); v.Kind() == reflect.Pointer && v.IsNil() {
		return nil
	}
	return authz
}

type admitAllChecker struct{}

// AdmitAll returns an AdmissionChecker that admits every authenticated principal. Use it only when
// authentication is disabled.
func AdmitAll() AdmissionChecker { return admitAllChecker{} }

func (admitAllChecker) IsUserAuthorized(context.Context, string) (bool, error) {
	return true, nil
}

// admit runs the deployment admission check for an authenticated email. The email is logged but
// never returned to the caller.
func (a *Authorizer) admit(ctx context.Context, email string) error {
	if a.admission == nil {
		logging.Error(ctx, "Admission checker is not configured; denying request")
		return fmt.Errorf("admission checker is not configured: %w", grpcerr.ErrInternal)
	}
	admitted, err := a.admission.IsUserAuthorized(ctx, email)
	if err != nil {
		logging.Error(ctx, "Admission check failed", "email", email, "error", err)
		return fmt.Errorf("admission check failed: %w", grpcerr.ErrInternal)
	}
	if !admitted {
		logging.Warn(ctx, "Access forbidden: principal is not admitted to this deployment", "email", email)
		return fmt.Errorf("principal is not admitted to this deployment: %w", grpcerr.ErrPermissionDenied)
	}
	return nil
}
