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
package workload

import (
	"context"
	"fmt"
	"sort"

	corev1 "golang/k8s_io/api/v/v0_23/core/v1/v1"
	"golang/k8s_io/apimachinery/v/v0_23/pkg/types/types"
	"golang/sigs_k8s_io/controller_runtime/v/v0_11/pkg/client/client"
	fmpb "protobuf/field_mask_go_proto"
	v1beta1 "sigs.k8s.io/kueue/apis/kueue/v1beta1"
	spb "xmanager_cloud/experiment_state_server/proto/messages_go_proto"
	"xmanager_cloud/logging/logging"
	storagepkg "xmanager_cloud/storage/storage"
)

// ConvertWorkloadToWorkUnitExecutionUpdate converts a Workload to a WorkUnitExecutionUpdate.
func ConvertWorkloadToWorkUnitExecutionUpdate(ctx context.Context, workload *v1beta1.Workload, mgmtClusterInfo *spb.ClusterInfo, reader client.Reader) (*storagepkg.WorkUnitExecutionUpdate, error) {
	_, wid, eid, err := parseXMCLabelsFromPodSet(ctx, workload)
	if err != nil {
		return nil, fmt.Errorf("failed to parse XMC labels: %w", err)
	}

	metadata := &spb.WorkUnitExecutionMetadata{}
	maskPaths := []string{
		"metadata.executing_cluster",
		"metadata.multi_kueue_cluster_name",
		"metadata.resource_flavor",
		"metadata.node_selectors",
	}

	executingCluster, multiKueueClusterName, err := getMultiKueueClusterInfo(ctx, workload, mgmtClusterInfo, reader)
	if err != nil {
		return nil, err
	}
	metadata.ExecutingCluster = executingCluster
	metadata.MultiKueueClusterName = multiKueueClusterName

	resourceFlavor := getResourceFlavorInfo(workload)
	metadata.ResourceFlavor = resourceFlavor
	if resourceFlavor != "" {
		nodeLabels, err := getNodeLabelsFromResourceFlavor(ctx, resourceFlavor, reader)
		if err != nil {
			logging.Warn(ctx, "Failed to get NodeLabels for ResourceFlavor", "resourceFlavor", resourceFlavor, "error", err)
		}
		metadata.NodeSelectors = nodeLabels
	}

	return &storagepkg.WorkUnitExecutionUpdate{
		WID: wid,
		EID: eid,
		WorkUnitExecution: &spb.WorkUnitExecution{
			Metadata: metadata,
		},
		UpdateMask: &fmpb.FieldMask{Paths: maskPaths},
	}, nil
}

// getMultiKueueClusterInfo retrieves the ClusterInfo and MultiKueueCluster name associated with the Workload.
// If the Workload's status has a ClusterName, it fetches the corresponding MultiKueueCluster
// and extracts the ClusterInfo from its labels. Otherwise, it returns the provided mgmtClusterInfo.
func getMultiKueueClusterInfo(ctx context.Context, workload *v1beta1.Workload, mgmtClusterInfo *spb.ClusterInfo, reader client.Reader) (*spb.ClusterInfo, string, error) {
	if workload.Status.ClusterName == nil || *workload.Status.ClusterName == "" {
		return mgmtClusterInfo, "", nil
	}
	clusterName := *workload.Status.ClusterName
	var mc v1beta1.MultiKueueCluster
	if err := reader.Get(logging.Strip(ctx), types.NamespacedName{Name: clusterName}, &mc); err != nil {
		return nil, "", fmt.Errorf("failed to get MultiKueueCluster %s: %w", clusterName, err)
	}

	var clusterInfo *spb.ClusterInfo
	if mc.Labels != nil {
		clusterInfo = &spb.ClusterInfo{
			Project:     mc.Labels["xmc.google.com/project"],
			Location:    mc.Labels["xmc.google.com/location"],
			ClusterName: mc.Labels["xmc.google.com/cluster-name"],
		}
	}
	return clusterInfo, clusterName, nil
}

// getResourceFlavorInfo retrieves the ResourceFlavor name from the Workload's admission status.
func getResourceFlavorInfo(workload *v1beta1.Workload) string {
	if workload.Status.Admission == nil || len(workload.Status.Admission.PodSetAssignments) == 0 {
		return ""
	}

	psAssignment := workload.Status.Admission.PodSetAssignments[0]
	flavors := psAssignment.Flavors

	if len(flavors) == 0 {
		return ""
	}
	if len(flavors) == 1 {
		for _, flavor := range flavors {
			return string(flavor)
		}
	}

	// If multiple flavors, try to prioritize CPU.
	if cpuFlavor, exists := flavors[corev1.ResourceCPU]; exists {
		return string(cpuFlavor)
	}

	// Otherwise, return any flavor deterministically (e.g., the first in sorted keys).
	var keys []corev1.ResourceName
	for k := range flavors {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		return keys[i] < keys[j]
	})
	return string(flavors[keys[0]])
}

// getNodeLabelsFromResourceFlavor retrieves the NodeLabels from the v1beta1.ResourceFlavor entity
// for a given ResourceFlavor name.
func getNodeLabelsFromResourceFlavor(ctx context.Context, flavorName string, reader client.Reader) (map[string]string, error) {
	if flavorName == "" {
		return nil, nil
	}
	var rf v1beta1.ResourceFlavor
	err := reader.Get(logging.Strip(ctx), types.NamespacedName{Name: flavorName}, &rf)
	if err != nil {
		return nil, fmt.Errorf("failed to get ResourceFlavor %s: %w", flavorName, err)
	}
	return rf.Spec.NodeLabels, nil
}
