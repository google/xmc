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
	"testing"
)

func TestFakeStatusDetector_Lifecycle(t *testing.T) {
	ctx := context.Background()
	fake := NewFakeStatusDetector()

	ref := WrapperRef{Group: "ray.io", Version: "v1", Kind: "RayCluster", Namespace: "default", Name: "ray-cluster"}

	// Initial query returns not found
	st, err := fake.DetectStatus(ctx, ref)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if st.Found || st.Ready {
		t.Fatalf("expected not found, got %+v", st)
	}

	// Configure status
	fake.SetStatus("default/ray-cluster", &ResourceStatus{Found: true, Ready: true, Message: "healthy"})
	st, err = fake.DetectStatus(ctx, ref)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !st.Found || !st.Ready || st.Message != "healthy" {
		t.Fatalf("unexpected status: %+v", st)
	}

	// Verify calls tracked
	calls := fake.GetCalls()
	if len(calls) != 2 {
		t.Fatalf("expected 2 calls, got %d", len(calls))
	}

	// Configure error
	expectedErr := errors.New("simulated detection failure")
	fake.SetError(expectedErr)
	_, err = fake.DetectStatus(ctx, ref)
	if !errors.Is(err, expectedErr) {
		t.Fatalf("expected error %v, got %v", expectedErr, err)
	}
}

func TestUnimplementedStatusDetector(t *testing.T) {
	ctx := context.Background()
	detector := NewUnimplementedStatusDetector()
	ref := WrapperRef{Group: "vllm.ai", Version: "v1alpha1", Kind: "VLLMService", Namespace: "xmc", Name: "llama3"}

	st, err := detector.DetectStatus(ctx, ref)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if st.Found || st.Ready {
		t.Fatalf("expected Found=false and Ready=false, got %+v", st)
	}
	if st.Message == "" {
		t.Fatalf("expected non-empty pending message, got empty")
	}
}
