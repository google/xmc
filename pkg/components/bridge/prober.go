// Copyright 2026 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//      http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package bridge

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"

	componentspb "xmanager_cloud/api/protos/components/v1alpha1"
)

// Prober defines the interface for checking component health and readiness.
type Prober interface {
	// ProbeHealth checks if the endpoints specified in the spec are reachable.
	ProbeHealth(ctx context.Context, spec *componentspb.ComponentSpecProto) (bool, string)
}

// NetworkProber performs TCP and HTTP connection probes.
type NetworkProber struct {
	httpClient *http.Client
	timeout    time.Duration
}

// NewNetworkProber creates a new NetworkProber instance.
func NewNetworkProber(timeout time.Duration) *NetworkProber {
	return &NetworkProber{
		httpClient: &http.Client{Timeout: timeout},
		timeout:    timeout,
	}
}

// ProbeHealth evaluates network reachability based on spec parameters.
func (p *NetworkProber) ProbeHealth(ctx context.Context, spec *componentspb.ComponentSpecProto) (bool, string) {
	if spec == nil || !spec.GetEnabled() {
		return false, "Component is disabled"
	}

	params := spec.GetParameters()
	if len(params) == 0 {
		return true, "No specific network endpoints declared; assumed healthy"
	}

	// 1. Check HTTP health check endpoint if specified
	if healthURL, ok := params["healthCheckUrl"]; ok && healthURL != "" {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, healthURL, nil)
		if err != nil {
			return false, fmt.Sprintf("invalid healthCheckUrl: %v", err)
		}
		resp, err := p.httpClient.Do(req)
		if err != nil {
			return false, fmt.Sprintf("HTTP probe failed: %v", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode < 200 || resp.StatusCode >= 400 {
			return false, fmt.Sprintf("HTTP probe returned status %d", resp.StatusCode)
		}
		return true, fmt.Sprintf("HTTP probe OK (status %d)", resp.StatusCode)
	}

	// 2. Check TCP endpoints (clusterEndpoints, brokerEndpoints, or host:port)
	var endpointCandidates []string
	if ep, ok := params["clusterEndpoints"]; ok && ep != "" {
		endpointCandidates = append(endpointCandidates, strings.Split(ep, ",")...)
	}
	if ep, ok := params["brokerEndpoints"]; ok && ep != "" {
		endpointCandidates = append(endpointCandidates, strings.Split(ep, ",")...)
	}
	if ep, ok := params["redpandaBrokers"]; ok && ep != "" {
		endpointCandidates = append(endpointCandidates, strings.Split(ep, ",")...)
	}

	for _, ep := range endpointCandidates {
		target := strings.TrimSpace(ep)
		if target == "" {
			continue
		}
		dialer := net.Dialer{Timeout: p.timeout}
		conn, err := dialer.DialContext(ctx, "tcp", target)
		if err != nil {
			// In isolated testing or offline mode, report dial error details
			return false, fmt.Sprintf("TCP dial to %q failed: %v", target, err)
		}
		conn.Close()
	}

	return true, "Endpoints reachable"
}
