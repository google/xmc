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
// Package kueuesync provides functions to sync workunit scheduling information inside kueue with
// actual workunit status.
package kueuesync

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	metav1 "golang/k8s_io/apimachinery/v/v0_23/pkg/apis/meta/v1/v1"
	"golang/k8s_io/apimachinery/v/v0_23/pkg/runtime/schema/schema"
	"golang/k8s_io/client_go/v/v0_23/kubernetes/kubernetes"
	runtimeclient "golang/sigs_k8s_io/controller_runtime/v/v0_11/pkg/client/client"
	fmpb "protobuf/field_mask_go_proto"
	timestamppb "protobuf/timestamp_go_proto"
	v1alpha2 "sigs.k8s.io/jobset/api/jobset/v1alpha2"
	v1beta1 "sigs.k8s.io/kueue/apis/kueue/v1beta1"
	essmsgpb "xmanager_cloud/experiment_state_server/proto/messages_go_proto"
	"xmanager_cloud/kubernetes/common/labels"
	"xmanager_cloud/kubernetes/reconciler/shared/shared"
	"xmanager_cloud/logging/logging"
	"xmanager_cloud/storage/storage"
)

// Params holds dependencies and configuration for constructing a KueueSync Reconciler.
type Params struct {
	Client                           runtimeclient.Client
	Clientset                        kubernetes.Interface
	Storage                          storage.Storage
	UpdateBatcher                    *shared.UpdateBatcher
	KueueSyncTickDuration            time.Duration
	MaxConcurrentKueueSyncReconciles int
}

// Reconciler runs in background to sync queue position.
type Reconciler struct {
	Client                           runtimeclient.Client
	Clientset                        kubernetes.Interface
	Storage                          storage.Storage
	UpdateBatcher                    *shared.UpdateBatcher
	KueueSyncTickDuration            time.Duration
	MaxConcurrentKueueSyncReconciles int
}

// NewReconciler creates and validates a new KueueSync Reconciler.
func NewReconciler(p Params) (*Reconciler, error) {
	if p.Client == nil {
		return nil, errors.New("client is required")
	}
	if p.Clientset == nil {
		return nil, errors.New("clientset is required")
	}
	if p.UpdateBatcher == nil {
		return nil, errors.New("update batcher is required")
	}
	maxConcurrent := p.MaxConcurrentKueueSyncReconciles
	if maxConcurrent <= 0 {
		maxConcurrent = 1
	}
	return &Reconciler{
		Client:                           p.Client,
		Clientset:                        p.Clientset,
		Storage:                          p.Storage,
		UpdateBatcher:                    p.UpdateBatcher,
		KueueSyncTickDuration:            p.KueueSyncTickDuration,
		MaxConcurrentKueueSyncReconciles: maxConcurrent,
	}, nil
}

const (
	pendingWorkloadsAPIPathTemplate = "/apis/visibility.kueue.x-k8s.io/v1beta2/clusterqueues/%s/pendingworkloads"
	maxConcurrentQueueFetches       = 10
	limitForPendingWorkloadsAPICall = "10000"
)

// pendingWorkload is a summary of a pending workload in a ClusterQueue.
type pendingWorkload struct {
	Metadata               metav1.ObjectMeta `json:"metadata,omitempty"`
	PositionInClusterQueue int               `json:"positionInClusterQueue,omitempty"`
}

// pendingWorkloadList is a list of pending workloads in a ClusterQueue.
type pendingWorkloadList struct {
	Items []pendingWorkload `json:"items"`
}

// WorkloadPositionInfo contains information about a pending workload's position in the queue.
type WorkloadPositionInfo struct {
	PositionInQueue       int
	TotalWorkloadsInQueue int
	OwnerName             string
	Namespace             string
}

// ClusterQueueGVR is GVR for Kueue ClusterQueues.
var ClusterQueueGVR = schema.GroupVersionResource{
	Group:    "kueue.x-k8s.io",
	Version:  "v1beta1",
	Resource: "clusterqueues",
}

// Start implements manager.Runnable for KueueSync Reconciler.
func (r *Reconciler) Start(ctx context.Context) error {
	r.RunPositionInQueueSync(ctx)
	return nil
}

// RunPositionInQueueSync starts sync worker that periodically updates work unit notes about their
// position in the queue.
func (r *Reconciler) RunPositionInQueueSync(ctx context.Context) {
	ticker := time.NewTicker(r.KueueSyncTickDuration)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			r.updateWorkUnitNotesAboutPosition(ctx)

		case <-ctx.Done():
			logging.Debug(ctx, "Shutting down Kueue sync worker.")
			return
		}
	}
}

func (r *Reconciler) fetchPendingWorkloadsFromQueue(ctx context.Context, cqName string) ([]pendingWorkload, error) {
	var summary pendingWorkloadList
	path := fmt.Sprintf(pendingWorkloadsAPIPathTemplate, cqName)
	logging.Debug(ctx, "Getting all pending workloads from the ClusterQueue API", "path", path)

	body, err := r.Clientset.CoreV1().RESTClient().Get().AbsPath(path).Param("limit", limitForPendingWorkloadsAPICall).Do(ctx).Raw()

	if err != nil {
		return nil, fmt.Errorf("failed to get pending workloads for ClusterQueue %s: %w", cqName, err)
	}

	if err := json.Unmarshal(body, &summary); err != nil {
		return nil, fmt.Errorf("failed to unmarshal pending workloads for ClusterQueue %s: %w", cqName, err)
	}
	return summary.Items, nil
}

func (r *Reconciler) updateWorkUnitNotesAboutPosition(ctx context.Context) {
	logging.Info(ctx, "Update Kueue WorkUnit position in queue sync.")
	defer logging.Info(ctx, "Updated Kueue WorkUnit position in queue sync.")

	var cqList v1beta1.ClusterQueueList
	if err := r.Client.List(ctx, &cqList); err != nil {
		logging.Error(ctx, "Failed to list ClusterQueues from cache", "error", err)
		return
	}
	cqNames := make([]string, 0, len(cqList.Items))
	for i := range cqList.Items {
		cqNames = append(cqNames, cqList.Items[i].Name)
	}
	var workloadWaitGroup sync.WaitGroup
	var queueWaitGroup sync.WaitGroup

	queueSem := make(chan struct{}, maxConcurrentQueueFetches)
	limit := r.MaxConcurrentKueueSyncReconciles
	if limit <= 0 {
		limit = 1
	}
	workloadSem := make(chan struct{}, limit)

	for _, cqName := range cqNames {
		queueWaitGroup.Add(1)
		queueSem <- struct{}{}
		go func(cqName string) {
			defer queueWaitGroup.Done()
			defer func() { <-queueSem }()

			pendingWorkloads, err := r.fetchPendingWorkloadsFromQueue(ctx, cqName)
			if err != nil {
				logging.Error(ctx, "Failed to get pending workloads for ClusterQueue", "clusterqueue", cqName, "error", err.Error())
				return
			}

			for _, pw := range pendingWorkloads {
				logging.Debug(ctx, "Found pending workload", "clusterqueue", cqName, "positionInQueue", pw.PositionInClusterQueue, "totalNumberOfWorkloadsInQueue", len(pendingWorkloads), "owner", pw.Metadata.OwnerReferences)

				if len(pw.Metadata.OwnerReferences) != 1 {
					logging.Warn(ctx, "Pending workload should have only one owner", "clusterqueue", cqName, "workload", pw.Metadata.Name, "owner", pw.Metadata.OwnerReferences)
					continue
				}

				wpi := WorkloadPositionInfo{
					PositionInQueue:       pw.PositionInClusterQueue + 1,
					TotalWorkloadsInQueue: len(pendingWorkloads),
					OwnerName:             pw.Metadata.OwnerReferences[0].Name,
					Namespace:             pw.Metadata.Namespace,
				}

				workloadWaitGroup.Add(1)
				workloadSem <- struct{}{}
				go func(info WorkloadPositionInfo) {
					defer workloadWaitGroup.Done()
					defer func() { <-workloadSem }()

					jobSet, err := r.fetchJobSetByName(ctx, info.OwnerName, info.Namespace)
					if err != nil {
						logging.Error(ctx, "Failed to fetch JobSet", "jobset", info.OwnerName, "error", err)
						return
					}

					posUpdate, err := r.createWorkUnitExecutionUpdate(ctx, jobSet, info)
					if err != nil {
						logging.Warn(ctx, "Failed to create WorkUnitExecutionUpdate", "jobset", jobSet.GetName(), "error", err)
						return
					}

					r.UpdateBatcher.Add(ctx, posUpdate.ExperimentID, *posUpdate.Update, shared.KueueSyncSource)
				}(wpi)
			}
		}(cqName)
	}
	queueWaitGroup.Wait()
	workloadWaitGroup.Wait()
}

// WorkUnitPositionUpdate holds the update payload and parent experiment ID for queue position updates.
type WorkUnitPositionUpdate struct {
	Update       *storage.WorkUnitExecutionUpdate
	ExperimentID int64
}

func (r *Reconciler) createWorkUnitExecutionUpdate(ctx context.Context, jobSet *v1alpha2.JobSet, info WorkloadPositionInfo) (WorkUnitPositionUpdate, error) {
	parsedLabels, err := labels.ParseXMCLabels(jobSet.GetLabels())
	if err != nil {
		return WorkUnitPositionUpdate{}, fmt.Errorf("failed to get update information from JobSet %s: %w", jobSet.GetName(), err)
	}
	experimentID, workUnitID, executionID := parsedLabels.XID, parsedLabels.WID, parsedLabels.EID

	message := fmt.Sprintf("Work unit is #%d in queue. There are %d workloads in the queue total.", info.PositionInQueue, info.TotalWorkloadsInQueue)
	workUnitUpdate := &storage.WorkUnitExecutionUpdate{
		WID: workUnitID,
		EID: executionID,
		UpdateMask: &fmpb.FieldMask{
			Paths: []string{"status.scheduling_information"},
		},
		WorkUnitExecution: &essmsgpb.WorkUnitExecution{
			Status: &essmsgpb.WorkUnitStatus{
				SchedulingInformation: &essmsgpb.StructuredMessage{
					UserFacingText: message,
					Severity:       essmsgpb.StructuredMessage_INFO,
					Source:         essmsgpb.StructuredMessage_SCHEDULER,
					CreateTime:     timestamppb.New(time.Now()),
					AuxiliaryInfo: &essmsgpb.AuxiliaryInfo{
						Detail: &essmsgpb.AuxiliaryInfo_QueueInfo{
							QueueInfo: &essmsgpb.QueueInfo{
								PositionInQueue:       int32(info.PositionInQueue),
								TotalWorkloadsInQueue: int32(info.TotalWorkloadsInQueue),
							},
						},
					},
				},
			},
		},
	}
	return WorkUnitPositionUpdate{
		Update:       workUnitUpdate,
		ExperimentID: experimentID,
	}, nil
}

func (r *Reconciler) fetchJobSetByName(ctx context.Context, name string, namespace string) (*v1alpha2.JobSet, error) {
	jobSet := &v1alpha2.JobSet{}
	err := r.Client.Get(ctx, runtimeclient.ObjectKey{Namespace: namespace, Name: name}, jobSet)
	if err != nil {
		return nil, fmt.Errorf("failed to get JobSet for workload %s: %w", name, err)
	}

	return jobSet, nil
}
