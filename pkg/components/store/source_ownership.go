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
// namespace or cluster UID, or with a non-positive generation or epoch.
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
	metadata := map[string]string{
		"operation": operation, "owner_namespace": owner.Namespace, "owner_uid": owner.UID,
		"owner_cluster_uid": owner.ClusterUID, "owner_generation": strconv.FormatInt(owner.Generation, 10),
	}
	return errorWithInfo(codes.FailedPrecondition, ReasonStaleGeneration, metadata, fmt.Sprintf(
		"component %q is owned by generation %d of source %q, newer than the generation %d of the changed request",
		canonicalName, owner.Generation, owner.UID, src.Generation))
}
