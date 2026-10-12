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
	"net/mail"
	"strings"
	"time"

	xpb "github.com/google/xmc/api/protos/xid"
	"github.com/google/xmc/internal/logging"
)

// IsUserAuthorized checks if a user is authorized for a given role on an XidEntry.
// The user is expected to be in the form of "username@domain.com", but the domain is optional.
func (a *Authorizer) IsUserAuthorized(ctx context.Context, user string, acls *xpb.XidAcls, role Role) (bool, error) {
	if _, err := mail.ParseAddress(user); err != nil {
		logging.Warn(ctx, "Invalid user email", "user", user, "error", err)
		return false, fmt.Errorf("invalid user email: %w", err)
	}

	var domain string
	if components := strings.Split(user, "@"); len(components) == 2 {
		domain = components[1]
	}

	// All roles check Owners
	if a.isPrincipalInList(ctx, user, domain, acls.GetOwners()) {
		return true, nil
	}

	// Writer and Reader roles also check Writers
	if role == Writer || role == Reader {
		if a.isPrincipalInList(ctx, user, domain, acls.GetWriters()) {
			return true, nil
		}
	}

	// Only Reader role also checks Readers
	if role == Reader {
		if a.isPrincipalInList(ctx, user, domain, acls.GetReaders()) {
			return true, nil
		}
	}

	logging.Warn(ctx, "User is not authorized for role", "user", user, "role", role)
	return false, nil
}

// IsUserAuthorizedForChart checks if a user is authorized for Row-Level Security (RLS) access on an XidEntry.
func (a *Authorizer) IsUserAuthorizedForChart(ctx context.Context, user string, acls *xpb.XidAcls) (bool, error) {
	return a.IsUserAuthorized(ctx, user, acls, Reader)
}

// isPrincipalInList checks if a user is in a list of authorized principals.
func (a *Authorizer) isPrincipalInList(ctx context.Context, user, domain string, authorizedUsers []*xpb.XidAcls_Role) bool {
	for _, authorizedUser := range authorizedUsers {
		if user == authorizedUser.GetUser() {
			logging.Info(ctx, "User is authorized", "user", user, "authorized_user", authorizedUser.GetUser())
			return true
		}

		if groupEmail := authorizedUser.GetGroup(); groupEmail != "" {
			isMember, err := a.isUserInGroup(ctx, user, groupEmail)

			// Log an error, but continue checking for other auths. This is to avoid a single group
			// causing the entire authorization check to fail.
			if err != nil {
				logging.Warn(ctx, "Group check failed", "user", user, "group", groupEmail, "error", err)
				continue
			}
			if isMember {
				logging.Info(ctx, "User is authorized via group", "user", user, "group", groupEmail)
				return true
			}
		}

		if domain != "" && domain == authorizedUser.GetDomain() {
			logging.Info(ctx, "User is authorized via domain", "user", user, "domain", authorizedUser.GetDomain())
			return true
		}

		if user == authorizedUser.GetServiceAccount() {
			logging.Info(ctx, "User is authorized as service account", "user", user, "service_account", authorizedUser.GetServiceAccount())
			return true
		}

		if authorizedUser.GetAllAuthenticatedUsers() {
			logging.Info(ctx, "All authenticated users are authorized")
			return true
		}
	}

	return false
}

// isUserInGroup checks if a user is a member of a group.
// The cache is used to avoid calling the API for every request.
func (a *Authorizer) isUserInGroup(ctx context.Context, userEmail, groupEmail string) (bool, error) {
	if _, err := mail.ParseAddress(groupEmail); err != nil {
		return false, fmt.Errorf("invalid group email: %w", err)
	}

	// The cache key is a simple combination of user and group.
	cacheKey := userEmail + ":" + groupEmail

	// --- Step 1: Check the cache (Read Lock) ---
	a.cacheLock.RLock()
	entry, found := a.groupCache[cacheKey]
	if found && time.Now().Before(entry.expiresAt) {
		logging.Info(ctx, "Cache HIT for user in group", "user", userEmail, "group", groupEmail)
		a.cacheLock.RUnlock()
		return entry.isMember, entry.err
	}
	a.cacheLock.RUnlock()

	logging.Info(ctx, "Cache MISS for user in group", "user", userEmail, "group", groupEmail)

	// --- Step 2: Acquire Write Lock ---
	a.cacheLock.Lock()
	defer a.cacheLock.Unlock()

	// --- Step 3: Check cache AGAIN (inside the write lock) ---
	entry, found = a.groupCache[cacheKey]
	if found && time.Now().Before(entry.expiresAt) {
		logging.Info(ctx, "Cache HIT (after lock) for user in group", "user", userEmail, "group", groupEmail)
		return entry.isMember, entry.err
	}

	// --- Step 4: This is the ONLY goroutine to call the API ---
	isMember, err := a.checkGroupMembershipAPI(ctx, userEmail, groupEmail)
	if err != nil {
		a.groupCache[cacheKey] = cacheEntry{
			isMember:  false,
			err:       err,
			expiresAt: time.Now().Add(a.cacheErrorTTL),
		}
		return false, err
	}

	// --- Step 5: Update the cache ---
	a.groupCache[cacheKey] = cacheEntry{
		isMember:  isMember,
		err:       nil,
		expiresAt: time.Now().Add(a.cacheTTL),
	}

	return isMember, nil
}

// checkGroupMembershipAPI is the internal helper that performs the actual network calls.
func (a *Authorizer) checkGroupMembershipAPI(ctx context.Context, userEmail, groupEmail string) (bool, error) {
	// Step 1: Look up the group's unique resource name from its email.
	lookupReq := a.IDService.Groups.Lookup().GroupKeyId(groupEmail)
	lookupResp, err := lookupReq.Do()
	if err != nil {
		logging.Warn(ctx, "Failed to look up group", "group", groupEmail, "error", err)
		return false, err
	}
	groupName := lookupResp.Name

	// Step 2: Check membership using the group's resource name.
	query := fmt.Sprintf("member_key.id == '%s'", userEmail)
	checkReq := a.IDService.Groups.Memberships.CheckTransitiveMembership(groupName).Query(query)
	checkResp, err := checkReq.Do()
	if err != nil {
		logging.Warn(ctx, "Failed to check membership for user in group", "user", userEmail, "group", groupName, "error", err)
		return false, err
	}

	return checkResp.HasMembership, nil
}
