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
	"time"

	xpb "xmanager_cloud/api/protos/xid"
	"xmanager_cloud/internal/logging"
	"xmanager_cloud/pkg/auth/principal"
)

// IsUserAuthorized checks if a user is authorized for a given role on an XidEntry.
// The user is expected to be in the form of "username@domain.com", but the domain is optional.
func (a *Authorizer) IsUserAuthorized(ctx context.Context, user string, acls *xpb.XidAcls, role Role) (bool, error) {
	if _, err := mail.ParseAddress(user); err != nil {
		logging.Warn(ctx, "Invalid user email", "user", user, "error", err)
		return false, fmt.Errorf("invalid user email: %w", err)
	}

	// Principals are compared in canonical form because email addresses are case-insensitive.
	c := newCaller(ctx, principal.Normalize(user))

	// All roles check Owners
	if a.isPrincipalInList(ctx, c, acls.GetOwners()) {
		return true, nil
	}

	// Writer and Reader roles also check Writers
	if role == Writer || role == Reader {
		if a.isPrincipalInList(ctx, c, acls.GetWriters()) {
			return true, nil
		}
	}

	// Only Reader role also checks Readers
	if role == Reader {
		if a.isPrincipalInList(ctx, c, acls.GetReaders()) {
			return true, nil
		}
	}

	logging.Warn(ctx, "User is not authorized for role", "user", c.email, "role", role)
	return false, nil
}

// IsUserAuthorizedForChart checks if a user is authorized for Row-Level Security (RLS) access on an XidEntry.
func (a *Authorizer) IsUserAuthorizedForChart(ctx context.Context, user string, acls *xpb.XidAcls) (bool, error) {
	return a.IsUserAuthorized(ctx, user, acls, Reader)
}

// caller is the identity that ACL entries are evaluated against.
type caller struct {
	// email is the caller's email in canonical form.
	email string
	// domain is the canonical domain of email, or "" if it has none.
	domain string
	// groupEligible reports whether email may be embedded in a Cloud Identity membership query.
	groupEligible bool
}

// newCaller returns the caller for a canonical email. Group membership is only checked for plain
// ASCII email addresses (see principal.ValidatePlainEmail), so that user input cannot alter the
// membership query; other emails are logged once and evaluated against the remaining principal
// types.
func newCaller(ctx context.Context, email string) caller {
	c := caller{email: email, domain: emailDomain(email), groupEligible: true}
	if err := principal.ValidatePlainEmail(email); err != nil {
		logging.Info(ctx, "Skipping group-based authorization for unsupported email", "user", email, "reason", err)
		c.groupEligible = false
	}
	return c
}

// isPrincipalInList checks if a user is in a list of authorized principals. Principals are
// compared case-insensitively.
func (a *Authorizer) isPrincipalInList(ctx context.Context, c caller, authorizedUsers []*xpb.XidAcls_Role) bool {
	for _, authorizedUser := range authorizedUsers {
		if matchesIdentityPrincipal(ctx, c, authorizedUser) {
			return true
		}
		if a.matchesGroupPrincipal(ctx, c, authorizedUser) {
			return true
		}
	}

	return false
}

// matchesIdentityPrincipal reports whether an ACL entry grants access to the user directly: by
// user, service account, domain, or all authenticated users.
func matchesIdentityPrincipal(ctx context.Context, c caller, authorizedUser *xpb.XidAcls_Role) bool {
	if principalsMatch(authorizedUser.GetUser(), c.email) {
		logging.Info(ctx, "User is authorized", "user", c.email, "authorized_user", authorizedUser.GetUser())
		return true
	}
	if principalsMatch(authorizedUser.GetDomain(), c.domain) {
		logging.Info(ctx, "User is authorized via domain", "user", c.email, "domain", authorizedUser.GetDomain())
		return true
	}
	if principalsMatch(authorizedUser.GetServiceAccount(), c.email) {
		logging.Info(ctx, "User is authorized as service account", "user", c.email, "service_account", authorizedUser.GetServiceAccount())
		return true
	}
	if authorizedUser.GetAllAuthenticatedUsers() {
		logging.Info(ctx, "All authenticated users are authorized")
		return true
	}
	return false
}

// matchesGroupPrincipal reports whether an ACL entry grants access to the user via membership in
// a group. Group check failures are logged and treated as non-matches so that a single failing
// group does not fail the entire authorization check.
func (a *Authorizer) matchesGroupPrincipal(ctx context.Context, c caller, authorizedUser *xpb.XidAcls_Role) bool {
	groupEmail := authorizedUser.GetGroup()
	if groupEmail == "" || !c.groupEligible {
		return false
	}
	isMember, err := a.isUserInGroup(ctx, c.email, groupEmail)
	if err != nil {
		logging.Warn(ctx, "Group check failed", "user", c.email, "group", groupEmail, "error", err)
		return false
	}
	if isMember {
		logging.Info(ctx, "User is authorized via group", "user", c.email, "group", groupEmail)
	}
	return isMember
}

// isUserInGroup checks if a user is a member of a group.
// The cache is used to avoid calling the API for every request.
func (a *Authorizer) isUserInGroup(ctx context.Context, userEmail, groupEmail string) (bool, error) {
	if _, err := mail.ParseAddress(groupEmail); err != nil {
		return false, fmt.Errorf("invalid group email: %w", err)
	}
	// Reject member emails that could alter the membership query before consulting the cache or
	// the API. IsUserAuthorized already skips group checks for such emails; this guard is defense
	// in depth.
	if err := principal.ValidatePlainEmail(userEmail); err != nil {
		return false, err
	}

	// Canonicalize both principals up front so the cache key and the membership API query use the
	// same form, and differently-cased spellings of the same principals share one cache entry.
	userEmail = principal.Normalize(userEmail)
	groupEmail = principal.Normalize(groupEmail)
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
	query := transitiveMembershipQuery(userEmail)
	checkReq := a.IDService.Groups.Memberships.CheckTransitiveMembership(groupName).Query(query)
	checkResp, err := checkReq.Do()
	if err != nil {
		logging.Warn(ctx, "Failed to check membership for user in group", "user", userEmail, "group", groupName, "error", err)
		return false, err
	}

	return checkResp.HasMembership, nil
}
