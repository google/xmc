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
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"time"

	"golang/google_api/googleapi/googleapi"
	"golang/google_api/idtoken/idtoken"
	apierrors "golang/k8s_io/apimachinery/v/v0_23/pkg/api/errors/errors"
	"golang/prometheus/client/prometheus/promauto/promauto"
	"golang/prometheus/client/prometheus/prometheus"
	"golang/protobuf/v2/encoding/protojson/protojson"
	"golang/uuid/uuid"
	"tink/go/jwt/jwt"
	"xmanager_cloud/cmd/actuator/pausescheduling"
	"xmanager_cloud/common/interfaces"
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
)

// TokenValidator defines the interface for validating authentication tokens (e.g. Google ID tokens or JWKS JWTs)
// against an expected audience and verifying identity claims.
type TokenValidator interface {
	// Validate verifies the provided token string against the target audience.
	// Returns an error if verification fails or required claims are missing.
	Validate(ctx context.Context, token string, audience string) error
}

var _ TokenValidator = (*GoogleIDTokenValidator)(nil)

// GoogleIDTokenValidator validates Google ID tokens against an expected audience and service account email.
type GoogleIDTokenValidator struct {
	ExpectedServiceAccount string
	ValidateFunc           func(ctx context.Context, token, audience string) (*idtoken.Payload, error)
}

// Validate validates the Google ID token.
func (v *GoogleIDTokenValidator) Validate(ctx context.Context, token string, audience string) error {
	if v == nil {
		return fmt.Errorf("token validator is nil")
	}
	if token == "" {
		return fmt.Errorf("auth token is empty")
	}
	if v.ExpectedServiceAccount == "" {
		return fmt.Errorf("expected service account is not configured")
	}

	validateFn := v.ValidateFunc
	if validateFn == nil {
		validateFn = idtoken.Validate
	}

	idPayload, err := validateFn(ctx, token, audience)
	if err != nil {
		return fmt.Errorf("failed to validate ID token: %w", err)
	}
	if idPayload == nil || idPayload.Claims == nil {
		return fmt.Errorf("id token payload or claims is nil")
	}

	email, ok := idPayload.Claims["email"].(string)
	if !ok {
		return fmt.Errorf("email claim not found in ID token")
	}

	if email != v.ExpectedServiceAccount {
		return fmt.Errorf("service account mismatch")
	}

	return nil
}

// K8sClient defines the Kubernetes operations required by the work unit task handler.
type K8sClient interface {
	CreateWorkUnit(ctx context.Context, payload *actuatortaskpb.ActuatorPayload) error
	DeleteWorkUnit(ctx context.Context, payload *actuatortaskpb.ActuatorPayload) error
	RestartWorkUnit(ctx context.Context, payload *actuatortaskpb.ActuatorPayload) error
	UpdateWorkUnit(ctx context.Context, payload *actuatortaskpb.ActuatorPayload) error
}

// Params contains the dependencies required to instantiate a Handler.
type Params struct {
	K8sClient              K8sClient
	ProjectID              string
	Audience               string
	XManagerServiceAccount string
	JWTVerifier            func() (jwt.Verifier, error)
	TokenValidator         TokenValidator
	PauseHandler           pausescheduling.PauseHandler
}

// Handler processes incoming actuation requests.
type Handler struct {
	k8sClient              K8sClient
	projectID              string
	audience               string
	xmanagerServiceAccount string
	JWTVerifier            func() (jwt.Verifier, error)
	tokenValidator         TokenValidator
	pauseHandler           pausescheduling.PauseHandler
}

// New creates a new Handler.
func New(p Params) (*Handler, error) {
	if p.K8sClient == nil {
		return nil, fmt.Errorf("k8s client is required")
	}
	if p.ProjectID == "" {
		return nil, fmt.Errorf("project ID is required")
	}
	if p.Audience == "" {
		return nil, fmt.Errorf("audience is required")
	}
	if p.XManagerServiceAccount == "" {
		return nil, fmt.Errorf("xmanager service account is required")
	}
	if p.JWTVerifier == nil {
		return nil, fmt.Errorf("jwt verifier is required")
	}

	validator := p.TokenValidator
	if validator == nil {
		validator = &GoogleIDTokenValidator{
			ExpectedServiceAccount: p.XManagerServiceAccount,
		}
	}

	return &Handler{
		k8sClient:              p.K8sClient,
		projectID:              p.ProjectID,
		audience:               p.Audience,
		xmanagerServiceAccount: p.XManagerServiceAccount,
		JWTVerifier:            p.JWTVerifier,
		tokenValidator:         validator,
		pauseHandler:           p.PauseHandler,
	}, nil
}

// TokenValidator returns the configured token validator.
func (h *Handler) TokenValidator() TokenValidator {
	return h.tokenValidator
}

func (h *Handler) isPaused(metadata *actuatortaskpb.Metadata) bool {
	return h.pauseHandler != nil && h.pauseHandler.IsPaused(metadata)
}

func (h *Handler) shouldPause(action *actuatortaskpb.Action) bool {
	return h.pauseHandler != nil && h.pauseHandler.ShouldPause(action)
}

func (h *Handler) handlePause(ctx context.Context, payload *actuatortaskpb.ActuatorPayload, traceID string) error {
	if h.pauseHandler == nil {
		return nil
	}
	return h.pauseHandler.HandlePause(ctx, payload, traceID)
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

	if h.isPaused(payload.GetMetadata()) && h.shouldPause(action) {
		return h.handlePause(ctx, payload, traceID)
	}

	switch {
	case action.GetCreate() != nil:
		logging.Info(ctx, "Received create request for work unit")
		if err := h.k8sClient.CreateWorkUnit(ctx, payload); err != nil {
			return err
		}
		workUnitsCreated.WithLabelValues().Inc()
		return nil
	case action.GetDelete() != nil:
		logging.Info(ctx, "Received delete request for work unit")
		return h.k8sClient.DeleteWorkUnit(ctx, payload)
	case action.GetRestart() != nil:
		logging.Info(ctx, "Received restart request for work unit")
		return h.k8sClient.RestartWorkUnit(ctx, payload)
	case action.GetUpdate() != nil:
		logging.Info(ctx, "Received update request for work unit")
		return h.k8sClient.UpdateWorkUnit(ctx, payload)
	default:
		logging.Warn(ctx, "Unsupported action type", "action", action)
		return apierrors.NewBadRequest("unsupported action type")
	}
}

func failTask(ctx context.Context, msg string, err error, retryable bool) redisclient.TaskResult {
	if err != nil {
		logging.Warn(ctx, msg, "error", err)
		err = fmt.Errorf("%s: %w", msg, err)
	} else {
		logging.Warn(ctx, msg)
		err = fmt.Errorf("%s", msg)
	}
	ctMessagesFailed.WithLabelValues().Inc()
	return redisclient.TaskResult{WasRunSuccessful: false, ShouldBeRetried: retryable, Error: err}
}

// HandleWorkUnitRedisTask processes a task received from Redis.
func (h *Handler) HandleWorkUnitRedisTask(ctx context.Context, workerID, data string) redisclient.TaskResult {
	logger := logging.NewGCPLogger(slog.LevelInfo, h.projectID)
	ctx = logging.WithLogger(ctx, logger)

	logging.Info(ctx, "Processing Redis task", "worker_id", workerID)

	var msg interfaces.RedisTask
	if err := json.Unmarshal([]byte(data), &msg); err != nil {
		return failTask(ctx, "failed to unmarshal redis message", err, false)
	}

	if err := h.validateSignature(msg.Signature, msg.Payload); err != nil {
		return failTask(ctx, "failed to validate signature", err, false)
	}

	if err := h.validateAuthTokenString(ctx, msg.OidcToken); err != nil {
		return failTask(ctx, "failed to validate OIDC token", err, isRetryableError(err))
	}

	var payload actuatortaskpb.ActuatorPayload
	if err := protojson.Unmarshal(msg.Payload, &payload); err != nil {
		return failTask(ctx, "failed to unmarshal actuator payload", err, false)
	}

	if err := h.handleActuatorPayload(ctx, &payload); err != nil {
		return failTask(ctx, "failed to process Redis task", err, isRetryableError(err))
	}

	return redisclient.TaskResult{WasRunSuccessful: true, ShouldBeRetried: false, Error: nil}
}

func isTransientHTTPStatus(code int) bool {
	switch code {
	case http.StatusTooManyRequests,
		http.StatusInternalServerError,
		http.StatusBadGateway,
		http.StatusServiceUnavailable,
		http.StatusGatewayTimeout:
		return true
	default:
		return false
	}
}

func isK8sRetryableError(err error) bool {
	var statusErr *apierrors.StatusError
	if errors.As(err, &statusErr) {
		if statusErr == nil {
			return false
		}
		err = statusErr
	}
	return apierrors.IsTimeout(err) || apierrors.IsServerTimeout(err) ||
		apierrors.IsTooManyRequests(err) || apierrors.IsServiceUnavailable(err) ||
		apierrors.IsInternalError(err) || apierrors.IsConflict(err)
}

func isRetryableError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	var timeoutErr interface{ Timeout() bool }
	if errors.As(err, &timeoutErr) && timeoutErr.Timeout() {
		return true
	}
	var tempErr interface{ Temporary() bool }
	if errors.As(err, &tempErr) && tempErr.Temporary() {
		return true
	}
	var gErr *googleapi.Error
	if errors.As(err, &gErr) && isTransientHTTPStatus(gErr.Code) {
		return true
	}
	var statusCodeErr interface{ StatusCode() int }
	if errors.As(err, &statusCodeErr) && isTransientHTTPStatus(statusCodeErr.StatusCode()) {
		return true
	}
	var httpStatusErr interface{ HTTPStatusCode() int }
	if errors.As(err, &httpStatusErr) && isTransientHTTPStatus(httpStatusErr.HTTPStatusCode()) {
		return true
	}
	return isK8sRetryableError(err)
}

// validateAuthTokenString validates the auth token string using the configured TokenValidator.
func (h *Handler) validateAuthTokenString(ctx context.Context, authToken string) error {
	if h.tokenValidator == nil {
		return fmt.Errorf("token validator is not configured")
	}
	return h.tokenValidator.Validate(ctx, authToken, h.audience)
}

func calculateSHA256(payload []byte) string {
	hash := sha256.Sum256(payload)
	return fmt.Sprintf("sha256:%x", hash)
}

// validateSignature validates the signature of the request using the verifying key.
func (h *Handler) validateSignature(tokenString string, body []byte) error {
	if tokenString == "" {
		return fmt.Errorf("signature not found")
	}

	verifier, err := h.JWTVerifier()
	if err != nil {
		return fmt.Errorf("failed to get JWT verifier: %w", err)
	}

	validator, err := jwt.NewValidator(&jwt.ValidatorOpts{ExpectedAudience: &h.audience, ExpectedIssuer: &h.xmanagerServiceAccount})
	if err != nil {
		return fmt.Errorf("failed to create JWT validator: %w", err)
	}

	verifiedJWT, err := verifier.VerifyAndDecode(tokenString, validator)
	if err != nil {
		return fmt.Errorf("failed to verify JWT: %w", err)
	}

	extractedSubject, err := verifiedJWT.Subject()
	if err != nil {
		return fmt.Errorf("failed to get JWT subject: %w", err)
	}
	if extractedSubject != h.xmanagerServiceAccount {
		return fmt.Errorf("invalid JWT subject claim: expected %s, got %s", h.xmanagerServiceAccount, extractedSubject)
	}

	issuedAt, err := verifiedJWT.IssuedAt()
	if err != nil {
		return fmt.Errorf("failed to get JWT issued-at: %w", err)
	}

	expiresAt, err := verifiedJWT.ExpiresAt()
	if err != nil {
		return fmt.Errorf("failed to get JWT expiration: %w", err)
	}
	if expiresAt.Before(time.Now()) {
		return fmt.Errorf("JWT expired")
	}
	if issuedAt.After(expiresAt) {
		return fmt.Errorf("JWT issued-at is after expiration")
	}

	bodyHash := calculateSHA256(body)
	messageDigest, err := verifiedJWT.StringClaim("message_digest")
	if err != nil {
		return fmt.Errorf("failed to get JWT message_digest: %w", err)
	}
	if messageDigest != bodyHash {
		return fmt.Errorf("invalid JWT message_digest claim")
	}

	return nil
}
