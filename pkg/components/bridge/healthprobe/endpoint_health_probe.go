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

package healthprobe

import (
	"context"
	"errors"
	"time"
)

// EndpointProber is the active-probe capability shared by HealthProber and StatusDetectorHealthProber.
type EndpointProber interface {
	// Probe executes an active HTTP/TCP probe against target.
	Probe(ctx context.Context, target string, timeout time.Duration) (*HealthProbeResult, error)
}

// ErrNilProbeResult reports that a prober returned neither a result nor an error.
var ErrNilProbeResult = errors.New("health prober returned nil result")

// ProbeEndpointHealth runs an active health probe against endpoint and returns the prober's result.
// Probing is skipped, reporting a healthy result without a message, when endpoint is empty or prober
// is nil. A probe error is returned as is, and a nil probe result is reported as ErrNilProbeResult,
// so a nil error always comes with a non-nil result. Callers own how outcomes map to messages.
func ProbeEndpointHealth(ctx context.Context, prober EndpointProber, endpoint string, timeout time.Duration) (*HealthProbeResult, error) {
	if endpoint == "" || prober == nil {
		return &HealthProbeResult{Healthy: true}, nil
	}
	res, err := prober.Probe(ctx, endpoint, timeout)
	if err != nil {
		return nil, err
	}
	if res == nil {
		return nil, ErrNilProbeResult
	}
	return res, nil
}
