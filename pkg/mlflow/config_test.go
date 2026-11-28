//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package mlflow

import (
	"context"
	"testing"

	"github.com/google/go-cmp/cmp"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"xmanager_cloud/pkg/components/crreader/crreadertest"
)

func readyMLflowComponent(ns string, params map[string]any) *unstructured.Unstructured {
	return &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": "xmc.dev/v1alpha1",
			"kind":       "Component",
			"metadata": map[string]any{
				"name":      ComponentName,
				"namespace": ns,
			},
			"spec": map[string]any{
				"enabled":    true,
				"parameters": params,
			},
			"status": map[string]any{
				"state": "COMPONENT_STATE_ENABLED",
				"conditions": []any{
					map[string]any{"type": "Ready", "status": "True"},
				},
			},
		},
	}
}

func TestResolveConfig(t *testing.T) {
	ctx := context.Background()

	tests := []struct {
		name        string
		namespace   string
		getter      *crreadertest.FakeGetter
		secrets     *crreadertest.FakeSecretReader
		wantEnabled bool
		wantCfg     *Config
		wantErr     bool
	}{
		{
			name:        "absent component returns false",
			namespace:   "team-a",
			getter:      &crreadertest.FakeGetter{},
			wantEnabled: false,
		},
		{
			name:      "disabled component returns false",
			namespace: "xmc",
			getter: &crreadertest.FakeGetter{
				Components: map[string]*unstructured.Unstructured{
					"xmc/mlflow": {
						Object: map[string]any{
							"metadata": map[string]any{"name": "mlflow", "namespace": "xmc"},
							"spec":     map[string]any{"enabled": false},
						},
					},
				},
			},
			wantEnabled: false,
		},
		{
			name:      "unready component returns false",
			namespace: "xmc",
			getter: &crreadertest.FakeGetter{
				Components: map[string]*unstructured.Unstructured{
					"xmc/mlflow": {
						Object: map[string]any{
							"metadata": map[string]any{"name": "mlflow", "namespace": "xmc"},
							"spec":     map[string]any{"enabled": true},
							"status": map[string]any{
								"conditions": []any{map[string]any{"type": "Ready", "status": "False"}},
							},
						},
					},
				},
			},
			wantEnabled: false,
		},
		{
			name:      "resolves from default xmc namespace with comma-delimited trackingUri and token",
			namespace: "team-a",
			getter: &crreadertest.FakeGetter{
				Components: map[string]*unstructured.Unstructured{
					"xmc/mlflow": readyMLflowComponent("xmc", map[string]any{
						"trackingUri":    " , https://mlflow.internal:5000/ , https://backup:5000",
						"secretName":     "mlflow-creds",
						"secretTokenKey": "token",
					}),
				},
			},
			secrets: &crreadertest.FakeSecretReader{
				Secrets: map[string]map[string]string{
					"xmc/mlflow-creds": {"token": "secret-tok-123"},
				},
			},
			wantEnabled: true,
			wantCfg: &Config{
				TrackingURI:     "https://mlflow.internal:5000",
				UIBaseURL:       "https://mlflow.internal:5000",
				SecretNamespace: "xmc",
				SecretName:      "mlflow-creds",
				SecretTokenKey:  "token",
				creds:           Credentials{token: "secret-tok-123"},
			},
		},
		{
			name:      "resolves basic auth and explicit uiBaseUrl",
			namespace: "xmc",
			getter: &crreadertest.FakeGetter{
				Components: map[string]*unstructured.Unstructured{
					"xmc/mlflow": readyMLflowComponent("xmc", map[string]any{
						"trackingUri":       "http://mlflow-service.xmc.svc.cluster.local:5000",
						"uiBaseUrl":         "https://mlflow.example.com/,https://other.example.com",
						"secretName":        "mlflow-basic",
						"secretUsernameKey": "username",
						"secretPasswordKey": "password",
					}),
				},
			},
			secrets: &crreadertest.FakeSecretReader{
				Secrets: map[string]map[string]string{
					"xmc/mlflow-basic": {
						"username": "alice",
						"password": "s3cr3t",
					},
				},
			},
			wantEnabled: true,
			wantCfg: &Config{
				TrackingURI:       "http://mlflow-service.xmc.svc.cluster.local:5000",
				UIBaseURL:         "https://mlflow.example.com",
				SecretNamespace:   "xmc",
				SecretName:        "mlflow-basic",
				SecretUsernameKey: "username",
				SecretPasswordKey: "password",
				creds:             Credentials{username: "alice", password: "s3cr3t"},
			},
		},
		{
			name:      "missing secret key returns error with enabled true",
			namespace: "xmc",
			getter: &crreadertest.FakeGetter{
				Components: map[string]*unstructured.Unstructured{
					"xmc/mlflow": readyMLflowComponent("xmc", map[string]any{
						"trackingUri":    "http://mlflow:5000",
						"secretName":     "mlflow-creds",
						"secretTokenKey": "token",
					}),
				},
			},
			secrets:     &crreadertest.FakeSecretReader{},
			wantEnabled: true,
			wantErr:     true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg, enabled, err := ResolveConfig(ctx, tc.getter, tc.secrets, tc.namespace)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("ResolveConfig() succeeded, want error")
				}
				return
			}
			if err != nil {
				t.Fatalf("ResolveConfig() unexpected error: %v", err)
			}
			if enabled != tc.wantEnabled {
				t.Fatalf("ResolveConfig() enabled = %v, want %v", enabled, tc.wantEnabled)
			}
			if diff := cmp.Diff(tc.wantCfg, cfg, cmp.AllowUnexported(Config{}, Credentials{})); diff != "" {
				t.Errorf("ResolveConfig() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}
