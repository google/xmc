// Copyright 2026 Google LLC
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

// Package mlflow resolves the mlflow Component Custom Resource and provides a
// minimal MLflow 3.x REST client for creating and managing tracking runs.
package mlflow

import (
	"context"
	"fmt"
	"strings"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"xmanager_cloud/pkg/components/crreader"
)

// ComponentName is the canonical Kubernetes Component CR name for MLflow.
const ComponentName = "mlflow"

// Credentials holds resolved MLflow authentication material for server-side REST calls.
// It is never serialized into JobSets, tags, status messages, or logs.
type Credentials struct {
	token    string
	username string
	password string
}

// Config holds the resolved MLflow component configuration and Secret key references.
type Config struct {
	TrackingURI       string
	UIBaseURL         string
	SecretNamespace   string
	SecretName        string
	SecretTokenKey    string
	SecretUsernameKey string
	SecretPasswordKey string
	creds             Credentials
}

// ResolveConfig reads the mlflow Component CR from namespace (falling back to the
// default components namespace) and resolves its tracking configuration and credentials.
// Returns (nil, false, nil) when the component is absent, disabled, or not Ready.
func ResolveConfig(ctx context.Context, getter crreader.Getter, secrets crreader.SecretReader, namespace string) (*Config, bool, error) {
	if getter == nil {
		return nil, false, fmt.Errorf("component getter cannot be nil")
	}
	comp, compNS, err := fetchComponent(ctx, getter, namespace)
	if err != nil {
		return nil, false, err
	}
	if comp == nil || crreader.IsDisabled(comp) || !isComponentReady(comp) {
		return nil, false, nil
	}
	cfg, err := buildConfigFromComponent(comp, compNS)
	if err != nil {
		return nil, false, err
	}
	if cfg.SecretName != "" {
		if err := populateCredentials(ctx, secrets, cfg); err != nil {
			return cfg, true, err
		}
	}
	return cfg, true, nil
}

func fetchComponent(ctx context.Context, getter crreader.Getter, namespace string) (*unstructured.Unstructured, string, error) {
	targetNS := crreader.ResolveNamespace(namespace)
	comp, err := getter.GetComponent(ctx, targetNS, ComponentName)
	if err == nil {
		return comp, targetNS, nil
	}
	if !apierrors.IsNotFound(err) {
		return nil, "", fmt.Errorf("failed to get component %s/%s: %w", targetNS, ComponentName, err)
	}
	defaultNS := crreader.ResolveNamespace("")
	if targetNS == defaultNS {
		return nil, "", nil
	}
	comp, err = getter.GetComponent(ctx, defaultNS, ComponentName)
	if apierrors.IsNotFound(err) {
		return nil, "", nil
	}
	if err != nil {
		return nil, "", fmt.Errorf("failed to get component %s/%s: %w", defaultNS, ComponentName, err)
	}
	return comp, defaultNS, nil
}

func buildConfigFromComponent(comp *unstructured.Unstructured, compNS string) (*Config, error) {
	params, err := crreader.Parameters(comp)
	if err != nil {
		return nil, fmt.Errorf("failed to parse spec.parameters from component %s/%s: %w", compNS, ComponentName, err)
	}
	envVars, err := crreader.AccessEnvVars(comp)
	if err != nil {
		return nil, fmt.Errorf("failed to parse status.access.envVars from component %s/%s: %w", compNS, ComponentName, err)
	}
	trackingURI := firstNonEmptyToken(params["trackingUri"], envVars["TRACKING_URI"], envVars["ENDPOINT"])
	if trackingURI == "" {
		return nil, fmt.Errorf("component %s/%s has no trackingUri configured", compNS, ComponentName)
	}
	uiBaseURL := firstNonEmptyToken(params["uiBaseUrl"], envVars["UI_BASE_URL"])
	if uiBaseURL == "" {
		uiBaseURL = trackingURI
	}
	secretNS := comp.GetNamespace()
	if secretNS == "" {
		secretNS = compNS
	}
	return &Config{
		TrackingURI:       strings.TrimRight(trackingURI, "/"),
		UIBaseURL:         strings.TrimRight(uiBaseURL, "/"),
		SecretNamespace:   secretNS,
		SecretName:        firstNonEmpty(params["secretName"], envVars["SECRET_NAME"]),
		SecretTokenKey:    firstNonEmpty(params["secretTokenKey"], envVars["SECRET_TOKEN_KEY"]),
		SecretUsernameKey: firstNonEmpty(params["secretUsernameKey"], envVars["SECRET_USERNAME_KEY"]),
		SecretPasswordKey: firstNonEmpty(params["secretPasswordKey"], envVars["SECRET_PASSWORD_KEY"]),
	}, nil
}

func populateCredentials(ctx context.Context, secrets crreader.SecretReader, cfg *Config) error {
	if secrets == nil {
		return fmt.Errorf("secret reader cannot be nil when secretName is configured")
	}
	if cfg.SecretTokenKey != "" {
		tok, err := secrets.GetSecretValue(ctx, cfg.SecretNamespace, cfg.SecretName, cfg.SecretTokenKey)
		if err != nil {
			return fmt.Errorf("failed to read mlflow token secret: %w", err)
		}
		cfg.creds.token = tok
		return nil
	}
	if cfg.SecretUsernameKey != "" || cfg.SecretPasswordKey != "" {
		if cfg.SecretUsernameKey == "" || cfg.SecretPasswordKey == "" {
			return fmt.Errorf("both secretUsernameKey and secretPasswordKey must be set for basic auth")
		}
		user, err := secrets.GetSecretValue(ctx, cfg.SecretNamespace, cfg.SecretName, cfg.SecretUsernameKey)
		if err != nil {
			return fmt.Errorf("failed to read mlflow username secret: %w", err)
		}
		pass, err := secrets.GetSecretValue(ctx, cfg.SecretNamespace, cfg.SecretName, cfg.SecretPasswordKey)
		if err != nil {
			return fmt.Errorf("failed to read mlflow password secret: %w", err)
		}
		cfg.creds.username = user
		cfg.creds.password = pass
		return nil
	}
	return fmt.Errorf("secretName %q is set without secretTokenKey or username/password keys", cfg.SecretName)
}

func isComponentReady(comp *unstructured.Unstructured) bool {
	if comp == nil || comp.Object == nil {
		return false
	}
	conditions, ok, err := unstructured.NestedSlice(comp.Object, "status", "conditions")
	if err != nil || !ok {
		return false
	}
	for _, c := range conditions {
		cMap, ok := c.(map[string]any)
		if !ok {
			continue
		}
		if cMap["type"] == "Ready" {
			return cMap["status"] == "True"
		}
	}
	return false
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if trimmed := strings.TrimSpace(v); trimmed != "" {
			return trimmed
		}
	}
	return ""
}

func firstNonEmptyToken(values ...string) string {
	for _, v := range values {
		for _, tok := range strings.Split(v, ",") {
			if trimmed := strings.TrimSpace(tok); trimmed != "" {
				return trimmed
			}
		}
	}
	return ""
}
