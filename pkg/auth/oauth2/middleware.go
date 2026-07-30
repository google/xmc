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
	"net/http"
	"net/url"
	"slices"
	"strings"

	"github.com/golang-jwt/jwt/v5"
	"github.com/rs/cors"
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
		if a.authenticateCookie(w, r, next) {
			return
		}
		if a.authenticateBearer(w, r, next) {
			return
		}
		http.Error(w, "Unauthorized: Session token is invalid or missing", http.StatusUnauthorized)
	})
}

// SecurityMiddlewares wraps the handler with all standard security middlewares
// SecurityHeaders -> CrossOriginProtection -> CORS.
func (a *Authenticator) SecurityMiddlewares(next http.Handler) http.Handler {
	log.Printf("[Middleware Auth] Building security middleware chain (Security Headers -> CSRF -> CORS)")
	handler := next
	handler = a.corsMiddleware(handler)
	handler = a.crossOriginProtection(handler)
	handler = a.securityHeadersMiddleware(handler)
	return handler
}

// authenticateCookie checks for and validates the session_token cookie.
// It returns true if it handled the request (authenticated the request, or written an error status).
// It returns false if no cookie was found or if cookie verification failed (falling back to bearer tokens).
func (a *Authenticator) authenticateCookie(w http.ResponseWriter, r *http.Request, next http.Handler) bool {
	cookie, err := r.Cookie("session_token")
	if err != nil || cookie.Value == "" {
		return false
	}

	claims, err := ParseSessionToken(cookie.Value, a.JWTSecret)
	if err != nil {
		return false
	}

	authorized, authErr := a.IsUserAuthorized(r.Context(), claims.Email)
	if authErr != nil {
		log.Printf("[Middleware Auth] Cookie IAM authorization check error: %v", authErr)
		http.Error(w, "Internal authorization check error", http.StatusInternalServerError)
		return true
	}

	if !authorized {
		log.Printf("[Middleware Auth] Access forbidden: Cookie owner %q has been de-authorized", claims.Email)
		http.Error(w, "Forbidden: Identity has been de-authorized", http.StatusForbidden)
		return true
	}

	r.Header.Set("Authorization", "Bearer "+cookie.Value)
	r.Header.Set("x-goog-user-email", claims.Email)
	r.Header.Set("x-goog-iap-jwt-assertion", cookie.Value)
	ctx := context.WithValue(r.Context(), UserClaimsContextKey, claims)
	next.ServeHTTP(w, r.WithContext(ctx))
	return true
}

// authenticateBearer checks for and validates the Authorization Bearer header.
// It returns true if it handled the request (authenticated the request, or written an error status).
// It returns false if no token was found or if validation failed.
func (a *Authenticator) authenticateBearer(w http.ResponseWriter, r *http.Request, next http.Handler) bool {
	authHeader := r.Header.Get("Authorization")
	if !strings.HasPrefix(authHeader, "Bearer ") {
		return false
	}

	tokenStr := strings.TrimPrefix(authHeader, "Bearer ")

	if a.authenticateSessionJWT(w, r, tokenStr, next) {
		return true
	}
	if a.authenticateOIDC(w, r, tokenStr, next) {
		return true
	}

	return false
}

// authenticateSessionJWT attempts to parse the token as a signed session JWT token.
// If it succeeds, it verifies authorization and handles the request, returning true.
// If parsing fails, it returns false to fall back to OIDC token verification.
func (a *Authenticator) authenticateSessionJWT(w http.ResponseWriter, r *http.Request, tokenStr string, next http.Handler) bool {
	claims, err := ParseSessionToken(tokenStr, a.JWTSecret)
	if err != nil {
		return false
	}

	authorized, authErr := a.IsUserAuthorized(r.Context(), claims.Email)
	if authErr != nil {
		log.Printf("[Middleware Auth] Token IAM authorization check error: %v", authErr)
		http.Error(w, "Internal authorization check error", http.StatusInternalServerError)
		return true
	}

	if !authorized {
		log.Printf("[Middleware Auth] Access forbidden: Token owner %q has been de-authorized", claims.Email)
		http.Error(w, "Forbidden: Identity has been de-authorized", http.StatusForbidden)
		return true
	}

	r.Header.Set("x-goog-user-email", claims.Email)
	r.Header.Set("x-goog-iap-jwt-assertion", tokenStr)
	ctx := context.WithValue(r.Context(), UserClaimsContextKey, claims)
	next.ServeHTTP(w, r.WithContext(ctx))
	return true
}

// authenticateOIDC attempts to validate the token as a Google-signed OIDC ID Token.
// If it succeeds, it verifies authorization and handles the request, returning true.
// If validation fails, it returns false.
func (a *Authenticator) authenticateOIDC(w http.ResponseWriter, r *http.Request, tokenStr string, next http.Handler) bool {
	if a.Config.ClientID == "" {
		return false
	}

	payload, idTokenErr := a.IDTokenValidator(r.Context(), tokenStr, a.Config.ClientID)
	if idTokenErr != nil || payload == nil {
		return false
	}

	emailClaim, _ := payload.Claims["email"].(string)
	nameClaim, _ := payload.Claims["name"].(string)
	pictureClaim, _ := payload.Claims["picture"].(string)

	authorized, authErr := a.IsUserAuthorized(r.Context(), emailClaim)
	if authErr != nil {
		log.Printf("[Middleware Auth] IAM authorization check error: %v", authErr)
		http.Error(w, "Internal authorization check error", http.StatusInternalServerError)
		return true
	}

	if !authorized {
		log.Printf("[Middleware Auth] Access forbidden: principal %q is unauthorized", emailClaim)
		http.Error(w, "Forbidden: Identity is unauthorized", http.StatusForbidden)
		return true
	}

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
	return true
}

// corsMiddleware configures cross-origin access using rs/cors package.
func (a *Authenticator) corsMiddleware(next http.Handler) http.Handler {
	log.Printf("[Middleware CORS] Building CORS middleware")
	origins := make([]string, len(a.Config.AllowedOrigins))
	copy(origins, a.Config.AllowedOrigins)
	if a.Config.FrontendURL != "" {
		frontendOrigin := a.Config.FrontendURL
		if u, err := url.Parse(a.Config.FrontendURL); err == nil && u.Scheme != "" && u.Host != "" {
			frontendOrigin = fmt.Sprintf("%s://%s", u.Scheme, u.Host)
		}
		if !slices.Contains(origins, frontendOrigin) {
			origins = append(origins, frontendOrigin)
		}
	}

	log.Printf("[Middleware CORS] configuring CORS middleware with allowed origins: %v", origins)
	c := cors.New(cors.Options{
		AllowedOrigins:   origins,
		AllowedMethods:   []string{"GET", "POST", "PUT", "PATCH", "DELETE", "OPTIONS"},
		AllowedHeaders:   []string{"*"},
		ExposedHeaders:   []string{"grpc-metadata-content-type", "grpc-status", "grpc-message"},
		AllowCredentials: true,
		MaxAge:           300,
	})
	return c.Handler(next)
}

// securityHeadersMiddleware applies secure response headers.
func (a *Authenticator) securityHeadersMiddleware(next http.Handler) http.Handler {
	log.Printf("[Middleware Headers] Building Security Headers middleware")
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

// crossOriginProtection sets up the standard CrossOriginProtection filter.
func (a *Authenticator) crossOriginProtection(next http.Handler) http.Handler {
	log.Printf("[Middleware COP] Building Cross-Origin Protection (CSRF) middleware")
	cop := http.NewCrossOriginProtection()
	for _, origin := range a.Config.AllowedOrigins {
		if err := cop.AddTrustedOrigin(origin); err != nil {
			log.Printf("[Middleware COP] Failed to add allowed origin %q: %v", origin, err)
		}
	}
	addFrontendTrustedOrigin(cop, a.Config.FrontendURL)
	return cop.Handler(next)
}

func addFrontendTrustedOrigin(cop *http.CrossOriginProtection, frontendURL string) {
	if frontendURL == "" {
		return
	}
	u, err := url.Parse(frontendURL)
	if err != nil {
		log.Printf("[Middleware COP] Failed to parse frontend URL %q: %v", frontendURL, err)
		return
	}
	if u.Scheme == "" || u.Host == "" {
		log.Printf("[Middleware COP] Frontend URL %q is missing scheme or host", frontendURL)
		return
	}
	frontendOrigin := fmt.Sprintf("%s://%s", u.Scheme, u.Host)
	if err := cop.AddTrustedOrigin(frontendOrigin); err != nil {
		log.Printf("[Middleware COP] Failed to add frontend origin %q: %v", frontendOrigin, err)
	}
}
