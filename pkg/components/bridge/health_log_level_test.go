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
	"cmp"
	"context"
	"log/slog"
	"slices"
	"testing"
	"time"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

const evaluatedHealthMessage = "Evaluated component health"

// TestReconcileComponent_HealthLogLevel checks that a health evaluation logs at Info only when it
// changes the Ready or Degraded condition recorded in the custom resource, or is the first
// evaluation of a generation, and at Debug for the unchanged result of a periodic pass. The logged
// reason is the final Ready reason, e.g. StaleIntent when CMS rejects the observed-state report.
func TestReconcileComponent_HealthLogLevel(t *testing.T) {
	in := sourcedRedpandaInput("uid-a", time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC), 7)
	steady := healthyConditions(t, in)
	tests := []struct {
		name       string
		existing   []Condition
		obsErr     error
		wantLevel  slog.Level
		wantReason string
	}{
		{name: "first evaluation", wantLevel: slog.LevelInfo},
		{name: "unchanged health", existing: steady, wantLevel: slog.LevelDebug},
		{
			name:      "Ready message only changed",
			existing:  withCondition(steady, ConditionTypeReady, func(c *Condition) { c.Message = "previous message" }),
			wantLevel: slog.LevelDebug,
		},
		{
			name:      "Ready reason changed",
			existing:  withCondition(steady, ConditionTypeReady, func(c *Condition) { c.Reason = ReasonHelmStatusUnavailable }),
			wantLevel: slog.LevelInfo,
		},
		{
			name:      "Ready status changed",
			existing:  withCondition(steady, ConditionTypeReady, func(c *Condition) { c.Status = ConditionStatusFalse }),
			wantLevel: slog.LevelInfo,
		},
		{
			name:      "Degraded status changed",
			existing:  withCondition(steady, ConditionTypeDegraded, func(c *Condition) { c.Status = ConditionStatusTrue }),
			wantLevel: slog.LevelInfo,
		},
		{
			name:      "first evaluation of a new generation",
			existing:  withCondition(steady, ConditionTypeReady, func(c *Condition) { c.ObservedGeneration = in.Generation - 1 }),
			wantLevel: slog.LevelInfo,
		},
		{
			name:       "stale observed-state report",
			existing:   steady,
			obsErr:     status.Error(codes.FailedPrecondition, `stale observation: component "redpanda" is owned by source "uid-b"`),
			wantLevel:  slog.LevelInfo,
			wantReason: ReasonStaleIntent,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r, err := NewReconciler(&mockCMS{obsErr: tt.obsErr}, nil, nil)
			if err != nil {
				t.Fatalf("NewReconciler() error = %v", err)
			}
			ctx, logs := recordLogs()
			pass := in
			pass.ExistingConditions = tt.existing
			if _, err := r.ReconcileComponent(ctx, pass); err != nil {
				t.Fatalf("ReconcileComponent() error = %v", err)
			}
			got := logs.singleRecord(t, evaluatedHealthMessage)
			if got.level != tt.wantLevel {
				t.Errorf("%q level = %v, want %v", evaluatedHealthMessage, got.level, tt.wantLevel)
			}
			if wantReason := cmp.Or(tt.wantReason, ReasonComponentHealthy); got.attrs["reason"] != wantReason {
				t.Errorf("%q reason = %v, want %q", evaluatedHealthMessage, got.attrs["reason"], wantReason)
			}
		})
	}
}

// healthyConditions returns the conditions of one pass over in, which must report it healthy.
func healthyConditions(t *testing.T, in ReconcileComponentInput) []Condition {
	t.Helper()
	r, err := NewReconciler(&mockCMS{}, nil, nil)
	if err != nil {
		t.Fatalf("NewReconciler() error = %v", err)
	}
	res, err := r.ReconcileComponent(context.Background(), in)
	if err != nil {
		t.Fatalf("ReconcileComponent() error = %v", err)
	}
	if got := findCondition(res.Conditions, ConditionTypeReady); got == nil || got.Status != ConditionStatusTrue {
		t.Fatalf("Ready condition = %+v, want status %q", got, ConditionStatusTrue)
	}
	return res.Conditions
}

// withCondition returns a copy of conds with mutate applied to the condition of type condType.
func withCondition(conds []Condition, condType string, mutate func(c *Condition)) []Condition {
	out := slices.Clone(conds)
	if c := findCondition(out, condType); c != nil {
		mutate(c)
	}
	return out
}
