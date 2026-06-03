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
	"os"
	"strings"
)

// Config holds the configuration properties for OAuth2 and the authorization checks.
type Config struct {
	Port           string
	MockAuth       bool
	AllowedDomains []string
	AllowedEmails  []string
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
func LoadConfig() *Config {
	port := getEnv("PORT", "8080")
	mockAuth := getEnvBool("MOCK_AUTH", false)

	allowedDomainsStr := getEnv("ALLOWED_DOMAINS", "")
	var allowedDomains []string
	for _, part := range strings.FieldsFunc(allowedDomainsStr, func(r rune) bool {
		return r == ' ' || r == ','
	}) {
		part = strings.TrimSpace(part)
		if part != "" {
			allowedDomains = append(allowedDomains, part)
		}
	}

	allowedEmailsStr := getEnv("ALLOWED_EMAILS", "")
	var allowedEmails []string
	for _, part := range strings.FieldsFunc(allowedEmailsStr, func(r rune) bool {
		return r == ' ' || r == ','
	}) {
		part = strings.TrimSpace(part)
		if part != "" {
			allowedEmails = append(allowedEmails, part)
		}
	}

	return &Config{
		Port:           port,
		MockAuth:       mockAuth,
		AllowedDomains: allowedDomains,
		AllowedEmails:  allowedEmails,
		GCPProjectID:   getEnv("GCP_PROJECT_ID", ""),
		TargetIAMRole:  getEnv("AUTHORIZED_IAM_ROLE", "roles/viewer"),
		ClientID:       getEnv("CLIENT_ID", ""),
		ClientSecret:   getEnvOrFile("CLIENT_SECRET", ""),
		JWTSecret:      getEnvOrFile("JWT_SECRET", "default-jwt-secret-for-local-dev-only-12345"),
		RedirectURL:    getEnv("REDIRECT_URL", "http://localhost:8080/api/callback"),
		AuthURL:        getEnv("AUTH_URL", "https://accounts.google.com/o/oauth2/v2/auth"),
		TokenURL:       getEnv("TOKEN_URL", "https://oauth2.googleapis.com/token"),
		APIURL:         getEnv("API_URL", "https://openidconnect.googleapis.com/v1/userinfo"),
		UsePKCE:        getEnvBool("USE_PKCE", true),
		FrontendURL:    getEnv("FRONTEND_URL", "http://localhost:8080"),
		SecureCookie:   getEnvBool("SECURE_COOKIE", !mockAuth),
		TrustXForwardedProto: getEnvBool("TRUST_X_FORWARDED_PROTO", false),
	}
}

func getEnvOrFile(key, defaultVal string) string {
	if fileVar, ok := os.LookupEnv(key + "_FILE"); ok {
		if content, readErr := os.ReadFile(fileVar); readErr == nil {
			return strings.TrimSpace(string(content))
		}
	}
	if val, ok := os.LookupEnv(key); ok {
		if _, err := os.Stat(val); err == nil {
			if content, readErr := os.ReadFile(val); readErr == nil {
				return strings.TrimSpace(string(content))
			}
		}
		return val
	}
	return defaultVal
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
