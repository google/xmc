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
	"strings"

	"github.com/google/uuid"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"k8s.io/apimachinery/pkg/util/validation"
)

// RequestSource identifies the declarative object (Kubernetes CR) that sent a request. The owner
// of a record is the (cluster, namespace) of the object that last wrote it, and UID is the
// object's incarnation within that owner. Generation orders requests within one UID.
type RequestSource struct {
	UID, Namespace, ClusterUID string
	Generation, Epoch          int64
	// ClusterName is the human-readable name of the source's cluster, used only in messages. It is
	// optional and never recorded.
	ClusterName string
	// ExpectedOwnerUID is a compare-and-swap guard: when it names the owner UID, a source with
	// another UID in the owner's namespace and cluster takes the record over. It is never recorded.
	ExpectedOwnerUID string
}

// SourceOwner is the recorded owner of a component: the source of its last owner write.
type SourceOwner struct {
	UID, Namespace, ClusterUID string
	Generation, Epoch          int64
}

// maxSourceUIDLength is the width of the SourceUid column of Components.
const maxSourceUIDLength = 64

// ValidateRequestSource returns an InvalidArgument error for a source without a valid UID (or with
// an expected owner UID wider than its column), whose namespace or set cluster name is not a
// DNS-1123 label, whose cluster UID is not a canonical UUID (validateClusterUID), or with a
// non-positive generation or epoch. The cluster name is optional, but the caller controls it and
// it is echoed in rejection messages, so a set name is bounded like ValidateClusterIdentity's.
func ValidateRequestSource(src RequestSource) error {
	switch {
	case !fitsColumn(src.UID, maxSourceUIDLength):
		return status.Errorf(codes.InvalidArgument, "request source uid must have 1 to %d bytes", maxSourceUIDLength)
	case src.Generation <= 0:
		return status.Error(codes.InvalidArgument, "request source generation must be positive")
	case src.Epoch <= 0:
		return status.Error(codes.InvalidArgument, "request source cluster epoch must be positive")
	case len(src.ExpectedOwnerUID) > maxSourceUIDLength:
		return status.Errorf(codes.InvalidArgument, "request source expected owner uid must have at most %d bytes", maxSourceUIDLength)
	}
	if err := validateDNS1123Label("request source namespace", src.Namespace); err != nil {
		return err
	}
	if src.ClusterName != "" {
		if err := validateDNS1123Label("request source cluster name", src.ClusterName); err != nil {
			return err
		}
	}
	return validateClusterUID(src.ClusterUID)
}

// validateClusterUID returns an InvalidArgument error unless uid is a canonical (lowercase,
// hyphenated) UUID, as the UID of the kube-system Namespace that identifies a cluster is.
func validateClusterUID(uid string) error {
	if u, err := uuid.Parse(uid); err != nil || u.String() != uid {
		return status.Errorf(codes.InvalidArgument, "cluster uid %q must be a lowercase hyphenated UUID, the kube-system Namespace UID", uid)
	}
	return nil
}

// validateDNS1123Label returns an InvalidArgument error naming field unless value is a DNS-1123
// label, which also bounds it to the 63-byte width of its column.
func validateDNS1123Label(field, value string) error {
	if errs := validation.IsDNS1123Label(value); len(errs) > 0 {
		return status.Errorf(codes.InvalidArgument, "%s %q must be a DNS-1123 label: %s", field, value, strings.Join(errs, "; "))
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

// isOlderGeneration reports whether src is an older generation of the owner object o (same UID). A
// nil o has no generations.
func (o *SourceOwner) isOlderGeneration(src RequestSource) bool {
	return o != nil && o.UID == src.UID && src.Generation < o.Generation
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
// intent from src, an older generation of the owner object. Only SetComponentIntent orders
// requests by generation (R3): every other sourced RPC from the owner object is always allowed, so
// that informer lag never blocks an observation, a deactivation or a deletion.
func staleGenerationError(canonicalName string, owner *SourceOwner, src RequestSource) error {
	metadata := ownerMetadata(operationSetComponentIntent, owner)
	metadata[MetadataKeyOwnerGeneration] = strconv.FormatInt(owner.Generation, 10)
	return errorWithInfo(codes.FailedPrecondition, ReasonStaleGeneration, metadata, fmt.Sprintf(
		"component %q is owned by generation %d of source %q, newer than the generation %d of the changed request",
		canonicalName, owner.Generation, owner.UID, src.Generation))
}

// checkSourceCreation applies R1 to a request of operation from src for canonicalName, which has
// no active record. A CAS guard (ExpectedOwnerUID) names an owner that no longer exists, so the
// request is rejected with SOURCE_INCARNATION_MISMATCH, with empty owner metadata, instead of
// silently creating a record.
func checkSourceCreation(operation, canonicalName string, src RequestSource) error {
	if src.ExpectedOwnerUID == "" {
		return nil
	}
	return errorWithInfo(codes.FailedPrecondition, ReasonSourceIncarnationMismatch, ownerMetadata(operation, &SourceOwner{}), fmt.Sprintf(
		"component %q has no active record, so source %q cannot take it over from expected owner %q",
		canonicalName, src.UID, src.ExpectedOwnerUID))
}

// checkSourceOwnership applies the ownership rules to a request of operation from src for the
// active record canonicalName owned by owner, and returns the rejection of a source that may not
// write it:
//   - R2: no recorded owner (written only by unsourced intents): src adopts the record.
//   - R4: an owner in another namespace keeps the record: SOURCE_NAMESPACE_CONFLICT.
//   - R2: an owner without a UID (cleared by a fence takeover) in src's namespace and cluster: src
//     adopts it. The takeover records the declaring cluster, so a fenced-out cluster cannot.
//   - R3: the owner object itself (same UID) writes; planSourcedIntent orders its generations.
//   - R5: another incarnation in the owner's namespace and cluster adopts the record when
//     ExpectedOwnerUID names the owner UID, which the caller sets after an uncached GET in its
//     cluster proves the owner object is gone. A GET cannot prove that for another cluster.
//   - R6: any other incarnation is rejected: SOURCE_INCARNATION_MISMATCH.
//
// R1 (no active record) is checkSourceCreation.
func checkSourceOwnership(operation, canonicalName string, owner *SourceOwner, src RequestSource) error {
	switch {
	case owner == nil:
		return nil
	case owner.Namespace != src.Namespace:
		return errorWithInfo(codes.FailedPrecondition, ReasonSourceNamespaceConflict, ownerMetadata(operation, owner), fmt.Sprintf(
			"component %q is owned by source %q in namespace %q; the request comes from namespace %q",
			canonicalName, owner.UID, owner.Namespace, src.Namespace))
	case owner.UID == "" && owner.ClusterUID == src.ClusterUID, owner.UID == src.UID:
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
		metadataKeyOperation:       operation,
		MetadataKeyOwnerNamespace:  owner.Namespace,
		MetadataKeyOwnerUID:        owner.UID,
		MetadataKeyOwnerClusterUID: owner.ClusterUID,
	}
}

// optionalRequestSource validates and returns the single source in srcs, or nil when srcs is empty.
// More than one source is rejected rather than silently dropped.
func optionalRequestSource(srcs []RequestSource) (*RequestSource, error) {
	if len(srcs) == 0 {
		return nil, nil
	}
	if len(srcs) > 1 {
		return nil, status.Errorf(codes.InvalidArgument, "at most one request source may be given, got %d", len(srcs))
	}
	if err := ValidateRequestSource(srcs[0]); err != nil {
		return nil, err
	}
	src := srcs[0]
	return &src, nil
}

// checkSourceMutation checks whether src may mutate the active record canonicalName owned by owner
// in operation. A nil src bypasses ownership checks; otherwise src must pass checkSourceOwnership.
// The owner object itself is always allowed at any generation: only SetComponentIntent returns
// STALE_GENERATION (R3), so informer lag never blocks an observation, deactivation or deletion.
func checkSourceMutation(operation, canonicalName string, owner *SourceOwner, src *RequestSource) error {
	if src == nil {
		return nil
	}
	return checkSourceOwnership(operation, canonicalName, owner, *src)
}

// nonIntentOwner returns the owner recorded by a non-intent mutation (UpdateObservedGeneration,
// PrepareDeactivation, CompleteDeactivation) from src on a record currently owned by existing.
// When existing is already owned by src.UID (R3), existing.Generation (the last committed intent's
// CR generation) is preserved so an observation or deactivation call never advances
// SourceGeneration past a gated SetComponentIntent. An adoption (R2 or R5) records Generation 0,
// so the adopter's next intent is never rejected as STALE_GENERATION.
func nonIntentOwner(existing *SourceOwner, src *RequestSource) *SourceOwner {
	if src == nil {
		return existing
	}
	next := ownerOf(*src)
	next.Generation = 0
	if existing != nil && existing.UID == src.UID {
		next.Generation = existing.Generation
	}
	return next
}
