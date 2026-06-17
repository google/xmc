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
	"testing"

	"golang/cmp/cmp"
	"golang/protobuf/v2/testing/protocmp/protocmp"

	corev1 "golang/k8s_io/api/v/v0_23/core/v1/v1"
	metav1 "golang/k8s_io/apimachinery/v/v0_23/pkg/apis/meta/v1/v1"
	"golang/k8s_io/apimachinery/v/v0_23/pkg/runtime/runtime"
	"golang/sigs_k8s_io/controller_runtime/v/v0_11/pkg/client/fake/fake"
	fmpb "protobuf/field_mask_go_proto"
	spb "xmanager_cloud/experiment_state_server/proto/messages_go_proto"
	v1beta1 "sigs.k8s.io/kueue/apis/kueue/v1beta1"
	"xmanager_cloud/kubernetes/common/constants"
	storagepkg "xmanager_cloud/storage/storage"
)

var workloadSpecWithNecessaryLabels = v1beta1.WorkloadSpec{
	PodSets: []v1beta1.PodSet{
		{
			Template: corev1.PodTemplateSpec{
				ObjectMeta: metav1.ObjectMeta{
					Labels: map[string]string{
						constants.ExperimentIDLabel: "1",
						constants.WorkUnitIDLabel:   "1",
						constants.ExecutionIDLabel:  "1",
					},
				},
			},
		},
	},
}

func TestConvertWorkloadToWorkUnitExecutionUpdate(t *testing.T) {
	scheme := runtime.NewScheme()
	v1beta1.AddToScheme(scheme)

	mgmtClusterInfo := &spb.ClusterInfo{
		Project:     "mgmt-project",
		Location:    "mgmt-location",
		ClusterName: "mgmt-cluster",
	}

	executingClusterInfo := &spb.ClusterInfo{
		Project:     "exec-project",
		Location:    "exec-location",
		ClusterName: "exec-cluster",
	}

	multiKueueCluster := &v1beta1.MultiKueueCluster{
		ObjectMeta: metav1.ObjectMeta{
			Name: "worker-cluster",
			Labels: map[string]string{
				"xmc.google.com/project":      "exec-project",
				"xmc.google.com/location":     "exec-location",
				"xmc.google.com/cluster-name": "exec-cluster",
			},
		},
	}

	resourceFlavor := &v1beta1.ResourceFlavor{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-flavor",
		},
		Spec: v1beta1.ResourceFlavorSpec{
			NodeLabels: map[string]string{"test-label-key": "test-label-value"},
		},
	}

	multiKueueClusterNoLabels := &v1beta1.MultiKueueCluster{
		ObjectMeta: metav1.ObjectMeta{
			Name: "worker-cluster-no-labels",
		},
	}

	resourceFlavorNoNodeLabels := &v1beta1.ResourceFlavor{
		ObjectMeta: metav1.ObjectMeta{
			Name: "test-flavor-no-node-labels",
		},
	}

	testCases := []struct {
		name     string
		workload *v1beta1.Workload
		objs     []runtime.Object
		want     *storagepkg.WorkUnitExecutionUpdate
		wantErr  bool
	}{
		{
			name: "multikueue",
			workload: &v1beta1.Workload{
				ObjectMeta: metav1.ObjectMeta{},
				Spec:       workloadSpecWithNecessaryLabels,
				Status: v1beta1.WorkloadStatus{
					ClusterName: stringPtr("worker-cluster"),
				},
			},
			objs: []runtime.Object{multiKueueCluster},
			want: &storagepkg.WorkUnitExecutionUpdate{
				WID: 1,
				EID: 1,
				WorkUnitExecution: &spb.WorkUnitExecution{
					Metadata: &spb.WorkUnitExecutionMetadata{
						ExecutingCluster:      executingClusterInfo,
						MultiKueueClusterName: "worker-cluster",
						ResourceFlavor:        "",
						NodeSelectors:         nil,
					},
				},
				UpdateMask: &fmpb.FieldMask{Paths: []string{"metadata.executing_cluster", "metadata.multi_kueue_cluster_name", "metadata.resource_flavor", "metadata.node_selectors"}},
			},
		},
		{
			name: "no clustername",
			workload: &v1beta1.Workload{
				ObjectMeta: metav1.ObjectMeta{},
				Spec:       workloadSpecWithNecessaryLabels,
			},
			want: &storagepkg.WorkUnitExecutionUpdate{
				WID: 1,
				EID: 1,
				WorkUnitExecution: &spb.WorkUnitExecution{
					Metadata: &spb.WorkUnitExecutionMetadata{
						ExecutingCluster:      mgmtClusterInfo,
						MultiKueueClusterName: "",
						ResourceFlavor:        "",
						NodeSelectors:         nil,
					},
				},
				UpdateMask: &fmpb.FieldMask{Paths: []string{"metadata.executing_cluster", "metadata.multi_kueue_cluster_name", "metadata.resource_flavor", "metadata.node_selectors"}},
			},
		},
		{
			name: "with flavor",
			workload: &v1beta1.Workload{
				ObjectMeta: metav1.ObjectMeta{},
				Spec:       workloadSpecWithNecessaryLabels,
				Status: v1beta1.WorkloadStatus{
					Admission: &v1beta1.Admission{
						PodSetAssignments: []v1beta1.PodSetAssignment{
							{
								Flavors: map[corev1.ResourceName]v1beta1.ResourceFlavorReference{
									"cpu": "test-flavor",
								},
							},
						},
					},
				},
			},
			objs: []runtime.Object{resourceFlavor},
			want: &storagepkg.WorkUnitExecutionUpdate{
				WID: 1,
				EID: 1,
				WorkUnitExecution: &spb.WorkUnitExecution{
					Metadata: &spb.WorkUnitExecutionMetadata{
						ExecutingCluster:      mgmtClusterInfo,
						MultiKueueClusterName: "",
						ResourceFlavor:        "test-flavor",
						NodeSelectors:         map[string]string{"test-label-key": "test-label-value"},
					},
				},
				UpdateMask: &fmpb.FieldMask{Paths: []string{"metadata.executing_cluster", "metadata.multi_kueue_cluster_name", "metadata.resource_flavor", "metadata.node_selectors"}},
			},
		},
		{
			name: "multikueue with flavor",
			workload: &v1beta1.Workload{
				ObjectMeta: metav1.ObjectMeta{},
				Spec:       workloadSpecWithNecessaryLabels,
				Status: v1beta1.WorkloadStatus{
					ClusterName: stringPtr("worker-cluster"),
					Admission: &v1beta1.Admission{
						PodSetAssignments: []v1beta1.PodSetAssignment{
							{
								Flavors: map[corev1.ResourceName]v1beta1.ResourceFlavorReference{
									"cpu": "test-flavor",
								},
							},
						},
					},
				},
			},
			objs: []runtime.Object{multiKueueCluster, resourceFlavor},
			want: &storagepkg.WorkUnitExecutionUpdate{
				WID: 1,
				EID: 1,
				WorkUnitExecution: &spb.WorkUnitExecution{
					Metadata: &spb.WorkUnitExecutionMetadata{
						ExecutingCluster:      executingClusterInfo,
						MultiKueueClusterName: "worker-cluster",
						ResourceFlavor:        "test-flavor",
						NodeSelectors:         map[string]string{"test-label-key": "test-label-value"},
					},
				},
				UpdateMask: &fmpb.FieldMask{Paths: []string{"metadata.executing_cluster", "metadata.multi_kueue_cluster_name", "metadata.resource_flavor", "metadata.node_selectors"}},
			},
		},
		{
			name: "multikueue not found",
			workload: &v1beta1.Workload{
				ObjectMeta: metav1.ObjectMeta{},
				Spec:       workloadSpecWithNecessaryLabels,
				Status: v1beta1.WorkloadStatus{
					ClusterName: stringPtr("worker-cluster-not-found"),
				},
			},
			objs:    []runtime.Object{},
			wantErr: true,
		},
		{
			// Tests the scenario where a ResourceFlavor is referenced in the Workload's admission,
			// but the ResourceFlavor object itself does not exist in the cluster.
			name: "resource flavor object not found",
			workload: &v1beta1.Workload{
				ObjectMeta: metav1.ObjectMeta{},
				Spec:       workloadSpecWithNecessaryLabels,
				Status: v1beta1.WorkloadStatus{
					Admission: &v1beta1.Admission{
						PodSetAssignments: []v1beta1.PodSetAssignment{
							{
								Flavors: map[corev1.ResourceName]v1beta1.ResourceFlavorReference{
									"cpu": "flavor-not-found",
								},
							},
						},
					},
				},
			},
			objs: []runtime.Object{},
			want: &storagepkg.WorkUnitExecutionUpdate{
				WID: 1,
				EID: 1,
				WorkUnitExecution: &spb.WorkUnitExecution{
					Metadata: &spb.WorkUnitExecutionMetadata{
						ExecutingCluster:      mgmtClusterInfo,
						MultiKueueClusterName: "",
						ResourceFlavor:        "flavor-not-found",
						NodeSelectors:         nil,
					},
				},
				UpdateMask: &fmpb.FieldMask{Paths: []string{"metadata.executing_cluster", "metadata.multi_kueue_cluster_name", "metadata.resource_flavor", "metadata.node_selectors"}},
			},
		},
		{
			name: "multikueue no labels",
			workload: &v1beta1.Workload{
				ObjectMeta: metav1.ObjectMeta{},
				Spec:       workloadSpecWithNecessaryLabels,
				Status: v1beta1.WorkloadStatus{
					ClusterName: stringPtr("worker-cluster-no-labels"),
				},
			},
			objs: []runtime.Object{multiKueueClusterNoLabels},
			want: &storagepkg.WorkUnitExecutionUpdate{
				WID: 1,
				EID: 1,
				WorkUnitExecution: &spb.WorkUnitExecution{
					Metadata: &spb.WorkUnitExecutionMetadata{
						ExecutingCluster:      nil,
						MultiKueueClusterName: "worker-cluster-no-labels",
						ResourceFlavor:        "",
						NodeSelectors:         nil,
					},
				},
				UpdateMask: &fmpb.FieldMask{Paths: []string{"metadata.executing_cluster", "metadata.multi_kueue_cluster_name", "metadata.resource_flavor", "metadata.node_selectors"}},
			},
		},
		{
			name: "flavor no node labels",
			workload: &v1beta1.Workload{
				ObjectMeta: metav1.ObjectMeta{},
				Spec:       workloadSpecWithNecessaryLabels,
				Status: v1beta1.WorkloadStatus{
					Admission: &v1beta1.Admission{
						PodSetAssignments: []v1beta1.PodSetAssignment{
							{
								Flavors: map[corev1.ResourceName]v1beta1.ResourceFlavorReference{
									"cpu": "test-flavor-no-node-labels",
								},
							},
						},
					},
				},
			},
			objs: []runtime.Object{resourceFlavorNoNodeLabels},
			want: &storagepkg.WorkUnitExecutionUpdate{
				WID: 1,
				EID: 1,
				WorkUnitExecution: &spb.WorkUnitExecution{
					Metadata: &spb.WorkUnitExecutionMetadata{
						ExecutingCluster:      mgmtClusterInfo,
						MultiKueueClusterName: "",
						ResourceFlavor:        "test-flavor-no-node-labels",
						NodeSelectors:         nil,
					},
				},
				UpdateMask: &fmpb.FieldMask{Paths: []string{"metadata.executing_cluster", "metadata.multi_kueue_cluster_name", "metadata.resource_flavor", "metadata.node_selectors"}},
			},
		},
		{
			name: "with multiple flavors picks first",
			workload: &v1beta1.Workload{
				ObjectMeta: metav1.ObjectMeta{},
				Spec:       workloadSpecWithNecessaryLabels,
				Status: v1beta1.WorkloadStatus{
					Admission: &v1beta1.Admission{
						PodSetAssignments: []v1beta1.PodSetAssignment{
							{
								Flavors: map[corev1.ResourceName]v1beta1.ResourceFlavorReference{
									"argos":  "test-flavor-1",
									"nvidia": "test-flavor-2",
								},
							},
						},
					},
				},
			},
			objs: []runtime.Object{
				&v1beta1.ResourceFlavor{
					ObjectMeta: metav1.ObjectMeta{Name: "test-flavor-1"},
					Spec:       v1beta1.ResourceFlavorSpec{NodeLabels: map[string]string{"label1": "value1"}},
				},
				&v1beta1.ResourceFlavor{
					ObjectMeta: metav1.ObjectMeta{Name: "test-flavor-2"},
					Spec:       v1beta1.ResourceFlavorSpec{NodeLabels: map[string]string{"label2": "value2"}},
				},
			},
			want: &storagepkg.WorkUnitExecutionUpdate{
				WID: 1,
				EID: 1,
				WorkUnitExecution: &spb.WorkUnitExecution{
					Metadata: &spb.WorkUnitExecutionMetadata{
						ExecutingCluster:      mgmtClusterInfo,
						MultiKueueClusterName: "",
						ResourceFlavor:        "test-flavor-1",
						NodeSelectors:         map[string]string{"label1": "value1"},
					},
				},
				UpdateMask: &fmpb.FieldMask{Paths: []string{"metadata.executing_cluster", "metadata.multi_kueue_cluster_name", "metadata.resource_flavor", "metadata.node_selectors"}},
			},
		},
		{
			name: "multiple flavors no cpu",
			workload: &v1beta1.Workload{
				ObjectMeta: metav1.ObjectMeta{},
				Spec:       workloadSpecWithNecessaryLabels,
				Status: v1beta1.WorkloadStatus{
					Admission: &v1beta1.Admission{
						PodSetAssignments: []v1beta1.PodSetAssignment{
							{
								Flavors: map[corev1.ResourceName]v1beta1.ResourceFlavorReference{
									"gpu": "test-flavor-gpu",
									"tpu": "test-flavor-tpu",
								},
							},
						},
					},
				},
			},
			objs: []runtime.Object{
				&v1beta1.ResourceFlavor{
					ObjectMeta: metav1.ObjectMeta{Name: "test-flavor-gpu"},
					Spec:       v1beta1.ResourceFlavorSpec{NodeLabels: map[string]string{"label-gpu": "value-gpu"}},
				},
				&v1beta1.ResourceFlavor{
					ObjectMeta: metav1.ObjectMeta{Name: "test-flavor-tpu"},
					Spec:       v1beta1.ResourceFlavorSpec{NodeLabels: map[string]string{"label-tpu": "value-tpu"}},
				},
			},
			want: &storagepkg.WorkUnitExecutionUpdate{
				WID: 1,
				EID: 1,
				WorkUnitExecution: &spb.WorkUnitExecution{
					Metadata: &spb.WorkUnitExecutionMetadata{
						ExecutingCluster:      mgmtClusterInfo,
						MultiKueueClusterName: "",
						ResourceFlavor:        "test-flavor-gpu",
						NodeSelectors:         map[string]string{"label-gpu": "value-gpu"},
					},
				},
				UpdateMask: &fmpb.FieldMask{Paths: []string{"metadata.executing_cluster", "metadata.multi_kueue_cluster_name", "metadata.resource_flavor", "metadata.node_selectors"}},
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			fakeReader := fake.NewClientBuilder().WithScheme(scheme).WithRuntimeObjects(tc.objs...).Build()
			got, err := ConvertWorkloadToWorkUnitExecutionUpdate(context.Background(), tc.workload, mgmtClusterInfo, fakeReader)
			if (err != nil) != tc.wantErr {
				t.Errorf("ConvertWorkloadToWorkUnitExecutionUpdate() error = %v, wantErr %v", err, tc.wantErr)
				return
			}
			if diff := cmp.Diff(tc.want, got, protocmp.Transform()); diff != "" {
				t.Errorf("ConvertWorkloadToWorkUnitExecutionUpdate() returned diff (-want +got):\n%s", diff)
			}
		})
	}
}

func stringPtr(s string) *string {
	return &s
}
