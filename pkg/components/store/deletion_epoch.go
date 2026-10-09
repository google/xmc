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
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// StaleDeletionMessagePrefix starts the FailedPrecondition message returned when a sourced deletion
// does not come from the recorded source (the owner) of the component. The GitOps bridge matches
// codes.FailedPrecondition plus this prefix to release its custom resource without deleting the
// component in CMS.
const StaleDeletionMessagePrefix = "stale deletion:"

// validateDeletionSource returns an InvalidArgument error for a non-nil src that cannot order
// deletions (see ValidateIntentSource), and nil for a nil or valid one.
func validateDeletionSource(src *IntentSource) error {
	if src == nil {
		return nil
	}
	return ValidateIntentSource(*src)
}

// rejectStaleDeletion returns a FailedPrecondition error starting with StaleDeletionMessagePrefix
// when src is not the owner of rec, and nil otherwise. Only the owner may delete a sourced record:
// a deletion without a source, of a record without a recorded source, or from the recorded object
// (same UID) proceeds; one from an older object, or from a newer object that has not adopted the
// record, is rejected.
func rejectStaleDeletion(canonicalName string, rec *ComponentRecord, src *IntentSource) error {
	if src == nil || rec.Source == nil {
		return nil
	}
	relation := "an older object"
	switch classifyIntentEpoch(rec.Source, *src) {
	case epochSame:
		return nil
	case epochNewer:
		relation = "a newer object that has not adopted it"
	}
	owner := rec.Source
	return status.Errorf(codes.FailedPrecondition, StaleDeletionMessagePrefix+" component %q is owned by source %q created at %s; the deletion comes from %s, source %q created at %s", canonicalName, owner.UID, owner.CreateTime.Format(time.RFC3339Nano), relation, src.UID, src.CreateTime.Format(time.RFC3339Nano))
}
