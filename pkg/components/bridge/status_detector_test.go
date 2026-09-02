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
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	"k8s.io/client-go/restmapper"

	componentspb "xmanager_cloud/api/protos/components/v1alpha1"
)

type statusTestStringer struct {
	val string
}

func (s statusTestStringer) String() string {
	return s.val
}

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
	var nilUnstructured *unstructured.Unstructured
	tests := []struct {
		name    string
		data    any
		path    string
		want    string
		wantErr bool
	}{
		{name: "empty data", data: nil, path: "status.phase", want: ""},
		{name: "typed nil unstructured pointer", data: nilUnstructured, path: "status.phase", want: ""},
		{name: "empty path", data: data, path: "", want: ""},
		{name: "unstructured wrapper", data: &unstructured.Unstructured{Object: data}, path: "status.phase", want: "Running"},
		{name: "nested string", data: data, path: "status.phase", want: "Running"},
		{name: "index condition status", data: data, path: "status.conditions[0].status", want: "True"},
		{name: "missing key", data: data, path: "status.nonexistent", want: ""},
		{name: "invalid path syntax", data: data, path: "{.status[", wantErr: true},
		{name: "cached expression evaluation", data: data, path: "status.phase", want: "Running"},
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

func TestStatusDetector_DetectStatus(t *testing.T) {
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
	clusterObj := makeObj("infra.io", "v1", "ClusterPool", "", "global-pool", 1, map[string]any{
		"phase": "Ready",
	})

	dynClient := dynamicfake.NewSimpleDynamicClient(newTestScheme(), ch, ray, unready, clusterObj)
	rm := restmapper.NewDiscoveryRESTMapper([]*restmapper.APIGroupResources{
		{
			Group:              metav1.APIGroup{Name: "clickhouse.altinity.com", Versions: []metav1.GroupVersionForDiscovery{{GroupVersion: "clickhouse.altinity.com/v1", Version: "v1"}}},
			VersionedResources: map[string][]metav1.APIResource{"v1": {{Name: "clickhouseinstallations", Namespaced: true, Kind: "ClickHouseInstallation"}}},
		},
		{
			Group:              metav1.APIGroup{Name: "ray.io", Versions: []metav1.GroupVersionForDiscovery{{GroupVersion: "ray.io/v1", Version: "v1"}}},
			VersionedResources: map[string][]metav1.APIResource{"v1": {{Name: "rayclusters", Namespaced: true, Kind: "RayCluster"}}},
		},
		{
			Group:              metav1.APIGroup{Name: "infra.io", Versions: []metav1.GroupVersionForDiscovery{{GroupVersion: "infra.io/v1", Version: "v1"}}},
			VersionedResources: map[string][]metav1.APIResource{"v1": {{Name: "clusterpools", Namespaced: false, Kind: "ClusterPool"}}},
		},
	})

	detector := NewK8sStatusDetector(dynClient, rm)

	tests := []struct {
		name         string
		detector     *K8sStatusDetector
		ref          WrapperRef
		wantFound    bool
		wantReady    bool
		wantGen      int64
		wantEndpoint string
		wantSecret   string
		wantErr      bool
	}{
		{
			name:     "clickhouse custom status and secret path",
			detector: detector,
			ref: WrapperRef{
				Group: "clickhouse.altinity.com", Version: "v1", Kind: "ClickHouseInstallation",
				Namespace: "xmc", Name: "my-ch", ReadyConditionPath: "status.status", ExpectedReadyValue: "Completed",
			},
			wantFound: true, wantReady: true, wantGen: 4,
			wantEndpoint: "http://my-ch.xmc.svc.cluster.local:8123", wantSecret: "my-ch-credentials",
		},
		{
			name:      "ray cluster standard condition path",
			detector:  detector,
			ref:       WrapperRef{Group: "ray.io", Version: "v1", Kind: "RayCluster", Namespace: "xmc", Name: "my-ray"},
			wantFound: true, wantReady: true, wantGen: 2, wantEndpoint: "http://my-ray-head.xmc:8265",
		},
		{
			name:      "cluster scoped resource with namespace ignored",
			detector:  detector,
			ref:       WrapperRef{Group: "infra.io", Version: "v1", Kind: "ClusterPool", Namespace: "xmc", Name: "global-pool"},
			wantFound: true, wantReady: true, wantGen: 1,
		},
		{
			name:      "unready resource with condition failure message",
			detector:  detector,
			ref:       WrapperRef{Group: "ray.io", Version: "v1", Kind: "RayCluster", Namespace: "xmc", Name: "unready-ray"},
			wantFound: true, wantReady: false, wantGen: 1,
		},
		{
			name:      "resource not found",
			detector:  detector,
			ref:       WrapperRef{Group: "ray.io", Version: "v1", Kind: "RayCluster", Namespace: "xmc", Name: "non-existent"},
			wantFound: false, wantReady: false,
		},
		{
			name:      "missing CRD with RESTMapper NoMatch",
			detector:  detector,
			ref:       WrapperRef{Group: "unknown.io", Version: "v1", Kind: "UnknownResource", Namespace: "xmc", Name: "foo"},
			wantFound: false, wantReady: false,
		},
		{
			name:      "fallback GVR resolution without RESTMapper",
			detector:  NewK8sStatusDetector(dynClient, nil),
			ref:       WrapperRef{Group: "ray.io", Version: "v1", Kind: "RayCluster", Namespace: "xmc", Name: "my-ray"},
			wantFound: true, wantReady: true, wantGen: 2,
		},
		{
			name: "uninitialized client error", detector: &K8sStatusDetector{}, ref: WrapperRef{Kind: "K", Name: "N"}, wantErr: true,
		},
		{
			name: "empty kind or name error", detector: detector, ref: WrapperRef{Kind: ""}, wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res, err := tt.detector.DetectStatus(context.Background(), tt.ref)
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

func TestStatusDetector_DetectStatus_ExtractionDetails(t *testing.T) {
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

	detector := NewK8sStatusDetector(dynamicfake.NewSimpleDynamicClient(newTestScheme(), c1, c2, c3, c4, c5, c6, c7, c8), nil)
	ctx := context.Background()

	t.Run("phase fallback active", func(t *testing.T) {
		status, err := detector.DetectStatus(ctx, WrapperRef{Group: "custom.io", Version: "v1", Kind: "CustomCluster", Name: "c2", Namespace: "xmc"})
		if err != nil || !status.Ready || status.Message != "Phase is Ready" {
			t.Errorf("expected phase fallback Ready: status=%+v, err=%v", status, err)
		}
	})

	t.Run("sibling message extraction on custom condition path", func(t *testing.T) {
		status, err := detector.DetectStatus(ctx, WrapperRef{
			Group: "custom.io", Version: "v1", Kind: "CustomCluster", Name: "c1", Namespace: "xmc",
			ReadyConditionPath: "{.status.conditions[1].status}", ExpectedReadyValue: "Synced",
		})
		if err != nil || status.Ready || status.Message != "Still syncing" {
			t.Errorf("expected sibling message 'Still syncing', got %+v", status)
		}
	})

	t.Run("candidate url, host, and auth secret extraction", func(t *testing.T) {
		status3, err := detector.DetectStatus(ctx, WrapperRef{Group: "custom.io", Version: "v1", Kind: "CustomCluster", Name: "c3", Namespace: "xmc"})
		if err != nil || status3.Endpoint != "http://c3-url.xmc:9000" || status3.ConnectionSecret != "c3-auth-sec" {
			t.Errorf("unexpected url/auth extraction: status=%+v, err=%v", status3, err)
		}
		status4, err := detector.DetectStatus(ctx, WrapperRef{Group: "custom.io", Version: "v1", Kind: "CustomCluster", Name: "c4", Namespace: "xmc"})
		if err != nil || status4.Endpoint != "c4-host.xmc" {
			t.Errorf("unexpected host extraction: status=%+v, err=%v", status4, err)
		}
	})

	t.Run("generation parsing variations and errors", func(t *testing.T) {
		statusFloat, _ := detector.DetectStatus(ctx, WrapperRef{Group: "custom.io", Version: "v1", Kind: "CustomCluster", Name: "c1", Namespace: "xmc", ObservedGenerationPath: "status.genFloat"})
		if statusFloat.ObservedGeneration != 12 {
			t.Errorf("expected observedGen 12: got %d", statusFloat.ObservedGeneration)
		}
		statusStr, _ := detector.DetectStatus(ctx, WrapperRef{Group: "custom.io", Version: "v1", Kind: "CustomCluster", Name: "c1", Namespace: "xmc", ObservedGenerationPath: "status.genStr"})
		if statusStr.ObservedGeneration != 15 {
			t.Errorf("expected observedGen 15: got %d", statusStr.ObservedGeneration)
		}
		if _, err := detector.DetectStatus(ctx, WrapperRef{Group: "custom.io", Version: "v1", Kind: "CustomCluster", Name: "c7", Namespace: "xmc", ObservedGenerationPath: "status.genBool"}); err == nil {
			t.Error("expected error for bool generation")
		}
		if _, err := detector.DetectStatus(ctx, WrapperRef{Group: "custom.io", Version: "v1", Kind: "CustomCluster", Name: "c7", Namespace: "xmc", ObservedGenerationPath: "status.genStrBad"}); err == nil {
			t.Error("expected error for invalid string generation")
		}
		if _, err := detector.DetectStatus(ctx, WrapperRef{Group: "custom.io", Version: "v1", Kind: "CustomCluster", Name: "c7", Namespace: "xmc", ObservedGenerationPath: "status.gen["}); err == nil {
			t.Error("expected error for invalid generation JSONPath")
		}
	})

	t.Run("custom paths and non-map status", func(t *testing.T) {
		status7, err := detector.DetectStatus(ctx, WrapperRef{
			Group: "custom.io", Version: "v1", Kind: "CustomCluster", Name: "c7", Namespace: "xmc",
			EndpointJSONPath: "status.head.serviceName", ConnectionSecretJSONPath: "status.secret.name",
		})
		if err != nil || status7.Endpoint != "c7-head-svc" || status7.ConnectionSecret != "c7-sec-name" {
			t.Errorf("unexpected head/secret extraction: status=%+v, err=%v", status7, err)
		}
		status8, err := detector.DetectStatus(ctx, WrapperRef{Group: "custom.io", Version: "v1", Kind: "CustomCluster", Name: "c8", Namespace: "xmc"})
		if err != nil || status8.Ready {
			t.Errorf("unexpected status for non-map status: status=%+v, err=%v", status8, err)
		}
		status5, _ := detector.DetectStatus(ctx, WrapperRef{Group: "custom.io", Version: "v1", Kind: "CustomCluster", Name: "c5", Namespace: "xmc", ReadyConditionPath: "status.state", ExpectedReadyValue: "Healthy"})
		if status5.Ready || status5.Message != `Expected ready value "Healthy", got "Degraded"` {
			t.Errorf("unexpected mismatch status: status=%+v", status5)
		}
		status6, _ := detector.DetectStatus(ctx, WrapperRef{Group: "custom.io", Version: "v1", Kind: "CustomCluster", Name: "c6", Namespace: "xmc"})
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
			if _, err := detector.DetectStatus(ctx, wRef); err == nil {
				t.Errorf("expected error for invalid query path in %+v", wRef)
			}
		}
	})

	t.Run("duck typing address and crossplane secret fallback", func(t *testing.T) {
		duckObj := makeObj("knative.dev", "v1", "Service", "xmc", "knative-svc", 1, map[string]any{
			"address":             map[string]any{"url": "http://knative-svc.xmc.svc.cluster.local"},
			"connectionSecretRef": map[string]any{"name": "knative-secret"},
			"conditions": []any{
				"invalid-non-map-condition",
				map[string]any{"type": "ready", "status": true, "message": "Service live"},
			},
		})
		duckDetector := NewK8sStatusDetector(dynamicfake.NewSimpleDynamicClient(newTestScheme(), duckObj), nil)
		res, err := duckDetector.DetectStatus(ctx, WrapperRef{Group: "knative.dev", Version: "v1", Kind: "Service", Name: "knative-svc", Namespace: "xmc"})
		if err != nil || !res.Ready || res.Endpoint != "http://knative-svc.xmc.svc.cluster.local" || res.ConnectionSecret != "knative-secret" {
			t.Errorf("unexpected duck typing resolution: res=%+v, err=%v", res, err)
		}
	})

	t.Run("unstructured value and nil object handling", func(t *testing.T) {
		uVal := unstructured.Unstructured{Object: map[string]any{"status": map[string]any{"phase": "Active"}}}
		got, err := EvaluateJSONPath(uVal, "status.phase")
		if err != nil || got != "Active" {
			t.Errorf("EvaluateJSONPath(uVal) = (%q, %v), want Active", got, err)
		}

		nilRes, err := ExtractResourceStatus(nil, WrapperRef{})
		if err != nil || nilRes.Found || nilRes.Message != "object is nil" {
			t.Errorf("ExtractResourceStatus(nil) = (%+v, %v), want not found nil msg", nilRes, err)
		}

		st, msg, found := getConditionStatusAndMessage(nil, "Ready")
		if found || st != "" || msg != "" {
			t.Errorf("getConditionStatusAndMessage(nil) = (%s, %s, %v), want not found", st, msg, found)
		}

		if deriveSiblingMessagePath("") != "" {
			t.Error("deriveSiblingMessagePath('') should return empty")
		}
		if deriveSiblingMessagePath("status.phase") != "" {
			t.Error("deriveSiblingMessagePath('status.phase') should return empty")
		}
	})

	t.Run("status.observedGeneration numeric and string variations", func(t *testing.T) {
		for _, tt := range []struct {
			name string
			val  any
			want int64
		}{
			{"int64", int64(10), 10},
			{"int32", int32(20), 20},
			{"int", int(30), 30},
			{"float64", float64(40), 40},
			{"string", "50", 50},
			{"stringer", statusTestStringer{val: "60"}, 60},
		} {
			t.Run(tt.name, func(t *testing.T) {
				genObj := makeObj("custom.io", "v1", "CustomCluster", "xmc", "gen-test", 1, map[string]any{
					"observedGeneration": tt.val,
				})
				gen, err := extractObservedGeneration(genObj, "")
				if err != nil || gen != tt.want {
					t.Errorf("extractObservedGeneration() = (%d, %v), want %d", gen, err, tt.want)
				}
			})
		}
	})

	t.Run("custom path missing and condition Ready message fallback", func(t *testing.T) {
		fallbackObj := makeObj("custom.io", "v1", "CustomCluster", "xmc", "fallback-c", 1, map[string]any{
			"customState": "Unknown",
			"conditions": []any{
				map[string]any{"type": "Ready", "status": "False", "message": "Failing ready condition"},
			},
		})
		d := NewK8sStatusDetector(dynamicfake.NewSimpleDynamicClient(newTestScheme(), fallbackObj), nil)

		// Case: readyConditionPath returns empty -> "Readiness status not reported"
		st1, err := d.DetectStatus(ctx, WrapperRef{Group: "custom.io", Version: "v1", Kind: "CustomCluster", Name: "fallback-c", Namespace: "xmc", ReadyConditionPath: "status.nonexistent"})
		if err != nil || st1.Ready || st1.Message != "Readiness status not reported" {
			t.Errorf("expected not reported status: st1=%+v, err=%v", st1, err)
		}

		// Case: readyConditionPath does not end in .status, falls back to Ready condition message
		st2, err := d.DetectStatus(ctx, WrapperRef{Group: "custom.io", Version: "v1", Kind: "CustomCluster", Name: "fallback-c", Namespace: "xmc", ReadyConditionPath: "status.customState", ExpectedReadyValue: "Healthy"})
		if err != nil || st2.Ready || st2.Message != "Failing ready condition" {
			t.Errorf("expected falling ready condition message fallback: st2=%+v, err=%v", st2, err)
		}
	})
}

type fakeErrorRESTMapper struct {
	meta.RESTMapper
	err error
}

func (f *fakeErrorRESTMapper) RESTMapping(gk schema.GroupKind, versions ...string) (*meta.RESTMapping, error) {
	return nil, f.err
}

func TestStatusDetector_RESTMapperError(t *testing.T) {
	d := NewK8sStatusDetector(dynamicfake.NewSimpleDynamicClient(newTestScheme()), &fakeErrorRESTMapper{err: errors.New("generic rest mapper failure")})
	_, err := d.DetectStatus(context.Background(), WrapperRef{Group: "g", Version: "v", Kind: "K", Name: "n", Namespace: "ns"})
	if err == nil || !strings.Contains(err.Error(), "generic rest mapper failure") {
		t.Errorf("expected rest mapper failure, got err=%v", err)
	}
}

func TestToBridgeWrapperRef(t *testing.T) {
	if ref := ToBridgeWrapperRef(nil, "default"); ref.Name != "" {
		t.Errorf("expected empty ref for nil proto: %+v", ref)
	}

	pbRef := &componentspb.WrapperRef{
		CrdGroup:        "clickhouse.altinity.com",
		CrdVersion:      "v1",
		CrdKind:         "ClickHouseInstallation",
		TargetName:      "my-ch",
		TargetNamespace: "",
	}
	ref := ToBridgeWrapperRef(pbRef, "fallback-ns")
	if ref.Group != "clickhouse.altinity.com" || ref.Name != "my-ch" || ref.Namespace != "fallback-ns" {
		t.Errorf("unexpected bridge wrapper ref: %+v", ref)
	}
}

func TestResolveGVR(t *testing.T) {
	rm := restmapper.NewDiscoveryRESTMapper([]*restmapper.APIGroupResources{
		{
			Group:              metav1.APIGroup{Name: "apps", Versions: []metav1.GroupVersionForDiscovery{{GroupVersion: "apps/v1", Version: "v1"}}},
			VersionedResources: map[string][]metav1.APIResource{"v1": {{Name: "deployments", Namespaced: true, Kind: "Deployment"}}},
		},
		{
			Group:              metav1.APIGroup{Name: "infra.io", Versions: []metav1.GroupVersionForDiscovery{{GroupVersion: "infra.io/v1", Version: "v1"}}},
			VersionedResources: map[string][]metav1.APIResource{"v1": {{Name: "clusterpools", Namespaced: false, Kind: "ClusterPool"}}},
		},
	})

	gvr, isNamespaced, err := ResolveGVR(WrapperRef{Group: "apps", Version: "v1", Kind: "Deployment"}, rm)
	if err != nil || !isNamespaced || gvr.Resource != "deployments" {
		t.Errorf("expected namespaced deployment GVR, got gvr=%+v, isNamespaced=%v, err=%v", gvr, isNamespaced, err)
	}

	gvr, isNamespaced, err = ResolveGVR(WrapperRef{Group: "infra.io", Version: "v1", Kind: "ClusterPool"}, rm)
	if err != nil || isNamespaced || gvr.Resource != "clusterpools" {
		t.Errorf("expected cluster-scoped clusterpools GVR, got gvr=%+v, isNamespaced=%v, err=%v", gvr, isNamespaced, err)
	}

	gvr, isNamespaced, err = ResolveGVR(WrapperRef{Group: "apps", Version: "v1", Kind: "Deployment"}, nil)
	if err != nil || !isNamespaced || gvr.Resource != "deployments" {
		t.Errorf("expected fallback guess GVR, got gvr=%+v, isNamespaced=%v, err=%v", gvr, isNamespaced, err)
	}
}
