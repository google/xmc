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

// Package artifactlifecycle provides the Experiment State Server artifact lifecycle framework.
package artifactlifecycle

import (
	"context"
	"fmt"
	"strings"

	essmsgpb "xmanager_cloud/api/protos/experiment"
	taskqueuepb "xmanager_cloud/api/protos/task_queue"
	"xmanager_cloud/internal/logging"
	"xmanager_cloud/internal/storage"
)

// Rule defines an artifact lifecycle policy for an artifact type.
type Rule interface {
	// Name returns a human-readable identifier for the rule.
	Name() string

	// Matches returns true if this rule governs the given artifact.
	Matches(artifact *essmsgpb.Artifact) bool

	// TargetExecutionStates returns the set of WorkUnitExecution states that require notification.
	TargetExecutionStates() []essmsgpb.WorkUnitStatus_WorkUnitState

	// OnExecutionStateChanged handles a matching state transition event.
	OnExecutionStateChanged(ctx context.Context, store storage.Storage, event *taskqueuepb.ArtifactLifecycleEvent, artifact *essmsgpb.Artifact) error

	// OnExecutionSuperseded is invoked when RestartWorkUnit advances to EID+1
	// to cleanly finalize external resources associated with the superseded execution.
	OnExecutionSuperseded(ctx context.Context, store storage.Storage, xid, wid, oldEID int64, artifact *essmsgpb.Artifact) error
}

// Registry manages the set of registered artifact lifecycle rules.
type Registry struct {
	rules []Rule
}

// NewRegistry creates a new Registry with the provided rules.
func NewRegistry(rules ...Rule) *Registry {
	return &Registry{rules: rules}
}

// Register adds a rule to the registry.
func (r *Registry) Register(rule Rule) {
	if rule != nil {
		r.rules = append(r.rules, rule)
	}
}

// Rules returns all registered rules.
func (r *Registry) Rules() []Rule {
	return r.rules
}

// RequiredNotifyWhenAnnotation computes the dot-separated string (e.g. "STARTING.RUNNING.COMPLETED.FAILED.STOPPED")
// of target execution states required by any matching rules for the given slice of artifacts.
// Returns "" if no matching rules require notification.
func (r *Registry) RequiredNotifyWhenAnnotation(artifacts []*essmsgpb.Artifact) string {
	if r == nil || len(artifacts) == 0 {
		return ""
	}

	stateSet := make(map[essmsgpb.WorkUnitStatus_WorkUnitState]bool)
	for _, art := range artifacts {
		for _, rule := range r.rules {
			if rule.Matches(art) {
				for _, state := range rule.TargetExecutionStates() {
					stateSet[state] = true
				}
			}
		}
	}
	if len(stateSet) == 0 {
		return ""
	}

	var states []string
	canonicalOrder := []essmsgpb.WorkUnitStatus_WorkUnitState{
		essmsgpb.WorkUnitStatus_STARTING,
		essmsgpb.WorkUnitStatus_RUNNING,
		essmsgpb.WorkUnitStatus_COMPLETED,
		essmsgpb.WorkUnitStatus_FAILED,
		essmsgpb.WorkUnitStatus_STOPPED,
	}
	for _, s := range canonicalOrder {
		if stateSet[s] {
			states = append(states, s.String())
		}
	}
	return strings.Join(states, ".")
}

// HandleExecutionStateChanged dispatches an event to all matching rules for the work unit's artifacts.
func (r *Registry) HandleExecutionStateChanged(ctx context.Context, store storage.Storage, event *taskqueuepb.ArtifactLifecycleEvent) error {
	if r == nil || event == nil {
		return nil
	}
	parent := fmt.Sprintf("experiments/%d/workUnits/%d", event.GetExperimentId(), event.GetWorkUnitId())
	artifacts, _, err := store.ListArtifacts(ctx, parent, 100, "", "", "", 0)
	if err != nil {
		return fmt.Errorf("failed to list artifacts for %s: %w", parent, err)
	}

	for _, art := range artifacts {
		for _, rule := range r.rules {
			if rule.Matches(art) {
				if err := rule.OnExecutionStateChanged(ctx, store, event, art); err != nil {
					logging.Error(ctx, "Lifecycle rule OnExecutionStateChanged failed", "rule", rule.Name(), "error", err)
					return err
				}
			}
		}
	}
	return nil
}

// HandleExecutionSuperseded dispatches superseded execution cleanup to all matching rules.
func (r *Registry) HandleExecutionSuperseded(ctx context.Context, store storage.Storage, xid, wid, oldEID int64) error {
	if r == nil {
		return nil
	}
	parent := fmt.Sprintf("experiments/%d/workUnits/%d", xid, wid)
	artifacts, _, err := store.ListArtifacts(ctx, parent, 100, "", "", "", 0)
	if err != nil {
		return fmt.Errorf("failed to list artifacts for %s: %w", parent, err)
	}

	for _, art := range artifacts {
		for _, rule := range r.rules {
			if rule.Matches(art) {
				if err := rule.OnExecutionSuperseded(ctx, store, xid, wid, oldEID, art); err != nil {
					logging.Error(ctx, "Lifecycle rule OnExecutionSuperseded failed", "rule", rule.Name(), "error", err)
					return err
				}
			}
		}
	}
	return nil
}
