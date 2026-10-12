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
package workunittaskhandler

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	essmsgpb "github.com/google/xmc/api/protos/experiment"
	actuatortaskpb "github.com/google/xmc/api/protos/task_queue"
	"github.com/google/xmc/cmd/actuator/pausescheduling"
	"github.com/google/xmc/internal/security/securitytest"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/tink-crypto/tink-go/v2/jwt"
	"google.golang.org/api/googleapi"
	"google.golang.org/api/idtoken"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/validation/field"
	"k8s.io/utils/ptr"
)

const testAudience = "test-audience"
const testServiceAccount = "test@serviceaccount.com"

type jwtOptions struct {
	Audience          *string
	Issuer            *string
	Subject           *string
	IssuedAt          *time.Time
	ExpiresAt         *time.Time
	MessageDigest     *string
	WithoutMessage    bool
	WithoutExpiration bool
}

func issueTestJWT(t *testing.T, opts *jwtOptions) string {
	t.Helper()
	signer, err := securitytest.MustCreateTestSigner(t)()
	if err != nil {
		t.Fatalf("Failed to create JWT signer: %v", err)
	}

	customClaims := map[string]any{}
	if !opts.WithoutMessage && opts.MessageDigest != nil {
		customClaims["message_digest"] = *opts.MessageDigest
	}

	rawJWT, err := jwt.NewRawJWT(&jwt.RawJWTOptions{
		Audience:          opts.Audience,
		Issuer:            opts.Issuer,
		Subject:           opts.Subject,
		IssuedAt:          opts.IssuedAt,
		ExpiresAt:         opts.ExpiresAt,
		CustomClaims:      customClaims,
		WithoutExpiration: opts.WithoutExpiration,
	})
	if err != nil {
		t.Fatalf("Failed to create raw JWT: %v", err)
	}

	token, err := signer.SignAndEncode(rawJWT)
	if err != nil {
		t.Fatalf("Failed to sign and encode JWT: %v", err)
	}
	return token
}

func validJWTOptions(body []byte, audience, serviceAccount string) *jwtOptions {
	now := time.Now()
	exp := time.Now().Add(4 * time.Minute)
	digest := calculateSHA256(body)
	return &jwtOptions{
		Audience:      &audience,
		Issuer:        &serviceAccount,
		Subject:       &serviceAccount,
		IssuedAt:      &now,
		ExpiresAt:     &exp,
		MessageDigest: &digest,
	}
}

type mockK8sClient struct {
	err          error
	createCalls  int
	deleteCalls  int
	restartCalls int
	updateCalls  int
	lastPayload  *actuatortaskpb.ActuatorPayload
}

func (m *mockK8sClient) CreateWorkUnit(ctx context.Context, payload *actuatortaskpb.ActuatorPayload) error {
	m.createCalls++
	m.lastPayload = payload
	return m.err
}
func (m *mockK8sClient) DeleteWorkUnit(ctx context.Context, payload *actuatortaskpb.ActuatorPayload) error {
	m.deleteCalls++
	m.lastPayload = payload
	return m.err
}
func (m *mockK8sClient) RestartWorkUnit(ctx context.Context, payload *actuatortaskpb.ActuatorPayload) error {
	m.restartCalls++
	m.lastPayload = payload
	return m.err
}
func (m *mockK8sClient) UpdateWorkUnit(ctx context.Context, payload *actuatortaskpb.ActuatorPayload) error {
	m.updateCalls++
	m.lastPayload = payload
	return m.err
}

type mockPauseHandler struct {
	isPaused    bool
	shouldPause bool
	err         error
	handleCalls int
	lastPayload *actuatortaskpb.ActuatorPayload
}

func (m *mockPauseHandler) IsPaused(metadata *actuatortaskpb.Metadata) bool {
	if metadata.GetResumptionTime() != nil {
		m.isPaused = false
	}
	return m.isPaused
}

func (m *mockPauseHandler) ShouldPause(action *actuatortaskpb.Action) bool {
	return m.shouldPause
}

func (m *mockPauseHandler) HandlePause(ctx context.Context, payload *actuatortaskpb.ActuatorPayload, traceID string) error {
	m.handleCalls++
	m.lastPayload = payload
	return m.err
}

type testHandlerOption func(*Params)

func withK8sClient(client K8sClient) testHandlerOption {
	return func(p *Params) {
		p.K8sClient = client
	}
}

func withServiceAccount(sa string) testHandlerOption {
	return func(p *Params) {
		p.XManagerServiceAccount = sa
	}
}

func withJWTVerifier(v func() (jwt.Verifier, error)) testHandlerOption {
	return func(p *Params) {
		p.JWTVerifier = v
	}
}

func withTokenValidator(v TokenValidator) testHandlerOption {
	return func(p *Params) {
		p.TokenValidator = v
	}
}

func withPauseHandler(ph pausescheduling.PauseHandler) testHandlerOption {
	return func(p *Params) {
		p.PauseHandler = ph
	}
}

func newTestTaskHandler(t *testing.T, opts ...testHandlerOption) *Handler {
	t.Helper()
	params := Params{
		K8sClient:              &mockK8sClient{},
		ProjectID:              "test-project",
		Audience:               testAudience,
		XManagerServiceAccount: testServiceAccount,
		JWTVerifier:            securitytest.MustCreateTestVerifier(t),
		TokenValidator: &GoogleIDTokenValidator{
			ExpectedServiceAccount: testServiceAccount,
			ValidateFunc: func(_ context.Context, _, aud string) (*idtoken.Payload, error) {
				if aud != testAudience {
					return nil, fmt.Errorf("unexpected audience: got %q, want %q", aud, testAudience)
				}
				return &idtoken.Payload{
					Claims: map[string]any{
						"email": testServiceAccount,
					},
				}, nil
			},
		},
		PauseHandler: &mockPauseHandler{},
	}
	for _, opt := range opts {
		opt(&params)
	}
	h, err := New(params)
	if err != nil {
		t.Fatalf("failed to create test task handler: %v", err)
	}
	return h
}

func TestNew_Validation(t *testing.T) {
	validParams := Params{
		K8sClient:              &mockK8sClient{},
		ProjectID:              "test-project",
		Audience:               testAudience,
		XManagerServiceAccount: testServiceAccount,
		JWTVerifier:            securitytest.MustCreateTestVerifier(t),
	}

	testCases := []struct {
		name       string
		modify     func(p *Params)
		wantErrMsg string
	}{
		{
			name: "missing_k8s_client",
			modify: func(p *Params) {
				p.K8sClient = nil
			},
			wantErrMsg: "k8s client is required",
		},
		{
			name: "missing_project_id",
			modify: func(p *Params) {
				p.ProjectID = ""
			},
			wantErrMsg: "project ID is required",
		},
		{
			name: "missing_audience",
			modify: func(p *Params) {
				p.Audience = ""
			},
			wantErrMsg: "audience is required",
		},
		{
			name: "missing_service_account",
			modify: func(p *Params) {
				p.XManagerServiceAccount = ""
			},
			wantErrMsg: "xmanager service account is required",
		},
		{
			name: "missing_jwt_verifier",
			modify: func(p *Params) {
				p.JWTVerifier = nil
			},
			wantErrMsg: "jwt verifier is required",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			p := validParams
			tc.modify(&p)
			handler, err := New(p)
			if err == nil {
				t.Fatalf("New() expected error containing %q, got nil error (handler: %v)", tc.wantErrMsg, handler)
			}
			if !strings.Contains(err.Error(), tc.wantErrMsg) {
				t.Errorf("New() error mismatch: got %q, want error containing %q", err.Error(), tc.wantErrMsg)
			}
		})
	}
}

func TestHandleWorkUnitRedisTask_K8sClientError(t *testing.T) {
	workUnitsCreated.Reset()
	ctMessagesFailed.Reset()
	ctMessagesProcessed.Reset()
	handler := newTestTaskHandler(t, withK8sClient(&mockK8sClient{err: apierrors.NewInternalError(errors.New("internal error"))}))

	payload := []byte(`{
    "kind": "WORK_UNIT_ACTUATION_REQUEST",
    "target": "experiments/12345/workUnits/678/executions/1",
    "action": {
      "create": {}
    },
    "workUnit": {
      "name": "work-unit-1",
      "urls": [
        {"url": "http://someurl.com/"}
      ],
      "executable": {
        "schedulingConstraints": {
          "queueName": "test-queue"
        },
        "jobset":{
          "jobs":[
            {
              "name":"job1",
              "spec":{
                "resources":{
                  "cpuResources":{
                    "cpuMillicores":100,
                    "memoryBytes":1024
                  }
                },
                "dockerImage":"foo/bar"
              },
              "replicas":1
            }
          ]
        }
      }
    },
    "metadata": {},
    "createTime": "2025-10-29T10:00:00Z"
  }`)

	signature := issueTestJWT(t, validJWTOptions(payload, testAudience, testServiceAccount))
	b64Payload := base64.StdEncoding.EncodeToString(payload)
	redisMsg := fmt.Sprintf(`{
		"payload": %q,
		"signature": %q,
		"oidcToken": "valid-token"
	}`, b64Payload, signature)

	if val := testutil.ToFloat64(ctMessagesFailed.WithLabelValues()); val != 0 {
		t.Errorf("ctMessagesFailed counter should be 0 before test, got %v", val)
	}
	if val := testutil.ToFloat64(ctMessagesProcessed.WithLabelValues()); val != 0 {
		t.Errorf("ctMessagesProcessed counter should be 0 before test, got %v", val)
	}

	result := handler.HandleWorkUnitRedisTask(context.Background(), "test-worker", redisMsg)

	if result.WasRunSuccessful {
		t.Errorf("HandleWorkUnitRedisTask returned success=true, want false")
	}

	if val := testutil.ToFloat64(ctMessagesFailed.WithLabelValues()); val != 1 {
		t.Errorf("ctMessagesFailed counter mismatch: got %v, want 1", val)
	}
	if val := testutil.ToFloat64(ctMessagesProcessed.WithLabelValues()); val != 1 {
		t.Errorf("ctMessagesProcessed counter mismatch: got %v, want 1", val)
	}
}

func TestHandleWorkUnitRedisTask_MalformedJSON(t *testing.T) {
	ctMessagesFailed.Reset()
	ctMessagesProcessed.Reset()
	handler := newTestTaskHandler(t)

	payload := []byte(`this is not json`)
	signature := issueTestJWT(t, validJWTOptions(payload, testAudience, testServiceAccount))
	b64Payload := base64.StdEncoding.EncodeToString(payload)
	redisMsg := fmt.Sprintf(`{
		"payload": %q,
		"signature": %q,
		"oidcToken": "valid-token"
	}`, b64Payload, signature)

	initialMetricCount := testutil.ToFloat64(ctMessagesFailed.WithLabelValues())
	if initialMetricCount != 0 {
		t.Errorf("ctMessagesFailed counter should be 0 before test, got %v", initialMetricCount)
	}
	initialProcessedMetricCount := testutil.ToFloat64(ctMessagesProcessed.WithLabelValues())
	if initialProcessedMetricCount != 0 {
		t.Errorf("ctMessagesProcessed counter mismatch: got %v, want 0", initialProcessedMetricCount)
	}

	result := handler.HandleWorkUnitRedisTask(context.Background(), "test-worker", redisMsg)

	if result.WasRunSuccessful {
		t.Errorf("HandleWorkUnitRedisTask returned success=true, want false")
	}

	finalMetricCount := testutil.ToFloat64(ctMessagesFailed.WithLabelValues())
	if finalMetricCount != 1 {
		t.Errorf("ctMessagesFailed counter mismatch: got %v, want 1", finalMetricCount)
	}
	finalProcessedMetricCount := testutil.ToFloat64(ctMessagesProcessed.WithLabelValues())
	if finalProcessedMetricCount != 0 {
		t.Errorf("ctMessagesProcessed counter mismatch: got %v, want 0", finalProcessedMetricCount)
	}
}

func TestHandleWorkUnitRedisTask_Auth(t *testing.T) {
	testCases := []struct {
		name                   string
		oidcToken              string
		tokenValidator         TokenValidator
		xmanagerServiceAccount string
		wantErrMsg             string
		wantShouldBeRetried    bool
	}{
		{
			name:                   "no_oidc_token",
			oidcToken:              "",
			tokenValidator:         nil,
			xmanagerServiceAccount: testServiceAccount,
			wantErrMsg:             "auth token is empty",
			wantShouldBeRetried:    false,
		},
		{
			name:      "token_validation_error",
			oidcToken: "fake-token",
			tokenValidator: &GoogleIDTokenValidator{
				ExpectedServiceAccount: testServiceAccount,
				ValidateFunc: func(_ context.Context, _, _ string) (*idtoken.Payload, error) {
					return nil, errors.New("failed to validate ID token")
				},
			},
			xmanagerServiceAccount: testServiceAccount,
			wantErrMsg:             "failed to validate ID token",
			wantShouldBeRetried:    false,
		},
		{
			name:      "service_account_mismatch",
			oidcToken: "fake-token",
			tokenValidator: &GoogleIDTokenValidator{
				ExpectedServiceAccount: testServiceAccount,
				ValidateFunc: func(_ context.Context, _, _ string) (*idtoken.Payload, error) {
					return &idtoken.Payload{
						Claims: map[string]any{"email": "different@serviceaccount.com"},
					}, nil
				},
			},
			xmanagerServiceAccount: testServiceAccount,
			wantErrMsg:             "service account mismatch",
			wantShouldBeRetried:    false,
		},
		{
			name:      "token_validation_deadline_exceeded",
			oidcToken: "fake-token",
			tokenValidator: &mockTokenValidator{
				err: context.DeadlineExceeded,
			},
			xmanagerServiceAccount: testServiceAccount,
			wantErrMsg:             "context deadline exceeded",
			wantShouldBeRetried:    true,
		},
		{
			name:      "token_validation_transient_googleapi_503",
			oidcToken: "fake-token",
			tokenValidator: &mockTokenValidator{
				err: &googleapi.Error{Code: http.StatusServiceUnavailable, Message: "service unavailable"},
			},
			xmanagerServiceAccount: testServiceAccount,
			wantErrMsg:             "service unavailable",
			wantShouldBeRetried:    true,
		},
		{
			name:      "token_validation_transient_googleapi_429",
			oidcToken: "fake-token",
			tokenValidator: &mockTokenValidator{
				err: &googleapi.Error{Code: http.StatusTooManyRequests, Message: "rate limit exceeded"},
			},
			xmanagerServiceAccount: testServiceAccount,
			wantErrMsg:             "rate limit exceeded",
			wantShouldBeRetried:    true,
		},
		{
			name:      "token_validation_non_transient_googleapi_401",
			oidcToken: "fake-token",
			tokenValidator: &mockTokenValidator{
				err: &googleapi.Error{Code: http.StatusUnauthorized, Message: "unauthorized"},
			},
			xmanagerServiceAccount: testServiceAccount,
			wantErrMsg:             "unauthorized",
			wantShouldBeRetried:    false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			var opts []testHandlerOption
			opts = append(opts, withServiceAccount(tc.xmanagerServiceAccount))
			if tc.tokenValidator != nil {
				opts = append(opts, withTokenValidator(tc.tokenValidator))
			}
			handler := newTestTaskHandler(t, opts...)

			payload := []byte(`{
				"kind": "WORK_UNIT_ACTUATION_REQUEST",
				"target": "experiments/12345/workUnits/678/executions/1",
				"action": {"create": {}},
				"metadata": {},
				"createTime": "2025-10-29T10:00:00Z"
			}`)
			signatureServiceAccount := tc.xmanagerServiceAccount
			if signatureServiceAccount == "" {
				signatureServiceAccount = testServiceAccount
			}
			signature := issueTestJWT(t, validJWTOptions(payload, testAudience, signatureServiceAccount))
			b64Payload := base64.StdEncoding.EncodeToString(payload)
			redisMsg := fmt.Sprintf(`{
				"payload": %q,
				"signature": %q,
				"oidcToken": %q
			}`, b64Payload, signature, tc.oidcToken)

			result := handler.HandleWorkUnitRedisTask(context.Background(), "test-worker", redisMsg)

			if result.WasRunSuccessful {
				t.Errorf("HandleWorkUnitRedisTask returned success=true, want false")
			}
			if result.ShouldBeRetried != tc.wantShouldBeRetried {
				t.Errorf("HandleWorkUnitRedisTask ShouldBeRetried = %v, want %v", result.ShouldBeRetried, tc.wantShouldBeRetried)
			}
			if result.Error == nil {
				t.Fatal("HandleWorkUnitRedisTask returned nil error, want non-nil")
			}
			if !strings.Contains(result.Error.Error(), tc.wantErrMsg) {
				t.Errorf("HandleWorkUnitRedisTask error mismatch: got %q, want it to contain %q", result.Error.Error(), tc.wantErrMsg)
			}
		})
	}
}

type mockTokenValidator struct {
	err error
}

func (m *mockTokenValidator) Validate(ctx context.Context, token string, audience string) error {
	return m.err
}

func TestGoogleIDTokenValidator(t *testing.T) {
	t.Run("empty token returns error", func(t *testing.T) {
		v := &GoogleIDTokenValidator{ExpectedServiceAccount: testServiceAccount}
		err := v.Validate(context.Background(), "", testAudience)
		if err == nil || !strings.Contains(err.Error(), "auth token is empty") {
			t.Errorf("Validate(\"\") error = %v, want substring 'auth token is empty'", err)
		}
	})

	t.Run("empty expected service account returns error", func(t *testing.T) {
		v := &GoogleIDTokenValidator{ExpectedServiceAccount: ""}
		err := v.Validate(context.Background(), "some-token", testAudience)
		if err == nil || !strings.Contains(err.Error(), "expected service account is not configured") {
			t.Errorf("Validate() error = %v, want substring 'expected service account is not configured'", err)
		}
	})

	t.Run("validate error returned", func(t *testing.T) {
		v := &GoogleIDTokenValidator{
			ExpectedServiceAccount: testServiceAccount,
			ValidateFunc: func(context.Context, string, string) (*idtoken.Payload, error) {
				return nil, errors.New("underlying validation error")
			},
		}
		err := v.Validate(context.Background(), "tok", testAudience)
		if err == nil || !strings.Contains(err.Error(), "failed to validate ID token") {
			t.Errorf("Validate() error = %v, want substring 'failed to validate ID token'", err)
		}
	})

	t.Run("missing email claim", func(t *testing.T) {
		v := &GoogleIDTokenValidator{
			ExpectedServiceAccount: testServiceAccount,
			ValidateFunc: func(context.Context, string, string) (*idtoken.Payload, error) {
				return &idtoken.Payload{Claims: map[string]any{}}, nil
			},
		}
		err := v.Validate(context.Background(), "tok", testAudience)
		if err == nil || !strings.Contains(err.Error(), "email claim not found") {
			t.Errorf("Validate() error = %v, want substring 'email claim not found'", err)
		}
	})

	t.Run("service account mismatch", func(t *testing.T) {
		v := &GoogleIDTokenValidator{
			ExpectedServiceAccount: testServiceAccount,
			ValidateFunc: func(context.Context, string, string) (*idtoken.Payload, error) {
				return &idtoken.Payload{Claims: map[string]any{"email": "other@sa.com"}}, nil
			},
		}
		err := v.Validate(context.Background(), "tok", testAudience)
		if err == nil || !strings.Contains(err.Error(), "service account mismatch") {
			t.Errorf("Validate() error = %v, want substring 'service account mismatch'", err)
		}
	})

	t.Run("success", func(t *testing.T) {
		v := &GoogleIDTokenValidator{
			ExpectedServiceAccount: testServiceAccount,
			ValidateFunc: func(context.Context, string, string) (*idtoken.Payload, error) {
				return &idtoken.Payload{Claims: map[string]any{"email": testServiceAccount}}, nil
			},
		}
		if err := v.Validate(context.Background(), "tok", testAudience); err != nil {
			t.Errorf("Validate() error = %v, want nil", err)
		}
	})
}

func TestHandleWorkUnitRedisTask_CustomTokenValidatorSuccess(t *testing.T) {
	workUnitsCreated.Reset()
	ctMessagesProcessed.Reset()
	ctMessagesFailed.Reset()
	handler := newTestTaskHandler(t, withTokenValidator(&mockTokenValidator{}))

	payload := []byte(`{
    "kind": "WORK_UNIT_ACTUATION_REQUEST",
    "target": "experiments/12345/workUnits/678/executions/1",
    "action": {
      "create": {}
    },
    "workUnit": {
      "name": "work-unit-1",
      "executable": {
        "schedulingConstraints": {
          "queueName": "test-queue"
        }
      }
    },
    "metadata": {},
    "createTime": "2025-10-29T10:00:00Z"
  }`)

	signature := issueTestJWT(t, validJWTOptions(payload, testAudience, testServiceAccount))
	b64Payload := base64.StdEncoding.EncodeToString(payload)
	redisMsg := fmt.Sprintf(`{
		"payload": %q,
		"signature": %q,
		"oidcToken": "valid-mock-token"
	}`, b64Payload, signature)

	result := handler.HandleWorkUnitRedisTask(context.Background(), "test-worker", redisMsg)
	if result.Error != nil {
		t.Fatalf("HandleWorkUnitRedisTask returned error with mockTokenValidator: %v", result.Error)
	}
	if !result.WasRunSuccessful {
		t.Errorf("HandleWorkUnitRedisTask returned success=false, want true")
	}
}

func TestNew_DefaultTokenValidator(t *testing.T) {
	t.Run("defaults to GoogleIDTokenValidator", func(t *testing.T) {
		h, err := New(Params{
			K8sClient:              &mockK8sClient{},
			ProjectID:              "test-project",
			Audience:               testAudience,
			XManagerServiceAccount: testServiceAccount,
			JWTVerifier:            securitytest.MustCreateTestVerifier(t),
		})
		if err != nil {
			t.Fatalf("New() failed: %v", err)
		}
		if _, ok := h.TokenValidator().(*GoogleIDTokenValidator); !ok {
			t.Errorf("tokenValidator is %T, want *GoogleIDTokenValidator", h.TokenValidator())
		}
	})

	t.Run("explicit token validator", func(t *testing.T) {
		customVal := &mockTokenValidator{}
		h, err := New(Params{
			K8sClient:              &mockK8sClient{},
			ProjectID:              "test-project",
			Audience:               testAudience,
			XManagerServiceAccount: testServiceAccount,
			JWTVerifier:            securitytest.MustCreateTestVerifier(t),
			TokenValidator:         customVal,
		})
		if err != nil {
			t.Fatalf("New() failed: %v", err)
		}
		if h.TokenValidator() != customVal {
			t.Errorf("tokenValidator got %v, want %v", h.TokenValidator(), customVal)
		}
	})
}

type actuatorPayloadTestCase struct {
	name        string
	action      *actuatortaskpb.Action
	k8sErr      error
	expectedErr bool
}

func TestHandleActuatorPayload(t *testing.T) {
	gr := schema.GroupResource{Group: "test", Resource: "test"}
	testCases := []actuatorPayloadTestCase{
		{
			name:        "create_no_error",
			action:      &actuatortaskpb.Action{Action: &actuatortaskpb.Action_Create{Create: &actuatortaskpb.CreateAction{}}},
			k8sErr:      nil,
			expectedErr: false,
		},
		{
			name:        "create_not_found",
			action:      &actuatortaskpb.Action{Action: &actuatortaskpb.Action_Create{Create: &actuatortaskpb.CreateAction{}}},
			k8sErr:      apierrors.NewNotFound(gr, "foo"),
			expectedErr: true,
		},
		{
			name:        "delete_already_exists",
			action:      &actuatortaskpb.Action{Action: &actuatortaskpb.Action_Delete{Delete: &actuatortaskpb.DeleteAction{}}},
			k8sErr:      apierrors.NewAlreadyExists(gr, "foo"),
			expectedErr: true,
		},
		{
			name:        "restart_invalid",
			action:      &actuatortaskpb.Action{Action: &actuatortaskpb.Action_Restart{Restart: &actuatortaskpb.RestartAction{}}},
			k8sErr:      apierrors.NewInvalid(schema.GroupKind{}, "foo", field.ErrorList{}),
			expectedErr: true,
		},
		{
			name:        "create_forbidden",
			action:      &actuatortaskpb.Action{Action: &actuatortaskpb.Action_Create{Create: &actuatortaskpb.CreateAction{}}},
			k8sErr:      apierrors.NewForbidden(gr, "foo", fmt.Errorf("forbidden")),
			expectedErr: true,
		},
		{
			name:        "delete_unauthorized",
			action:      &actuatortaskpb.Action{Action: &actuatortaskpb.Action_Delete{Delete: &actuatortaskpb.DeleteAction{}}},
			k8sErr:      apierrors.NewUnauthorized("unauthorized"),
			expectedErr: true,
		},
		{
			name:        "create_bad_request",
			action:      &actuatortaskpb.Action{Action: &actuatortaskpb.Action_Create{Create: &actuatortaskpb.CreateAction{}}},
			k8sErr:      apierrors.NewBadRequest("bad request"),
			expectedErr: true,
		},
		{
			name:        "restart_gone",
			action:      &actuatortaskpb.Action{Action: &actuatortaskpb.Action_Restart{Restart: &actuatortaskpb.RestartAction{}}},
			k8sErr:      apierrors.NewGone("gone"),
			expectedErr: true,
		},
		{
			name:        "create_request_entity_too_large",
			action:      &actuatortaskpb.Action{Action: &actuatortaskpb.Action_Create{Create: &actuatortaskpb.CreateAction{}}},
			k8sErr:      apierrors.NewRequestEntityTooLargeError("request entity too large"),
			expectedErr: true,
		},
		{
			name:        "restart_timeout",
			action:      &actuatortaskpb.Action{Action: &actuatortaskpb.Action_Restart{Restart: &actuatortaskpb.RestartAction{}}},
			k8sErr:      apierrors.NewTimeoutError("timeout", 0),
			expectedErr: true,
		},
		{
			name:        "create_too_many_requests",
			action:      &actuatortaskpb.Action{Action: &actuatortaskpb.Action_Create{Create: &actuatortaskpb.CreateAction{}}},
			k8sErr:      apierrors.NewTooManyRequests("too many requests", 0),
			expectedErr: true,
		},
		{
			name:        "delete_service_unavailable",
			action:      &actuatortaskpb.Action{Action: &actuatortaskpb.Action_Delete{Delete: &actuatortaskpb.DeleteAction{}}},
			k8sErr:      apierrors.NewServiceUnavailable("service unavailable"),
			expectedErr: true,
		},
		{
			name:        "restart_internal_error",
			action:      &actuatortaskpb.Action{Action: &actuatortaskpb.Action_Restart{Restart: &actuatortaskpb.RestartAction{}}},
			k8sErr:      apierrors.NewInternalError(fmt.Errorf("internal error")),
			expectedErr: true,
		},
		{
			name:        "create_conflict",
			action:      &actuatortaskpb.Action{Action: &actuatortaskpb.Action_Create{Create: &actuatortaskpb.CreateAction{}}},
			k8sErr:      apierrors.NewConflict(gr, "foo", fmt.Errorf("conflict")),
			expectedErr: true,
		},
		{
			name:        "delete_unknown_error",
			action:      &actuatortaskpb.Action{Action: &actuatortaskpb.Action_Delete{Delete: &actuatortaskpb.DeleteAction{}}},
			k8sErr:      fmt.Errorf("unknown error"),
			expectedErr: true,
		},
		{
			name:        "update_no_error",
			action:      &actuatortaskpb.Action{Action: &actuatortaskpb.Action_Update{Update: &actuatortaskpb.UpdateAction{}}},
			k8sErr:      nil,
			expectedErr: false,
		},
		{
			name:        "update_conflict",
			action:      &actuatortaskpb.Action{Action: &actuatortaskpb.Action_Update{Update: &actuatortaskpb.UpdateAction{}}},
			k8sErr:      apierrors.NewConflict(gr, "foo", fmt.Errorf("conflict")),
			expectedErr: true,
		},
		{
			name:   "unknown_error_with_causes",
			action: &actuatortaskpb.Action{Action: &actuatortaskpb.Action_Delete{Delete: &actuatortaskpb.DeleteAction{}}},
			k8sErr: &apierrors.StatusError{
				ErrStatus: metav1.Status{
					Status: metav1.StatusFailure,
					Code:   http.StatusTeapot,
					Reason: metav1.StatusReasonUnknown,
					Details: &metav1.StatusDetails{
						Causes: []metav1.StatusCause{
							{
								Type:    "FieldValueInvalid",
								Message: "some cause",
								Field:   "some.field",
							},
						},
					},
				},
			},
			expectedErr: true,
		},
		{
			name:        "unsupported_action",
			action:      &actuatortaskpb.Action{},
			k8sErr:      nil,
			expectedErr: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			ctMessagesProcessed.Reset()
			h := newTestTaskHandler(t, withK8sClient(&mockK8sClient{err: tc.k8sErr}))

			payload := &actuatortaskpb.ActuatorPayload{
				Target: ptr.To("workunits/123"),
				Action: tc.action,
			}

			err := h.handleActuatorPayload(context.Background(), payload)
			if (err != nil) != tc.expectedErr {
				t.Errorf("handleActuatorPayload() returned error %v, want error %v", err, tc.expectedErr)
			}

			if val := testutil.ToFloat64(ctMessagesProcessed.WithLabelValues()); val != 1 {
				t.Errorf("ctMessagesProcessed counter mismatch: got %v, want 1", val)
			}
		})
	}
}

func TestHandleWorkUnitRedisTask_Success(t *testing.T) {
	workUnitsCreated.Reset()
	ctMessagesProcessed.Reset()
	ctMessagesFailed.Reset()
	handler := newTestTaskHandler(t)

	payload := []byte(`{
    "kind": "WORK_UNIT_ACTUATION_REQUEST",
    "target": "experiments/12345/workUnits/678/executions/1",
    "action": {
      "create": {}
    },
    "workUnit": {
      "name": "work-unit-1",
      "executable": {
        "schedulingConstraints": {
          "queueName": "test-queue"
        },
        "jobset":{
          "jobs":[
            {
              "name":"job1",
              "spec":{
                "resources":{
                  "cpuResources":{
                    "cpuMillicores":100,
                    "memoryBytes":1024
                  }
                },
                "dockerImage":"foo/bar"
              },
              "replicas":1
            }
          ]
        }
      }
    },
    "metadata": {},
    "createTime": "2025-10-29T10:00:00Z"
  }`)

	signature := issueTestJWT(t, validJWTOptions(payload, testAudience, testServiceAccount))
	b64Payload := base64.StdEncoding.EncodeToString(payload)
	redisMsg := fmt.Sprintf(`{
		"payload": %q,
		"signature": %q,
		"oidcToken": "valid-token"
	}`, b64Payload, signature)

	result := handler.HandleWorkUnitRedisTask(context.Background(), "test-worker", redisMsg)

	if result.Error != nil {
		t.Fatalf("HandleWorkUnitRedisTask returned error: %v, want nil", result.Error)
	}
	if !result.WasRunSuccessful {
		t.Errorf("HandleWorkUnitRedisTask returned success=false, want true")
	}

	if val := testutil.ToFloat64(workUnitsCreated.WithLabelValues()); val != 1 {
		t.Errorf("workUnitsCreated counter mismatch: got %v, want 1", val)
	}
	if val := testutil.ToFloat64(ctMessagesProcessed.WithLabelValues()); val != 1 {
		t.Errorf("ctMessagesProcessed counter mismatch: got %v, want 1", val)
	}
	if val := testutil.ToFloat64(ctMessagesFailed.WithLabelValues()); val != 0 {
		t.Errorf("ctMessagesFailed counter mismatch: got %v, want 0", val)
	}
}

func TestHandleWorkUnitRedisTask_Failures(t *testing.T) {
	ctMessagesFailed.Reset()
	ctMessagesProcessed.Reset()

	validPayload := []byte(`{
    "kind": "WORK_UNIT_ACTUATION_REQUEST",
    "target": "experiments/12345/workUnits/678/executions/1",
    "action": { "create": {} },
    "metadata": {},
    "createTime": "2025-10-29T10:00:00Z"
  }`)

	malformedActuatorPayload := []byte(`{"foo": "bar"}`)
	signatureForValidPayload := issueTestJWT(t, validJWTOptions(validPayload, testAudience, testServiceAccount))
	signatureForMalformedPayload := issueTestJWT(t, validJWTOptions(malformedActuatorPayload, testAudience, testServiceAccount))
	b64ValidPayload := base64.StdEncoding.EncodeToString(validPayload)
	b64MalformedActuatorPayload := base64.StdEncoding.EncodeToString(malformedActuatorPayload)

	testCases := []struct {
		name           string
		redisMsg       string
		wantErrMsg     string
		tokenValidator TokenValidator
	}{
		{
			name:       "unmarshal_error",
			redisMsg:   "this is not json",
			wantErrMsg: "failed to unmarshal redis message",
		},
		{
			name:       "invalid_signature",
			redisMsg:   fmt.Sprintf(`{"payload": %q, "signature": "invalid-signature", "oidcToken": "valid-token"}`, b64ValidPayload),
			wantErrMsg: "failed to validate signature",
		},
		{
			name:       "unmarshal_payload_error",
			redisMsg:   fmt.Sprintf(`{"payload": %q, "signature": %q, "oidcToken": "valid-token"}`, b64MalformedActuatorPayload, signatureForMalformedPayload),
			wantErrMsg: "failed to unmarshal actuator payload",
		},
		{
			name:       "missing_signature",
			redisMsg:   fmt.Sprintf(`{"payload": %q, "oidcToken": "valid-token"}`, b64ValidPayload),
			wantErrMsg: "failed to validate signature",
		},
		{
			name:       "missing_oidc_token",
			redisMsg:   fmt.Sprintf(`{"payload": %q, "signature": %q}`, b64ValidPayload, signatureForValidPayload),
			wantErrMsg: "failed to validate OIDC token: auth token is empty",
			tokenValidator: &GoogleIDTokenValidator{
				ExpectedServiceAccount: testServiceAccount,
			},
		},
		{
			name:       "oidc_token_invalid",
			redisMsg:   fmt.Sprintf(`{"payload": %q, "signature": %q, "oidcToken": "invalid-token"}`, b64ValidPayload, signatureForValidPayload),
			wantErrMsg: "failed to validate OIDC token",
			tokenValidator: &GoogleIDTokenValidator{
				ExpectedServiceAccount: testServiceAccount,
				ValidateFunc: func(context.Context, string, string) (*idtoken.Payload, error) {
					return nil, errors.New("failed to validate ID token")
				},
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			var opts []testHandlerOption
			if tc.tokenValidator != nil {
				opts = append(opts, withTokenValidator(tc.tokenValidator))
			}
			handler := newTestTaskHandler(t, opts...)
			result := handler.HandleWorkUnitRedisTask(context.Background(), "test-worker", tc.redisMsg)
			if result.WasRunSuccessful {
				t.Errorf("HandleWorkUnitRedisTask returned success=true, want false")
			}
			if result.Error == nil {
				t.Errorf("HandleWorkUnitRedisTask returned err=nil, want non-nil error")
			}
			if !strings.Contains(result.Error.Error(), tc.wantErrMsg) {
				t.Errorf("HandleWorkUnitRedisTask returned err=%q, want error containing %q", result.Error, tc.wantErrMsg)
			}
		})
	}
}

func TestHandleWorkUnitRedisTask_SignatureValidation(t *testing.T) {
	ctMessagesFailed.Reset()
	ctMessagesProcessed.Reset()
	payload := []byte(`{
    "kind": "WORK_UNIT_ACTUATION_REQUEST",
    "target": "experiments/12345/workUnits/678/executions/1",
    "action": { "create": {} },
    "metadata": {},
    "createTime": "2025-10-29T10:00:00Z"
  }`)

	defaultOpts := validJWTOptions(payload, testAudience, testServiceAccount)

	failOptsAud := validJWTOptions(payload, testAudience, testServiceAccount)
	failOptsAud.Audience = ptr.To("another-audience")

	failOptsIss := validJWTOptions(payload, testAudience, testServiceAccount)
	failOptsIss.Issuer = ptr.To("another-issuer")

	failOptsSub := validJWTOptions(payload, testAudience, testServiceAccount)
	failOptsSub.Subject = ptr.To("another-subject")

	failOptsNoExp := validJWTOptions(payload, testAudience, testServiceAccount)
	failOptsNoExp.ExpiresAt = nil
	failOptsNoExp.WithoutExpiration = true

	failOptsExp := validJWTOptions(payload, testAudience, testServiceAccount)
	exp := time.Now().Add(-time.Hour)
	failOptsExp.ExpiresAt = &exp

	failOptsNoIat := validJWTOptions(payload, testAudience, testServiceAccount)
	failOptsNoIat.IssuedAt = nil

	failOptsDigest := validJWTOptions(payload, testAudience, testServiceAccount)
	failOptsDigest.MessageDigest = ptr.To("sha256:invalid")

	testCases := []struct {
		name        string
		signature   string
		jwtVerifier func() (jwt.Verifier, error)
	}{
		{
			name:      "no_signature_header",
			signature: "",
		},
		{
			name:        "verifier_error",
			signature:   issueTestJWT(t, defaultOpts),
			jwtVerifier: func() (jwt.Verifier, error) { return nil, errors.New("key error") },
		},
		{
			name:      "invalid_signature",
			signature: "invalid-signature",
		},
		{
			name:      "invalid_aud",
			signature: issueTestJWT(t, failOptsAud),
		},
		{
			name:      "invalid_iss",
			signature: issueTestJWT(t, failOptsIss),
		},
		{
			name:      "invalid_sub",
			signature: issueTestJWT(t, failOptsSub),
		},
		{
			name:      "missing_exp",
			signature: issueTestJWT(t, failOptsNoExp),
		},
		{
			name:      "expired_token",
			signature: issueTestJWT(t, failOptsExp),
		},
		{
			name:      "missing_iat",
			signature: issueTestJWT(t, failOptsNoIat),
		},
		{
			name:      "invalid_digest",
			signature: issueTestJWT(t, failOptsDigest),
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			jwtVerifier := tc.jwtVerifier
			if jwtVerifier == nil {
				jwtVerifier = securitytest.MustCreateTestVerifier(t)
			}
			handler := newTestTaskHandler(t, withJWTVerifier(jwtVerifier))

			b64Payload := base64.StdEncoding.EncodeToString(payload)
			redisMsg := fmt.Sprintf(`{
				"payload": %q,
				"signature": %q,
				"oidcToken": "valid-token"
			}`, b64Payload, tc.signature)

			result := handler.HandleWorkUnitRedisTask(context.Background(), "test-worker", redisMsg)

			if result.WasRunSuccessful {
				t.Errorf("HandleWorkUnitRedisTask with signature %q: got success=true, want false", tc.signature)
			}
			if result.Error == nil {
				t.Errorf("HandleWorkUnitRedisTask with signature %q: got nil error, want non-nil", tc.signature)
			}
		})
	}
}

func TestIsRetryableError(t *testing.T) {
	gr := schema.GroupResource{Group: "test", Resource: "test"}
	testCases := []struct {
		name string
		err  error
		want bool
	}{
		{
			name: "timeout_error",
			err:  apierrors.NewTimeoutError("timeout", 0),
			want: true,
		},
		{
			name: "server_timeout_error",
			err:  apierrors.NewServerTimeout(gr, "server timeout", 0),
			want: true,
		},
		{
			name: "too_many_requests_error",
			err:  apierrors.NewTooManyRequests("too many requests", 0),
			want: true,
		},
		{
			name: "service_unavailable_error",
			err:  apierrors.NewServiceUnavailable("service unavailable"),
			want: true,
		},
		{
			name: "internal_error",
			err:  apierrors.NewInternalError(fmt.Errorf("internal error")),
			want: true,
		},
		{
			name: "conflict_error",
			err:  apierrors.NewConflict(gr, "conflict", fmt.Errorf("conflict")),
			want: true,
		},
		{
			name: "not_found_error",
			err:  apierrors.NewNotFound(gr, "not found"),
			want: false,
		},
		{
			name: "already_exists_error",
			err:  apierrors.NewAlreadyExists(gr, "already exists"),
			want: false,
		},
		{
			name: "invalid_error",
			err:  apierrors.NewInvalid(schema.GroupKind{}, "invalid", field.ErrorList{}),
			want: false,
		},
		{
			name: "forbidden_error",
			err:  apierrors.NewForbidden(gr, "forbidden", fmt.Errorf("forbidden")),
			want: false,
		},
		{
			name: "unauthorized_error",
			err:  apierrors.NewUnauthorized("unauthorized"),
			want: false,
		},
		{
			name: "bad_request_error",
			err:  apierrors.NewBadRequest("bad request"),
			want: false,
		},
		{
			name: "gone_error",
			err:  apierrors.NewGone("gone"),
			want: false,
		},
		{
			name: "request_entity_too_large_error",
			err:  apierrors.NewRequestEntityTooLargeError("request entity too large"),
			want: false,
		},
		{
			name: "unknown_error",
			err:  fmt.Errorf("unknown error"),
			want: false,
		},
		{
			name: "wrapped_service_unavailable_error",
			err:  fmt.Errorf("failed to create JobSet: %w", apierrors.NewServiceUnavailable("required component not found")),
			want: true,
		},
		{
			name: "doubly_wrapped_service_unavailable_error",
			err:  fmt.Errorf("outer: %w", fmt.Errorf("inner: %w", apierrors.NewServiceUnavailable("required component unhealthy"))),
			want: true,
		},
		{
			name: "context_deadline_exceeded",
			err:  context.DeadlineExceeded,
			want: true,
		},
		{
			name: "wrapped_context_deadline_exceeded",
			err:  fmt.Errorf("request timeout: %w", context.DeadlineExceeded),
			want: true,
		},
		{
			name: "wrapped_timeout_error",
			err:  fmt.Errorf("network failure: %w", mockTimeoutErr{active: true}),
			want: true,
		},
		{
			name: "inactive_timeout_error",
			err:  mockTimeoutErr{active: false},
			want: false,
		},
		{
			name: "wrapped_non_retryable_error",
			err:  fmt.Errorf("failed: %w", apierrors.NewBadRequest("invalid")),
			want: false,
		},
		{
			name: "googleapi_503_service_unavailable",
			err:  &googleapi.Error{Code: http.StatusServiceUnavailable, Message: "Service Unavailable"},
			want: true,
		},
		{
			name: "googleapi_429_too_many_requests",
			err:  &googleapi.Error{Code: http.StatusTooManyRequests, Message: "Too Many Requests"},
			want: true,
		},
		{
			name: "googleapi_500_internal_server_error",
			err:  &googleapi.Error{Code: http.StatusInternalServerError, Message: "Internal Server Error"},
			want: true,
		},
		{
			name: "googleapi_502_bad_gateway",
			err:  &googleapi.Error{Code: http.StatusBadGateway, Message: "Bad Gateway"},
			want: true,
		},
		{
			name: "googleapi_504_gateway_timeout",
			err:  &googleapi.Error{Code: http.StatusGatewayTimeout, Message: "Gateway Timeout"},
			want: true,
		},
		{
			name: "googleapi_400_bad_request",
			err:  &googleapi.Error{Code: http.StatusBadRequest, Message: "Bad Request"},
			want: false,
		},
		{
			name: "googleapi_404_not_found",
			err:  &googleapi.Error{Code: http.StatusNotFound, Message: "Not Found"},
			want: false,
		},
		{
			name: "wrapped_googleapi_503",
			err:  fmt.Errorf("api call failed: %w", &googleapi.Error{Code: http.StatusServiceUnavailable, Message: "Service Unavailable"}),
			want: true,
		},
		{
			name: "mock_status_code_503",
			err:  mockStatusCodeErr{code: http.StatusServiceUnavailable},
			want: true,
		},
		{
			name: "mock_status_code_400",
			err:  mockStatusCodeErr{code: http.StatusBadRequest},
			want: false,
		},
		{
			name: "mock_http_status_code_429",
			err:  mockHTTPStatusCodeErr{code: http.StatusTooManyRequests},
			want: true,
		},
		{
			name: "mock_http_status_code_404",
			err:  mockHTTPStatusCodeErr{code: http.StatusNotFound},
			want: false,
		},
		{
			name: "nil_error",
			err:  nil,
			want: false,
		},
		{
			name: "typed_nil_status_error",
			err:  func() error { var e *apierrors.StatusError; return e }(),
			want: false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			got := isRetryableError(tc.err)
			if got != tc.want {
				t.Errorf("isRetryableError(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}

type mockTimeoutErr struct{ active bool }

func (m mockTimeoutErr) Error() string   { return "mock timeout" }
func (m mockTimeoutErr) Timeout() bool   { return m.active }
func (m mockTimeoutErr) Temporary() bool { return m.active }

type mockStatusCodeErr struct{ code int }

func (m mockStatusCodeErr) Error() string   { return fmt.Sprintf("status code %d", m.code) }
func (m mockStatusCodeErr) StatusCode() int { return m.code }

type mockHTTPStatusCodeErr struct{ code int }

func (m mockHTTPStatusCodeErr) Error() string       { return fmt.Sprintf("http status code %d", m.code) }
func (m mockHTTPStatusCodeErr) HTTPStatusCode() int { return m.code }

func TestHandleActuatorPayload_PauseHandler_Paused(t *testing.T) {
	k8sClient := &mockK8sClient{}
	pauseHandler := &mockPauseHandler{isPaused: true, shouldPause: true}
	handler := newTestTaskHandler(t, withK8sClient(k8sClient), withPauseHandler(pauseHandler))

	payload := &actuatortaskpb.ActuatorPayload{
		Target: proto.String("experiments/100/workUnits/200/executions/1"),
		Action: &actuatortaskpb.Action{
			Action: &actuatortaskpb.Action_Create{Create: &actuatortaskpb.CreateAction{}},
		},
		WorkUnit: &essmsgpb.WorkUnit{Name: "experiments/100/workUnits/200"},
	}

	if err := handler.handleActuatorPayload(context.Background(), payload); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if pauseHandler.handleCalls != 1 {
		t.Errorf("pauseHandler.handleCalls = %d, want 1", pauseHandler.handleCalls)
	}
	if k8sClient.createCalls != 0 {
		t.Errorf("k8sClient.CreateWorkUnit called %d times, want 0", k8sClient.createCalls)
	}
}

func TestHandleActuatorPayload_PauseHandler_NotPaused(t *testing.T) {
	k8sClient := &mockK8sClient{}
	pauseHandler := &mockPauseHandler{isPaused: false, shouldPause: true}
	handler := newTestTaskHandler(t, withK8sClient(k8sClient), withPauseHandler(pauseHandler))

	payload := &actuatortaskpb.ActuatorPayload{
		Target: proto.String("experiments/100/workUnits/200/executions/1"),
		Action: &actuatortaskpb.Action{
			Action: &actuatortaskpb.Action_Create{Create: &actuatortaskpb.CreateAction{}},
		},
		WorkUnit: &essmsgpb.WorkUnit{Name: "experiments/100/workUnits/200"},
	}

	if err := handler.handleActuatorPayload(context.Background(), payload); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if pauseHandler.handleCalls != 0 {
		t.Errorf("pauseHandler.handleCalls = %d, want 0", pauseHandler.handleCalls)
	}
	if k8sClient.createCalls != 1 {
		t.Errorf("k8sClient.CreateWorkUnit called %d times, want 1", k8sClient.createCalls)
	}
}

func TestHandleActuatorPayload_PauseHandler_DeleteBypasses(t *testing.T) {
	k8sClient := &mockK8sClient{}
	pauseHandler := &mockPauseHandler{isPaused: true, shouldPause: false}
	handler := newTestTaskHandler(t, withK8sClient(k8sClient), withPauseHandler(pauseHandler))

	payload := &actuatortaskpb.ActuatorPayload{
		Target: proto.String("experiments/100/workUnits/200/executions/1"),
		Action: &actuatortaskpb.Action{
			Action: &actuatortaskpb.Action_Delete{Delete: &actuatortaskpb.DeleteAction{}},
		},
		WorkUnit: &essmsgpb.WorkUnit{Name: "experiments/100/workUnits/200"},
	}

	if err := handler.handleActuatorPayload(context.Background(), payload); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if pauseHandler.handleCalls != 0 {
		t.Errorf("pauseHandler.handleCalls = %d, want 0", pauseHandler.handleCalls)
	}
	if k8sClient.deleteCalls != 1 {
		t.Errorf("k8sClient.DeleteWorkUnit called %d times, want 1", k8sClient.deleteCalls)
	}
}

func TestHandleActuatorPayload_PauseHandler_Error(t *testing.T) {
	pauseHandler := &mockPauseHandler{isPaused: true, shouldPause: true, err: errors.New("pause handling failed")}
	handler := newTestTaskHandler(t, withPauseHandler(pauseHandler))

	payload := &actuatortaskpb.ActuatorPayload{
		Target: proto.String("experiments/100/workUnits/200/executions/1"),
		Action: &actuatortaskpb.Action{
			Action: &actuatortaskpb.Action_Create{Create: &actuatortaskpb.CreateAction{}},
		},
		WorkUnit: &essmsgpb.WorkUnit{Name: "experiments/100/workUnits/200"},
	}

	if err := handler.handleActuatorPayload(context.Background(), payload); err == nil {
		t.Fatal("expected error from pauseHandler, got nil")
	}
}

func TestHandleActuatorPayload_PauseHandler_ResumedOnResumptionTime(t *testing.T) {
	k8sClient := &mockK8sClient{}
	resumptionTime := time.Date(2026, 9, 24, 10, 5, 0, 0, time.UTC)
	pauseHandler := &mockPauseHandler{
		isPaused:    true,
		shouldPause: true,
	}
	handler := newTestTaskHandler(t, withK8sClient(k8sClient), withPauseHandler(pauseHandler))

	payload := &actuatortaskpb.ActuatorPayload{
		Target: proto.String("experiments/100/workUnits/200/executions/1"),
		Action: &actuatortaskpb.Action{
			Action: &actuatortaskpb.Action_Create{Create: &actuatortaskpb.CreateAction{}},
		},
		WorkUnit: &essmsgpb.WorkUnit{Name: "experiments/100/workUnits/200"},
		Metadata: &actuatortaskpb.Metadata{
			ResumptionRunId: proto.String("run-1"),
			ResumptionTime:  timestamppb.New(resumptionTime),
		},
	}

	if err := handler.handleActuatorPayload(context.Background(), payload); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if pauseHandler.handleCalls != 0 {
		t.Errorf("pauseHandler.handleCalls = %d, want 0 (task should not be buffered)", pauseHandler.handleCalls)
	}
	if k8sClient.createCalls != 1 {
		t.Errorf("k8sClient.CreateWorkUnit called %d times, want 1", k8sClient.createCalls)
	}
}
