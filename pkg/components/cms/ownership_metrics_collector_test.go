// Copyright 2026 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package cms

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/prometheus/client_golang/prometheus/testutil"

	"xmanager_cloud/pkg/components/store"
)

// fakeGaugeReader returns gauges or err and counts its reads.
type fakeGaugeReader struct {
	gauges store.OwnershipGauges
	err    error
	reads  chan time.Time
}

func (f *fakeGaugeReader) ReadOwnershipGauges(_ context.Context, now time.Time) (store.OwnershipGauges, error) {
	if f.reads != nil {
		f.reads <- now
	}
	return f.gauges, f.err
}

// blockingGaugeReader blocks each read until its ctx is done and reports the ctx error on errs.
type blockingGaugeReader struct {
	errs chan error
}

func (b *blockingGaugeReader) ReadOwnershipGauges(ctx context.Context, _ time.Time) (store.OwnershipGauges, error) {
	<-ctx.Done()
	select {
	case b.errs <- ctx.Err():
	default:
	}
	return store.OwnershipGauges{}, ctx.Err()
}

// ownershipGaugeValues returns the epoch, fence last-seen timestamp and stale owner gauge values.
func ownershipGaugeValues() []float64 {
	return []float64{
		testutil.ToFloat64(store.ClusterFenceEpochGauge()),
		testutil.ToFloat64(store.ClusterFenceLastSeenGauge()),
		testutil.ToFloat64(store.ComponentOwnerStaleGauge()),
	}
}

func TestCollectOwnershipMetrics(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 10, 14, 12, 0, 0, 0, time.UTC)
	lastSeen := now.Add(-90 * time.Second)
	declared := store.OwnershipGauges{FenceDeclared: true, FenceEpoch: 7, FenceLastSeenTime: lastSeen, StaleOwners: 2}
	tests := []struct {
		name    string
		reader  *fakeGaugeReader
		want    []float64 // Epoch, last-seen Unix seconds, stale owners.
		wantErr bool
	}{
		{name: "declared fence", reader: &fakeGaugeReader{gauges: declared}, want: []float64{7, float64(lastSeen.Unix()), 2}},
		{name: "undeclared fence reports zero", reader: &fakeGaugeReader{gauges: store.OwnershipGauges{StaleOwners: 1}}, want: []float64{0, 0, 1}},
		{name: "read error keeps previous values", reader: &fakeGaugeReader{err: errors.New("spanner unavailable")}, want: []float64{7, float64(lastSeen.Unix()), 2}, wantErr: true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// Start from known values so that the error case can assert they are kept.
			store.RecordOwnershipGauges(declared)
			err := CollectOwnershipMetrics(ctx, tc.reader, now)
			if gotErr := err != nil; gotErr != tc.wantErr {
				t.Fatalf("CollectOwnershipMetrics() error = %v, wantErr %v", err, tc.wantErr)
			}
			if diff := cmp.Diff(tc.want, ownershipGaugeValues()); diff != "" {
				t.Errorf("ownership gauges mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestRunOwnershipMetricsCollector_CollectsImmediatelyAndStopsOnCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	reader := &fakeGaugeReader{gauges: store.OwnershipGauges{FenceDeclared: true, FenceEpoch: 9}, reads: make(chan time.Time, 1)}
	done := make(chan struct{})
	go func() {
		defer close(done)
		RunOwnershipMetricsCollector(ctx, reader, time.Hour)
	}()

	select {
	case <-reader.reads:
	case <-time.After(10 * time.Second):
		t.Fatal("RunOwnershipMetricsCollector() did not collect before the first tick")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("RunOwnershipMetricsCollector() did not return after ctx was canceled")
	}
	if got := testutil.ToFloat64(store.ClusterFenceEpochGauge()); got != 9 {
		t.Errorf("xmc_cms_cluster_fence_epoch = %v, want 9", got)
	}
}

func TestRunOwnershipMetricsCollector_BoundsEachRead(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	reader := &blockingGaugeReader{errs: make(chan error, 1)}
	done := make(chan struct{})
	go func() {
		defer close(done)
		RunOwnershipMetricsCollector(ctx, reader, 10*time.Millisecond)
	}()
	defer func() {
		cancel()
		<-done
	}()

	// Two timed-out reads in a row show that each read has a deadline and that the loop moves on.
	for i := 1; i <= 2; i++ {
		select {
		case err := <-reader.errs:
			if !errors.Is(err, context.DeadlineExceeded) {
				t.Fatalf("read %d ctx error = %v, want %v", i, err, context.DeadlineExceeded)
			}
		case <-time.After(10 * time.Second):
			t.Fatalf("RunOwnershipMetricsCollector() read %d did not time out", i)
		}
	}
}
