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
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"xmanager_cloud/internal/kubernetes/common/constants"
	"xmanager_cloud/internal/logging"
	"xmanager_cloud/internal/redislock"
	"xmanager_cloud/internal/task_queue_redis/redisclient"
	"xmanager_cloud/internal/validator"
	"xmanager_cloud/pkg/study"
)

const (
	defaultLeaseTTL         = 70 * time.Second
	defaultReconcileTimeout = 60 * time.Second
	defaultReleaseTimeout   = 2 * time.Second
	studyLockKeyPrefix      = "xmc:study-reconcile-lock:"
	outcomeReconciled       = "reconciled"
	outcomeContended        = "contended"
	outcomeRetryError       = "retry_error"
	outcomeInvalidPayload   = "invalid_payload"
	outcomeInvalidStudy     = "invalid_study"
)

var reconcileTicksTotal = promauto.NewCounterVec(prometheus.CounterOpts{
	Name: "xmc_study_controller_reconcile_ticks_total",
	Help: "Total number of study reconcile ticks processed by the Study Controller, partitioned by outcome.",
}, []string{"outcome"})

func init() {
	for _, o := range []string{outcomeReconciled, outcomeContended, outcomeRetryError, outcomeInvalidPayload, outcomeInvalidStudy} {
		reconcileTicksTotal.WithLabelValues(o)
	}
}

// Lease represents an acquired per-study reconcile lock. *redislock.Lease satisfies it.
type Lease interface {
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
	Locker                           Locker
	Reconcile                        ReconcileFunc
	ReconcileTimeout, ReleaseTimeout time.Duration
}

// Handler processes study reconcile ticks under a distributed per-study lease lock.
type Handler struct {
	locker                           Locker
	reconcile                        ReconcileFunc
	reconcileTimeout, releaseTimeout time.Duration
}

// NewRedisHandler constructs a Handler sharing consumer's underlying *redis.Client pool.
func NewRedisHandler(consumer *redisclient.ConsumerClient, reconcile ReconcileFunc) (*Handler, error) {
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
	return NewHandler(HandlerParams{Locker: redisLocker{locker: locker}, Reconcile: reconcile})
}

// NewHandler validates p and returns a Handler with defaults applied to zero-valued parameters.
func NewHandler(p HandlerParams) (*Handler, error) {
	if p.Locker == nil {
		return nil, errors.New("study reconcile locker must not be nil")
	}
	if p.Reconcile == nil {
		p.Reconcile = noopReconcile
	}
	if p.ReconcileTimeout == 0 {
		p.ReconcileTimeout = defaultReconcileTimeout
	}
	if p.ReleaseTimeout == 0 {
		p.ReleaseTimeout = defaultReleaseTimeout
	}
	if p.ReconcileTimeout < 0 || p.ReleaseTimeout < 0 {
		return nil, errors.New("study reconcile handler durations must be positive")
	}
	if p.ReconcileTimeout >= constants.RedisProcessTimeout {
		return nil, fmt.Errorf("reconcile timeout (%v) must be less than redis process timeout (%v)", p.ReconcileTimeout, constants.RedisProcessTimeout)
	}
	return &Handler{
		locker:           p.Locker,
		reconcile:        p.Reconcile,
		reconcileTimeout: p.ReconcileTimeout,
		releaseTimeout:   p.ReleaseTimeout,
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
		if errors.Is(err, study.ErrInvalidStudyConfig) && ctx.Err() == nil {
			reconcileTicksTotal.WithLabelValues(outcomeInvalidStudy).Inc()
			logging.Warn(ctx, "Dropping invalid study config tick", "worker_id", workerID)
			return redisclient.TaskResult{WasRunSuccessful: false, ShouldBeRetried: false, Error: err}
		}
		reconcileTicksTotal.WithLabelValues(outcomeRetryError).Inc()
		logging.Warn(ctx, "Failed to process study reconcile tick", "worker_id", workerID, "error", err)
		return redisclient.TaskResult{WasRunSuccessful: false, ShouldBeRetried: true, Error: err}
	}
	reconcileTicksTotal.WithLabelValues(outcomeReconciled).Inc()
	logging.Info(ctx, "Processed study reconcile tick", "worker_id", workerID)
	return redisclient.TaskResult{WasRunSuccessful: true}
}

func (h *Handler) reconcileWithLease(ctx context.Context, workerID string, xid int64, lease Lease) error {
	reconcileCtx, cancel := context.WithTimeout(ctx, h.reconcileTimeout)
	defer h.releaseLease(ctx, workerID, lease)
	defer cancel()
	if err := h.reconcile(reconcileCtx, xid); err != nil {
		return err
	}
	return reconcileCtx.Err()
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
