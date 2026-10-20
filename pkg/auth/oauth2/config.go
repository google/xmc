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
	"net/url"
	"os"
	"strings"
	"time"

	"xmanager_cloud/internal/logging"
)

// devOnlyJWTSecret is a well-known, non-secret value used to sign session tokens
// only in local/dev mode (MOCK_AUTH or DISABLE_AUTHENTICATION). It must never be
// used when authentication is enabled: when JWT_SECRET is unset in that case,
// LoadConfig fails fast instead of silently signing tokens with this known value.
const devOnlyJWTSecret = "default-jwt-secret-for-local-dev-only-12345"

// minJWTSecretLen is the minimum length (bytes) required for JWT_SECRET when
// authentication is enabled, so operators cannot configure a weak, easily
// brute-forced HS256 signing key. 32 bytes matches the HS256 output size.
const minJWTSecretLen = 32

const defaultSessionTTL = time.Hour

// CLI session token lifetime bounds (CLI_SESSION_TTL). CLI tokens cannot be refreshed, so the
// default covers a working day; the maximum caps how long a copied token stays usable.
const (
	defaultCLISessionTTL = 12 * time.Hour
	minCLISessionTTL     = time.Minute
	maxCLISessionTTL     = 30 * 24 * time.Hour
)

// Config holds the configuration properties for OAuth2 and the authorization checks.
type Config struct {
	Port                  string
	MockAuth              bool
	DisableAuthentication bool
	AllowedDomains        []string
	AllowedEmails         []string
	AllowedOrigins        []string
	GCPProjectID          string
	TargetIAMRole         string
	AdminIAMRole          string
	ClientID              string
	ClientSecret          string
	JWTSecret             string
	SessionTTL            time.Duration
	CLISessionTTL         time.Duration
	RedirectURL           string
	AuthURL               string
	TokenURL              string
	APIURL                string
	UsePKCE               bool
	FrontendURL           string
	SecureCookie          bool
	TrustXForwardedProto  bool
	CookieDomain          string
	AllowedHosts          []string
	OIDCIssuerURL         string
	Username              string
	Password              string
	// AdditionalAudiences are OAuth client IDs, besides ClientID, whose Google ID tokens are
	// accepted (e.g. a deployer-owned Desktop client). See AcceptedAudiences.
	AdditionalAudiences []string
	// EmailVerification (OIDC_EMAIL_VERIFICATION) adapts the sign-in and JWKS email_verified checks
	// to non-Google identity providers. The zero value behaves as EmailVerificationRequire.
	EmailVerification EmailVerification
}

// LoadConfig bootstraps the configuration from environment variables.
func LoadConfig() (*Config, error) {
	mockAuth := getEnvBool("MOCK_AUTH", false)
	disableAuth := getEnvBool("DISABLE_AUTHENTICATION", false)

	if mockAuth || disableAuth {
		logging.Warn(context.Background(), "Non-production auth mode enabled", "mock_auth", mockAuth, "disable_authentication", disableAuth)
	}

	clientSecret, err := loadClientSecret()
	if err != nil {
		return nil, err
	}

	jwtSecret, err := loadJWTSecret(mockAuth, disableAuth)
	if err != nil {
		return nil, err
	}

	sessionTTL, err := time.ParseDuration(getEnv("SESSION_TTL", defaultSessionTTL.String()))
	if err != nil {
		return nil, fmt.Errorf("invalid `SESSION_TTL`: %w", err)
	}
	if sessionTTL < time.Second {
		return nil, fmt.Errorf("`SESSION_TTL` must be at least `1s`")
	}
	cliSessionTTL, err := loadCLISessionTTL()
	if err != nil {
		return nil, err
	}

	username, _ := getEnvOrFile("OIDC_USERNAME", getEnv("AUTH_USERNAME", ""))
	password, _ := getEnvOrFile("OIDC_PASSWORD", getEnv("AUTH_PASSWORD", ""))

	frontendURL := getEnv("FRONTEND_URL", "http://localhost:8080")
	redirectURL := getEnv("REDIRECT_URL", "http://localhost:8080/api/callback")
	cookieDomain, allowedHosts := loadHostSettings(frontendURL, redirectURL)

	oidcIssuerURL := getEnv("OIDC_ISSUER_URL", "")
	defaultAuthURL, defaultTokenURL, defaultAPIURL := loadDefaultOIDCURLs(oidcIssuerURL)

	clientID := getEnv("CLIENT_ID", "")
	if clientID == "" {
		clientID = getEnv("OIDC_CLIENT_ID", "")
	}

	additionalAudiences, err := parseAdditionalAudiences(getEnv("ADDITIONAL_AUDIENCES", ""))
	if err != nil {
		return nil, err
	}

	cfg := &Config{
		Port:                  getEnv("PORT", "8080"),
		MockAuth:              mockAuth,
		DisableAuthentication: disableAuth,
		AllowedDomains:        parseCommaSpaceSeparated(getEnv("ALLOWED_DOMAINS", "")),
		AllowedEmails:         parseCommaSpaceSeparated(getEnv("ALLOWED_EMAILS", "")),
		AllowedOrigins:        parseCommaSpaceSeparated(getEnv("ALLOWED_ORIGINS", "")),
		GCPProjectID:          getEnv("GCP_PROJECT_ID", ""),
		TargetIAMRole:         getEnv("AUTHORIZED_IAM_ROLE", "roles/viewer"),
		AdminIAMRole:          getEnv("ADMIN_IAM_ROLE", "roles/owner"),
		ClientID:              clientID,
		AdditionalAudiences:   additionalAudiences,
		ClientSecret:          clientSecret,
		JWTSecret:             jwtSecret,
		SessionTTL:            sessionTTL,
		CLISessionTTL:         cliSessionTTL,
		Username:              username,
		Password:              password,
		RedirectURL:           redirectURL,
		AuthURL:               getEnv("AUTH_URL", defaultAuthURL),
		TokenURL:              getEnv("TOKEN_URL", defaultTokenURL),
		APIURL:                getEnv("API_URL", defaultAPIURL),
		UsePKCE:               getEnvBool("USE_PKCE", true),
		FrontendURL:           frontendURL,
		SecureCookie:          getEnvBool("SECURE_COOKIE", !mockAuth),
		TrustXForwardedProto:  getEnvBool("TRUST_X_FORWARDED_PROTO", false),
		CookieDomain:          cookieDomain,
		AllowedHosts:          allowedHosts,
		OIDCIssuerURL:         oidcIssuerURL,
	}
	if err := validateEntraAuthority(cfg.AuthURL); err != nil {
		return nil, err
	}
	cfg.EmailVerification, err = loadEmailVerification(context.Background(), cfg)
	if err != nil {
		return nil, err
	}
	return cfg, nil
}

func loadCLISessionTTL() (time.Duration, error) {
	ttl, err := time.ParseDuration(getEnv("CLI_SESSION_TTL", defaultCLISessionTTL.String()))
	if err != nil {
		return 0, fmt.Errorf("invalid `CLI_SESSION_TTL`: %w", err)
	}
	if ttl < minCLISessionTTL || ttl > maxCLISessionTTL {
		return 0, fmt.Errorf("`CLI_SESSION_TTL` must be between `%s` and `%s`", minCLISessionTTL, maxCLISessionTTL)
	}
	return ttl, nil
}

func loadClientSecret() (string, error) {
	clientSecret, err := getEnvOrFile("CLIENT_SECRET", "")
	if err != nil {
		return "", fmt.Errorf("failed to load CLIENT_SECRET: %w", err)
	}
	if clientSecret != "" {
		return clientSecret, nil
	}
	clientSecret, err = getEnvOrFile("OIDC_CLIENT_SECRET", "")
	if err != nil {
		return "", fmt.Errorf("failed to load OIDC_CLIENT_SECRET: %w", err)
	}
	if clientSecret == "" {
		logging.Warn(context.Background(), "Neither CLIENT_SECRET nor OIDC_CLIENT_SECRET is set; OAuth2 code exchange may fail if required by IdP")
	}
	return clientSecret, nil
}

func loadJWTSecret(mockAuth, disableAuth bool) (string, error) {
	jwtSecret, err := getEnvOrFile("JWT_SECRET", "")
	if err != nil {
		return "", fmt.Errorf("failed to load JWT_SECRET: %w", err)
	}
	if jwtSecret == "" {
		if mockAuth || disableAuth {
			logging.Warn(context.Background(), "Non-production auth active: using ephemeral dev-only JWT secret", "mock_auth", mockAuth, "disable_authentication", disableAuth)
			return devOnlyJWTSecret, nil
		}
		return "", fmt.Errorf("jwt_secret must be set (via JWT_SECRET or JWT_SECRET_FILE) when authentication is enabled")
	}
	if !mockAuth && !disableAuth && len(jwtSecret) < minJWTSecretLen {
		return "", fmt.Errorf("jwt_secret must be at least %d bytes when authentication is enabled", minJWTSecretLen)
	}
	return jwtSecret, nil
}

func loadHostSettings(frontendURL, redirectURL string) (string, []string) {
	var cookieDomain string
	var allowedHosts []string

	if u, err := url.Parse(frontendURL); err == nil {
		host := u.Hostname()
		if !isLocalhost(host) {
			cookieDomain = host
		}
		allowedHosts = append(allowedHosts, strings.ToLower(u.Host))
	}
	if u, err := url.Parse(redirectURL); err == nil {
		allowedHosts = append(allowedHosts, strings.ToLower(u.Host))
	}
	return cookieDomain, allowedHosts
}

func loadDefaultOIDCURLs(oidcIssuerURL string) (string, string, string) {
	if oidcIssuerURL == "" {
		return "https://accounts.google.com/o/oauth2/v2/auth",
			"https://oauth2.googleapis.com/token",
			"https://openidconnect.googleapis.com/v1/userinfo"
	}
	issuer := strings.TrimRight(oidcIssuerURL, "/")
	return fmt.Sprintf("%s/auth", issuer),
		fmt.Sprintf("%s/token", issuer),
		fmt.Sprintf("%s/userinfo", issuer)
}

// IsNonProdAuth identifies whether non-production or local IdP (e.g., Dex, localhost, mock auth) is active.
func (c *Config) IsNonProdAuth() bool {
	if c.MockAuth || c.DisableAuthentication {
		return true
	}
	if c.OIDCIssuerURL != "" {
		if u, err := url.Parse(c.OIDCIssuerURL); err != nil || u.Hostname() != "accounts.google.com" {
			return true
		}
	}
	if c.AuthURL != "" {
		if u, err := url.Parse(c.AuthURL); err != nil || u.Hostname() != "accounts.google.com" {
			return true
		}
	}
	return false
}

func parseCommaSpaceSeparated(val string) []string {
	return strings.FieldsFunc(val, func(r rune) bool {
		return r == ' ' || r == ','
	})
}

func getEnvOrFile(key, defaultVal string) (string, error) {
	if fileVar, ok := os.LookupEnv(key + "_FILE"); ok {
		content, readErr := os.ReadFile(fileVar)
		if readErr != nil {
			return "", readErr
		}
		return strings.TrimSpace(string(content)), nil
	}
	val, ok := os.LookupEnv(key)
	if !ok {
		return defaultVal, nil
	}
	if _, err := os.Stat(val); err != nil {
		return val, nil // Treat as literal string
	}
	content, readErr := os.ReadFile(val)
	if readErr != nil {
		return "", readErr
	}
	return strings.TrimSpace(string(content)), nil
}

func getEnv(key, defaultVal string) string {
	if val, ok := os.LookupEnv(key); ok {
		return val
	}
	return defaultVal
}

func getEnvBool(key string, defaultVal bool) bool {
	if val, ok := os.LookupEnv(key); ok {
		val = strings.ToLower(strings.TrimSpace(val))
		return val == "true" || val == "1" || val == "yes"
	}
	return defaultVal
}
