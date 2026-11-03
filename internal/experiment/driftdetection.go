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

package experiment

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"
	componentspb "xmanager_cloud/api/protos/components/v1alpha1"
	essmsgpb "xmanager_cloud/api/protos/experiment"
	"xmanager_cloud/internal/logging"
	"xmanager_cloud/internal/storage"
)

const (
	maxDriftDetails    = 10
	driftCacheTTL      = 30 * time.Second
	maxDriftCacheItems = 1000
)

type driftCacheEntry struct {
	diffs             []string
	experimentalNames []string
	expiresAt         time.Time
}

type driftCache struct {
	mu      sync.RWMutex
	entries map[int64]driftCacheEntry
}

func newDriftCache() *driftCache {
	return &driftCache{
		entries: make(map[int64]driftCacheEntry),
	}
}

func (c *driftCache) get(xid int64, now time.Time) (driftCacheEntry, bool) {
	if c == nil {
		return driftCacheEntry{}, false
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	entry, ok := c.entries[xid]
	if !ok || now.After(entry.expiresAt) {
		return driftCacheEntry{}, false
	}
	return entry, true
}

func (c *driftCache) set(xid int64, diffs, experimentalNames []string, now time.Time) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()

	if len(c.entries) >= maxDriftCacheItems {
		for k, v := range c.entries {
			if now.After(v.expiresAt) {
				delete(c.entries, k)
			}
		}
	}

	c.entries[xid] = driftCacheEntry{
		diffs:             diffs,
		experimentalNames: experimentalNames,
		expiresAt:         now.Add(driftCacheTTL),
	}
}

// DriftStore provides the storage lookup required to evaluate experiment component drift and stability.
type DriftStore interface {
	FetchDriftedComponents(ctx context.Context, xid int64, limit int) ([]storage.ComponentDiff, error)
	FetchExperimentSnapshotComponents(ctx context.Context, xid int64) ([]storage.ExperimentSnapshotComponent, error)
}

// DriftEvaluator detects component drift and experimental component usage for active experiments
// and caches results per experiment for a short TTL.
type DriftEvaluator struct {
	store            DriftStore
	enableComponents bool
	driftCache       *driftCache
}

// NewDriftEvaluator creates a DriftEvaluator backed by store. When enableComponents is false,
// Evaluate is a no-op.
func NewDriftEvaluator(store DriftStore, enableComponents bool) *DriftEvaluator {
	return &DriftEvaluator{
		store:            store,
		enableComponents: enableComponents,
		driftCache:       newDriftCache(),
	}
}

// Evaluate checks whether an active experiment's component snapshots have diverged
// from live component catalog generations or reference experimental components. If drift or
// experimental component usage is detected, warning notices are attached to the status.
func (e *DriftEvaluator) Evaluate(ctx context.Context, xid int64, expStatus *essmsgpb.ExperimentStatus) error {
	if !e.enableComponents {
		return nil
	}
	if expStatus == nil || expStatus.GetActiveState() != essmsgpb.ExperimentStatus_ACTIVE {
		return nil
	}

	now := time.Now()
	if entry, ok := e.driftCache.get(xid, now); ok {
		attachComponentWarnings(expStatus, entry.diffs, entry.experimentalNames)
		return nil
	}

	diffs, err := e.fetchDriftDetails(ctx, xid)
	if err != nil {
		return fmt.Errorf("failed to fetch drift details: %w", err)
	}
	experimentalNames, err := e.fetchExperimentalSnapshotNames(ctx, xid)
	if err != nil {
		return fmt.Errorf("failed to fetch experimental snapshot components: %w", err)
	}

	e.driftCache.set(xid, diffs, experimentalNames, now)
	prevText := expStatus.GetStructuredStatusMessage().GetUserFacingText()
	attachComponentWarnings(expStatus, diffs, experimentalNames)
	if updatedText := expStatus.GetStructuredStatusMessage().GetUserFacingText(); updatedText != prevText {
		logging.Warn(ctx, "Attached component warning notice", "xid", xid, "warning", updatedText)
	}

	return nil
}

func attachComponentWarnings(expStatus *essmsgpb.ExperimentStatus, diffs, experimentalNames []string) {
	if len(experimentalNames) > 0 {
		msg := formatExperimentalWarningMessage(experimentalNames)
		attachWarningMessage(expStatus, msg, "uses experimental component")
	}
	if len(diffs) > 0 {
		attachDriftWarningMessage(expStatus, formatDriftWarningMessage(diffs))
	}
}

// fetchExperimentalSnapshotNames returns the names of snapshotted EXPERIMENTAL components.
func (e *DriftEvaluator) fetchExperimentalSnapshotNames(ctx context.Context, xid int64) ([]string, error) {
	snapshots, err := e.store.FetchExperimentSnapshotComponents(ctx, xid)
	if err != nil {
		return nil, err
	}
	var names []string
	for _, snap := range snapshots {
		if snap.Spec == nil || snap.Spec.GetStability() != componentspb.ComponentStability_COMPONENT_STABILITY_EXPERIMENTAL {
			continue
		}
		if len(names) >= maxDriftDetails {
			break
		}
		name := fmt.Sprintf("component_id=%d", snap.ComponentID)
		if snap.Name != nil && *snap.Name != "" {
			name = *snap.Name
		}
		names = append(names, name)
	}
	return names, nil
}

// fetchDriftDetails queries drifted component generation comparisons for an experiment.
func (e *DriftEvaluator) fetchDriftDetails(ctx context.Context, xid int64) ([]string, error) {
	components, err := e.store.FetchDriftedComponents(ctx, xid, maxDriftDetails+1)
	if err != nil {
		return nil, err
	}

	diffs := make([]string, 0, maxDriftDetails+1)
	for _, component := range components {
		if len(diffs) >= maxDriftDetails {
			diffs = append(diffs, "...")
			break
		}
		diffs = append(diffs, formatComponentDiff(component))
	}
	return diffs, nil
}

// formatComponentDiff formats the description of a single drifted component.
func formatComponentDiff(diff storage.ComponentDiff) string {
	if diff.Name == nil {
		return fmt.Sprintf("Component ID %d (missing from catalog, snapshot generation %d)", diff.ComponentID, diff.LockedGeneration)
	}
	if diff.State != nil && *diff.State == storage.ComponentStateDeleted {
		return fmt.Sprintf("%s (deleted from catalog, snapshot generation %d)", *diff.Name, diff.LockedGeneration)
	}
	var observedGeneration int64
	if diff.ObservedGeneration != nil {
		observedGeneration = *diff.ObservedGeneration
	}
	return fmt.Sprintf("%s (snapshot generation %d != live observed generation %d)", *diff.Name, diff.LockedGeneration, observedGeneration)
}

// formatDriftWarningMessage constructs a StructuredMessage with WARNING severity.
func formatDriftWarningMessage(diffs []string) *essmsgpb.StructuredMessage {
	warningText := "Component drift detected: live component generations diverge from snapshots"
	if len(diffs) > 0 {
		warningText = fmt.Sprintf("%s: %s", warningText, strings.Join(diffs, ", "))
	}
	return &essmsgpb.StructuredMessage{
		Severity:       essmsgpb.StructuredMessage_WARNING,
		Source:         essmsgpb.StructuredMessage_XMANAGER_BACKEND,
		UserFacingText: warningText,
		CreateTime:     timestamppb.Now(),
	}
}

// formatExperimentalWarningMessage constructs a StructuredMessage with WARNING severity for experimental components.
func formatExperimentalWarningMessage(componentNames []string) *essmsgpb.StructuredMessage {
	warningText := "uses experimental component"
	if len(componentNames) == 1 {
		warningText = fmt.Sprintf("uses experimental component %s", componentNames[0])
	} else if len(componentNames) > 1 {
		warningText = fmt.Sprintf("uses experimental components: %s", strings.Join(componentNames, ", "))
	}
	return &essmsgpb.StructuredMessage{
		Severity:       essmsgpb.StructuredMessage_WARNING,
		Source:         essmsgpb.StructuredMessage_XMANAGER_BACKEND,
		UserFacingText: warningText,
		CreateTime:     timestamppb.Now(),
	}
}

// attachDriftWarningMessage safely sets or merges the drift notice into expStatus.
func attachDriftWarningMessage(expStatus *essmsgpb.ExperimentStatus, driftMsg *essmsgpb.StructuredMessage) {
	attachWarningMessage(expStatus, driftMsg, "Component drift detected")
}

// attachWarningMessage safely sets or merges warningMsg into expStatus unless dedupSubstring is already present.
func attachWarningMessage(expStatus *essmsgpb.ExperimentStatus, warningMsg *essmsgpb.StructuredMessage, dedupSubstring string) {
	currentMsg := expStatus.GetStructuredStatusMessage()
	if currentMsg == nil {
		expStatus.StructuredStatusMessage = warningMsg
		return
	}

	// Preserve higher-severity messages (e.g., ERROR, FATAL) without modification.
	if currentMsg.GetSeverity() > essmsgpb.StructuredMessage_WARNING {
		return
	}

	// Prevent duplicate warning notices.
	if dedupSubstring != "" && strings.Contains(currentMsg.GetUserFacingText(), dedupSubstring) {
		return
	}

	// Upgrade severity if current severity is lower than WARNING (e.g. INFO, UNSPECIFIED).
	if currentMsg.GetSeverity() < essmsgpb.StructuredMessage_WARNING {
		currentMsg.Severity = essmsgpb.StructuredMessage_WARNING
	}

	if currentMsg.GetUserFacingText() == "" {
		currentMsg.UserFacingText = warningMsg.GetUserFacingText()
		return
	}
	currentMsg.UserFacingText = fmt.Sprintf("%s; %s", currentMsg.GetUserFacingText(), warningMsg.GetUserFacingText())
}
