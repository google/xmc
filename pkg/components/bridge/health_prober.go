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

// Package bridge implements active network health probing and coordinate
// resolution for XMC components.
package bridge

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	componentspb "xmanager_cloud/api/protos/components/v1alpha1"
)

const (
	defaultHealthProbeTimeout = 5 * time.Second
)

// HealthProbeResult contains health probe telemetry and outcome.
type HealthProbeResult struct {
	// Healthy indicates whether the network probe succeeded (HTTP 2xx/3xx or TCP connection established).
	Healthy bool `json:"healthy"`
	// StatusCode is the HTTP response status code (if an HTTP probe was executed).
	StatusCode int `json:"statusCode,omitempty"`
	// Latency is the measured round-trip probe execution time.
	Latency time.Duration `json:"latency,omitempty"`
	// Message is a human-readable diagnostic message describing probe outcome.
	Message string `json:"message,omitempty"`
}

// HealthProber performs active HTTP and TCP network probes against component endpoints.
type HealthProber interface {
	// Probe dispatches a health probe to target, automatically inferring HTTP vs TCP based on protocol prefix.
	Probe(ctx context.Context, target string, timeout time.Duration) (*HealthProbeResult, error)
	// ProbeHTTP executes an HTTP GET health check against targetURL.
	ProbeHTTP(ctx context.Context, targetURL string, timeout time.Duration) (*HealthProbeResult, error)
	// ProbeTCP verifies TCP network socket establishment against address (host:port).
	ProbeTCP(ctx context.Context, address string, timeout time.Duration) (*HealthProbeResult, error)
}

// NetworkHealthProber implements HealthProber for HTTP and TCP network connectivity.
type NetworkHealthProber struct {
	httpClient   *http.Client
	dialer       *net.Dialer
	probeTimeout time.Duration
}

var _ HealthProber = (*NetworkHealthProber)(nil)

// HealthProberOption configures a NetworkHealthProber instance.
type HealthProberOption func(*NetworkHealthProber)

// WithHealthHTTPClient sets a custom HTTP client for HTTP health probing.
func WithHealthHTTPClient(c *http.Client) HealthProberOption {
	return func(p *NetworkHealthProber) {
		if p != nil && c != nil {
			p.httpClient = c
		}
	}
}

// WithHealthDialer sets a custom net.Dialer for TCP health probing.
func WithHealthDialer(d *net.Dialer) HealthProberOption {
	return func(p *NetworkHealthProber) {
		if p != nil && d != nil {
			p.dialer = d
		}
	}
}

// WithHealthProbeTimeout sets the default probe timeout duration.
func WithHealthProbeTimeout(t time.Duration) HealthProberOption {
	return func(p *NetworkHealthProber) {
		if p != nil && t > 0 {
			p.probeTimeout = t
		}
	}
}

// NewNetworkHealthProber creates a new NetworkHealthProber instance with optional configurations.
func NewNetworkHealthProber(opts ...HealthProberOption) *NetworkHealthProber {
	p := &NetworkHealthProber{
		httpClient:   &http.Client{},
		dialer:       &net.Dialer{},
		probeTimeout: defaultHealthProbeTimeout,
	}
	for _, opt := range opts {
		if opt != nil {
			opt(p)
		}
	}
	return p
}

// Probe dispatches a health probe by protocol prefix (http://, https://, tcp://, or bare host:port).
func (p *NetworkHealthProber) Probe(ctx context.Context, target string, timeout time.Duration) (*HealthProbeResult, error) {
	if p == nil {
		return nil, errors.New("network health prober is nil")
	}
	trimmed := strings.TrimSpace(target)
	lower := strings.ToLower(trimmed)
	if strings.HasPrefix(lower, "http://") || strings.HasPrefix(lower, "https://") {
		return p.ProbeHTTP(ctx, trimmed, timeout)
	}
	tcpTrimmed := trimmed
	if strings.HasPrefix(lower, "tcp://") {
		tcpTrimmed = trimmed[6:]
	}
	return p.ProbeTCP(ctx, tcpTrimmed, timeout)
}

// ProbeHTTP executes a bounded HTTP GET probe against targetURL, reading up to 64KB of the response body.
func (p *NetworkHealthProber) ProbeHTTP(ctx context.Context, targetURL string, timeout time.Duration) (*HealthProbeResult, error) {
	if p == nil {
		return nil, errors.New("network health prober is nil")
	}
	if strings.TrimSpace(targetURL) == "" {
		return nil, errors.New("targetURL must not be empty")
	}
	if _, err := url.ParseRequestURI(targetURL); err != nil {
		return nil, fmt.Errorf("invalid probe URL %q: %w", targetURL, err)
	}
	if timeout <= 0 {
		timeout = p.probeTimeout
		if timeout <= 0 {
			timeout = defaultHealthProbeTimeout
		}
	}
	probeCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	req, err := http.NewRequestWithContext(probeCtx, http.MethodGet, targetURL, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create probe request: %w", err)
	}
	client := p.httpClient
	if client == nil {
		client = http.DefaultClient
	}
	start := time.Now()
	resp, err := client.Do(req)
	latency := time.Since(start)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return &HealthProbeResult{Healthy: false, Latency: latency, Message: fmt.Sprintf("HTTP probe failed: %v", err)}, nil
	}
	defer resp.Body.Close()
	if _, err := io.Copy(io.Discard, io.LimitReader(resp.Body, 64*1024)); err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return &HealthProbeResult{Healthy: false, Latency: time.Since(start), Message: fmt.Sprintf("HTTP body read failed: %v", err)}, nil
	}
	healthy := resp.StatusCode >= http.StatusOK && resp.StatusCode < http.StatusBadRequest
	msg := fmt.Sprintf("HTTP %d", resp.StatusCode)
	if !healthy {
		msg = fmt.Sprintf("HTTP %d (unhealthy status)", resp.StatusCode)
	}
	return &HealthProbeResult{Healthy: healthy, StatusCode: resp.StatusCode, Latency: time.Since(start), Message: msg}, nil
}

// ProbeTCP tests TCP network socket connectivity to address (host:port).
func (p *NetworkHealthProber) ProbeTCP(ctx context.Context, address string, timeout time.Duration) (*HealthProbeResult, error) {
	if p == nil {
		return nil, errors.New("network health prober is nil")
	}
	trimmed := strings.TrimSpace(address)
	if len(trimmed) >= 6 && strings.EqualFold(trimmed[:6], "tcp://") {
		tcpCandidate := trimmed[6:]
		if _, _, err := net.SplitHostPort(tcpCandidate); err == nil {
			trimmed = tcpCandidate
		}
	}
	if trimmed == "" {
		return nil, errors.New("address must not be empty")
	}
	if _, _, err := net.SplitHostPort(trimmed); err != nil {
		return nil, fmt.Errorf("invalid TCP address %q: %w", address, err)
	}
	if timeout <= 0 {
		timeout = p.probeTimeout
		if timeout <= 0 {
			timeout = defaultHealthProbeTimeout
		}
	}
	probeCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	dialer := p.dialer
	if dialer == nil {
		dialer = &net.Dialer{}
	}
	start := time.Now()
	conn, err := dialer.DialContext(probeCtx, "tcp", trimmed)
	latency := time.Since(start)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return &HealthProbeResult{Healthy: false, Latency: latency, Message: fmt.Sprintf("TCP dial failed: %v", err)}, nil
	}
	_ = conn.Close()
	return &HealthProbeResult{Healthy: true, Latency: latency, Message: "TCP dial succeeded"}, nil
}

// ParseCoordinates extracts scheme, host, port, service name, and namespace from an endpoint string.
func ParseCoordinates(endpoint, defaultNamespace string) *componentspb.ResolvedComponentCoordinates {
	trimmed := strings.TrimSpace(endpoint)
	if trimmed == "" {
		return nil
	}

	scheme, hostPort := extractSchemeAndHost(trimmed)
	host, port := extractHostAndPort(scheme, hostPort)
	svcName, ns := parseServiceAndNamespace(host, defaultNamespace)

	return &componentspb.ResolvedComponentCoordinates{
		Scheme:      scheme,
		Host:        host,
		Port:        port,
		ServiceName: svcName,
		Namespace:   ns,
	}
}

// extractSchemeAndHost separates the URI scheme from the host and port string.
func extractSchemeAndHost(endpoint string) (string, string) {
	if strings.Contains(endpoint, "://") {
		if u, err := url.Parse(endpoint); err == nil {
			scheme := "http"
			if u.Scheme != "" {
				scheme = u.Scheme
			}
			return scheme, u.Host
		}
	}
	if strings.Contains(endpoint, "/") {
		if u, err := url.Parse("http://" + endpoint); err == nil && u.Host != "" {
			return "http", u.Host
		}
	}
	return "http", endpoint
}

// extractHostAndPort separates host and port, handling default HTTP/HTTPS ports (80/443) and IPv6 brackets.
func extractHostAndPort(scheme, hostPort string) (string, uint32) {
	host, portStr, err := net.SplitHostPort(hostPort)
	if err != nil {
		host = hostPort
		if strings.HasPrefix(host, "[") && strings.HasSuffix(host, "]") {
			host = host[1 : len(host)-1]
		}
		switch scheme {
		case "https":
			portStr = "443"
		case "http":
			portStr = "80"
		}
	}

	var port uint32
	if p, err := strconv.ParseUint(portStr, 10, 32); err == nil && p <= 65535 {
		port = uint32(p)
	}
	return host, port
}

// parseServiceAndNamespace extracts service name and namespace from Kubernetes FQDNs (svc.namespace or svc.namespace.svc.cluster.local).
func parseServiceAndNamespace(host, defaultNamespace string) (string, string) {
	if ip := net.ParseIP(host); ip != nil {
		return "", defaultNamespace
	}
	host = strings.TrimSuffix(host, ".")
	parts := strings.Split(host, ".")
	if len(parts) >= 3 && parts[2] == "svc" {
		return parts[0], parts[1]
	}
	if len(parts) >= 2 && parts[1] != "svc" && parts[1] != "" {
		return parts[0], parts[1]
	}
	return parts[0], defaultNamespace
}

// FakeHealthProber is an in-memory test double for HealthProber.
type FakeHealthProber struct {
	mu       sync.Mutex
	Results  map[string]*HealthProbeResult
	ProbeErr error
}

var _ HealthProber = (*FakeHealthProber)(nil)

// NewFakeHealthProber creates a new FakeHealthProber.
func NewFakeHealthProber() *FakeHealthProber {
	return &FakeHealthProber{
		Results: make(map[string]*HealthProbeResult),
	}
}

// SetResult records the probe result for a given target.
func (f *FakeHealthProber) SetResult(target string, res *HealthProbeResult) {
	if f == nil {
		return
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.Results == nil {
		f.Results = make(map[string]*HealthProbeResult)
	}
	f.Results[target] = res
}

// Probe returns the preconfigured probe result for target.
func (f *FakeHealthProber) Probe(ctx context.Context, target string, timeout time.Duration) (*HealthProbeResult, error) {
	if f == nil {
		return nil, errors.New("fake health prober is nil")
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.ProbeErr != nil {
		return nil, f.ProbeErr
	}
	if res, ok := f.Results[target]; ok {
		return res, nil
	}
	return &HealthProbeResult{Healthy: true, Message: "Probe succeeded"}, nil
}

// ProbeHTTP delegates to Probe.
func (f *FakeHealthProber) ProbeHTTP(ctx context.Context, targetURL string, timeout time.Duration) (*HealthProbeResult, error) {
	return f.Probe(ctx, targetURL, timeout)
}

// ProbeTCP delegates to Probe.
func (f *FakeHealthProber) ProbeTCP(ctx context.Context, address string, timeout time.Duration) (*HealthProbeResult, error) {
	return f.Probe(ctx, address, timeout)
}
