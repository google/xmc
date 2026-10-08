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
	"maps"
	"slices"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"

	componentspb "xmanager_cloud/api/protos/components/v1alpha1"
)

// StaleIntentMessagePrefix starts the FailedPrecondition message returned when a changed intent
// carries a desired generation older than the stored generation. The GitOps bridge matches
// codes.FailedPrecondition plus this prefix to report the StaleIntent condition.
const StaleIntentMessagePrefix = "stale intent:"

// isUnchangedIntent reports whether applying an intent to the existing record rec would change
// nothing, in which case SetComponentIntent must be a no-op. An intent is unchanged when its
// desired generation is not newer than rec.Generation (0 means unspecified) and its spec (ignoring
// the generation field), labels and extracted dependencies equal those of rec. Both store
// implementations use this single definition so their semantics stay identical.
//
// A stale desired generation with an equal spec is a no-op, so records whose generation was bumped
// by earlier non-idempotent writes stop growing once callers pass their own generation. A changed
// intent is rejected by rejectStaleIntent when its desired generation is stale, and otherwise
// bumped to max(desiredGen, rec.Generation+1) by the stores.
func isUnchangedIntent(rec *ComponentRecord, spec *componentspb.ComponentSpec, labels map[string]string, deps []string, desiredGen int64) bool {
	if desiredGen > rec.Generation {
		return false
	}
	if !maps.Equal(rec.Labels, labels) || !slices.Equal(rec.Dependencies, deps) {
		return false
	}
	// Steady-state intents repeat the stored spec including its generation; skip the clones.
	if proto.Equal(rec.Spec, spec) {
		return true
	}
	return proto.Equal(specWithoutGeneration(rec.Spec), specWithoutGeneration(spec))
}

// rejectStaleIntent returns a FailedPrecondition error starting with StaleIntentMessagePrefix when
// desiredGen is specified and older than rec.Generation, and nil otherwise. Stores call it only for
// changed intents (after isUnchangedIntent), so a stale writer cannot overwrite a newer spec while
// a stale replay of the stored spec stays a no-op. A desired generation equal to rec.Generation is
// accepted and bumped, as is an unspecified (0) one.
func rejectStaleIntent(canonicalName string, rec *ComponentRecord, desiredGen int64) error {
	if desiredGen <= 0 || desiredGen >= rec.Generation {
		return nil
	}
	return status.Errorf(codes.FailedPrecondition, StaleIntentMessagePrefix+" component %q has generation %d, newer than the desired generation %d of the changed intent", canonicalName, rec.Generation, desiredGen)
}

// specWithoutGeneration returns spec with the generation field cleared, or nil for a nil spec. It
// returns spec itself when its generation is already 0 and a copy otherwise, so callers must treat
// the result as read-only.
func specWithoutGeneration(spec *componentspb.ComponentSpec) *componentspb.ComponentSpec {
	if spec == nil || spec.GetGeneration() == 0 {
		return spec
	}
	cp := proto.Clone(spec).(*componentspb.ComponentSpec)
	cp.Generation = 0
	return cp
}
