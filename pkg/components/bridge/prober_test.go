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
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	dynamicfake "k8s.io/client-go/dynamic/fake"
)

func TestNormalizeJSONPath(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "empty path",
			input:    "",
			expected: "",
		},
		{
			name:     "whitespace only",
			input:    "   ",
			expected: "",
		},
		{
			name:     "already bracketed dotted path",
			input:    "{.status.phase}",
			expected: "{.status.phase}",
		},
		{
			name:     "dotted path without brackets",
			input:    ".status.phase",
			expected: "{.status.phase}",
		},
		{
			name:     "path without leading dot",
			input:    "status.phase",
			expected: "{.status.phase}",
		},
		{
			name:     "dollar root prefix",
			input:    "$.status.phase",
			expected: "{.status.phase}",
		},
		{
			name:     "bracketed dollar root prefix",
			input:    "{$.status.phase}",
			expected: "{.status.phase}",
		},
		{
			name:     "filter query with double quotes",
			input:    `$.status.conditions[?(@.type=="Ready")].status`,
			expected: `{.status.conditions[?(@.type=="Ready")].status}`,
		},
		{
			name:     "filter query already bracketed",
			input:    `{.status.conditions[?(@.type=='Ready')].status}`,
			expected: `{.status.conditions[?(@.type=='Ready')].status}`,
		},
		{
			name:     "array index expression",
			input:    "$[0].endpoint",
			expected: "{[0].endpoint}",
		},
		{
			name:     "bracketed array index expression",
			input:    "[0].endpoint",
			expected: "{[0].endpoint}",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := NormalizeJSONPath(tc.input)
			if got != tc.expected {
				t.Errorf("NormalizeJSONPath(%q) = %q, want %q", tc.input, got, tc.expected)
			}
		})
	}
}

func TestEvaluateJSONPath(t *testing.T) {
	data := map[string]any{
		"status": map[string]any{
			"phase":              "Running",
			"observedGeneration": int64(42),
			"endpoint":           "http://my-service.default.svc:8080",
			"endpoints": []any{
				"http://node1:8080",
				"http://node2:8080",
			},
			"conditions": []any{
				map[string]any{
					"type":    "Initialized",
					"status":  "True",
					"message": "Pod initialized",
				},
				map[string]any{
					"type":    "Ready",
					"status":  "True",
					"message": "Pod ready",
				},
			},
		},
	}

	tests := []struct {
		name     string
		path     string
		expected string
		wantErr  bool
	}{
		{
			name:     "direct field path",
			path:     ".status.phase",
			expected: "Running",
		},
		{
			name:     "dollar prefix field path",
			path:     "$.status.endpoint",
			expected: "http://my-service.default.svc:8080",
		},
		{
			name:     "array index element",
			path:     ".status.endpoints[0]",
			expected: "http://node1:8080",
		},
		{
			name:     "condition filter query",
			path:     `$.status.conditions[?(@.type=="Ready")].status`,
			expected: "True",
		},
		{
			name:     "missing key returns empty string without error",
			path:     ".status.nonExistentField",
			expected: "",
			wantErr:  false,
		},
		{
			name:     "empty path returns empty string",
			path:     "",
			expected: "",
			wantErr:  false,
		},
		{
			name:    "malformed jsonpath expression returns error",
			path:    "{.status[",
			wantErr: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, err := EvaluateJSONPath(data, tc.path)
			if (err != nil) != tc.wantErr {
				t.Fatalf("EvaluateJSONPath() err = %v, wantErr = %v", err, tc.wantErr)
			}
			if !tc.wantErr && got != tc.expected {
				t.Errorf("EvaluateJSONPath() = %q, want %q", got, tc.expected)
			}
		})
	}

	// Test passing *unstructured.Unstructured
	u := &unstructured.Unstructured{Object: data}
	got, err := EvaluateJSONPath(u, ".status.phase")
	if err != nil {
		t.Fatalf("EvaluateJSONPath(unstructured) unexpected error: %v", err)
	}
	if got != "Running" {
		t.Errorf("EvaluateJSONPath(unstructured) = %q, want %q", got, "Running")
	}
}

func newTestUnstructured(apiVersion, kind, ns, name string, generation int64, status map[string]any) *unstructured.Unstructured {
	obj := &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": apiVersion,
			"kind":       kind,
			"metadata": map[string]any{
				"name":       name,
				"namespace":  ns,
				"generation": generation,
			},
		},
	}
	if status != nil {
		obj.Object["status"] = status
	}
	return obj
}

func TestDetectStatus_SuccessCases(t *testing.T) {
	scheme := runtime.NewScheme()

	jobsetObj := newTestUnstructured(
		"jobset.x-k8s.io/v1alpha2",
		"JobSet",
		"xmc-system",
		"test-jobset",
		2,
		map[string]any{
			"observedGeneration": int64(2),
			"conditions": []any{
				map[string]any{
					"type":               "Ready",
					"status":             "True",
					"reason":             "AllReplicasReady",
					"message":            "All replicas are operational",
					"lastTransitionTime": "2026-08-20T12:00:00Z",
				},
			},
			"endpoint":         "http://test-jobset-leader.xmc-system:8080",
			"connectionSecret": "test-jobset-secret",
		},
	)

	clickhouseObj := newTestUnstructured(
		"clickhouse.altinity.com/v1",
		"ClickHouseInstallation",
		"clickhouse",
		"test-chi",
		5,
		map[string]any{
			"observedGeneration": int64(5),
			"status":             "Completed",
			"endpoint":           "http://clickhouse-chi.clickhouse:8123",
			"auth": map[string]any{
				"secretName": "chi-credentials",
			},
		},
	)

	rayClusterObj := newTestUnstructured(
		"ray.io/v1",
		"RayCluster",
		"ray-ns",
		"test-ray",
		1,
		map[string]any{
			"phase": "Running",
			"endpoints": []any{
				"http://ray-head.ray-ns:8265",
			},
		},
	)

	notReadyObj := newTestUnstructured(
		"apps/v1",
		"Deployment",
		"default",
		"unready-app",
		3,
		map[string]any{
			"observedGeneration": int64(3),
			"conditions": []any{
				map[string]any{
					"type":    "Ready",
					"status":  "False",
					"message": "Containers are in CrashLoopBackOff",
				},
			},
		},
	)

	dynClient := dynamicfake.NewSimpleDynamicClient(scheme, jobsetObj, clickhouseObj, rayClusterObj, notReadyObj)
	prober := New(dynClient)
	ctx := context.Background()

	t.Run("JobSet ready with condition and endpoint", func(t *testing.T) {
		ref := WrapperRef{
			Group:     "jobset.x-k8s.io",
			Version:   "v1alpha2",
			Kind:      "JobSet",
			Name:      "test-jobset",
			Namespace: "xmc-system",
		}

		res, err := prober.DetectStatus(ctx, ref)
		if err != nil {
			t.Fatalf("DetectStatus unexpected error: %v", err)
		}
		if !res.Found {
			t.Errorf("Expected Found = true")
		}
		if !res.Ready {
			t.Errorf("Expected Ready = true")
		}
		if res.ObservedGeneration != 2 {
			t.Errorf("ObservedGeneration = %d, want 2", res.ObservedGeneration)
		}
		if res.Endpoint != "http://test-jobset-leader.xmc-system:8080" {
			t.Errorf("Endpoint = %q, want %q", res.Endpoint, "http://test-jobset-leader.xmc-system:8080")
		}
		if res.ConnectionSecret != "test-jobset-secret" {
			t.Errorf("ConnectionSecret = %q, want %q", res.ConnectionSecret, "test-jobset-secret")
		}
	})

	t.Run("ClickHouse custom status field and secret path", func(t *testing.T) {
		ref := WrapperRef{
			Group:                    "clickhouse.altinity.com",
			Version:                  "v1",
			Kind:                     "ClickHouseInstallation",
			Name:                     "test-chi",
			Namespace:                "clickhouse",
			ObservedGenerationPath:   "$.status.observedGeneration",
			ReadyConditionPath:       "$.status.status",
			ExpectedReadyValue:       "Completed",
			EndpointJSONPath:         "$.status.endpoint",
			ConnectionSecretJSONPath: "$.status.auth.secretName",
		}

		res, err := prober.DetectStatus(ctx, ref)
		if err != nil {
			t.Fatalf("DetectStatus unexpected error: %v", err)
		}
		if !res.Found || !res.Ready {
			t.Errorf("Expected Found = true, Ready = true, got Found=%v, Ready=%v", res.Found, res.Ready)
		}
		if res.ObservedGeneration != 5 {
			t.Errorf("ObservedGeneration = %d, want 5", res.ObservedGeneration)
		}
		if res.Endpoint != "http://clickhouse-chi.clickhouse:8123" {
			t.Errorf("Endpoint = %q, want %q", res.Endpoint, "http://clickhouse-chi.clickhouse:8123")
		}
		if res.ConnectionSecret != "chi-credentials" {
			t.Errorf("ConnectionSecret = %q, want %q", res.ConnectionSecret, "chi-credentials")
		}
	})

	t.Run("RayCluster fallback to status.phase and endpoints array", func(t *testing.T) {
		ref := WrapperRef{
			Group:     "ray.io",
			Version:   "v1",
			Kind:      "RayCluster",
			Name:      "test-ray",
			Namespace: "ray-ns",
		}

		res, err := prober.DetectStatus(ctx, ref)
		if err != nil {
			t.Fatalf("DetectStatus unexpected error: %v", err)
		}
		if !res.Found || !res.Ready {
			t.Errorf("Expected Found = true, Ready = true")
		}
		if res.Endpoint != "http://ray-head.ray-ns:8265" {
			t.Errorf("Endpoint = %q, want %q", res.Endpoint, "http://ray-head.ray-ns:8265")
		}
	})

	t.Run("Deployment unready condition extraction", func(t *testing.T) {
		ref := WrapperRef{
			Group:     "apps",
			Version:   "v1",
			Kind:      "Deployment",
			Name:      "unready-app",
			Namespace: "default",
		}

		res, err := prober.DetectStatus(ctx, ref)
		if err != nil {
			t.Fatalf("DetectStatus unexpected error: %v", err)
		}
		if !res.Found {
			t.Errorf("Expected Found = true")
		}
		if res.Ready {
			t.Errorf("Expected Ready = false")
		}
		if res.Message != "Containers are in CrashLoopBackOff" {
			t.Errorf("Message = %q, want %q", res.Message, "Containers are in CrashLoopBackOff")
		}
	})
}

func TestDetectStatus_ErrorAndEdgeCases(t *testing.T) {
	scheme := runtime.NewScheme()
	dynClient := dynamicfake.NewSimpleDynamicClient(scheme)
	prober := New(dynClient)
	ctx := context.Background()

	t.Run("Resource not found", func(t *testing.T) {
		ref := WrapperRef{
			Group:     "jobset.x-k8s.io",
			Version:   "v1alpha2",
			Kind:      "JobSet",
			Name:      "non-existent-resource",
			Namespace: "default",
		}

		res, err := prober.DetectStatus(ctx, ref)
		if err != nil {
			t.Fatalf("Expected nil error for NotFound, got: %v", err)
		}
		if res.Found {
			t.Errorf("Expected Found = false")
		}
		if res.Ready {
			t.Errorf("Expected Ready = false")
		}
	})

	t.Run("Missing CRD / NoMatchError via RESTMapper", func(t *testing.T) {
		mapper := meta.NewDefaultRESTMapper([]schema.GroupVersion{})
		// Mapper has no registered kinds, so it returns NoKindMatchError
		p := New(dynClient, WithRESTMapper(mapper))

		ref := WrapperRef{
			Group:     "unknown.crd.io",
			Version:   "v1",
			Kind:      "UnknownResource",
			Name:      "instance-1",
			Namespace: "default",
		}

		res, err := p.DetectStatus(ctx, ref)
		if err != nil {
			t.Fatalf("Expected nil error for NoMatchError, got: %v", err)
		}
		if res.Found {
			t.Errorf("Expected Found = false")
		}
		if res.Ready {
			t.Errorf("Expected Ready = false")
		}
	})

	t.Run("Missing wrapperRef Name or Kind returns error", func(t *testing.T) {
		_, err := prober.DetectStatus(ctx, WrapperRef{Name: ""})
		if err == nil {
			t.Errorf("Expected error for missing Name and Kind")
		}

		_, err = prober.DetectStatus(ctx, WrapperRef{Name: "foo", Kind: ""})
		if err == nil {
			t.Errorf("Expected error for missing Kind")
		}
	})

	t.Run("Uninitialized dynamic client returns error", func(t *testing.T) {
		p := New(nil)
		_, err := p.DetectStatus(ctx, WrapperRef{Name: "test", Kind: "JobSet"})
		if err == nil {
			t.Errorf("Expected error for uninitialized dynamic client")
		}
	})

	t.Run("Malformed JSONPath in observedGeneration returns error", func(t *testing.T) {
		obj := newTestUnstructured("v1", "ConfigMap", "default", "cm-1", 1, map[string]any{})
		c := dynamicfake.NewSimpleDynamicClient(scheme, obj)
		p := New(c)

		ref := WrapperRef{
			Group:                  "",
			Version:                "v1",
			Kind:                   "ConfigMap",
			Name:                   "cm-1",
			Namespace:              "default",
			ObservedGenerationPath: "{.status[",
		}

		_, err := p.DetectStatus(ctx, ref)
		if err == nil {
			t.Errorf("Expected error for malformed ObservedGenerationPath")
		}
	})

	t.Run("Malformed JSONPath in readyCondition returns error", func(t *testing.T) {
		obj := newTestUnstructured("v1", "ConfigMap", "default", "cm-2", 1, map[string]any{})
		c := dynamicfake.NewSimpleDynamicClient(scheme, obj)
		p := New(c)

		ref := WrapperRef{
			Group:              "",
			Version:            "v1",
			Kind:               "ConfigMap",
			Name:               "cm-2",
			Namespace:          "default",
			ReadyConditionPath: "{.status[",
		}

		_, err := p.DetectStatus(ctx, ref)
		if err == nil {
			t.Errorf("Expected error for malformed ReadyConditionPath")
		}
	})

	t.Run("Malformed JSONPath in endpoint returns error", func(t *testing.T) {
		obj := newTestUnstructured("v1", "ConfigMap", "default", "cm-3", 1, map[string]any{})
		c := dynamicfake.NewSimpleDynamicClient(scheme, obj)
		p := New(c)

		ref := WrapperRef{
			Group:            "",
			Version:          "v1",
			Kind:             "ConfigMap",
			Name:             "cm-3",
			Namespace:        "default",
			EndpointJSONPath: "{.status[",
		}

		_, err := p.DetectStatus(ctx, ref)
		if err == nil {
			t.Errorf("Expected error for malformed EndpointJSONPath")
		}
	})

	t.Run("Malformed JSONPath in connectionSecret returns error", func(t *testing.T) {
		obj := newTestUnstructured("v1", "ConfigMap", "default", "cm-4", 1, map[string]any{})
		c := dynamicfake.NewSimpleDynamicClient(scheme, obj)
		p := New(c)

		ref := WrapperRef{
			Group:                    "",
			Version:                  "v1",
			Kind:                     "ConfigMap",
			Name:                     "cm-4",
			Namespace:                "default",
			ConnectionSecretJSONPath: "{.status[",
		}

		_, err := p.DetectStatus(ctx, ref)
		if err == nil {
			t.Errorf("Expected error for malformed ConnectionSecretJSONPath")
		}
	})

	t.Run("Cluster-scoped resource lookup", func(t *testing.T) {
		nodeObj := newTestUnstructured("v1", "Node", "", "node-1", 1, map[string]any{
			"phase": "Running",
		})
		c := dynamicfake.NewSimpleDynamicClient(scheme, nodeObj)
		p := New(c)

		ref := WrapperRef{
			Group:     "",
			Version:   "v1",
			Kind:      "Node",
			Name:      "node-1",
			Namespace: "",
		}

		res, err := p.DetectStatus(ctx, ref)
		if err != nil {
			t.Fatalf("DetectStatus unexpected error: %v", err)
		}
		if !res.Found || !res.Ready {
			t.Errorf("Expected Found=true, Ready=true, got Found=%v, Ready=%v", res.Found, res.Ready)
		}
	})

	t.Run("Successful RESTMapper resolution", func(t *testing.T) {
		mapper := meta.NewDefaultRESTMapper([]schema.GroupVersion{{Group: "xmc.dev", Version: "v1alpha1"}})
		mapper.Add(schema.GroupVersionKind{Group: "xmc.dev", Version: "v1alpha1", Kind: "CustomTarget"}, meta.RESTScopeNamespace)

		obj := newTestUnstructured("xmc.dev/v1alpha1", "CustomTarget", "default", "custom-1", 1, map[string]any{
			"observedGeneration": int64(1),
			"conditions": []any{
				map[string]any{
					"type":   "Ready",
					"status": "True",
				},
			},
		})
		c := dynamicfake.NewSimpleDynamicClient(scheme, obj)
		p := New(c, WithRESTMapper(mapper))

		ref := WrapperRef{
			Group:     "xmc.dev",
			Version:   "v1alpha1",
			Kind:      "CustomTarget",
			Name:      "custom-1",
			Namespace: "default",
		}

		res, err := p.DetectStatus(ctx, ref)
		if err != nil {
			t.Fatalf("DetectStatus unexpected error: %v", err)
		}
		if !res.Found || !res.Ready {
			t.Errorf("Expected Found=true, Ready=true")
		}
	})
}

func TestHealthProber_HTTP(t *testing.T) {
	p := New(nil)
	ctx := context.Background()

	t.Run("HTTP 200 OK with default timeout", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("ok"))
		}))
		defer srv.Close()

		res, err := p.ProbeHTTP(ctx, srv.URL, 0)
		if err != nil {
			t.Fatalf("ProbeHTTP unexpected error: %v", err)
		}
		if !res.Healthy {
			t.Errorf("Expected Healthy = true, got false (msg: %s)", res.Message)
		}
		if res.StatusCode != http.StatusOK {
			t.Errorf("StatusCode = %d, want %d", res.StatusCode, http.StatusOK)
		}
	})

	t.Run("HTTP 503 Service Unavailable", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusServiceUnavailable)
		}))
		defer srv.Close()

		res, err := p.ProbeHTTP(ctx, srv.URL, 2*time.Second)
		if err != nil {
			t.Fatalf("ProbeHTTP unexpected error: %v", err)
		}
		if res.Healthy {
			t.Errorf("Expected Healthy = false, got true")
		}
		if res.StatusCode != http.StatusServiceUnavailable {
			t.Errorf("StatusCode = %d, want %d", res.StatusCode, http.StatusServiceUnavailable)
		}
	})

	t.Run("Unreachable HTTP host", func(t *testing.T) {
		res, err := p.ProbeHTTP(ctx, "http://127.0.0.1:54321/unreachable", 100*time.Millisecond)
		if err != nil {
			t.Fatalf("Expected nil error with failed probe result, got: %v", err)
		}
		if res.Healthy {
			t.Errorf("Expected Healthy = false for unreachable host")
		}
	})

	t.Run("Invalid URL format", func(t *testing.T) {
		_, err := p.ProbeHTTP(ctx, "not-a-valid-url", 1*time.Second)
		if err == nil {
			t.Errorf("Expected error for invalid URL")
		}

		_, err = p.ProbeHTTP(ctx, "", 1*time.Second)
		if err == nil {
			t.Errorf("Expected error for empty URL")
		}
	})
}

func TestHealthProber_TCP(t *testing.T) {
	p := New(nil)
	ctx := context.Background()

	t.Run("TCP Connect Success with default timeout", func(t *testing.T) {
		l, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatalf("net.Listen error: %v", err)
		}
		defer l.Close()

		res, err := p.ProbeTCP(ctx, l.Addr().String(), 0)
		if err != nil {
			t.Fatalf("ProbeTCP unexpected error: %v", err)
		}
		if !res.Healthy {
			t.Errorf("Expected Healthy = true, got false (%s)", res.Message)
		}
	})

	t.Run("TCP Address with prefix", func(t *testing.T) {
		l, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatalf("net.Listen error: %v", err)
		}
		defer l.Close()

		res, err := p.ProbeTCP(ctx, "tcp://"+l.Addr().String(), 2*time.Second)
		if err != nil {
			t.Fatalf("ProbeTCP unexpected error: %v", err)
		}
		if !res.Healthy {
			t.Errorf("Expected Healthy = true, got false")
		}
	})

	t.Run("TCP Dial Failure", func(t *testing.T) {
		res, err := p.ProbeTCP(ctx, "127.0.0.1:54321", 100*time.Millisecond)
		if err != nil {
			t.Fatalf("Expected nil error with failed probe result, got: %v", err)
		}
		if res.Healthy {
			t.Errorf("Expected Healthy = false for closed port")
		}
	})

	t.Run("Invalid TCP Address", func(t *testing.T) {
		_, err := p.ProbeTCP(ctx, "invalid-host-without-port", 1*time.Second)
		if err == nil {
			t.Errorf("Expected error for missing port")
		}

		_, err = p.ProbeTCP(ctx, "", 1*time.Second)
		if err == nil {
			t.Errorf("Expected error for empty address")
		}
	})
}

func TestHealthProber_ProbeDispatcher(t *testing.T) {
	p := New(nil)
	ctx := context.Background()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	httpRes, err := p.Probe(ctx, srv.URL, 2*time.Second)
	if err != nil || !httpRes.Healthy {
		t.Errorf("Probe(HTTP) failed: res=%v, err=%v", httpRes, err)
	}

	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("net.Listen error: %v", err)
	}
	defer l.Close()

	tcpRes, err := p.Probe(ctx, l.Addr().String(), 2*time.Second)
	if err != nil || !tcpRes.Healthy {
		t.Errorf("Probe(TCP) failed: res=%v, err=%v", tcpRes, err)
	}
}

func TestProber_Options(t *testing.T) {
	customClient := &http.Client{Timeout: 1 * time.Second}
	customDialer := &net.Dialer{Timeout: 1 * time.Second}
	mapper := meta.NewDefaultRESTMapper([]schema.GroupVersion{})

	p := New(nil,
		WithHTTPClient(customClient),
		WithDialer(customDialer),
		WithRESTMapper(mapper),
	)

	if p.httpClient != customClient {
		t.Errorf("httpClient pointer = %p, want %p", p.httpClient, customClient)
	}
	if p.dialer != customDialer {
		t.Errorf("dialer pointer = %p, want %p", p.dialer, customDialer)
	}
	if p.restMapper != mapper {
		t.Errorf("restMapper not set correctly")
	}
}

func TestHealthProber_ContextCancellation(t *testing.T) {
	p := &Prober{} // nil httpClient and dialer to test defensive fallbacks

	t.Run("HTTP Probe Parent Context Cancelled", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel() // cancel immediately

		_, err := p.ProbeHTTP(ctx, "http://127.0.0.1:8080/health", 1*time.Second)
		if err == nil {
			t.Fatalf("Expected non-nil error on cancelled context")
		}
	})

	t.Run("TCP Probe Parent Context Cancelled", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel() // cancel immediately

		_, err := p.ProbeTCP(ctx, "127.0.0.1:8080", 1*time.Second)
		if err == nil {
			t.Fatalf("Expected non-nil error on cancelled context")
		}
	})
}

func TestDetectStatus_ConditionWithoutMessage(t *testing.T) {
	scheme := runtime.NewScheme()
	obj := newTestUnstructured(
		"apps/v1",
		"StatefulSet",
		"default",
		"sts-1",
		1,
		map[string]any{
			"conditions": []any{
				map[string]any{
					"type":   "Ready",
					"status": "False",
				},
			},
		},
	)

	dynClient := dynamicfake.NewSimpleDynamicClient(scheme, obj)
	p := New(dynClient)

	ref := WrapperRef{
		Group:     "apps",
		Version:   "v1",
		Kind:      "StatefulSet",
		Name:      "sts-1",
		Namespace: "default",
	}

	res, err := p.DetectStatus(context.Background(), ref)
	if err != nil {
		t.Fatalf("DetectStatus unexpected error: %v", err)
	}
	if res.Ready {
		t.Errorf("Expected Ready = false")
	}
	if res.Message != `Expected ready value "True", got "False"` {
		t.Errorf("Message = %q, want %q", res.Message, `Expected ready value "True", got "False"`)
	}
}
