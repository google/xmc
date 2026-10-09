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

package store_test

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/testing/protocmp"

	componentspb "xmanager_cloud/api/protos/components/v1alpha1"
	"xmanager_cloud/pkg/components/store"
)

// TestExecuteDeletion_EpochParity runs the same rows against MemoryStore and SpannerStore so both
// decide sourced deletions identically: only the owner (recorded source) deletes a sourced record.
// Each row seeds an ENABLED component, from seedSource or unsourced when it is nil, and deletes it
// from src.
func TestExecuteDeletion_EpochParity(t *testing.T) {
	seeded := epochSource("uid-b", 0, 3)
	const ownedBy = `is owned by source "uid-b"`
	tests := []struct {
		name       string
		seedSource *store.IntentSource
		src        *store.IntentSource
		wantCode   codes.Code
		wantPrefix string
		// wantDetails are substrings the rejection message must contain.
		wantDetails []string
	}{
		{name: "older object is stale", seedSource: seeded, src: epochSource("uid-a", -time.Hour, 5), wantCode: codes.FailedPrecondition, wantPrefix: store.StaleDeletionMessagePrefix, wantDetails: []string{ownedBy, "from an older object"}},
		{name: "same creation time with a smaller uid is stale", seedSource: seeded, src: epochSource("uid-a", 0, 1), wantCode: codes.FailedPrecondition, wantPrefix: store.StaleDeletionMessagePrefix, wantDetails: []string{ownedBy, "from an older object"}},
		{name: "newer object that has not adopted the record is stale", seedSource: seeded, src: epochSource("uid-c", time.Hour, 1), wantCode: codes.FailedPrecondition, wantPrefix: store.StaleDeletionMessagePrefix, wantDetails: []string{ownedBy, "from a newer object that has not adopted it"}},
		{name: "same creation time with a greater uid is stale", seedSource: seeded, src: epochSource("uid-c", 0, 1), wantCode: codes.FailedPrecondition, wantPrefix: store.StaleDeletionMessagePrefix, wantDetails: []string{ownedBy, "from a newer object that has not adopted it"}},
		{name: "recorded object deletes", seedSource: seeded, src: epochSource("uid-b", 0, 3)},
		{name: "older generation of the recorded object deletes", seedSource: seeded, src: epochSource("uid-b", 0, 1)},
		{name: "unsourced deletion of a sourced record deletes", seedSource: seeded},
		{name: "sourced deletion of an unsourced record deletes", src: epochSource("uid-a", -time.Hour, 1)},
		{name: "invalid source is rejected", seedSource: seeded, src: &store.IntentSource{CreateTime: epochTime, Generation: 1}, wantCode: codes.InvalidArgument},
	}
	for _, backend := range intentStoreBackends() {
		t.Run(backend.name, func(t *testing.T) {
			ctx := context.Background()
			s := backend.newStore(t)
			// Subtests are intentionally sequential (no t.Parallel) because they share one store.
			for i, tt := range tests {
				t.Run(tt.name, func(t *testing.T) {
					name := fmt.Sprintf("deletion-epoch-%d", i)
					before := seedEpochComponent(ctx, t, s, name, tt.seedSource, 1)

					err := s.ExecuteDeletion(ctx, name, componentspb.DeletionPolicy_DELETION_POLICY_DELETE, false, tt.src)
					if tt.wantCode != codes.OK {
						assertDeletionRejected(ctx, t, s, name, err, tt.wantCode, tt.wantPrefix, tt.wantDetails, before)
						return
					}
					if err != nil {
						t.Fatalf("ExecuteDeletion() error = %v", err)
					}
					if _, err := s.GetComponent(ctx, name); status.Code(err) != codes.NotFound {
						t.Errorf("GetComponent(%s) after deletion error = %v, want NotFound", name, err)
					}
				})
			}
		})
	}
}

// assertDeletionRejected checks that err has wantCode and a message starting with wantPrefix and
// containing each of wantDetails, and that the rejected deletion left the stored record as it was
// seeded (before).
func assertDeletionRejected(ctx context.Context, t *testing.T, s store.Store, name string, err error, wantCode codes.Code, wantPrefix string, wantDetails []string, before *store.ComponentRecord) {
	t.Helper()
	msg := status.Convert(err).Message()
	if status.Code(err) != wantCode || !strings.HasPrefix(msg, wantPrefix) {
		t.Fatalf("ExecuteDeletion() error = %v, want %v starting with %q", err, wantCode, wantPrefix)
	}
	for _, detail := range wantDetails {
		if !strings.Contains(msg, detail) {
			t.Errorf("ExecuteDeletion() message = %q, want it to contain %q", msg, detail)
		}
	}
	stored, err := s.GetComponent(ctx, name)
	if err != nil {
		t.Fatalf("GetComponent(%s) error = %v", name, err)
	}
	if diff := cmp.Diff(before, stored, protocmp.Transform()); diff != "" {
		t.Errorf("rejected deletion changed the stored record (-before +stored):\n%s", diff)
	}
}
