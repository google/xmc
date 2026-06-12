// Copyright 2025 Google LLC
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
	"net/http"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	xpb "xmanager_cloud/xid_service/proto/messages_go_proto"
)

func TestIsAuthorized_RoleHierarchy(t *testing.T) {
	a := &Authorizer{}
	ctx := context.Background()

	xidAcls := &xpb.XidAcls{
		Owners:  []*xpb.XidAcls_Role{{Principal: &xpb.XidAcls_Role_User{User: "owner@example.com"}}},
		Writers: []*xpb.XidAcls_Role{{Principal: &xpb.XidAcls_Role_User{User: "writer@example.com"}}},
		Readers: []*xpb.XidAcls_Role{{Principal: &xpb.XidAcls_Role_User{User: "reader@example.com"}}},
	}

	testCases := []struct {
		name           string
		user           string
		role           Role
		wantAuthorized bool
	}{
		{"Owner as Owner", "owner@example.com", Owner, true},
		{"Writer as Owner", "writer@example.com", Owner, false},
		{"Reader as Owner", "reader@example.com", Owner, false},
		{"Owner as Writer", "owner@example.com", Writer, true},
		{"Writer as Writer", "writer@example.com", Writer, true},
		{"Reader as Writer", "reader@example.com", Writer, false},
		{"Owner as Reader", "owner@example.com", Reader, true},
		{"Writer as Reader", "writer@example.com", Reader, true},
		{"Reader as Reader", "reader@example.com", Reader, true},
		{"Other as Reader", "other@example.com", Reader, false},
		{"Other as Owner", "other@example.com", Owner, false},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := a.IsUserAuthorized(ctx, tc.user, xidAcls, tc.role)
			if err != nil {
				t.Fatalf("Unexpected error: %v", err)
			}
			if got != tc.wantAuthorized {
				t.Errorf("IsUserAuthorized(%s, xidAcls, %v) = %v; want %v", tc.user, tc.role, got, tc.wantAuthorized)
			}
		})
	}
}

func TestIsAuthorized_PrincipalTypes(t *testing.T) {
	a := &Authorizer{}
	ctx := context.Background()

	xidAclsNoAllUsers := &xpb.XidAcls{
		Readers: []*xpb.XidAcls_Role{
			{Principal: &xpb.XidAcls_Role_User{User: "user@example.com"}},
			{Principal: &xpb.XidAcls_Role_Domain{Domain: "example.com"}},
			{Principal: &xpb.XidAcls_Role_ServiceAccount{ServiceAccount: "sa@project.iam.gserviceaccount.com"}},
		},
	}
	xidAclsAllUsers := &xpb.XidAcls{
		Readers: []*xpb.XidAcls_Role{
			{Principal: &xpb.XidAcls_Role_AllAuthenticatedUsers{AllAuthenticatedUsers: true}},
		},
	}

	testCases := []struct {
		name string
		user string
		acls *xpb.XidAcls
		want bool
	}{
		{"Direct User Match", "user@example.com", xidAclsNoAllUsers, true},
		{"Service Account Match", "sa@project.iam.gserviceaccount.com", xidAclsNoAllUsers, true},
		{"Domain Match", "test@example.com", xidAclsNoAllUsers, true},
		{"Domain Match (user with @)", "user@example.com", xidAclsNoAllUsers, true},
		{"All Users Match", "random-user@other-domain.com", xidAclsAllUsers, true},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := a.IsUserAuthorized(ctx, tc.user, tc.acls, Reader)
			if err != nil {
				t.Fatalf("Unexpected error: %v", err)
			}
			if got != tc.want {
				t.Errorf("IsUserAuthorized(%s, xidAcls, %v) = %v; want %v", tc.user, Reader, got, tc.want)
			}
		})
	}

	t.Run("No Match", func(t *testing.T) {
		user := "no-match@another.com"
		got, err := a.IsUserAuthorized(ctx, user, xidAclsNoAllUsers, Reader)
		if err != nil {
			t.Fatalf("Unexpected error: %v", err)
		}
		if got != false {
			t.Errorf("IsUserAuthorized(%s, xidAclsNoAllUsers, %v) = %v; want %v", user, Reader, got, false)
		}
	})
}

func TestIsUserInGroup_CachingAndAPI(t *testing.T) {
	server, responses := NewTestServer()
	defer server.Close()

	a := NewTestAuthorizer(t, 10*time.Minute, 1*time.Minute, server)
	ctx := context.Background()

	userEmail := "member@example.com"
	groupEmail := "group@example.com"
	groupName := "groups/12345"
	membershipKey := groupName + ":" + userEmail

	responses.lookup[groupEmail] = groupName
	responses.membership[membershipKey] = true

	userEmail2 := "not-member@example.com"
	membershipKey2 := groupName + ":" + userEmail2
	responses.membership[membershipKey2] = false

	t.Run("Cache Miss", func(t *testing.T) {
		atomic.StoreInt64(&responses.apiCalls, 0)

		isMember, err := a.isUserInGroup(ctx, userEmail, groupEmail)
		if err != nil {
			t.Fatalf("Unexpected error: %v", err)
		}
		if !isMember {
			t.Errorf("isUserInGroup(%s, %s) = %v, want %v", userEmail, groupEmail, isMember, true)
		}
		if calls := atomic.LoadInt64(&responses.apiCalls); calls != 2 {
			t.Errorf("Expected 2 API calls for cache miss, got %d", calls)
		}
	})

	t.Run("Cache Hit", func(t *testing.T) {
		atomic.StoreInt64(&responses.apiCalls, 0)

		isMember, err := a.isUserInGroup(ctx, userEmail, groupEmail)
		if err != nil {
			t.Fatalf("Unexpected error: %v", err)
		}
		if !isMember {
			t.Errorf("isUserInGroup(%s, %s) = %v, want %v", userEmail, groupEmail, isMember, true)
		}
		if calls := atomic.LoadInt64(&responses.apiCalls); calls != 0 {
			t.Errorf("Expected 0 API calls for cache hit, got %d", calls)
		}
	})

	t.Run("Cache Key Uniqueness (User)", func(t *testing.T) {
		atomic.StoreInt64(&responses.apiCalls, 0)

		isMember, err := a.isUserInGroup(ctx, userEmail2, groupEmail)
		if err != nil {
			t.Fatalf("Unexpected error: %v", err)
		}
		if isMember {
			t.Errorf("isUserInGroup(%s, %s) = %v, want %v", userEmail2, groupEmail, isMember, false)
		}
		if calls := atomic.LoadInt64(&responses.apiCalls); calls != 2 {
			t.Errorf("Expected 2 API calls for new user, got %d", calls)
		}
		_, _ = a.isUserInGroup(ctx, userEmail2, groupEmail)
		if calls := atomic.LoadInt64(&responses.apiCalls); calls != 2 {
			t.Errorf("Expected 0 new API calls for subsequent hit, got %d", atomic.LoadInt64(&responses.apiCalls)-2)
		}
	})

	t.Run("Cache Expiration", func(t *testing.T) {
		shortTTLAuthorizer := NewTestAuthorizer(t, 20*time.Millisecond, 2*time.Millisecond, server)
		atomic.StoreInt64(&responses.apiCalls, 0)

		_, err := shortTTLAuthorizer.isUserInGroup(ctx, userEmail, groupEmail)
		if err != nil {
			t.Fatal(err)
		}
		if calls := atomic.LoadInt64(&responses.apiCalls); calls != 2 {
			t.Errorf("Expected 2 API calls for first miss, got %d", calls)
		}

		_, err = shortTTLAuthorizer.isUserInGroup(ctx, userEmail, groupEmail)
		if err != nil {
			t.Fatal(err)
		}
		if calls := atomic.LoadInt64(&responses.apiCalls); calls != 2 {
			t.Errorf("Expected 0 new API calls for cache hit, got %d", atomic.LoadInt64(&responses.apiCalls)-2)
		}

		time.Sleep(30 * time.Millisecond)

		_, err = shortTTLAuthorizer.isUserInGroup(ctx, userEmail, groupEmail)
		if err != nil {
			t.Fatal(err)
		}
		if calls := atomic.LoadInt64(&responses.apiCalls); calls != 4 {
			t.Errorf("Expected 2 new API calls after expiration, got %d total", calls)
		}
	})
}

func TestIsUserInGroup_Concurrency(t *testing.T) {
	server, responses := NewTestServer()
	defer server.Close()

	a := NewTestAuthorizer(t, 10*time.Minute, 1*time.Minute, server)
	ctx := context.Background()

	user := "user@example.com"
	group := "group@example.com"
	responses.lookup[group] = "groups/123"
	responses.membership["groups/123:"+user] = true

	atomic.StoreInt64(&responses.apiCalls, 0)

	numGoroutines := 100
	var wg sync.WaitGroup
	wg.Add(numGoroutines)

	startGate := make(chan struct{})

	for i := 0; i < numGoroutines; i++ {
		go func() {
			defer wg.Done()
			<-startGate
			isMember, err := a.isUserInGroup(ctx, user, group)
			if err != nil {
				t.Errorf("Goroutine failed: %v", err)
			}
			if !isMember {
				t.Errorf("isUserInGroup(%s, %s) = %v, want %v", user, group, isMember, true)
			}
		}()
	}

	close(startGate)
	wg.Wait()

	if calls := atomic.LoadInt64(&responses.apiCalls); calls != 2 {
		t.Errorf("Expected exactly 2 API calls (one miss) for %d concurrent requests, got %d", numGoroutines, calls)
	}
}

func TestIsUserInGroup_APIErrors(t *testing.T) {
	server, responses := NewTestServer()
	defer server.Close()

	a := NewTestAuthorizer(t, 10*time.Minute, 1*time.Minute, server)
	ctx := context.Background()

	t.Run("Lookup Fails", func(t *testing.T) {
		userEmail := "user@example.com"
		groupEmail := "unknown-group@example.com"
		cacheKey := userEmail + ":" + groupEmail

		atomic.StoreInt64(&responses.apiCalls, 0)

		// First call: API error occurs
		isMember, err := a.isUserInGroup(ctx, userEmail, groupEmail)
		if err == nil {
			t.Error("Expected an error for unknown group, got nil")
		}
		if isMember {
			t.Errorf("isUserInGroup(%s, %s) = %v, want %v on error", userEmail, groupEmail, isMember, false)
		}
		if calls := atomic.LoadInt64(&responses.apiCalls); calls != 1 {
			t.Errorf("Expected 1 API call for Lookup failure, got %d", calls)
		}

		// Check if error is cached
		entry, found := a.groupCache[cacheKey]
		if !found {
			t.Fatal("Error result should have been cached, but wasn't")
		}
		if entry.err == nil {
			t.Error("Cached entry did not store the error")
		}

		// Second call: Should hit cache
		atomic.StoreInt64(&responses.apiCalls, 0)
		isMember, err = a.isUserInGroup(ctx, userEmail, groupEmail)
		if err == nil {
			t.Error("Expected a cached error, got nil")
		}
		if isMember {
			t.Errorf("isUserInGroup(%s, %s) = %v, want %v on cached error", userEmail, groupEmail, isMember, false)
		}
		if calls := atomic.LoadInt64(&responses.apiCalls); calls != 0 {
			t.Errorf("Expected 0 API calls for cached error, got %d", calls)
		}
	})

	t.Run("CheckMembership Fails", func(t *testing.T) {
		groupEmail := "failing-group@example.com"
		groupName := "groups/fail"
		userEmail := "user@example.com"
		cacheKey := userEmail + ":" + groupEmail

		responses.lookup[groupEmail] = groupName
		path := fmt.Sprintf("/v1beta1/%s/memberships:checkTransitiveMembership", groupName)
		responses.httpErr[path] = http.StatusInternalServerError
		defer delete(responses.httpErr, path)

		atomic.StoreInt64(&responses.apiCalls, 0)

		// First call: API error occurs
		isMember, err := a.isUserInGroup(ctx, userEmail, groupEmail)
		if err == nil {
			t.Error("Expected an error for CheckMembership failure, got nil")
		}
		if isMember {
			t.Errorf("isUserInGroup(%s, %s) = %v, want %v on error", userEmail, groupEmail, isMember, false)
		}
		if calls := atomic.LoadInt64(&responses.apiCalls); calls != 2 {
			t.Errorf("Expected 2 API calls (Lookup + CheckMembership), got %d", calls)
		}

		// Check if error is cached
		entry, found := a.groupCache[cacheKey]
		if !found {
			t.Fatal("Error result should have been cached, but wasn't")
		}
		if entry.err == nil {
			t.Error("Cached entry did not store the error")
		}

		// Second call: Should hit cache
		atomic.StoreInt64(&responses.apiCalls, 0)
		isMember, err = a.isUserInGroup(ctx, userEmail, groupEmail)
		if err == nil {
			t.Error("Expected a cached error, got nil")
		}
		if isMember {
			t.Errorf("isUserInGroup(%s, %s) = %v, want %v on cached error", userEmail, groupEmail, isMember, false)
		}
		if calls := atomic.LoadInt64(&responses.apiCalls); calls != 0 {
			t.Errorf("Expected 0 API calls for cached error, got %d", calls)
		}
	})
}

func TestIsPrincipalInList_GroupErrorResiliency(t *testing.T) {
	server, responses := NewTestServer()
	defer server.Close()

	a := NewTestAuthorizer(t, 10*time.Minute, 1*time.Minute, server)
	ctx := context.Background()

	failingGroup := "failing-group@example.com"
	goodUser := "good-user@example.com"

	responses.lookup[failingGroup] = "groups/fail"
	path := "/v1beta1/groups/fail/memberships:checkTransitiveMembership"
	responses.httpErr[path] = http.StatusBadGateway

	xidAcls := &xpb.XidAcls{
		Readers: []*xpb.XidAcls_Role{
			{Principal: &xpb.XidAcls_Role_Group{Group: failingGroup}},
			{Principal: &xpb.XidAcls_Role_User{User: goodUser}},
		},
	}

	authorized, err := a.IsUserAuthorized(ctx, goodUser, xidAcls, Reader)
	if err != nil {
		t.Fatalf("IsAuthorized returned an error: %v", err)
	}
	if !authorized {
		t.Errorf("IsUserAuthorized(%s, xidAcls, %v) = %v, want %v", goodUser, Reader, authorized, true)
	}
}
