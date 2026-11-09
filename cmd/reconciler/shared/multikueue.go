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
	v1beta1 "sigs.k8s.io/kueue/apis/kueue/v1beta1"
	spb "xmanager_cloud/api/protos/experiment"
	"xmanager_cloud/internal/kubernetes/common/constants"
)

// ClusterInfoFromMultiKueueCluster returns the GKE cluster behind a MultiKueueCluster,
// read from its xmc.google.com cluster labels. It returns nil when the
// MultiKueueCluster has no labels; missing individual labels leave the
// corresponding fields empty.
func ClusterInfoFromMultiKueueCluster(mc *v1beta1.MultiKueueCluster) *spb.ClusterInfo {
	if mc == nil || mc.Labels == nil {
		return nil
	}
	return &spb.ClusterInfo{
		Project:     mc.Labels[constants.MultiKueueClusterProjectLabel],
		Location:    mc.Labels[constants.MultiKueueClusterLocationLabel],
		ClusterName: mc.Labels[constants.MultiKueueClusterNameLabel],
	}
}
