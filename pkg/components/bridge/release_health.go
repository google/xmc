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
	"errors"
	"fmt"
	"strings"
	"time"

	"helm.sh/helm/v3/pkg/release"
	"helm.sh/helm/v3/pkg/storage/driver"

	"xmanager_cloud/logging/logging"
)

// healthCacheTTL bounds how long a deployed-release verdict is reused before ComponentHealth re-queries Helm.
const healthCacheTTL = time.Minute

// ComponentHealth reports whether the component's last Helm upgrade succeeded, no upgrade is in flight, and its
// release is deployed. Deployed verdicts are cached for healthCacheTTL to avoid querying Helm on every poll.
func (m *K8sHelmManager) ComponentHealth(ctx context.Context, ref ComponentRef) (bool, string, string) {
	if m == nil {
		return false, ReasonHelmRunnerUninitialized, "Helm manager is uninitialized; cannot verify release status"
	}
	if _, err := m.validateAndResolve(ref); err != nil {
		return false, ReasonInvalidComponentRef, err.Error()
	}
	if err := m.GetUpgradeError(ref); err != nil {
		return false, ReasonHelmUpgradeFailed, fmt.Sprintf("Helm upgrade failed: %v", err)
	}
	if m.isUpgradePending(ref) {
		return false, ReasonHelmUpgradePending, "Helm upgrade is queued or in progress"
	}
	if m.runner == nil {
		return false, ReasonHelmRunnerUninitialized, "Helm action runner is uninitialized; cannot verify release status"
	}
	return m.releaseHealth(ctx, ref)
}

// releaseHealth returns the cached deployed verdict if still fresh, otherwise queries Helm, verifies that every object
// in the deployed release still exists, and caches a healthy result. A missing, terminally failed, or drifted release
// marks the component for repair so the next reconcile pass re-runs its Helm upgrade at the current generation.
func (m *K8sHelmManager) releaseHealth(ctx context.Context, ref ComponentRef) (bool, string, string) {
	releaseName := ComponentReleaseName(ref.Name)
	m.mu.RLock()
	verifiedAt, cached := m.healthVerifiedAt[ref.String()]
	m.mu.RUnlock()
	if cached && time.Since(verifiedAt) < healthCacheTTL {
		return true, ReasonComponentHealthy, fmt.Sprintf("Helm release %q deployed", releaseName)
	}
	rel, err := m.runner.Status(ctx, releaseName, ref.Namespace)
	healthy, reason, msg := evaluateReleaseHealth(releaseName, rel, err)
	if healthy {
		healthy, reason, msg = m.releaseResourceHealth(ctx, releaseName, rel)
	}
	m.recordReleaseHealth(ctx, ref, healthy, releaseNeedsRepair(reason, releaseStatus(rel)), reason)
	return healthy, reason, msg
}

// releaseResourceHealth reports drift when objects rendered by a deployed release no longer exist in the cluster.
// Lookup failures are transient: the deployed verdict is kept so an API blip never triggers a re-upgrade.
func (m *K8sHelmManager) releaseResourceHealth(ctx context.Context, releaseName string, rel *release.Release) (bool, string, string) {
	deployedMsg := fmt.Sprintf("Helm release %q deployed", releaseName)
	missing, err := m.runner.MissingResources(ctx, rel)
	if err != nil {
		logging.Warn(ctx, "Failed to verify Helm release resources; trusting deployed status", "release", releaseName, "err", err)
		return true, ReasonComponentHealthy, deployedMsg
	}
	if len(missing) == 0 {
		return true, ReasonComponentHealthy, deployedMsg
	}
	return false, ReasonHelmReleaseDrifted, fmt.Sprintf("Helm release %q is missing %d resource(s) (%s); the bridge re-applies the release, rate-limited by repair backoff", releaseName, len(missing), strings.Join(missing, ", "))
}

// recordReleaseHealth caches a healthy verdict, or marks the component for repair when the release needs re-applying
// and its repair backoff has elapsed.
func (m *K8sHelmManager) recordReleaseHealth(ctx context.Context, ref ComponentRef, healthy, repair bool, reason string) {
	key := componentKey(ref.Namespace, ref.Name)
	now := time.Now()
	m.mu.Lock()
	defer m.mu.Unlock()
	if healthy {
		if m.healthVerifiedAt == nil {
			m.healthVerifiedAt = make(map[string]time.Time)
		}
		m.healthVerifiedAt[ref.String()] = now
		m.resetRepairBackoffIfStableLocked(key, now)
		return
	}
	if !repair {
		return
	}
	if attempt, ok := m.repairBackoff[key]; ok && now.Before(attempt.notBefore) {
		logging.Info(ctx, "Component Helm release needs repair; deferring re-upgrade until repair backoff elapses", "name", ref.Name, "namespace", ref.Namespace, "reason", reason, "attempts", attempt.count, "notBefore", attempt.notBefore)
		return
	}
	if m.repairNeeded == nil {
		m.repairNeeded = make(map[string]bool)
	}
	m.repairNeeded[key] = true
	logging.Warn(ctx, "Component Helm release needs repair; scheduling re-upgrade at current generation", "name", ref.Name, "namespace", ref.Namespace, "reason", reason)
}

// evaluateReleaseHealth maps a Helm status query result to a health verdict, condition reason, and message.
func evaluateReleaseHealth(releaseName string, rel *release.Release, statusErr error) (bool, string, string) {
	if errors.Is(statusErr, driver.ErrReleaseNotFound) {
		return false, ReasonHelmReleaseNotFound, fmt.Sprintf("Helm release %q not found", releaseName)
	}
	if statusErr != nil {
		return false, ReasonHelmStatusUnavailable, fmt.Sprintf("failed to query Helm release %q status: %v", releaseName, statusErr)
	}
	status := releaseStatus(rel)
	if status.IsPending() {
		return false, ReasonHelmReleaseStuck, fmt.Sprintf("Helm release %q is stuck in %q with no upgrade in flight; recover with `helm rollback` (or `helm uninstall` for pending-install)", releaseName, status)
	}
	if status != release.StatusDeployed {
		return false, ReasonHelmReleaseNotReady, fmt.Sprintf("Helm release %q status is %q", releaseName, status)
	}
	return true, ReasonComponentHealthy, fmt.Sprintf("Helm release %q deployed", releaseName)
}

func releaseStatus(rel *release.Release) release.Status {
	if rel == nil || rel.Info == nil {
		return release.StatusUnknown
	}
	return rel.Info.Status
}
