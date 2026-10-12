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

package bridge

import (
	"math/rand/v2"
	"time"

	"k8s.io/client-go/util/workqueue"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"
)

const (
	// componentRetryBaseDelay is the delay before the first retry of a failed Component pass.
	componentRetryBaseDelay = 5 * time.Second
	// componentRetryMaxDelay caps the per-Component retry delay.
	componentRetryMaxDelay = 300 * time.Second
	// componentRetryJitter is the relative spread applied to every retry delay, so Components that
	// failed together (for example during a CMS outage) do not retry in lockstep.
	componentRetryJitter = 0.1
)

// NewComponentRateLimiter returns the per-Component retry policy for failed reconcile passes:
// exponential backoff from 5s, doubling up to 300s, with ±10% jitter. A successful pass resets it.
func NewComponentRateLimiter() workqueue.TypedRateLimiter[reconcile.Request] {
	return &jitteredRateLimiter[reconcile.Request]{
		TypedRateLimiter: workqueue.NewTypedItemExponentialFailureRateLimiter[reconcile.Request](componentRetryBaseDelay, componentRetryMaxDelay),
		maxDelay:         componentRetryMaxDelay,
		jitter:           componentRetryJitter,
		randFloat:        rand.Float64,
	}
}

// jitteredRateLimiter spreads the delays of the wrapped rate limiter by a uniform factor in
// [1-jitter, 1+jitter], never exceeding maxDelay.
type jitteredRateLimiter[T comparable] struct {
	workqueue.TypedRateLimiter[T]
	maxDelay  time.Duration
	jitter    float64
	randFloat func() float64
}

// When returns the jittered delay before item is retried.
func (l *jitteredRateLimiter[T]) When(item T) time.Duration {
	factor := 1 + l.jitter*(2*l.randFloat()-1)
	return min(time.Duration(float64(l.TypedRateLimiter.When(item))*factor), l.maxDelay)
}
