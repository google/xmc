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
package kueuesync

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"google.golang.org/protobuf/testing/protocmp"
	fmpb "google.golang.org/protobuf/types/known/fieldmaskpb"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	kubernetesfake "k8s.io/client-go/kubernetes/fake"
	clientscheme "k8s.io/client-go/kubernetes/scheme"
	corev1 "k8s.io/client-go/kubernetes/typed/core/v1"
	"k8s.io/client-go/rest"
	fakerest "k8s.io/client-go/rest/fake"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	v1alpha2 "sigs.k8s.io/jobset/api/jobset/v1alpha2"
	essmsgpb "xmanager_cloud/api/protos/experiment"
	"xmanager_cloud/cmd/reconciler/clusterqueue"
	"xmanager_cloud/cmd/reconciler/shared"
	constants "xmanager_cloud/internal/kubernetes/common/constants"
	storagepkg "xmanager_cloud/internal/storage"
	"xmanager_cloud/internal/workunit"
)

const (
	ClusterQueue1 = "cq1"
)

// JobSetGVR is GVR for JobSets.
var JobSetGVR = schema.GroupVersionResource{
	Group:    "jobset.x-k8s.io",
	Version:  "v1alpha2",
	Resource: "jobsets",
}

var testPendingWorkloads = pendingWorkloadList{
	Items: []pendingWorkload{
		{
			Metadata: metav1.ObjectMeta{
				Name:      "workload1",
				Namespace: "default",
				OwnerReferences: []metav1.OwnerReference{{
					APIVersion: "jobset.x-k8s.io/v1alpha2",
					Kind:       "JobSet",
					Name:       "owner1",
				}},
			},
			PositionInClusterQueue: 0,
		},
		{
			Metadata: metav1.ObjectMeta{
				Name:      "workload2",
				Namespace: "default",
				OwnerReferences: []metav1.OwnerReference{{
					APIVersion: "jobset.x-k8s.io/v1alpha2",
					Kind:       "JobSet",
					Name:       "owner2",
				}},
			},
			PositionInClusterQueue: 1,
		},
		{
			Metadata: metav1.ObjectMeta{
				Name:      "workload3",
				Namespace: "default",
				OwnerReferences: []metav1.OwnerReference{{
					APIVersion: "jobset.x-k8s.io/v1alpha2",
					Kind:       "JobSet",
					Name:       "owner3_no_jobset",
				}},
			},
			PositionInClusterQueue: 2,
		},
		{
			Metadata: metav1.ObjectMeta{
				Name:      "workload4",
				Namespace: "default",
				OwnerReferences: []metav1.OwnerReference{{
					APIVersion: "jobset.x-k8s.io/v1alpha2",
					Kind:       "JobSet",
					Name:       "owner4_no_labels",
				}},
			},
			PositionInClusterQueue: 3,
		},
		{
			Metadata: metav1.ObjectMeta{
				Name:      "workload5_no_owner",
				Namespace: "default",
			},
			PositionInClusterQueue: 4,
		},
		{
			Metadata: metav1.ObjectMeta{
				Name:      "workload6_two_owners",
				Namespace: "default",
				OwnerReferences: []metav1.OwnerReference{{
					APIVersion: "jobset.x-k8s.io/v1alpha2",
					Kind:       "JobSet",
					Name:       "owner6_1",
				}, {
					APIVersion: "jobset.x-k8s.io/v1alpha2",
					Kind:       "JobSet",
					Name:       "owner6_2",
				}},
			},
			PositionInClusterQueue: 5,
		},
	},
}

type fakeClient struct {
	client.Client
	jobsets []v1alpha2.JobSet
}

func (f *fakeClient) List(ctx context.Context, list client.ObjectList, opts ...client.ListOption) error {
	listOpts := client.ListOptions{}
	listOpts.ApplyOptions(opts)
	if listOpts.FieldSelector != nil {
		jobSetList, ok := list.(*v1alpha2.JobSetList)
		if !ok {
			return fmt.Errorf("can only list JobSetList with field selector")
		}
		var jobsets []v1alpha2.JobSet
		for _, js := range f.jobsets {
			if listOpts.Namespace != "" && js.Namespace != listOpts.Namespace {
				continue
			}
			if listOpts.FieldSelector.Matches(fields.Set{"metadata.name": js.Name}) {
				jobsets = append(jobsets, js)
			}
		}
		jobSetList.Items = jobsets
		return nil
	}
	return f.Client.List(ctx, list, opts...)
}

// fakeCoreV1Impl provides a fake corev1.CoreV1Interface implementation that returns a fake RESTClient.
type fakeCoreV1Impl struct {
	corev1.CoreV1Interface
	fakeRestClient rest.Interface
}

func (c *fakeCoreV1Impl) RESTClient() rest.Interface {
	return c.fakeRestClient
}

// fakeClientset provides a fake kubernetes.Interface implementation that returns a fake CoreV1().
type fakeClientset struct {
	*kubernetesfake.Clientset
	fakeCoreV1 corev1.CoreV1Interface
}

func (c *fakeClientset) CoreV1() corev1.CoreV1Interface {
	return c.fakeCoreV1
}

type KueueTestFixture struct {
	Ctx           context.Context
	Reconciler    *Reconciler
	UpdateBatcher *shared.UpdateBatcher
}

func setupKueueTestClientWithFakeRestClient(t *testing.T, restClient rest.Interface) KueueTestFixture {
	t.Helper()
	ctx := context.Background()
	fcs := kubernetesfake.NewSimpleClientset()
	cs := &fakeClientset{
		Clientset: fcs,
		fakeCoreV1: &fakeCoreV1Impl{
			CoreV1Interface: fcs.CoreV1(),
			fakeRestClient:  restClient,
		},
	}
	updateBatcher := shared.NewUpdateBatcher(ctx, &shared.BatcherConfig{
		TickDuration:           1 * time.Minute,
		MaxParallelExperiments: 1,
	}, &mockStorage{})

	s := runtime.NewScheme()
	v1alpha2.AddToScheme(s)
	clientscheme.AddToScheme(s)
	cl := fake.NewClientBuilder().WithScheme(s).Build()
	cqCache := clusterqueue.NewNameCache()

	reconciler, err := NewReconciler(Params{
		Client:                           cl,
		Clientset:                        cs,
		CQCache:                          cqCache,
		UpdateBatcher:                    updateBatcher,
		KueueSyncTickDuration:            1 * time.Minute,
		MaxConcurrentKueueSyncReconciles: 100,
	})
	if err != nil {
		t.Fatalf("NewReconciler() unexpected error: %v", err)
	}
	return KueueTestFixture{
		Ctx:           ctx,
		Reconciler:    reconciler,
		UpdateBatcher: updateBatcher,
	}
}

func TestNewReconciler_Validation(t *testing.T) {
	s := runtime.NewScheme()
	v1alpha2.AddToScheme(s)
	clientscheme.AddToScheme(s)
	cl := fake.NewClientBuilder().WithScheme(s).Build()
	cs := kubernetesfake.NewSimpleClientset()
	cqCache := clusterqueue.NewNameCache()
	updateBatcher := shared.NewUpdateBatcher(t.Context(), &shared.BatcherConfig{
		TickDuration:           1 * time.Minute,
		MaxParallelExperiments: 1,
	}, &mockStorage{})

	tests := []struct {
		name    string
		params  Params
		wantErr bool
	}{
		{
			name: "valid params",
			params: Params{
				Client:        cl,
				Clientset:     cs,
				CQCache:       cqCache,
				UpdateBatcher: updateBatcher,
			},
			wantErr: false,
		},
		{
			name: "missing client",
			params: Params{
				Clientset:     cs,
				CQCache:       cqCache,
				UpdateBatcher: updateBatcher,
			},
			wantErr: true,
		},
		{
			name: "missing clientset",
			params: Params{
				Client:        cl,
				CQCache:       cqCache,
				UpdateBatcher: updateBatcher,
			},
			wantErr: true,
		},
		{
			name: "missing cq cache",
			params: Params{
				Client:        cl,
				Clientset:     cs,
				UpdateBatcher: updateBatcher,
			},
			wantErr: true,
		},
		{
			name: "missing update batcher",
			params: Params{
				Client:    cl,
				Clientset: cs,
				CQCache:   cqCache,
			},
			wantErr: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			rec, err := NewReconciler(tc.params)
			if (err != nil) != tc.wantErr {
				t.Fatalf("NewReconciler() error = %v, wantErr %v", err, tc.wantErr)
			}
			if !tc.wantErr && rec == nil {
				t.Fatal("NewReconciler() returned nil reconciler without error")
			}
		})
	}
}

func createFakeRestClient(t *testing.T, workloadsJSON []byte) *fakerest.RESTClient {
	t.Helper()
	return &fakerest.RESTClient{
		NegotiatedSerializer: serializer.NewCodecFactory(clientscheme.Scheme),
		Client: fakerest.CreateHTTPClient(func(req *http.Request) (*http.Response, error) {
			if strings.Contains(req.URL.Path, fmt.Sprintf("/clusterqueues/%s/pendingworkloads", ClusterQueue1)) {
				return &http.Response{
					StatusCode: http.StatusOK,
					Body:       io.NopCloser(bytes.NewReader(workloadsJSON)),
				}, nil
			}
			return &http.Response{StatusCode: http.StatusNotFound, Body: io.NopCloser(strings.NewReader(""))}, nil
		}),
	}
}

// TODO - (internal issue): Consider using a fake instead of a mock.
type mockStorage struct {
	storagepkg.Storage
	calls map[int64][]*storagepkg.WorkUnitExecutionUpdate
}

// UpdateWorkUnitExecutionsWithTransitions records the call like
// UpdateWorkUnitExecutions and reports no committed transitions.
func (m *mockStorage) UpdateWorkUnitExecutionsWithTransitions(ctx context.Context, xid int64, updates []*storagepkg.WorkUnitExecutionUpdate) (map[int64]*essmsgpb.WorkUnitExecution, map[int64]workunit.StateTransition, error) {
	res, err := m.UpdateWorkUnitExecutions(ctx, xid, updates)
	return res, nil, err
}

func (m *mockStorage) UpdateWorkUnitExecutions(ctx context.Context, xid int64, updates []*storagepkg.WorkUnitExecutionUpdate) (map[int64]*essmsgpb.WorkUnitExecution, error) {
	if m.calls == nil {
		m.calls = make(map[int64][]*storagepkg.WorkUnitExecutionUpdate)
	}
	m.calls[xid] = append(m.calls[xid], updates...)
	return nil, nil
}

func TestFetchPendingWorkloadsFromQueue(t *testing.T) {
	workloadsJSON, err := json.Marshal(testPendingWorkloads)
	if err != nil {
		t.Fatalf("Failed to marshal workloads: %v", err)
	}

	fakeRestClient := createFakeRestClient(t, workloadsJSON)
	fixture := setupKueueTestClientWithFakeRestClient(t, fakeRestClient)

	got, err := fixture.Reconciler.fetchPendingWorkloadsFromQueue(fixture.Ctx, ClusterQueue1)
	if err != nil {
		t.Fatalf("FetchPendingWorkloadsFromQueue() returned unexpected error: %v", err)
	}
	if diff := cmp.Diff(testPendingWorkloads.Items, got); diff != "" {
		t.Errorf("FetchPendingWorkloadsFromQueue() returned diff (-want +got):\n%s", diff)
	}
}

func TestUpdateWorkUnitNotesAboutPosition(t *testing.T) {
	workloadsJSON, err := json.Marshal(testPendingWorkloads)
	if err != nil {
		t.Fatalf("Failed to marshal workloads: %v", err)
	}

	fakeRestClient := createFakeRestClient(t, workloadsJSON)
	fixture := setupKueueTestClientWithFakeRestClient(t, fakeRestClient)
	ctx, reconciler, updateBatcher := fixture.Ctx, fixture.Reconciler, fixture.UpdateBatcher
	cqCache := clusterqueue.NewNameCache()
	cqCache.Add(ClusterQueue1)
	reconciler.CQCache = cqCache

	js1 := &v1alpha2.JobSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "owner1",
			Namespace: "default",
			Labels: map[string]string{
				constants.WorkUnitIDLabel:   "1",
				constants.ExperimentIDLabel: "101",
				constants.ExecutionIDLabel:  "1001",
			},
		},
	}
	js2 := &v1alpha2.JobSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "owner2",
			Namespace: "default",
			Labels: map[string]string{
				constants.WorkUnitIDLabel:   "2",
				constants.ExperimentIDLabel: "102",
				constants.ExecutionIDLabel:  "1002",
			},
		},
	}
	js4 := &v1alpha2.JobSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "owner4_no_labels",
			Namespace: "default",
		},
	}
	s := runtime.NewScheme()
	v1alpha2.AddToScheme(s)
	clientscheme.AddToScheme(s)
	cl := &fakeClient{
		Client: fake.NewClientBuilder().
			WithScheme(s).
			WithObjects(js1, js2, js4).
			Build(),
		jobsets: []v1alpha2.JobSet{*js1, *js2, *js4},
	}
	reconciler.Client = cl

	reconciler.updateWorkUnitNotesAboutPosition(ctx)

	wantCalls := map[int64][]storagepkg.WorkUnitExecutionUpdate{
		101: {{
			WID: 1,
			EID: 1001,
			UpdateMask: &fmpb.FieldMask{
				Paths: []string{"status.scheduling_information"},
			},
			WorkUnitExecution: &essmsgpb.WorkUnitExecution{
				Status: &essmsgpb.WorkUnitStatus{
					SchedulingInformation: &essmsgpb.StructuredMessage{
						UserFacingText: "Work unit is #1 in queue. There are 6 workloads in the queue total.",
						Severity:       essmsgpb.StructuredMessage_INFO,
						Source:         essmsgpb.StructuredMessage_SCHEDULER,
						AuxiliaryInfo: &essmsgpb.AuxiliaryInfo{
							Detail: &essmsgpb.AuxiliaryInfo_QueueInfo{
								QueueInfo: &essmsgpb.QueueInfo{
									PositionInQueue:       1,
									TotalWorkloadsInQueue: 6,
								},
							},
						},
					},
				},
			},
		}},
		102: {{
			WID: 2,
			EID: 1002,
			UpdateMask: &fmpb.FieldMask{
				Paths: []string{"status.scheduling_information"},
			},
			WorkUnitExecution: &essmsgpb.WorkUnitExecution{
				Status: &essmsgpb.WorkUnitStatus{
					SchedulingInformation: &essmsgpb.StructuredMessage{
						UserFacingText: "Work unit is #2 in queue. There are 6 workloads in the queue total.",
						Severity:       essmsgpb.StructuredMessage_INFO,
						Source:         essmsgpb.StructuredMessage_SCHEDULER,
						AuxiliaryInfo: &essmsgpb.AuxiliaryInfo{
							Detail: &essmsgpb.AuxiliaryInfo_QueueInfo{
								QueueInfo: &essmsgpb.QueueInfo{
									PositionInQueue:       2,
									TotalWorkloadsInQueue: 6,
								},
							},
						},
					},
				},
			},
		}},
	}

	sortWorkUnitExecutionUpdates := cmpopts.SortSlices(func(a, b storagepkg.WorkUnitExecutionUpdate) bool {
		return a.WID < b.WID
	})

	gotCalls := make(map[int64][]storagepkg.WorkUnitExecutionUpdate)
	for xid, widMap := range updateBatcher.Updates {
		for _, sourceMap := range widMap {
			if pending, ok := sourceMap[shared.KueueSyncSource]; ok {
				gotCalls[xid] = append(gotCalls[xid], pending.WorkUnitExecutionUpdate)
			}
		}
	}

	if diff := cmp.Diff(wantCalls, gotCalls, protocmp.Transform(), sortWorkUnitExecutionUpdates, protocmp.IgnoreFields(&essmsgpb.StructuredMessage{}, "create_time")); diff != "" {
		t.Errorf("UpdateWorkUnitNotesAboutPosition() made unexpected calls to storage (-want +got):\n%s", diff)
	}
}
