// Copyright 2026 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//      http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package lease

import (
	"context"
	"os"
	"time"

	"github.com/redis/go-redis/v9"
	"go/log"

)

// Coordinator manages leader election using Redis SET NX.
type Coordinator struct {
	client   redis.Cmdable
	lockKey  string
	identity string
	ttl      time.Duration
	interval time.Duration
}

// NewCoordinator creates a new Redis leader coordinator.
func NewCoordinator(client redis.Cmdable, lockKey string, identity string, ttl time.Duration, interval time.Duration) *Coordinator {
	if identity == "" {
		identity, _ = os.Hostname()
		if identity == "" {
			identity = "unknown-host"
		}
	}
	return &Coordinator{
		client:   client,
		lockKey:  lockKey,
		identity: identity,
		ttl:      ttl,
		interval: interval,
	}
}

// Run executes the leader election loop.
func (c *Coordinator) Run(ctx context.Context, startWorker func(ctx context.Context), stopWorker func()) {
	ticker := time.NewTicker(c.interval)
	defer ticker.Stop()

	var workerCtx context.Context
	var cancelWorker context.CancelFunc
	isLeader := false

	defer func() {
		if cancelWorker != nil {
			cancelWorker()
		}
		stopWorker()
		if isLeader {
			// Best effort release lock
			releaseCtx, releaseCancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer releaseCancel()
			c.release(releaseCtx)
		}
	}()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			won, err := c.tryAcquire(ctx)
			if err != nil {
				log.Errorf("Leader election: failed to acquire/renew lock: %v", err)
				if isLeader {
					log.Warningf("Leader election: stepping down due to redis error: %v", err)
					if cancelWorker != nil {
						cancelWorker()
						cancelWorker = nil
					}
					stopWorker()
					isLeader = false
				}
				continue
			}

			if won {
				if !isLeader {
					log.Infof("Leader election: won leadership as %s", c.identity)
					isLeader = true
					workerCtx, cancelWorker = context.WithCancel(ctx)
					go startWorker(workerCtx)
				}
			} else {
				if isLeader {
					log.Warningf("Leader election: lost leadership")
					if cancelWorker != nil {
						cancelWorker()
						cancelWorker = nil
					}
					stopWorker()
					isLeader = false
				}
			}
		}
	}
}

func (c *Coordinator) tryAcquire(ctx context.Context) (bool, error) {
	ok, err := c.client.SetNX(ctx, c.lockKey, c.identity, c.ttl).Result()
	if err != nil {
		return false, err
	}
	if ok {
		return true, nil
	}

	script := `
if redis.call("get", KEYS[1]) == ARGV[1] then
    return redis.call("pexpire", KEYS[1], ARGV[2])
else
    return 0
end`
	res, err := c.client.Eval(ctx, script, []string{c.lockKey}, c.identity, int64(c.ttl/time.Millisecond)).Int64()
	if err != nil {
		return false, err
	}
	return res == 1, nil
}

func (c *Coordinator) release(ctx context.Context) {
	script := `
if redis.call("get", KEYS[1]) == ARGV[1] then
    return redis.call("del", KEYS[1])
else
    return 0
end`
	_ = c.client.Eval(ctx, script, []string{c.lockKey}, c.identity).Err()
}
