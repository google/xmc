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
	"context"
	"time"

	"helm.sh/helm/v3/pkg/release"

	"xmanager_cloud/internal/logging"
)

// Repair backoff rate-limits re-upgrades of a release that keeps drifting (e.g. another actor keeps deleting one of
// its objects): each repair writes a Helm revision, so repairs back off exponentially from repairBackoffBase up to
// repairBackoffMax, with up to repairBackoffJitter extra randomized delay. This is separate from, and deliberately
// longer than, transient-error retry backoff.
const (
	repairBackoffBase   = 30 * time.Second
	repairBackoffMax    = 10 * time.Minute
	repairBackoffJitter = 0.1
)

// repairAttempt records the repairs consumed for a component since its backoff was last reset.
type repairAttempt struct {
	count     int
	last      time.Time
	notBefore time.Time
}

// NeedsRepair returns true if the component is active, was actuated at the given generation, and its release has
// since been found missing, failed, or drifted. A pending repair for an older generation does not count: a new spec
// goes through the regular (dependency-gated) upgrade path instead.
func (m *K8sHelmManager) NeedsRepair(ref ComponentRef, generation int64) bool {
	if m == nil || ref.Name == "" || ref.Namespace == "" {
		return false
	}
	key := componentKey(ref.Namespace, ref.Name)
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.repairNeeded[key] && m.activeState[key] && m.actuatedGenerations[key] == generation
}

// isRepairMarked reports whether the component is marked for repair, regardless of generation.
func (m *K8sHelmManager) isRepairMarked(ref ComponentRef) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.repairNeeded[componentKey(ref.Namespace, ref.Name)]
}

// resetRepairBackoffIfStableLocked drops the repair backoff once the release has stayed healthy for repairBackoffMax
// after the last repair. Resetting on any healthy verdict would defeat the backoff: a repaired release is briefly
// healthy before an actor that keeps deleting its objects strikes again. Callers must hold m.mu.
func (m *K8sHelmManager) resetRepairBackoffIfStableLocked(key string, now time.Time) {
	attempt, ok := m.repairBackoff[key]
	if ok && now.Sub(attempt.last) >= repairBackoffMax {
		delete(m.repairBackoff, key)
	}
}

// recordRepairAttempt consumes the component's repair marker and arms its backoff for the next repair.
func (m *K8sHelmManager) recordRepairAttempt(ctx context.Context, ref ComponentRef) {
	key := componentKey(ref.Namespace, ref.Name)
	now := time.Now()
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.repairNeeded, key)
	attempt := m.repairBackoff[key]
	attempt.count++
	attempt.last = now
	attempt.notBefore = now.Add(m.repairDelay(attempt.count))
	m.repairBackoff[key] = attempt
	logging.Info(ctx, "Repairing component Helm release", "name", ref.Name, "namespace", ref.Namespace, "attempt", attempt.count, "nextRepairNotBefore", attempt.notBefore)
}

// repairDelay returns the jittered backoff that must elapse after the attempt-th repair before the next one:
// repairBackoffBase doubled per attempt, capped at repairBackoffMax, plus up to repairBackoffJitter of extra delay.
func (m *K8sHelmManager) repairDelay(attempt int) time.Duration {
	delay := repairBackoffMax
	if shift := attempt - 1; shift < 16 && repairBackoffBase<<shift < repairBackoffMax {
		delay = repairBackoffBase << shift
	}
	return delay + time.Duration(float64(delay)*repairBackoffJitter*m.jitter())
}

// clearRepairLocked drops the component's repair marker and backoff. Callers must hold m.mu.
func (m *K8sHelmManager) clearRepairLocked(key string) {
	delete(m.repairNeeded, key)
	delete(m.repairBackoff, key)
}

// releaseNeedsRepair reports whether a health verdict means the release must be re-applied: it is missing, its
// objects drifted, or its latest revision is terminally failed or uninstalled. Every other verdict, including a
// release that is uninstalling, never triggers repair so the bridge cannot race another actor's Helm operation.
func releaseNeedsRepair(reason string, status release.Status) bool {
	switch reason {
	case ReasonHelmReleaseNotFound, ReasonHelmReleaseDrifted:
		return true
	case ReasonHelmReleaseNotReady:
		return status == release.StatusFailed || status == release.StatusUninstalled
	}
	return false
}
