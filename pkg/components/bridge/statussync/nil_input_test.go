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

package statussync

import (
	"context"
	"testing"

	"xmanager_cloud/pkg/components/bridge"

	"github.com/google/go-cmp/cmp"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
)

// The bridge itself never reaches these paths: it skips the status write when Reconcile returns
// no result, and items always come from a LIST response. These are exported entry points in a
// public package though, so the preconditions are enforced rather than assumed.

func TestApplyStatusRejectsNilInput(t *testing.T) {
	tests := []struct {
		name string
		item *unstructured.Unstructured
		res  *bridge.ReconcileResult
	}{
		{name: "nil component", item: nil, res: &bridge.ReconcileResult{}},
		{name: "nil reconcile result", item: &unstructured.Unstructured{}, res: nil},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// A nil client proves no API call is attempted: reaching one would panic.
			w := NewWriter(nil, "xmc")
			if err := w.ApplyStatus(context.Background(), tt.item, tt.res); err == nil {
				t.Error("ApplyStatus() = nil, want an error")
			}
		})
	}
}

func TestSyncFinalizersRejectsNilComponent(t *testing.T) {
	w := NewWriter(nil, "xmc")
	got, err := w.SyncFinalizers(context.Background(), nil, &bridge.ReconcileResult{AddFinalizer: true})
	if err == nil {
		t.Error("SyncFinalizers() error = nil, want an error")
	}
	if got != nil {
		t.Errorf("SyncFinalizers() = %v, want nil", got)
	}
}

func TestBuildStatusNilResult(t *testing.T) {
	if diff := cmp.Diff(ComponentStatus{}, BuildStatus(nil)); diff != "" {
		t.Errorf("BuildStatus(nil) mismatch (-want +got):\n%s", diff)
	}
}

func TestBuildStatusMapNilResult(t *testing.T) {
	// Must be nil, not a map of zero values: Server-Side Apply would write those zeros over a
	// live status, silently resetting observedGeneration and state.
	if got := BuildStatusMap(nil); got != nil {
		t.Errorf("BuildStatusMap(nil) = %v, want nil", got)
	}
}
