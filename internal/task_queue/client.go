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
// Package client provides a client for enqueuing tasks to Cloud Tasks. These tasks are used to
// actuate WorkUnits on the Kubernetes Actuator.
package client

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"time"

	"github.com/google/uuid"
	"github.com/tink-crypto/tink-go/v2/jwt"
	"golang.org/x/oauth2/google"
	"google.golang.org/api/iamcredentials/v1"
	"google.golang.org/api/option"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"xmanager_cloud/common/interfaces"
	"xmanager_cloud/kubernetes/common/constants"
	"xmanager_cloud/logging/logging"
	"xmanager_cloud/task_queue_redis/redisclient"

	"google.golang.org/protobuf/types/known/timestamppb"
	essmsgpb "xmanager_cloud/experiment_state_server/proto/messages_go_proto"
	actuatortaskpb "xmanager_cloud/task_queue/proto/task_go_proto"
)

const (
	// SignatureHeader is the name of the header containing the task payload signature.
	SignatureHeader = "signature"
	// DefaultSignatureTTLSeconds is the default signature TTL in seconds.
	DefaultSignatureTTLSeconds = 3600
)

var osHostname = os.Hostname

type Client struct {
	invokerSA        string
	gcpProjectID     string
	actuatorURL      string
	actuatorAudience string
	signer           func() (jwt.Signer, error)
	signatureTTL     time.Duration
	redisClient      redisclient.Publisher
}

// New creates a new Client instance.
func New(ctx context.Context, projectID, invokerSA, actuatorURL, actuatorAudience string, signer func() (jwt.Signer, error), signatureTTL int, redisAddr string) (*Client, error) {
	aud := actuatorAudience
	if aud == "" {
		aud = actuatorURL
	}
	ttl := signatureTTL
	if ttl == 0 {
		logging.Warn(ctx, "signatureTTL is 0, using default value", "default", DefaultSignatureTTLSeconds)
		ttl = DefaultSignatureTTLSeconds
	}

	redisConfig := redisclient.Config{
		Addr: redisAddr,
	}
	redisClient := redisclient.NewPublisher(redisConfig, constants.RedisStream)
	logging.Info(ctx, "Initialized Redis task queue publisher")

	return &Client{
		invokerSA:        invokerSA,
		gcpProjectID:     projectID,
		actuatorURL:      actuatorURL,
		actuatorAudience: aud,
		signer:           signer,
		signatureTTL:     time.Duration(ttl) * time.Second,
		redisClient:      redisClient,
	}, nil
}

// EnqueueWorkUnitTask creates and enqueues a Cloud Task to actuate a WorkUnit.
func (c *Client) EnqueueWorkUnitTask(ctx context.Context, target string, workUnit *essmsgpb.WorkUnit, action *actuatortaskpb.Action, operationName string) error {
	now := timestamppb.Now()

	traceID, ok := logging.OperationID(ctx)
	if !ok {
		traceID = uuid.NewString()
	}

	metadata := &actuatortaskpb.Metadata{
		IdempotencyKey: proto.String(generateIdempotencyKey(target, action)),
		OperationName:  proto.String(operationName),
		TraceId:        proto.String(traceID),
	}

	payload := &actuatortaskpb.ActuatorPayload{
		Kind:       actuatortaskpb.ActuatorPayload_WORK_UNIT_ACTUATION_REQUEST.Enum(),
		Target:     &target,
		WorkUnit:   workUnit,
		Action:     action,
		Metadata:   metadata,
		CreateTime: now,
	}

	body, err := protojson.Marshal(payload)
	if err != nil {
		logging.Warn(ctx, "Failed to marshal actuator payload", "error", err)
		return fmt.Errorf("failed to marshal actuator payload: %w", err)
	}

	bodyHash := calculateSHA256(body)
	jwtToken, err := c.createJwtToken(bodyHash)
	if err != nil {
		logging.Warn(ctx, "Failed to create message signature", "error", err)
		return fmt.Errorf("failed to create message signature: %w", err)
	}

	oidcTokenString, err := generateOIDCToken(ctx, c.invokerSA, c.actuatorAudience)
	if err != nil {
		logging.Warn(ctx, "Failed to generate OIDC token for redis task", "error", err)
		return fmt.Errorf("failed to generate OIDC token for redis task: %w", err)
	}

	msg := interfaces.RedisTask{
		Payload:   body,
		Signature: jwtToken,
		OidcToken: oidcTokenString,
	}
	redisMsg, err := json.Marshal(msg)
	if err != nil {
		logging.Warn(ctx, "Failed to marshal redis message", "error", err)
		return fmt.Errorf("failed to marshal redis message: %w", err)
	}
	err = c.redisClient.AddMessage(ctx, string(redisMsg), "0")
	if err != nil {
		return fmt.Errorf("failed to add message to redis: %w", err)
	}
	return nil
}

// Close cleans up the underlying client resources.
func (c *Client) Close() error {
	if c.redisClient == nil {
		return nil
	}
	return c.redisClient.Close(context.Background())
}

// PingRedis checks the health of the Redis connection.
func (c *Client) PingRedis(ctx context.Context) error {
	if c.redisClient == nil {
		logging.Error(ctx, "Redis client is nil")
		return fmt.Errorf("redis client is nil")
	}
	return c.redisClient.Ping(ctx)
}

// GetQueueDepth returns the total number of unread and pending messages in the Redis stream.
func (c *Client) GetQueueDepth(ctx context.Context) (int64, error) {
	if c.redisClient == nil {
		return 0, fmt.Errorf("redis client is nil")
	}
	return c.redisClient.GetQueueDepth(ctx)
}

func generateIdempotencyKey(target string, action *actuatortaskpb.Action) string {
	var actionType string
	switch action.GetAction().(type) {
	case *actuatortaskpb.Action_Create:
		actionType = "CREATE"
	case *actuatortaskpb.Action_Delete:
		actionType = "DELETE"
	case *actuatortaskpb.Action_Restart:
		actionType = "RESTART"
	case *actuatortaskpb.Action_Update:
		actionType = "UPDATE"
	default:
		actionType = "UNKNOWN"
	}
	return fmt.Sprintf("%s_%s", target, actionType)
}

func calculateSHA256(payload []byte) string {
	hash := sha256.Sum256(payload)
	return fmt.Sprintf("sha256:%x", hash)
}

func (c *Client) createJwtToken(bodyHash string) (string, error) {
	now := time.Now()
	expires := now.Add(c.signatureTTL)

	rawJWT, err := jwt.NewRawJWT(&jwt.RawJWTOptions{
		Audience:  &c.actuatorAudience,
		Subject:   &c.invokerSA,
		Issuer:    &c.invokerSA,
		IssuedAt:  &now,
		ExpiresAt: &expires,
		CustomClaims: map[string]any{
			"message_digest": bodyHash,
		},
	})
	if err != nil {
		return "", fmt.Errorf("failed to create raw JWT: %w", err)
	}

	signer, err := c.signer()
	if err != nil {
		return "", fmt.Errorf("failed to get JWT signer: %w", err)
	}

	token, err := signer.SignAndEncode(rawJWT)
	if err != nil {
		return "", fmt.Errorf("failed to sign and encode JWT: %w", err)
	}
	return token, nil
}

var generateOIDCToken = func(ctx context.Context, serviceAccountEmail string, audience string) (string, error) {
	creds, err := google.FindDefaultCredentials(ctx, iamcredentials.CloudPlatformScope)
	if err != nil {
		return "", fmt.Errorf("failed to find default credentials: %w", err)
	}

	iamService, err := iamcredentials.NewService(ctx, option.WithCredentials(creds))
	if err != nil {
		return "", fmt.Errorf("failed to create iamcredentials service: %w", err)
	}

	resourceName := "projects/-/serviceAccounts/" + serviceAccountEmail
	req := &iamcredentials.GenerateIdTokenRequest{
		Audience:     audience,
		IncludeEmail: true,
	}

	logging.Info(ctx, "Generate ID token for service account", "resource_name", resourceName)
	resp, err := iamService.Projects.ServiceAccounts.GenerateIdToken(resourceName, req).Context(ctx).Do()
	if err != nil {
		return "", fmt.Errorf("failed to generate ID token: %w", err)
	}
	logging.Info(ctx, "Generated ID token for service account", "resource_name", resourceName)
	return resp.Token, nil
}

// CreateAction creates an action for the Actuator to create a WorkUnit.
func CreateAction(experimentPriority essmsgpb.Priority, author string) *actuatortaskpb.Action {
	return &actuatortaskpb.Action{
		Action: &actuatortaskpb.Action_Create{
			Create: &actuatortaskpb.CreateAction{
				ExperimentPriority: experimentPriority.Enum(),
				Author:             proto.String(author),
			},
		},
	}
}

// DeleteAction creates an action for the Actuator to delete a WorkUnit.
func DeleteAction() *actuatortaskpb.Action {
	return &actuatortaskpb.Action{
		Action: &actuatortaskpb.Action_Delete{
			Delete: &actuatortaskpb.DeleteAction{},
		},
	}
}

// RestartAction creates an action for the Actuator to restart a WorkUnit.
func RestartAction(experimentPriority essmsgpb.Priority, author string) *actuatortaskpb.Action {
	return &actuatortaskpb.Action{
		Action: &actuatortaskpb.Action_Restart{
			Restart: &actuatortaskpb.RestartAction{
				ExperimentPriority: experimentPriority.Enum(),
				Author:             proto.String(author),
			},
		},
	}
}
