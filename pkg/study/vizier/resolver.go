// Copyright 2026 Google LLC
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

// Package vizier resolves the live vizier Component CR endpoint and manages the shared Vizier gRPC
// connection and readiness probe for xmc-study-controller.
package vizier

import (
	"context"
	"errors"
	"fmt"
	"math/rand/v2"
	"net"
	"strconv"
	"strings"
	"time"
	"unicode"

	"xmanager_cloud/internal/logging"
	"xmanager_cloud/pkg/components/crreader"
)

const (
	vizierComponentName   = "vizier"
	defaultInitialBackoff = 500 * time.Millisecond
	defaultMaxBackoff     = 10 * time.Second
)

// WaitForEndpoint polls the live vizier Component CR in namespace until it is enabled and has a
// valid host:port spec.parameters.endpoint, backing off exponentially with jitter up to 10s.
func WaitForEndpoint(ctx context.Context, getter crreader.Getter, namespace string) (string, error) {
	return waitForEndpoint(ctx, getter, namespace, defaultInitialBackoff, defaultMaxBackoff)
}

func waitForEndpoint(ctx context.Context, getter crreader.Getter, namespace string, initialBackoff, maxBackoff time.Duration) (string, error) {
	if getter == nil {
		return "", errors.New("component getter cannot be nil")
	}
	ns := crreader.ResolveNamespace(namespace)
	delay := initialBackoff
	for {
		endpoint, err := resolveEndpoint(ctx, getter, ns)
		if err == nil {
			logging.Info(ctx, "Resolved Vizier endpoint from Component CR", "namespace", ns, "component", vizierComponentName, "endpoint", endpoint)
			return endpoint, nil
		}
		if ctx.Err() != nil {
			return "", ctx.Err()
		}
		logging.Warn(ctx, "Waiting for valid Vizier Component CR endpoint", "namespace", ns, "component", vizierComponentName, "error", err, "retry_in", delay)
		if err := sleepWithJitter(ctx, delay); err != nil {
			return "", err
		}
		delay = min(delay*2, maxBackoff)
	}
}

func resolveEndpoint(ctx context.Context, getter crreader.Getter, ns string) (string, error) {
	comp, err := getter.GetComponent(ctx, ns, vizierComponentName)
	if err != nil {
		return "", fmt.Errorf("failed to get component %s/%s: %w", ns, vizierComponentName, err)
	}
	if crreader.IsDisabled(comp) {
		return "", fmt.Errorf("component %s/%s is disabled", ns, vizierComponentName)
	}
	params, err := crreader.Parameters(comp)
	if err != nil {
		return "", fmt.Errorf("failed to parse parameters for %s/%s: %w", ns, vizierComponentName, err)
	}
	return validateEndpoint(params["endpoint"])
}

// validateEndpoint validates raw as an ASCII host:port without scheme, userinfo, path, query,
// fragment, whitespace, control characters, or multibyte runes. Errors never echo raw.
func validateEndpoint(raw string) (string, error) {
	if raw == "" {
		return "", errors.New("vizier endpoint is required")
	}
	for _, r := range raw {
		if r > 0x7f || unicode.IsSpace(r) || unicode.IsControl(r) {
			return "", errors.New("invalid vizier endpoint: must be ASCII host:port without whitespace or control characters")
		}
	}
	if strings.ContainsAny(raw, "/?#@\\") {
		return "", errors.New("invalid vizier endpoint: must be host:port without a scheme, credentials, path, query, or fragment")
	}
	host, port, err := net.SplitHostPort(raw)
	if err != nil || host == "" {
		return "", errors.New("invalid vizier endpoint: must be host:port, with IPv6 hosts in brackets")
	}
	if strings.Contains(host, ":") && net.ParseIP(host) == nil {
		return "", errors.New("invalid vizier endpoint: malformed IPv6 host")
	}
	n, err := strconv.ParseUint(port, 10, 64)
	if err != nil || n < 1 || n > 65535 {
		return "", errors.New("invalid vizier endpoint: port must be between 1 and 65535")
	}
	return raw, nil
}

func sleepWithJitter(ctx context.Context, base time.Duration) error {
	if base <= 0 {
		return ctx.Err()
	}
	// Apply +/-20% jitter so retries never synchronize across replicas.
	jitter := time.Duration(float64(base) * (0.8 + 0.4*rand.Float64()))
	timer := time.NewTimer(jitter)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
