// Copyright 2026 Google LLC
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

package main

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2/lib/driver"
	"github.com/google/go-cmp/cmp"
	"github.com/grpc-ecosystem/grpc-gateway/v2/runtime"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"xmanager_cloud/cmd/dashboard_service/server"
	"xmanager_cloud/internal/dashboard/dataengine"
	"xmanager_cloud/pkg/auth/oauth2"
	xmcbigquery "xmanager_cloud/pkg/bigquery"
	bqconfig "xmanager_cloud/pkg/bigquery/componentconfig"
	"xmanager_cloud/pkg/clickhouse"
	"xmanager_cloud/pkg/clickhouse/componentconfig"
	"xmanager_cloud/pkg/components/crreader"
	"xmanager_cloud/pkg/components/crreader/crreadertest"
)

func TestSetupHTTPHandler(t *testing.T) {
	jwtSecret := "test-secret-12345678901234567890"
	authenticator := oauth2.NewAuthenticator(&oauth2.Config{
		JWTSecret:     jwtSecret,
		AllowedEmails: []string{"user@example.com"},
	})

	validToken, err := oauth2.GenerateSessionToken("user@example.com", "User", "", time.Hour, []byte(jwtSecret))
	if err != nil {
		t.Fatalf("GenerateSessionToken failed: %v", err)
	}

	dummyGWMux := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"dashboards": []}`))
	})

	tests := []struct {
		name           string
		path           string
		method         string
		authHeader     string
		cookie         *http.Cookie
		readinessCheck func(context.Context) error
		wantStatus     int
		wantBody       string
	}{
		{
			name:       "healthz probe returns OK without auth",
			path:       "/healthz",
			method:     http.MethodGet,
			wantStatus: http.StatusOK,
			wantBody:   "OK",
		},
		{
			name:       "readyz probe returns OK without auth and nil check",
			path:       "/readyz",
			method:     http.MethodGet,
			wantStatus: http.StatusOK,
			wantBody:   "OK",
		},
		{
			name:   "readyz probe returns OK with healthy check",
			path:   "/readyz",
			method: http.MethodGet,
			readinessCheck: func(ctx context.Context) error {
				return nil
			},
			wantStatus: http.StatusOK,
			wantBody:   "OK",
		},
		{
			name:   "readyz probe returns 503 with failing check",
			path:   "/readyz",
			method: http.MethodGet,
			readinessCheck: func(ctx context.Context) error {
				return errors.New("spanner connection refused")
			},
			wantStatus: http.StatusServiceUnavailable,
			wantBody:   "spanner connection refused",
		},
		{
			name:       "root fallback probe returns OK without auth",
			path:       "/",
			method:     http.MethodGet,
			wantStatus: http.StatusOK,
			wantBody:   "Dashboard service OK",
		},
		{
			name:       "unauthenticated request to API endpoint is rejected with 401",
			path:       "/v1/dashboards",
			method:     http.MethodGet,
			wantStatus: http.StatusUnauthorized,
		},
		{
			name:       "authenticated request with Bearer token passes to gateway",
			path:       "/v1/dashboards",
			method:     http.MethodGet,
			authHeader: "Bearer " + validToken,
			wantStatus: http.StatusOK,
			wantBody:   `{"dashboards": []}`,
		},
		{
			name:       "authenticated request with session cookie passes to gateway",
			path:       "/v1/dashboards",
			method:     http.MethodGet,
			cookie:     &http.Cookie{Name: "session_token", Value: validToken},
			wantStatus: http.StatusOK,
			wantBody:   `{"dashboards": []}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			handler := setupHTTPHandler(authenticator, dummyGWMux, tt.readinessCheck)

			req := httptest.NewRequest(tt.method, tt.path, nil)
			if tt.authHeader != "" {
				req.Header.Set("Authorization", tt.authHeader)
			}
			if tt.cookie != nil {
				req.AddCookie(tt.cookie)
			}
			rec := httptest.NewRecorder()

			handler.ServeHTTP(rec, req)

			if rec.Code != tt.wantStatus {
				t.Errorf("GET %s status = %d, want %d", tt.path, rec.Code, tt.wantStatus)
			}
			if tt.wantBody != "" && !strings.Contains(rec.Body.String(), tt.wantBody) {
				t.Errorf("GET %s body = %q, want containing %q", tt.path, rec.Body.String(), tt.wantBody)
			}
		})
	}
}

func TestSetupHTTPHandlerSecurityHeaders(t *testing.T) {
	mockAuthenticator := oauth2.NewAuthenticator(&oauth2.Config{
		MockAuth: true,
	})
	dummyGWMux := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	handler := setupHTTPHandler(mockAuthenticator, dummyGWMux, nil)
	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	if rec.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Errorf("Expected X-Content-Type-Options: nosniff, got %q", rec.Header().Get("X-Content-Type-Options"))
	}
	if rec.Header().Get("X-Frame-Options") != "DENY" {
		t.Errorf("Expected X-Frame-Options: DENY, got %q", rec.Header().Get("X-Frame-Options"))
	}
}

func TestIncomingHeaderMatcher_Propagation(t *testing.T) {
	mux := runtime.NewServeMux(runtime.WithIncomingHeaderMatcher(oauth2.IncomingHeaderMatcher))

	req := httptest.NewRequest(http.MethodGet, "/v1/dashboards", nil)
	req.Header.Set("x-goog-user-email", "test-user@google.com")
	req.Header.Set("x-goog-iap-jwt-assertion", "jwt-test-assertion")
	req.Header.Set("x-custom-unmatched-header", "drop-me")

	ctx, err := runtime.AnnotateIncomingContext(context.Background(), mux, req, "TestEndpoint")
	if err != nil {
		t.Fatalf("AnnotateIncomingContext failed: %v", err)
	}

	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		t.Fatal("Expected metadata in context, got none")
	}

	if emails := md.Get("x-goog-user-email"); len(emails) != 1 || emails[0] != "test-user@google.com" {
		t.Errorf("Expected x-goog-user-email = %q, got %v", "test-user@google.com", emails)
	}

	if jwts := md.Get("x-goog-iap-jwt-assertion"); len(jwts) != 1 || jwts[0] != "jwt-test-assertion" {
		t.Errorf("Expected x-goog-iap-jwt-assertion = %q, got %v", "jwt-test-assertion", jwts)
	}

	if dropped := md.Get("x-custom-unmatched-header"); len(dropped) > 0 {
		t.Errorf("Expected x-custom-unmatched-header to be dropped, got %v", dropped)
	}
}

// stubClickHouseConn stands in for a reachable ClickHouse; the engine is never queried through it.
type stubClickHouseConn struct{}

func (stubClickHouseConn) Query(ctx context.Context, query string, args ...any) (driver.Rows, error) {
	return nil, errors.New("Query not implemented in stub")
}

func (stubClickHouseConn) QueryRow(ctx context.Context, query string, args ...any) driver.Row {
	return nil
}

func (stubClickHouseConn) Ping(ctx context.Context) error { return nil }

// departingClickHouseConn answers until down is set, standing in for a ClickHouse that goes away
// after the engine has already been built against it.
type departingClickHouseConn struct {
	stubClickHouseConn
	down bool
}

func (c *departingClickHouseConn) Ping(ctx context.Context) error {
	if c.down {
		return errors.New("connection refused")
	}
	return nil
}

// connectorReturning builds a connector that always reports the given outcome.
func connectorReturning(conn dataengine.ClickHouseConn, err error) dataengine.Connector {
	return func(ctx context.Context) (dataengine.ClickHouseConn, error) {
		return conn, err
	}
}

func TestInitDataEngine_SkippedWhenClickHouseIsNotConfigured(t *testing.T) {
	// Most deployments run without the metrics pipeline. They must not acquire a provider, or
	// readiness would hold every one of their pods out of the service endpoints.
	t.Setenv("CLICKHOUSE_DSN", "")
	t.Setenv("CLICKHOUSE_HOST", "")

	if provider := initDataEngine(context.Background(), &server.Config{}); provider != nil {
		t.Errorf("initDataEngine() = %v, want nil when ClickHouse is not configured", provider)
	}
}

func TestInitDataEngine_SurvivesUnreachableClickHouse(t *testing.T) {
	// Port 1 refuses the connection immediately, standing in for a ClickHouse that the deployment
	// configured but that is not up yet. Startup must continue rather than fail fatally.
	t.Setenv("CLICKHOUSE_DSN", "")
	t.Setenv("CLICKHOUSE_HOST", "127.0.0.1")
	t.Setenv("CLICKHOUSE_PORT", "1")

	provider := initDataEngine(context.Background(), &server.Config{})

	if provider == nil {
		t.Fatal("initDataEngine() = nil, want a provider that keeps retrying")
	}
}

func TestChartDataEngine_NilProviderStaysNilInterface(t *testing.T) {
	// A nil *Provider must not become a non-nil interface holding a nil pointer: the server's
	// "data engine is not configured" guard compares against nil, and would let the request through.
	// The variable is typed explicitly so that the comparison is the one the server actually makes.
	var got server.ChartDataEngine = chartDataEngine(nil)

	if got != nil {
		t.Errorf("chartDataEngine(nil) = %v, want a nil interface", got)
	}
}

func TestChartDataEngine_WrapsProvider(t *testing.T) {
	provider := dataengine.NewDynamicProvider(buildClickHouseResolver(false, "", nil))

	got := chartDataEngine(provider)

	if got != provider {
		t.Errorf("chartDataEngine(provider) = %v, want the provider itself", got)
	}
}

func TestNewReadinessCheck_OmittedWithoutDependencies(t *testing.T) {
	if check := newReadinessCheck(nil, nil); check != nil {
		t.Error("newReadinessCheck(nil, nil) returned a check, want none")
	}
}

func TestNewReadinessCheck_FailsWhileTheDataEngineIsUnavailable(t *testing.T) {
	provider := dataengine.NewProvider(connectorReturning(nil, errors.New("clickhouse is down")))

	err := newReadinessCheck(nil, provider)(context.Background())

	if err == nil {
		t.Fatal("readiness check passed with an unavailable data engine, want an error")
	}
	if !strings.Contains(err.Error(), "chart data engine is unavailable") {
		t.Errorf("readiness check reported %q, want it to name the chart data engine", err)
	}
}

func TestNewReadinessCheck_PassesOnceTheDataEngineIsBuilt(t *testing.T) {
	provider := dataengine.NewProvider(connectorReturning(stubClickHouseConn{}, nil))

	if err := newReadinessCheck(nil, provider)(context.Background()); err != nil {
		t.Errorf("readiness check with a reachable ClickHouse returned %v, want nil", err)
	}
}

func TestNewReadinessCheck_FailsWhenClickHouseStopsAnswering(t *testing.T) {
	conn := &departingClickHouseConn{}
	check := newReadinessCheck(nil, dataengine.NewProvider(connectorReturning(conn, nil)))
	if err := check(context.Background()); err != nil {
		t.Fatalf("readiness check with a reachable ClickHouse returned %v, want nil", err)
	}

	// The engine is cached by that first check and is never rebuilt, so its mere existence stays
	// true after ClickHouse goes away. Only reaching the connection can tell the difference, and
	// without that the pod keeps reporting ready while every chart request fails.
	conn.down = true

	if err := check(context.Background()); err == nil {
		t.Error("readiness check passed after ClickHouse stopped answering, want an error")
	}
}

func TestInitXidClient(t *testing.T) {
	client, conn, err := initXidClient("localhost:8082")
	if err != nil {
		t.Fatalf("initXidClient failed: %v", err)
	}
	if client == nil {
		t.Fatal("expected non-nil XidServiceClient")
	}
	if conn != nil {
		_ = conn.Close()
	}
}

func TestGracefulShutdown_Signal(t *testing.T) {
	httpServer := &http.Server{
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		}),
	}
	grpcServer := grpc.NewServer()

	quit := make(chan os.Signal, 1)
	errChan := make(chan error, 2)

	// Send signal immediately to trigger shutdown
	quit <- syscall.SIGTERM

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	done := make(chan struct{})
	go func() {
		gracefulShutdown(ctx, httpServer, grpcServer, quit, errChan)
		close(done)
	}()

	select {
	case <-done:
		// Succeeded cleanly
	case <-time.After(3 * time.Second):
		t.Fatal("gracefulShutdown timed out on signal")
	}
}

func TestGracefulShutdown_ServerError(t *testing.T) {
	httpServer := &http.Server{
		Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		}),
	}
	grpcServer := grpc.NewServer()

	quit := make(chan os.Signal, 1)
	errChan := make(chan error, 2)

	// Send error to trigger shutdown
	errChan <- errors.New("simulated fatal server error")

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	done := make(chan struct{})
	go func() {
		gracefulShutdown(ctx, httpServer, grpcServer, quit, errChan)
		close(done)
	}()

	select {
	case <-done:
		// Succeeded cleanly
	case <-time.After(3 * time.Second):
		t.Fatal("gracefulShutdown timed out on error")
	}
}

type fakeReadinessChecker struct {
	err error
}

func (f fakeReadinessChecker) CheckReady(ctx context.Context) error {
	return f.err
}

func TestNewReadinessCheck_DatabaseCheck(t *testing.T) {
	ctx := context.Background()
	if err := newReadinessCheck(fakeReadinessChecker{}, nil)(ctx); err != nil {
		t.Errorf("newReadinessCheck(healthyDB, nil) = %v, want nil", err)
	}
	if err := newReadinessCheck(fakeReadinessChecker{err: errors.New("db down")}, nil)(ctx); err == nil {
		t.Fatal("newReadinessCheck(unhealthyDB, nil) = nil, want error")
	}
}

func TestResolveClickHouseWithSource(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel()

	t.Run("surfaces initialization error", func(t *testing.T) {
		resolve := resolveClickHouseWithSource(componentconfig.Source{}, errors.New("k8s init failed"))
		if _, _, err := resolve(ctx); err == nil || !strings.Contains(err.Error(), "k8s init failed") {
			t.Errorf("resolve() error = %v, want k8s init failed", err)
		}
	})

	t.Run("surfaces component resolution error", func(t *testing.T) {
		resolve := resolveClickHouseWithSource(componentconfig.Source{
			Enabled: true,
			Getter:  &crreadertest.FakeGetter{Err: errors.New("rbac forbidden")},
		}, nil)
		if _, _, err := resolve(ctx); err == nil || !strings.Contains(err.Error(), "rbac forbidden") {
			t.Errorf("resolve() error = %v, want rbac forbidden", err)
		}
	})
}

func TestBuildClickHouseResolver_InvalidKubeconfigSurfacesErrorOnResolve(t *testing.T) {
	t.Setenv("KUBERNETES_SERVICE_HOST", "")
	t.Setenv("KUBERNETES_SERVICE_PORT", "")
	badKubeconfig := t.TempDir() + "/bad-kubeconfig"
	if err := os.WriteFile(badKubeconfig, []byte(":::invalid:::"), 0600); err != nil {
		t.Fatalf("failed to write invalid kubeconfig: %v", err)
	}
	t.Setenv("KUBECONFIG", badKubeconfig)

	resolve := buildClickHouseResolver(true, "xmc", nil)
	if _, _, err := resolve(context.Background()); err == nil {
		t.Errorf("resolve() with invalid kubeconfig succeeded, want error")
	}
}

func TestBuildClickHouseResolver_UsesCustomGetter(t *testing.T) {
	t.Setenv("KUBERNETES_SERVICE_HOST", "")
	t.Setenv("KUBERNETES_SERVICE_PORT", "")
	badKubeconfig := t.TempDir() + "/bad-kubeconfig"
	if err := os.WriteFile(badKubeconfig, []byte(":::invalid:::"), 0600); err != nil {
		t.Fatalf("failed to write invalid kubeconfig: %v", err)
	}
	t.Setenv("KUBECONFIG", badKubeconfig)

	pwFile := t.TempDir() + "/ch-pw"
	if err := os.WriteFile(pwFile, []byte("secret-pw\n"), 0600); err != nil {
		t.Fatalf("failed to write password file: %v", err)
	}
	t.Setenv("CLICKHOUSE_PASSWORD_FILE", pwFile)
	cr := &unstructured.Unstructured{
		Object: map[string]any{
			"spec": map[string]any{
				"enabled":    true,
				"parameters": map[string]any{"host": "127.0.0.1", "nativePort": "9000"},
			},
			"status": map[string]any{"state": "COMPONENT_STATE_ENABLED"},
		},
	}
	customGetter := &crreadertest.FakeGetter{Component: cr}

	resolve := buildClickHouseResolver(true, "xmc", customGetter)
	fp, connect, err := resolve(context.Background())
	if err != nil {
		t.Fatalf("resolve() with customGetter error: %v", err)
	}
	if fp == "" || connect == nil {
		t.Errorf("resolve() returned fp=%q, connect=%v; want non-empty fp and non-nil connect", fp, connect)
	}

	// When CLICKHOUSE_PASSWORD_FILE is unset, lazySecretReader attempts K8s Secret initialization and surfaces kubeconfig error.
	t.Setenv("CLICKHOUSE_PASSWORD_FILE", "")
	if _, _, err := resolve(context.Background()); err == nil {
		t.Errorf("resolve() without password file and invalid kubeconfig succeeded, want secret reader init error")
	}

	// Verify lazySecretReader delegates when reader is pre-populated.
	lazy := &lazySecretReader{
		reader: &crreadertest.FakeSecretReader{
			Secrets: map[string]map[string]string{"xmc/ch": {"pw": "val"}},
		},
	}
	if got, err := lazy.GetSecretValue(context.Background(), "xmc", "ch", "pw"); err != nil || got != "val" {
		t.Errorf("lazy.GetSecretValue() = (%q, %v), want (\"val\", nil)", got, err)
	}
}

func TestClickHouseProviderRebuildsWhenComponentCRChanges(t *testing.T) {
	t.Setenv("CLICKHOUSE_PASSWORD_FILE", "")
	cr := &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": "xmc.dev/v1alpha1",
			"kind":       "Component",
			"metadata":   map[string]any{"name": "clickhouse", "namespace": "xmc"},
			"spec": map[string]any{
				"enabled": true,
				"parameters": map[string]any{
					"defaultDatabase":   "analytics",
					"user":              "reader",
					"secretName":        "ch-secret",
					"secretPasswordKey": "clickhouse-password",
				},
			},
			"status": map[string]any{
				"state": "COMPONENT_STATE_ENABLED",
				"access": map[string]any{
					"envVars": map[string]any{
						"HOST": "ch-v1.xmc.svc.cluster.local",
						"PORT": "9000",
					},
				},
			},
		},
	}
	getter := &crreadertest.FakeGetter{Component: cr}
	secrets := &crreadertest.FakeSecretReader{
		Secrets: map[string]map[string]string{
			"xmc/ch-secret": {"clickhouse-password": "pw-1"},
		},
	}

	var dialedAddrs [][]string
	resolve := resolveClickHouseWithDialer(
		componentconfig.Source{Enabled: true, Namespace: "xmc", Getter: getter, Secrets: secrets},
		nil,
		func(ctx context.Context, opts *clickhouse.Options) (dataengine.ClickHouseConn, error) {
			dialedAddrs = append(dialedAddrs, opts.Addr)
			return stubClickHouseConn{}, nil
		},
	)
	provider := dataengine.NewDynamicProvider(resolve, dataengine.WithRetryInterval(0))

	if err := provider.Ping(context.Background()); err != nil {
		t.Fatalf("initial Ping() error: %v", err)
	}
	if err := provider.Ping(context.Background()); err != nil {
		t.Fatalf("second Ping() with unchanged CR error: %v", err)
	}
	if diff := cmp.Diff([][]string{{"ch-v1.xmc.svc.cluster.local:9000"}}, dialedAddrs); diff != "" {
		t.Fatalf("dialedAddrs after unchanged Ping mismatch (-want +got):\n%s", diff)
	}

	if err := unstructured.SetNestedField(cr.Object, "ch-v2.xmc.svc.cluster.local", "status", "access", "envVars", "HOST"); err != nil {
		t.Fatalf("SetNestedField failed: %v", err)
	}
	if err := provider.Ping(context.Background()); err != nil {
		t.Fatalf("Ping() after CR update error: %v", err)
	}
	wantAddrs := [][]string{
		{"ch-v1.xmc.svc.cluster.local:9000"},
		{"ch-v2.xmc.svc.cluster.local:9000"},
	}
	if diff := cmp.Diff(wantAddrs, dialedAddrs); diff != "" {
		t.Errorf("dialedAddrs after CR update mismatch (-want +got):\n%s", diff)
	}
}

func TestIsClickHouseExpected(t *testing.T) {
	ctx := context.Background()

	t.Run("returns true when static environment is configured", func(t *testing.T) {
		t.Setenv("CLICKHOUSE_HOST", "127.0.0.1")
		if !isClickHouseExpected(ctx, false, "xmc", nil) {
			t.Errorf("isClickHouseExpected() = false, want true")
		}
	})

	t.Run("returns false when static env unset and components disabled", func(t *testing.T) {
		t.Setenv("CLICKHOUSE_HOST", "")
		t.Setenv("CLICKHOUSE_DSN", "")
		if isClickHouseExpected(ctx, false, "xmc", nil) {
			t.Errorf("isClickHouseExpected() = true, want false")
		}
	})

	t.Run("returns false when CR is NotFound", func(t *testing.T) {
		t.Setenv("CLICKHOUSE_HOST", "")
		t.Setenv("CLICKHOUSE_DSN", "")
		getter := &crreadertest.FakeGetter{Err: apierrors.NewNotFound(schema.GroupResource{Group: "xmc.dev", Resource: "components"}, "clickhouse")}
		if isClickHouseExpected(ctx, true, "xmc", getter) {
			t.Errorf("isClickHouseExpected() = true, want false")
		}
	})

	t.Run("returns false when CR is disabled", func(t *testing.T) {
		t.Setenv("CLICKHOUSE_HOST", "")
		t.Setenv("CLICKHOUSE_DSN", "")
		u := &unstructured.Unstructured{
			Object: map[string]any{
				"spec":   map[string]any{"enabled": false},
				"status": map[string]any{"state": "COMPONENT_STATE_DISABLED"},
			},
		}
		getter := &crreadertest.FakeGetter{Component: u}
		if isClickHouseExpected(ctx, true, "xmc", getter) {
			t.Errorf("isClickHouseExpected() = true, want false")
		}
	})

	t.Run("returns true when CR is present and enabled", func(t *testing.T) {
		t.Setenv("CLICKHOUSE_HOST", "")
		t.Setenv("CLICKHOUSE_DSN", "")
		u := &unstructured.Unstructured{
			Object: map[string]any{
				"spec":   map[string]any{"enabled": true},
				"status": map[string]any{"state": "COMPONENT_STATE_ENABLED"},
			},
		}
		getter := &crreadertest.FakeGetter{Component: u}
		if !isClickHouseExpected(ctx, true, "xmc", getter) {
			t.Errorf("isClickHouseExpected() = false, want true")
		}
	})
}

type countingComponentGetter struct {
	mu    sync.Mutex
	inner crreader.Getter
	calls map[string]int
}

func (c *countingComponentGetter) GetComponent(ctx context.Context, namespace, name string) (*unstructured.Unstructured, error) {
	c.mu.Lock()
	if c.calls == nil {
		c.calls = make(map[string]int)
	}
	c.calls[name]++
	c.mu.Unlock()
	return c.inner.GetComponent(ctx, namespace, name)
}

func TestInitDataEngineWithGetter_UnconfiguredReturnsNil(t *testing.T) {
	ctx := context.Background()
	t.Setenv("CLICKHOUSE_HOST", "")
	t.Setenv("CLICKHOUSE_DSN", "")
	t.Setenv("BIGQUERY_DATASET", "")
	t.Setenv("METRICS_STORE", "")
	serverCfg := &server.Config{
		EnableComponents:    true,
		ComponentsNamespace: "xmc",
	}
	getter := &countingComponentGetter{
		inner: &crreadertest.FakeGetter{Err: apierrors.NewNotFound(schema.GroupResource{Group: "xmc.dev", Resource: "components"}, "clickhouse")},
	}

	provider := initDataEngineWithGetter(ctx, serverCfg, getter)
	if provider != nil {
		t.Errorf("initDataEngineWithGetter() = %v, want nil", provider)
	}
	if diff := cmp.Diff(map[string]int{"clickhouse": 1, "bigquery": 1}, getter.calls); diff != "" {
		t.Errorf("startup GetComponent calls mismatch (-want +got):\n%s", diff)
	}
}

func TestInitDataEngineWithGetter_AutoDetectBigQueryComponentCR(t *testing.T) {
	ctx := context.Background()
	t.Setenv("CLICKHOUSE_HOST", "")
	t.Setenv("CLICKHOUSE_DSN", "")
	t.Setenv("BIGQUERY_DATASET", "")
	t.Setenv("METRICS_STORE", "")
	serverCfg := &server.Config{
		EnableComponents:    true,
		ComponentsNamespace: "xmc",
	}
	bqCR := &unstructured.Unstructured{
		Object: map[string]any{
			"spec":   map[string]any{"enabled": true},
			"status": map[string]any{"state": "COMPONENT_STATE_ENABLED"},
		},
	}
	getter := &countingComponentGetter{
		inner: &crreadertest.FakeGetter{Components: map[string]*unstructured.Unstructured{"bigquery": bqCR}},
	}

	provider := initDataEngineWithGetter(ctx, serverCfg, getter)
	if provider == nil {
		t.Fatal("initDataEngineWithGetter() = nil, want non-nil BigQuery provider")
	}
	defer provider.Close()
	if diff := cmp.Diff(map[string]int{"clickhouse": 1, "bigquery": 2}, getter.calls); diff != "" {
		t.Errorf("startup GetComponent calls mismatch (-want +got):\n%s", diff)
	}
}

type stubBigQueryConn struct{ tableRef string }

func (s stubBigQueryConn) Query(context.Context, string, ...any) (xmcbigquery.Rows, error) {
	return nil, errors.New("not implemented")
}
func (s stubBigQueryConn) Ping(context.Context) error { return nil }
func (s stubBigQueryConn) TableRef() string           { return s.tableRef }
func (s stubBigQueryConn) Close() error               { return nil }

func TestBigQueryMetricsStoreSelectionAndBuilder(t *testing.T) {
	ctx := context.Background()
	for _, tc := range []struct {
		name, cfgVal, envVal, want string
		wantErr                    bool
	}{
		{"empty auto-detect", "", "", "", false},
		{"cfg clickhouse", "ClickHouse", "", "clickhouse", false},
		{"env bigquery fallback", "", "  BIGQUERY ", "bigquery", false},
		{"cfg overrides env", "bigquery", "clickhouse", "bigquery", false},
		{"invalid store", "postgres", "", "", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("METRICS_STORE", tc.envVal)
			got, err := resolveMetricsStore(&server.Config{MetricsStore: tc.cfgVal})
			if (err != nil) != tc.wantErr || got != tc.want {
				t.Errorf("resolveMetricsStore() = (%q, %v), want (%q, wantErr=%v)", got, err, tc.want, tc.wantErr)
			}
		})
	}

	t.Setenv("BIGQUERY_PROJECT_ID", "")
	t.Setenv("GOOGLE_CLOUD_PROJECT", "")
	t.Setenv("BIGQUERY_DATASET", "xmc_metrics")
	t.Setenv("BIGQUERY_TABLE", "xmc_metrics")
	staticSrc := bqconfig.Source{Enabled: false}
	if _, _, err := resolveBigQueryWithDialer(staticSrc, func(context.Context, *xmcbigquery.Options) (xmcbigquery.Conn, error) {
		return stubBigQueryConn{}, nil
	})(ctx); err == nil {
		t.Error("resolveBigQueryWithDialer() without project ID succeeded, want error")
	}
	staticWithProj := bqconfig.Source{Enabled: false, FallbackProjectID: "proj-1"}
	_, buildFail, err := resolveBigQueryWithDialer(staticWithProj, func(context.Context, *xmcbigquery.Options) (xmcbigquery.Conn, error) {
		return nil, errors.New("dial failed")
	})(ctx)
	if err != nil {
		t.Fatalf("resolveBigQueryWithDialer() error: %v", err)
	}
	if _, err := buildFail(ctx); err == nil {
		t.Error("buildFail() with failing dialer succeeded, want error")
	}
	_, buildOK, err := resolveBigQueryWithDialer(staticWithProj, func(_ context.Context, opts *xmcbigquery.Options) (xmcbigquery.Conn, error) {
		ref, _ := opts.TableRef()
		return stubBigQueryConn{tableRef: ref}, nil
	})(ctx)
	if err != nil {
		t.Fatalf("resolveBigQueryWithDialer() unexpected error: %v", err)
	}
	engine, err := buildOK(ctx)
	if err != nil {
		t.Fatalf("buildOK() unexpected error: %v", err)
	}
	_ = engine.Close()

	t.Setenv("CLICKHOUSE_HOST", "")
	t.Setenv("CLICKHOUSE_DSN", "")
	if p := initDataEngineWithGetter(ctx, &server.Config{MetricsStore: "invalid"}, nil); p != nil {
		t.Errorf("initDataEngineWithGetter(invalid) = %v, want nil", p)
	}
	// Auto-detects BigQuery when ClickHouse is unconfigured and BIGQUERY_DATASET is set (project missing triggers non-fatal warm warning).
	if p := initDataEngineWithGetter(ctx, &server.Config{MetricsStore: ""}, nil); p == nil {
		t.Error("initDataEngineWithGetter(auto-detect BIGQUERY_DATASET) = nil, want non-nil provider")
	}
	if p := initDataEngineWithGetter(ctx, &server.Config{MetricsStore: "bigquery"}, nil); p == nil {
		t.Error("initDataEngineWithGetter(explicit bigquery) = nil, want non-nil provider")
	}
}

func TestBigQueryProviderRebuildsWhenComponentCRChanges(t *testing.T) {
	ctx := context.Background()
	t.Setenv("CLICKHOUSE_HOST", "")
	t.Setenv("CLICKHOUSE_DSN", "")
	t.Setenv("BIGQUERY_DATASET", "")
	t.Setenv("BIGQUERY_PROJECT_ID", "")
	t.Setenv("GOOGLE_CLOUD_PROJECT", "")
	t.Setenv("KUBERNETES_SERVICE_HOST", "")
	t.Setenv("KUBERNETES_SERVICE_PORT", "")
	badKubeconfig := t.TempDir() + "/bad-kubeconfig"
	_ = os.WriteFile(badKubeconfig, []byte(":::invalid:::"), 0600)
	t.Setenv("KUBECONFIG", badKubeconfig)

	if _, _, err := buildBigQueryResolver(true, "xmc", "proj-1", nil)(ctx); err == nil {
		t.Error("buildBigQueryResolver() with invalid kubeconfig succeeded, want error")
	}
	if isBigQueryExpected(ctx, true, "xmc", nil) {
		t.Error("isBigQueryExpected() with invalid kubeconfig = true, want false")
	}

	cr := &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": "xmc.dev/v1alpha1",
			"kind":       "Component",
			"metadata":   map[string]any{"name": "bigquery", "namespace": "xmc"},
			"spec":       map[string]any{"enabled": true, "parameters": map[string]any{"dataset": "ds_v1"}},
			"status": map[string]any{
				"state":  "COMPONENT_STATE_ENABLED",
				"access": map[string]any{"envVars": map[string]any{"PROJECT_ID": "proj-1", "DATASET": "ds_v1", "TABLE": "xmc_metrics"}},
			},
		},
	}
	getter := &crreadertest.FakeGetter{Components: map[string]*unstructured.Unstructured{"bigquery": cr}}
	if !isBigQueryExpected(ctx, true, "xmc", getter) {
		t.Error("isBigQueryExpected() with enabled Component/bigquery = false, want true")
	}
	if fp, build, err := buildBigQueryResolver(true, "xmc", "proj-1", getter)(ctx); err != nil || fp == "" || build == nil {
		t.Fatalf("buildBigQueryResolver() = (%q, %v, %v), want non-empty fp and builder", fp, build, err)
	}

	var dialedRefs []string
	resolve := resolveBigQueryWithDialer(
		bqconfig.Source{Enabled: true, Namespace: "xmc", FallbackProjectID: "proj-1", Getter: getter},
		func(_ context.Context, opts *xmcbigquery.Options) (xmcbigquery.Conn, error) {
			ref, _ := opts.TableRef()
			dialedRefs = append(dialedRefs, ref)
			return stubBigQueryConn{tableRef: ref}, nil
		},
	)
	provider := dataengine.NewDynamicEngineProvider(resolve, dataengine.WithRetryInterval(0))
	defer provider.Close()

	if err := provider.Ping(ctx); err != nil {
		t.Fatalf("initial Ping() error: %v", err)
	}
	if err := provider.Ping(ctx); err != nil {
		t.Fatalf("unchanged Ping() error: %v", err)
	}
	_ = unstructured.SetNestedField(cr.Object, "ds_v2", "status", "access", "envVars", "DATASET")
	if err := provider.Ping(ctx); err != nil {
		t.Fatalf("Ping() after CR update error: %v", err)
	}
	wantRefs := []string{"`proj-1.ds_v1.xmc_metrics`", "`proj-1.ds_v2.xmc_metrics`"}
	if diff := cmp.Diff(wantRefs, dialedRefs); diff != "" {
		t.Errorf("dialedRefs mismatch (-want +got):\n%s", diff)
	}
}

func TestResolvers_FallBackToStaticEnvWhenKubeconfigUnavailable(t *testing.T) {
	ctx := context.Background()
	t.Setenv("KUBERNETES_SERVICE_HOST", "")
	t.Setenv("KUBERNETES_SERVICE_PORT", "")
	badKubeconfig := t.TempDir() + "/bad-kubeconfig"
	if err := os.WriteFile(badKubeconfig, []byte(":::invalid:::"), 0600); err != nil {
		t.Fatalf("failed to write invalid kubeconfig: %v", err)
	}
	t.Setenv("KUBECONFIG", badKubeconfig)

	t.Run("clickhouse static fallback", func(t *testing.T) {
		t.Setenv("CLICKHOUSE_HOST", "127.0.0.1")
		t.Setenv("CLICKHOUSE_PORT", "9000")
		fp, connect, err := buildClickHouseResolver(true, "xmc", nil)(ctx)
		if err != nil || fp == "" || connect == nil {
			t.Fatalf("buildClickHouseResolver() = (%q, %v, %v), want static fallback", fp, connect, err)
		}
	})

	t.Run("bigquery static fallback", func(t *testing.T) {
		t.Setenv("BIGQUERY_DATASET", "xmc_metrics")
		t.Setenv("BIGQUERY_TABLE", "xmc_metrics")
		fp, build, err := buildBigQueryResolver(true, "xmc", "proj-1", nil)(ctx)
		if err != nil || fp == "" || build == nil {
			t.Fatalf("buildBigQueryResolver() = (%q, %v, %v), want static fallback", fp, build, err)
		}
	})

	t.Run("isComponentExpected logs and returns true on non-NotFound error", func(t *testing.T) {
		forbiddenGetter := &crreadertest.FakeGetter{
			Err: apierrors.NewForbidden(schema.GroupResource{Group: "xmc.dev", Resource: "components"}, "bigquery", errors.New("denied")),
		}
		if !isComponentExpected(ctx, "xmc", "bigquery", forbiddenGetter) {
			t.Error("isComponentExpected() on Forbidden = false, want true")
		}
	})
}
