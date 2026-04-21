// Package auth provides authentication and authorization utilities.
package auth

import (
	"context"
	"fmt"
	"sync"
	"time"

	"golang/google_api/cloudidentity/v1beta1/cloudidentity"
	"xmanager_cloud/logging/logging"

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
}

// cacheEntry stores the result of a membership check and when it expires.
type cacheEntry struct {
	isMember  bool
	err       error
	expiresAt time.Time
}

// Authorizer now includes a thread-safe cache for group memberships.
type Authorizer struct {
	IDService     *cloudidentity.Service
	groupCache    map[string]cacheEntry
	cacheLock     sync.RWMutex
	cacheTTL      time.Duration
	cacheErrorTTL time.Duration
	iapAudiences  []string
	// disableAuthentication disables authentication for testing.
	disableAuthentication bool
	iapIssuer             string
}

// NewAuthorizer creates a new Authorizer with an initialized group cache.
// cacheTTL (e.g., 10 * time.Minute) defines how long to cache results.
func NewAuthorizer(ctx context.Context, idService *cloudidentity.Service, cacheTTL time.Duration, cacheErrorTTL time.Duration, iapAudiences []string, iapIssuer string, disableAuthentication bool) (*Authorizer, error) {
	for _, aud := range iapAudiences {
		if aud == "" {
			return nil, fmt.Errorf("IAP audience cannot be an empty string")
		}
	}
	if disableAuthentication {
		logging.Warn(ctx, "Authentication is disabled")
	}

	if iapIssuer == "" {
		return nil, fmt.Errorf("IAP issuer cannot be an empty string")
	}
	return &Authorizer{
		IDService:             idService,
		groupCache:            make(map[string]cacheEntry),
		cacheTTL:              cacheTTL,
		cacheErrorTTL:         cacheErrorTTL,
		iapAudiences:          iapAudiences,
		disableAuthentication: disableAuthentication,
		iapIssuer:             iapIssuer,
	}, nil
}
