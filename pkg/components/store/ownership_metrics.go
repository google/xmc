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
	"slices"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

// Values of the reason label on xmc_cms_component_owner_adoptions_total.
const (
	// AdoptionReasonInitial labels a sourced creation of a component record (R1).
	AdoptionReasonInitial = "initial"
	// AdoptionReasonSameClusterRecreate labels a same-cluster CAS adoption by a new CR UID (R5).
	AdoptionReasonSameClusterRecreate = "same_cluster_recreate"
	// AdoptionReasonEpochTakeover labels an adoption of a record reassigned by a fence takeover (R2).
	AdoptionReasonEpochTakeover = "epoch_takeover"
	// AdoptionReasonUnsourcedClaim labels the first sourced claim of a record written without a source (R2).
	AdoptionReasonUnsourcedClaim = "unsourced_claim"
)

// OwnerStaleThreshold is the age after which an active component owner's last-seen timestamp is
// counted as stale by xmc_cms_component_owner_stale. A fence takeover sets SourceLastSeenTime to
// its commit time, so reassigned records get the same period as an adoption grace period.
const OwnerStaleThreshold = 15 * time.Minute

// sourceConflictReasons lists the ErrorInfo reasons counted by
// xmc_cms_component_source_conflicts_total; each is pre-registered as a label value.
var sourceConflictReasons = []string{
	ReasonStaleGeneration,
	ReasonSourceIncarnationMismatch,
	ReasonSourceNamespaceConflict,
	ReasonClusterFenced,
	ReasonClusterEpochRegressed,
	ReasonClusterNotDeclared,
}

var (
	ownerAdoptionsTotal       = newOwnerAdoptionsCounter()
	sourceConflictsTotal      = newSourceConflictsCounter()
	clusterFenceEpochGauge    = newClusterFenceEpochGauge()
	clusterFenceLastSeenGauge = newClusterFenceLastSeenGauge()
	componentOwnerStaleGauge  = newComponentOwnerStaleGauge()
)

func newOwnerAdoptionsCounter() *prometheus.CounterVec {
	counter := promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "xmc_cms_component_owner_adoptions_total",
		Help: "Total number of committed component owner adoptions in CMS by reason.",
	}, []string{"reason"})
	for _, r := range []string{
		AdoptionReasonInitial,
		AdoptionReasonSameClusterRecreate,
		AdoptionReasonEpochTakeover,
		AdoptionReasonUnsourcedClaim,
	} {
		counter.WithLabelValues(r)
	}
	return counter
}

func newSourceConflictsCounter() *prometheus.CounterVec {
	counter := promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "xmc_cms_component_source_conflicts_total",
		Help: "Total number of component source ownership and cluster fence conflicts in CMS by reason.",
	}, []string{"reason"})
	for _, r := range sourceConflictReasons {
		counter.WithLabelValues(r)
	}
	return counter
}

func newClusterFenceEpochGauge() prometheus.Gauge {
	return promauto.NewGauge(prometheus.GaugeOpts{
		Name: "xmc_cms_cluster_fence_epoch",
		Help: "Active cluster ownership fence epoch declared in CMS.",
	})
}

func newClusterFenceLastSeenGauge() prometheus.Gauge {
	return promauto.NewGauge(prometheus.GaugeOpts{
		Name: "xmc_cms_cluster_fence_last_seen_timestamp_seconds",
		Help: "Unix time at which the active cluster last declared its ownership fence epoch to CMS.",
	})
}

func newComponentOwnerStaleGauge() prometheus.Gauge {
	return promauto.NewGauge(prometheus.GaugeOpts{
		Name: "xmc_cms_component_owner_stale",
		Help: "Number of active owned components in CMS whose owner was last seen 15 minutes ago or more.",
	})
}

// OwnerAdoptionsCounter returns the counter for the given adoption reason label.
func OwnerAdoptionsCounter(reason string) prometheus.Counter {
	return ownerAdoptionsTotal.WithLabelValues(reason)
}

// SourceConflictsCounter returns the counter for the given source conflict reason label.
func SourceConflictsCounter(reason string) prometheus.Counter {
	return sourceConflictsTotal.WithLabelValues(reason)
}

// ClusterFenceEpochGauge returns the gauge tracking the active cluster fence epoch.
func ClusterFenceEpochGauge() prometheus.Gauge {
	return clusterFenceEpochGauge
}

// ClusterFenceLastSeenGauge returns the gauge tracking the fence's LastSeenTime as Unix seconds.
func ClusterFenceLastSeenGauge() prometheus.Gauge {
	return clusterFenceLastSeenGauge
}

// ComponentOwnerStaleGauge returns the gauge tracking active components with stale owners.
func ComponentOwnerStaleGauge() prometheus.Gauge {
	return componentOwnerStaleGauge
}

// adoptionReason classifies how a committed sourced write changed the record owner from prev.
func adoptionReason(prev *SourceOwner, src *RequestSource, created bool) string {
	if src == nil {
		return ""
	}
	switch {
	case created:
		return AdoptionReasonInitial
	case prev == nil:
		return AdoptionReasonUnsourcedClaim
	case prev.UID == "":
		return AdoptionReasonEpochTakeover
	case prev.UID != src.UID:
		return AdoptionReasonSameClusterRecreate
	default:
		return ""
	}
}

// RecordOwnerAdoption increments xmc_cms_component_owner_adoptions_total when a committed write
// adopts a component record under src.
func RecordOwnerAdoption(prev *SourceOwner, src *RequestSource, created bool) {
	if reason := adoptionReason(prev, src, created); reason != "" {
		ownerAdoptionsTotal.WithLabelValues(reason).Inc()
	}
}

// RecordSourceConflictError increments xmc_cms_component_source_conflicts_total when err carries a
// source ownership or cluster fence conflict reason, and returns err unchanged.
func RecordSourceConflictError(err error) error {
	if reason := ErrorInfoReason(err); slices.Contains(sourceConflictReasons, reason) {
		sourceConflictsTotal.WithLabelValues(reason).Inc()
	}
	return err
}

// RecordOwnershipGauges sets the CMS ownership gauges from g. The fence gauge exports the stored
// LastSeenTime rather than its age, so an alert on its age keeps growing even when no replica can
// refresh it. Before the first declaration both fence gauges report 0.
func RecordOwnershipGauges(g OwnershipGauges) {
	componentOwnerStaleGauge.Set(float64(g.StaleOwners))
	if !g.FenceDeclared {
		clusterFenceEpochGauge.Set(0)
		clusterFenceLastSeenGauge.Set(0)
		return
	}
	clusterFenceEpochGauge.Set(float64(g.FenceEpoch))
	clusterFenceLastSeenGauge.Set(float64(g.FenceLastSeenTime.UnixNano()) / 1e9)
}

// isOwnerStale reports whether rec is an active owned record whose SourceLastSeenTime is at or
// before cutoff. It matches the WHERE clause of SpannerStore's stale-owner count.
func isOwnerStale(rec *ComponentRecord, cutoff time.Time) bool {
	if IsDeletedRecord(rec) || rec.Owner == nil || rec.SourceLastSeenTime.IsZero() {
		return false
	}
	return !rec.SourceLastSeenTime.After(cutoff)
}

// CountStaleOwnedComponents returns the number of active records in catalog whose owner is stale
// at now.
func CountStaleOwnedComponents(catalog map[string]*ComponentRecord, now time.Time) int64 {
	cutoff := now.Add(-OwnerStaleThreshold)
	var count int64
	for _, rec := range catalog {
		if isOwnerStale(rec, cutoff) {
			count++
		}
	}
	return count
}
