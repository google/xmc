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
	"fmt"
	"strconv"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	"xmanager_cloud/pkg/components/store"
)

// ValidateClusterIdentity returns an InvalidArgument error for a cluster whose UID is not a
// canonical UUID, whose name is not a DNS-1123 label, or whose epoch is not positive. It shares the
// UID and label checks with ValidateRequestSource.
func ValidateClusterIdentity(c store.ClusterIdentity) error {
	if err := validateClusterUID(c.UID); err != nil {
		return err
	}
	if err := validateDNS1123Label("cluster name", c.Name); err != nil {
		return err
	}
	if c.Epoch <= 0 {
		return status.Error(codes.InvalidArgument, "cluster epoch must be positive")
	}
	return nil
}

// PlanDeclaration decides how a declaration by c changes fence, the current fence holder (nil
// before the first declaration). The first owner keeps the fence until another cluster declares a
// strictly higher epoch, so two clusters that declare the same epoch cannot both write.
func PlanDeclaration(fence *store.ClusterIdentity, c store.ClusterIdentity) (store.DeclarationOutcome, error) {
	switch {
	case fence == nil:
		return store.DeclarationCreated, nil
	case fence.UID == c.UID && c.Epoch == fence.Epoch:
		return store.DeclarationRefreshed, nil
	case fence.UID == c.UID && c.Epoch > fence.Epoch:
		return store.DeclarationRaised, nil
	case fence.UID == c.UID:
		return 0, fenceError(store.OperationDeclareClusterEpoch, store.ReasonClusterEpochRegressed, fence,
			fmt.Sprintf("cluster %q declared epoch %d, lower than its active epoch %d", c.Name, c.Epoch, fence.Epoch))
	case c.Epoch > fence.Epoch:
		return store.DeclarationTakeover, nil
	}
	return 0, fenceError(store.OperationDeclareClusterEpoch, store.ReasonClusterFenced, fence,
		fmt.Sprintf("cluster %q at epoch %d is fenced: cluster %q holds the fence at epoch %d; declare a higher epoch to take over", c.Name, c.Epoch, fence.Name, fence.Epoch))
}

// CheckClusterFence returns the rejection of a request of operation from src unless src comes
// from the fence holder at the fence epoch. Unsourced requests (nil src) bypass the fence. Before
// the first declaration (nil fence), and for the holder's cluster at a newer epoch, the source must
// declare its epoch first (CLUSTER_NOT_DECLARED); the holder's cluster at a lower epoch has
// regressed (CLUSTER_EPOCH_REGRESSED); any other cluster is fenced (CLUSTER_FENCED).
func CheckClusterFence(operation string, fence *store.ClusterIdentity, src *store.RequestSource) error {
	switch {
	case src == nil:
		return nil
	case fence == nil:
		return fenceError(operation, store.ReasonClusterNotDeclared, nil, fmt.Sprintf(
			"epoch %d of cluster %q is not declared; no cluster has declared the ownership fence", src.Epoch, clusterDisplayName(src)))
	case fence.UID != src.ClusterUID:
		return fenceError(operation, store.ReasonClusterFenced, fence, fmt.Sprintf(
			"cluster %q at epoch %d is fenced: cluster %q holds the fence at epoch %d", clusterDisplayName(src), src.Epoch, fence.Name, fence.Epoch))
	case src.Epoch > fence.Epoch:
		return fenceError(operation, store.ReasonClusterNotDeclared, fence, fmt.Sprintf(
			"epoch %d of cluster %q is not declared; the fence is at epoch %d", src.Epoch, clusterDisplayName(src), fence.Epoch))
	case src.Epoch < fence.Epoch:
		return fenceError(operation, store.ReasonClusterEpochRegressed, fence, fmt.Sprintf(
			"cluster %q at epoch %d is lower than its active epoch %d", clusterDisplayName(src), src.Epoch, fence.Epoch))
	}
	return nil
}

// clusterDisplayName returns the name of src's cluster for messages, or its UID when unnamed.
func clusterDisplayName(src *store.RequestSource) string {
	if src.ClusterName != "" {
		return src.ClusterName
	}
	return src.ClusterUID
}

// fenceError returns the FailedPrecondition rejection with reason against the fence holder. The
// metadata names the holder in active_* keys, which are omitted when there is no fence (nil).
func fenceError(operation, reason string, fence *store.ClusterIdentity, msg string) error {
	meta := map[string]string{store.MetadataKeyOperation: operation}
	if fence != nil {
		meta[store.MetadataKeyActiveClusterUID] = fence.UID
		meta[store.MetadataKeyActiveClusterName] = fence.Name
		meta[store.MetadataKeyActiveEpoch] = strconv.FormatInt(fence.Epoch, 10)
	}
	return store.ErrorWithInfo(codes.FailedPrecondition, reason, meta, msg)
}

// TakenOverOwner returns the owner that a fence takeover by c leaves on a record owned by owner:
// the same namespace and generation, the new cluster and epoch, and no UID (adopted under R2).
func TakenOverOwner(owner *store.SourceOwner, c store.ClusterIdentity) *store.SourceOwner {
	if owner == nil {
		return nil
	}
	return &store.SourceOwner{Generation: owner.Generation, Namespace: owner.Namespace, ClusterUID: c.UID, Epoch: c.Epoch}
}
