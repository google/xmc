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
	"sync/atomic"
	"testing"
	"time"

	xpb "xmanager_cloud/api/protos/xid"
)

// maliciousMemberEmails are member emails that would alter an unescaped membership query.
var maliciousMemberEmails = []string{
	`a' || true || '@example.com`,
	`x'||true||'@example.com`,
	`"a' || true || '"@example.com`,
	`a\@example.com`,
	`a\'@example.com`,
	`Bob <bob@example.com>`,
	`user@example.com' || member_key.id != '`,
}

func TestTransitiveMembershipQuery(t *testing.T) {
	testCases := []struct {
		name  string
		email string
		want  string
	}{
		{"plain", "user@example.com", `member_key.id == 'user@example.com'`},
		{"apostrophe is escaped", "o'brien@example.com", `member_key.id == 'o\'brien@example.com'`},
		{"injection stays inside the literal", `a' || true || '@example.com`, `member_key.id == 'a\' || true || \'@example.com'`},
		{"backslash is escaped", `a\'@example.com`, `member_key.id == 'a\\\'@example.com'`},
	}
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			if got := transitiveMembershipQuery(tc.email); got != tc.want {
				t.Errorf("transitiveMembershipQuery(%q) = %q, want %q", tc.email, got, tc.want)
			}
		})
	}
}

func TestIsUserInGroup_RejectsMaliciousMemberEmail(t *testing.T) {
	server, responses := NewTestServer()
	defer server.Close()
	a := NewTestAuthorizer(t, 10*time.Minute, 1*time.Minute, server)
	responses.lookup["group@example.com"] = "groups/123"

	for _, email := range maliciousMemberEmails {
		t.Run(email, func(t *testing.T) {
			atomic.StoreInt64(&responses.apiCalls, 0)
			isMember, err := a.isUserInGroup(context.Background(), email, "group@example.com")
			if err == nil || isMember {
				t.Errorf("isUserInGroup(%q) = (%v, %v), want (false, error)", email, isMember, err)
			}
			if calls := atomic.LoadInt64(&responses.apiCalls); calls != 0 {
				t.Errorf("isUserInGroup(%q) made %d API calls, want 0", email, calls)
			}
		})
	}
}

func TestIsUserAuthorized_MaliciousUserSkipsGroupCheck(t *testing.T) {
	server, responses := NewTestServer()
	defer server.Close()
	a := NewTestAuthorizer(t, 10*time.Minute, 1*time.Minute, server)
	responses.lookup["group@example.com"] = "groups/123"
	acls := &xpb.XidAcls{Readers: []*xpb.XidAcls_Role{groupRole("group@example.com"), groupRole("other@example.com")}}

	for _, user := range maliciousMemberEmails {
		t.Run(user, func(t *testing.T) {
			atomic.StoreInt64(&responses.apiCalls, 0)
			// Users that are not even parseable addresses are rejected with an error; the rest
			// must be denied without any group lookup.
			authorized, _ := a.IsUserAuthorized(context.Background(), user, acls, Reader)
			if authorized {
				t.Errorf("IsUserAuthorized(%q) = true, want false", user)
			}
			if calls := atomic.LoadInt64(&responses.apiCalls); calls != 0 {
				t.Errorf("IsUserAuthorized(%q) made %d API calls, want 0", user, calls)
			}
		})
	}
}

func TestIsUserAuthorized_ConformingUserChecksGroup(t *testing.T) {
	server, responses := NewTestServer()
	defer server.Close()
	a := NewTestAuthorizer(t, 10*time.Minute, 1*time.Minute, server)
	responses.lookup["group@example.com"] = "groups/123"
	responses.membership["groups/123:member@example.com"] = true
	acls := &xpb.XidAcls{Readers: []*xpb.XidAcls_Role{groupRole("group@example.com")}}

	authorized, err := a.IsUserAuthorized(context.Background(), "Member@Example.com", acls, Reader)
	if err != nil {
		t.Fatalf("IsUserAuthorized() returned unexpected error: %v", err)
	}
	if !authorized {
		t.Errorf("IsUserAuthorized(group member) = false, want true")
	}
}
