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
package main

import (
	"testing"
	"time"
)

func TestLoadConfig(t *testing.T) {
	cfg := Load()
	if cfg.MetricsPort != 8082 {
		t.Errorf("Load().MetricsPort = %d, want 8082", cfg.MetricsPort)
	}
	if cfg.ReconciliationTimeout != 2*time.Minute {
		t.Errorf("Load().ReconciliationTimeout = %v, want %v", cfg.ReconciliationTimeout, 2*time.Minute)
	}
}
