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
	"fmt"
	"net/url"
	"os"
	"strings"
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
}

// LoadConfig bootstraps the configuration from environment variables.
func LoadConfig() (*Config, error) {
	port := getEnv("PORT", "8080")
	mockAuth := getEnvBool("MOCK_AUTH", false)
	disableAuth := getEnvBool("DISABLE_AUTHENTICATION", false)

	allowedDomains := parseCommaSpaceSeparated(getEnv("ALLOWED_DOMAINS", ""))
	allowedEmails := parseCommaSpaceSeparated(getEnv("ALLOWED_EMAILS", ""))
	allowedOrigins := parseCommaSpaceSeparated(getEnv("ALLOWED_ORIGINS", ""))

	clientSecret, err := getEnvOrFile("CLIENT_SECRET", "")
	if err != nil {
		return nil, fmt.Errorf("failed to load CLIENT_SECRET: %w", err)
	}
	jwtSecret, err := getEnvOrFile("JWT_SECRET", "")
	if err != nil {
		return nil, fmt.Errorf("failed to load JWT_SECRET: %w", err)
	}
	if jwtSecret == "" {
		if mockAuth || disableAuth {
			// Local/dev only: no real secret configured, but auth is off anyway.
			jwtSecret = devOnlyJWTSecret
		} else {
			return nil, fmt.Errorf("JWT_SECRET must be set (via JWT_SECRET or JWT_SECRET_FILE) when authentication is enabled")
		}
	} else if !mockAuth && !disableAuth && len(jwtSecret) < minJWTSecretLen {
		return nil, fmt.Errorf("JWT_SECRET must be at least %d bytes when authentication is enabled", minJWTSecretLen)
	}

	frontendURL := getEnv("FRONTEND_URL", "http://localhost:8080")
	redirectURL := getEnv("REDIRECT_URL", "http://localhost:8080/api/callback")

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

	return &Config{
		Port:                  port,
		MockAuth:              mockAuth,
		DisableAuthentication: disableAuth,
		AllowedDomains:        allowedDomains,
		AllowedEmails:         allowedEmails,
		AllowedOrigins:        allowedOrigins,
		GCPProjectID:          getEnv("GCP_PROJECT_ID", ""),
		TargetIAMRole:         getEnv("AUTHORIZED_IAM_ROLE", "roles/viewer"),
		AdminIAMRole:          getEnv("ADMIN_IAM_ROLE", "roles/owner"),
		ClientID:              getEnv("CLIENT_ID", ""),
		ClientSecret:          clientSecret,
		JWTSecret:             jwtSecret,
		RedirectURL:           redirectURL,
		AuthURL:               getEnv("AUTH_URL", "https://accounts.google.com/o/oauth2/v2/auth"),
		TokenURL:              getEnv("TOKEN_URL", "https://oauth2.googleapis.com/token"),
		APIURL:                getEnv("API_URL", "https://openidconnect.googleapis.com/v1/userinfo"),
		UsePKCE:               getEnvBool("USE_PKCE", true),
		FrontendURL:           frontendURL,
		SecureCookie:          getEnvBool("SECURE_COOKIE", !mockAuth),
		TrustXForwardedProto:  getEnvBool("TRUST_X_FORWARDED_PROTO", false),
		CookieDomain:          cookieDomain,
		AllowedHosts:          allowedHosts,
	}, nil
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
