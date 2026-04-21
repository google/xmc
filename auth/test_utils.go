package auth

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"golang/google_api/cloudidentity/v1beta1/cloudidentity"
	"golang/google_api/option/option"
	xpb "xmanager_cloud/xid_service/proto/messages_go_proto"
)

// MockAPIResponse is a struct that holds the responses for the mock Cloud Identity API.
type MockAPIResponse struct {
	lookup     map[string]string
	membership map[string]bool
	httpErr    map[string]int
	apiCalls   int64
}

// NewTestServer creates a mock Cloud Identity API server and returns it along with the responses
// struct.
func NewTestServer() (*httptest.Server, *MockAPIResponse) {
	responses := &MockAPIResponse{
		lookup:     make(map[string]string),
		membership: make(map[string]bool),
		httpErr:    make(map[string]int),
	}

	mux := http.NewServeMux()

	mux.HandleFunc("/v1beta1/groups:lookup", func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt64(&responses.apiCalls, 1)
		groupEmail := r.URL.Query().Get("groupKey.id")

		if code, ok := responses.httpErr[r.URL.Path]; ok {
			http.Error(w, "mock lookup error", code)
			return
		}

		if groupName, ok := responses.lookup[groupEmail]; ok {
			json.NewEncoder(w).Encode(map[string]string{"name": groupName})
		} else {
			http.Error(w, "group not found", http.StatusNotFound)
		}
	})

	mux.HandleFunc("/v1beta1/", func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "memberships:checkTransitiveMembership") {
			atomic.AddInt64(&responses.apiCalls, 1)

			path := r.URL.Path
			path = strings.TrimPrefix(path, "/v1beta1/")
			path = strings.TrimSuffix(path, "/memberships:checkTransitiveMembership")

			groupName := path

			userEmail := r.URL.Query().Get("query")
			userEmail = strings.TrimSuffix(strings.TrimPrefix(userEmail, "member_key.id == '"), "'")

			if code, ok := responses.httpErr[r.URL.Path]; ok {
				http.Error(w, "mock membership error", code)
				return
			}

			key := groupName + ":" + userEmail
			if has, ok := responses.membership[key]; ok {
				json.NewEncoder(w).Encode(map[string]bool{"hasMembership": has})
			} else {
				json.NewEncoder(w).Encode(map[string]bool{"hasMembership": false})
			}
		}
	})

	return httptest.NewServer(mux), responses
}

// NewTestAuthorizer creates a new Authorizer for testing with a mock Cloud Identity API server.
func NewTestAuthorizer(t *testing.T, cacheTTL, cacheErrorTTL time.Duration, server *httptest.Server) *Authorizer {
	t.Helper()
	ctx := context.Background()

	idService, err := cloudidentity.NewService(ctx,
		option.WithEndpoint(server.URL),
		option.WithoutAuthentication(),
		option.WithHTTPClient(server.Client()),
	)
	if err != nil {
		t.Fatalf("Failed to create test Cloud Identity service: %v", err)
	}

	return &Authorizer{
		IDService:     idService,
		groupCache:    make(map[string]cacheEntry),
		cacheLock:     sync.RWMutex{},
		cacheTTL:      cacheTTL,
		cacheErrorTTL: cacheErrorTTL,
	}
}

// MockAuth is a mock implementation of AuthorizerInterface for testing.
type MockAuth struct {
	AuthnErr error
	IsAuthed bool
	AuthzErr error
}

// AuthenticateUserFromContext mocks user authentication.
func (m *MockAuth) AuthenticateUserFromContext(ctx context.Context) (string, string, error) {
	if m.AuthnErr != nil {
		return "", "", m.AuthnErr
	}
	return "test@example.com", "id", m.AuthnErr
}

// IsUserAuthorized mocks user authorization.
func (m *MockAuth) IsUserAuthorized(ctx context.Context, user string, acls *xpb.XidAcls, role Role) (bool, error) {
	return m.IsAuthed, m.AuthzErr
}
