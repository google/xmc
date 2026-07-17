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
	"task/go/status"
	essmsgpb "xmanager_cloud/experiment_state_server/proto/messages_go_proto"
	"xmanager_cloud/internal/logging"
	"xmanager_cloud/internal/validator"
	storagepkg "xmanager_cloud/storage/storage"
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

var (
	updatesAddedTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "batcher_updates_added_total",
		Help: "Total number of updates added to the batcher",
	}, []string{"xid"})

	batchesProcessedTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "batcher_cycles_processed_total",
		Help: "Total number of batch processing cycles completed",
	}, []string{})

	updatesProcessedTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "batcher_updates_processed_total",
		Help: "Total number of work unit updates processed",
	}, []string{"xid"})

	experimentsPerCycle = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "batcher_experiments_per_cycle",
		Help:    "Distribution of the number of experiments processed in each cycle",
		Buckets: prometheus.ExponentialBuckets(1, 2, 10),
	}, []string{})

	workunitsPerExperiment = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "batcher_workunits_per_experiment",
		Help:    "Distribution of the number of work unit updates per experiment in each cycle",
		Buckets: prometheus.ExponentialBuckets(1, 2, 10),
	}, []string{"xid"})

	batchProcessingDuration = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "batcher_cycle_duration_seconds",
		Help:    "Duration of the batch processing cycle",
		Buckets: prometheus.DefBuckets,
	}, []string{})

	updateWorkUnitExecutionsCalls = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "xmc_actuator_batcher_update_calls_total",
			Help: "Total number of calls to UpdateWorkUnitExecutions per experiment.",
		},
		[]string{"xid"},
	)

	updateWorkUnitExecutionsSuccess = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "xmc_actuator_batcher_update_success_total",
			Help: "Total number of successful calls to UpdateWorkUnitExecutions per experiment.",
		},
		[]string{"xid"},
	)

	updateWorkUnitExecutionsFailures = promauto.NewCounterVec(
		prometheus.CounterOpts{
			Name: "xmc_actuator_batcher_update_failures_total",
			Help: "Total number of failed calls to UpdateWorkUnitExecutions per experiment.",
		},
		[]string{"xid", "reason"},
	)

	updateWorkUnitExecutionsDuration = promauto.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:    "xmc_actuator_batcher_update_duration_seconds",
			Help:    "Duration of UpdateWorkUnitExecutions calls.",
			Buckets: prometheus.DefBuckets,
		},
		[]string{"xid"},
	)

	updatesPerBatch = promauto.NewHistogramVec(
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

// BatcherConfig holds configuration for the UpdateBatcher.
type BatcherConfig struct {
	TickDuration           time.Duration
	MaxParallelExperiments int
}

// UpdateBatcher is responsible for batching updates and processing them.
type UpdateBatcher struct {
	Updates                map[int64]map[int64]map[UpdateSource]storagepkg.WorkUnitExecutionUpdate
	UpdatesMu              sync.Mutex
	tickDuration           time.Duration
	maxParallelExperiments int
	Storage                StorageUpdater // Use an interface for testability
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
	b.Updates[xid][update.WID][source] = update
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

// processBatches processes one batch of updates. Returns true if any batches were processed.
func (b *UpdateBatcher) processBatches(ctx context.Context) bool {
	startTime := time.Now()
	defer func() {
		duration := time.Since(startTime)
		batchProcessingDuration.WithLabelValues().Observe(duration.Seconds())
	}()
	b.UpdatesMu.Lock()
	if len(b.Updates) == 0 {
		b.UpdatesMu.Unlock()
		return false
	}
	currentUpdates := b.Updates
	b.Updates = make(map[int64]map[int64]map[UpdateSource]storagepkg.WorkUnitExecutionUpdate)
	b.UpdatesMu.Unlock()

	logging.Info(ctx, "Processing batches", "numExperiments", len(currentUpdates))
	batchesProcessedTotal.WithLabelValues().Inc()
	experimentsPerCycle.WithLabelValues().Observe(float64(len(currentUpdates)))

	limit := b.maxParallelExperiments
	sem := make(chan struct{}, limit)

	var wg sync.WaitGroup
	for xid, widMap := range currentUpdates {
		wg.Add(1)
		sem <- struct{}{} // Acquire semaphore slot

		go func(xid int64, widMap map[int64]map[UpdateSource]storagepkg.WorkUnitExecutionUpdate) {
			defer func() {
				<-sem // Release semaphore slot
				wg.Done()
			}()
			b.processExperimentUpdates(ctx, xid, widMap)
		}(xid, widMap)
	}
	wg.Wait()

	return true
}

func (b *UpdateBatcher) processExperimentUpdates(ctx context.Context, xid int64, widMap map[int64]map[UpdateSource]storagepkg.WorkUnitExecutionUpdate) {
	ctx = logging.WithExperimentName(ctx, validator.ConstructExperimentName(xid))

	var updatesToSend []*storagepkg.WorkUnitExecutionUpdate
	numUpdates := 0
	for _, sourceMap := range widMap {
		if jsUpdate, ok := sourceMap[JobsetSource]; ok {
			u := jsUpdate
			updatesToSend = append(updatesToSend, &u)
			numUpdates++
		}
		if wlUpdate, ok := sourceMap[WorkloadSource]; ok {
			u := wlUpdate
			updatesToSend = append(updatesToSend, &u)
			numUpdates++
		}
		if ksUpdate, ok := sourceMap[KueueSyncSource]; ok {
			u := ksUpdate
			updatesToSend = append(updatesToSend, &u)
			numUpdates++
		}
	}

	if numUpdates > 0 {
		xidStr := fmt.Sprintf("%d", xid)
		updatesProcessedTotal.WithLabelValues(xidStr).Add(float64(numUpdates))
		workunitsPerExperiment.WithLabelValues(xidStr).Observe(float64(len(widMap)))
	}

	if len(updatesToSend) == 0 {
		return
	}

	if b.Storage == nil {
		logging.Error(ctx, "Storage is not initialized in UpdateBatcher")
		updateWorkUnitExecutionsFailures.WithLabelValues(fmt.Sprintf("%d", xid), "storage_nil").Inc()
		return
	}

	xidStr := fmt.Sprintf("%d", xid)
	updateWorkUnitExecutionsCalls.WithLabelValues(xidStr).Inc()
	updatesPerBatch.WithLabelValues(xidStr).Observe(float64(len(updatesToSend)))
	b.callUpdateWorkUnitExecutions(ctx, xid, updatesToSend)
}

func (b *UpdateBatcher) callUpdateWorkUnitExecutions(ctx context.Context, xid int64, updatesToSend []*storagepkg.WorkUnitExecutionUpdate) {
	ctx = logging.WithExperimentName(ctx, validator.ConstructExperimentName(xid))

	xidStr := fmt.Sprintf("%d", xid)
	storageCallStartTime := time.Now()

	taskCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()

	logging.Info(taskCtx, "Update work unit executions for xid", "xid", xid, "numUpdates", len(updatesToSend))
	res, err := b.Storage.UpdateWorkUnitExecutions(taskCtx, xid, updatesToSend)

	duration := time.Since(storageCallStartTime).Seconds()
	updateWorkUnitExecutionsDuration.WithLabelValues(xidStr).Observe(duration)

	if err != nil {
		logging.Error(taskCtx, "Failed to update work unit executions for xid", "xid", xid, "error", err)
		// Attempt to get a status code, default to "unknown"
		reason := "unknown"
		st, ok := status.FromError(err)
		if ok {
			reason = st.Code.String()
		} else if errors.Is(err, context.DeadlineExceeded) {
			reason = "deadline_exceeded"
		}
		updateWorkUnitExecutionsFailures.WithLabelValues(xidStr, reason).Inc()
	} else {
		logging.Info(taskCtx, "Updated work unit executions for xid", "xid", xid, "res", res)
		updateWorkUnitExecutionsSuccess.WithLabelValues(xidStr).Inc()
	}
}
