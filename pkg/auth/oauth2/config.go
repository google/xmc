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
	"os"
	"strings"
)

// Config holds the configuration properties for OAuth2 and the authorization checks.
type Config struct {
	Port           string
	MockAuth       bool
	DisableAuthentication bool
	AllowedDomains []string
	AllowedEmails  []string
	AllowedOrigins []string
	GCPProjectID   string
	TargetIAMRole  string
	ClientID       string
	ClientSecret   string
	JWTSecret      string
	RedirectURL    string
	AuthURL        string
	TokenURL       string
	APIURL         string
	UsePKCE        bool
	FrontendURL    string
	SecureCookie   bool
	TrustXForwardedProto bool
}

// LoadConfig bootstraps the configuration from environment variables.
func LoadConfig() (*Config, error) {
	port := getEnv("PORT", "8080")
	mockAuth := getEnvBool("MOCK_AUTH", false)

	allowedDomains := parseCommaSpaceSeparated(getEnv("ALLOWED_DOMAINS", ""))
	allowedEmails := parseCommaSpaceSeparated(getEnv("ALLOWED_EMAILS", ""))
	allowedOrigins := parseCommaSpaceSeparated(getEnv("ALLOWED_ORIGINS", ""))

	clientSecret, err := getEnvOrFile("CLIENT_SECRET", "")
	if err != nil {
		return nil, fmt.Errorf("failed to load CLIENT_SECRET: %w", err)
	}
	jwtSecret, err := getEnvOrFile("JWT_SECRET", "default-jwt-secret-for-local-dev-only-12345")
	if err != nil {
		return nil, fmt.Errorf("failed to load JWT_SECRET: %w", err)
	}

	return &Config{
		Port:           port,
		MockAuth:       mockAuth,
		DisableAuthentication: getEnvBool("DISABLE_AUTHENTICATION", false),
		AllowedDomains: allowedDomains,
		AllowedEmails:  allowedEmails,
		AllowedOrigins: allowedOrigins,
		GCPProjectID:   getEnv("GCP_PROJECT_ID", ""),
		TargetIAMRole:  getEnv("AUTHORIZED_IAM_ROLE", "roles/viewer"),
		ClientID:       getEnv("CLIENT_ID", ""),
		ClientSecret:   clientSecret,
		JWTSecret:      jwtSecret,
		RedirectURL:    getEnv("REDIRECT_URL", "http://localhost:8080/api/callback"),
		AuthURL:        getEnv("AUTH_URL", "https://accounts.google.com/o/oauth2/v2/auth"),
		TokenURL:       getEnv("TOKEN_URL", "https://oauth2.googleapis.com/token"),
		APIURL:         getEnv("API_URL", "https://openidconnect.googleapis.com/v1/userinfo"),
		UsePKCE:        getEnvBool("USE_PKCE", true),
		FrontendURL:    getEnv("FRONTEND_URL", "http://localhost:8080"),
		SecureCookie:   getEnvBool("SECURE_COOKIE", !mockAuth),
		TrustXForwardedProto: getEnvBool("TRUST_X_FORWARDED_PROTO", false),
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
