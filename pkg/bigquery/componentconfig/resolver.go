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

// Package componentconfig resolves BigQuery client options from the Kubernetes
// bigquery Component Custom Resource or static environment configuration.
package componentconfig

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"strings"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"xmanager_cloud/internal/logging"
	xmcbigquery "xmanager_cloud/pkg/bigquery"
	"xmanager_cloud/pkg/components/crreader"
)

// BigQueryComponentName is the canonical Kubernetes Component CR name for BigQuery.
const BigQueryComponentName = "bigquery"

// ErrNotConfigured indicates that BigQuery is not configured because the Component CR is absent
// or disabled and static environment variables are not set.
var ErrNotConfigured = errors.New("bigquery is not configured")

// Source holds the configuration and reader required to resolve BigQuery options from a Component CR.
type Source struct {
	Enabled           bool
	Namespace         string
	FallbackProjectID string
	Getter            crreader.Getter
}

// OptionsFingerprint returns a deterministic SHA-256 hex digest of the BigQuery target coordinates in opts.
func OptionsFingerprint(opts *xmcbigquery.Options) string {
	if opts == nil {
		return ""
	}
	payload := opts.ProjectID + "\x00" + opts.Dataset + "\x00" + opts.Table
	sum := sha256.Sum256([]byte(payload))
	return hex.EncodeToString(sum[:])
}

// ResolveOptions returns BigQuery options resolved from the bigquery Component CR when src.Enabled
// is true, falling back to environment variables only when the CR is NotFound or disabled.
func ResolveOptions(ctx context.Context, src Source) (*xmcbigquery.Options, error) {
	if !src.Enabled {
		return xmcbigquery.GetOptions(src.FallbackProjectID)
	}
	if src.Getter == nil {
		return nil, fmt.Errorf("component getter cannot be nil when enable_components is true")
	}
	ns := crreader.ResolveNamespace(src.Namespace)
	comp, err := src.Getter.GetComponent(ctx, ns, BigQueryComponentName)
	if apierrors.IsNotFound(err) || (err == nil && (comp == nil || crreader.IsDisabled(comp))) {
		return resolveStaticFallback(ctx, ns, src.FallbackProjectID)
	}
	if err != nil {
		return nil, fmt.Errorf("failed to get component %s/%s: %w", ns, BigQueryComponentName, err)
	}
	return parseComponentOptions(ctx, ns, comp, src.FallbackProjectID)
}

func resolveStaticFallback(ctx context.Context, ns, fallbackProjectID string) (*xmcbigquery.Options, error) {
	if !xmcbigquery.Configured() {
		return nil, ErrNotConfigured
	}
	logging.Info(ctx, "BigQuery Component CR is absent or disabled; falling back to static environment configuration",
		"namespace", ns, "component", BigQueryComponentName)
	return xmcbigquery.GetOptions(fallbackProjectID)
}

func parseComponentOptions(ctx context.Context, ns string, comp *unstructured.Unstructured, fallbackProjectID string) (*xmcbigquery.Options, error) {
	params, err := crreader.Parameters(comp)
	if err != nil {
		return nil, fmt.Errorf("failed to parse spec.parameters from component %s/%s: %w", ns, BigQueryComponentName, err)
	}
	envVars, err := crreader.AccessEnvVars(comp)
	if err != nil {
		return nil, fmt.Errorf("failed to parse status.access.envVars from component %s/%s: %w", ns, BigQueryComponentName, err)
	}
	opts := &xmcbigquery.Options{
		ProjectID: firstNonEmpty(envVars["PROJECT_ID"], params["projectId"], os.Getenv("BIGQUERY_PROJECT_ID"), os.Getenv("GOOGLE_CLOUD_PROJECT"), fallbackProjectID),
		Dataset:   firstNonEmpty(envVars["DATASET"], params["dataset"], xmcbigquery.DefaultDataset),
		Table:     firstNonEmpty(envVars["TABLE"], params["table"], xmcbigquery.DefaultTable),
	}
	if err := opts.Validate(); err != nil {
		return nil, fmt.Errorf("invalid configuration in component %s/%s: %w", ns, BigQueryComponentName, err)
	}
	logging.Info(ctx, "BigQuery configuration resolved from Component CR",
		"namespace", ns, "component", BigQueryComponentName, "project", opts.ProjectID, "dataset", opts.Dataset, "table", opts.Table)
	return opts, nil
}

func firstNonEmpty(values ...string) string {
	for _, v := range values {
		if trimmed := strings.TrimSpace(v); trimmed != "" {
			return trimmed
		}
	}
	return ""
}
