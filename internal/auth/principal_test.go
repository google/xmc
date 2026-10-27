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
	"testing"

	"github.com/google/go-cmp/cmp"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/testing/protocmp"
	xpb "xmanager_cloud/api/protos/xid"
)

func TestNormalizeACLs(t *testing.T) {
	allUsers := &xpb.XidAcls_Role{Principal: &xpb.XidAcls_Role_AllAuthenticatedUsers{AllAuthenticatedUsers: true}}

	testCases := []struct {
		name string
		acls *xpb.XidAcls
		want *xpb.XidAcls
	}{
		{name: "nil", acls: nil, want: nil},
		{name: "empty", acls: &xpb.XidAcls{}, want: &xpb.XidAcls{}},
		{
			name: "all principal types in all roles",
			acls: &xpb.XidAcls{
				Owners:  []*xpb.XidAcls_Role{userRole(" Owner@Example.com "), saRole("SA@Project.iam.gserviceaccount.com")},
				Writers: []*xpb.XidAcls_Role{groupRole("Team@Example.com")},
				Readers: []*xpb.XidAcls_Role{domainRole("Example.COM"), allUsers},
			},
			want: &xpb.XidAcls{
				Owners:  []*xpb.XidAcls_Role{userRole("owner@example.com"), saRole("sa@project.iam.gserviceaccount.com")},
				Writers: []*xpb.XidAcls_Role{groupRole("team@example.com")},
				Readers: []*xpb.XidAcls_Role{domainRole("example.com"), allUsers},
			},
		},
		{
			name: "non-ASCII is not folded",
			acls: &xpb.XidAcls{Owners: []*xpb.XidAcls_Role{userRole("\u212Aate@Corp.com")}},
			want: &xpb.XidAcls{Owners: []*xpb.XidAcls_Role{userRole("\u212Aate@corp.com")}},
		},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			original := proto.Clone(tc.acls)
			got := NormalizeACLs(tc.acls)
			if diff := cmp.Diff(tc.want, got, protocmp.Transform()); diff != "" {
				t.Errorf("NormalizeACLs() mismatch (-want +got):\n%s", diff)
			}
			if diff := cmp.Diff(original, proto.Message(tc.acls), protocmp.Transform()); diff != "" {
				t.Errorf("NormalizeACLs() modified its input (-before +after):\n%s", diff)
			}
		})
	}
}

func TestPrincipalsMatch(t *testing.T) {
	testCases := []struct {
		name string
		a, b string
		want bool
	}{
		{"identical", "user@example.com", "user@example.com", true},
		{"different case", "User@Example.com", "user@example.com", true},
		{"whitespace", " user@example.com ", "user@example.com", true},
		{"different principals", "user@example.com", "other@example.com", false},
		{"kelvin sign does not fold to k", "\u212Aate@corp.com", "kate@corp.com", false},
		{"both empty", "", "", false},
		{"blank principal", "  ", "", false},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			if got := principalsMatch(tc.a, tc.b); got != tc.want {
				t.Errorf("principalsMatch(%q, %q) = %v, want %v", tc.a, tc.b, got, tc.want)
			}
		})
	}
}

func TestEmailDomain(t *testing.T) {
	testCases := []struct {
		name  string
		email string
		want  string
	}{
		{"simple", "user@example.com", "example.com"},
		{"mixed case", "user@Example.COM", "example.com"},
		{"no at sign", "user", ""},
		{"trailing at sign", "user@", ""},
		{"quoted local part with at sign", `"a@b"@example.com`, "example.com"},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			if got := emailDomain(tc.email); got != tc.want {
				t.Errorf("emailDomain(%q) = %q, want %q", tc.email, got, tc.want)
			}
		})
	}
}
