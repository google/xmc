// Copyright 2025 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//	http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.
package shared

import (
	"testing"

	"github.com/google/go-cmp/cmp"
	"google.golang.org/protobuf/testing/protocmp"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	v1beta1 "sigs.k8s.io/kueue/apis/kueue/v1beta1"
	spb "xmanager_cloud/api/protos/experiment"
)

func TestClusterInfoFromMultiKueueCluster(t *testing.T) {
	tests := []struct {
		name string
		mc   *v1beta1.MultiKueueCluster
		want *spb.ClusterInfo
	}{
		{name: "nil cluster", mc: nil, want: nil},
		{name: "no labels", mc: &v1beta1.MultiKueueCluster{}, want: nil},
		{
			name: "all labels",
			mc: &v1beta1.MultiKueueCluster{ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{
				"xmc.google.com/project":      "worker-proj",
				"xmc.google.com/location":     "us-east4",
				"xmc.google.com/cluster-name": "worker-gke",
			}}},
			want: &spb.ClusterInfo{Project: "worker-proj", Location: "us-east4", ClusterName: "worker-gke"},
		},
		{
			name: "partial labels",
			mc:   &v1beta1.MultiKueueCluster{ObjectMeta: metav1.ObjectMeta{Labels: map[string]string{"xmc.google.com/project": "worker-proj"}}},
			want: &spb.ClusterInfo{Project: "worker-proj"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if diff := cmp.Diff(tc.want, ClusterInfoFromMultiKueueCluster(tc.mc), protocmp.Transform()); diff != "" {
				t.Errorf("ClusterInfoFromMultiKueueCluster() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}
