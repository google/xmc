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

package store

import (
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// Values of the previous_source label of intentEpochChangesTotal.
const (
	// previousSourcePresent labels an adoption of a record written by an older source object.
	previousSourcePresent = "present"
	// previousSourceAbsent labels an adoption of a record without a recorded source, i.e. one
	// written only by unsourced intents before sources were recorded.
	previousSourceAbsent = "absent"
)

// intentEpochChangesTotal counts committed sourced intents that adopted a component record from a
// newer source object, at most once per adoption: an adoption whose commit outcome is ambiguous may
// go unreported. Component names and UIDs are deliberately not labels: they are unbounded.
var intentEpochChangesTotal = newIntentEpochChangesCounter()

// newIntentEpochChangesCounter registers the counter on the default registry and initializes both
// label values, so that each series is exported at 0 before its first adoption.
func newIntentEpochChangesCounter() *prometheus.CounterVec {
	counter := promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "xmc_cms_component_epoch_changes_total",
		Help: "Committed component intents whose newer source object adopted an existing record, " +
			"previously written by an older object (previous_source=present) or with no recorded " +
			"source (previous_source=absent).",
	}, []string{"previous_source"})
	counter.WithLabelValues(previousSourcePresent)
	counter.WithLabelValues(previousSourceAbsent)
	return counter
}

// countIntentEpochChange increments intentEpochChangesTotal for an adoption of a record whose
// recorded source was previous (nil for a record without one).
func countIntentEpochChange(previous *IntentSource) {
	label := previousSourcePresent
	if previous == nil {
		label = previousSourceAbsent
	}
	intentEpochChangesTotal.WithLabelValues(label).Inc()
}
