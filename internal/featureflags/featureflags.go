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

// Package featureflags provides unified feature flag definitions and environment-variable loading for XMC services.
package featureflags

import (
	"github.com/google/xmc/internal/configutil"
)

const (
	// PauseSchedulingFlagName is the configuration key name for the pause scheduling feature flag.
	PauseSchedulingFlagName = "XMC_PAUSE_SCHEDULING_ENABLED"
)

// Flags represents the global feature flags across XMC services.
type Flags struct {
	// PauseSchedulingEnabled toggles the global scheduling pause feature flag across Actuator, ESS, Health Service, and UI.
	PauseSchedulingEnabled bool
}

// Load initializes and returns a Flags instance populated from environment variables.
func Load() Flags {
	var f Flags

	flags := []struct {
		target *bool
		name   string
		defVal bool
	}{
		{&f.PauseSchedulingEnabled, PauseSchedulingFlagName, false},
	}

	for _, flag := range flags {
		configutil.BoolEnv(flag.target, flag.name, flag.defVal)
	}

	return f
}
