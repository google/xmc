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

package bridge

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	componentspb "xmanager_cloud/api/protos/components/v1alpha1"
)

// StatusDetectorHealthProber defines the status detection and active health checking interface for 3P custom resources.
type StatusDetectorHealthProber interface {
	// DetectStatus queries the target 3P custom resource to determine readiness and extract access coordinates.
	DetectStatus(ctx context.Context, ref WrapperRef) (*ResourceStatus, error)
	// Probe executes an active HTTP/TCP probe against target.
	Probe(ctx context.Context, target string, timeout time.Duration) (*HealthProbeResult, error)
	// ProbeHealth evaluates the runtime health of a component based on its spec and default namespace, returning health status and message.
	ProbeHealth(ctx context.Context, spec *componentspb.ComponentSpec, defaultNamespace string) (bool, string)
}

// BridgeProber combines StatusDetector and HealthProber into StatusDetectorHealthProber.
type BridgeProber struct {
	detector     StatusDetector
	healthProber HealthProber
	probeTimeout time.Duration
}

var _ StatusDetectorHealthProber = (*BridgeProber)(nil)

// NewBridgeProber creates a BridgeProber combining detector and health prober.
func NewBridgeProber(detector StatusDetector, prober HealthProber, probeTimeout time.Duration) *BridgeProber {
	if probeTimeout <= 0 {
		probeTimeout = 5 * time.Second
	}
	return &BridgeProber{
		detector:     detector,
		healthProber: prober,
		probeTimeout: probeTimeout,
	}
}

// DetectStatus queries the target 3P resource via the underlying dynamic StatusDetector.
func (p *BridgeProber) DetectStatus(ctx context.Context, ref WrapperRef) (*ResourceStatus, error) {
	if p.detector == nil {
		return nil, errors.New("status detector is uninitialized")
	}
	return p.detector.DetectStatus(ctx, ref)
}

// Probe executes an active HTTP/TCP health probe against target using the configured probe timeout.
func (p *BridgeProber) Probe(ctx context.Context, target string, timeout time.Duration) (*HealthProbeResult, error) {
	if p.healthProber == nil {
		return nil, errors.New("health prober is uninitialized")
	}
	if timeout <= 0 {
		timeout = p.probeTimeout
	}
	return p.healthProber.Probe(ctx, target, timeout)
}

// ProbeHealth evaluates whether a component is healthy based on its spec and default namespace.
// For 3P wrappers, it queries the status detector and verifies endpoint health via active probing.
// Non-3P components are reported healthy; the Reconciler only relies on this for 1P components when no
// HelmManager is configured, otherwise 1P health comes from HelmManager.ComponentHealth.
func (p *BridgeProber) ProbeHealth(ctx context.Context, spec *componentspb.ComponentSpec, defaultNamespace string) (bool, string) {
	if spec == nil {
		return false, "spec is nil"
	}
	if spec.GetType() != componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_3P_WRAPPER {
		return true, "Component healthy"
	}
	if spec.GetWrapperRef() == nil {
		return p.probeOperatorlessHealth(ctx, spec)
	}
	wRef := ToBridgeWrapperRef(spec.GetWrapperRef(), defaultNamespace)
	if p.detector == nil {
		return false, "status detector is uninitialized"
	}
	res, err := p.detector.DetectStatus(ctx, wRef)
	if err != nil {
		return false, fmt.Sprintf("status detection error: %v", err)
	}
	if res == nil {
		return false, "status detector returned nil status"
	}
	if !res.Found {
		return false, res.Message
	}
	if !res.Ready {
		return false, res.Message
	}
	return p.endpointVerdict(ctx, res.Endpoint, res.Message)
}

func (p *BridgeProber) probeOperatorlessHealth(ctx context.Context, spec *componentspb.ComponentSpec) (bool, string) {
	endpoint := resolveProbeEndpoint(spec.GetParameters())
	if endpoint == "" {
		return false, "no wrapperRef or connection endpoint parameters configured"
	}
	return p.endpointVerdict(ctx, endpoint, "Component healthy")
}

// endpointVerdict probes endpoint and returns the probe's message, falling back to
// "health probe failed" on failure or to healthyMsg on success when the probe reports none.
func (p *BridgeProber) endpointVerdict(ctx context.Context, endpoint, healthyMsg string) (bool, string) {
	ok, probeMsg := p.probe3PEndpoint(ctx, endpoint)
	if probeMsg != "" {
		return ok, probeMsg
	}
	if !ok {
		return false, "health probe failed"
	}
	return true, healthyMsg
}

func (p *BridgeProber) probe3PEndpoint(ctx context.Context, endpoint string) (bool, string) {
	if endpoint == "" || p.healthProber == nil {
		return true, ""
	}
	probeRes, err := p.healthProber.Probe(ctx, endpoint, p.probeTimeout)
	if err != nil {
		return false, fmt.Sprintf("health probe error: %v", err)
	}
	if probeRes == nil {
		return false, "health prober returned nil result"
	}
	if !probeRes.Healthy {
		return false, probeRes.Message
	}
	return true, probeRes.Message
}

func firstNonEmptyToken(val string) string {
	if val == "" {
		return ""
	}
	for _, part := range strings.Split(val, ",") {
		if first := strings.TrimSpace(part); first != "" {
			return first
		}
	}
	return ""
}

func resolveProbeEndpoint(params map[string]string) string {
	if len(params) == 0 {
		return ""
	}
	if ep := firstNonEmptyToken(params["healthCheckEndpoint"]); ep != "" {
		return ep
	}
	return resolveConnectionEndpoint(params)
}

func resolveConnectionEndpoint(params map[string]string) string {
	if len(params) == 0 {
		return ""
	}
	for _, k := range []string{"endpoint", "clusterEndpoints", "brokerEndpoints"} {
		if first := firstNonEmptyToken(params[k]); first != "" {
			return first
		}
	}
	host := firstNonEmptyToken(params["host"])
	if host == "" {
		return ""
	}
	return formatHostEndpoint(host, params)
}

func formatHostEndpoint(host string, params map[string]string) string {
	if strings.Count(host, ":") == 1 || (strings.HasPrefix(host, "[") && strings.Contains(host, "]:")) {
		return host
	}
	for _, pk := range []string{"nativePort", "kafkaPort", "port", "httpPort"} {
		port := strings.TrimSpace(params[pk])
		if port == "" {
			continue
		}
		if strings.Count(host, ":") > 1 && !strings.HasPrefix(host, "[") {
			return fmt.Sprintf("[%s]:%s", host, port)
		}
		return fmt.Sprintf("%s:%s", host, port)
	}
	return host
}
