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
// Package workunittaskhandler handles the incoming work unit tasks.
package workunittaskhandler

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"golang/google_api/idtoken/idtoken"
	apierrors "golang/k8s_io/apimachinery/v/v0_23/pkg/api/errors/errors"
	"golang/prometheus/client/prometheus/promauto/promauto"
	"golang/prometheus/client/prometheus/prometheus"
	"golang/protobuf/v2/encoding/protojson/protojson"
	"golang/uuid/uuid"
	"tink/go/jwt/jwt"
	"xmanager_cloud/common/interfaces"
	"xmanager_cloud/kubernetes/actuator/k8s_client/k8sclient"
	"xmanager_cloud/logging/logging"
	actuatortaskpb "xmanager_cloud/task_queue/proto/task_go_proto"
	"xmanager_cloud/task_queue_redis/redisclient"
)

var (
	ctMessagesProcessed = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "xm_cloud_actuator_ct_messages_processed_total",
		Help: "Total number of CT messages correctly processed by the actuator",
	}, []string{})
	ctMessagesFailed = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "xm_cloud_actuator_ct_messages_failed_total",
		Help: "Total number of CT messages that failed processing",
	}, []string{})
	workUnitsCreated = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "xm_cloud_actuator_workunits_created_total",
		Help: "Total number of WorkUnits successfully created",
	}, []string{})
	idtokenValidate = idtoken.Validate
)

type k8sClient interface {
	CreateWorkUnit(ctx context.Context, payload *actuatortaskpb.ActuatorPayload) error
	DeleteWorkUnit(ctx context.Context, payload *actuatortaskpb.ActuatorPayload) error
	RestartWorkUnit(ctx context.Context, payload *actuatortaskpb.ActuatorPayload) error
	UpdateWorkUnit(ctx context.Context, payload *actuatortaskpb.ActuatorPayload) error
	ManagedXProfGCSBucket(ctx context.Context) (string, error)
}

// Handler processes incoming actuation requests.
type Handler struct {
	k8sClient              k8sClient
	projectID              string
	audience               string
	xmanagerServiceAccount string
	JWTVerifier            func() (jwt.Verifier, error)
	redisClient            redisclient.Consumer
}

// New creates a new Handler.
func New(client *k8sclient.Client, projectID string, audience string, xmanagerServiceAccount string, jwtVerifier func() (jwt.Verifier, error), redisClient redisclient.Consumer) *Handler {
	return &Handler{
		k8sClient:              client,
		projectID:              projectID,
		audience:               audience,
		xmanagerServiceAccount: xmanagerServiceAccount,
		JWTVerifier:            jwtVerifier,
		redisClient:            redisClient,
	}
}

func (h *Handler) handleActuatorPayload(ctx context.Context, payload *actuatortaskpb.ActuatorPayload) error {
	ctMessagesProcessed.WithLabelValues().Inc()

	ctx = logging.WithWorkUnitExecutionName(ctx, payload.GetTarget())

	traceID := payload.GetMetadata().GetTraceId()
	if traceID == "" {
		traceID = uuid.New()
	}
	ctx = logging.InjectOperation(ctx, traceID)

	operationName := payload.GetMetadata().GetOperationName()
	if operationName != "" {
		ctx = logging.WithOperationName(ctx, operationName)
	}

	action := payload.GetAction()

	if action.GetCreate() != nil {
		logging.Info(ctx, "Received create request for work unit")
		if err := h.k8sClient.CreateWorkUnit(ctx, payload); err != nil {
			return err
		}
		workUnitsCreated.WithLabelValues().Inc()

	} else if action.GetDelete() != nil {
		logging.Info(ctx, "Received delete request for work unit")
		if err := h.k8sClient.DeleteWorkUnit(ctx, payload); err != nil {
			return err
		}

	} else if action.GetRestart() != nil {
		logging.Info(ctx, "Received restart request for work unit")
		if err := h.k8sClient.RestartWorkUnit(ctx, payload); err != nil {
			return err
		}

	} else if action.GetUpdate() != nil {
		logging.Info(ctx, "Received update request for work unit")
		if err := h.k8sClient.UpdateWorkUnit(ctx, payload); err != nil {
			return err
		}

	} else {
		logging.Warn(ctx, "Unsupported action type", "action", action)
		return apierrors.NewBadRequest("unsupported action type")
	}

	return nil
}

// HandleWorkUnitRedisTask processes a task received from Redis.
func (h *Handler) HandleWorkUnitRedisTask(ctx context.Context, workerID, data string) redisclient.TaskResult {
	logger := logging.NewGCPLogger(slog.LevelInfo, h.projectID)
	ctx = logging.WithLogger(ctx, logger)

	logging.Info(ctx, "Processing Redis task", "worker_id", workerID)

	var msg interfaces.RedisTask
	if err := json.Unmarshal([]byte(data), &msg); err != nil {
		logging.Warn(ctx, "Failed to unmarshal Redis message", "error", err)
		ctMessagesFailed.WithLabelValues().Inc()
		return redisclient.TaskResult{WasRunSuccessful: false, ShouldBeRetried: false, Error: fmt.Errorf("failed to unmarshal redis message: %w", err)}
	}

	if !h.validateSignature(ctx, msg.Signature, msg.Payload) {
		logging.Warn(ctx, "Failed to validate signature")
		ctMessagesFailed.WithLabelValues().Inc()
		return redisclient.TaskResult{WasRunSuccessful: false, ShouldBeRetried: false, Error: fmt.Errorf("failed to validate signature")}
	}

	if err := h.validateAuthTokenString(ctx, msg.OidcToken); err != nil {
		logging.Warn(ctx, "Failed to validate OIDC token", "error", err)
		ctMessagesFailed.WithLabelValues().Inc()
		return redisclient.TaskResult{WasRunSuccessful: false, ShouldBeRetried: false, Error: fmt.Errorf("failed to validate OIDC token: %w", err)}
	}

	var payload actuatortaskpb.ActuatorPayload
	if err := protojson.Unmarshal(msg.Payload, &payload); err != nil {
		logging.Warn(ctx, "Failed to unmarshal ActuatorPayload", "error", err)
		ctMessagesFailed.WithLabelValues().Inc()
		return redisclient.TaskResult{WasRunSuccessful: false, ShouldBeRetried: false, Error: fmt.Errorf("failed to unmarshal actuator payload: %w", err)}
	}

	err := h.handleActuatorPayload(ctx, &payload)
	if err != nil {
		logging.Warn(ctx, "Failed to process Redis task", "error", err)
		ctMessagesFailed.WithLabelValues().Inc()
		return redisclient.TaskResult{WasRunSuccessful: false, ShouldBeRetried: isRetryableError(err), Error: err}
	}

	return redisclient.TaskResult{WasRunSuccessful: true, ShouldBeRetried: false, Error: nil}
}

func isRetryableError(err error) bool {
	return apierrors.IsTimeout(err) || apierrors.IsServerTimeout(err) || apierrors.IsTooManyRequests(err) || apierrors.IsServiceUnavailable(err) || apierrors.IsInternalError(err) || apierrors.IsConflict(err)
}

// extractOIDCToken extracts the Authorization token from the Authorization header.
func extractOIDCToken(authHeader string) (string, bool) {
	parts := strings.Split(authHeader, " ")
	if len(parts) != 2 || strings.ToLower(parts[0]) != "bearer" {
		return "", false
	}
	return parts[1], true
}

// validateAuthTokenString validates the auth token string, including the service account email.
func (h *Handler) validateAuthTokenString(ctx context.Context, authToken string) error {
	if authToken == "" {
		return fmt.Errorf("auth token is empty")
	}

	idPayload, err := idtokenValidate(ctx, authToken, h.audience)
	if err != nil {
		return fmt.Errorf("failed to validate ID token: %w", err)
	}

	email, ok := idPayload.Claims["email"].(string)
	if !ok {
		return fmt.Errorf("email claim not found in ID token")
	}

	if email != h.xmanagerServiceAccount {
		return fmt.Errorf("service account mismatch")
	}

	return nil
}

func calculateSHA256(payload []byte) string {
	hash := sha256.Sum256(payload)
	return fmt.Sprintf("sha256:%x", hash)
}

// validateSignature validates the signature of the request using the verifying key.
func (h *Handler) validateSignature(ctx context.Context, tokenString string, body []byte) bool {
	if tokenString == "" {
		logging.Warn(ctx, "Signature not found")
		return false
	}

	verifier, err := h.JWTVerifier()
	if err != nil {
		logging.Warn(ctx, "Failed to get JWT verifier", "error", err)
		return false
	}

	validator, err := jwt.NewValidator(&jwt.ValidatorOpts{ExpectedAudience: &h.audience, ExpectedIssuer: &h.xmanagerServiceAccount})
	if err != nil {
		logging.Warn(ctx, "Failed to create JWT validator", "error", err)
		return false
	}

	verifiedJWT, err := verifier.VerifyAndDecode(tokenString, validator)
	if err != nil {
		logging.Warn(ctx, "Failed to verify JWT", "error", err)
		return false
	}

	extractedSubject, err := verifiedJWT.Subject()
	if err != nil {
		logging.Warn(ctx, "Failed to get JWT subject", "error", err)
		return false
	}
	if extractedSubject != h.xmanagerServiceAccount {
		logging.Warn(ctx, "Invalid JWT subject claim")
		return false
	}

	issuedAt, err := verifiedJWT.IssuedAt()
	if err != nil {
		logging.Warn(ctx, "Failed to get JWT issued-at", "error", err)
		return false
	}

	expiresAt, err := verifiedJWT.ExpiresAt()
	if err != nil {
		logging.Warn(ctx, "Failed to get JWT expiration", "error", err)
		return false
	}
	if expiresAt.Before(time.Now()) {
		logging.Warn(ctx, "JWT expired")
		return false
	}
	if issuedAt.After(expiresAt) {
		logging.Warn(ctx, "JWT issued-at is after expiration")
		return false
	}

	bodyHash := calculateSHA256(body)

	messageDigest, err := verifiedJWT.StringClaim("message_digest")
	if err != nil {
		logging.Warn(ctx, "Failed to get JWT message_digest", "error", err)
		return false
	}
	if messageDigest != bodyHash {
		logging.Warn(ctx, "Invalid JWT message_digest claim")
		return false
	}

	return true
}
