// Copyright 2026 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//      http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package oauth2

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"cloud.google.com/go/iam/apiv1/iampb"
	resourcemanager "cloud.google.com/go/resourcemanager/apiv3"
	"golang.org/x/oauth2"
	"golang.org/x/sync/singleflight"
	"golang.org/x/time/rate"
	"google.golang.org/api/idtoken"
	"xmanager_cloud/internal/logging"
)

var ErrAdminRoleNotConfigured = errors.New("admin IAM role not configured")

type policyCacheEntry struct {
	policy *iampb.Policy
	expiry time.Time
}

// Authenticator manages session issuance, validation, and 3-stage hybrid authorization checking.
type Authenticator struct {
	Config      *Config
	OAuthConfig *oauth2.Config
	JWTSecret   []byte

	mu             sync.Mutex
	projectsClient *resourcemanager.ProjectsClient
	policyCache    policyCacheEntry
	sfGroup        singleflight.Group

	PolicyFetcher    func(context.Context) (*iampb.Policy, error)
	IDTokenValidator func(ctx context.Context, idToken string, audience string) (*idtoken.Payload, error)

	// Injected provider for OAuth client interactions.
	OAuthProvider OAuthProvider

	// CLI login state, set by EnableCLILogin. The CLI login endpoints are disabled while cliCodes
	// is nil.
	cliCodes           CLICodeStore
	cliLoginKey        []byte
	cliConfirmationKey []byte
	cliTokenLimiter    *rate.Limiter

	// cliSessionStore records every minted CLI token; set by EnableCLISessions. CLI tokens are not
	// minted while it is nil.
	cliSessionStore CLISessionStore
}

// NewAuthenticator initializes a new instance of Authenticator.
func NewAuthenticator(cfg *Config) *Authenticator {
	a := &Authenticator{
		Config:    cfg,
		JWTSecret: []byte(cfg.JWTSecret),
		OAuthConfig: &oauth2.Config{
			ClientID:     cfg.ClientID,
			ClientSecret: cfg.ClientSecret,
			RedirectURL:  cfg.RedirectURL,
			Scopes:       []string{"openid", "email", "profile"},
			Endpoint: oauth2.Endpoint{
				AuthURL:  cfg.AuthURL,
				TokenURL: cfg.TokenURL,
			},
		},
	}

	a.OAuthProvider = &GoogleOAuthProvider{
		Authenticator: a,
	}
	a.PolicyFetcher = func(ctx context.Context) (*iampb.Policy, error) {
		client, err := a.getProjectsClient(ctx)
		if err != nil {
			return nil, err
		}
		req := &iampb.GetIamPolicyRequest{
			Resource: "projects/" + a.Config.GCPProjectID,
		}
		return client.GetIamPolicy(ctx, req)
	}
	a.IDTokenValidator = idtoken.Validate
	configureMockIfEnabled(a)

	return a
}

// Close closes the underlying GCP Projects client if it was initialized.
func (a *Authenticator) Close() error {
	a.mu.Lock()
	defer a.mu.Unlock()

	if a.projectsClient != nil {
		err := a.projectsClient.Close()
		a.projectsClient = nil
		return err
	}
	return nil
}

func (a *Authenticator) getProjectsClient(ctx context.Context) (*resourcemanager.ProjectsClient, error) {
	a.mu.Lock()
	if a.projectsClient != nil {
		client := a.projectsClient
		a.mu.Unlock()
		return client, nil
	}
	a.mu.Unlock()

	client, err := resourcemanager.NewProjectsClient(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to create GCP Projects Resource Manager client: %w", err)
	}

	a.mu.Lock()
	if a.projectsClient != nil {
		client.Close()
		existing := a.projectsClient
		a.mu.Unlock()
		return existing, nil
	}
	a.projectsClient = client
	a.mu.Unlock()
	return client, nil
}

func (a *Authenticator) getIamPolicy(ctx context.Context) (*iampb.Policy, error) {
	a.mu.Lock()
	if a.policyCache.policy != nil && time.Now().Before(a.policyCache.expiry) {
		policy := a.policyCache.policy
		a.mu.Unlock()
		return policy, nil
	}
	a.mu.Unlock()

	val, err, _ := a.sfGroup.Do("get_iam_policy", func() (interface{}, error) {
		policy, err := a.PolicyFetcher(ctx)
		if err != nil {
			return nil, err
		}

		a.mu.Lock()
		a.policyCache = policyCacheEntry{
			policy: policy,
			expiry: time.Now().Add(2 * time.Minute),
		}
		a.mu.Unlock()
		return policy, nil
	})

	if err != nil {
		return nil, err
	}
	return val.(*iampb.Policy), nil
}

// IsUserAuthorized implements the 3-stage Hybrid Pipeline: Local Email Allowlist -> Domain Allowlist -> GCP IAM Policy Check.
func (a *Authenticator) IsUserAuthorized(ctx context.Context, email string) (bool, error) {
	emailLower := strings.ToLower(strings.TrimSpace(email))

	// If all allowlists and ProjectID are blank, default to open access (allow all) only if DisableAuthentication is true.
	if len(a.Config.AllowedEmails) == 0 && len(a.Config.AllowedDomains) == 0 && a.Config.GCPProjectID == "" {
		if a.Config.DisableAuthentication {
			return true, nil
		}
		return false, fmt.Errorf("fail-closed: AllowedEmails, AllowedDomains, and GCPProjectID are all empty; access is denied unless DISABLE_AUTHENTICATION=true")
	}

	// 1. STAGE 1: Local Guest/Admin Email Allowlist Check (Fast Path)
	if len(a.Config.AllowedEmails) > 0 {
		for _, allowedEmail := range a.Config.AllowedEmails {
			if strings.ToLower(strings.TrimSpace(allowedEmail)) == emailLower {
				return true, nil
			}
		}
	}

	// 2. STAGE 2: Local Domain Allowlist Check (Fast Path)
	if a.isAllowedDomain(emailDomain(emailLower)) {
		return true, nil
	}

	// 3. STAGE 3: GCP IAM Fallback Check
	if a.Config.GCPProjectID != "" {
		return a.IsUserAuthorizedViaIAM(ctx, emailLower)
	}

	return false, nil
}

// emailDomain returns the part after the single "@" of email, or "" when email does not have
// exactly one "@".
func emailDomain(email string) string {
	parts := strings.Split(email, "@")
	if len(parts) != 2 {
		return ""
	}
	return parts[1]
}

// isAllowedDomain reports whether domain is in AllowedDomains, ignoring case and surrounding
// spaces. An empty domain is never allowed.
func (a *Authenticator) isAllowedDomain(domain string) bool {
	domain = strings.ToLower(strings.TrimSpace(domain))
	if domain == "" {
		return false
	}
	for _, allowedDomain := range a.Config.AllowedDomains {
		if strings.ToLower(strings.TrimSpace(allowedDomain)) == domain {
			return true
		}
	}
	return false
}

// IsUserAuthorizedViaIAM queries Google Cloud Resource Manager API to verify IAM role mappings for the default TargetIAMRole.
func (a *Authenticator) IsUserAuthorizedViaIAM(ctx context.Context, email string) (bool, error) {
	return a.IsUserAuthorizedForRoleViaIAM(ctx, email, a.Config.TargetIAMRole)
}

// IsUserAuthorizedForRoleViaIAM queries Google Cloud Resource Manager API to verify IAM role mappings for a specific role.
func (a *Authenticator) IsUserAuthorizedForRoleViaIAM(ctx context.Context, email string, role string) (bool, error) {
	role = strings.TrimSpace(role)
	email = strings.ToLower(strings.TrimSpace(email))
	if role == "" || email == "" {
		return false, nil
	}
	policy, err := a.getIamPolicy(ctx)
	if err != nil {
		return false, err
	}

	userPrincipal := "user:" + email
	saPrincipal := "serviceaccount:" + email
	domainPrincipal := ""
	parts := strings.Split(email, "@")
	if len(parts) == 2 {
		domainPrincipal = "domain:" + parts[1]
	}

	logging.Info(ctx, "[IAM Pipeline] Verifying role for members", "role", role, "user", userPrincipal, "service_account", saPrincipal, "domain", domainPrincipal)

	for _, binding := range policy.Bindings {
		if binding.Role == role {
			for _, member := range binding.Members {
				memberLower := strings.ToLower(member)
				if memberLower == userPrincipal || memberLower == saPrincipal || (domainPrincipal != "" && memberLower == domainPrincipal) {
					return true, nil
				}
			}
		}
	}

	return false, nil
}

// IsAdmin checks if the given user email has admin privileges.
// If DisableAuthentication is true, it always returns true.
// If AdminIAMRole is not configured, it returns ErrAdminRoleNotConfigured.
func (a *Authenticator) IsAdmin(ctx context.Context, email string) (bool, error) {
	if a.Config.DisableAuthentication {
		return true, nil
	}
	adminRole := a.Config.AdminIAMRole
	if adminRole == "" {
		return false, ErrAdminRoleNotConfigured
	}
	return a.IsUserAuthorizedForRoleViaIAM(ctx, email, adminRole)
}
