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

// Package componentconfig resolves ClickHouse client options from the Kubernetes
// clickhouse Component Custom Resource and referenced credentials Secret.
package componentconfig

import (
	"context"
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
	"time"

	"github.com/ClickHouse/clickhouse-go/v2"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"xmanager_cloud/internal/logging"
	xmcclickhouse "xmanager_cloud/pkg/clickhouse"
	"xmanager_cloud/pkg/components/crreader"
)

// ClickHouseComponentName is the canonical Kubernetes Component CR name for ClickHouse.
const ClickHouseComponentName = "clickhouse"

// ErrNotConfigured indicates that ClickHouse is not configured because the Component CR is absent
// or disabled and static environment variables are not set.
var ErrNotConfigured = errors.New("clickhouse is not configured")

const (
	defaultPort        = "9000"
	defaultDatabase    = "default"
	defaultUser        = "default"
	defaultSecretName  = "clickhouse-secret"
	defaultSecretKey   = "clickhouse-password"
	defaultDialTimeout = 5 * time.Second
)

// Source holds the configuration and readers required to resolve ClickHouse options from a Component CR.
type Source struct {
	Enabled   bool
	Namespace string
	Getter    crreader.Getter
	Secrets   crreader.SecretReader
}

// ResolveOptions returns ClickHouse connection options resolved from the clickhouse Component CR
// when src.Enabled is true, falling back to environment variables only when the CR is NotFound or disabled.
func ResolveOptions(ctx context.Context, src Source) (*clickhouse.Options, error) {
	if !src.Enabled {
		return xmcclickhouse.GetOptions()
	}
	if src.Getter == nil {
		return nil, fmt.Errorf("component getter cannot be nil when enable_components is true")
	}
	ns := crreader.ResolveNamespace(src.Namespace)
	comp, err := src.Getter.GetComponent(ctx, ns, ClickHouseComponentName)
	if apierrors.IsNotFound(err) || (err == nil && (comp == nil || crreader.IsDisabled(comp))) {
		if !xmcclickhouse.Configured() {
			return nil, ErrNotConfigured
		}
		logging.Info(ctx, "ClickHouse Component CR is absent or disabled; falling back to static environment configuration", "namespace", ns, "component", ClickHouseComponentName)
		return xmcclickhouse.GetOptions()
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get component %s/%s: %w", ns, ClickHouseComponentName, err)
	}
	params, err := crreader.Parameters(comp)
	if err != nil {
		return nil, fmt.Errorf("failed to parse spec.parameters from component %s/%s: %w", ns, ClickHouseComponentName, err)
	}
	envVars, err := crreader.AccessEnvVars(comp)
	if err != nil {
		return nil, fmt.Errorf("failed to parse status.access.envVars from component %s/%s: %w", ns, ClickHouseComponentName, err)
	}
	addrs, addrSource, err := resolveAddresses(ns, params, envVars)
	if err != nil {
		return nil, err
	}
	password, err := resolvePassword(ctx, src.Secrets, ns, params, envVars)
	if err != nil {
		return nil, err
	}
	database := firstNonEmpty(params["defaultDatabase"], defaultDatabase)
	username := firstNonEmpty(params["user"], defaultUser)
	logging.Info(ctx, "ClickHouse configuration resolved from Component CR",
		"namespace", ns, "component", ClickHouseComponentName, "source", addrSource, "addrs", addrs, "database", database, "user", username)
	return &clickhouse.Options{
		Addr:        addrs,
		Auth:        clickhouse.Auth{Database: database, Username: username, Password: password},
		DialTimeout: defaultDialTimeout,
	}, nil
}

func resolveAddresses(ns string, params, envVars map[string]string) ([]string, string, error) {
	envPort := firstNonEmpty(envVars["PORT"], params["nativePort"], defaultPort)
	specPort := firstNonEmpty(params["nativePort"], defaultPort)

	// If status.access.envVars["ENDPOINT"] contains multiple endpoints, prefer it.
	if endpoint := strings.TrimSpace(envVars["ENDPOINT"]); strings.Contains(endpoint, ",") {
		addrs, err := parseEndpoints(endpoint, envPort)
		if err != nil {
			return nil, "", err
		}
		if len(addrs) > 1 {
			return addrs, "status.access.envVars.ENDPOINT", nil
		}
	}

	// Preserve multi-replica clusterEndpoints when xmc-bridge only populates the first probed endpoint.
	if clusterEndpoints := strings.TrimSpace(params["clusterEndpoints"]); strings.Contains(clusterEndpoints, ",") {
		addrs, err := parseEndpoints(clusterEndpoints, specPort)
		if err != nil {
			return nil, "", err
		}
		if len(addrs) > 1 {
			return addrs, "spec.parameters.clusterEndpoints", nil
		}
	}

	// Check status.access.envVars["ENDPOINT"] before HOST.
	if endpoint := strings.TrimSpace(envVars["ENDPOINT"]); endpoint != "" {
		addrs, err := parseEndpoints(endpoint, envPort)
		return addrs, "status.access.envVars.ENDPOINT", err
	}
	if host := strings.TrimSpace(envVars["HOST"]); host != "" {
		addr, err := joinValidatedHostPort(host, envPort)
		return []string{addr}, "status.access.envVars.HOST", err
	}
	if clusterEndpoints := strings.TrimSpace(params["clusterEndpoints"]); clusterEndpoints != "" {
		addrs, err := parseEndpoints(clusterEndpoints, specPort)
		return addrs, "spec.parameters.clusterEndpoints", err
	}
	if host := strings.TrimSpace(params["host"]); host != "" {
		addr, err := joinValidatedHostPort(host, specPort)
		return []string{addr}, "spec.parameters.host", err
	}
	return nil, "", fmt.Errorf("component %s/%s has no ClickHouse endpoint or host configured", ns, ClickHouseComponentName)
}

func parseEndpoints(raw, defaultPortStr string) ([]string, error) {
	if _, err := parsePort(defaultPortStr); err != nil {
		return nil, err
	}
	var addrs []string
	for _, token := range strings.Split(raw, ",") {
		if token = strings.TrimSpace(token); token == "" {
			continue
		}
		addr, err := normalizeEndpointToken(token, defaultPortStr)
		if err != nil {
			return nil, err
		}
		addrs = append(addrs, addr)
	}
	if len(addrs) == 0 {
		return nil, fmt.Errorf("no valid ClickHouse endpoints in %q", raw)
	}
	return addrs, nil
}

func normalizeEndpointToken(token, defaultPortStr string) (string, error) {
	if host, port, err := net.SplitHostPort(token); err == nil {
		return joinValidatedHostPort(host, port)
	}
	cleanToken := strings.TrimPrefix(strings.TrimSuffix(token, "]"), "[")
	if strings.Contains(token, ":") && net.ParseIP(cleanToken) == nil {
		return "", fmt.Errorf("invalid ClickHouse endpoint %q", token)
	}
	return joinValidatedHostPort(cleanToken, defaultPortStr)
}

func joinValidatedHostPort(host, portStr string) (string, error) {
	if strings.TrimSpace(host) == "" {
		return "", fmt.Errorf("clickhouse host cannot be empty")
	}
	port, err := parsePort(portStr)
	if err != nil {
		return "", err
	}
	return net.JoinHostPort(strings.TrimSpace(host), strconv.Itoa(port)), nil
}

func parsePort(portStr string) (int, error) {
	port, err := strconv.Atoi(strings.TrimSpace(portStr))
	if err != nil || port < 1 || port > 65535 {
		return 0, fmt.Errorf("invalid ClickHouse port %q: must be an integer between 1 and 65535", portStr)
	}
	return port, nil
}

func resolvePassword(ctx context.Context, secrets crreader.SecretReader, ns string, params, envVars map[string]string) (string, error) {
	if pw, ok, err := xmcclickhouse.PasswordFromFileEnv(); ok || err != nil {
		return pw, err
	}
	if secrets == nil {
		return "", fmt.Errorf("secret reader cannot be nil when resolving ClickHouse component password without CSI")
	}
	return secrets.GetSecretValue(ctx, ns, firstNonEmpty(envVars["SECRET_NAME"], params["secretName"], defaultSecretName), firstNonEmpty(params["secretPasswordKey"], defaultSecretKey))
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if trimmed := strings.TrimSpace(v); trimmed != "" {
			return trimmed
		}
	}
	return ""
}
