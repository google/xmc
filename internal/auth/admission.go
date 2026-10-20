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
	"slices"
	"strings"

	"xmanager_cloud/internal/grpcerr"
	"xmanager_cloud/internal/logging"
	"xmanager_cloud/pkg/auth/oauth2"
)

// AdmissionChecker decides whether an authenticated principal may use this deployment at all,
// before any per-experiment ACL is consulted. *oauth2.Authenticator satisfies it with the same
// email allowlist, domain allowlist and project IAM role pipeline the HTTP middleware runs, so
// gRPC and HTTP admit exactly the same principals.
type AdmissionChecker interface {
	IsUserAuthorized(ctx context.Context, email string) (bool, error)
}

// DeploymentAdmission returns the admission checker a binary's gRPC authorizer should use: its HTTP
// authenticator, or AdmitAll when DISABLE_AUTHENTICATION is set (dev and KinD clusters only),
// mirroring cms.NewDisabledAuthorizer. It returns nil for a nil authenticator, which NewAuthorizer
// rejects.
func DeploymentAdmission(authenticator *oauth2.Authenticator) AdmissionChecker {
	if authenticator == nil {
		return nil
	}
	if authenticator.Config != nil && authenticator.Config.DisableAuthentication {
		return AdmitAll()
	}
	return authenticator
}

type admitAllChecker struct{}

// AdmitAll returns an AdmissionChecker that admits every authenticated principal. Use it only when
// authentication is disabled.
func AdmitAll() AdmissionChecker { return admitAllChecker{} }

func (admitAllChecker) IsUserAuthorized(context.Context, string) (bool, error) {
	return true, nil
}

type principalChecker map[string]bool

// AdmitPrincipals returns an AdmissionChecker that admits exactly the given emails, compared
// case-insensitively. Blank entries are ignored.
func AdmitPrincipals(emails ...string) AdmissionChecker {
	principals := make(principalChecker, len(emails))
	for _, email := range emails {
		if normalized := normalizeEmail(email); normalized != "" {
			principals[normalized] = true
		}
	}
	return principals
}

func (p principalChecker) IsUserAuthorized(_ context.Context, email string) (bool, error) {
	return p[normalizeEmail(email)], nil
}

func normalizeEmail(email string) string {
	return strings.ToLower(strings.TrimSpace(email))
}

type anyOfChecker []AdmissionChecker

// AnyOf returns an AdmissionChecker that admits a principal if any of checkers does. Checkers are
// consulted in order and the first error stops the evaluation, so put cheap, infallible checkers
// such as AdmitPrincipals first. It returns nil when any checker is nil (for example
// DeploymentAdmission of a nil authenticator), which NewAuthorizer rejects, so a missing checker
// fails at startup rather than panicking or being silently skipped at request time.
func AnyOf(checkers ...AdmissionChecker) AdmissionChecker {
	if slices.Contains(checkers, nil) {
		return nil
	}
	return anyOfChecker(checkers)
}

func (c anyOfChecker) IsUserAuthorized(ctx context.Context, email string) (bool, error) {
	for _, checker := range c {
		admitted, err := checker.IsUserAuthorized(ctx, email)
		if err != nil || admitted {
			return admitted, err
		}
	}
	return false, nil
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
