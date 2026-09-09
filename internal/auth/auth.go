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
// Package auth provides authentication and authorization utilities.
package auth

import (
	"context"
	"fmt"
	"sync"
	"time"

	"google.golang.org/api/cloudidentity/v1beta1"
	"xmanager_cloud/pkg/auth/jwks"

	xpb "xmanager_cloud/xid_service/proto/messages_go_proto"
)

// Role represents a user's authorization level.
type Role int

const (
	// Unknown is the default value for the role.
	Unknown Role = iota
	// Owner are users who can modify the experiment metadata and ACLs.
	Owner
	// Writer are users who can read/write and change the experiment state.
	Writer
	// Reader are users who can read the experiment data.
	Reader
)

// DefaultIAPIssuer is the default IAP issuer.
const DefaultIAPIssuer = "https://cloud.google.com/iap"

// AuthorizerInterface defines the methods for an authorizer.
type AuthorizerInterface interface {
	// AuthenticateUserFromContext returns the user's email address and their identity service ID
	// from the context. It returns the email address in the form of "foo@example.com", the user ID
	// which uniquely identifies the user, and an error if the user is not authenticated.
	// Se https://docs.cloud.google.com/iap/docs/signed-headers-howto#retrieving_the_user_identity
	// for more details about the user ID and email.
	AuthenticateUserFromContext(ctx context.Context) (string, string, error)
	// IsUserAuthorized checks whether the user has the given role in the ACLs. It returns true if the
	// user has the given role, and an error if the authorization check fails.
	IsUserAuthorized(ctx context.Context, user string, acls *xpb.XidAcls, role Role) (bool, error)
	// IsUserAuthorizedForChart checks if a user is authorized for Row-Level Security (RLS) access on an XidEntry.
	IsUserAuthorizedForChart(ctx context.Context, user string, acls *xpb.XidAcls) (bool, error)
}

// cacheEntry stores the result of a membership check and when it expires.
type cacheEntry struct {
	isMember  bool
	err       error
	expiresAt time.Time
}

// TokenValidator defines an interface for validating tokens against a JWKS endpoint or other token validator.
type TokenValidator interface {
	ValidateClaimsAnyAudience(ctx context.Context, tokenStr string, audiences []string) (*jwks.Claims, error)
}

// Authorizer now includes a thread-safe cache for group memberships.
type Authorizer struct {
	IDService     *cloudidentity.Service
	groupCache    map[string]cacheEntry
	cacheLock     sync.RWMutex
	cacheTTL      time.Duration
	cacheErrorTTL time.Duration
	iapAudiences  []string
	iapIssuer     string
	jwtSecret     []byte
	jwksValidator TokenValidator
}

// SetJWKSValidator configures a JWKS token validator for OIDC token verification.
func (a *Authorizer) SetJWKSValidator(v TokenValidator) {
	a.jwksValidator = v
}

// NewAuthorizer creates a new Authorizer with an initialized group cache.
// cacheTTL (e.g., 10 * time.Minute) defines how long to cache results.
func NewAuthorizer(ctx context.Context, idService *cloudidentity.Service, cacheTTL time.Duration, cacheErrorTTL time.Duration, iapAudiences []string, iapIssuer string, args ...any) (*Authorizer, error) {
	for _, aud := range iapAudiences {
		if aud == "" {
			return nil, fmt.Errorf("IAP audience cannot be an empty string")
		}
	}

	if iapIssuer == "" {
		return nil, fmt.Errorf("IAP issuer cannot be an empty string")
	}

	var jwtSecret []byte
	if len(args) == 1 {
		jwtSecret, _ = args[0].([]byte)
	} else if len(args) >= 2 {
		jwtSecret, _ = args[1].([]byte)
	}

	return &Authorizer{
		IDService:     idService,
		groupCache:    make(map[string]cacheEntry),
		cacheTTL:      cacheTTL,
		cacheErrorTTL: cacheErrorTTL,
		iapAudiences:  iapAudiences,
		iapIssuer:     iapIssuer,
		jwtSecret:     jwtSecret,
	}, nil
}
