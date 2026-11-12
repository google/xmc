// Copyright 2026 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//	http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package experiment

import (
	"context"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/testing/protocmp"
	componentspb "xmanager_cloud/api/protos/components/v1alpha1"
	essmsgpb "xmanager_cloud/api/protos/experiment"
	"xmanager_cloud/internal/experiment/experimenttest"
	"xmanager_cloud/pkg/components/store"
)

// hpoRecord returns an hpo record with params as its parameters.
func hpoRecord(params map[string]string) *store.ComponentRecord {
	return &store.ComponentRecord{Name: hpoComponentName, Spec: &componentspb.ComponentSpec{Parameters: params}}
}

func budgetParam(limit string) map[string]string {
	return map[string]string{maxTrialBudgetParameter: limit}
}

func TestStudyTrialBudgetLimit(t *testing.T) {
	tests := []struct {
		name string
		rec  *store.ComponentRecord
		want int32
	}{
		{name: "parameter", rec: hpoRecord(budgetParam("250")), want: 250},
		{name: "explicit plus sign", rec: hpoRecord(budgetParam("+250")), want: 250},
		{name: "int32 maximum", rec: hpoRecord(budgetParam("2147483647")), want: 2147483647},
		{name: "no hpo record", rec: nil, want: defaultMaxTrialBudget},
		{name: "no spec", rec: &store.ComponentRecord{Name: hpoComponentName}, want: defaultMaxTrialBudget},
		{name: "missing parameter", rec: hpoRecord(nil), want: defaultMaxTrialBudget},
		{name: "empty", rec: hpoRecord(budgetParam("")), want: defaultMaxTrialBudget},
		{name: "malformed", rec: hpoRecord(budgetParam("abc")), want: defaultMaxTrialBudget},
		{name: "decimal", rec: hpoRecord(budgetParam("250.5")), want: defaultMaxTrialBudget},
		{name: "exponent", rec: hpoRecord(budgetParam("1e+06")), want: defaultMaxTrialBudget},
		{name: "surrounding whitespace", rec: hpoRecord(budgetParam(" 250 ")), want: defaultMaxTrialBudget},
		{name: "multibyte digits", rec: hpoRecord(budgetParam("２５０")), want: defaultMaxTrialBudget},
		{name: "zero", rec: hpoRecord(budgetParam("0")), want: defaultMaxTrialBudget},
		{name: "negative", rec: hpoRecord(budgetParam("-5")), want: defaultMaxTrialBudget},
		{name: "int32 overflow", rec: hpoRecord(budgetParam("2147483648")), want: defaultMaxTrialBudget},
		{name: "int64 overflow", rec: hpoRecord(budgetParam("99999999999999999999")), want: defaultMaxTrialBudget},
		{name: "long control characters", rec: hpoRecord(budgetParam(strings.Repeat("\n\x00", 100))), want: defaultMaxTrialBudget},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := studyTrialBudgetLimit(context.Background(), tc.rec); got != tc.want {
				t.Errorf("studyTrialBudgetLimit() = %d, want %d", got, tc.want)
			}
		})
	}
}

func TestTruncateForLog(t *testing.T) {
	long := strings.Repeat("a", maxLoggedParameterLen+10)
	// "é" is two bytes; the cut at maxLoggedParameterLen falls inside the last full one.
	multibyte := strings.Repeat("a", maxLoggedParameterLen-1) + "éé"
	tests := []struct {
		name  string
		value string
		want  string
	}{
		{name: "short", value: "250", want: `"250"`},
		{name: "control characters are escaped", value: "a\nb", want: `"a\nb"`},
		{name: "long value is cut", value: long, want: `"` + long[:maxLoggedParameterLen] + `"`},
		{name: "cut keeps whole runes", value: multibyte, want: `"` + strings.Repeat("a", maxLoggedParameterLen-1) + `"`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := truncateForLog(tc.value); got != tc.want {
				t.Errorf("truncateForLog(%q) = %s, want %s", tc.value, got, tc.want)
			}
		})
	}
}

func TestClampStudyTrialBudget(t *testing.T) {
	const clampWarning = "max_trial_budget 10 exceeds the hpo component limit; clamped to 5"
	recs := []*store.ComponentRecord{{Name: "vizier"}, hpoRecord(budgetParam("5"))}
	study := func(budget int32, status *essmsgpb.ExperimentStatus) *essmsgpb.Experiment {
		cfg := experimenttest.StudyConfig()
		cfg.MaxTrialBudget = budget
		return &essmsgpb.Experiment{ExperimentType: essmsgpb.Experiment_STUDY, TypeConfig: &essmsgpb.Experiment_StudyConfig{StudyConfig: cfg}, Status: status}
	}
	withMessage := func(severity essmsgpb.StructuredMessage_Severity, text string) *essmsgpb.ExperimentStatus {
		return &essmsgpb.ExperimentStatus{StructuredStatusMessage: &essmsgpb.StructuredMessage{Severity: severity, UserFacingText: text}}
	}
	tests := []struct {
		name       string
		exp        *essmsgpb.Experiment
		wantBudget int32
		wantMsg    *essmsgpb.StructuredMessage
	}{
		{name: "above limit is clamped with a warning", exp: study(10, &essmsgpb.ExperimentStatus{}), wantBudget: 5,
			wantMsg: &essmsgpb.StructuredMessage{Severity: essmsgpb.StructuredMessage_WARNING, Source: essmsgpb.StructuredMessage_XMANAGER_BACKEND, UserFacingText: clampWarning}},
		{name: "nil status gets the warning", exp: study(10, nil), wantBudget: 5,
			wantMsg: &essmsgpb.StructuredMessage{Severity: essmsgpb.StructuredMessage_WARNING, Source: essmsgpb.StructuredMessage_XMANAGER_BACKEND, UserFacingText: clampWarning}},
		{name: "at limit is unchanged", exp: study(5, &essmsgpb.ExperimentStatus{}), wantBudget: 5},
		{name: "below limit is unchanged", exp: study(4, &essmsgpb.ExperimentStatus{}), wantBudget: 4},
		{name: "info message is upgraded and merged", exp: study(10, withMessage(essmsgpb.StructuredMessage_INFO, "queued")), wantBudget: 5,
			wantMsg: &essmsgpb.StructuredMessage{Severity: essmsgpb.StructuredMessage_WARNING, UserFacingText: "queued; " + clampWarning}},
		{name: "error message is kept", exp: study(10, withMessage(essmsgpb.StructuredMessage_ERROR, "failed")), wantBudget: 5,
			wantMsg: &essmsgpb.StructuredMessage{Severity: essmsgpb.StructuredMessage_ERROR, UserFacingText: "failed"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			clampStudyTrialBudget(context.Background(), tc.exp, recs)

			if got := tc.exp.GetStudyConfig().GetMaxTrialBudget(); got != tc.wantBudget {
				t.Errorf("max_trial_budget = %d, want %d", got, tc.wantBudget)
			}
			if diff := cmp.Diff(tc.wantMsg, tc.exp.GetStatus().GetStructuredStatusMessage(), protocmp.Transform(), protocmp.IgnoreFields(&essmsgpb.StructuredMessage{}, "create_time")); diff != "" {
				t.Errorf("status message diff (-want +got):\n%s", diff)
			}
		})
	}
}

func TestClampStudyTrialBudget_SkipsNonStudies(t *testing.T) {
	recs := []*store.ComponentRecord{hpoRecord(budgetParam("5"))}
	tests := []struct {
		name string
		exp  *essmsgpb.Experiment
	}{
		{name: "standard with a study config", exp: &essmsgpb.Experiment{
			ExperimentType: essmsgpb.Experiment_STANDARD, TypeConfig: &essmsgpb.Experiment_StudyConfig{StudyConfig: experimenttest.StudyConfig()}, Status: &essmsgpb.ExperimentStatus{}}},
		{name: "study without a study config", exp: &essmsgpb.Experiment{ExperimentType: essmsgpb.Experiment_STUDY, Status: &essmsgpb.ExperimentStatus{}}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			want := proto.Clone(tc.exp)

			clampStudyTrialBudget(context.Background(), tc.exp, recs)

			if diff := cmp.Diff(want, tc.exp, protocmp.Transform()); diff != "" {
				t.Errorf("clampStudyTrialBudget() changed the experiment (-want +got):\n%s", diff)
			}
		})
	}
}
