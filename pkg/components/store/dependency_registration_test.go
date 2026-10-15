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

package store

import (
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
)

const (
	notRegisteredMissing = `rpc error: code = FailedPrecondition desc = dependency not found: component "ingestion-gateway" depends on "missing", which is not registered`
	notRegisteredGone    = `rpc error: code = FailedPrecondition desc = dependency not found: component "ingestion-gateway" depends on "gone", which is not registered`
)

// registrationCatalog returns a catalog with two registered components, a soft-deleted one, a nil
// entry, and a "redpanda" -> "ingestion-gateway" edge so that the reverse edge would close a cycle.
func registrationCatalog() map[string]*ComponentRecord {
	now := time.Now().UTC()
	return map[string]*ComponentRecord{
		"redpanda": {ComponentID: 11, Name: "redpanda", Dependencies: []string{"ingestion-gateway"}},
		"auth":     {ComponentID: 22, Name: "auth"},
		"gone":     {ComponentID: 33, Name: "gone", DeleteTime: &now},
		"nil-rec":  nil,
	}
}

func TestResolveDependencyIDs(t *testing.T) {
	tests := []struct {
		name    string
		deps    []string
		want    map[string]int64
		wantErr string
	}{
		{name: "no dependencies", deps: nil, want: map[string]int64{}},
		{name: "all registered", deps: []string{"auth", "redpanda"}, want: map[string]int64{"auth": 22, "redpanda": 11}},
		{name: "unregistered", deps: []string{"redpanda", "missing"}, wantErr: notRegisteredMissing},
		{name: "soft-deleted", deps: []string{"gone"}, wantErr: notRegisteredGone},
		{name: "nil record", deps: []string{"nil-rec"}, wantErr: `rpc error: code = FailedPrecondition desc = dependency not found: component "ingestion-gateway" depends on "nil-rec", which is not registered`},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := resolveDependencyIDs(registrationCatalog(), "ingestion-gateway", tt.deps)
			if tt.wantErr != "" {
				if err == nil || err.Error() != tt.wantErr {
					t.Fatalf("resolveDependencyIDs() error = %v, want %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("resolveDependencyIDs() unexpected error: %v", err)
			}
			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("resolveDependencyIDs() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestValidateIntentDependencies(t *testing.T) {
	tests := []struct {
		name    string
		target  string
		deps    []string
		want    map[string]int64
		wantErr string
	}{
		{name: "registered dependencies resolve to IDs", target: "ingestion-gateway", deps: []string{"auth"}, want: map[string]int64{"auth": 22}},
		{name: "unregistered dependency", target: "ingestion-gateway", deps: []string{"auth", "missing"}, wantErr: notRegisteredMissing},
		{name: "soft-deleted dependency", target: "ingestion-gateway", deps: []string{"gone"}, wantErr: notRegisteredGone},
		{name: "cycle is reported before registration", target: "ingestion-gateway", deps: []string{"missing", "redpanda"}, wantErr: "rpc error: code = FailedPrecondition desc = dependency cycle detected: ingestion-gateway -> redpanda -> ingestion-gateway"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := validateIntentDependencies(registrationCatalog(), tt.target, tt.deps)
			if tt.wantErr != "" {
				if err == nil || err.Error() != tt.wantErr {
					t.Fatalf("validateIntentDependencies() error = %v, want %q", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("validateIntentDependencies() unexpected error: %v", err)
			}
			if diff := cmp.Diff(tt.want, got); diff != "" {
				t.Errorf("validateIntentDependencies() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}
