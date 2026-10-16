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
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"google.golang.org/genproto/googleapis/rpc/errdetails"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/testing/protocmp"
)

// UIDs of the kube-system Namespaces of two test clusters.
const (
	clusterAUID = "0c5b6f0e-5d7a-4c63-9a0e-6f2d1b3a4c01"
	clusterBUID = "0c5b6f0e-5d7a-4c63-9a0e-6f2d1b3a4c02"
)

// baseCluster is a valid cluster that test cases modify.
var baseCluster = ClusterIdentity{UID: clusterAUID, Name: "prod", Epoch: 3}

// modifiedCluster returns a copy of baseCluster changed by modify.
func modifiedCluster(modify func(*ClusterIdentity)) ClusterIdentity {
	c := baseCluster
	modify(&c)
	return c
}

func TestValidateClusterIdentity(t *testing.T) {
	tests := []struct {
		name    string
		c       ClusterIdentity
		wantErr bool
	}{
		{name: "valid cluster", c: baseCluster},
		{name: "empty uid", c: modifiedCluster(func(c *ClusterIdentity) { c.UID = "" }), wantErr: true},
		{name: "uid that is not a UUID", c: modifiedCluster(func(c *ClusterIdentity) { c.UID = "cluster-a" }), wantErr: true},
		{name: "uppercase uid", c: modifiedCluster(func(c *ClusterIdentity) { c.UID = strings.ToUpper(clusterAUID) }), wantErr: true},
		{name: "uid in braces", c: modifiedCluster(func(c *ClusterIdentity) { c.UID = "{" + clusterAUID + "}" }), wantErr: true},
		{name: "empty name", c: modifiedCluster(func(c *ClusterIdentity) { c.Name = "" }), wantErr: true},
		{name: "name that is not a DNS-1123 label", c: modifiedCluster(func(c *ClusterIdentity) { c.Name = "Prod_1" }), wantErr: true},
		{name: "name longer than a label", c: modifiedCluster(func(c *ClusterIdentity) { c.Name = strings.Repeat("n", 64) }), wantErr: true},
		{name: "zero epoch", c: modifiedCluster(func(c *ClusterIdentity) { c.Epoch = 0 }), wantErr: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateClusterIdentity(tt.c)
			if (err != nil) != tt.wantErr || (tt.wantErr && status.Code(err) != codes.InvalidArgument) {
				t.Errorf("ValidateClusterIdentity() error = %v, want InvalidArgument %t", err, tt.wantErr)
			}
		})
	}
}

func TestPlanDeclaration(t *testing.T) {
	fence := &baseCluster
	dr := func(epoch int64) ClusterIdentity { return ClusterIdentity{UID: clusterBUID, Name: "dr", Epoch: epoch} }
	tests := []struct {
		name       string
		fence      *ClusterIdentity
		c          ClusterIdentity
		want       DeclarationOutcome
		wantReason string
	}{
		{name: "first declaration creates the fence", c: baseCluster, want: DeclarationCreated},
		{name: "active cluster at the same epoch refreshes", fence: fence, c: modifiedCluster(func(c *ClusterIdentity) { c.Name = "prod-renamed" }), want: DeclarationRefreshed},
		{name: "active cluster at a higher epoch raises it", fence: fence, c: modifiedCluster(func(c *ClusterIdentity) { c.Epoch = 4 }), want: DeclarationRaised},
		{name: "active cluster at a lower epoch regressed", fence: fence, c: modifiedCluster(func(c *ClusterIdentity) { c.Epoch = 2 }), wantReason: "CLUSTER_EPOCH_REGRESSED"},
		{name: "another cluster at a higher epoch takes over", fence: fence, c: dr(4), want: DeclarationTakeover},
		{name: "another cluster at the same epoch is fenced", fence: fence, c: dr(3), wantReason: "CLUSTER_FENCED"},
		{name: "another cluster at a lower epoch is fenced", fence: fence, c: dr(1), wantReason: "CLUSTER_FENCED"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := planDeclaration(tt.fence, tt.c)
			if tt.wantReason == "" {
				if err != nil || got != tt.want {
					t.Errorf("planDeclaration() = %v, %v; want %v, nil", got, err, tt.want)
				}
				return
			}
			st := status.Convert(err)
			want := []any{&errdetails.ErrorInfo{
				Reason:   tt.wantReason,
				Domain:   ErrorInfoDomain,
				Metadata: map[string]string{"operation": "DeclareClusterEpoch", "active_cluster_uid": baseCluster.UID, "active_cluster_name": "prod", "active_epoch": "3"},
			}}
			if st.Code() != codes.FailedPrecondition {
				t.Errorf("planDeclaration() code = %v, want %v", st.Code(), codes.FailedPrecondition)
			}
			if diff := cmp.Diff(want, st.Details(), protocmp.Transform()); diff != "" {
				t.Errorf("planDeclaration() details mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestTakenOverOwner(t *testing.T) {
	owner := &SourceOwner{UID: "uid-a", Generation: 7, Namespace: "xmc", ClusterUID: clusterAUID, Epoch: 3}
	want := &SourceOwner{Generation: 7, Namespace: "xmc", ClusterUID: clusterBUID, Epoch: 4}
	if diff := cmp.Diff(want, takenOverOwner(owner, ClusterIdentity{UID: clusterBUID, Name: "dr", Epoch: 4})); diff != "" {
		t.Errorf("takenOverOwner() mismatch (-want +got):\n%s", diff)
	}
	if got := takenOverOwner(nil, baseCluster); got != nil {
		t.Errorf("takenOverOwner(nil) = %+v, want nil", got)
	}
}
