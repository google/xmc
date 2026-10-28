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
	ctrlmetrics "sigs.k8s.io/controller-runtime/pkg/metrics"
	essmsgpb "xmanager_cloud/api/protos/experiment"
	"xmanager_cloud/internal/kubernetes/common/notifywhen"
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
)

// StorageUpdater defines the interface for updating work unit executions.
type StorageUpdater interface {
	UpdateWorkUnitExecutions(ctx context.Context, xid int64, updates []*storagepkg.WorkUnitExecutionUpdate) (map[int64]*essmsgpb.WorkUnitExecution, error)
}

// TransitionStorageUpdater is a StorageUpdater that also reports the committed
// state transitions, which the batcher needs to publish lifecycle events.
type TransitionStorageUpdater interface {
	StorageUpdater
	UpdateWorkUnitExecutionsWithTransitions(ctx context.Context, xid int64, updates []*storagepkg.WorkUnitExecutionUpdate) (map[int64]*essmsgpb.WorkUnitExecution, map[int64]workunit.StateTransition, error)
}

// PendingUpdate is a storage update waiting to be flushed, together with the
// lifecycle hints used to publish an event once it is committed.
type PendingUpdate struct {
	storagepkg.WorkUnitExecutionUpdate
	Hints LifecycleHints
}

// BatcherConfig holds configuration for the UpdateBatcher.
type BatcherConfig struct {
	TickDuration           time.Duration
	MaxParallelExperiments int
}

// UpdateBatcher is responsible for batching updates and processing them.
type UpdateBatcher struct {
	Updates map[int64]map[int64]map[UpdateSource]PendingUpdate
	// UpdatesMu guards Updates and retries. Sharing one lock lets each tick
	// snapshot pending updates and merge due retries atomically.
	UpdatesMu              sync.Mutex
	tickDuration           time.Duration
	maxParallelExperiments int
	Storage                TransitionStorageUpdater // Use an interface for testability
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
func NewUpdateBatcher(ctx context.Context, cfg *BatcherConfig, storage TransitionStorageUpdater) *UpdateBatcher {
	maxParallelExperiments := cfg.MaxParallelExperiments
	if maxParallelExperiments <= 0 {
		logging.Warn(ctx, "MaxParallelExperiments was <= 0, defaulting to 1", "maxParallelExperiments", maxParallelExperiments)
		maxParallelExperiments = 1
	}
	return &UpdateBatcher{
		Updates:                make(map[int64]map[int64]map[UpdateSource]PendingUpdate),
		tickDuration:           cfg.TickDuration,
		maxParallelExperiments: maxParallelExperiments,
		Storage:                storage,
	}
}

// Add adds an update without lifecycle hints to the batch.
func (b *UpdateBatcher) Add(ctx context.Context, xid int64, update storagepkg.WorkUnitExecutionUpdate, source UpdateSource) {
	b.AddWithLifecycleHints(ctx, xid, update, LifecycleHints{}, source)
}

// AddWithLifecycleHints adds an update to the batch together with the hints
// used to publish a lifecycle event once the update is committed.
func (b *UpdateBatcher) AddWithLifecycleHints(ctx context.Context, xid int64, update storagepkg.WorkUnitExecutionUpdate, hints LifecycleHints, source UpdateSource) {
	updatesAddedTotal.WithLabelValues(fmt.Sprintf("%d", xid)).Inc()
	b.UpdatesMu.Lock()
	defer b.UpdatesMu.Unlock()
	if _, exists := b.Updates[xid]; !exists {
		b.Updates[xid] = make(map[int64]map[UpdateSource]PendingUpdate)
	}
	if _, exists := b.Updates[xid][update.WID]; !exists {
		b.Updates[xid][update.WID] = make(map[UpdateSource]PendingUpdate)
	}
	// Storage skips updates whose EID is not the current execution, so letting a
	// stale (lower-EID) update replace a pending newer one would silently drop the
	// newer execution's update.
	if pending, exists := b.Updates[xid][update.WID][source]; exists {
		if update.EID < pending.EID {
			return
		}
		if update.EID == pending.EID && pending.Hints.ForceNotify {
			hints.ForceNotify = true
		}
	}
	b.Updates[xid][update.WID][source] = PendingUpdate{WorkUnitExecutionUpdate: update, Hints: hints}
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

func (b *UpdateBatcher) processExperimentUpdates(ctx context.Context, xid int64, widMap map[int64]map[UpdateSource]PendingUpdate, retries []batchEntry) {
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
	if err := b.callUpdateWorkUnitExecutions(ctx, xid, batch); err != nil {
		b.handleFlushFailure(ctx, xid, batch, err)
	}
}

// callUpdateWorkUnitExecutions sends the batch's updates to storage, records
// call metrics, publishes lifecycle events for committed updates, and returns
// the storage error, if any.
func (b *UpdateBatcher) callUpdateWorkUnitExecutions(ctx context.Context, xid int64, batch []batchEntry) error {
	ctx = logging.WithExperimentName(ctx, validator.ConstructExperimentName(xid))

	xidStr := fmt.Sprintf("%d", xid)
	updateWorkUnitExecutionsCalls.WithLabelValues(xidStr).Inc()
	storageCallStartTime := time.Now()

	taskCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	logging.Info(taskCtx, "Update work unit executions for xid", "xid", xid, "numUpdates", len(batch))
	res, transitions, err := b.Storage.UpdateWorkUnitExecutionsWithTransitions(taskCtx, xid, updatePointers(batch))

	duration := time.Since(storageCallStartTime).Seconds()
	updateWorkUnitExecutionsDuration.WithLabelValues(xidStr).Observe(duration)

	if err != nil {
		logging.Error(taskCtx, "Failed to update work unit executions for xid", "xid", xid, "error", err)
		updateWorkUnitExecutionsFailures.WithLabelValues(xidStr, storageFailureReason(err)).Inc()
		return err
	}
	logging.Info(taskCtx, "Updated work unit executions for xid", "xid", xid, "res", res)
	updateWorkUnitExecutionsSuccess.WithLabelValues(xidStr).Inc()

	b.notifyLifecycleEvents(taskCtx, xid, batch, transitions)
	return nil
}

func (b *UpdateBatcher) notifyLifecycleEvents(ctx context.Context, xid int64, batch []batchEntry, transitions map[int64]workunit.StateTransition) {
	if b.LifecycleNotifier == nil {
		return
	}
	for _, entry := range batch {
		oldState, newState, ok := lifecycleTransitionToPublish(entry, transitions)
		if !ok {
			continue
		}
		PublishLifecycleEvent(ctx, b.LifecycleNotifier, LifecycleEvent{
			XID:            xid,
			WID:            entry.update.WID,
			EID:            entry.update.EID,
			OldState:       oldState,
			NewState:       newState,
			RuntimeContext: entry.hints.RuntimeContext,
		})
	}
}

// lifecycleTransitionToPublish returns the (old, new) states to publish for a
// committed entry, and whether an event should be published at all. A committed
// transition is published when the state changed (or ForceNotify is set) and
// the new state matches notify-when. Without a committed transition, only a
// ForceNotify entry whose own state matches notify-when is published.
func lifecycleTransitionToPublish(entry batchEntry, transitions map[int64]workunit.StateTransition) (oldState, newState essmsgpb.WorkUnitStatus_WorkUnitState, ok bool) {
	hints := entry.hints
	if trans, hasTrans := transitions[entry.update.WID]; hasTrans {
		changed := trans.OldState != trans.NewState
		return trans.OldState, trans.NewState, (changed || hints.ForceNotify) && notifywhen.Matches(trans.NewState, hints.NotifyWhen)
	}
	status := entry.update.WorkUnitExecution.GetStatus()
	if !hints.ForceNotify || status == nil {
		return oldState, newState, false
	}
	return essmsgpb.WorkUnitStatus_WORK_UNIT_STATE_UNSPECIFIED, status.GetState(), notifywhen.Matches(status.GetState(), hints.NotifyWhen)
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
