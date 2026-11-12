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
	"fmt"
	"strconv"
	"unicode/utf8"

	"google.golang.org/protobuf/types/known/timestamppb"
	essmsgpb "xmanager_cloud/api/protos/experiment"
	"xmanager_cloud/internal/logging"
	"xmanager_cloud/pkg/components/store"
)

const (
	// maxTrialBudgetParameter is the hpo component parameter that limits the trial budget of a study.
	maxTrialBudgetParameter = "maxTrialBudget"
	// defaultMaxTrialBudget limits the trial budget of a study when the hpo component does not set a
	// valid maxTrialBudget parameter.
	defaultMaxTrialBudget = 1000
	// maxLoggedParameterLen bounds how many bytes of an invalid parameter value are logged.
	maxLoggedParameterLen = 64
)

// clampStudyTrialBudget caps the max_trial_budget of a study exp at the limit of the hpo component
// among recs, and attaches a WARNING status message when it lowers the budget. Experiments that are
// not studies are left unchanged.
func clampStudyTrialBudget(ctx context.Context, exp *essmsgpb.Experiment, recs []*store.ComponentRecord) {
	cfg := exp.GetStudyConfig()
	if exp.GetExperimentType() != essmsgpb.Experiment_STUDY || cfg == nil {
		return
	}
	limit := studyTrialBudgetLimit(ctx, findComponentRecord(recs, hpoComponentName))
	requested := cfg.GetMaxTrialBudget()
	if requested <= limit {
		return
	}
	cfg.MaxTrialBudget = limit
	if exp.Status == nil {
		exp.Status = &essmsgpb.ExperimentStatus{}
	}
	text := fmt.Sprintf("max_trial_budget %d exceeds the %s component limit; clamped to %d", requested, hpoComponentName, limit)
	attachWarningMessage(exp.Status, &essmsgpb.StructuredMessage{
		Severity:       essmsgpb.StructuredMessage_WARNING,
		Source:         essmsgpb.StructuredMessage_XMANAGER_BACKEND,
		UserFacingText: text,
		CreateTime:     timestamppb.Now(),
	}, text)
}

// logStudyTrialBudgetClamp logs when the committed study config created holds a lower
// max_trial_budget than the requested one. It runs once after the transaction commits, rather than
// once per transaction attempt.
func logStudyTrialBudgetClamp(ctx context.Context, requested, created *essmsgpb.StudyConfig) {
	if created.GetMaxTrialBudget() < requested.GetMaxTrialBudget() {
		logging.Warn(ctx, "Clamped study trial budget to the hpo component limit",
			"requested", requested.GetMaxTrialBudget(), "limit", created.GetMaxTrialBudget())
	}
}

// studyTrialBudgetLimit returns the positive maxTrialBudget parameter of the hpo record rec, or
// defaultMaxTrialBudget when rec is nil or the parameter is missing, malformed, or not positive. The
// fallback is logged on every transaction attempt that reads the record.
func studyTrialBudgetLimit(ctx context.Context, rec *store.ComponentRecord) int32 {
	var raw string
	if rec != nil {
		raw = rec.Spec.GetParameters()[maxTrialBudgetParameter]
	}
	limit, err := strconv.ParseInt(raw, 10, 32)
	if err != nil || limit <= 0 {
		logging.Warn(ctx, "Missing or invalid hpo component parameter, using the default trial budget limit",
			"parameter", maxTrialBudgetParameter, "value", truncateForLog(raw), "default", defaultMaxTrialBudget)
		return defaultMaxTrialBudget
	}
	return int32(limit)
}

// findComponentRecord returns the record named name among recs, or nil.
func findComponentRecord(recs []*store.ComponentRecord, name string) *store.ComponentRecord {
	for _, rec := range recs {
		if rec.Name == name {
			return rec
		}
	}
	return nil
}

// truncateForLog quotes at most the first maxLoggedParameterLen bytes of value, cut on a rune
// boundary, so that a log line cannot be forged with control characters or grow unbounded.
func truncateForLog(value string) string {
	if len(value) > maxLoggedParameterLen {
		cut := maxLoggedParameterLen
		for cut > 0 && !utf8.RuneStart(value[cut]) {
			cut--
		}
		value = value[:cut]
	}
	return strconv.Quote(value)
}
