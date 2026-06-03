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
	"log"
	"net/http"
	"strings"

	"github.com/golang-jwt/jwt/v5"
)

type contextKey string

// UserClaimsContextKey is the key used to store and retrieve User session claims from the request context.
const UserClaimsContextKey contextKey = "user_claims"

// GetClaims extracts the session claims from a request context if present.
func GetClaims(ctx context.Context) (*SessionClaims, bool) {
	claims, ok := ctx.Value(UserClaimsContextKey).(*SessionClaims)
	return claims, ok
}

// Authenticate is a middleware verifying interactive session cookies or programmatic Bearer tokens.
func (a *Authenticator) Authenticate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 1. Check session cookie first
		cookie, err := r.Cookie("session_token")
		if err == nil && cookie.Value != "" {
			claims, err := ParseSessionToken(cookie.Value, a.JWTSecret)
			if err == nil {
				// ENFORCE real-time authorization re-verification
				authorized, authErr := a.IsUserAuthorized(r.Context(), claims.Email)
				if authErr == nil && authorized {
					r.Header.Set("Authorization", "Bearer "+cookie.Value)
					r.Header.Set("x-goog-user-email", claims.Email)
					r.Header.Set("x-goog-iap-jwt-assertion", cookie.Value)
					ctx := context.WithValue(r.Context(), UserClaimsContextKey, claims)
					next.ServeHTTP(w, r.WithContext(ctx))
					return
				}

				if authErr != nil {
					log.Printf("[Middleware Auth] Cookie IAM authorization check error: %v", authErr)
					http.Error(w, "Internal authorization check error", http.StatusInternalServerError)
					return
				}

				log.Printf("[Middleware Auth] Access forbidden: Cookie owner %q has been de-authorized", claims.Email)
				http.Error(w, "Forbidden: Identity has been de-authorized", http.StatusForbidden)
				return
			}
		}

		// 2. Check Bearer Authorization header
		authHeader := r.Header.Get("Authorization")
		if strings.HasPrefix(authHeader, "Bearer ") {
			tokenStr := strings.TrimPrefix(authHeader, "Bearer ")

			// 2.1 Mock SA token support
			if a.Config.MockAuth && tokenStr == "mock-sa-token" {
				claims := &SessionClaims{
					Email:   "service-account@mycompany.com",
					Name:    "Mock Service Account",
					Picture: "https://example.com/sa-avatar.png",
					RegisteredClaims: jwt.RegisteredClaims{
						Issuer: "google-service-account",
					},
				}
				ctx := context.WithValue(r.Context(), UserClaimsContextKey, claims)
				next.ServeHTTP(w, r.WithContext(ctx))
				return
			}

			// 2.2 Verify if the Bearer token is a signed session JWT token
			claims, err := ParseSessionToken(tokenStr, a.JWTSecret)
			if err == nil {
				// ENFORCE real-time authorization re-verification
				authorized, authErr := a.IsUserAuthorized(r.Context(), claims.Email)
				if authErr == nil && authorized {
					r.Header.Set("x-goog-user-email", claims.Email)
					r.Header.Set("x-goog-iap-jwt-assertion", tokenStr)
					ctx := context.WithValue(r.Context(), UserClaimsContextKey, claims)
					next.ServeHTTP(w, r.WithContext(ctx))
					return
				}

				if authErr != nil {
					log.Printf("[Middleware Auth] Token IAM authorization check error: %v", authErr)
					http.Error(w, "Internal authorization check error", http.StatusInternalServerError)
					return
				}

				log.Printf("[Middleware Auth] Access forbidden: Token owner %q has been de-authorized", claims.Email)
				http.Error(w, "Forbidden: Identity has been de-authorized", http.StatusForbidden)
				return
			}

			// 2.3 Verify as Google-signed OIDC ID Token (programmatic Service Accounts)
			if a.Config.ClientID != "" {
				payload, idTokenErr := a.idTokenValidator(r.Context(), tokenStr, a.Config.ClientID)
				if idTokenErr == nil && payload != nil {
					emailClaim, _ := payload.Claims["email"].(string)
					nameClaim, _ := payload.Claims["name"].(string)
					pictureClaim, _ := payload.Claims["picture"].(string)

					authorized, authErr := a.IsUserAuthorized(r.Context(), emailClaim)
					if authErr == nil && authorized {
						r.Header.Set("x-goog-user-email", emailClaim)
						r.Header.Set("x-goog-iap-jwt-assertion", tokenStr)
						googleClaims := &SessionClaims{
							Email:   emailClaim,
							Name:    nameClaim,
							Picture: pictureClaim,
							RegisteredClaims: jwt.RegisteredClaims{
								Issuer: payload.Issuer,
							},
						}
						ctx := context.WithValue(r.Context(), UserClaimsContextKey, googleClaims)
						next.ServeHTTP(w, r.WithContext(ctx))
						return
					}
					
					if authErr != nil {
						log.Printf("[Middleware Auth] IAM authorization check error: %v", authErr)
						http.Error(w, "Internal authorization check error", http.StatusInternalServerError)
						return
					}

					log.Printf("[Middleware Auth] Access forbidden: principal %q is unauthorized", emailClaim)
					http.Error(w, "Forbidden: Identity is unauthorized", http.StatusForbidden)
					return
				}
			}
		}

		http.Error(w, "Unauthorized: Session token is invalid or missing", http.StatusUnauthorized)
	})
}

// CORSMiddleware configures cross-origin access.
func (a *Authenticator) CORSMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		origin := r.Header.Get("Origin")
		allowedOrigin := a.Config.FrontendURL
		if origin != "" {
			if origin == a.Config.FrontendURL {
				allowedOrigin = origin
			} else {
				for _, ao := range a.Config.AllowedOrigins {
					if origin == ao {
						allowedOrigin = origin
						break
					}
				}
			}
		}
		w.Header().Set("Access-Control-Allow-Origin", allowedOrigin)
		w.Header().Set("Access-Control-Allow-Credentials", "true")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")

		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusOK)
			return
		}

		next.ServeHTTP(w, r)
	})
}

// SecurityHeadersMiddleware applies secure response headers.
func (a *Authenticator) SecurityHeadersMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-XSS-Protection", "1; mode=block")
		w.Header().Set("Referrer-Policy", "strict-origin-when-cross-origin")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self' 'unsafe-inline'; style-src 'self' 'unsafe-inline' https://fonts.googleapis.com; font-src 'self' https://fonts.gstatic.com; img-src 'self' data: https:;")

		if r.TLS != nil || (a.Config.TrustXForwardedProto && r.Header.Get("X-Forwarded-Proto") == "https") {
			w.Header().Set("Strict-Transport-Security", "max-age=63072000; includeSubDomains; preload")
		}

		next.ServeHTTP(w, r)
	})
}
