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

// Package notifywhen owns the wire format of the xmc.google.com/notify-when
// JobSet annotation. ESS encodes the value and the Reconciler matches against it.
package notifywhen

import (
	"strings"

	essmsgpb "xmanager_cloud/api/protos/experiment"
)

const separator = "."

// canonicalOrder is the deterministic order in which states are encoded.
var canonicalOrder = []essmsgpb.WorkUnitStatus_WorkUnitState{
	essmsgpb.WorkUnitStatus_STARTING,
	essmsgpb.WorkUnitStatus_RUNNING,
	essmsgpb.WorkUnitStatus_COMPLETED,
	essmsgpb.WorkUnitStatus_FAILED,
	essmsgpb.WorkUnitStatus_STOPPED,
}

// Encode returns the dot-separated annotation value (for example
// "STARTING.RUNNING.COMPLETED") for the given states in canonical order.
// States outside the canonical set are ignored. Returns "" for no states.
func Encode(states []essmsgpb.WorkUnitStatus_WorkUnitState) string {
	requested := make(map[essmsgpb.WorkUnitStatus_WorkUnitState]bool, len(states))
	for _, s := range states {
		requested[s] = true
	}
	var tokens []string
	for _, s := range canonicalOrder {
		if requested[s] {
			tokens = append(tokens, s.String())
		}
	}
	return strings.Join(tokens, separator)
}

// Matches reports whether state is listed in the encoded annotation value.
func Matches(state essmsgpb.WorkUnitStatus_WorkUnitState, value string) bool {
	if value == "" {
		return false
	}
	for _, token := range strings.Split(value, separator) {
		if token == state.String() {
			return true
		}
	}
	return false
}
