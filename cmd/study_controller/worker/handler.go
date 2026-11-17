// Copyright 2026 Google LLC
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

// Package worker consumes study reconcile ticks from the xmc-study-reconcile-stream Redis stream.
package worker

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"xmanager_cloud/internal/kubernetes/common/constants"
	"xmanager_cloud/internal/logging"
	"xmanager_cloud/internal/redislock"
	"xmanager_cloud/internal/task_queue_redis/redisclient"
	"xmanager_cloud/internal/validator"
)

const (
	defaultLeaseTTL         = 15 * time.Second
	defaultRenewInterval    = defaultLeaseTTL / 3
	defaultReconcileTimeout = 60 * time.Second
	defaultReleaseTimeout   = 2 * time.Second
	studyLockKeyPrefix      = "xmc:study-reconcile-lock:"
	outcomeReconciled       = "reconciled"
	outcomeContended        = "contended"
	outcomeRetryError       = "retry_error"
	outcomeInvalidPayload   = "invalid_payload"
)

var reconcileTicksTotal = promauto.NewCounterVec(prometheus.CounterOpts{
	Name: "xmc_study_controller_reconcile_ticks_total",
	Help: "Total number of study reconcile ticks processed by the Study Controller, partitioned by outcome.",
}, []string{"outcome"})

func init() {
	for _, o := range []string{outcomeReconciled, outcomeContended, outcomeRetryError, outcomeInvalidPayload} {
		reconcileTicksTotal.WithLabelValues(o)
	}
}

// Lease represents an acquired per-study reconcile lock. *redislock.Lease satisfies it.
type Lease interface {
	Extend(ctx context.Context) error
	Release(ctx context.Context) error
}

var _ Lease = (*redislock.Lease)(nil)

// Locker acquires per-study reconcile leases without blocking.
type Locker interface {
	Acquire(ctx context.Context, key string) (Lease, error)
}

type redisLocker struct{ locker *redislock.Locker }

func (r redisLocker) Acquire(ctx context.Context, key string) (Lease, error) {
	lease, err := r.locker.Acquire(ctx, key)
	if err != nil {
		return nil, err
	}
	return lease, nil
}

// ReconcileFunc reconciles the study identified by xid under a held per-study lease.
type ReconcileFunc func(ctx context.Context, xid int64) error

func noopReconcile(context.Context, int64) error { return nil }
func studyLockKey(xid int64) string              { return fmt.Sprintf("%s%d", studyLockKeyPrefix, xid) }

// HandlerParams configures a study reconcile Handler.
type HandlerParams struct {
	Locker                                          Locker
	Reconcile                                       ReconcileFunc
	RenewInterval, ReconcileTimeout, ReleaseTimeout time.Duration
}

// Handler processes study reconcile ticks under a distributed per-study lease lock.
type Handler struct {
	locker                                          Locker
	reconcile                                       ReconcileFunc
	renewInterval, reconcileTimeout, releaseTimeout time.Duration
}

// NewRedisHandler constructs a Handler sharing consumer's underlying *redis.Client pool.
func NewRedisHandler(consumer *redisclient.ConsumerClient) (*Handler, error) {
	if consumer == nil {
		return nil, errors.New("redis consumer client must not be nil")
	}
	lockClient, ok := consumer.Client.(redislock.Client)
	if !ok {
		return nil, errors.New("redis consumer client does not implement redislock.Client")
	}
	locker, err := redislock.New(lockClient, defaultLeaseTTL)
	if err != nil {
		return nil, err
	}
	return NewHandler(HandlerParams{Locker: redisLocker{locker: locker}})
}

// NewHandler validates p and returns a Handler with defaults applied to zero-valued parameters.
func NewHandler(p HandlerParams) (*Handler, error) {
	if p.Locker == nil {
		return nil, errors.New("study reconcile locker must not be nil")
	}
	if p.Reconcile == nil {
		p.Reconcile = noopReconcile
	}
	if p.RenewInterval == 0 {
		p.RenewInterval = defaultRenewInterval
	}
	if p.ReconcileTimeout == 0 {
		p.ReconcileTimeout = defaultReconcileTimeout
	}
	if p.ReleaseTimeout == 0 {
		p.ReleaseTimeout = defaultReleaseTimeout
	}
	if p.RenewInterval < 0 || p.ReconcileTimeout < 0 || p.ReleaseTimeout < 0 {
		return nil, errors.New("study reconcile handler durations must be positive")
	}
	if p.ReconcileTimeout >= constants.RedisProcessTimeout {
		return nil, fmt.Errorf("reconcile timeout (%v) must be less than redis process timeout (%v)", p.ReconcileTimeout, constants.RedisProcessTimeout)
	}
	return &Handler{
		locker: p.Locker, reconcile: p.Reconcile,
		renewInterval: p.RenewInterval, reconcileTimeout: p.ReconcileTimeout, releaseTimeout: p.ReleaseTimeout,
	}, nil
}

// HandleTask handles one study reconcile tick under a per-study Redis lease.
func (h *Handler) HandleTask(ctx context.Context, workerID, data string) redisclient.TaskResult {
	xid, err := parseReconcilePayload(data)
	if err != nil {
		reconcileTicksTotal.WithLabelValues(outcomeInvalidPayload).Inc()
		logging.Warn(ctx, "Dropping invalid study reconcile tick", "worker_id", workerID, "payload_bytes", len(data), "error", err)
		return redisclient.TaskResult{WasRunSuccessful: false, ShouldBeRetried: false, Error: err}
	}
	ctx = logging.WithExperimentName(ctx, validator.ConstructExperimentName(xid))
	lease, err := h.locker.Acquire(ctx, studyLockKey(xid))
	if errors.Is(err, redislock.ErrNotAcquired) {
		reconcileTicksTotal.WithLabelValues(outcomeContended).Inc()
		logging.Info(ctx, "Skipped contended study reconcile tick", "worker_id", workerID)
		return redisclient.TaskResult{WasRunSuccessful: true}
	}
	if err != nil {
		reconcileTicksTotal.WithLabelValues(outcomeRetryError).Inc()
		logging.Warn(ctx, "Failed to acquire study reconcile lock", "worker_id", workerID, "error", err)
		return redisclient.TaskResult{WasRunSuccessful: false, ShouldBeRetried: true, Error: err}
	}
	if err := h.reconcileWithLease(ctx, workerID, xid, lease); err != nil {
		reconcileTicksTotal.WithLabelValues(outcomeRetryError).Inc()
		logging.Warn(ctx, "Failed to process study reconcile tick", "worker_id", workerID, "error", err)
		return redisclient.TaskResult{WasRunSuccessful: false, ShouldBeRetried: true, Error: err}
	}
	reconcileTicksTotal.WithLabelValues(outcomeReconciled).Inc()
	logging.Info(ctx, "Processed study reconcile tick", "worker_id", workerID)
	return redisclient.TaskResult{WasRunSuccessful: true}
}

func (h *Handler) reconcileWithLease(ctx context.Context, workerID string, xid int64, lease Lease) error {
	reconcileCtx, stopTimeout := context.WithTimeout(ctx, h.reconcileTimeout)
	defer stopTimeout()
	reconcileCtx, cancelReconcile := context.WithCancelCause(reconcileCtx)
	defer cancelReconcile(nil)

	renewCtx, stopRenew := context.WithCancel(reconcileCtx)
	var renewErr error
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		renewErr = h.renewLoop(ctx, renewCtx, workerID, lease, cancelReconcile)
	}()

	reconcileErr := h.reconcile(reconcileCtx, xid)
	stopRenew()
	wg.Wait()
	if !errors.Is(renewErr, redislock.ErrLeaseLost) {
		h.releaseLease(ctx, workerID, lease)
	}
	if renewErr != nil {
		return errors.Join(reconcileErr, renewErr)
	}
	if reconcileErr != nil {
		return reconcileErr
	}
	return reconcileCtx.Err()
}

func (h *Handler) renewLoop(logCtx, renewCtx context.Context, workerID string, lease Lease, cancelReconcile context.CancelCauseFunc) error {
	ticker := time.NewTicker(h.renewInterval)
	defer ticker.Stop()
	for {
		select {
		case <-renewCtx.Done():
			return nil
		case <-ticker.C:
			extendCtx, cancel := context.WithTimeout(renewCtx, h.renewInterval)
			err := lease.Extend(extendCtx)
			cancel()
			if err == nil {
				continue
			}
			if renewCtx.Err() != nil && errors.Is(err, renewCtx.Err()) {
				return nil
			}
			msg := "Failed to extend study reconcile lock lease"
			if errors.Is(err, redislock.ErrLeaseLost) {
				msg = "Lost study reconcile lock lease during renewal"
			}
			logging.Warn(logCtx, msg, "worker_id", workerID, "error", err)
			cancelReconcile(err)
			return err
		}
	}
}

func (h *Handler) releaseLease(ctx context.Context, workerID string, lease Lease) {
	releaseCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), h.releaseTimeout)
	defer cancel()
	err := lease.Release(releaseCtx)
	if err == nil {
		return
	}
	msg := "Failed to release study reconcile lock"
	if errors.Is(err, redislock.ErrLeaseLost) {
		msg = "Study reconcile lock lease was already lost before release"
	}
	logging.Warn(ctx, msg, "worker_id", workerID, "error", err)
}
