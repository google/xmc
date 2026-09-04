// Copyright 2025 Google LLC
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
package client

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/tink-crypto/tink-go/v2/jwt"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/testing/protocmp"
	"xmanager_cloud/common/interfaces"
	essmsgpb "xmanager_cloud/experiment_state_server/proto/messages_go_proto"
	"xmanager_cloud/security/securitytest"
	actuatortaskpb "xmanager_cloud/task_queue/proto/task_go_proto"
	"xmanager_cloud/task_queue_redis/redisclient"
)

type mockTokenProvider struct {
	Token string
	Err   error
}

func (m *mockTokenProvider) GenerateToken(ctx context.Context, invokerSA, audience string) (string, error) {
	return m.Token, m.Err
}

func createTaskQueueClient(ctx context.Context, t *testing.T, opts ...Option) (*Client, *redisclient.FakeClient) {
	t.Helper()
	fakeRedis := redisclient.NewFakeClient()
	keyFunc := securitytest.MustCreateTestSigner(t)
	allOpts := append([]Option{
		WithTokenProvider(&mockTokenProvider{Token: "mock-oidc-token"}),
		WithRedisPublisher(fakeRedis),
	}, opts...)
	taskQueueClient, err := New(ctx, "test-project", "test-sa", "http://actuator.example.com", "test-audience", keyFunc, DefaultSignatureTTLSeconds, "", allOpts...)
	if err != nil {
		t.Fatalf("Failed to create task queue client: %v", err)
	}
	return taskQueueClient, fakeRedis
}

func TestNewClient(t *testing.T) {
	projectID := "test-project"
	actuatorURL := "http://actuator.example.com"
	invokerSA := "test-sa"
	audience := "test-audience"
	ctx := t.Context()

	keyFunc := securitytest.MustCreateTestSigner(t)

	c, err := New(ctx, projectID, invokerSA, actuatorURL, audience, keyFunc, DefaultSignatureTTLSeconds, "")
	if err != nil {
		t.Fatalf("New() failed with error: %v", err)
	}

	if c.gcpProjectID != projectID {
		t.Errorf("New().gcpProjectID %q, want %q", c.gcpProjectID, projectID)
	}
	if c.actuatorURL != actuatorURL {
		t.Errorf("New().actuatorURL = %q, want %q", c.actuatorURL, actuatorURL)
	}
	if c.invokerSA != invokerSA {
		t.Errorf("invokerSA got %q, want %q", c.invokerSA, invokerSA)
	}
	if c.actuatorAudience != audience {
		t.Errorf("New().actuatorAudience got %q, want %q", c.actuatorAudience, audience)
	}

	// Test audience defaulting to actuatorURL.
	c2, err := New(t.Context(), projectID, invokerSA, actuatorURL, "", keyFunc, DefaultSignatureTTLSeconds, "")
	if err != nil {
		t.Fatalf("New() failed with error: %v", err)
	}
	if c2.actuatorAudience != actuatorURL {
		t.Errorf("New().actuatorAudience got %q, want %q", c2.actuatorAudience, actuatorURL)
	}
}

func TestEnqueueWorkUnitTask(t *testing.T) {
	workUnit := &essmsgpb.WorkUnit{Name: "experiments/123/workUnits/1"}
	operationName := "op1"
	testCases := []struct {
		name       string
		action     *actuatortaskpb.Action
		target     string
		actionType string
	}{
		{
			name: "create action",
			action: &actuatortaskpb.Action{
				Action: &actuatortaskpb.Action_Create{
					Create: &actuatortaskpb.CreateAction{},
				},
			},
			target:     "experiments/123/workUnits/1",
			actionType: "CREATE",
		},
		{
			name: "delete action",
			action: &actuatortaskpb.Action{
				Action: &actuatortaskpb.Action_Delete{
					Delete: &actuatortaskpb.DeleteAction{},
				},
			},
			target:     "experiments/123/workUnits/2",
			actionType: "DELETE",
		},
		{
			name: "restart action",
			action: &actuatortaskpb.Action{
				Action: &actuatortaskpb.Action_Restart{
					Restart: &actuatortaskpb.RestartAction{},
				},
			},
			target:     "experiments/123/workUnits/3",
			actionType: "RESTART",
		},
		{
			name: "update action",
			action: &actuatortaskpb.Action{
				Action: &actuatortaskpb.Action_Update{
					Update: &actuatortaskpb.UpdateAction{},
				},
			},
			target:     "experiments/123/workUnits/4",
			actionType: "UPDATE",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			ctx := t.Context()

			c, redisClient := createTaskQueueClient(ctx, t, WithTokenProvider(&mockTokenProvider{Token: "mock-oidc-token"}))

			if err := c.EnqueueWorkUnitTask(ctx, tc.target, workUnit, tc.action, operationName); err != nil {
				t.Fatalf("EnqueueWorkUnitTask() failed with error: %v", err)
			}

			if len(redisClient.Messages) != 1 {
				t.Fatalf("Expected 1 message in Redis, but got %d", len(redisClient.Messages))
			}

			var redisTask interfaces.RedisTask
			if err := json.Unmarshal([]byte(redisClient.Messages[0]), &redisTask); err != nil {
				t.Fatalf("Failed to unmarshal redis message: %v", err)
			}

			if redisTask.Signature == "" {
				t.Fatalf("Signature is empty, want non-empty")
			}

			verifier, err := securitytest.MustCreateTestVerifier(t)()
			if err != nil {
				t.Fatalf("Failed to create JWT verifier: %v", err)
			}

			validator, err := jwt.NewValidator(&jwt.ValidatorOpts{ExpectedAudience: &c.actuatorAudience, ExpectedIssuer: &c.invokerSA})
			if err != nil {
				t.Fatalf("Failed to create JWT validator: %v", err)
			}

			verifiedJWT, err := verifier.VerifyAndDecode(redisTask.Signature, validator)
			if err != nil {
				t.Fatalf("Failed to verify or decode token: %v", err)
			}

			iat, err := verifiedJWT.IssuedAt()
			if err != nil {
				t.Fatalf("Iat claim not found")
			}

			exp, err := verifiedJWT.ExpiresAt()
			if err != nil {
				t.Fatalf("Exp claim not found")
			}

			wantTTL := c.signatureTTL
			if got := exp.Sub(iat); got != wantTTL {
				t.Errorf("Token expiration mismatch: got %v, want %v", got, wantTTL)
			}

			if redisTask.OidcToken != "mock-oidc-token" {
				t.Errorf("OidcToken got %q, want %q", redisTask.OidcToken, "mock-oidc-token")
			}

			payload := &actuatortaskpb.ActuatorPayload{}
			if err := protojson.Unmarshal(redisTask.Payload, payload); err != nil {
				t.Fatalf("Failed to unmarshal payload: %v", err)
			}

			wantPayload := &actuatortaskpb.ActuatorPayload{
				Kind:     actuatortaskpb.ActuatorPayload_WORK_UNIT_ACTUATION_REQUEST.Enum(),
				Target:   proto.String(tc.target),
				WorkUnit: workUnit,
				Action:   tc.action,
			}
			if diff := cmp.Diff(wantPayload, payload, protocmp.Transform(), protocmp.IgnoreFields(&actuatortaskpb.ActuatorPayload{}, "metadata", "create_time")); diff != "" {
				t.Errorf("Payload mismatch (-want +got):\n%s", diff)
			}

			if payload.GetCreateTime() == nil {
				t.Error("Payload CreateTime should not be nil")
			}

			if payload.GetMetadata() == nil {
				t.Fatal("Payload Metadata should not be nil")
			}
			wantIdempotencyKeyPrefix := fmt.Sprintf("%s_%s_", tc.target, tc.actionType)
			if !strings.HasPrefix(payload.GetMetadata().GetIdempotencyKey(), wantIdempotencyKeyPrefix) {
				t.Errorf("IdempotencyKey got %q, want prefix %q", payload.GetMetadata().GetIdempotencyKey(), wantIdempotencyKeyPrefix)
			}
			if payload.GetMetadata().GetOperationName() != operationName {
				t.Errorf("OperationName got %q, want %q", payload.GetMetadata().GetOperationName(), operationName)
			}
		})
	}
}

func TestEnqueueWorkUnitTask_IdempotencyKeyIsUnique(t *testing.T) {
	ctx := t.Context()

	c, redisClient := createTaskQueueClient(ctx, t, WithTokenProvider(&mockTokenProvider{Token: "mock-oidc-token"}))

	action := &actuatortaskpb.Action{
		Action: &actuatortaskpb.Action_Create{
			Create: &actuatortaskpb.CreateAction{},
		},
	}
	target := "experiments/123/workUnits/1"
	workUnit := &essmsgpb.WorkUnit{Name: "experiments/123/workUnits/1"}
	operationName := "op1"

	err1 := c.EnqueueWorkUnitTask(ctx, target, workUnit, action, operationName)
	// Idempotency key uses time with nanosecond precision, but sleep to be sure.
	time.Sleep(2 * time.Nanosecond)
	err2 := c.EnqueueWorkUnitTask(ctx, target, workUnit, action, operationName)

	if err1 != nil || err2 != nil {
		t.Fatalf("EnqueueWorkUnitTask failed, err1: %v, err2: %v", err1, err2)
	}

	if len(redisClient.Messages) != 2 {
		t.Fatalf("Expected 2 messages, got %d", len(redisClient.Messages))
	}

	var redisTask1, redisTask2 interfaces.RedisTask
	json.Unmarshal([]byte(redisClient.Messages[0]), &redisTask1)
	json.Unmarshal([]byte(redisClient.Messages[1]), &redisTask2)

	payload1 := &actuatortaskpb.ActuatorPayload{}
	protojson.Unmarshal(redisTask1.Payload, payload1)
	payload2 := &actuatortaskpb.ActuatorPayload{}
	protojson.Unmarshal(redisTask2.Payload, payload2)

	if payload1.GetMetadata().GetIdempotencyKey() == payload2.GetMetadata().GetIdempotencyKey() {
		t.Errorf("Idempotency keys are identical, but should be unique: %q", payload1.GetMetadata().GetIdempotencyKey())
	}
}

func TestEnqueueWorkUnitTask_TokenProviderFailure(t *testing.T) {
	ctx := t.Context()

	c, _ := createTaskQueueClient(ctx, t, WithTokenProvider(&mockTokenProvider{Err: fmt.Errorf("token provider error")}))

	action := &actuatortaskpb.Action{
		Action: &actuatortaskpb.Action_Create{
			Create: &actuatortaskpb.CreateAction{},
		},
	}
	err := c.EnqueueWorkUnitTask(ctx, "target", nil, action, "op")
	if err == nil {
		t.Fatal("EnqueueWorkUnitTask() succeeded, want error")
	}
	if !strings.Contains(err.Error(), "token provider error") {
		t.Errorf("Error message %q does not contain 'token provider error'", err.Error())
	}
}

func TestGenerateIdempotencyKeyUnknown(t *testing.T) {
	key := generateIdempotencyKey("target", &actuatortaskpb.Action{}, time.Now())
	if !strings.HasPrefix(key, "target_UNKNOWN_") {
		t.Errorf("generateIdempotencyKey with unknown action got %q, want prefix %q", key, "target_UNKNOWN_")
	}
}

func TestOidcTokenProvider(t *testing.T) {
	ctx := t.Context()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		if err := r.ParseForm(); err != nil {
			http.Error(w, "invalid form", http.StatusBadRequest)
			return
		}
		if r.Form.Get("grant_type") != "password" || r.Form.Get("client_id") != "test-client" {
			http.Error(w, "invalid credentials", http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id_token":   "mock-oidc-id-token",
			"expires_in": 3600,
		})
	}))
	defer server.Close()

	p := NewOidcTokenProvider(OidcTokenProviderConfig{
		TokenURL:     server.URL,
		ClientID:     "test-client",
		ClientSecret: "test-secret",
		Username:     "admin@xmc.local",
		Password:     "password",
		HTTPClient:   server.Client(),
	})

	token, err := p.GenerateToken(ctx, "admin@xmc.local", "aud")
	if err != nil {
		t.Fatalf("OidcTokenProvider.GenerateToken() returned unexpected error: %v", err)
	}
	if token != "mock-oidc-id-token" {
		t.Errorf("OidcTokenProvider.GenerateToken() returned %q, want 'mock-oidc-id-token'", token)
	}

	// Verify token caching (second call returns cached token without hitting server)
	server.Close()
	cachedToken, err := p.GenerateToken(ctx, "admin@xmc.local", "aud")
	if err != nil {
		t.Fatalf("OidcTokenProvider.GenerateToken() cached call failed: %v", err)
	}
	if cachedToken != "mock-oidc-id-token" {
		t.Errorf("OidcTokenProvider.GenerateToken() cached token = %q, want 'mock-oidc-id-token'", cachedToken)
	}
}

func TestGcpIAMTokenProvider_MissingCredentials(t *testing.T) {
	ctx := t.Context()
	t.Setenv("GOOGLE_APPLICATION_CREDENTIALS", "/nonexistent/credentials.json")
	p := &GcpIAMTokenProvider{}
	token, err := p.GenerateToken(ctx, "sa@test.com", "aud")
	if err == nil {
		t.Fatalf("GcpIAMTokenProvider.GenerateToken() succeeded unexpectedly with token %q, want error", token)
	}
	if !strings.Contains(err.Error(), "failed to find default credentials") {
		t.Errorf("GenerateToken() error %q want substring 'failed to find default credentials'", err.Error())
	}
}

type staticTestTokenProvider struct {
	token string
}

func (s *staticTestTokenProvider) GenerateToken(ctx context.Context, invokerSA, audience string) (string, error) {
	return s.token, nil
}

func TestNew_DefaultTokenProvider(t *testing.T) {
	ctx := t.Context()
	keyFunc := securitytest.MustCreateTestSigner(t)

	t.Run("defaults to GcpIAMTokenProvider", func(t *testing.T) {
		c, err := New(ctx, "project", "sa", "http://actuator", "", keyFunc, 3600, "")
		if err != nil {
			t.Fatalf("New() failed: %v", err)
		}
		if _, ok := c.tokenProvider.(*GcpIAMTokenProvider); !ok {
			t.Errorf("tokenProvider is %T, want *GcpIAMTokenProvider", c.tokenProvider)
		}
	})

	t.Run("explicit custom token provider", func(t *testing.T) {
		customProvider := &staticTestTokenProvider{token: "test-token"}
		c, err := New(ctx, "project", "sa", "http://actuator", "", keyFunc, 3600, "", WithTokenProvider(customProvider))
		if err != nil {
			t.Fatalf("New() failed: %v", err)
		}
		if c.tokenProvider != customProvider {
			t.Errorf("tokenProvider got %v, want %v", c.tokenProvider, customProvider)
		}
	})
}
