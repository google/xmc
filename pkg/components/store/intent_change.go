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

	"google.golang.org/protobuf/proto"

	componentspb "xmanager_cloud/api/protos/components/v1alpha1"
)

// isUnchangedIntent reports whether applying an intent to the existing record rec would change
// nothing, in which case SetComponentIntent must be a no-op. An intent is unchanged when its
// desired generation is not newer than rec.Generation (0 means unspecified) and its spec (ignoring
// the generation field), labels and extracted dependencies equal those of rec. Both store
// implementations use this single definition so their semantics stay identical.
//
// A stale desired generation with an equal spec is a no-op, so records whose generation was bumped
// by earlier non-idempotent writes stop growing once callers pass their own generation. A changed
// intent is not an unchanged one and is bumped to max(desiredGen, rec.Generation+1) by the stores.
func isUnchangedIntent(rec *ComponentRecord, spec *componentspb.ComponentSpec, labels map[string]string, deps []string, desiredGen int64) bool {
	// TODO(b/548436300): reject a changed spec with 0 < desiredGen < rec.Generation (stale writer) once legacy-inflated generations are remediated.
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

// intentWrite is what applying an intent to an existing active record writes.
type intentWrite int

const (
	// writeNothing leaves the record untouched.
	writeNothing intentWrite = iota
	// writeSpec stores the intent under a bumped generation and resets the state.
	writeSpec
)

// intentPlan is the outcome of applying an intent to an existing active record.
type intentPlan struct {
	write      intentWrite
	generation int64 // Generation of the record after the write.
}

// planIntent decides how an intent ordered by desiredGen applies to the existing active record rec.
// Both stores use it so their semantics stay identical.
func planIntent(rec *ComponentRecord, spec *componentspb.ComponentSpec, labels map[string]string, deps []string, desiredGen int64) intentPlan {
	if isUnchangedIntent(rec, spec, labels, deps, desiredGen) {
		return intentPlan{write: writeNothing, generation: rec.Generation}
	}
	return intentPlan{write: writeSpec, generation: max(desiredGen, rec.Generation+1)}
}

// newIntentGeneration returns the generation of a newly created component: max(desiredGen, 1).
func newIntentGeneration(desiredGen int64) int64 {
	return max(desiredGen, 1)
}

// planLiveIntent returns the active record of canonicalName in catalog (nil when there is none)
// and the plan for applying the intent to it; a missing or soft-deleted record is planned as a
// create.
func planLiveIntent(catalog map[string]*ComponentRecord, canonicalName string, spec *componentspb.ComponentSpec, labels map[string]string, deps []string, desiredGen int64) (*ComponentRecord, intentPlan) {
	rec := catalog[canonicalName]
	if isDeletedRecord(rec) {
		return nil, intentPlan{write: writeSpec, generation: newIntentGeneration(desiredGen)}
	}
	return rec, planIntent(rec, spec, labels, deps, desiredGen)
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
