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

package ownership

import (
	"maps"
	"slices"

	"google.golang.org/protobuf/proto"

	componentspb "xmanager_cloud/api/protos/components/v1alpha1"
	"xmanager_cloud/pkg/components/store"
)

// IsUnchangedIntent reports whether applying an intent to the existing record rec would change
// nothing, in which case SetComponentIntent must be a no-op. An intent is unchanged when its
// desired generation is not newer than rec.Generation (0 means unspecified) and its spec (ignoring
// the generation field), labels and extracted dependencies equal those of rec. Both store
// implementations use this single definition so their semantics stay identical.
//
// A stale desired generation with an equal spec is a no-op, so records whose generation was bumped
// by earlier non-idempotent writes stop growing once callers pass their own generation. A changed
// intent is not an unchanged one and is bumped to max(desiredGen, rec.Generation+1) by the stores.
func IsUnchangedIntent(rec *store.ComponentRecord, spec *componentspb.ComponentSpec, labels map[string]string, deps []string, desiredGen int64) bool {
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

// IntentWrite is what applying an intent writes; for a new component the plan is always WriteSpec.
type IntentWrite int

const (
	// WriteNothing leaves the record untouched.
	WriteNothing IntentWrite = iota
	// WriteOwnerOnly records the request source as owner without touching the spec or generation.
	WriteOwnerOnly
	// WriteSpec stores the intent under a bumped generation and resets the state.
	WriteSpec
)

// IntentPlan is the outcome of applying an intent: what is written and the resulting generation.
type IntentPlan struct {
	Write      IntentWrite
	Generation int64              // Generation of the record after the write.
	PrevOwner  *store.SourceOwner // Recorded owner before the intent was planned.
	Created    bool               // True when the intent creates a new active record.
}

// PlanIntent decides how an intent applies to the existing active record rec: by src for a sourced
// intent (SetSourcedComponentIntent) and by desiredGen otherwise (SetComponentIntent).
func PlanIntent(canonicalName string, rec *store.ComponentRecord, spec *componentspb.ComponentSpec, labels map[string]string, deps []string, desiredGen int64, src *store.RequestSource) (IntentPlan, error) {
	if src != nil {
		return PlanSourcedIntent(canonicalName, rec, !IsUnchangedIntent(rec, spec, labels, deps, 0), *src)
	}
	if IsUnchangedIntent(rec, spec, labels, deps, desiredGen) {
		return IntentPlan{Write: WriteNothing, Generation: rec.Generation}, nil
	}
	return IntentPlan{Write: WriteSpec, Generation: max(desiredGen, rec.Generation+1)}, nil
}

// PlanSourcedIntent decides how an intent from src applies to rec. After CheckSourceOwnership, an
// intent from the owner object (same UID) at an older generation is stale when changed and a no-op
// otherwise. Any other intent, including an adoption, is current: a changed one bumps the
// generation by 1, and an unchanged one records src as owner.
func PlanSourcedIntent(canonicalName string, rec *store.ComponentRecord, changed bool, src store.RequestSource) (IntentPlan, error) {
	if err := CheckSourceOwnership(store.OperationSetComponentIntent, canonicalName, rec.Owner, src); err != nil {
		return IntentPlan{}, err
	}
	plan := IntentPlan{Write: WriteNothing, Generation: rec.Generation}
	olderGeneration := rec.Owner.IsOlderGeneration(src)
	switch {
	case olderGeneration && changed:
		return IntentPlan{}, StaleGenerationError(canonicalName, rec.Owner, src)
	case olderGeneration:
		// An unchanged replay of an older generation keeps the newer recorded owner, even at a newer
		// fence epoch: the recorded epoch moves with the owner object's next owner write.
	case changed:
		plan.Write, plan.Generation = WriteSpec, rec.Generation+1
	case !rec.Owner.Records(src):
		plan.Write = WriteOwnerOnly
	}
	return plan, nil
}

// NewIntentGeneration returns the generation of a newly created component: 1 for a sourced intent,
// else max(desiredGen, 1).
func NewIntentGeneration(desiredGen int64, src *store.RequestSource) int64 {
	if src != nil {
		return 1
	}
	return max(desiredGen, 1)
}

// PlanLiveIntent returns the active record of canonicalName in catalog (nil when there is none)
// and the plan for applying the intent to it; a missing or soft-deleted record is planned as a
// create unless CheckSourceCreation rejects src. Both stores call it before validating
// dependencies so source rejections take precedence.
func PlanLiveIntent(catalog map[string]*store.ComponentRecord, canonicalName string, spec *componentspb.ComponentSpec, labels map[string]string, deps []string, desiredGen int64, src *store.RequestSource) (*store.ComponentRecord, IntentPlan, error) {
	rec := catalog[canonicalName]
	if store.IsDeletedRecord(rec) {
		if src != nil {
			if err := CheckSourceCreation(store.OperationSetComponentIntent, canonicalName, *src); err != nil {
				return nil, IntentPlan{}, err
			}
		}
		return nil, IntentPlan{Write: WriteSpec, Generation: NewIntentGeneration(desiredGen, src), Created: true}, nil
	}
	plan, err := PlanIntent(canonicalName, rec, spec, labels, deps, desiredGen, src)
	plan.PrevOwner = store.CloneSourceOwner(rec.Owner)
	return rec, plan, err
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
