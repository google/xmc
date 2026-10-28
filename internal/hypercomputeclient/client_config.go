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

package hypercomputeclient

import (
	"context"
	"fmt"

	"google.golang.org/api/impersonate"
)

const cloudPlatformScope = "https://www.googleapis.com/auth/cloud-platform"

// Config holds the deployment settings needed to build a ControlPlaneClient.
type Config struct {
	// Project is the GCP project hosting the MLRun resources.
	Project string
	// Environment is one of "autopush", "staging", or "prod".
	Environment string
	// Location defaults to "us-central1" when empty.
	Location string
	// ImpersonateServiceAccount, when set, is impersonated for all API calls.
	ImpersonateServiceAccount string
}

// NewControlPlaneClientFromConfig builds a ControlPlaneClient from cfg, wiring an
// impersonated token source when cfg.ImpersonateServiceAccount is set.
func NewControlPlaneClientFromConfig(ctx context.Context, cfg Config) (*ControlPlaneClient, error) {
	var opts []Option
	if cfg.ImpersonateServiceAccount != "" {
		ts, err := impersonate.CredentialsTokenSource(ctx, impersonate.CredentialsConfig{
			TargetPrincipal: cfg.ImpersonateServiceAccount,
			Scopes:          []string{cloudPlatformScope},
		})
		if err != nil {
			return nil, fmt.Errorf("create impersonated token source for %q: %w", cfg.ImpersonateServiceAccount, err)
		}
		opts = append(opts, WithTokenSource(ts))
	}
	return NewControlPlaneClient(ctx, cfg.Project, cfg.Environment, cfg.Location, opts...)
}
