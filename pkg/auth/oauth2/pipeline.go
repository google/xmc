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
	"fmt"
	"log"
	"strings"
	"sync"
	"time"

	resourcemanager "cloud.google.com/go/resourcemanager/apiv3"
	"cloud.google.com/go/iam/apiv1/iampb"
	"golang.org/x/oauth2"
)

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
	policyFetcher  func(context.Context) (*iampb.Policy, error)
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
	a.policyFetcher = func(ctx context.Context) (*iampb.Policy, error) {
		client, err := a.getProjectsClientLocked(ctx)
		if err != nil {
			return nil, err
		}
		req := &iampb.GetIamPolicyRequest{
			Resource: "projects/" + a.Config.GCPProjectID,
		}
		return client.GetIamPolicy(ctx, req)
	}
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

func (a *Authenticator) getProjectsClientLocked(ctx context.Context) (*resourcemanager.ProjectsClient, error) {
	if a.projectsClient != nil {
		return a.projectsClient, nil
	}
	client, err := resourcemanager.NewProjectsClient(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to create GCP Projects Resource Manager client: %w", err)
	}
	a.projectsClient = client
	return client, nil
}

func (a *Authenticator) getIamPolicy(ctx context.Context) (*iampb.Policy, error) {
	a.mu.Lock()
	defer a.mu.Unlock()

	if a.policyCache.policy != nil && time.Now().Before(a.policyCache.expiry) {
		return a.policyCache.policy, nil
	}

	policy, err := a.policyFetcher(ctx)
	if err != nil {
		return nil, err
	}

	a.policyCache = policyCacheEntry{
		policy: policy,
		expiry: time.Now().Add(2 * time.Minute),
	}
	return policy, nil
}


// IsUserAuthorized implements the 3-stage Hybrid Pipeline: Local Email Whitelist -> Domain Whitelist -> GCP IAM Policy Check.
func (a *Authenticator) IsUserAuthorized(ctx context.Context, email string) (bool, error) {
	emailLower := strings.ToLower(strings.TrimSpace(email))

	// If all whitelists and ProjectID are blank, default to open access (allow all) only if DisableAuthentication is true.
	if len(a.Config.AllowedEmails) == 0 && len(a.Config.AllowedDomains) == 0 && a.Config.GCPProjectID == "" {
		if a.Config.DisableAuthentication {
			return true, nil
		}
		return false, fmt.Errorf("fail-closed: AllowedEmails, AllowedDomains, and GCPProjectID are all empty; access is denied unless DISABLE_AUTHENTICATION=true")
	}

	// 1. STAGE 1: Local Guest/Admin Email Whitelist Check (Fast Path)
	if len(a.Config.AllowedEmails) > 0 {
		for _, allowedEmail := range a.Config.AllowedEmails {
			if strings.ToLower(strings.TrimSpace(allowedEmail)) == emailLower {
				return true, nil
			}
		}
	}

	// 2. STAGE 2: Local Domain Whitelist Check (Fast Path)
	if len(a.Config.AllowedDomains) > 0 {
		parts := strings.Split(emailLower, "@")
		if len(parts) == 2 {
			domain := parts[1]
			for _, allowedDomain := range a.Config.AllowedDomains {
				if strings.ToLower(strings.TrimSpace(allowedDomain)) == domain {
					return true, nil
				}
			}
		}
	}

	// 3. STAGE 3: GCP IAM Fallback Check
	if a.Config.GCPProjectID != "" {
		if a.Config.MockAuth {
			// Mock behavior for test environments
			return (emailLower == "mock-admin@mycompany.com" || emailLower == "mock-sa-user@mycompany.com"), nil
		}

		return a.IsUserAuthorizedViaIAM(ctx, emailLower)
	}

	return false, nil
}

// IsUserAuthorizedViaIAM queries Google Cloud Resource Manager API to verify IAM role mappings.
func (a *Authenticator) IsUserAuthorizedViaIAM(ctx context.Context, email string) (bool, error) {
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

	targetRole := a.Config.TargetIAMRole
	log.Printf("[IAM Pipeline] Verifying role %q for members user:%s serviceaccount:%s domain:%s", targetRole, email, email, domainPrincipal)

	for _, binding := range policy.Bindings {
		if binding.Role == targetRole {
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
