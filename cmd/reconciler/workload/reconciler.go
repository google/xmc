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
// Package workload provides a reconciler for Workloads.
package workload

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"regexp"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/source"
	v1beta1 "sigs.k8s.io/kueue/apis/kueue/v1beta1"
	"xmanager_cloud/internal/logging"
	"xmanager_cloud/internal/validator"
	"xmanager_cloud/kubernetes/common/labels"
	"xmanager_cloud/kubernetes/reconciler/shared/shared"
	"xmanager_cloud/storage/storage"

	fmpb "protobuf/field_mask_go_proto"
	timestamppb "protobuf/timestamp_go_proto"
	actuatorpb "xmanager_cloud/experiment_state_server/proto/messages_go_proto"
)

var (
	reconcileTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "workload_reconcile_total",
		Help: "Total number of Workload reconciliations started",
	}, []string{})

	reconcileErrorsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "workload_reconcile_errors_total",
		Help: "Total number of Workload reconciliations that returned an error",
	}, []string{})

	reconcileDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "workload_reconcile_duration_milliseconds",
		Help:    "Duration of Workload Reconcile calls in milliseconds",
		Buckets: []float64{0.01, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10, 25, 50, 100, 250, 500, 1000},
	}, []string{})

	updatesSentTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "workload_updates_sent_to_batcher_total",
		Help: "Total number of updates sent to the batcher from the Workload reconciler",
	}, []string{})

	queueNotFoundRegex = regexp.MustCompile("^LocalQueue (.*) doesn't exist$")
)

// Reconciler implements reconcile.Reconciler for Workloads.
type Reconciler struct {
	client.Client
	Scheme                      *runtime.Scheme
	Batcher                     *shared.UpdateBatcher
	ManagementClusterInfo       *actuatorpb.ClusterInfo
	MaxConcurrentReconciles     int
	DetectInadmissibleWorkUnits bool
}

func hasValidXMCLabels(ctx context.Context, workload *v1beta1.Workload) bool {
	_, _, _, err := parseXMCLabelsFromPodSet(ctx, workload)
	if errors.Is(err, labels.ErrMissingXMCLabels) {
		logging.Warn(ctx, "Workload is missing XMC labels and will be skipped", "workload", workload.GetName(), "error", err)
	} else {
		logging.Error(ctx, "Workload has invalid XMC labels and will be skipped", "workload", workload.GetName(), "error", err)
	}
	return err == nil
}

func parseXMCLabelsFromPodSet(ctx context.Context, workload *v1beta1.Workload) (int64, int64, int64, error) {
	if len(workload.Spec.PodSets) == 0 {
		logging.Error(ctx, "Workload has no PodSets", "workload", workload.GetName())
		return 0, 0, 0, fmt.Errorf("Workload has no PodSets")
	}

	return labels.ParseXMCLabels(workload.Spec.PodSets[0].Template.Labels)
}

func workloadPredicate(ctx context.Context) predicate.TypedPredicate[*v1beta1.Workload] {
	return predicate.TypedFuncs[*v1beta1.Workload]{
		CreateFunc: func(e event.TypedCreateEvent[*v1beta1.Workload]) bool {
			strippedCtx := logging.Strip(ctx)
			workload := e.Object
			if workload == nil {
				logging.Error(strippedCtx, "CreateEvent object is nil")
				return false
			}

			return hasValidXMCLabels(strippedCtx, workload)
		},
		DeleteFunc: func(e event.TypedDeleteEvent[*v1beta1.Workload]) bool {
			return false
		},
		UpdateFunc: func(e event.TypedUpdateEvent[*v1beta1.Workload]) bool {
			oldWorkload := e.ObjectOld
			newWorkload := e.ObjectNew
			if oldWorkload == nil || newWorkload == nil {
				return false
			}

			if oldWorkload.GetResourceVersion() == newWorkload.GetResourceVersion() {
				return false
			}

			generationSame := oldWorkload.Generation == newWorkload.Generation
			statusSame := areWorkloadStatusEqual(oldWorkload, newWorkload)

			if generationSame && statusSame {
				return false
			}

			strippedCtx := logging.Strip(ctx)
			if !hasValidXMCLabels(strippedCtx, oldWorkload) || !hasValidXMCLabels(strippedCtx, newWorkload) {
				return false
			}

			return true
		},
		GenericFunc: func(e event.TypedGenericEvent[*v1beta1.Workload]) bool {
			return false
		},
	}
}

// Reconcile fetches the Workload and updates the WorkUnitExecution status.
func (r *Reconciler) Reconcile(ctx context.Context, req ctrl.Request) (result ctrl.Result, recErr error) {
	ctx = logging.WithLogger(ctx, logging.NewGCPLogger(slog.LevelInfo, ""))

	logging.Info(ctx, "Reconcile Workload", "workload", req.NamespacedName)
	reconcileTotal.WithLabelValues().Inc()
	startTime := time.Now()
	defer func() {
		duration := time.Since(startTime)
		reconcileDuration.WithLabelValues().Observe(float64(duration.Nanoseconds()) / 1e6)
		if recErr != nil {
			reconcileErrorsTotal.WithLabelValues().Inc()
			logging.Error(ctx, "Reconcile Workload returned an error", "workload", req.NamespacedName, "error", recErr)
		} else {
			logging.Info(ctx, "Reconciled Workload", "workload", req.NamespacedName)
		}
	}()

	var workload v1beta1.Workload
	// This function CANNOT BE CALLED WITHOUT A STRIPPED CONTEXT. If it gets it's hand on an slog,
	// it blocks the entire logging system for some reason.
	if err := r.Get(logging.Strip(ctx), req.NamespacedName, &workload); err != nil {
		if apierrors.IsNotFound(err) {
			logging.Warn(ctx, "Workload not found, no action needed", "workload", req.NamespacedName)
			return ctrl.Result{}, nil
		}
		logging.Error(ctx, "Error fetching Workload", "workload", req.NamespacedName, "error", err)
		recErr = err
		return ctrl.Result{}, err
	}

	xid, wid, eid, err := parseXMCLabelsFromPodSet(ctx, &workload)
	if err != nil {
		logging.Error(ctx, "Failed to parse labels for Workload", "workload", workload.GetName(), "error", err)
		return ctrl.Result{}, nil
	}

	ctx = logging.WithWorkUnitExecutionName(ctx, validator.ConstructWorkUnitExecutionName(xid, wid, eid))

	update, err := ConvertWorkloadToWorkUnitExecutionUpdate(ctx, &workload, r.ManagementClusterInfo, r.Client)
	if err != nil {
		logging.Error(ctx, "Failed to convert Workload to WorkUnitExecutionUpdate", "namespace", workload.Namespace, "workload", workload.GetName(), "error", err)
		recErr = err
	}

	// with the additional information and merge the updates.
	if r.DetectInadmissibleWorkUnits {
		isWorkloadStuck, condition := r.getStuckWorkloadCondition(ctx, &workload, xid, wid, eid)
		if isWorkloadStuck {
			update = r.updateWorkUnitExecutionUpdateWithInadmissibleReason(ctx, update, wid, eid, r.getReasonForInadmissibleWorkload(ctx, condition))
		}
	}

	if update != nil {
		logging.Info(ctx, "Adding workload update to batch", "namespace", workload.Namespace, "workload", workload.GetName())
		updatesSentTotal.WithLabelValues().Inc()
		// This function CANNOT BE CALLED WITHOUT A STRIPPED CONTEXT. If it gets it's hand on an slog,
		// it blocks the entire logging system for some reason.
		r.Batcher.Add(logging.Strip(ctx), xid, *update, shared.WorkloadSource)
	}

	return ctrl.Result{}, nil
}

func (r *Reconciler) getStuckWorkloadCondition(ctx context.Context, workload *v1beta1.Workload, experimentID, workUnitID, executionID int64) (bool, metav1.Condition) {
	for _, condition := range workload.Status.Conditions {
		if condition.Type == "QuotaReserved" && condition.Status == metav1.ConditionFalse && condition.Reason == "Inadmissible" {
			return true, condition
		}
	}
	return false, metav1.Condition{}
}

func (r *Reconciler) getReasonForInadmissibleWorkload(ctx context.Context, condition metav1.Condition) string {
	matches := queueNotFoundRegex.FindStringSubmatch(condition.Message)
	if len(matches) > 1 {
		return fmt.Sprintf("Work unit is inadmissible because queue %q was not found.", matches[1])
	}
	return fmt.Sprintf("Work unit is inadmissible, please check the workload status for more details: %s", condition.Message)
}

func (r *Reconciler) updateWorkUnitExecutionUpdateWithInadmissibleReason(ctx context.Context, update *storage.WorkUnitExecutionUpdate, wid, eid int64, reasonForInadmissibleWorkload string) *storage.WorkUnitExecutionUpdate {
	if update == nil {
		update = &storage.WorkUnitExecutionUpdate{
			WID: wid,
			EID: eid,
		}
	}
	if update.UpdateMask == nil {
		update.UpdateMask = &fmpb.FieldMask{}
	}
	if update.WorkUnitExecution == nil {
		update.WorkUnitExecution = &actuatorpb.WorkUnitExecution{}
	}
	if update.WorkUnitExecution.Status == nil {
		update.WorkUnitExecution.Status = &actuatorpb.WorkUnitStatus{}
	}
	update.WorkUnitExecution.Status.SchedulingInformation = &actuatorpb.StructuredMessage{
		UserFacingText: reasonForInadmissibleWorkload,
		Severity:       actuatorpb.StructuredMessage_ERROR,
		Source:         actuatorpb.StructuredMessage_SCHEDULER,
		CreateTime:     timestamppb.New(time.Now()),
		AuxiliaryInfo:  &actuatorpb.AuxiliaryInfo{},
	}
	update.UpdateMask.Paths = append(update.UpdateMask.Paths, "status.scheduling_information")
	return update
}

// SetupWithManager sets up the controller with the Manager.
func (r *Reconciler) SetupWithManager(ctx context.Context, mgr ctrl.Manager) error {
	logging.Debug(ctx, "Setting up workload reconciler controller with MaxConcurrentReconciles", "MaxConcurrentReconciles", r.MaxConcurrentReconciles)
	c, err := controller.New("workload", mgr, controller.Options{
		Reconciler:              r,
		MaxConcurrentReconciles: r.MaxConcurrentReconciles,
	})
	if err != nil {
		return err
	}
	return c.Watch(source.Kind(mgr.GetCache(), &v1beta1.Workload{}, &handler.TypedEnqueueRequestForObject[*v1beta1.Workload]{}, workloadPredicate(ctx)))
}

func areWorkloadStatusEqual(old, new *v1beta1.Workload) bool {
	o, n := old.Status, new.Status
	if (o.ClusterName == nil) != (n.ClusterName == nil) {
		return false
	}
	if o.ClusterName != nil && n.ClusterName != nil && *o.ClusterName != *n.ClusterName {
		return false
	}

	if (o.Admission == nil) != (n.Admission == nil) {
		return false
	}
	if o.Admission != nil && n.Admission != nil {
		if o.Admission.ClusterQueue != n.Admission.ClusterQueue {
			return false
		}
		if len(o.Admission.PodSetAssignments) != len(n.Admission.PodSetAssignments) {
			return false
		}
		for i := range o.Admission.PodSetAssignments {
			oa, na := o.Admission.PodSetAssignments[i], n.Admission.PodSetAssignments[i]
			if oa.Name != na.Name {
				return false
			}
			if (oa.Count == nil) != (na.Count == nil) {
				return false
			}
			if oa.Count != nil && na.Count != nil && *oa.Count != *na.Count {
				return false
			}
			if len(oa.Flavors) != len(na.Flavors) {
				return false
			}
			for k, v := range oa.Flavors {
				if na.Flavors[k] != v {
					return false
				}
			}
		}
	}

	if len(o.Conditions) != len(n.Conditions) {
		return false
	}
	for i := range o.Conditions {
		if o.Conditions[i].Type != n.Conditions[i].Type ||
			o.Conditions[i].Status != n.Conditions[i].Status ||
			o.Conditions[i].Reason != n.Conditions[i].Reason ||
			o.Conditions[i].Message != n.Conditions[i].Message {
			return false
		}
	}

	return true
}
