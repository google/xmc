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

// ClusterIdentity identifies a Kubernetes cluster that writes Component intents: the UID of its
// kube-system Namespace, a human-readable name used only in messages, and its fencing epoch.
type ClusterIdentity struct {
	UID, Name string
	Epoch     int64
}

// DeclarationOutcome is how an accepted DeclareClusterEpoch changed the ownership fence: created,
// refreshed (heartbeat and any changed name), raised, or took over every active owned component.
type DeclarationOutcome int

const (
	DeclarationCreated DeclarationOutcome = iota + 1
	DeclarationRefreshed
	DeclarationRaised
	DeclarationTakeover
)

// DeclarationResult is the result of an accepted DeclareClusterEpoch: the outcome, the resulting
// fence holder, and the number of active components whose owner moved in a takeover.
type DeclarationResult struct {
	Outcome           DeclarationOutcome
	Active            ClusterIdentity
	RecordsReassigned int64
}

// ValidateClusterIdentity returns an InvalidArgument error for a cluster whose UID is not a
// canonical UUID, whose name is not a DNS-1123 label, or whose epoch is not positive.
func ValidateClusterIdentity(c ClusterIdentity) error {
	if u, err := uuid.Parse(c.UID); err != nil || u.String() != c.UID {
		return status.Errorf(codes.InvalidArgument, "cluster uid %q must be a lowercase hyphenated UUID, the kube-system Namespace UID", c.UID)
	}
	if errs := validation.IsDNS1123Label(c.Name); len(errs) > 0 {
		return status.Errorf(codes.InvalidArgument, "cluster name %q must be a DNS-1123 label: %s", c.Name, strings.Join(errs, "; "))
	}
	if c.Epoch <= 0 {
		return status.Error(codes.InvalidArgument, "cluster epoch must be positive")
	}
	return nil
}

// planDeclaration decides how a declaration by c changes fence, the current fence holder (nil
// before the first declaration). The first owner keeps the fence until another cluster declares a
// strictly higher epoch, so two clusters that declare the same epoch cannot both write.
func planDeclaration(fence *ClusterIdentity, c ClusterIdentity) (DeclarationOutcome, error) {
	switch {
	case fence == nil:
		return DeclarationCreated, nil
	case fence.UID == c.UID && c.Epoch == fence.Epoch:
		return DeclarationRefreshed, nil
	case fence.UID == c.UID && c.Epoch > fence.Epoch:
		return DeclarationRaised, nil
	case fence.UID == c.UID:
		return 0, fenceError(ReasonClusterEpochRegressed, fence,
			fmt.Sprintf("cluster %q declared epoch %d, lower than its active epoch %d", c.Name, c.Epoch, fence.Epoch))
	case c.Epoch > fence.Epoch:
		return DeclarationTakeover, nil
	}
	return 0, fenceError(ReasonClusterFenced, fence,
		fmt.Sprintf("cluster %q at epoch %d is fenced: cluster %q holds the fence at epoch %d; declare a higher epoch to take over", c.Name, c.Epoch, fence.Name, fence.Epoch))
}

// fenceError returns the FailedPrecondition rejection with reason against the fence holder.
func fenceError(reason string, fence *ClusterIdentity, msg string) error {
	meta := map[string]string{"operation": "DeclareClusterEpoch", "active_cluster_name": fence.Name, "active_epoch": strconv.FormatInt(fence.Epoch, 10)}
	return errorWithInfo(codes.FailedPrecondition, reason, meta, msg)
}

// takenOverOwner returns the owner that a fence takeover by c leaves on a record owned by owner:
// the same namespace and generation, the new cluster and epoch, and no UID (adopted under R2).
func takenOverOwner(owner *SourceOwner, c ClusterIdentity) *SourceOwner {
	if owner == nil {
		return nil
	}
	return &SourceOwner{Generation: owner.Generation, Namespace: owner.Namespace, ClusterUID: c.UID, Epoch: c.Epoch}
}
