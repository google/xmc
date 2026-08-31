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

package bridge_test

import (
	"context"
	"errors"
	"testing"

	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	dynamicfake "k8s.io/client-go/dynamic/fake"

	componentspb "xmanager_cloud/api/protos/components/v1alpha1"
	"xmanager_cloud/pkg/components/bridge"
)

type fakeRESTMapper struct {
	meta.RESTMapper
	mapping *meta.RESTMapping
	err     error
}

func (f *fakeRESTMapper) RESTMapping(gk schema.GroupKind, versions ...string) (*meta.RESTMapping, error) {
	if f.err != nil {
		return nil, f.err
	}
	return f.mapping, nil
}

func TestResolveGVR(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name           string
		ref            bridge.WrapperRef
		restMapper     meta.RESTMapper
		wantResource   schema.GroupVersionResource
		wantNamespaced bool
		wantErr        bool
	}{
		{
			name: "UsesRESTMapperWhenAvailable",
			ref: bridge.WrapperRef{
				Group:   "ray.io",
				Version: "v1",
				Kind:    "RayCluster",
			},
			restMapper: &fakeRESTMapper{
				mapping: &meta.RESTMapping{
					Resource: schema.GroupVersionResource{
						Group:    "ray.io",
						Version:  "v1",
						Resource: "rayclusters",
					},
					Scope: meta.RESTScopeNamespace,
				},
			},
			wantResource: schema.GroupVersionResource{
				Group:    "ray.io",
				Version:  "v1",
				Resource: "rayclusters",
			},
			wantNamespaced: true,
			wantErr:        false,
		},
		{
			name: "UsesRESTMapperWithoutVersion",
			ref: bridge.WrapperRef{
				Group: "ray.io",
				Kind:  "RayCluster",
			},
			restMapper: &fakeRESTMapper{
				mapping: &meta.RESTMapping{
					Resource: schema.GroupVersionResource{
						Group:    "ray.io",
						Version:  "v1",
						Resource: "rayclusters",
					},
					Scope: meta.RESTScopeNamespace,
				},
			},
			wantResource: schema.GroupVersionResource{
				Group:    "ray.io",
				Version:  "v1",
				Resource: "rayclusters",
			},
			wantNamespaced: true,
			wantErr:        false,
		},
		{
			name: "FallbackToUnsafeGuessWhenMapperNil",
			ref: bridge.WrapperRef{
				Group:   "clickhouse.altinity.com",
				Version: "v1",
				Kind:    "ClickHouseInstallation",
			},
			restMapper: nil,
			wantResource: schema.GroupVersionResource{
				Group:    "clickhouse.altinity.com",
				Version:  "v1",
				Resource: "clickhouseinstallations",
			},
			wantNamespaced: true,
			wantErr:        false,
		},
		{
			name: "RESTMapperError",
			ref: bridge.WrapperRef{
				Group:   "invalid.io",
				Version: "v1",
				Kind:    "UnknownKind",
			},
			restMapper: &fakeRESTMapper{
				err: errors.New("no matching kind found"),
			},
			wantResource:   schema.GroupVersionResource{},
			wantNamespaced: false,
			wantErr:        true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			gvr, namespaced, err := bridge.ResolveGVR(tc.ref, tc.restMapper)
			if (err != nil) != tc.wantErr {
				t.Fatalf("ResolveGVR() error = %v, wantErr = %v", err, tc.wantErr)
			}
			if !tc.wantErr {
				if gvr != tc.wantResource {
					t.Errorf("ResolveGVR() resource = %v, want %v", gvr, tc.wantResource)
				}
				if namespaced != tc.wantNamespaced {
					t.Errorf("ResolveGVR() namespaced = %v, want %v", namespaced, tc.wantNamespaced)
				}
			}
		})
	}
}

func TestExtractResourceStatus_DefaultReadiness(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		ref       bridge.WrapperRef
		obj       *unstructured.Unstructured
		wantFound bool
		wantReady bool
	}{
		{
			name: "ConditionReadyTrue",
			obj: &unstructured.Unstructured{
				Object: map[string]any{
					"status": map[string]any{
						"conditions": []any{
							map[string]any{
								"type":   "Ready",
								"status": "True",
							},
						},
					},
				},
			},
			wantFound: true,
			wantReady: true,
		},
		{
			name: "CustomExpectedReadyValue",
			ref: bridge.WrapperRef{
				ExpectedReadyValue: "Healthy",
			},
			obj: &unstructured.Unstructured{
				Object: map[string]any{
					"status": map[string]any{
						"conditions": []any{
							map[string]any{
								"type":   "Ready",
								"status": "Healthy",
							},
						},
					},
				},
			},
			wantFound: true,
			wantReady: true,
		},
		{
			name: "ConditionReadyFalse",
			obj: &unstructured.Unstructured{
				Object: map[string]any{
					"status": map[string]any{
						"conditions": []any{
							map[string]any{
								"type":   "Ready",
								"status": "False",
							},
						},
					},
				},
			},
			wantFound: true,
			wantReady: false,
		},
		{
			name: "PhaseRunning",
			obj: &unstructured.Unstructured{
				Object: map[string]any{
					"status": map[string]any{
						"phase": "Running",
					},
				},
			},
			wantFound: true,
			wantReady: true,
		},
		{
			name:      "NilObject",
			obj:       nil,
			wantFound: false,
			wantReady: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			status, err := bridge.ExtractResourceStatus(tc.obj, tc.ref)
			if err != nil {
				t.Fatalf("ExtractResourceStatus() unexpected error: %v", err)
			}
			if status.Found != tc.wantFound {
				t.Errorf("status.Found = %v, want %v", status.Found, tc.wantFound)
			}
			if status.Ready != tc.wantReady {
				t.Errorf("status.Ready = %v, want %v", status.Ready, tc.wantReady)
			}
		})
	}
}

func TestK8sStatusDetector_DetectStatus(t *testing.T) {
	t.Parallel()

	scheme := runtime.NewScheme()
	unstrObj := &unstructured.Unstructured{
		Object: map[string]any{
			"apiVersion": "ray.io/v1",
			"kind":       "RayCluster",
			"metadata": map[string]any{
				"name":       "ray-cluster-1",
				"namespace":  "xmc",
				"generation": int64(1),
			},
			"status": map[string]any{
				"conditions": []any{
					map[string]any{
						"type":   "Ready",
						"status": "True",
					},
				},
			},
		},
	}

	dynClient := dynamicfake.NewSimpleDynamicClient(scheme, unstrObj)
	detector := bridge.NewK8sStatusDetector(dynClient, nil)

	ref := bridge.WrapperRef{
		Group:     "ray.io",
		Version:   "v1",
		Kind:      "RayCluster",
		Name:      "ray-cluster-1",
		Namespace: "xmc",
	}

	status, err := detector.DetectStatus(context.Background(), ref)
	if err != nil {
		t.Fatalf("DetectStatus() unexpected error: %v", err)
	}
	if !status.Found || !status.Ready {
		t.Errorf("DetectStatus() expected found=true ready=true, got found=%v ready=%v", status.Found, status.Ready)
	}
}

func TestToBridgeWrapperRef(t *testing.T) {
	t.Parallel()

	pbRef := &componentspb.WrapperRef{
		CrdGroup:        "vllm.ai",
		CrdVersion:      "v1alpha1",
		CrdKind:         "VLLMService",
		TargetName:      "vllm-service-1",
		TargetNamespace: "",
	}

	ref := bridge.ToBridgeWrapperRef(pbRef, "default-ns")
	if ref.Group != "vllm.ai" || ref.Kind != "VLLMService" || ref.Name != "vllm-service-1" || ref.Namespace != "default-ns" {
		t.Errorf("ToBridgeWrapperRef() = %+v, unexpected mapping", ref)
	}
}

func TestFakeStatusDetector(t *testing.T) {
	t.Parallel()

	fake := bridge.NewFakeStatusDetector()
	fake.SetStatus("test-ns/my-ray", &bridge.ResourceStatus{
		Found: true,
		Ready: true,
	})

	status, err := fake.DetectStatus(context.Background(), bridge.WrapperRef{
		Name:      "my-ray",
		Namespace: "test-ns",
	})
	if err != nil {
		t.Fatalf("DetectStatus() unexpected error: %v", err)
	}
	if !status.Found || !status.Ready {
		t.Errorf("DetectStatus() = %+v, want found=true ready=true", status)
	}
}
