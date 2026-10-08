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
	"context"
	"maps"
	"slices"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"

	componentspb "xmanager_cloud/api/protos/components/v1alpha1"
	"xmanager_cloud/internal/logging"
)

// StaleIntentMessagePrefix starts the FailedPrecondition message returned when a changed sourced
// intent is older than the recorded source of the component.
const StaleIntentMessagePrefix = "stale intent:"

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
	// writeSourceOnly records the intent source without touching the spec, generation or state.
	writeSourceOnly
	// writeSpec stores the intent under a bumped generation and resets the state.
	writeSpec
)

// intentPlan is the outcome of applying an intent to an existing active record.
type intentPlan struct {
	write      intentWrite
	generation int64 // Generation of the record after the write.
	// epochChange is true when a sourced intent adopts a newer object (or a record without a
	// source), whose source previousSource then replaces.
	epochChange    bool
	previousSource *IntentSource
}

// planIntent decides how an intent applies to the existing active record rec. A sourced intent
// (SetSourcedComponentIntent) is ordered by the epoch of src. An unsourced intent
// (SetComponentIntent) is ordered by desiredGen: against rec.Generation for a record without a
// source, and against the recorded source generation otherwise, because desiredGen then comes from
// the same declarative object as that source while rec.Generation is independent of it. Both
// stores use it so their semantics stay identical.
func planIntent(canonicalName string, rec *ComponentRecord, spec *componentspb.ComponentSpec, labels map[string]string, deps []string, desiredGen int64, src *IntentSource) (intentPlan, error) {
	if src != nil {
		return planSourcedIntent(canonicalName, rec, !isUnchangedIntent(rec, spec, labels, deps, 0), *src)
	}
	if rec.Source != nil {
		return planUnsourcedIntentOnSourcedRecord(canonicalName, rec, !isUnchangedIntent(rec, spec, labels, deps, 0), desiredGen)
	}
	if isUnchangedIntent(rec, spec, labels, deps, desiredGen) {
		return intentPlan{write: writeNothing, generation: rec.Generation}, nil
	}
	return intentPlan{write: writeSpec, generation: max(desiredGen, rec.Generation+1)}, nil
}

// planUnsourcedIntentOnSourcedRecord decides how an unsourced intent applies to rec, which has a
// recorded source, given whether it changes the stored spec, labels or dependencies. A changed
// intent is stale when desiredGen is specified and older than the recorded source generation, and
// otherwise bumps the generation by 1. The recorded source is kept.
func planUnsourcedIntentOnSourcedRecord(canonicalName string, rec *ComponentRecord, changed bool, desiredGen int64) (intentPlan, error) {
	if !changed {
		return intentPlan{write: writeNothing, generation: rec.Generation}, nil
	}
	if desiredGen > 0 && desiredGen < rec.Source.Generation {
		return intentPlan{}, status.Errorf(codes.FailedPrecondition, StaleIntentMessagePrefix+" component %q was adopted from generation %d of source %q, newer than the desired generation %d of the changed intent", canonicalName, rec.Source.Generation, rec.Source.UID, desiredGen)
	}
	return intentPlan{write: writeSpec, generation: rec.Generation + 1}, nil
}

// planSourcedIntent decides how an intent from src applies to rec, given whether it changes the
// stored spec, labels or dependencies.
func planSourcedIntent(canonicalName string, rec *ComponentRecord, changed bool, src IntentSource) (intentPlan, error) {
	epoch := classifyIntentEpoch(rec.Source, src)
	if err := rejectStaleSource(canonicalName, rec, changed, src, epoch); err != nil {
		return intentPlan{}, err
	}
	plan := intentPlan{write: writeNothing, generation: rec.Generation, epochChange: epoch == epochNewer, previousSource: rec.Source}
	switch {
	case epoch == epochSame && src.Generation < rec.Source.Generation:
		// An unchanged replay of an older generation keeps the newer recorded source.
	case changed:
		plan.write, plan.generation = writeSpec, rec.Generation+1
	case rec.Source == nil || !sameIntentSource(*rec.Source, src):
		plan.write = writeSourceOnly
	}
	return plan, nil
}

// rejectStaleSource returns a FailedPrecondition error starting with StaleIntentMessagePrefix for
// an intent from an older object than the recorded source, or for a changed intent from an older
// generation of the same object, and nil otherwise.
func rejectStaleSource(canonicalName string, rec *ComponentRecord, changed bool, src IntentSource, epoch intentEpoch) error {
	switch {
	case epoch == epochOlder:
		return status.Errorf(codes.FailedPrecondition, StaleIntentMessagePrefix+" component %q was adopted from source %q created at %s, newer than source %q created at %s", canonicalName, rec.Source.UID, rec.Source.CreateTime.Format(time.RFC3339Nano), src.UID, src.CreateTime.Format(time.RFC3339Nano))
	case epoch == epochSame && changed && src.Generation < rec.Source.Generation:
		return status.Errorf(codes.FailedPrecondition, StaleIntentMessagePrefix+" component %q was adopted from generation %d of source %q, newer than the generation %d of the changed intent", canonicalName, rec.Source.Generation, src.UID, src.Generation)
	}
	return nil
}

// reportIntentEpochChange logs and counts (xmc_cms_component_epoch_changes_total), after a
// committed write, that a sourced intent adopted a newer object (or a record without a source) for
// the component canonicalName. Callers run it once per commit, outside any retried transaction.
func reportIntentEpochChange(ctx context.Context, canonicalName string, plan intentPlan, src *IntentSource) {
	if !plan.epochChange || src == nil {
		return
	}
	countIntentEpochChange(plan.previousSource)
	var prevUID, prevCreateTime string
	if prev := plan.previousSource; prev != nil {
		prevUID, prevCreateTime = prev.UID, prev.CreateTime.Format(time.RFC3339Nano)
	}
	logging.Info(ctx, "Component intent source epoch changed", "name", canonicalName, "previousUid", prevUID, "previousCreateTime", prevCreateTime, "uid", src.UID, "createTime", src.CreateTime.Format(time.RFC3339Nano), "generation", plan.generation)
}

// intentEpoch orders the source of an intent against the recorded source of a component.
type intentEpoch int

const (
	// epochSame means both come from the same object.
	epochSame intentEpoch = iota
	// epochNewer means the intent comes from a newer object, or no source is recorded.
	epochNewer
	// epochOlder means the intent comes from an older object.
	epochOlder
)

// classifyIntentEpoch orders src against the recorded source, which is nil for records never
// written by a sourced intent. Objects are ordered by creation time; Kubernetes creation times
// have one-second granularity, so equal times are ordered by UID to keep the outcome deterministic
// and the same on every replica.
func classifyIntentEpoch(recorded *IntentSource, src IntentSource) intentEpoch {
	switch {
	case recorded == nil:
		return epochNewer
	case recorded.UID == src.UID:
		return epochSame
	case src.CreateTime.After(recorded.CreateTime):
		return epochNewer
	case src.CreateTime.Before(recorded.CreateTime):
		return epochOlder
	case src.UID > recorded.UID:
		return epochNewer
	default:
		return epochOlder
	}
}

// sameIntentSource reports whether a and b identify the same object at the same generation.
func sameIntentSource(a, b IntentSource) bool {
	return a.UID == b.UID && a.Generation == b.Generation && a.CreateTime.Equal(b.CreateTime)
}

// maxIntentSourceUIDLength is the width of the Components.SourceUid column.
const maxIntentSourceUIDLength = 64

// ValidateIntentSource returns an InvalidArgument error for a source that cannot order intents:
// one without a UID (or with one wider than the SourceUid column), a creation time, or a positive
// generation. SetSourcedComponentIntent applies it; API layers may call it to validate early.
func ValidateIntentSource(src IntentSource) error {
	switch {
	case src.UID == "" || len(src.UID) > maxIntentSourceUIDLength:
		return status.Errorf(codes.InvalidArgument, "intent source uid must have 1 to %d bytes", maxIntentSourceUIDLength)
	case src.CreateTime.IsZero():
		return status.Error(codes.InvalidArgument, "intent source create time is required")
	case src.Generation <= 0:
		return status.Error(codes.InvalidArgument, "intent source generation must be positive")
	}
	return nil
}

// newIntentGeneration returns the generation of a newly created component: 1 for a sourced intent,
// whose generations are independent of the source, else max(desiredGen, 1).
func newIntentGeneration(desiredGen int64, src *IntentSource) int64 {
	if src != nil {
		return 1
	}
	return max(desiredGen, 1)
}

// cloneIntentSource returns a copy of src with CreateTime in UTC and truncated to microseconds, the
// precision Spanner stores, so that a replayed source compares equal to the recorded one. It
// returns nil for a nil src.
func cloneIntentSource(src *IntentSource) *IntentSource {
	if src == nil {
		return nil
	}
	cp := *src
	cp.CreateTime = cp.CreateTime.UTC().Truncate(time.Microsecond)
	return &cp
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
