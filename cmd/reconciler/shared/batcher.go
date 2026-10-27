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
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	ctrlmetrics "sigs.k8s.io/controller-runtime/pkg/metrics"
	essmsgpb "xmanager_cloud/api/protos/experiment"
	taskqueuepb "xmanager_cloud/api/protos/task_queue"
	"xmanager_cloud/internal/logging"
	storagepkg "xmanager_cloud/internal/storage"
	"xmanager_cloud/internal/validator"
	"xmanager_cloud/internal/workunit"
)

// UpdateSource indicates the origin of a WorkUnitExecutionUpdate.
type UpdateSource int

const (
	// JobsetSource means the update came from the jobset reconciler.
	JobsetSource UpdateSource = iota
	// WorkloadSource means the update came from the workload reconciler.
	WorkloadSource
	// KueueSyncSource means the update came from kueue sync.
	KueueSyncSource
)

// MetricsFactory registers reconciler metrics with the controller-runtime registry,
// which is served by the manager's metrics endpoint.
var MetricsFactory = promauto.With(ctrlmetrics.Registry)

var (
	updatesAddedTotal = MetricsFactory.NewCounterVec(prometheus.CounterOpts{
		Name: "batcher_updates_added_total",
		Help: "Total number of updates added to the batcher",
	}, []string{"xid"})

	batchesProcessedTotal = MetricsFactory.NewCounterVec(prometheus.CounterOpts{
		Name: "batcher_cycles_processed_total",
		Help: "Total number of batch processing cycles completed",
	}, []string{})

	updatesProcessedTotal = MetricsFactory.NewCounterVec(prometheus.CounterOpts{
		Name: "batcher_updates_processed_total",
		Help: "Total number of work unit updates processed",
	}, []string{"xid"})

	experimentsPerCycle = MetricsFactory.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "batcher_experiments_per_cycle",
		Help:    "Distribution of the number of experiments processed in each cycle",
		Buckets: prometheus.ExponentialBuckets(1, 2, 10),
	}, []string{})

	workunitsPerExperiment = MetricsFactory.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "batcher_workunits_per_experiment",
		Help:    "Distribution of the number of work unit updates per experiment in each cycle",
		Buckets: prometheus.ExponentialBuckets(1, 2, 10),
	}, []string{"xid"})

	batchProcessingDuration = MetricsFactory.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "batcher_cycle_duration_seconds",
		Help:    "Duration of the batch processing cycle",
		Buckets: prometheus.DefBuckets,
	}, []string{})

	updateWorkUnitExecutionsCalls = MetricsFactory.NewCounterVec(
		prometheus.CounterOpts{
			Name: "xmc_actuator_batcher_update_calls_total",
			Help: "Total number of calls to UpdateWorkUnitExecutions per experiment.",
		},
		[]string{"xid"},
	)

	updateWorkUnitExecutionsSuccess = MetricsFactory.NewCounterVec(
		prometheus.CounterOpts{
			Name: "xmc_actuator_batcher_update_success_total",
			Help: "Total number of successful calls to UpdateWorkUnitExecutions per experiment.",
		},
		[]string{"xid"},
	)

	updateWorkUnitExecutionsFailures = MetricsFactory.NewCounterVec(
		prometheus.CounterOpts{
			Name: "xmc_actuator_batcher_update_failures_total",
			Help: "Total number of failed calls to UpdateWorkUnitExecutions per experiment.",
		},
		[]string{"xid", "reason"},
	)

	updateWorkUnitExecutionsDuration = MetricsFactory.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "xmc_actuator_batcher_update_duration_seconds",
			Help:    "Duration of UpdateWorkUnitExecutions calls.",
			Buckets: prometheus.DefBuckets,
		},
		[]string{"xid"},
	)

	updatesPerBatch = MetricsFactory.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "xmc_actuator_batcher_updates_per_batch",
			Help:    "Number of work unit updates per batch call to UpdateWorkUnitExecutions.",
			Buckets: prometheus.ExponentialBuckets(1, 2, 10), // Buckets from 1 to 512
		},
		[]string{"xid"},
	)

	lifecycleEventsPublishedTotal = MetricsFactory.NewCounterVec(
		prometheus.CounterOpts{
			Name: "xmc_actuator_batcher_lifecycle_events_published_total",
			Help: "Total number of artifact lifecycle events published to Redis.",
		},
		[]string{"state"},
	)
)

// StorageUpdater defines the interface for updating work unit executions.
type StorageUpdater interface {
	UpdateWorkUnitExecutions(ctx context.Context, xid int64, updates []*storagepkg.WorkUnitExecutionUpdate) (map[int64]*essmsgpb.WorkUnitExecution, error)
}

// BatcherConfig holds configuration for the UpdateBatcher.
type BatcherConfig struct {
	TickDuration           time.Duration
	MaxParallelExperiments int
}

// UpdateBatcher is responsible for batching updates and processing them.
type UpdateBatcher struct {
	Updates map[int64]map[int64]map[UpdateSource]storagepkg.WorkUnitExecutionUpdate
	// UpdatesMu guards Updates and retries. Sharing one lock lets each tick
	// snapshot pending updates and merge due retries atomically.
	UpdatesMu              sync.Mutex
	tickDuration           time.Duration
	maxParallelExperiments int
	Storage                StorageUpdater // Use an interface for testability
	LifecycleNotifier      LifecycleNotifier

	// retries holds updates whose flush failed transiently, keyed by
	// (xid, wid, source). Guarded by UpdatesMu; lazily initialized.
	retries  map[slotKey]retryEntry
	removed  map[slotKey]int64
	flushing bool
	// now returns the current time; nil means time.Now. Injectable for tests.
	now func() time.Time
}

// NewUpdateBatcher creates a new UpdateBatcher.
func NewUpdateBatcher(ctx context.Context, cfg *BatcherConfig, storage StorageUpdater) *UpdateBatcher {
	maxParallelExperiments := cfg.MaxParallelExperiments
	if maxParallelExperiments <= 0 {
		logging.Warn(ctx, "MaxParallelExperiments was <= 0, defaulting to 1", "maxParallelExperiments", maxParallelExperiments)
		maxParallelExperiments = 1
	}
	return &UpdateBatcher{
		Updates:                make(map[int64]map[int64]map[UpdateSource]storagepkg.WorkUnitExecutionUpdate),
		tickDuration:           cfg.TickDuration,
		maxParallelExperiments: maxParallelExperiments,
		Storage:                storage,
	}
}

// Add adds an update to the batch.
func (b *UpdateBatcher) Add(ctx context.Context, xid int64, update storagepkg.WorkUnitExecutionUpdate, source UpdateSource) {
	updatesAddedTotal.WithLabelValues(fmt.Sprintf("%d", xid)).Inc()
	b.UpdatesMu.Lock()
	defer b.UpdatesMu.Unlock()
	if _, exists := b.Updates[xid]; !exists {
		b.Updates[xid] = make(map[int64]map[UpdateSource]storagepkg.WorkUnitExecutionUpdate)
	}
	if _, exists := b.Updates[xid][update.WID]; !exists {
		b.Updates[xid][update.WID] = make(map[UpdateSource]storagepkg.WorkUnitExecutionUpdate)
	}
	// Storage skips updates whose EID is not the current execution, so letting a
	// stale (lower-EID) update replace a pending newer one would silently drop the
	// newer execution's update.
	if pending, exists := b.Updates[xid][update.WID][source]; exists {
		if update.EID < pending.EID {
			return
		}
		if update.EID == pending.EID && pending.ForceNotify {
			update.ForceNotify = true
		}
	}
	b.Updates[xid][update.WID][source] = update
}

// Remove evicts any pending or queued-for-retry update for (xid, wid, source)
// whose EID is <= eid. Used after a terminal status is persisted synchronously
// on deletion so a stale pending update cannot flush later without triggering a
// second Spanner write.
func (b *UpdateBatcher) Remove(xid, wid, eid int64, source UpdateSource) {
	b.UpdatesMu.Lock()
	defer b.UpdatesMu.Unlock()
	b.removeRetryLocked(slotKey{xid: xid, wid: wid, source: source}, eid)
	byWID, ok := b.Updates[xid]
	if !ok {
		return
	}
	bySource, ok := byWID[wid]
	if !ok {
		return
	}
	if pending, ok := bySource[source]; !ok || pending.EID > eid {
		return
	}
	delete(bySource, source)
	if len(bySource) == 0 {
		delete(byWID, wid)
	}
	if len(byWID) == 0 {
		delete(b.Updates, xid)
	}
}

// BatchProcessor runs the background processing loop.
func (b *UpdateBatcher) BatchProcessor(ctx context.Context) error {
	ctx = logging.WithLogger(ctx, logging.NewGCPLogger(slog.LevelInfo, ""))

	logging.Debug(ctx, "Starting batch processor")
	ticker := time.NewTicker(b.tickDuration)
	defer ticker.Stop()
	defer logging.Debug(ctx, "Stopping batch processor")

	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			b.processBatches(ctx)
		}
	}
}

// processBatches processes one batch of updates, including retries that are due.
// Returns true if any batches were processed. It is only called from the single
// BatchProcessor goroutine and never runs concurrently with itself; Add and the
// per-experiment flush goroutines may run concurrently with it.
func (b *UpdateBatcher) processBatches(ctx context.Context) bool {
	startTime := time.Now()
	defer func() {
		duration := time.Since(startTime)
		batchProcessingDuration.WithLabelValues().Observe(duration.Seconds())
	}()
	pending, retries := b.takeSnapshots()
	if len(pending) == 0 {
		return false
	}
	defer b.finishFlush()

	logging.Info(ctx, "Processing batches", "numExperiments", len(pending))
	batchesProcessedTotal.WithLabelValues().Inc()
	experimentsPerCycle.WithLabelValues().Observe(float64(len(pending)))

	sem := make(chan struct{}, b.maxParallelExperiments)
	var wg sync.WaitGroup
	for xid, widMap := range pending {
		xidRetries := retries[xid]
		wg.Add(1)
		sem <- struct{}{} // Acquire semaphore slot
		go func() {
			defer func() {
				<-sem // Release semaphore slot
				wg.Done()
			}()
			b.processExperimentUpdates(ctx, xid, widMap, xidRetries)
		}()
	}
	wg.Wait()
	return true
}

func (b *UpdateBatcher) processExperimentUpdates(ctx context.Context, xid int64, widMap map[int64]map[UpdateSource]storagepkg.WorkUnitExecutionUpdate, retries []batchEntry) {
	ctx = logging.WithExperimentName(ctx, validator.ConstructExperimentName(xid))
	xidStr := fmt.Sprintf("%d", xid)
	if len(retries) > 0 {
		updatesRetriedTotal.WithLabelValues(xidStr).Add(float64(len(retries)))
	}
	batch := buildExperimentBatch(widMap, retries)
	if len(batch) == 0 {
		return
	}
	updatesProcessedTotal.WithLabelValues(xidStr).Add(float64(len(batch)))
	workunitsPerExperiment.WithLabelValues(xidStr).Observe(float64(countWorkUnits(batch)))

	if b.Storage == nil {
		logging.Error(ctx, "Storage is not initialized in UpdateBatcher")
		updateWorkUnitExecutionsFailures.WithLabelValues(xidStr, "storage_nil").Inc()
		return
	}

	updatesPerBatch.WithLabelValues(xidStr).Observe(float64(len(batch)))
	if err := b.callUpdateWorkUnitExecutions(ctx, xid, updatePointers(batch)); err != nil {
		b.handleFlushFailure(ctx, xid, batch, err)
	}
}

// callUpdateWorkUnitExecutions sends updatesToSend to storage, records call
// metrics, and returns the storage error, if any.
func (b *UpdateBatcher) callUpdateWorkUnitExecutions(ctx context.Context, xid int64, updatesToSend []*storagepkg.WorkUnitExecutionUpdate) error {
	ctx = logging.WithExperimentName(ctx, validator.ConstructExperimentName(xid))

	xidStr := fmt.Sprintf("%d", xid)
	updateWorkUnitExecutionsCalls.WithLabelValues(xidStr).Inc()
	storageCallStartTime := time.Now()

	taskCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	logging.Info(taskCtx, "Update work unit executions for xid", "xid", xid, "numUpdates", len(updatesToSend))
	var res map[int64]*essmsgpb.WorkUnitExecution
	var transitions map[int64]workunit.StateTransition
	var err error
	if tu, ok := b.Storage.(TransitionStorageUpdater); ok {
		res, transitions, err = tu.UpdateWorkUnitExecutionsWithTransitions(taskCtx, xid, updatesToSend)
	} else {
		res, err = b.Storage.UpdateWorkUnitExecutions(taskCtx, xid, updatesToSend)
	}

	duration := time.Since(storageCallStartTime).Seconds()
	updateWorkUnitExecutionsDuration.WithLabelValues(xidStr).Observe(duration)

	if err != nil {
		logging.Error(taskCtx, "Failed to update work unit executions for xid", "xid", xid, "error", err)
		updateWorkUnitExecutionsFailures.WithLabelValues(xidStr, storageFailureReason(err)).Inc()
		return err
	}
	logging.Info(taskCtx, "Updated work unit executions for xid", "xid", xid, "res", res)
	updateWorkUnitExecutionsSuccess.WithLabelValues(xidStr).Inc()

	b.notifyLifecycleEvents(taskCtx, xid, updatesToSend, transitions)
	return nil
}

func (b *UpdateBatcher) notifyLifecycleEvents(ctx context.Context, xid int64, updates []*storagepkg.WorkUnitExecutionUpdate, transitions map[int64]workunit.StateTransition) {
	if b.LifecycleNotifier == nil {
		return
	}
	for _, update := range updates {
		if update == nil {
			continue
		}
		trans, hasTrans := transitions[update.WID]
		shouldNotify := false
		var oldState, newState essmsgpb.WorkUnitStatus_WorkUnitState
		if hasTrans {
			oldState = trans.OldState
			newState = trans.NewState
			if (oldState != newState || update.ForceNotify) && MatchesNotifyWhen(newState, update.NotifyWhen) {
				shouldNotify = true
			}
		} else if update.ForceNotify && update.WorkUnitExecution != nil && update.WorkUnitExecution.Status != nil && MatchesNotifyWhen(update.WorkUnitExecution.Status.State, update.NotifyWhen) {
			shouldNotify = true
			newState = update.WorkUnitExecution.Status.State
		}

		if shouldNotify {
			event := &taskqueuepb.ArtifactLifecycleEvent{
				ExperimentId:        proto.Int64(xid),
				WorkUnitId:          proto.Int64(update.WID),
				WorkUnitExecutionId: proto.Int64(update.EID),
				OldState:            oldState.Enum(),
				NewState:            newState.Enum(),
				Payload: &taskqueuepb.ArtifactLifecycleEvent_JobSetRuntimeContext{
					JobSetRuntimeContext: update.RuntimeContext,
				},
			}
			lifecycleEventsPublishedTotal.WithLabelValues(newState.String()).Inc()
			if notifyErr := b.LifecycleNotifier.NotifyLifecycleEvent(ctx, event); notifyErr != nil {
				logging.Error(ctx, "Failed to publish artifact lifecycle event", "xid", xid, "wid", update.WID, "eid", update.EID, "error", notifyErr)
			} else {
				logging.Info(ctx, "Published artifact lifecycle event to Redis", "xid", xid, "wid", update.WID, "eid", update.EID, "oldState", oldState.String(), "newState", newState.String())
			}
		}
	}
}

// storageFailureReason returns the metric label for a storage error: its gRPC
// code if it has one, "deadline_exceeded" for context deadlines, else "unknown".
func storageFailureReason(err error) string {
	if st, ok := status.FromError(err); ok {
		return st.Code().String()
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "deadline_exceeded"
	}
	return "unknown"
}
