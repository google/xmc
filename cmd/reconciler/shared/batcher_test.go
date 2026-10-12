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
	"sync"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/google/go-cmp/cmp/cmpopts"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	metricspb "github.com/prometheus/client_model/go"
	"google.golang.org/protobuf/testing/protocmp"
	ctrlmetrics "sigs.k8s.io/controller-runtime/pkg/metrics"
	essmsgpb "xmanager_cloud/api/protos/experiment"
	"xmanager_cloud/internal/grpcerr"
	storagepkg "xmanager_cloud/internal/storage"
)

// mockStorageUpdater is a mock implementation of the StorageUpdater interface.
// We use a mock here instead of a fake because the batcher only needs to verify
// that UpdateWorkUnitExecutions is called with the correct arguments. The actual
// logic of updating the storage is tested in the storage layer's tests.
// TODO - (internal issue): Consider using a fake instead of a mock.
type mockStorageUpdater struct {
	calls     map[int64][]*storagepkg.WorkUnitExecutionUpdate
	updateErr error
	updateRes map[int64]*essmsgpb.WorkUnitExecution
}

func (m *mockStorageUpdater) UpdateWorkUnitExecutions(ctx context.Context, xid int64, updates []*storagepkg.WorkUnitExecutionUpdate) (map[int64]*essmsgpb.WorkUnitExecution, error) {
	if m.calls == nil {
		m.calls = make(map[int64][]*storagepkg.WorkUnitExecutionUpdate)
	}
	m.calls[xid] = append(m.calls[xid], updates...)
	return m.updateRes, m.updateErr
}

func (m *mockStorageUpdater) clearCalls() {
	m.calls = make(map[int64][]*storagepkg.WorkUnitExecutionUpdate)
}

func TestNewUpdateBatcher(t *testing.T) {
	tests := []struct {
		name                 string
		config               *BatcherConfig
		wantParallelizalimit int
	}{
		{
			name: "positive MaxParallelExperiments",
			config: &BatcherConfig{
				TickDuration:           250 * time.Millisecond,
				MaxParallelExperiments: 5,
			},
			wantParallelizalimit: 5,
		},
		{
			name: "zero MaxParallelExperiments",
			config: &BatcherConfig{
				TickDuration:           250 * time.Millisecond,
				MaxParallelExperiments: 0,
			},
			wantParallelizalimit: 1,
		},
		{
			name: "negative MaxParallelExperiments",
			config: &BatcherConfig{
				TickDuration:           250 * time.Millisecond,
				MaxParallelExperiments: -2,
			},
			wantParallelizalimit: 1,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			b := NewUpdateBatcher(t.Context(), tc.config, &mockStorageUpdater{})
			if b.maxParallelExperiments != tc.wantParallelizalimit {
				t.Errorf("NewUpdateBatcher() maxParallelExperiments = %d, want %d", b.maxParallelExperiments, tc.wantParallelizalimit)
			}
		})
	}
}

func TestUpdateBatcherAdd(t *testing.T) {
	updatesAddedTotal.Reset()
	b := NewUpdateBatcher(t.Context(), &BatcherConfig{TickDuration: 250 * time.Millisecond}, &mockStorageUpdater{})
	ctx := context.Background()

	u1 := storagepkg.WorkUnitExecutionUpdate{WID: 1}
	u2 := storagepkg.WorkUnitExecutionUpdate{WID: 2}

	b.Add(ctx, 123, u1, JobsetSource)
	b.Add(ctx, 123, u2, JobsetSource)
	b.Add(ctx, 456, u1, WorkloadSource)

	b.UpdatesMu.Lock()
	defer b.UpdatesMu.Unlock()

	want := map[int64]map[int64]map[UpdateSource]storagepkg.WorkUnitExecutionUpdate{
		123: {
			1: {JobsetSource: u1},
			2: {JobsetSource: u2},
		},
		456: {
			1: {WorkloadSource: u1},
		},
	}

	if diff := cmp.Diff(want, b.Updates, protocmp.Transform()); diff != "" {
		t.Errorf("Unexpected updates map (-want +got):\n%s", diff)
	}

	if val := testutil.ToFloat64(updatesAddedTotal.WithLabelValues("123")); val != 2 {
		t.Errorf("updatesAddedTotal counter mismatch for xid 123: got %v, want 2", val)
	}
	if val := testutil.ToFloat64(updatesAddedTotal.WithLabelValues("456")); val != 1 {
		t.Errorf("updatesAddedTotal counter mismatch for xid 456: got %v, want 1", val)
	}
}

func TestProcessBatches_Storage(t *testing.T) {
	ctx := context.Background()
	mockStorage := &mockStorageUpdater{}
	updateWorkUnitExecutionsCalls.Reset()
	updateWorkUnitExecutionsSuccess.Reset()
	updateWorkUnitExecutionsFailures.Reset()
	updateWorkUnitExecutionsDuration.Reset()
	updatesPerBatch.Reset()
	batcher := NewUpdateBatcher(t.Context(), &BatcherConfig{TickDuration: 1 * time.Millisecond}, mockStorage)

	// Add some updates
	update1 := storagepkg.WorkUnitExecutionUpdate{WID: 1, WorkUnitExecution: &essmsgpb.WorkUnitExecution{Name: "test1"}}
	update2 := storagepkg.WorkUnitExecutionUpdate{WID: 2, WorkUnitExecution: &essmsgpb.WorkUnitExecution{Name: "test2"}}
	batcher.Add(ctx, 123, update1, JobsetSource)
	batcher.Add(ctx, 123, update2, JobsetSource)
	batcher.Add(ctx, 456, update1, WorkloadSource)

	// Set mock expectations
	mockStorage.updateRes = map[int64]*essmsgpb.WorkUnitExecution{
		1: {Name: "updated1"},
		2: {Name: "updated2"},
	}

	// Process batches
	batcher.processBatches(ctx)

	// Assert calls
	if len(mockStorage.calls) != 2 {
		t.Errorf("Expected 2 calls to UpdateWorkUnitExecutions, got %d", len(mockStorage.calls))
	}

	sortWorkUnitExecutionUpdates := cmpopts.SortSlices(func(a, b *storagepkg.WorkUnitExecutionUpdate) bool {
		return a.WID < b.WID
	})

	expectedUpdates123 := []*storagepkg.WorkUnitExecutionUpdate{&update1, &update2}
	if diff := cmp.Diff(expectedUpdates123, mockStorage.calls[123], protocmp.Transform(), cmp.AllowUnexported(storagepkg.WorkUnitExecutionUpdate{}), sortWorkUnitExecutionUpdates); diff != "" {
		t.Errorf("Unexpected updates for xid 123 diff (-want +got):\n%s", diff)
	}

	expectedUpdates456 := []*storagepkg.WorkUnitExecutionUpdate{&update1}
	if diff := cmp.Diff(expectedUpdates456, mockStorage.calls[456], protocmp.Transform(), cmp.AllowUnexported(storagepkg.WorkUnitExecutionUpdate{})); diff != "" {
		t.Errorf("Unexpected updates for xid 456 diff (-want +got):\n%s", diff)
	}

	// Test error case
	mockStorage.clearCalls()
	mockStorage.updateErr = fmt.Errorf("simulated error")
	batcher.Add(ctx, 789, update1, JobsetSource)
	batcher.processBatches(ctx)
	if len(mockStorage.calls[789]) != 1 {
		t.Errorf("Expected UpdateWorkUnitExecutions to be called even if it returns an error")
	}
}

func TestProcessBatches_Metrics(t *testing.T) {
	ctx := context.Background()

	update1 := storagepkg.WorkUnitExecutionUpdate{WID: 1, WorkUnitExecution: &essmsgpb.WorkUnitExecution{Name: "test1"}}
	update2 := storagepkg.WorkUnitExecutionUpdate{WID: 2, WorkUnitExecution: &essmsgpb.WorkUnitExecution{Name: "test2"}}

	t.Run("Success cases", func(t *testing.T) {
		updateWorkUnitExecutionsCalls.Reset()
		updateWorkUnitExecutionsSuccess.Reset()
		updateWorkUnitExecutionsFailures.Reset()
		updateWorkUnitExecutionsDuration.Reset()
		updatesPerBatch.Reset()

		mockStorage := &mockStorageUpdater{}
		batcher := NewUpdateBatcher(t.Context(), &BatcherConfig{TickDuration: 1 * time.Millisecond}, mockStorage)

		batcher.Add(ctx, 123, update1, JobsetSource)
		batcher.Add(ctx, 123, update2, JobsetSource)
		batcher.Add(ctx, 456, update1, WorkloadSource)

		mockStorage.updateRes = map[int64]*essmsgpb.WorkUnitExecution{
			1: {Name: "updated1"},
			2: {Name: "updated2"},
		}

		batcher.processBatches(ctx)

		if got := testutil.ToFloat64(updateWorkUnitExecutionsCalls.WithLabelValues("123")); got != 1 {
			t.Errorf("updateWorkUnitExecutionsCalls counter for xid 123: got %v, want 1", got)
		}
		if got := testutil.ToFloat64(updateWorkUnitExecutionsCalls.WithLabelValues("456")); got != 1 {
			t.Errorf("updateWorkUnitExecutionsCalls counter for xid 456: got %v, want 1", got)
		}
		if got := testutil.ToFloat64(updateWorkUnitExecutionsSuccess.WithLabelValues("123")); got != 1 {
			t.Errorf("updateWorkUnitExecutionsSuccess counter for xid 123: got %v, want 1", got)
		}
		if got := testutil.ToFloat64(updateWorkUnitExecutionsSuccess.WithLabelValues("456")); got != 1 {
			t.Errorf("updateWorkUnitExecutionsSuccess counter for xid 456: got %v, want 1", got)
		}
		// Check that observations were recorded in the updateWorkUnitExecutionsDuration histogram.
		if _, ok := updateWorkUnitExecutionsDuration.WithLabelValues("123").(prometheus.Observer); !ok {
			t.Errorf("updateWorkUnitExecutionsDuration for xid 123 is not a prometheus.Observer")
		}
		if _, ok := updateWorkUnitExecutionsDuration.WithLabelValues("456").(prometheus.Observer); !ok {
			t.Errorf("updateWorkUnitExecutionsDuration for xid 456 is not a prometheus.Observer")
		}
		// Check that the updateWorkUnitExecutionsDuration histogram has observations.
		mfs, err := ctrlmetrics.Registry.Gather()
		if err != nil {
			t.Fatalf("Failed to gather metrics: %v", err)
		}

		checkHistogramHasObservations(t, mfs, "xmc_actuator_batcher_update_duration_seconds", "123")
		checkHistogramHasObservations(t, mfs, "xmc_actuator_batcher_update_duration_seconds", "456")

		// Check that observations were recorded in the updatesPerBatch histogram.
		if _, ok := updatesPerBatch.WithLabelValues("123").(prometheus.Observer); !ok {
			t.Errorf("updatesPerBatch for xid 123 is not a prometheus.Observer")
		}
		if _, ok := updatesPerBatch.WithLabelValues("456").(prometheus.Observer); !ok {
			t.Errorf("updatesPerBatch for xid 456 is not a prometheus.Observer")
		}
	})

	t.Run("No updates", func(t *testing.T) {
		updatesProcessedTotal.Reset()
		workunitsPerExperiment.Reset()

		mockStorage := &mockStorageUpdater{}
		batcher := NewUpdateBatcher(t.Context(), &BatcherConfig{TickDuration: 1 * time.Millisecond}, mockStorage)

		// Call processExperimentUpdates with an empty widMap
		batcher.processExperimentUpdates(ctx, 999, make(map[int64]map[UpdateSource]storagepkg.WorkUnitExecutionUpdate))

		xidStr := "999"
		if got := testutil.ToFloat64(updatesProcessedTotal.WithLabelValues(xidStr)); got != 0 {
			t.Errorf("updatesProcessedTotal counter for xid %s: got %v, want 0", xidStr, got)
		}

		// Gather metrics
		mfs, err := ctrlmetrics.Registry.Gather()
		if err != nil {
			t.Fatalf("Failed to gather metrics: %v", err)
		}

		// Verify that the workunitsPerExperiment metric for xid=999 is NOT found.
		if metricFound(mfs, "batcher_workunits_per_experiment", xidStr) {
			t.Errorf("workunitsPerExperiment metric for xid %s was found, but should not have been observed", xidStr)
		}
	})

	tests := []struct {
		name       string
		err        error
		wantReason string
	}{
		{
			name:       "unknown error",
			err:        fmt.Errorf("simulated error"),
			wantReason: "unknown",
		},
		{
			name:       "status error",
			err:        fmt.Errorf("invalid arg: %w", grpcerr.ErrInvalidArgument),
			wantReason: "InvalidArgument",
		},
		{
			name:       "deadline exceeded",
			err:        errors.New("context deadline exceeded"),
			wantReason: "unknown",
		},
		{
			name:       "deadline exceeded from context",
			err:        context.DeadlineExceeded,
			wantReason: "deadline_exceeded",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			updateWorkUnitExecutionsCalls.Reset()
			updateWorkUnitExecutionsSuccess.Reset()
			updateWorkUnitExecutionsFailures.Reset()
			updateWorkUnitExecutionsDuration.Reset()
			updatesPerBatch.Reset()

			mockStorage := &mockStorageUpdater{updateErr: test.err}
			batcher := NewUpdateBatcher(t.Context(), &BatcherConfig{TickDuration: 1 * time.Millisecond}, mockStorage)
			batcher.Add(ctx, 789, update1, JobsetSource)
			batcher.processBatches(ctx)

			if got := testutil.ToFloat64(updateWorkUnitExecutionsCalls.WithLabelValues("789")); got != 1 {
				t.Errorf("updateWorkUnitExecutionsCalls counter for xid 789: got %v, want 1", got)
			}
			if got := testutil.ToFloat64(updateWorkUnitExecutionsFailures.WithLabelValues("789", test.wantReason)); got != 1 {
				t.Errorf("updateWorkUnitExecutionsFailures counter for xid 789 with reason %q: got %v, want 1", test.wantReason, got)
			}
		})
	}
}

type mockBlockingStorageUpdater struct {
	startSignal chan struct{}
	active      int
	maxActive   int
	mu          sync.Mutex
}

func (m *mockBlockingStorageUpdater) UpdateWorkUnitExecutions(ctx context.Context, xid int64, updates []*storagepkg.WorkUnitExecutionUpdate) (map[int64]*essmsgpb.WorkUnitExecution, error) {
	m.mu.Lock()
	m.active++
	if m.active > m.maxActive {
		m.maxActive = m.active
	}
	m.mu.Unlock()

	// Wait for the signal to start processing.
	<-m.startSignal

	m.mu.Lock()
	m.active--
	m.mu.Unlock()
	return nil, nil
}

func TestProcessBatches_Parallelization(t *testing.T) {
	ctx := context.Background()
	maxParallel := 3
	blockingStorage := &mockBlockingStorageUpdater{
		startSignal: make(chan struct{}),
	}

	batcher := NewUpdateBatcher(t.Context(), &BatcherConfig{TickDuration: 1 * time.Millisecond, MaxParallelExperiments: maxParallel}, blockingStorage)

	// Add more updates than the parallel limit.
	for i := 0; i < maxParallel*2; i++ {
		batcher.Add(ctx, int64(i), storagepkg.WorkUnitExecutionUpdate{WID: 1}, JobsetSource)
	}

	// Trigger batch processing.
	go batcher.processBatches(ctx)

	// Allow some time for goroutines to start and block on the semaphore.
	time.Sleep(100 * time.Millisecond)

	// Check that at most `maxParallel` goroutines are active.
	blockingStorage.mu.Lock()
	currentActive := blockingStorage.active
	blockingStorage.mu.Unlock()
	if currentActive > maxParallel {
		t.Errorf("Expected at most %d active processExperimentUpdates, got %d", maxParallel, currentActive)
	}

	// Release the blocking signal.
	close(blockingStorage.startSignal)
}

// TestUpdateBatcherConcurrency stress tests the batcher by calling Add and processBatches
// from multiple goroutines simultaneously. This test does not perform value assertions;
// its primary purpose is to detect data races when run with the Go race detector,
// e.g., using `blaze test --config=gotsan`.
func TestUpdateBatcherConcurrency(t *testing.T) {
	b := NewUpdateBatcher(t.Context(), &BatcherConfig{TickDuration: 10 * time.Millisecond}, &mockStorageUpdater{})
	ctx := context.Background()
	wg := sync.WaitGroup{}

	// Simulate concurrent additions
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				b.Add(ctx, int64(j%10), storagepkg.WorkUnitExecutionUpdate{WID: int64(j)}, JobsetSource)
			}
		}()
	}

	// Simulate concurrent processing
	wg.Add(1)
	go func() {
		defer wg.Done()
		for j := 0; j < 100; j++ {
			b.processBatches(ctx)
			time.Sleep(5 * time.Millisecond) // Give Add a chance to run
		}
	}()
	wg.Wait()
}

func TestBatchProcessorShutdown(t *testing.T) {
	b := NewUpdateBatcher(t.Context(), &BatcherConfig{TickDuration: 10 * time.Millisecond}, &mockStorageUpdater{})
	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan struct{})
	go func() {
		b.BatchProcessor(ctx)
		close(done)
	}()

	// Cancel the context to signal shutdown
	cancel()

	// Wait for batchProcessor to exit
	select {
	case <-done:
	// batchProcessor exited cleanly
	case <-time.After(5 * time.Second):
		t.Errorf("batchProcessor did not shut down within timeout")
	}
}

func getHistogramSampleCount(mfs []*metricspb.MetricFamily, name, xid string) uint64 {
	for _, mf := range mfs {
		if mf.GetName() == name {
			for _, metric := range mf.GetMetric() {
				for _, label := range metric.GetLabel() {
					if label.GetName() == "xid" && label.GetValue() == xid {
						if metric.GetHistogram() != nil {
							return metric.GetHistogram().GetSampleCount()
						}
					}
				}
			}
		}
	}
	return 0
}

func checkHistogramHasObservations(t *testing.T, mfs []*metricspb.MetricFamily, name, xid string) {
	t.Helper()
	if count := getHistogramSampleCount(mfs, name, xid); count == 0 {
		t.Errorf("%s for xid %s has no observations", name, xid)
	}
}

func metricFound(mfs []*metricspb.MetricFamily, name, xid string) bool {
	for _, mf := range mfs {
		if mf.GetName() == name {
			for _, metric := range mf.GetMetric() {
				for _, label := range metric.GetLabel() {
					if label.GetName() == "xid" && label.GetValue() == xid {
						return true
					}
				}
			}
		}
	}
	return false
}
