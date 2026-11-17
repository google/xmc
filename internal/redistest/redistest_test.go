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

package redistest

import (
	"context"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/testcontainers/testcontainers-go"
)

func TestRunSuite_LifecycleAndStartFailure(t *testing.T) {
	var addr string
	var pingErr error
	code := runSuite(func() int {
		client := redis.NewClient(&redis.Options{Addr: addr})
		defer client.Close()
		pingErr = client.Ping(context.Background()).Err()
		return 0
	}, &addr, startRedisContainer)
	if code != 0 || addr == "" || pingErr != nil {
		t.Fatalf("runSuite() code=%d addr=%q pingErr=%v, want 0, non-empty, nil", code, addr, pingErr)
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	client := redis.NewClient(&redis.Options{Addr: addr, MaxRetries: 0})
	defer client.Close()
	if err := client.Ping(ctx).Err(); err == nil {
		t.Errorf("Ping(%q) after container termination succeeded, want error", addr)
	}

	cancelledCtx, cancelStart := context.WithCancel(context.Background())
	cancelStart()
	if code := runSuite(func() int { return 0 }, &addr, func(context.Context) (c testcontainers.Container, s string, err error) {
		return startRedisContainer(cancelledCtx)
	}); code != 1 {
		t.Errorf("runSuite(cancelled) code = %d, want 1", code)
	}
}
