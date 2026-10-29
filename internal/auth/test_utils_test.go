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
	"testing"

	xpb "xmanager_cloud/api/protos/xid"
)

func TestMockAuthIsUserAuthorized_CaseInsensitive(t *testing.T) {
	m := &MockAuth{CheckACLs: true}
	acls := &xpb.XidAcls{
		Owners:  []*xpb.XidAcls_Role{userRole("Owner@Example.com")},
		Writers: []*xpb.XidAcls_Role{userRole("Writer@Example.com")},
		Readers: []*xpb.XidAcls_Role{userRole("Reader@Example.com")},
	}

	testCases := []struct {
		name string
		user string
		role Role
		want bool
	}{
		{"owner as owner", "owner@example.com", Owner, true},
		{"writer as writer", "writer@example.com", Writer, true},
		{"writer as owner", "writer@example.com", Owner, false},
		{"reader as reader", "reader@example.com", Reader, true},
		{"reader as writer", "reader@example.com", Writer, false},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := m.IsUserAuthorized(context.Background(), tc.user, acls, tc.role)
			if err != nil {
				t.Fatalf("MockAuth.IsUserAuthorized() returned unexpected error: %v", err)
			}
			if got != tc.want {
				t.Errorf("MockAuth.IsUserAuthorized(%q, %v) = %v, want %v", tc.user, tc.role, got, tc.want)
			}
		})
	}
}
