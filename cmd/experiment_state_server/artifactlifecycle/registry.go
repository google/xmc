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

	essmsgpb "xmanager_cloud/api/protos/experiment"
	taskqueuepb "xmanager_cloud/api/protos/task_queue"
	"xmanager_cloud/internal/kubernetes/common/notifywhen"
	"xmanager_cloud/internal/logging"
	"xmanager_cloud/internal/storage"
)

// LifecycleRule declaratively defines an artifact lifecycle policy for an artifact type.
type LifecycleRule struct {
	// Name is a human-readable identifier for the rule.
	Name string

	// Matches returns true if this rule governs the given artifact.
	Matches func(artifact *essmsgpb.Artifact) bool

	// TargetExecutionStates is the set of WorkUnitExecution states that require notification.
	TargetExecutionStates []essmsgpb.WorkUnitStatus_WorkUnitState

	// OnExecutionStateChanged handles a matching state transition event.
	OnExecutionStateChanged func(ctx context.Context, store storage.Storage, event *taskqueuepb.ArtifactLifecycleEvent, artifact *essmsgpb.Artifact) error

	// OnExecutionSuperseded is invoked when RestartWorkUnit advances to EID+1
	// to cleanly finalize external resources associated with the superseded execution.
	OnExecutionSuperseded func(ctx context.Context, store storage.Storage, xid, wid, oldEID int64, artifact *essmsgpb.Artifact) error
}

// Registry manages the set of registered artifact lifecycle rules.
type Registry struct {
	rules []LifecycleRule
}

// NewRegistry creates a new Registry with the provided rules.
func NewRegistry(rules ...LifecycleRule) *Registry {
	return &Registry{rules: rules}
}

// Register adds a rule to the registry.
func (r *Registry) Register(rule LifecycleRule) {
	if rule.Matches != nil {
		r.rules = append(r.rules, rule)
	}
}

// Rules returns all registered rules.
func (r *Registry) Rules() []LifecycleRule {
	if r == nil {
		return nil
	}
	return r.rules
}

// RequiredNotifyWhenAnnotation returns the notify-when annotation value (see
// notifywhen.Encode) for the union of target execution states required by rules
// matching any of the given artifacts. Returns "" if no matching rule requires
// notification.
func (r *Registry) RequiredNotifyWhenAnnotation(artifacts []*essmsgpb.Artifact) string {
	if r == nil {
		return ""
	}
	var states []essmsgpb.WorkUnitStatus_WorkUnitState
	for _, art := range artifacts {
		for _, rule := range r.matchingRules(art) {
			states = append(states, rule.TargetExecutionStates...)
		}
	}
	return notifywhen.Encode(states)
}

// matchingRules returns the registered rules that govern the artifact.
func (r *Registry) matchingRules(artifact *essmsgpb.Artifact) []LifecycleRule {
	var matched []LifecycleRule
	for _, rule := range r.rules {
		if rule.Matches != nil && rule.Matches(artifact) {
			matched = append(matched, rule)
		}
	}
	return matched
}

// HandleExecutionStateChanged dispatches an event to all matching rules for the work unit's artifacts.
func (r *Registry) HandleExecutionStateChanged(ctx context.Context, store storage.Storage, event *taskqueuepb.ArtifactLifecycleEvent) error {
	if r == nil || event == nil {
		return nil
	}
	artifacts, err := ListWorkUnitArtifacts(ctx, store, event.GetExperimentId(), event.GetWorkUnitId())
	if err != nil {
		return err
	}
	for _, art := range artifacts {
		for _, rule := range r.matchingRules(art) {
			if rule.OnExecutionStateChanged == nil {
				continue
			}
			if err := rule.OnExecutionStateChanged(ctx, store, event, art); err != nil {
				logging.Error(ctx, "Lifecycle rule OnExecutionStateChanged failed", "rule", rule.Name, "error", err)
				return err
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
	artifacts, err := ListWorkUnitArtifacts(ctx, store, xid, wid)
	if err != nil {
		return err
	}
	for _, art := range artifacts {
		for _, rule := range r.matchingRules(art) {
			if rule.OnExecutionSuperseded == nil {
				continue
			}
			if err := rule.OnExecutionSuperseded(ctx, store, xid, wid, oldEID, art); err != nil {
				logging.Error(ctx, "Lifecycle rule OnExecutionSuperseded failed", "rule", rule.Name, "error", err)
				return err
			}
		}
	}
	return nil
}
