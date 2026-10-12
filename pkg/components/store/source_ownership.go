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
	"fmt"
	"strconv"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// RequestSource identifies the declarative object (Kubernetes CR) that sent a request. The owner
// of a record is the (cluster, namespace) of the object that last wrote it, and UID is the
// object's incarnation within that owner. Generation orders requests within one UID.
type RequestSource struct {
	UID, Namespace, ClusterUID string
	Generation, Epoch          int64
	// ExpectedOwnerUID is a compare-and-swap guard: when it names the owner UID, a source with
	// another UID in the owner's namespace and cluster takes the record over. It is never recorded.
	ExpectedOwnerUID string
}

// SourceOwner is the recorded owner of a component: the source of its last owner write.
type SourceOwner struct {
	UID, Namespace, ClusterUID string
	Generation, Epoch          int64
}

// Widths of the SourceUid / SourceClusterUid and SourceNamespace columns of Components.
const (
	maxSourceUIDLength       = 64
	maxSourceNamespaceLength = 63
)

// ValidateRequestSource returns an InvalidArgument error for a source without a valid UID,
// namespace or cluster UID (or an expected owner UID wider than its column), or with a
// non-positive generation or epoch.
func ValidateRequestSource(src RequestSource) error {
	switch {
	case !fitsColumn(src.UID, maxSourceUIDLength):
		return status.Errorf(codes.InvalidArgument, "request source uid must have 1 to %d bytes", maxSourceUIDLength)
	case src.Generation <= 0:
		return status.Error(codes.InvalidArgument, "request source generation must be positive")
	case !fitsColumn(src.Namespace, maxSourceNamespaceLength):
		return status.Errorf(codes.InvalidArgument, "request source namespace must have 1 to %d bytes", maxSourceNamespaceLength)
	case !fitsColumn(src.ClusterUID, maxSourceUIDLength):
		return status.Errorf(codes.InvalidArgument, "request source cluster uid must have 1 to %d bytes", maxSourceUIDLength)
	case src.Epoch <= 0:
		return status.Error(codes.InvalidArgument, "request source cluster epoch must be positive")
	case len(src.ExpectedOwnerUID) > maxSourceUIDLength:
		return status.Errorf(codes.InvalidArgument, "request source expected owner uid must have at most %d bytes", maxSourceUIDLength)
	}
	return nil
}

// fitsColumn reports whether s is non-empty and at most maxLen bytes long.
func fitsColumn(s string, maxLen int) bool {
	return s != "" && len(s) <= maxLen
}

// ownerOf returns the owner that an owner write from src records.
func ownerOf(src RequestSource) *SourceOwner {
	return &SourceOwner{UID: src.UID, Generation: src.Generation, Namespace: src.Namespace, ClusterUID: src.ClusterUID, Epoch: src.Epoch}
}

// records reports whether o already records src. A nil o records nothing.
func (o *SourceOwner) records(src RequestSource) bool {
	return o != nil && *o == *ownerOf(src)
}

// isOwnerObject reports whether src comes from the owner object o itself (same UID).
func (o *SourceOwner) isOwnerObject(src RequestSource) bool {
	return o != nil && o.UID == src.UID
}

// cloneSourceOwner returns a copy of o, or nil for a nil o.
func cloneSourceOwner(o *SourceOwner) *SourceOwner {
	if o == nil {
		return nil
	}
	cp := *o
	return &cp
}

// staleGenerationError returns the FailedPrecondition STALE_GENERATION rejection of a changed
// request of operation from src, an older generation of the owner object.
func staleGenerationError(operation, canonicalName string, owner *SourceOwner, src RequestSource) error {
	metadata := ownerMetadata(operation, owner)
	metadata["owner_generation"] = strconv.FormatInt(owner.Generation, 10)
	return errorWithInfo(codes.FailedPrecondition, ReasonStaleGeneration, metadata, fmt.Sprintf(
		"component %q is owned by generation %d of source %q, newer than the generation %d of the changed request",
		canonicalName, owner.Generation, owner.UID, src.Generation))
}

// checkSourceOwnership applies the ownership rules to a request of operation from src for the
// active record canonicalName owned by owner, and returns the rejection of a source that may not
// write it:
//   - R2: no recorded owner (written only by unsourced intents): src adopts the record.
//   - R4: an owner in another namespace keeps the record: SOURCE_NAMESPACE_CONFLICT.
//   - R3: the owner object itself (same UID) writes; planSourcedIntent orders its generations.
//   - R5: another incarnation in the owner's namespace and cluster adopts the record when
//     ExpectedOwnerUID names the owner UID, which the caller sets after an uncached GET in its
//     cluster proves the owner object is gone. A GET cannot prove that for another cluster.
//   - R6: any other incarnation is rejected: SOURCE_INCARNATION_MISMATCH.
//
// R1 (no active record) is the stores' create path.
func checkSourceOwnership(operation, canonicalName string, owner *SourceOwner, src RequestSource) error {
	switch {
	case owner == nil:
		return nil
	case owner.Namespace != src.Namespace:
		return errorWithInfo(codes.FailedPrecondition, ReasonSourceNamespaceConflict, ownerMetadata(operation, owner), fmt.Sprintf(
			"component %q is owned by source %q in namespace %q; the request comes from namespace %q",
			canonicalName, owner.UID, owner.Namespace, src.Namespace))
	case owner.UID == src.UID:
		return nil
	case owner.UID == src.ExpectedOwnerUID && owner.ClusterUID == src.ClusterUID:
		return nil
	}
	return errorWithInfo(codes.FailedPrecondition, ReasonSourceIncarnationMismatch, ownerMetadata(operation, owner), fmt.Sprintf(
		"component %q is owned by source %q in cluster %q; source %q must name it in expected_owner_uid, from the same cluster, to take it over",
		canonicalName, owner.UID, owner.ClusterUID, src.UID))
}

// ownerMetadata returns the ErrorInfo metadata of an ownership rejection of operation, naming the
// recorded owner.
func ownerMetadata(operation string, owner *SourceOwner) map[string]string {
	return map[string]string{
		"operation":         operation,
		"owner_namespace":   owner.Namespace,
		"owner_uid":         owner.UID,
		"owner_cluster_uid": owner.ClusterUID,
	}
}
