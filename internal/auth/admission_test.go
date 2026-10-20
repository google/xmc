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
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
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

func TestDeploymentAdmission(t *testing.T) {
	enabled := &fakeAdmissionChecker{admitted: true}
	var typedNil *fakeAdmissionChecker
	tests := []struct {
		name  string
		authz UserAuthorizer
		want  AdmissionChecker
	}{
		{name: "uses the authorizer", authz: enabled, want: enabled},
		{name: "nil interface", authz: nil, want: nil},
		{name: "typed nil pointer", authz: typedNil, want: nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := DeploymentAdmission(tt.authz); got != tt.want {
				t.Errorf("DeploymentAdmission() = %#v, want %#v", got, tt.want)
			}
		})
	}
}

func TestAdmitAll(t *testing.T) {
	admitted, err := AdmitAll().IsUserAuthorized(context.Background(), "anyone@example.com")
	if err != nil || !admitted {
		t.Errorf("AdmitAll().IsUserAuthorized() = (%v, %v), want (true, nil)", admitted, err)
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
