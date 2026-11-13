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

package redisclient

import (
	"testing"

	"github.com/google/go-cmp/cmp"
)

// redisOptionsSubset is the part of redis.Options that getRedisOptions derives from Config and
// the environment.
type redisOptionsSubset struct {
	Addr                  string
	Password              string
	ContextTimeoutEnabled bool
}

func TestNewClient_Options(t *testing.T) {
	tests := []struct {
		name     string
		password string
		cfg      Config
		want     redisOptionsSubset
	}{
		{
			name: "context timeouts are off by default",
			cfg:  Config{Addr: "redis-service:6379"},
			want: redisOptionsSubset{Addr: "redis-service:6379"},
		},
		{
			name: "context timeouts opt-in",
			cfg:  Config{Addr: "redis-service:6379", ContextTimeoutEnabled: true},
			want: redisOptionsSubset{Addr: "redis-service:6379", ContextTimeoutEnabled: true},
		},
		{
			name:     "password from the environment",
			password: "s3cret",
			cfg:      Config{Addr: "redis-service:6379"},
			want:     redisOptionsSubset{Addr: "redis-service:6379", Password: "s3cret"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("REDIS_PASSWORD", tc.password)
			client := NewClient(tc.cfg)
			defer client.Close()
			opts := client.Options()
			got := redisOptionsSubset{Addr: opts.Addr, Password: opts.Password, ContextTimeoutEnabled: opts.ContextTimeoutEnabled}
			if diff := cmp.Diff(tc.want, got); diff != "" {
				t.Errorf("NewClient(%+v) options mismatch (-want +got):\n%s", tc.cfg, diff)
			}
		})
	}
}
