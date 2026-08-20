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

// Package bridge implements dynamic 3P status detection, JSONPath evaluation,
// and active network health probing for XMC components.
package bridge

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/util/jsonpath"
)

const (
	defaultProbeTimeout = 5 * time.Second
	defaultReadyValue   = "True"
)

// WrapperRef configures reference metadata and JSONPath queries for third-party CRDs.
type WrapperRef struct {
	Group                    string `json:"group,omitempty"`
	Version                  string `json:"version,omitempty"`
	Kind                     string `json:"kind,omitempty"`
	Name                     string `json:"name,omitempty"`
	Namespace                string `json:"namespace,omitempty"`
	ObservedGenerationPath   string `json:"observedGenerationPath,omitempty"`
	ReadyConditionPath       string `json:"readyConditionPath,omitempty"`
	ExpectedReadyValue       string `json:"expectedReadyValue,omitempty"`
	EndpointJSONPath         string `json:"endpointJsonPath,omitempty"`
	ConnectionSecretJSONPath string `json:"connectionSecretJsonPath,omitempty"`
}

// ResourceStatus holds the detected runtime status extracted from a dynamic 3P resource.
type ResourceStatus struct {
	Found              bool           `json:"found"`
	Ready              bool           `json:"ready"`
	ObservedGeneration int64          `json:"observedGeneration,omitempty"`
	Endpoint           string         `json:"endpoint,omitempty"`
	ConnectionSecret   string         `json:"connectionSecret,omitempty"`
	Message            string         `json:"message,omitempty"`
	RawStatus          map[string]any `json:"rawStatus,omitempty"`
}

// HealthProbeResult contains health probe telemetry and outcome.
type HealthProbeResult struct {
	Healthy    bool          `json:"healthy"`
	StatusCode int           `json:"statusCode,omitempty"`
	Latency    time.Duration `json:"latency,omitempty"`
	Message    string        `json:"message,omitempty"`
}

// StatusDetector inspects third-party Kubernetes CRD status dynamically.
type StatusDetector interface {
	DetectStatus(ctx context.Context, ref WrapperRef) (*ResourceStatus, error)
}

// HealthProber performs active HTTP and TCP network probes.
type HealthProber interface {
	ProbeHTTP(ctx context.Context, targetURL string, timeout time.Duration) (*HealthProbeResult, error)
	ProbeTCP(ctx context.Context, address string, timeout time.Duration) (*HealthProbeResult, error)
	Probe(ctx context.Context, target string, timeout time.Duration) (*HealthProbeResult, error)
}

// Prober implements StatusDetector and HealthProber for dynamic 3P component integration.
type Prober struct {
	dynClient  dynamic.Interface
	restMapper meta.RESTMapper
	httpClient *http.Client
	dialer     *net.Dialer
}

// Option configures a Prober instance.
type Option func(*Prober)

// WithRESTMapper sets the RESTMapper used for GVK-to-GVR mapping.
// For Kubernetes CRDs with non-standard pluralizations (e.g. storageclasses, networkpolicies),
// configuring a dynamic discovery RESTMapper is recommended.
func WithRESTMapper(mapper meta.RESTMapper) Option {
	return func(p *Prober) {
		p.restMapper = mapper
	}
}

// WithHTTPClient sets a custom HTTP client for HTTP health probing.
func WithHTTPClient(client *http.Client) Option {
	return func(p *Prober) {
		if client != nil {
			p.httpClient = client
		}
	}
}

// WithDialer sets a custom net.Dialer for TCP health probing.
func WithDialer(dialer *net.Dialer) Option {
	return func(p *Prober) {
		if dialer != nil {
			p.dialer = dialer
		}
	}
}

// New creates a new Prober with the supplied dynamic client and options.
func New(dynClient dynamic.Interface, opts ...Option) *Prober {
	p := &Prober{
		dynClient:  dynClient,
		httpClient: &http.Client{},
		dialer:     &net.Dialer{},
	}
	for _, opt := range opts {
		opt(p)
	}
	return p
}

// NormalizeJSONPath converts various JSONPath formats into client-go JSONPath template syntax: {.path}.
func NormalizeJSONPath(path string) string {
	trimmed := strings.TrimSpace(path)
	if trimmed == "" {
		return ""
	}

	if strings.HasPrefix(trimmed, "{") && strings.HasSuffix(trimmed, "}") {
		trimmed = strings.TrimSpace(trimmed[1 : len(trimmed)-1])
	}

	if strings.HasPrefix(trimmed, "$") {
		trimmed = strings.TrimPrefix(trimmed, "$")
	}

	if !strings.HasPrefix(trimmed, ".") && !strings.HasPrefix(trimmed, "[") {
		trimmed = "." + trimmed
	}

	return fmt.Sprintf("{%s}", trimmed)
}

// EvaluateJSONPath parses and executes a JSONPath expression against the provided data object.
func EvaluateJSONPath(data any, path string) (string, error) {
	if data == nil || strings.TrimSpace(path) == "" {
		return "", nil
	}

	if u, ok := data.(*unstructured.Unstructured); ok {
		data = u.UnstructuredContent()
	}

	normPath := NormalizeJSONPath(path)

	jp := jsonpath.New("detector").AllowMissingKeys(true)
	if err := jp.Parse(normPath); err != nil {
		return "", fmt.Errorf("invalid jsonpath expression %q: %w", path, err)
	}

	var buf bytes.Buffer
	if err := jp.Execute(&buf, data); err != nil {
		return "", fmt.Errorf("failed to execute jsonpath %q: %w", path, err)
	}

	return strings.TrimSpace(buf.String()), nil
}

// DetectStatus queries the target 3P resource via the dynamic client and evaluates its status.
func (p *Prober) DetectStatus(ctx context.Context, ref WrapperRef) (*ResourceStatus, error) {
	if p.dynClient == nil {
		return nil, errors.New("dynamic client is uninitialized")
	}
	if ref.Name == "" || ref.Kind == "" {
		return nil, errors.New("wrapperRef name and kind are required")
	}

	gvr, err := p.resolveGVR(ref)
	if err != nil {
		if meta.IsNoMatchError(err) {
			return &ResourceStatus{
				Found:   false,
				Ready:   false,
				Message: fmt.Sprintf("CRD not installed: %v", err),
			}, nil
		}
		return nil, fmt.Errorf("failed to resolve GVR: %w", err)
	}

	obj, err := p.fetchResource(ctx, gvr, ref.Namespace, ref.Name)
	if err != nil {
		if apierrors.IsNotFound(err) {
			return &ResourceStatus{
				Found:   false,
				Ready:   false,
				Message: fmt.Sprintf("resource %s/%s not found", ref.Namespace, ref.Name),
			}, nil
		}
		if meta.IsNoMatchError(err) {
			return &ResourceStatus{
				Found:   false,
				Ready:   false,
				Message: fmt.Sprintf("CRD not installed: %v", err),
			}, nil
		}
		return nil, fmt.Errorf("failed to fetch dynamic resource: %w", err)
	}

	return p.extractResourceStatus(obj, ref)
}

func (p *Prober) resolveGVR(ref WrapperRef) (schema.GroupVersionResource, error) {
	if p.restMapper != nil {
		gvk := schema.GroupVersionKind{
			Group:   ref.Group,
			Version: ref.Version,
			Kind:    ref.Kind,
		}
		mapping, err := p.restMapper.RESTMapping(gvk.GroupKind(), gvk.Version)
		if err != nil {
			return schema.GroupVersionResource{}, err
		}
		return mapping.Resource, nil
	}

	resource := strings.ToLower(ref.Kind)
	if !strings.HasSuffix(resource, "s") {
		resource += "s"
	}
	return schema.GroupVersionResource{
		Group:    ref.Group,
		Version:  ref.Version,
		Resource: resource,
	}, nil
}

func (p *Prober) fetchResource(ctx context.Context, gvr schema.GroupVersionResource, ns, name string) (*unstructured.Unstructured, error) {
	if ns != "" {
		return p.dynClient.Resource(gvr).Namespace(ns).Get(ctx, name, metav1.GetOptions{})
	}
	return p.dynClient.Resource(gvr).Get(ctx, name, metav1.GetOptions{})
}

func (p *Prober) extractResourceStatus(obj *unstructured.Unstructured, ref WrapperRef) (*ResourceStatus, error) {
	rawStatus, _, _ := unstructured.NestedMap(obj.Object, "status")

	obsGen, err := p.extractObservedGeneration(obj, ref.ObservedGenerationPath)
	if err != nil {
		return nil, fmt.Errorf("failed to extract observedGeneration: %w", err)
	}

	ready, readyMsg, err := p.evaluateReadiness(obj, ref.ReadyConditionPath, ref.ExpectedReadyValue)
	if err != nil {
		return nil, fmt.Errorf("failed to evaluate readiness: %w", err)
	}

	endpoint, err := p.extractEndpoint(obj, ref.EndpointJSONPath)
	if err != nil {
		return nil, fmt.Errorf("failed to extract endpoint: %w", err)
	}

	secret, err := p.extractConnectionSecret(obj, ref.ConnectionSecretJSONPath)
	if err != nil {
		return nil, fmt.Errorf("failed to extract connection secret: %w", err)
	}

	msg := readyMsg
	if msg == "" {
		if ready {
			msg = "Resource is ready"
		} else {
			msg = "Resource is not ready"
		}
	}

	return &ResourceStatus{
		Found:              true,
		Ready:              ready,
		ObservedGeneration: obsGen,
		Endpoint:           endpoint,
		ConnectionSecret:   secret,
		Message:            msg,
		RawStatus:          rawStatus,
	}, nil
}

func (p *Prober) extractObservedGeneration(obj *unstructured.Unstructured, path string) (int64, error) {
	if path != "" {
		val, err := EvaluateJSONPath(obj.Object, path)
		if err != nil {
			return 0, err
		}
		if val != "" {
			return strconv.ParseInt(val, 10, 64)
		}
	}

	if obsGen, found, _ := unstructured.NestedInt64(obj.Object, "status", "observedGeneration"); found {
		return obsGen, nil
	}

	return obj.GetGeneration(), nil
}

func (p *Prober) evaluateReadiness(obj *unstructured.Unstructured, path, expectedValue string) (bool, string, error) {
	expected := expectedValue
	if expected == "" {
		expected = defaultReadyValue
	}

	targetPath := path
	if targetPath == "" {
		targetPath = "{.status.conditions[?(@.type==\"Ready\")].status}"
	}

	val, err := EvaluateJSONPath(obj.Object, targetPath)
	if err != nil {
		return false, "", err
	}

	if val == "" {
		return p.fallbackPhaseReadiness(obj, path)
	}

	if strings.EqualFold(strings.TrimSpace(val), strings.TrimSpace(expected)) {
		return true, "Readiness condition satisfied", nil
	}

	return false, p.extractConditionFailureMessage(obj, path, expected, val), nil
}

func (p *Prober) fallbackPhaseReadiness(obj *unstructured.Unstructured, path string) (bool, string, error) {
	if path == "" {
		phase, found, _ := unstructured.NestedString(obj.Object, "status", "phase")
		if found && (strings.EqualFold(phase, "Running") || strings.EqualFold(phase, "Ready") || strings.EqualFold(phase, "Active")) {
			return true, fmt.Sprintf("Phase is %s", phase), nil
		}
	}
	return false, "Readiness status not reported", nil
}

func deriveSiblingMessagePath(path string) string {
	if path == "" {
		return ""
	}
	trimmed := strings.TrimSuffix(strings.TrimSpace(path), "}")
	const statusSuffix = ".status"
	if !strings.HasSuffix(trimmed, statusSuffix) {
		return ""
	}
	msgPath := trimmed[:len(trimmed)-len(statusSuffix)] + ".message"
	if strings.HasSuffix(strings.TrimSpace(path), "}") {
		msgPath += "}"
	}
	return msgPath
}

func (p *Prober) extractConditionFailureMessage(obj *unstructured.Unstructured, path, expected, val string) string {
	if msgPath := deriveSiblingMessagePath(path); msgPath != "" {
		if msg, err := EvaluateJSONPath(obj.Object, msgPath); err == nil && msg != "" {
			return msg
		}
	}

	if condMsg, _ := EvaluateJSONPath(obj.Object, "{.status.conditions[?(@.type==\"Ready\")].message}"); condMsg != "" {
		return condMsg
	}

	return fmt.Sprintf("Expected ready value %q, got %q", expected, val)
}

func (p *Prober) extractEndpoint(obj *unstructured.Unstructured, path string) (string, error) {
	if path != "" {
		val, err := EvaluateJSONPath(obj.Object, path)
		if err != nil {
			return "", err
		}
		return strings.TrimSpace(val), nil
	}

	for _, candidatePath := range []string{
		"{.status.endpoint}",
		"{.status.endpoints[0]}",
		"{.status.url}",
		"{.status.host}",
	} {
		val, err := EvaluateJSONPath(obj.Object, candidatePath)
		if err == nil && val != "" {
			return strings.TrimSpace(val), nil
		}
	}
	return "", nil
}

func (p *Prober) extractConnectionSecret(obj *unstructured.Unstructured, path string) (string, error) {
	if path != "" {
		val, err := EvaluateJSONPath(obj.Object, path)
		if err != nil {
			return "", err
		}
		return strings.TrimSpace(val), nil
	}

	for _, candidatePath := range []string{
		"{.status.connectionSecret}",
		"{.status.secretName}",
		"{.status.auth.secretName}",
	} {
		val, err := EvaluateJSONPath(obj.Object, candidatePath)
		if err == nil && val != "" {
			return strings.TrimSpace(val), nil
		}
	}
	return "", nil
}

// ProbeHTTP performs an HTTP GET health probe against targetURL.
func (p *Prober) ProbeHTTP(ctx context.Context, targetURL string, timeout time.Duration) (*HealthProbeResult, error) {
	if strings.TrimSpace(targetURL) == "" {
		return nil, errors.New("target URL must not be empty")
	}

	parsedURL, err := url.Parse(targetURL)
	if err != nil || parsedURL.Scheme == "" || parsedURL.Host == "" {
		return nil, fmt.Errorf("invalid probe URL %q: %w", targetURL, err)
	}

	if timeout <= 0 {
		timeout = defaultProbeTimeout
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
		return &HealthProbeResult{
			Healthy: false,
			Latency: latency,
			Message: fmt.Sprintf("HTTP probe failed: %v", err),
		}, nil
	}
	defer resp.Body.Close()
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64*1024))

	healthy := resp.StatusCode >= http.StatusOK && resp.StatusCode < http.StatusBadRequest
	msg := fmt.Sprintf("HTTP %d", resp.StatusCode)
	if !healthy {
		msg = fmt.Sprintf("HTTP %d (unhealthy status)", resp.StatusCode)
	}

	return &HealthProbeResult{
		Healthy:    healthy,
		StatusCode: resp.StatusCode,
		Latency:    latency,
		Message:    msg,
	}, nil
}

// ProbeTCP tests TCP connectivity to address (host:port).
func (p *Prober) ProbeTCP(ctx context.Context, address string, timeout time.Duration) (*HealthProbeResult, error) {
	trimmed := strings.TrimPrefix(address, "tcp://")
	if strings.TrimSpace(trimmed) == "" {
		return nil, errors.New("address must not be empty")
	}

	if _, _, err := net.SplitHostPort(trimmed); err != nil {
		return nil, fmt.Errorf("invalid TCP address %q: %w", address, err)
	}

	if timeout <= 0 {
		timeout = defaultProbeTimeout
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
		return &HealthProbeResult{
			Healthy: false,
			Latency: latency,
			Message: fmt.Sprintf("TCP dial failed: %v", err),
		}, nil
	}
	_ = conn.Close()

	return &HealthProbeResult{
		Healthy: true,
		Latency: latency,
		Message: fmt.Sprintf("TCP connect to %s succeeded", trimmed),
	}, nil
}

// Probe determines the target protocol (HTTP or TCP) and performs the appropriate health probe.
func (p *Prober) Probe(ctx context.Context, target string, timeout time.Duration) (*HealthProbeResult, error) {
	trimmed := strings.TrimSpace(target)
	if strings.HasPrefix(trimmed, "http://") || strings.HasPrefix(trimmed, "https://") {
		return p.ProbeHTTP(ctx, trimmed, timeout)
	}
	return p.ProbeTCP(ctx, trimmed, timeout)
}
