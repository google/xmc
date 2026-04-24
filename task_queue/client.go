// Copyright 2025 Google LLC
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

	"golang/github_com/gax/v/v2/v2/gax"
	"golang/google_api/iamcredentials/v1/iamcredentials"
	"golang/google_api/option/option"
	"golang/oauth2/google/google"
	"golang/protobuf/v2/encoding/protojson/protojson"
	"golang/protobuf/v2/proto/proto"
	"golang/uuid/uuid"
	"tink/go/jwt/jwt"
	"xmanager_cloud/common/interfaces"
	"xmanager_cloud/kubernetes/common/constants"
	"xmanager_cloud/logging/logging"
	"xmanager_cloud/task_queue_redis/redisclient"

	cloudtaskspb "cloud/tasks/v2/cloudtasks_go_proto"
	targetpb "cloud/tasks/v2/target_go_proto"
	taskpb "cloud/tasks/v2/task_go_proto"
	timestamppb "protobuf/timestamp_go_proto"
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

// CloudTasksClient is an interface for the methods of cloudtasks.Client used by this package.
// This allows for dependency injection during testing.
type CloudTasksClient interface {
	CreateTask(ctx context.Context, req *cloudtaskspb.CreateTaskRequest, opts ...gax.CallOption) (*taskpb.Task, error)
	Close() error
}

// Client is a wrapper around the Cloud Tasks client for enqueuing tasks to the Actuator.
type Client struct {
	tasksClient      CloudTasksClient
	invokerSA        string
	queuePath        string
	gcpProjectID     string
	actuatorURL      string
	actuatorAudience string
	signer           func() (jwt.Signer, error)
	signatureTTL     time.Duration
	redisClient      redisclient.Publisher
}

// New creates a new Client instance.
func New(ctx context.Context, cloudTasksClient CloudTasksClient, projectID, location, queueID, invokerSA, actuatorURL, actuatorAudience string, signer func() (jwt.Signer, error), signatureTTL int, useRedis bool, redisAddr string) (*Client, error) {
	queuePath := fmt.Sprintf("projects/%s/locations/%s/queues/%s", projectID, location, queueID)
	aud := actuatorAudience
	if aud == "" {
		aud = actuatorURL
	}
	ttl := signatureTTL
	if ttl == 0 {
		logging.Warn(ctx, "signatureTTL is 0, using default value", "default", DefaultSignatureTTLSeconds)
		ttl = DefaultSignatureTTLSeconds
	}

	var redisClient redisclient.Publisher
	if useRedis {
		redisConfig := redisclient.Config{
			Addr: redisAddr,
		}
		redisClient = redisclient.NewPublisher(redisConfig, constants.RedisStream)
		logging.Info(ctx, "Initialized Redis task queue publisher")
	}

	return &Client{
		tasksClient:      cloudTasksClient,
		invokerSA:        invokerSA,
		queuePath:        queuePath,
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
		traceID = uuid.New()
	}

	metadata := &actuatortaskpb.Metadata{
		IdempotencyKey: proto.String(generateIdempotencyKey(target, action, now.AsTime())),
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

	if c.redisClient != nil {
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
		c.redisClient.AddMessage(ctx, string(redisMsg), "0")
		return nil
	}

	req := &cloudtaskspb.CreateTaskRequest{
		Parent: c.queuePath,
		Task: &taskpb.Task{
			MessageType: &taskpb.Task_HttpRequest{
				HttpRequest: &targetpb.HttpRequest{
					HttpMethod: targetpb.HttpMethod_POST,
					Url:        c.actuatorURL,
					Headers: map[string]string{
						"Content-Type":  "application/json",
						SignatureHeader: jwtToken,
					},
					AuthorizationHeader: &targetpb.HttpRequest_OidcToken{
						OidcToken: &targetpb.OidcToken{
							ServiceAccountEmail: c.invokerSA,
							Audience:            c.actuatorAudience,
						},
					},
					Body: body,
				},
			},
		},
	}

	// The CreateTask RPC fails if the context deadline is greater than 30s.
	timeout := 30 * time.Second
	if deadline, ok := ctx.Deadline(); ok {
		if remaining := time.Until(deadline); remaining < timeout {
			timeout = remaining
		}
	}
	createTaskCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	_, err = c.tasksClient.CreateTask(createTaskCtx, req)
	if err != nil {
		return fmt.Errorf("failed to create cloud task: %w", err)
	}

	return nil
}

// Close cleans up the underlying client resources.
func (c *Client) Close() error {
	return c.tasksClient.Close()
}

// PingRedis checks the health of the Redis connection.
func (c *Client) PingRedis(ctx context.Context) error {
	if c.redisClient == nil {
		logging.Error(ctx, "Redis client is nil")
		return fmt.Errorf("redis client is nil")
	}
	return c.redisClient.Ping(ctx)
}

func generateIdempotencyKey(target string, action *actuatortaskpb.Action, curTime time.Time) string {
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
	return fmt.Sprintf("%s_%s_%s", target, actionType, curTime.Format(time.RFC3339Nano))
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
