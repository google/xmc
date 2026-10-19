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
	"errors"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"xmanager_cloud/pkg/auth/oauth2"
)

// fakeAdmissionChecker is an AdmissionChecker test double that records the emails it was asked
// about.
type fakeAdmissionChecker struct {
	admitted bool
	err      error
	checked  []string
}

func (f *fakeAdmissionChecker) IsUserAuthorized(_ context.Context, email string) (bool, error) {
	f.checked = append(f.checked, email)
	return f.admitted, f.err
}

// admissionResult is the comparable outcome of an AdmissionChecker call.
type admissionResult struct {
	Admitted bool
	Err      error
}

func TestDeploymentAdmission(t *testing.T) {
	enabled := oauth2.NewAuthenticator(&oauth2.Config{})
	disabled := oauth2.NewAuthenticator(&oauth2.Config{DisableAuthentication: true})
	tests := []struct {
		name          string
		authenticator *oauth2.Authenticator
		want          AdmissionChecker
	}{
		{name: "uses the authenticator", authenticator: enabled, want: enabled},
		{name: "disabled authentication admits all", authenticator: disabled, want: AdmitAll()},
		{name: "nil authenticator", want: nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := DeploymentAdmission(tt.authenticator); got != tt.want {
				t.Errorf("DeploymentAdmission() = %#v, want %#v", got, tt.want)
			}
		})
	}
}

func TestAdmissionCheckers(t *testing.T) {
	crmDown := errors.New("crm unavailable")
	tests := []struct {
		name    string
		checker AdmissionChecker
		email   string
		want    admissionResult
	}{
		{name: "admit all", checker: AdmitAll(), email: "anyone@example.com", want: admissionResult{Admitted: true}},
		{name: "principal listed", checker: AdmitPrincipals("Bridge@Project.iam.gserviceaccount.com"), email: " bridge@project.iam.gserviceaccount.com ", want: admissionResult{Admitted: true}},
		{name: "principal not listed", checker: AdmitPrincipals("bridge@project.iam.gserviceaccount.com"), email: "other@example.com"},
		{name: "blank principals admit nobody", checker: AdmitPrincipals("", "  "), email: ""},
		{name: "any of admits on first match", checker: AnyOf(AdmitPrincipals("a@example.com"), &fakeAdmissionChecker{err: crmDown}), email: "a@example.com", want: admissionResult{Admitted: true}},
		{name: "any of falls through to later checker", checker: AnyOf(AdmitPrincipals("a@example.com"), &fakeAdmissionChecker{admitted: true}), email: "b@example.com", want: admissionResult{Admitted: true}},
		{name: "any of propagates errors", checker: AnyOf(AdmitPrincipals("a@example.com"), &fakeAdmissionChecker{err: crmDown}), email: "b@example.com", want: admissionResult{Err: crmDown}},
		{name: "any of denies when nobody admits", checker: AnyOf(AdmitPrincipals("a@example.com"), &fakeAdmissionChecker{}), email: "b@example.com"},
		{name: "empty any of denies", checker: AnyOf(), email: "a@example.com"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			admitted, err := tt.checker.IsUserAuthorized(context.Background(), tt.email)
			got := admissionResult{Admitted: admitted, Err: err}
			if diff := cmp.Diff(tt.want, got, cmpopts.EquateErrors()); diff != "" {
				t.Errorf("IsUserAuthorized(%q) mismatch (-want +got):\n%s", tt.email, diff)
			}
		})
	}
}

func TestAdmit(t *testing.T) {
	tests := []struct {
		name      string
		admission AdmissionChecker
		wantCode  codes.Code
	}{
		{name: "admitted", admission: &fakeAdmissionChecker{admitted: true}, wantCode: codes.OK},
		{name: "not admitted", admission: &fakeAdmissionChecker{}, wantCode: codes.PermissionDenied},
		{name: "checker error", admission: &fakeAdmissionChecker{err: errors.New("crm unavailable")}, wantCode: codes.Internal},
		{name: "admission not configured", wantCode: codes.Internal},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := &Authorizer{admission: tt.admission}
			err := a.admit(context.Background(), "user@example.com")
			if diff := cmp.Diff(tt.wantCode, status.Code(err)); diff != "" {
				t.Errorf("admit() code mismatch (-want +got):\n%s\nerror: %v", diff, err)
			}
		})
	}
}
