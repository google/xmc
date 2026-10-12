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
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/google/xmc/internal/common/interfaces"
	"github.com/google/xmc/internal/kubernetes/common/constants"
	"github.com/google/xmc/internal/logging"
	"github.com/google/xmc/internal/task_queue_redis/redisclient"

	"github.com/google/uuid"
	"github.com/tink-crypto/tink-go/v2/jwt"
	"golang.org/x/oauth2/google"
	"golang.org/x/sync/singleflight"
	"google.golang.org/api/iamcredentials/v1"
	"google.golang.org/api/option"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"

	essmsgpb "github.com/google/xmc/api/protos/experiment"
	actuatortaskpb "github.com/google/xmc/api/protos/task_queue"

	"google.golang.org/protobuf/types/known/timestamppb"
)

const (
	// SignatureHeader is the name of the header containing the task payload signature.
	SignatureHeader = "signature"
	// DefaultSignatureTTLSeconds is the default signature TTL in seconds.
	DefaultSignatureTTLSeconds = 3600
)

// TokenProvider defines the interface for generating authentication tokens (e.g. OIDC tokens)
// attached to task queue dispatches.
type TokenProvider interface {
	// GenerateToken generates or retrieves an authentication token for the given service account and audience.
	GenerateToken(ctx context.Context, invokerSA, audience string) (string, error)
}

// TokenProviderFunc allows plain functions or closures to satisfy TokenProvider.
type TokenProviderFunc func(ctx context.Context, invokerSA, audience string) (string, error)

// GenerateToken calls the underlying function.
func (f TokenProviderFunc) GenerateToken(ctx context.Context, invokerSA, audience string) (string, error) {
	return f(ctx, invokerSA, audience)
}

// GcpIAMTokenProvider generates OIDC ID tokens using Google Cloud IAM Credentials API.
type GcpIAMTokenProvider struct{}

// GenerateToken generates an OIDC ID token for the specified service account and audience.
func (p *GcpIAMTokenProvider) GenerateToken(ctx context.Context, invokerSA, audience string) (string, error) {
	if invokerSA == "" {
		return "", fmt.Errorf("invoker service account cannot be empty")
	}
	if audience == "" {
		return "", fmt.Errorf("audience cannot be empty")
	}

	creds, err := google.FindDefaultCredentials(ctx, iamcredentials.CloudPlatformScope)
	if err != nil {
		return "", fmt.Errorf("failed to find default credentials: %w", err)
	}

	iamService, err := iamcredentials.NewService(ctx, option.WithCredentials(creds))
	if err != nil {
		return "", fmt.Errorf("failed to create iamcredentials service: %w", err)
	}

	resourceName := "projects/-/serviceAccounts/" + invokerSA
	req := &iamcredentials.GenerateIdTokenRequest{
		Audience:     audience,
		IncludeEmail: true,
	}

	resp, err := iamService.Projects.ServiceAccounts.GenerateIdToken(resourceName, req).Context(ctx).Do()
	if err != nil {
		return "", fmt.Errorf("failed to generate ID token for service account: %w", err)
	}
	return resp.Token, nil
}

// OidcTokenProviderConfig holds parameters for fetching real signed OIDC tokens from an IdP.
type OidcTokenProviderConfig struct {
	TokenURL     string
	ClientID     string
	ClientSecret string
	Username     string
	Password     string
	Scope        string
	HTTPClient   *http.Client
}

// OidcTokenProvider fetches signed OIDC ID tokens from an OpenID Connect provider.
type OidcTokenProvider struct {
	cfg        OidcTokenProviderConfig
	httpClient *http.Client
	mu         sync.RWMutex
	sfGroup    singleflight.Group
	token      string
	expiry     time.Time
}

// NewOidcTokenProvider creates a new OidcTokenProvider.
func NewOidcTokenProvider(cfg OidcTokenProviderConfig) *OidcTokenProvider {
	client := cfg.HTTPClient
	if client == nil {
		client = &http.Client{Timeout: 10 * time.Second}
	}
	if cfg.Scope == "" {
		cfg.Scope = "openid email profile"
	}
	return &OidcTokenProvider{
		cfg:        cfg,
		httpClient: client,
	}
}

// GenerateToken fetches or returns a cached signed OIDC token.
func (p *OidcTokenProvider) GenerateToken(ctx context.Context, invokerSA, audience string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}

	p.mu.RLock()
	if p.token != "" && time.Now().Add(1*time.Minute).Before(p.expiry) {
		token := p.token
		p.mu.RUnlock()
		return token, nil
	}
	p.mu.RUnlock()

	res, err, _ := p.sfGroup.Do("fetchToken", func() (any, error) {
		p.mu.RLock()
		if p.token != "" && time.Now().Add(1*time.Minute).Before(p.expiry) {
			token := p.token
			p.mu.RUnlock()
			return token, nil
		}
		p.mu.RUnlock()

		token, ttl, err := p.fetchToken(ctx)
		if err != nil {
			return "", err
		}

		p.mu.Lock()
		p.token = token
		p.expiry = time.Now().Add(ttl)
		p.mu.Unlock()

		return token, nil
	})
	if err != nil {
		return "", err
	}
	token, ok := res.(string)
	if !ok {
		return "", fmt.Errorf("unexpected token type from singleflight: %T", res)
	}
	return token, nil
}

func (p *OidcTokenProvider) buildTokenRequest(ctx context.Context) (*http.Request, error) {
	data := url.Values{
		"scope": {p.cfg.Scope},
	}
	if p.cfg.Password != "" && p.cfg.Username != "" {
		data.Set("grant_type", "password")
		data.Set("username", p.cfg.Username)
		data.Set("password", p.cfg.Password)
	} else {
		data.Set("grant_type", "client_credentials")
	}

	useBasicAuth := p.cfg.ClientID != "" && p.cfg.ClientSecret != ""
	if !useBasicAuth && p.cfg.ClientID != "" {
		data.Set("client_id", p.cfg.ClientID)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, p.cfg.TokenURL, strings.NewReader(data.Encode()))
	if err != nil {
		return nil, fmt.Errorf("failed to create token request: %w", err)
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	if useBasicAuth {
		req.SetBasicAuth(p.cfg.ClientID, p.cfg.ClientSecret)
	}
	return req, nil
}

func (p *OidcTokenProvider) parseTokenResponse(bodyReader io.Reader) (string, time.Duration, error) {
	bodyBytes, err := io.ReadAll(io.LimitReader(bodyReader, 1<<20))
	if err != nil {
		return "", 0, fmt.Errorf("failed to read token response: %w", err)
	}

	var tokenResp struct {
		IDToken     string `json:"id_token"`
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
	}
	if err := json.Unmarshal(bodyBytes, &tokenResp); err != nil {
		return "", 0, fmt.Errorf("failed to parse token response: %w", err)
	}

	token := tokenResp.IDToken
	if token == "" {
		token = tokenResp.AccessToken
	}
	if token == "" {
		return "", 0, fmt.Errorf("token response did not contain id_token or access_token")
	}

	ttl := time.Duration(tokenResp.ExpiresIn) * time.Second
	if ttl <= 0 {
		ttl = 1 * time.Hour
	}

	return token, ttl, nil
}

func (p *OidcTokenProvider) fetchToken(ctx context.Context) (string, time.Duration, error) {
	timeout := p.httpClient.Timeout
	if timeout <= 0 {
		timeout = 15 * time.Second
	}
	fetchCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), timeout)
	defer cancel()

	req, err := p.buildTokenRequest(fetchCtx)
	if err != nil {
		return "", 0, err
	}

	resp, err := p.httpClient.Do(req)
	if err != nil {
		return "", 0, fmt.Errorf("failed to execute token request to %s: %w", p.cfg.TokenURL, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		bodyBytes, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		return "", 0, fmt.Errorf("token request failed with status %d: %s", resp.StatusCode, string(bodyBytes))
	}

	return p.parseTokenResponse(resp.Body)
}

// Option configures a Client instance.
type Option func(*Client)

// WithTokenProvider configures a custom TokenProvider on the Client.
func WithTokenProvider(tp TokenProvider) Option {
	return func(c *Client) {
		c.tokenProvider = tp
	}
}

// WithRedisPublisher configures a custom Redis publisher on the Client.
func WithRedisPublisher(publisher redisclient.Publisher) Option {
	return func(c *Client) {
		c.redisClient = publisher
	}
}

type Client struct {
	invokerSA        string
	gcpProjectID     string
	actuatorURL      string
	actuatorAudience string
	signer           func() (jwt.Signer, error)
	signatureTTL     time.Duration
	redisClient      redisclient.Publisher
	tokenProvider    TokenProvider
}

// New creates a new Client instance.
func New(ctx context.Context, projectID, invokerSA, actuatorURL, actuatorAudience string, signer func() (jwt.Signer, error), signatureTTL int, redisAddr string, opts ...Option) (*Client, error) {
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

	c := &Client{
		invokerSA:        invokerSA,
		gcpProjectID:     projectID,
		actuatorURL:      actuatorURL,
		actuatorAudience: aud,
		signer:           signer,
		signatureTTL:     time.Duration(ttl) * time.Second,
		redisClient:      redisClient,
	}

	for _, opt := range opts {
		opt(c)
	}

	if c.tokenProvider == nil {
		c.tokenProvider = &GcpIAMTokenProvider{}
	}

	return c, nil
}

// EnqueueWorkUnitTask creates and enqueues a Cloud Task to actuate a WorkUnit.
func (c *Client) EnqueueWorkUnitTask(ctx context.Context, target string, workUnit *essmsgpb.WorkUnit, action *actuatortaskpb.Action, operationName string) error {
	return c.enqueueTask(ctx, target, workUnit, action, operationName, "", time.Time{})
}

// EnqueueResumedWorkUnitTask creates and enqueues a replayed actuation task carrying resumption metadata.
func (c *Client) EnqueueResumedWorkUnitTask(ctx context.Context, target string, workUnit *essmsgpb.WorkUnit, action *actuatortaskpb.Action, operationName string, resumptionRunID string, resumptionTime time.Time) error {
	return c.enqueueTask(ctx, target, workUnit, action, operationName, resumptionRunID, resumptionTime)
}

func (c *Client) enqueueTask(ctx context.Context, target string, workUnit *essmsgpb.WorkUnit, action *actuatortaskpb.Action, operationName string, resumptionRunID string, resumptionTime time.Time) error {
	now := timestamppb.Now()

	traceID, ok := logging.OperationID(ctx)
	if !ok {
		traceID = uuid.NewString()
	}

	metadata := &actuatortaskpb.Metadata{
		IdempotencyKey: proto.String(generateIdempotencyKey(target, action, now.AsTime())),
		OperationName:  proto.String(operationName),
		TraceId:        proto.String(traceID),
	}
	if resumptionRunID != "" {
		metadata.ResumptionRunId = proto.String(resumptionRunID)
	}
	if !resumptionTime.IsZero() {
		metadata.ResumptionTime = timestamppb.New(resumptionTime)
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

	oidcTokenString, err := c.tokenProvider.GenerateToken(ctx, c.invokerSA, c.actuatorAudience)
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
	if err := c.redisClient.AddMessage(ctx, string(redisMsg), "0"); err != nil {
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

// CalculateBacklogLength returns the consumer group's backlog on the task stream.
func (c *Client) CalculateBacklogLength(ctx context.Context, group string, maxThreshold int) (int64, error) {
	if c == nil || c.redisClient == nil {
		logging.Warn(ctx, "Task queue Redis client not configured; reporting empty backlog")
		return 0, nil
	}
	return c.redisClient.BacklogLength(ctx, group, maxThreshold)
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
