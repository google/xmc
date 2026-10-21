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

package pgdb

import (
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

func TestFromPool(t *testing.T) {
	pool := &pgxpool.Pool{}
	tests := []struct {
		name string
		pool *pgxpool.Pool
		want DBTX
	}{
		{name: "nil pool returns nil DBTX", pool: nil, want: nil},
		{name: "non-nil pool is returned as DBTX", pool: pool, want: pool},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := FromPool(tt.pool); got != tt.want {
				t.Errorf("FromPool(%p) = %v, want %v", tt.pool, got, tt.want)
			}
		})
	}
}
