// Copyright 2026 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//	http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package worker

import (
	"testing"
)

func TestParseReconcilePayload(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		data    string
		want    int64
		wantErr bool
	}{
		{name: "valid", data: `{"experiment":"experiments/123"}`, want: 123},
		{name: "unknown fields are ignored", data: `{"experiment":"experiments/123","attempt":2}`, want: 123},
		{name: "empty", data: "", wantErr: true},
		{name: "malformed JSON", data: `{"experiment":`, wantErr: true},
		{name: "not an object", data: `"experiments/123"`, wantErr: true},
		{name: "missing experiment", data: `{}`, wantErr: true},
		{name: "empty experiment", data: `{"experiment":""}`, wantErr: true},
		{name: "xid resource name", data: `{"experiment":"xids/123"}`, wantErr: true},
		{name: "work unit resource name", data: `{"experiment":"experiments/123/workUnits/1"}`, wantErr: true},
		{name: "non-numeric xid", data: `{"experiment":"experiments/abc"}`, wantErr: true},
		{name: "multibyte xid digits", data: `{"experiment":"experiments/１２３"}`, wantErr: true},
		{name: "wrong field type", data: `{"experiment":123}`, wantErr: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := parseReconcilePayload(tc.data)
			if (err != nil) != tc.wantErr {
				t.Fatalf("parseReconcilePayload(%q) error = %v, wantErr %v", tc.data, err, tc.wantErr)
			}
			if got != tc.want {
				t.Errorf("parseReconcilePayload(%q) = %d, want %d", tc.data, got, tc.want)
			}
		})
	}
}
