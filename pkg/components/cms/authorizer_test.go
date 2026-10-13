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
	"errors"
	"testing"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	componentspb "xmanager_cloud/api/protos/components/v1alpha1"
)

type fakeUserAuthenticator struct {
	email string
	err   error
}

func (f *fakeUserAuthenticator) AuthenticateUserFromContext(context.Context) (string, string, error) {
	if f.err != nil {
		return "", "", f.err
	}
	return f.email, "uid-1", nil
}

type fakeAdminChecker struct {
	admins map[string]bool
	err    error
}

func (f *fakeAdminChecker) IsAdmin(_ context.Context, email string) (bool, error) {
	if f.err != nil {
		return false, f.err
	}
	return f.admins[email], nil
}

func TestAuthorizer_Authenticate(t *testing.T) {
	tests := []struct {
		name      string
		auth      *Authorizer
		wantEmail string
		wantCode  codes.Code
	}{
		{name: "nil authorizer", auth: nil, wantCode: codes.Unauthenticated},
		{name: "nil authenticator", auth: NewAuthorizer(AuthorizerConfig{}), wantCode: codes.Unauthenticated},
		{
			name:     "status error propagates",
			auth:     NewAuthorizer(AuthorizerConfig{Authenticator: &fakeUserAuthenticator{err: status.Error(codes.Unauthenticated, "missing token")}}),
			wantCode: codes.Unauthenticated,
		},
		{
			name:     "plain error converts to Unauthenticated",
			auth:     NewAuthorizer(AuthorizerConfig{Authenticator: &fakeUserAuthenticator{err: errors.New("jwt expired")}}),
			wantCode: codes.Unauthenticated,
		},
		{
			name:     "empty identity returns Unauthenticated",
			auth:     NewAuthorizer(AuthorizerConfig{Authenticator: &fakeUserAuthenticator{email: "   "}}),
			wantCode: codes.Unauthenticated,
		},
		{
			name:      "normalizes IAP prefix and casing",
			auth:      NewAuthorizer(AuthorizerConfig{Authenticator: &fakeUserAuthenticator{email: "accounts.google.com:Bridge-SA@Proj.iam.gserviceaccount.com"}}),
			wantEmail: "bridge-sa@proj.iam.gserviceaccount.com",
			wantCode:  codes.OK,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := tc.auth.Authenticate(context.Background())
			if status.Code(err) != tc.wantCode || got != tc.wantEmail {
				t.Fatalf("Authenticate() = (%q, %v), want (%q, %v)", got, status.Code(err), tc.wantEmail, tc.wantCode)
			}
		})
	}
}

func TestAuthorizer_AuthorizeMutating(t *testing.T) {
	const bridgeSA = "xmc-bridge@proj.iam.gserviceaccount.com"
	const adminUser = "admin@example.com"
	const viewerUser = "viewer@example.com"

	tests := []struct {
		name      string
		auth      *Authorizer
		wantEmail string
		wantCode  codes.Code
	}{
		{
			name:     "unauthenticated caller",
			auth:     NewAuthorizer(AuthorizerConfig{Authenticator: &fakeUserAuthenticator{err: status.Error(codes.Unauthenticated, "bad token")}, AuthorizedServiceAccounts: []string{bridgeSA}}),
			wantCode: codes.Unauthenticated,
		},
		{
			name:      "authorized service account",
			auth:      NewAuthorizer(AuthorizerConfig{Authenticator: &fakeUserAuthenticator{email: "accounts.google.com:" + bridgeSA}, AuthorizedServiceAccounts: []string{bridgeSA, "  "}}),
			wantEmail: bridgeSA,
			wantCode:  codes.OK,
		},
		{
			name:      "platform admin via AdminChecker",
			auth:      NewAuthorizer(AuthorizerConfig{Authenticator: &fakeUserAuthenticator{email: adminUser}, AdminChecker: &fakeAdminChecker{admins: map[string]bool{adminUser: true}}, AuthorizedServiceAccounts: []string{bridgeSA}}),
			wantEmail: adminUser,
			wantCode:  codes.OK,
		},
		{
			name:     "non-admin caller rejected",
			auth:     NewAuthorizer(AuthorizerConfig{Authenticator: &fakeUserAuthenticator{email: viewerUser}, AdminChecker: &fakeAdminChecker{admins: map[string]bool{adminUser: true}}, AuthorizedServiceAccounts: []string{bridgeSA}}),
			wantCode: codes.PermissionDenied,
		},
		{
			name:     "nil AdminChecker rejects non-SA",
			auth:     NewAuthorizer(AuthorizerConfig{Authenticator: &fakeUserAuthenticator{email: viewerUser}, AuthorizedServiceAccounts: []string{bridgeSA}}),
			wantCode: codes.PermissionDenied,
		},
		{
			name:     "AdminChecker error returns Internal",
			auth:     NewAuthorizer(AuthorizerConfig{Authenticator: &fakeUserAuthenticator{email: viewerUser}, AdminChecker: &fakeAdminChecker{err: errors.New("crm down")}, AuthorizedServiceAccounts: []string{bridgeSA}}),
			wantCode: codes.Internal,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := tc.auth.AuthorizeMutating(context.Background())
			if status.Code(err) != tc.wantCode || got != tc.wantEmail {
				t.Fatalf("AuthorizeMutating() = (%q, %v), want (%q, %v)", got, status.Code(err), tc.wantEmail, tc.wantCode)
			}
		})
	}
}

func TestDisabledAuthorizer(t *testing.T) {
	auth := NewDisabledAuthorizer()
	ctx := context.Background()
	if email, err := auth.Authenticate(ctx); err != nil || email != "anonymous@local" {
		t.Fatalf("Authenticate() = (%q, %v), want (anonymous@local, nil)", email, err)
	}
	if email, err := auth.AuthorizeMutating(ctx); err != nil || email != "anonymous@local" {
		t.Fatalf("AuthorizeMutating() = (%q, %v), want (anonymous@local, nil)", email, err)
	}
}

func TestServer_AuthorizationEnforcement(t *testing.T) {
	ctx := context.Background()
	const bridgeSA = "xmc-bridge@proj.iam.gserviceaccount.com"
	validSpec := &componentspb.ComponentSpec{Type: componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_1P_COMPONENT}

	invokeAll := func(srv *Server) (mutCodes, readCodes []codes.Code) {
		_, e1 := srv.SetComponentIntent(ctx, &componentspb.SetComponentIntentRequest{Name: "comp-1", Spec: validSpec})
		_, e2 := srv.PrepareDeactivation(ctx, &componentspb.PrepareDeactivationRequest{Name: "comp-1"})
		_, e3 := srv.CompleteDeactivation(ctx, &componentspb.CompleteDeactivationRequest{Name: "comp-1"})
		_, e4 := srv.UpdateObservedGeneration(ctx, &componentspb.UpdateObservedGenerationRequest{Name: "comp-1", ObservedGeneration: 1})
		_, e5 := srv.ExecuteDeletion(ctx, &componentspb.ExecuteDeletionRequest{Name: "comp-1"})
		_, e6 := srv.ResolveComponentSnapshot(ctx, &componentspb.ResolveComponentSnapshotRequest{Name: "comp-1"})
		_, e7 := srv.ValidateComponentRequirement(ctx, &componentspb.ValidateComponentRequirementRequest{Name: "comp-1"})
		_, e8 := srv.GetPlatformHealth(ctx, &componentspb.GetPlatformHealthRequest{})
		return []codes.Code{status.Code(e1), status.Code(e2), status.Code(e3), status.Code(e4), status.Code(e5)},
			[]codes.Code{status.Code(e6), status.Code(e7), status.Code(e8)}
	}

	t.Run("nil authorizer fails closed on all 8 RPCs", func(t *testing.T) {
		mutCodes, readCodes := invokeAll(NewServer(&stubStore{}))
		for i, c := range append(mutCodes, readCodes...) {
			if c != codes.Unauthenticated {
				t.Errorf("RPC[%d] code = %v, want %v", i, c, codes.Unauthenticated)
			}
		}
	})

	t.Run("non-admin authenticated caller denied on mutating and allowed on read RPCs", func(t *testing.T) {
		auth := NewAuthorizer(AuthorizerConfig{Authenticator: &fakeUserAuthenticator{email: "viewer@example.com"}, AuthorizedServiceAccounts: []string{bridgeSA}})
		mutCodes, readCodes := invokeAll(NewServer(&stubStore{}, WithAuthorizer(auth)))
		for i, c := range mutCodes {
			if c != codes.PermissionDenied {
				t.Errorf("mutating RPC[%d] code = %v, want %v", i, c, codes.PermissionDenied)
			}
		}
		for i, c := range readCodes {
			if c != codes.Unimplemented {
				t.Errorf("read RPC[%d] code = %v, want %v", i, c, codes.Unimplemented)
			}
		}
	})

	t.Run("authorized service account allowed on all 8 RPCs", func(t *testing.T) {
		auth := NewAuthorizer(AuthorizerConfig{Authenticator: &fakeUserAuthenticator{email: bridgeSA}, AuthorizedServiceAccounts: []string{bridgeSA}})
		mutCodes, readCodes := invokeAll(NewServer(&stubStore{}, WithAuthorizer(auth)))
		for i, c := range append(mutCodes, readCodes...) {
			if c != codes.Unimplemented {
				t.Errorf("RPC[%d] code = %v, want %v", i, c, codes.Unimplemented)
			}
		}
	})
}
