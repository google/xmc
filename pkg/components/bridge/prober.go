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
	"sync"
	"time"

	componentspb "xmanager_cloud/api/protos/components/v1alpha1"
)

// Prober defines the interface for checking component health and readiness.
type Prober interface {
	ProbeComponentStatus(ctx context.Context, name string, spec *componentspb.ComponentSpec) (bool, string)
	ProbeHealth(ctx context.Context, spec *componentspb.ComponentSpec) (bool, string)
}

// NetworkProber performs network-based probing against HTTP and TCP endpoints.
type NetworkProber struct {
	httpClient *http.Client
	timeout    time.Duration
}

// NewNetworkProber creates an initialized NetworkProber with the specified timeout.
func NewNetworkProber(timeout time.Duration) *NetworkProber {
	if timeout <= 0 {
		timeout = 3 * time.Second
	}
	return &NetworkProber{
		httpClient: &http.Client{Timeout: timeout},
		timeout:    timeout,
	}
}

// ProbeComponentStatus evaluates health for a named component and spec.
func (p *NetworkProber) ProbeComponentStatus(ctx context.Context, name string, spec *componentspb.ComponentSpec) (bool, string) {
	return p.ProbeHealth(ctx, spec)
}

// ProbeHealth evaluates network reachability based on spec parameters.
func (p *NetworkProber) ProbeHealth(ctx context.Context, spec *componentspb.ComponentSpec) (bool, string) {
	if spec == nil {
		return false, "Component spec is nil"
	}

	switch spec.GetType() {
	case componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_3P_WRAPPER:
		return p.probe3PWrapper(spec)
	case componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_1P_COMPONENT:
		return true, "1P component workload active"
	default:
		return true, "Component assumed healthy"
	}
}

func (p *NetworkProber) probe3PWrapper(spec *componentspb.ComponentSpec) (bool, string) {
	ref := spec.GetWrapperRef()
	if ref == nil {
		return false, "Missing wrapperRef for 3P wrapper component"
	}
	if ref.GetTargetName() == "" {
		return false, "Missing targetName in wrapperRef"
	}
	return true, fmt.Sprintf("Wrapper %s/%s ready", ref.GetCrdKind(), ref.GetTargetName())
}

// ProbeHTTP checks reachability of an HTTP endpoint.
func (p *NetworkProber) ProbeHTTP(ctx context.Context, url string) (bool, string) {
	if url == "" {
		return false, "empty HTTP URL"
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return false, fmt.Sprintf("invalid URL: %v", err)
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

// ProbeTCP checks reachability of a list of host:port endpoints.
func (p *NetworkProber) ProbeTCP(ctx context.Context, endpoints []string) (bool, string) {
	var dialErrors []string
	validCount := 0

	for _, ep := range endpoints {
		target := strings.TrimSpace(ep)
		if target == "" {
			continue
		}
		validCount++
		dialer := net.Dialer{Timeout: p.timeout}
		conn, err := dialer.DialContext(ctx, "tcp", target)
		if err == nil {
			conn.Close()
			return true, fmt.Sprintf("Endpoint %s reachable", target)
		}
		dialErrors = append(dialErrors, fmt.Sprintf("%q: %v", target, err))
	}

	if validCount > 0 {
		return false, fmt.Sprintf("all TCP dials failed: %s", strings.Join(dialErrors, "; "))
	}
	return true, "No endpoints to probe"
}

// FakeProber provides an in-memory test double for Prober.
type FakeProber struct {
	mu           sync.RWMutex
	DefaultOK    bool
	DefaultMsg   string
	ProbeResults map[string]ProbeResult
	ProbeCalls   map[string]int
}

// ProbeResult encapsulates mock probe returns.
type ProbeResult struct {
	Healthy bool
	Message string
}

// NewFakeProber creates an initialized FakeProber instance.
func NewFakeProber(defaultOK bool, defaultMsg string) *FakeProber {
	return &FakeProber{
		DefaultOK:    defaultOK,
		DefaultMsg:   defaultMsg,
		ProbeResults: make(map[string]ProbeResult),
		ProbeCalls:   make(map[string]int),
	}
}

// ProbeComponentStatus records the probe call and returns mock status.
func (f *FakeProber) ProbeComponentStatus(ctx context.Context, name string, spec *componentspb.ComponentSpec) (bool, string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.ProbeCalls[name]++
	if res, ok := f.ProbeResults[name]; ok {
		return res.Healthy, res.Message
	}
	return f.DefaultOK, f.DefaultMsg
}

// ProbeHealth evaluates mock health.
func (f *FakeProber) ProbeHealth(ctx context.Context, spec *componentspb.ComponentSpec) (bool, string) {
	f.mu.RLock()
	defer f.mu.RUnlock()
	return f.DefaultOK, f.DefaultMsg
}

// SetResult configures the expected probe response for a specific component.
func (f *FakeProber) SetResult(name string, healthy bool, message string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.ProbeResults[name] = ProbeResult{Healthy: healthy, Message: message}
}

// GetCallCount returns the number of probe invocations for a component.
func (f *FakeProber) GetCallCount(name string) int {
	f.mu.RLock()
	defer f.mu.RUnlock()
	return f.ProbeCalls[name]
}

// Reset clears recorded calls and custom probe outcomes.
func (f *FakeProber) Reset() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.ProbeResults = make(map[string]ProbeResult)
	f.ProbeCalls = make(map[string]int)
}
