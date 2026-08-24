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

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	"k8s.io/client-go/restmapper"

	componentspb "xmanager_cloud/api/protos/components/v1alpha1"
)

func newTestScheme() *runtime.Scheme {
	s := runtime.NewScheme()
	_ = metav1.AddMetaToScheme(s)
	return s
}

func makeObj(group, version, kind, ns, name string, gen int64, status map[string]any) *unstructured.Unstructured {
	u := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": group + "/" + version,
		"kind":       kind,
		"metadata":   map[string]any{"name": name, "namespace": ns, "generation": gen},
	}}
	if status != nil {
		u.Object["status"] = status
	}
	return u
}

func TestNormalizeJSONPath(t *testing.T) {
	tests := []struct{ in, want string }{
		{"", ""}, {"   ", ""}, {"status.phase", "{.status.phase}"},
		{".status.phase", "{.status.phase}"}, {"{.status.phase}", "{.status.phase}"},
		{"$status.phase", "{.status.phase}"}, {"[0].name", "{[0].name}"},
		{"status.conditions[?(@.type=='Ready')].status", "{.status.conditions[?(@.type=='Ready')].status}"},
	}
	for _, tt := range tests {
		if got := NormalizeJSONPath(tt.in); got != tt.want {
			t.Errorf("NormalizeJSONPath(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestEvaluateJSONPath(t *testing.T) {
	data := map[string]any{"status": map[string]any{
		"phase":      "Running",
		"conditions": []any{map[string]any{"type": "Ready", "status": "True", "message": "Cluster ready"}},
	}}
	tests := []struct {
		name    string
		data    any
		path    string
		want    string
		wantErr bool
	}{
		{name: "empty data", data: nil, path: "status.phase", want: ""},
		{name: "empty path", data: data, path: "", want: ""},
		{name: "unstructured wrapper", data: &unstructured.Unstructured{Object: data}, path: "status.phase", want: "Running"},
		{name: "nested string", data: data, path: "status.phase", want: "Running"},
		{name: "filter condition status", data: data, path: `status.conditions[?(@.type=="Ready")].status`, want: "True"},
		{name: "missing key", data: data, path: "status.nonexistent", want: ""},
		{name: "invalid path syntax", data: data, path: "{.status[", wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := EvaluateJSONPath(tt.data, tt.path)
			if (err != nil) != tt.wantErr || got != tt.want {
				t.Errorf("EvaluateJSONPath() = (%q, %v), want (%q, wantErr %v)", got, err, tt.want, tt.wantErr)
			}
		})
	}
}

func TestParseGenerationValue(t *testing.T) {
	tests := []struct {
		input   string
		want    int64
		wantErr bool
	}{
		{"", 0, false}, {"0", 0, false}, {"42", 42, false},
		{"42.0", 42, false}, {"abc", 0, true}, {"12.3.4", 0, true},
	}
	for _, tt := range tests {
		got, err := parseGenerationValue(tt.input)
		if (err != nil) != tt.wantErr || got != tt.want {
			t.Errorf("parseGenerationValue(%q) = (%d, %v), want (%d, %v)", tt.input, got, err, tt.want, tt.wantErr)
		}
	}
}

func TestProber_DetectStatus(t *testing.T) {
	ch := makeObj("clickhouse.altinity.com", "v1", "ClickHouseInstallation", "xmc", "my-ch", 4, map[string]any{
		"status": "Completed", "observedGeneration": int64(4),
		"endpoint": "http://my-ch.xmc.svc.cluster.local:8123", "connectionSecret": "my-ch-credentials",
	})
	ray := makeObj("ray.io", "v1", "RayCluster", "xmc", "my-ray", 2, map[string]any{
		"phase": "Running", "conditions": []any{map[string]any{"type": "Ready", "status": "True", "message": "Ray cluster is active"}},
		"url": "http://my-ray-head.xmc:8265",
	})
	unready := makeObj("ray.io", "v1", "RayCluster", "xmc", "unready-ray", 1, map[string]any{
		"phase": "Pending", "conditions": []any{map[string]any{"type": "Ready", "status": "False", "message": "Worker pods pending"}},
	})

	dynClient := dynamicfake.NewSimpleDynamicClient(newTestScheme(), ch, ray, unready)
	rm := restmapper.NewDiscoveryRESTMapper([]*restmapper.APIGroupResources{
		{
			Group:              metav1.APIGroup{Name: "clickhouse.altinity.com", Versions: []metav1.GroupVersionForDiscovery{{GroupVersion: "clickhouse.altinity.com/v1", Version: "v1"}}},
			VersionedResources: map[string][]metav1.APIResource{"v1": {{Name: "clickhouseinstallations", Namespaced: true, Kind: "ClickHouseInstallation"}}},
		},
		{
			Group:              metav1.APIGroup{Name: "ray.io", Versions: []metav1.GroupVersionForDiscovery{{GroupVersion: "ray.io/v1", Version: "v1"}}},
			VersionedResources: map[string][]metav1.APIResource{"v1": {{Name: "rayclusters", Namespaced: true, Kind: "RayCluster"}}},
		},
	})

	prober := New(dynClient, WithRESTMapper(rm), WithDialer(&net.Dialer{}), WithProbeTimeout(2*time.Second))

	tests := []struct {
		name         string
		prober       *Prober
		ref          WrapperRef
		wantFound    bool
		wantReady    bool
		wantGen      int64
		wantEndpoint string
		wantSecret   string
		wantErr      bool
	}{
		{
			name:   "clickhouse custom status and secret path",
			prober: prober,
			ref: WrapperRef{
				Group: "clickhouse.altinity.com", Version: "v1", Kind: "ClickHouseInstallation",
				Namespace: "xmc", Name: "my-ch", ReadyConditionPath: "status.status", ExpectedReadyValue: "Completed",
			},
			wantFound: true, wantReady: true, wantGen: 4,
			wantEndpoint: "http://my-ch.xmc.svc.cluster.local:8123", wantSecret: "my-ch-credentials",
		},
		{
			name:      "ray cluster standard condition path",
			prober:    prober,
			ref:       WrapperRef{Group: "ray.io", Version: "v1", Kind: "RayCluster", Namespace: "xmc", Name: "my-ray"},
			wantFound: true, wantReady: true, wantGen: 2, wantEndpoint: "http://my-ray-head.xmc:8265",
		},
		{
			name:      "unready resource with condition failure message",
			prober:    prober,
			ref:       WrapperRef{Group: "ray.io", Version: "v1", Kind: "RayCluster", Namespace: "xmc", Name: "unready-ray"},
			wantFound: true, wantReady: false, wantGen: 1,
		},
		{
			name:      "resource not found",
			prober:    prober,
			ref:       WrapperRef{Group: "ray.io", Version: "v1", Kind: "RayCluster", Namespace: "xmc", Name: "non-existent"},
			wantFound: false, wantReady: false,
		},
		{
			name:      "missing CRD with RESTMapper NoMatch",
			prober:    prober,
			ref:       WrapperRef{Group: "unknown.io", Version: "v1", Kind: "UnknownResource", Namespace: "xmc", Name: "foo"},
			wantFound: false, wantReady: false,
		},
		{
			name:      "fallback GVR resolution without RESTMapper",
			prober:    New(dynClient),
			ref:       WrapperRef{Group: "ray.io", Version: "v1", Kind: "RayCluster", Namespace: "xmc", Name: "my-ray"},
			wantFound: true, wantReady: true, wantGen: 2,
		},
		{
			name: "uninitialized client error", prober: &Prober{}, ref: WrapperRef{Kind: "K", Name: "N"}, wantErr: true,
		},
		{
			name: "empty kind or name error", prober: prober, ref: WrapperRef{Kind: ""}, wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res, err := tt.prober.DetectStatus(context.Background(), tt.ref)
			if (err != nil) != tt.wantErr {
				t.Fatalf("DetectStatus() error = %v, wantErr %v", err, tt.wantErr)
			}
			if err == nil {
				if res.Found != tt.wantFound || res.Ready != tt.wantReady || (tt.wantGen != 0 && res.ObservedGeneration != tt.wantGen) ||
					(tt.wantEndpoint != "" && res.Endpoint != tt.wantEndpoint) || (tt.wantSecret != "" && res.ConnectionSecret != tt.wantSecret) {
					t.Errorf("DetectStatus() = %+v, want Found=%v Ready=%v Gen=%d Ep=%s Sec=%s",
						res, tt.wantFound, tt.wantReady, tt.wantGen, tt.wantEndpoint, tt.wantSecret)
				}
			}
		})
	}
}

func TestProber_DetectStatus_ExtractionDetails(t *testing.T) {
	c1 := makeObj("custom.io", "v1", "CustomCluster", "xmc", "c1", 10, map[string]any{
		"phase": "Active",
		"conditions": []any{
			map[string]any{"type": "Ready", "status": "False", "message": "Pods starting up"},
			map[string]any{"type": "CustomReady", "status": "Progressing", "message": "Still syncing"},
		},
		"genFloat": float64(12), "genStr": "15", "genInvalid": "invalid-gen",
		"endpoints": []any{"http://c1-service.xmc:8080"}, "secretName": "c1-secret",
	})
	c2 := makeObj("custom.io", "v1", "CustomCluster", "xmc", "c2", 1, map[string]any{"phase": "Ready"})
	c3 := makeObj("custom.io", "v1", "CustomCluster", "xmc", "c3", 1, map[string]any{"url": "http://c3-url.xmc:9000", "auth": map[string]any{"secretName": "c3-auth-sec"}})
	c4 := makeObj("custom.io", "v1", "CustomCluster", "xmc", "c4", 1, map[string]any{"host": "c4-host.xmc"})
	c5 := makeObj("custom.io", "v1", "CustomCluster", "xmc", "c5", 1, map[string]any{"state": "Degraded"})
	c6 := makeObj("custom.io", "v1", "CustomCluster", "xmc", "c6", 1, nil)
	c7 := makeObj("custom.io", "v1", "CustomCluster", "xmc", "c7", 1, map[string]any{
		"genBool": true, "genStrBad": "12.3.4.bad", "head": map[string]any{"serviceName": "c7-head-svc"}, "secret": map[string]any{"name": "c7-sec-name"},
	})
	c8 := makeObj("custom.io", "v1", "CustomCluster", "xmc", "c8", 1, nil)
	c8.Object["status"] = "not-a-map"

	prober := New(dynamicfake.NewSimpleDynamicClient(newTestScheme(), c1, c2, c3, c4, c5, c6, c7, c8))
	ctx := context.Background()

	t.Run("phase fallback active", func(t *testing.T) {
		status, err := prober.DetectStatus(ctx, WrapperRef{Group: "custom.io", Version: "v1", Kind: "CustomCluster", Name: "c2", Namespace: "xmc"})
		if err != nil || !status.Ready || status.Message != "Phase is Ready" {
			t.Errorf("expected phase fallback Ready: status=%+v, err=%v", status, err)
		}
	})

	t.Run("sibling message extraction on custom condition path", func(t *testing.T) {
		status, err := prober.DetectStatus(ctx, WrapperRef{
			Group: "custom.io", Version: "v1", Kind: "CustomCluster", Name: "c1", Namespace: "xmc",
			ReadyConditionPath: "{.status.conditions[1].status}", ExpectedReadyValue: "Synced",
		})
		if err != nil || status.Ready || status.Message != "Still syncing" {
			t.Errorf("expected sibling message 'Still syncing', got %+v", status)
		}
	})

	t.Run("candidate url, host, and auth secret extraction", func(t *testing.T) {
		status3, err := prober.DetectStatus(ctx, WrapperRef{Group: "custom.io", Version: "v1", Kind: "CustomCluster", Name: "c3", Namespace: "xmc"})
		if err != nil || status3.Endpoint != "http://c3-url.xmc:9000" || status3.ConnectionSecret != "c3-auth-sec" {
			t.Errorf("unexpected url/auth extraction: status=%+v, err=%v", status3, err)
		}
		status4, err := prober.DetectStatus(ctx, WrapperRef{Group: "custom.io", Version: "v1", Kind: "CustomCluster", Name: "c4", Namespace: "xmc"})
		if err != nil || status4.Endpoint != "c4-host.xmc" {
			t.Errorf("unexpected host extraction: status=%+v, err=%v", status4, err)
		}
	})

	t.Run("generation parsing variations and errors", func(t *testing.T) {
		statusFloat, _ := prober.DetectStatus(ctx, WrapperRef{Group: "custom.io", Version: "v1", Kind: "CustomCluster", Name: "c1", Namespace: "xmc", ObservedGenerationPath: "status.genFloat"})
		if statusFloat.ObservedGeneration != 12 {
			t.Errorf("expected observedGen 12: got %d", statusFloat.ObservedGeneration)
		}
		statusStr, _ := prober.DetectStatus(ctx, WrapperRef{Group: "custom.io", Version: "v1", Kind: "CustomCluster", Name: "c1", Namespace: "xmc", ObservedGenerationPath: "status.genStr"})
		if statusStr.ObservedGeneration != 15 {
			t.Errorf("expected observedGen 15: got %d", statusStr.ObservedGeneration)
		}
		if _, err := prober.DetectStatus(ctx, WrapperRef{Group: "custom.io", Version: "v1", Kind: "CustomCluster", Name: "c7", Namespace: "xmc", ObservedGenerationPath: "status.genBool"}); err == nil {
			t.Error("expected error for bool generation")
		}
		if _, err := prober.DetectStatus(ctx, WrapperRef{Group: "custom.io", Version: "v1", Kind: "CustomCluster", Name: "c7", Namespace: "xmc", ObservedGenerationPath: "status.genStrBad"}); err == nil {
			t.Error("expected error for invalid string generation")
		}
		if _, err := prober.DetectStatus(ctx, WrapperRef{Group: "custom.io", Version: "v1", Kind: "CustomCluster", Name: "c7", Namespace: "xmc", ObservedGenerationPath: "status.gen["}); err == nil {
			t.Error("expected error for invalid generation JSONPath")
		}
	})

	t.Run("custom paths and non-map status", func(t *testing.T) {
		status7, err := prober.DetectStatus(ctx, WrapperRef{
			Group: "custom.io", Version: "v1", Kind: "CustomCluster", Name: "c7", Namespace: "xmc",
			EndpointJSONPath: "status.head.serviceName", ConnectionSecretJSONPath: "status.secret.name",
		})
		if err != nil || status7.Endpoint != "c7-head-svc" || status7.ConnectionSecret != "c7-sec-name" {
			t.Errorf("unexpected head/secret extraction: status=%+v, err=%v", status7, err)
		}
		status8, err := prober.DetectStatus(ctx, WrapperRef{Group: "custom.io", Version: "v1", Kind: "CustomCluster", Name: "c8", Namespace: "xmc"})
		if err != nil || status8.Ready {
			t.Errorf("unexpected status for non-map status: status=%+v, err=%v", status8, err)
		}
		status5, _ := prober.DetectStatus(ctx, WrapperRef{Group: "custom.io", Version: "v1", Kind: "CustomCluster", Name: "c5", Namespace: "xmc", ReadyConditionPath: "status.state", ExpectedReadyValue: "Healthy"})
		if status5.Ready || status5.Message != `Expected ready value "Healthy", got "Degraded"` {
			t.Errorf("unexpected mismatch status: status=%+v", status5)
		}
		status6, _ := prober.DetectStatus(ctx, WrapperRef{Group: "custom.io", Version: "v1", Kind: "CustomCluster", Name: "c6", Namespace: "xmc"})
		if status6.Ready || status6.Message != "Readiness status not reported" {
			t.Errorf("unexpected empty status: status=%+v", status6)
		}
	})

	t.Run("invalid jsonpath query errors", func(t *testing.T) {
		for _, wRef := range []WrapperRef{
			{Group: "custom.io", Version: "v1", Kind: "CustomCluster", Name: "c1", Namespace: "xmc", ReadyConditionPath: "{.status["},
			{Group: "custom.io", Version: "v1", Kind: "CustomCluster", Name: "c1", Namespace: "xmc", EndpointJSONPath: "{.status["},
			{Group: "custom.io", Version: "v1", Kind: "CustomCluster", Name: "c1", Namespace: "xmc", ConnectionSecretJSONPath: "{.status["},
		} {
			if _, err := prober.DetectStatus(ctx, wRef); err == nil {
				t.Errorf("expected error for invalid query path in %+v", wRef)
			}
		}
	})
}

func TestProber_ProbeHTTP_And_TCP(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/health" {
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write([]byte("OK"))
			return
		}
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer ts.Close()

	prober := New(dynamicfake.NewSimpleDynamicClient(newTestScheme()), WithHTTPClient(ts.Client()), WithProbeTimeout(2*time.Second))
	ctx := context.Background()

	t.Run("HTTP healthy and unhealthy", func(t *testing.T) {
		res, err := prober.ProbeHTTP(ctx, ts.URL+"/health", 2*time.Second)
		if err != nil || !res.Healthy || res.StatusCode != http.StatusOK {
			t.Fatalf("expected healthy HTTP probe: res=%+v, err=%v", res, err)
		}
		resErr, err := prober.ProbeHTTP(ctx, ts.URL+"/error", 2*time.Second)
		if err != nil || resErr.Healthy || resErr.StatusCode != http.StatusInternalServerError {
			t.Fatalf("expected unhealthy HTTP probe: res=%+v, err=%v", resErr, err)
		}
	})

	t.Run("HTTP error paths", func(t *testing.T) {
		if _, err := prober.ProbeHTTP(ctx, "", 0); err == nil {
			t.Error("expected error on empty URL")
		}
		if _, err := prober.ProbeHTTP(ctx, "://bad", 0); err == nil {
			t.Error("expected error on invalid URL")
		}
		canceledCtx, cancel := context.WithCancel(ctx)
		cancel()
		if _, err := prober.ProbeHTTP(canceledCtx, ts.URL+"/health", time.Second); err == nil {
			t.Error("expected error on canceled context")
		}
		resFail, err := prober.ProbeHTTP(ctx, "http://127.0.0.1:1", time.Second)
		if err != nil || resFail.Healthy {
			t.Errorf("expected failed HTTP probe: res=%+v, err=%v", resFail, err)
		}
	})

	t.Run("TCP healthy and error paths", func(t *testing.T) {
		ln, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatalf("failed to listen: %v", err)
		}
		defer ln.Close()

		res, err := prober.ProbeTCP(ctx, ln.Addr().String(), 2*time.Second)
		if err != nil || !res.Healthy {
			t.Fatalf("expected healthy TCP probe: res=%+v, err=%v", res, err)
		}
		resPrefix, err := prober.Probe(ctx, "tcp://"+ln.Addr().String(), 2*time.Second)
		if err != nil || !resPrefix.Healthy {
			t.Fatalf("expected healthy TCP probe with prefix: res=%+v, err=%v", resPrefix, err)
		}
		if _, err := prober.ProbeTCP(ctx, "", 0); err == nil {
			t.Error("expected error on empty address")
		}
		if _, err := prober.ProbeTCP(ctx, "invalid-no-port", 0); err == nil {
			t.Error("expected error on invalid address")
		}
		canceledCtx, cancel := context.WithCancel(ctx)
		cancel()
		if _, err := prober.ProbeTCP(canceledCtx, "127.0.0.1:8080", time.Second); err == nil {
			t.Error("expected error on canceled context")
		}
		resFail, err := prober.ProbeTCP(ctx, "127.0.0.1:1", time.Second)
		if err != nil || resFail.Healthy {
			t.Errorf("expected failed TCP probe: res=%+v, err=%v", resFail, err)
		}
	})

	t.Run("Probe protocol routing", func(t *testing.T) {
		resHTTP, err := prober.Probe(ctx, ts.URL+"/health", time.Second)
		if err != nil || !resHTTP.Healthy {
			t.Errorf("Probe() HTTP routing failed: res=%+v, err=%v", resHTTP, err)
		}
		resTCP, err := prober.Probe(ctx, "127.0.0.1:1", time.Second)
		if err != nil || resTCP.Healthy {
			t.Errorf("Probe() TCP routing failed: res=%+v, err=%v", resTCP, err)
		}
	})
}

func TestProber_DefaultProberHTTP_TCP(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) }))
	defer ts.Close()

	p := &Prober{}
	ctx := context.Background()

	resHTTP, err := p.ProbeHTTP(ctx, ts.URL, 0)
	if err != nil || !resHTTP.Healthy {
		t.Errorf("expected healthy default ProbeHTTP: res=%+v, err=%v", resHTTP, err)
	}
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to listen: %v", err)
	}
	defer ln.Close()

	resTCP, err := p.ProbeTCP(ctx, ln.Addr().String(), 0)
	if err != nil || !resTCP.Healthy {
		t.Errorf("expected healthy default ProbeTCP: res=%+v, err=%v", resTCP, err)
	}
}

func TestProber_ProbeHealth(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusOK) }))
	defer ts.Close()

	ch1 := makeObj("clickhouse.altinity.com", "v1", "ClickHouseInstallation", "xmc", "ch1", 1, map[string]any{
		"conditions": []any{map[string]any{"type": "Ready", "status": "True"}}, "endpoint": ts.URL,
	})
	chNoEp := makeObj("clickhouse.altinity.com", "v1", "ClickHouseInstallation", "xmc", "ch-no-ep", 1, map[string]any{
		"conditions": []any{map[string]any{"type": "Ready", "status": "True"}},
	})
	chUnready := makeObj("clickhouse.altinity.com", "v1", "ClickHouseInstallation", "xmc", "ch-unready", 1, map[string]any{
		"conditions": []any{map[string]any{"type": "Ready", "status": "False", "message": "Cluster installing"}},
	})
	chFail := makeObj("clickhouse.altinity.com", "v1", "ClickHouseInstallation", "xmc", "ch-fail", 1, map[string]any{
		"conditions": []any{map[string]any{"type": "Ready", "status": "True"}}, "endpoint": "http://127.0.0.1:1",
	})

	prober := New(dynamicfake.NewSimpleDynamicClient(newTestScheme(), ch1, chNoEp, chUnready, chFail), WithHTTPClient(ts.Client()))
	ctx := context.Background()

	if ok, _ := prober.ProbeHealth(ctx, nil); ok {
		t.Error("expected false for nil spec")
	}
	if ok, msg := prober.ProbeHealth(ctx, &componentspb.ComponentSpec{Type: componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_1P_COMPONENT}); !ok || msg != "Component healthy" {
		t.Errorf("unexpected outcome for 1P spec: ok=%v, msg=%s", ok, msg)
	}
	if ok, _ := prober.ProbeHealth(ctx, &componentspb.ComponentSpec{Type: componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_3P_WRAPPER}); !ok {
		t.Error("expected true when wrapperRef is nil")
	}
	make3PSpec := func(name string) *componentspb.ComponentSpec {
		return &componentspb.ComponentSpec{
			Type:       componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_3P_WRAPPER,
			WrapperRef: &componentspb.WrapperRef{CrdGroup: "clickhouse.altinity.com", CrdVersion: "v1", CrdKind: "ClickHouseInstallation", TargetName: name, TargetNamespace: "xmc"},
		}
	}
	if ok, _ := prober.ProbeHealth(ctx, make3PSpec("ch1")); !ok {
		t.Error("expected healthy 3P probe")
	}
	if ok, _ := prober.ProbeHealth(ctx, make3PSpec("ch-no-ep")); !ok {
		t.Error("expected healthy 3P probe without endpoint")
	}
	if ok, _ := prober.ProbeHealth(ctx, make3PSpec("ch-unready")); ok {
		t.Error("expected unhealthy 3P probe for unready resource")
	}
	if ok, _ := prober.ProbeHealth(ctx, make3PSpec("ch-fail")); ok {
		t.Error("expected unhealthy 3P probe for failing endpoint")
	}
	if ok, _ := (&Prober{}).ProbeHealth(ctx, make3PSpec("ch1")); ok {
		t.Error("expected error for uninitialized prober")
	}
}
