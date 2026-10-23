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

// Unit tests for the catalog reads in dbstore.go, run against memoryRepository.
package dbstore

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/testing/protocmp"

	componentspb "xmanager_cloud/api/protos/components/v1alpha1"
	"xmanager_cloud/pkg/components/store"
)

var (
	errInjectedRead = errors.New("injected read failure")
	baseTime        = time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
)

// enabledRecord returns the baseline fixture: an ENABLED component at generation 1.
func enabledRecord(id int64, name string) *store.ComponentRecord {
	return &store.ComponentRecord{
		ComponentID: id,
		Name:        name,
		Spec:        &componentspb.ComponentSpec{Type: componentspb.ComponentType_COMPONENT_TYPE_CUSTOM_1P_COMPONENT, Version: "v1.2.0"},
		State:       componentspb.ComponentState_COMPONENT_STATE_ENABLED,
		Generation:  1,
		CreateTime:  baseTime,
		UpdateTime:  baseTime,
	}
}

// withState returns a copy of rec in state.
func withState(rec *store.ComponentRecord, state componentspb.ComponentState) *store.ComponentRecord {
	cp := store.CloneRecord(rec)
	cp.State = state
	return cp
}

// readRecord returns rec as the repository reads return it: with deps and its Etag.
func readRecord(rec *store.ComponentRecord, deps ...string) *store.ComponentRecord {
	cp := store.CloneRecord(rec)
	cp.Dependencies = deps
	cp.Etag = store.GenerateEtag(cp)
	return cp
}

// newCatalogRepository returns a repository with an active "app" (ID 1) that depends on the active
// "db" (ID 2) and on the deleted "cache" (ID 3).
func newCatalogRepository() *memoryRepository {
	repo := newMemoryRepository()
	repo.putComponent(enabledRecord(1, "app"), 2, 3)
	repo.putComponent(enabledRecord(2, "db"))
	repo.putComponent(withState(enabledRecord(3, "cache"), componentspb.ComponentState_COMPONENT_STATE_DELETED))
	return repo
}

func TestStore_GetComponent(t *testing.T) {
	tests := []struct {
		name     string
		lookup   string
		readErr  error
		want     *store.ComponentRecord
		wantCode codes.Code
		wantErr  error
	}{
		{name: "canonical name", lookup: "app", want: readRecord(enabledRecord(1, "app"), "db")},
		{name: "full resource name", lookup: "components/db", want: readRecord(enabledRecord(2, "db"))},
		{name: "missing component", lookup: "missing", wantCode: codes.NotFound},
		{name: "deleted component", lookup: "cache", wantCode: codes.NotFound},
		{name: "invalid name", lookup: "", wantCode: codes.InvalidArgument},
		{name: "read failure", lookup: "app", readErr: errInjectedRead, wantErr: errInjectedRead},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := newCatalogRepository()
			repo.readErr = tt.readErr
			got, err := New(repo).GetComponent(context.Background(), tt.lookup)
			assertError(t, err, tt.wantCode, tt.wantErr)
			if diff := cmp.Diff(tt.want, got, protocmp.Transform()); diff != "" {
				t.Errorf("GetComponent(%q) mismatch (-want +got):\n%s", tt.lookup, diff)
			}
		})
	}
}

func TestStore_GetComponent_ReturnsCopy(t *testing.T) {
	s := New(newCatalogRepository())
	got, err := s.GetComponent(context.Background(), "app")
	if err != nil {
		t.Fatalf("GetComponent() failed: %v", err)
	}
	got.Spec.Version = "mutated"
	got.Dependencies[0] = "mutated"

	again, err := s.GetComponent(context.Background(), "app")
	if err != nil {
		t.Fatalf("GetComponent() failed: %v", err)
	}
	if diff := cmp.Diff(readRecord(enabledRecord(1, "app"), "db"), again, protocmp.Transform()); diff != "" {
		t.Errorf("GetComponent() after mutating a returned record (-want +got):\n%s", diff)
	}
}

func TestStore_GetComponentByID(t *testing.T) {
	tests := []struct {
		name     string
		id       int64
		readErr  error
		want     *store.ComponentRecord
		wantCode codes.Code
		wantErr  error
	}{
		{name: "active component", id: 1, want: readRecord(enabledRecord(1, "app"), "db")},
		{name: "deleted component", id: 3, wantCode: codes.NotFound},
		{name: "missing component", id: 42, wantCode: codes.NotFound},
		{name: "zero ID", id: 0, wantCode: codes.InvalidArgument},
		{name: "negative ID", id: -1, wantCode: codes.InvalidArgument},
		{name: "read failure", id: 1, readErr: errInjectedRead, wantErr: errInjectedRead},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := newCatalogRepository()
			repo.readErr = tt.readErr
			got, err := New(repo).GetComponentByID(context.Background(), tt.id)
			assertError(t, err, tt.wantCode, tt.wantErr)
			if diff := cmp.Diff(tt.want, got, protocmp.Transform()); diff != "" {
				t.Errorf("GetComponentByID(%d) mismatch (-want +got):\n%s", tt.id, diff)
			}
		})
	}
}

func TestStore_ListComponents(t *testing.T) {
	tests := []struct {
		name    string
		readErr error
		want    []*store.ComponentRecord
		wantErr error
	}{
		{
			name: "active components sorted by name",
			want: []*store.ComponentRecord{readRecord(enabledRecord(1, "app"), "db"), readRecord(enabledRecord(2, "db"))},
		},
		{name: "read failure", readErr: errInjectedRead, wantErr: errInjectedRead},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := newCatalogRepository()
			repo.readErr = tt.readErr
			got, err := New(repo).ListComponents(context.Background())
			assertError(t, err, codes.OK, tt.wantErr)
			if diff := cmp.Diff(tt.want, got, protocmp.Transform()); diff != "" {
				t.Errorf("ListComponents() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestStore_ValidateComponentRequirement(t *testing.T) {
	disabled := withState(enabledRecord(4, "worker"), componentspb.ComponentState_COMPONENT_STATE_DISABLED)
	tests := []struct {
		name       string
		lookup     string
		minVersion string
		readErr    error
		want       *store.RequirementValidationResult
		wantCode   codes.Code
		wantErr    error
	}{
		{
			name:       "enabled component at a newer version",
			lookup:     "db",
			minVersion: "v1.0.0",
			want:       &store.RequirementValidationResult{IsSatisfied: true, SatisfyingComponentName: "db", Reason: "requirement satisfied", Component: readRecord(enabledRecord(2, "db"))},
		},
		{
			name:   "disabled component",
			lookup: "worker",
			want: &store.RequirementValidationResult{
				SatisfyingComponentName: "worker",
				Reason:                  `component "worker" is in state COMPONENT_STATE_DISABLED, required ENABLED`,
				Component:               readRecord(disabled),
			},
		},
		{
			name:   "missing component",
			lookup: "missing",
			want:   &store.RequirementValidationResult{SatisfyingComponentName: "missing", Reason: `component "missing" not found`},
		},
		{name: "invalid minimum version", lookup: "db", minVersion: "not-a-version", wantCode: codes.InvalidArgument},
		{name: "invalid name", lookup: "", wantCode: codes.InvalidArgument},
		{name: "read failure", lookup: "db", readErr: errInjectedRead, wantErr: errInjectedRead},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			repo := newCatalogRepository()
			repo.putComponent(disabled)
			repo.readErr = tt.readErr
			got, err := New(repo).ValidateComponentRequirement(context.Background(), tt.lookup, componentspb.ComponentType_COMPONENT_TYPE_UNSPECIFIED, tt.minVersion)
			assertError(t, err, tt.wantCode, tt.wantErr)
			if diff := cmp.Diff(tt.want, got, protocmp.Transform()); diff != "" {
				t.Errorf("ValidateComponentRequirement(%q) mismatch (-want +got):\n%s", tt.lookup, diff)
			}
		})
	}
}

func TestStore_GetPlatformHealth(t *testing.T) {
	_, err := New(newMemoryRepository()).GetPlatformHealth(context.Background(), "", 0, "")
	assertError(t, err, codes.Unimplemented, nil)
}

// assertError checks that err has wantCode (codes.OK for no status) and wraps wantErr; both
// zero values mean that no error is expected.
func assertError(t *testing.T, err error, wantCode codes.Code, wantErr error) {
	t.Helper()
	switch {
	case wantErr != nil:
		if !errors.Is(err, wantErr) {
			t.Fatalf("error = %v, want %v", err, wantErr)
		}
	case wantCode != codes.OK:
		if got := status.Code(err); got != wantCode {
			t.Fatalf("error code = %v (%v), want %v", got, err, wantCode)
		}
	case err != nil:
		t.Fatalf("unexpected error: %v", err)
	}
}
