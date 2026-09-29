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

	"golang/prometheus/client/prometheus/promauto/promauto"
	"golang/prometheus/client/prometheus/prometheus"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller"
	"sigs.k8s.io/controller-runtime/pkg/event"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	ctrlmetrics "sigs.k8s.io/controller-runtime/pkg/metrics"
	"sigs.k8s.io/controller-runtime/pkg/predicate"
	"sigs.k8s.io/controller-runtime/pkg/source"
	v1beta1 "sigs.k8s.io/kueue/apis/kueue/v1beta1"
	"xmanager_cloud/kubernetes/common/labels"
	"xmanager_cloud/kubernetes/reconciler/shared/shared"
	"xmanager_cloud/logging/logging"
	"xmanager_cloud/storage/storage"
	"xmanager_cloud/validator/validator"

	fmpb "protobuf/field_mask_go_proto"
	timestamppb "protobuf/timestamp_go_proto"
	actuatorpb "xmanager_cloud/experiment_state_server/proto/messages_go_proto"
)

var (
	reconcileTotal = promauto.With(ctrlmetrics.Registry).NewCounterVec(prometheus.CounterOpts{
		Name: "workload_reconcile_total",
		Help: "Total number of Workload reconciliations started",
	}, []string{})

	reconcileErrorsTotal = promauto.With(ctrlmetrics.Registry).NewCounterVec(prometheus.CounterOpts{
		Name: "workload_reconcile_errors_total",
		Help: "Total number of Workload reconciliations that returned an error",
	}, []string{})

	reconcileDuration = promauto.With(ctrlmetrics.Registry).NewHistogramVec(prometheus.HistogramOpts{
		Name:    "workload_reconcile_duration_milliseconds",
		Help:    "Duration of Workload Reconcile calls in milliseconds",
		Buckets: []float64{0.01, 0.05, 0.1, 0.25, 0.5, 1, 2.5, 5, 10, 25, 50, 100, 250, 500, 1000},
	}, []string{})

	updatesSentTotal = promauto.With(ctrlmetrics.Registry).NewCounterVec(prometheus.CounterOpts{
		Name: "workload_updates_sent_to_batcher_total",
		Help: "Total number of updates sent to the batcher from the Workload reconciler",
	}, []string{})

	queueNotFoundRegex = regexp.MustCompile("^LocalQueue (.*) doesn't exist$")
)

// Params holds dependencies and configuration for constructing a Workload Reconciler.
type Params struct {
	Client                  client.Client
	Scheme                  *runtime.Scheme
	Batcher                 *shared.UpdateBatcher
	ManagementClusterInfo   *actuatorpb.ClusterInfo
	MaxConcurrentReconciles int
}

// Reconciler implements reconcile.Reconciler for Workloads.
type Reconciler struct {
	client.Client
	Scheme                  *runtime.Scheme
	Batcher                 *shared.UpdateBatcher
	ManagementClusterInfo   *actuatorpb.ClusterInfo
	MaxConcurrentReconciles int
}

// NewReconciler creates and validates a new Workload Reconciler.
func NewReconciler(p Params) (*Reconciler, error) {
	if p.Client == nil {
		return nil, errors.New("client is required")
	}
	if p.Scheme == nil {
		return nil, errors.New("scheme is required")
	}
	if p.Batcher == nil {
		return nil, errors.New("batcher is required")
	}
	return &Reconciler{
		Client:                  p.Client,
		Scheme:                  p.Scheme,
		Batcher:                 p.Batcher,
		ManagementClusterInfo:   p.ManagementClusterInfo,
		MaxConcurrentReconciles: p.MaxConcurrentReconciles,
	}, nil
}

func hasValidXMCLabels(ctx context.Context, workload *v1beta1.Workload) bool {
	_, err := parseXMCLabelsFromPodSet(ctx, workload)
	if err == nil {
		return true
	}
	if errors.Is(err, labels.ErrMissingXMCLabels) {
		logging.Warn(ctx, "Workload is missing XMC labels and will be skipped", "workload", workload.GetName(), "error", err)
		return false
	}
	logging.Error(ctx, "Workload has invalid XMC labels and will be skipped", "workload", workload.GetName(), "error", err)
	return false
}

func parseXMCLabelsFromPodSet(ctx context.Context, workload *v1beta1.Workload) (labels.XMCLabels, error) {
	if len(workload.Spec.PodSets) == 0 {
		logging.Error(ctx, "Workload has no PodSets", "workload", workload.GetName())
		return labels.XMCLabels{}, fmt.Errorf("Workload has no PodSets")
	}

	return labels.ParseXMCLabels(workload.Spec.PodSets[0].Template.Labels)
}

func workloadPredicate(ctx context.Context) predicate.TypedPredicate[*v1beta1.Workload] {
	return predicate.TypedFuncs[*v1beta1.Workload]{
		CreateFunc: func(e event.TypedCreateEvent[*v1beta1.Workload]) bool {
			ctx := logging.Strip(ctx)
			if e.Object == nil {
				logging.Error(ctx, "CreateEvent object is nil")
				return false
			}
			return hasValidXMCLabels(ctx, e.Object)
		},
		DeleteFunc: func(event.TypedDeleteEvent[*v1beta1.Workload]) bool {
			return false
		},
		UpdateFunc: func(e event.TypedUpdateEvent[*v1beta1.Workload]) bool {
			return shouldProcessWorkloadUpdate(ctx, e.ObjectOld, e.ObjectNew)
		},
		GenericFunc: func(event.TypedGenericEvent[*v1beta1.Workload]) bool {
			return false
		},
	}
}

func shouldProcessWorkloadUpdate(ctx context.Context, oldWorkload, newWorkload *v1beta1.Workload) bool {
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

	ctx = logging.Strip(ctx)
	return hasValidXMCLabels(ctx, oldWorkload) && hasValidXMCLabels(ctx, newWorkload)
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

	parsedLabels, err := parseXMCLabelsFromPodSet(ctx, &workload)
	if err != nil {
		logging.Error(ctx, "Failed to parse labels for Workload", "workload", workload.GetName(), "error", err)
		return ctrl.Result{}, nil
	}
	xid, wid, eid := parsedLabels.XID, parsedLabels.WID, parsedLabels.EID

	ctx = logging.WithWorkUnitExecutionName(ctx, validator.ConstructWorkUnitExecutionName(xid, wid, eid))

	update, err := ConvertWorkloadToWorkUnitExecutionUpdate(ctx, &workload, r.ManagementClusterInfo, r.Client)
	if err != nil {
		logging.Error(ctx, "Failed to convert Workload to WorkUnitExecutionUpdate", "namespace", workload.Namespace, "workload", workload.GetName(), "error", err)
		recErr = err
	}

	isWorkloadStuck, condition := r.getStuckWorkloadCondition(ctx, &workload, xid, wid, eid)
	if isWorkloadStuck {
		update = r.updateWorkUnitExecutionUpdateWithInadmissibleReason(ctx, update, wid, eid, r.getReasonForInadmissibleWorkload(ctx, condition))
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
	if r == nil {
		return errors.New("reconciler cannot be nil")
	}
	logging.Debug(ctx, "Setting up workload reconciler controller with MaxConcurrentReconciles", "MaxConcurrentReconciles", r.MaxConcurrentReconciles)
	err := ctrl.NewControllerManagedBy(mgr).
		Named("workload").
		WatchesRawSource(source.Kind(
			mgr.GetCache(),
			&v1beta1.Workload{},
			&handler.TypedEnqueueRequestForObject[*v1beta1.Workload]{},
			workloadPredicate(ctx),
		)).
		WithOptions(controller.Options{MaxConcurrentReconciles: r.MaxConcurrentReconciles}).
		Complete(r)
	if err != nil {
		return fmt.Errorf("failed to create workload controller: %w", err)
	}
	return nil
}

func areWorkloadStatusEqual(old, new *v1beta1.Workload) bool {
	o, n := old.Status, new.Status
	if (o.ClusterName == nil) != (n.ClusterName == nil) {
		return false
	}
	if o.ClusterName != nil && n.ClusterName != nil && *o.ClusterName != *n.ClusterName {
		return false
	}

	if !admissionEqual(o.Admission, n.Admission) {
		return false
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

func admissionEqual(o, n *v1beta1.Admission) bool {
	if o == nil || n == nil {
		return o == n
	}
	if o.ClusterQueue != n.ClusterQueue {
		return false
	}
	if len(o.PodSetAssignments) != len(n.PodSetAssignments) {
		return false
	}
	for i := range o.PodSetAssignments {
		if !podSetAssignmentEqual(&o.PodSetAssignments[i], &n.PodSetAssignments[i]) {
			return false
		}
	}
	return true
}

func podSetAssignmentEqual(oa, na *v1beta1.PodSetAssignment) bool {
	if oa.Name != na.Name {
		return false
	}
	if oa.Count != na.Count {
		if oa.Count == nil || na.Count == nil || *oa.Count != *na.Count {
			return false
		}
	}
	if len(oa.Flavors) != len(na.Flavors) {
		return false
	}
	for k, v := range oa.Flavors {
		if na.Flavors[k] != v {
			return false
		}
	}
	return true
}
