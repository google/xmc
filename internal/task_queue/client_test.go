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
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"github.com/tink-crypto/tink-go/v2/jwt"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/testing/protocmp"
	"xmanager_cloud/common/interfaces"
	essmsgpb "xmanager_cloud/experiment_state_server/proto/messages_go_proto"
	"xmanager_cloud/security/securitytest"
	actuatortaskpb "xmanager_cloud/task_queue/proto/task_go_proto"
)

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

			oldGenerateOIDCToken := generateOIDCToken
			generateOIDCToken = func(ctx context.Context, serviceAccountEmail string, audience string) (string, error) {
				return "mock-oidc-token", nil
			}
			defer func() {
				generateOIDCToken = oldGenerateOIDCToken
			}()

			c := CreateTaskQueueClient(ctx, t)
			redisClient := mockAndConfigureRedisClient(t, c)

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
				t.Errorf("Signature is empty, want non-empty")
			} else {
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
			wantIdempotencyKey := fmt.Sprintf("%s_%s", tc.target, tc.actionType)
			if payload.GetMetadata().GetIdempotencyKey() != wantIdempotencyKey {
				t.Errorf("IdempotencyKey got %q, want %q", payload.GetMetadata().GetIdempotencyKey(), wantIdempotencyKey)
			}
			if payload.GetMetadata().GetOperationName() != operationName {
				t.Errorf("OperationName got %q, want %q", payload.GetMetadata().GetOperationName(), operationName)
			}
		})
	}
}

func TestEnqueueWorkUnitTask_OIDCGeneratorFailure(t *testing.T) {
	ctx := t.Context()

	oldGenerateOIDCToken := generateOIDCToken
	defer func() {
		generateOIDCToken = oldGenerateOIDCToken
	}()

	generateOIDCToken = func(ctx context.Context, serviceAccountEmail string, audience string) (string, error) {
		return "", fmt.Errorf("oidc error")
	}

	c := CreateTaskQueueClient(ctx, t)

	action := &actuatortaskpb.Action{
		Action: &actuatortaskpb.Action_Create{
			Create: &actuatortaskpb.CreateAction{},
		},
	}
	err := c.EnqueueWorkUnitTask(ctx, "target", nil, action, "op")
	if err == nil {
		t.Fatal("EnqueueWorkUnitTask() succeeded, want error")
	}
	if !strings.Contains(err.Error(), "oidc error") {
		t.Errorf("Error message %q does not contain 'oidc error'", err.Error())
	}
}

func TestEnqueueWorkUnitTask_RedisAddMessageFailure(t *testing.T) {
	ctx := t.Context()

	oldGenerateOIDCToken := generateOIDCToken
	generateOIDCToken = func(ctx context.Context, serviceAccountEmail string, audience string) (string, error) {
		return "mock-oidc-token", nil
	}
	defer func() {
		generateOIDCToken = oldGenerateOIDCToken
	}()

	c := CreateTaskQueueClient(ctx, t)
	fakeRedis := GetFakeRedisClient(c)
	fakeRedis.AddMessageError = fmt.Errorf("redis xadd error")

	action := &actuatortaskpb.Action{
		Action: &actuatortaskpb.Action_Create{
			Create: &actuatortaskpb.CreateAction{},
		},
	}
	err := c.EnqueueWorkUnitTask(ctx, "target", nil, action, "op")
	if err == nil {
		t.Fatal("EnqueueWorkUnitTask() succeeded, want error")
	}
	if !strings.Contains(err.Error(), "redis xadd error") {
		t.Errorf("Error message %q does not contain 'redis xadd error'", err.Error())
	}
}

func TestGenerateIdempotencyKeyUnknown(t *testing.T) {
	key := generateIdempotencyKey("target", &actuatortaskpb.Action{})
	if key != "target_UNKNOWN" {
		t.Errorf("generateIdempotencyKey with unknown action got %q, want %q", key, "target_UNKNOWN")
	}
}
