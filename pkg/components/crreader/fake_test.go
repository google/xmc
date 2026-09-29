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

package crreader

import (
	"context"
	"errors"
	"testing"

	"github.com/google/go-cmp/cmp"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

func TestFakeGetter(t *testing.T) {
	t.Parallel()
	ctx := context.Background()

	var nilFake *FakeGetter
	if got, err := nilFake.GetComponent(ctx, "xmc", "clickhouse"); err != nil || got != nil {
		t.Errorf("nilFake.GetComponent() = (%v, %v), want (nil, nil)", got, err)
	}

	errFake := &FakeGetter{Err: errors.New("boom")}
	if _, err := errFake.GetComponent(ctx, "xmc", "clickhouse"); err == nil {
		t.Errorf("errFake.GetComponent() succeeded, want error")
	}

	compA := baselineComponent()
	compB := &unstructured.Unstructured{
		Object: map[string]any{
			"metadata": map[string]any{"name": "redpanda", "namespace": "custom"},
		},
	}
	mapFake := &FakeGetter{
		Component: compA,
		Components: map[string]*unstructured.Unstructured{
			"custom/redpanda": compB,
			"by-name-only":    compB,
		},
	}

	gotNS, err := mapFake.GetComponent(ctx, "custom", "redpanda")
	if err != nil {
		t.Fatalf("GetComponent(custom/redpanda) error: %v", err)
	}
	if diff := cmp.Diff(compB.Object, gotNS.Object); diff != "" {
		t.Errorf("GetComponent(custom/redpanda) mismatch (-want +got):\n%s", diff)
	}

	gotByName, err := mapFake.GetComponent(ctx, "other", "by-name-only")
	if err != nil {
		t.Fatalf("GetComponent(by-name-only) error: %v", err)
	}
	if diff := cmp.Diff(compB.Object, gotByName.Object); diff != "" {
		t.Errorf("GetComponent(by-name-only) mismatch (-want +got):\n%s", diff)
	}

	gotFallback, err := mapFake.GetComponent(ctx, "xmc", "clickhouse")
	if err != nil {
		t.Fatalf("GetComponent(fallback) error: %v", err)
	}
	if diff := cmp.Diff(compA.Object, gotFallback.Object); diff != "" {
		t.Errorf("GetComponent(fallback) mismatch (-want +got):\n%s", diff)
	}
	gotFallback.Object["mutated"] = true
	if _, ok := compA.Object["mutated"]; ok {
		t.Errorf("GetComponent did not DeepCopy returned Unstructured")
	}

	emptyFake := &FakeGetter{}
	if got, err := emptyFake.GetComponent(ctx, "xmc", "missing"); err != nil || got != nil {
		t.Errorf("emptyFake.GetComponent() = (%v, %v), want (nil, nil)", got, err)
	}
}
