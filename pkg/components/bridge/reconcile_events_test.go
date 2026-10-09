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
	"testing"

	"github.com/google/go-cmp/cmp"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/client-go/tools/record"
)

func TestEmitReconcileEvents(t *testing.T) {
	item := &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "xmc.dev/v1alpha1",
		"kind":       "Component",
		"metadata":   map[string]any{"name": "redpanda", "namespace": "xmc", "uid": "uid-old"},
	}}
	tests := []struct {
		name string
		res  *ReconcileResult
		want []string
	}{
		{name: "no result emits nothing", res: nil},
		{name: "result without an event emits nothing", res: &ReconcileResult{RemoveFinalizer: true}},
		{
			name: "warning event is emitted",
			res:  &ReconcileResult{WarningEvent: &Event{Reason: ReasonStaleDeletion, Message: "kept in CMS"}},
			want: []string{"Warning StaleDeletion kept in CMS"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			recorder := record.NewFakeRecorder(len(tt.want) + 1)
			EmitReconcileEvents(recorder, item, tt.res)
			close(recorder.Events)
			var got []string
			for e := range recorder.Events {
				got = append(got, e)
			}
			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("events mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestEmitReconcileEvents_NilRecorder(t *testing.T) {
	res := &ReconcileResult{WarningEvent: &Event{Reason: ReasonStaleDeletion, Message: "kept in CMS"}}
	EmitReconcileEvents(nil, &unstructured.Unstructured{}, res) // Must not panic.
}
