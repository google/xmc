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

package validator

import (
	"errors"
	"fmt"
	"math"
	"strings"
	"testing"

	essmsgpb "xmanager_cloud/api/protos/experiment"
	xidmessagespb "xmanager_cloud/api/protos/xid"
	"xmanager_cloud/internal/experiment/experimenttest"
	"xmanager_cloud/internal/grpcerr"
	"xmanager_cloud/internal/prototest"
)

// studyConfigCase builds a STUDY CreateExperimentRequest whose valid study config is mutated by fn.
func studyConfigCase(fn func(cfg *essmsgpb.StudyConfig)) *essmsgpb.CreateExperimentRequest {
	cfg := prototest.Modify(experimenttest.StudyConfig(), fn)
	return &essmsgpb.CreateExperimentRequest{
		Experiment: &essmsgpb.Experiment{
			Title:          "study",
			Acls:           &xidmessagespb.XidAcls{},
			ExperimentType: essmsgpb.Experiment_STUDY,
			TypeConfig:     &essmsgpb.Experiment_StudyConfig{StudyConfig: cfg},
		},
	}
}

// parameterCase builds a STUDY CreateExperimentRequest whose study has the single parameter param.
func parameterCase(param *essmsgpb.ParameterSpec) *essmsgpb.CreateExperimentRequest {
	return studyConfigCase(func(cfg *essmsgpb.StudyConfig) {
		cfg.Parameters = []*essmsgpb.ParameterSpec{param}
	})
}

func doubleParam(minValue, maxValue float64, scale essmsgpb.ParameterSpec_ScaleType) *essmsgpb.ParameterSpec {
	return &essmsgpb.ParameterSpec{
		ParameterId:        "x",
		ParameterValueSpec: &essmsgpb.ParameterSpec_DoubleValueSpec{DoubleValueSpec: &essmsgpb.DoubleValueSpec{MinValue: minValue, MaxValue: maxValue}},
		ScaleType:          scale,
	}
}

func integerParam(minValue, maxValue int64, scale essmsgpb.ParameterSpec_ScaleType) *essmsgpb.ParameterSpec {
	return &essmsgpb.ParameterSpec{
		ParameterId:        "x",
		ParameterValueSpec: &essmsgpb.ParameterSpec_IntegerValueSpec{IntegerValueSpec: &essmsgpb.IntegerValueSpec{MinValue: minValue, MaxValue: maxValue}},
		ScaleType:          scale,
	}
}

func categoricalParam(scale essmsgpb.ParameterSpec_ScaleType, values ...string) *essmsgpb.ParameterSpec {
	return &essmsgpb.ParameterSpec{
		ParameterId:        "x",
		ParameterValueSpec: &essmsgpb.ParameterSpec_CategoricalValueSpec{CategoricalValueSpec: &essmsgpb.CategoricalValueSpec{Values: values}},
		ScaleType:          scale,
	}
}

func discreteParam(scale essmsgpb.ParameterSpec_ScaleType, values ...float64) *essmsgpb.ParameterSpec {
	return &essmsgpb.ParameterSpec{
		ParameterId:        "x",
		ParameterValueSpec: &essmsgpb.ParameterSpec_DiscreteValueSpec{DiscreteValueSpec: &essmsgpb.DiscreteValueSpec{Values: values}},
		ScaleType:          scale,
	}
}

func namedParam(id string) *essmsgpb.ParameterSpec {
	return prototest.Modify(doubleParam(0, 1, essmsgpb.ParameterSpec_SCALE_TYPE_UNSPECIFIED), func(p *essmsgpb.ParameterSpec) {
		p.ParameterId = id
	})
}

func uniqueParams(n int) []*essmsgpb.ParameterSpec {
	params := make([]*essmsgpb.ParameterSpec, n)
	for i := range params {
		params[i] = namedParam(fmt.Sprintf("p%d", i))
	}
	return params
}

func uniqueStrings(n int) []string {
	values := make([]string, n)
	for i := range values {
		values[i] = fmt.Sprintf("v%d", i)
	}
	return values
}

func uniqueFloats(n int) []float64 {
	values := make([]float64, n)
	for i := range values {
		values[i] = float64(i)
	}
	return values
}

func TestValidateCreateExperimentRequest_StudyConfig(t *testing.T) {
	const (
		unspecified = essmsgpb.ParameterSpec_SCALE_TYPE_UNSPECIFIED
		linear      = essmsgpb.ParameterSpec_LINEAR
		logScale    = essmsgpb.ParameterSpec_LOG
		reverseLog  = essmsgpb.ParameterSpec_REVERSE_LOG
	)
	tests := []struct {
		name    string
		req     *essmsgpb.CreateExperimentRequest
		wantErr error
	}{
		{name: "valid", req: studyConfigCase(nil)},
		{name: "quasi_random_search", req: studyConfigCase(func(cfg *essmsgpb.StudyConfig) { cfg.Algorithm = "QUASI_RANDOM_SEARCH" })},
		{name: "grid_search", req: studyConfigCase(func(cfg *essmsgpb.StudyConfig) { cfg.Algorithm = "GRID_SEARCH" })},
		{
			name: "study_without_config",
			req: prototest.Modify(studyConfigCase(nil), func(r *essmsgpb.CreateExperimentRequest) {
				r.Experiment.TypeConfig = nil
			}),
			wantErr: grpcerr.ErrInvalidArgument,
		},
		{
			// Non-STUDY experiments with a study_config are covered by TestValidateCreateExperimentRequest.
			name: "standard_without_config",
			req: prototest.Modify(studyConfigCase(nil), func(r *essmsgpb.CreateExperimentRequest) {
				r.Experiment.ExperimentType = essmsgpb.Experiment_STANDARD
				r.Experiment.TypeConfig = nil
			}),
		},
		{name: "algorithm_empty", req: studyConfigCase(func(cfg *essmsgpb.StudyConfig) { cfg.Algorithm = "" }), wantErr: grpcerr.ErrInvalidArgument},
		{name: "algorithm_default", req: studyConfigCase(func(cfg *essmsgpb.StudyConfig) { cfg.Algorithm = "DEFAULT" }), wantErr: grpcerr.ErrInvalidArgument},
		{name: "algorithm_lowercase", req: studyConfigCase(func(cfg *essmsgpb.StudyConfig) { cfg.Algorithm = "random_search" }), wantErr: grpcerr.ErrInvalidArgument},
		{name: "algorithm_padded", req: studyConfigCase(func(cfg *essmsgpb.StudyConfig) { cfg.Algorithm = " RANDOM_SEARCH" }), wantErr: grpcerr.ErrInvalidArgument},
		{name: "budget_zero", req: studyConfigCase(func(cfg *essmsgpb.StudyConfig) { cfg.MaxTrialBudget = 0 }), wantErr: grpcerr.ErrInvalidArgument},
		{name: "budget_negative", req: studyConfigCase(func(cfg *essmsgpb.StudyConfig) { cfg.MaxTrialBudget = -1 }), wantErr: grpcerr.ErrInvalidArgument},
		{name: "parallel_trials_zero", req: studyConfigCase(func(cfg *essmsgpb.StudyConfig) { cfg.MaxParallelTrials = 0 }), wantErr: grpcerr.ErrInvalidArgument},
		{name: "parallel_trials_negative", req: studyConfigCase(func(cfg *essmsgpb.StudyConfig) { cfg.MaxParallelTrials = -1 }), wantErr: grpcerr.ErrInvalidArgument},
		{
			// Effective parallelism never exceeds the remaining budget, so a larger value is accepted.
			name: "parallel_trials_above_budget",
			req:  studyConfigCase(func(cfg *essmsgpb.StudyConfig) { cfg.MaxParallelTrials = cfg.GetMaxTrialBudget() + 1 }),
		},
		{name: "metrics_empty", req: studyConfigCase(func(cfg *essmsgpb.StudyConfig) { cfg.Metrics = nil }), wantErr: grpcerr.ErrInvalidArgument},
		{
			name: "metrics_multi_objective",
			req: studyConfigCase(func(cfg *essmsgpb.StudyConfig) {
				cfg.Metrics = append(cfg.Metrics, &essmsgpb.MetricSpec{MetricId: "loss", Goal: essmsgpb.MetricSpec_MINIMIZE})
			}),
			wantErr: grpcerr.ErrInvalidArgument,
		},
		{name: "metric_id_empty", req: studyConfigCase(func(cfg *essmsgpb.StudyConfig) { cfg.Metrics[0].MetricId = "" }), wantErr: grpcerr.ErrInvalidArgument},
		{name: "metric_id_whitespace", req: studyConfigCase(func(cfg *essmsgpb.StudyConfig) { cfg.Metrics[0].MetricId = " \t" }), wantErr: grpcerr.ErrInvalidArgument},
		{
			name:    "metric_id_too_long",
			req:     studyConfigCase(func(cfg *essmsgpb.StudyConfig) { cfg.Metrics[0].MetricId = strings.Repeat("a", maxStudyMetricIDLen+1) }),
			wantErr: grpcerr.ErrInvalidArgument,
		},
		{name: "metric_id_max_length", req: studyConfigCase(func(cfg *essmsgpb.StudyConfig) { cfg.Metrics[0].MetricId = strings.Repeat("a", maxStudyMetricIDLen) })},
		{name: "metric_id_multibyte", req: studyConfigCase(func(cfg *essmsgpb.StudyConfig) { cfg.Metrics[0].MetricId = "eval/精度" })},
		{name: "metric_id_inner_space", req: studyConfigCase(func(cfg *essmsgpb.StudyConfig) { cfg.Metrics[0].MetricId = "eval accuracy" })},
		{name: "metric_id_leading_space", req: studyConfigCase(func(cfg *essmsgpb.StudyConfig) { cfg.Metrics[0].MetricId = " accuracy" }), wantErr: grpcerr.ErrInvalidArgument},
		{name: "metric_id_trailing_newline", req: studyConfigCase(func(cfg *essmsgpb.StudyConfig) { cfg.Metrics[0].MetricId = "accuracy\n" }), wantErr: grpcerr.ErrInvalidArgument},
		{name: "metric_id_control_rune", req: studyConfigCase(func(cfg *essmsgpb.StudyConfig) { cfg.Metrics[0].MetricId = "acc\x00uracy" }), wantErr: grpcerr.ErrInvalidArgument},
		{name: "metric_id_zero_width_space", req: studyConfigCase(func(cfg *essmsgpb.StudyConfig) { cfg.Metrics[0].MetricId = "acc\u200buracy" }), wantErr: grpcerr.ErrInvalidArgument},
		{name: "metric_goal_minimize", req: studyConfigCase(func(cfg *essmsgpb.StudyConfig) { cfg.Metrics[0].Goal = essmsgpb.MetricSpec_MINIMIZE })},
		{
			name:    "metric_goal_unspecified",
			req:     studyConfigCase(func(cfg *essmsgpb.StudyConfig) { cfg.Metrics[0].Goal = essmsgpb.MetricSpec_GOAL_TYPE_UNSPECIFIED }),
			wantErr: grpcerr.ErrInvalidArgument,
		},
		{
			name:    "metric_goal_unknown",
			req:     studyConfigCase(func(cfg *essmsgpb.StudyConfig) { cfg.Metrics[0].Goal = essmsgpb.MetricSpec_GoalType(99) }),
			wantErr: grpcerr.ErrInvalidArgument,
		},
		{name: "parameters_empty", req: studyConfigCase(func(cfg *essmsgpb.StudyConfig) { cfg.Parameters = nil }), wantErr: grpcerr.ErrInvalidArgument},
		{name: "parameters_at_cap", req: studyConfigCase(func(cfg *essmsgpb.StudyConfig) { cfg.Parameters = uniqueParams(maxStudyParameters) })},
		{
			name:    "parameters_over_cap",
			req:     studyConfigCase(func(cfg *essmsgpb.StudyConfig) { cfg.Parameters = uniqueParams(maxStudyParameters + 1) }),
			wantErr: grpcerr.ErrInvalidArgument,
		},
		{name: "categorical_at_cap", req: parameterCase(categoricalParam(unspecified, uniqueStrings(maxStudyFeasibleValues)...))},
		{name: "categorical_over_cap", req: parameterCase(categoricalParam(unspecified, uniqueStrings(maxStudyFeasibleValues+1)...)), wantErr: grpcerr.ErrInvalidArgument},
		{name: "discrete_at_cap", req: parameterCase(discreteParam(unspecified, uniqueFloats(maxStudyFeasibleValues)...))},
		{name: "discrete_over_cap", req: parameterCase(discreteParam(unspecified, uniqueFloats(maxStudyFeasibleValues+1)...)), wantErr: grpcerr.ErrInvalidArgument},
		{
			name: "parameter_id_duplicate",
			req: studyConfigCase(func(cfg *essmsgpb.StudyConfig) {
				cfg.Parameters[1].ParameterId = cfg.Parameters[0].GetParameterId()
			}),
			wantErr: grpcerr.ErrInvalidArgument,
		},
		{name: "parameter_id_flag_safe", req: parameterCase(namedParam("_model.learning-rate_2"))},
		{name: "parameter_id_max_length", req: parameterCase(namedParam(strings.Repeat("a", maxStudyParameterIDLen)))},
		{name: "parameter_id_too_long", req: parameterCase(namedParam(strings.Repeat("a", maxStudyParameterIDLen+1))), wantErr: grpcerr.ErrInvalidArgument},
		{name: "parameter_id_empty", req: parameterCase(namedParam("")), wantErr: grpcerr.ErrInvalidArgument},
		{name: "parameter_id_multibyte", req: parameterCase(namedParam("学習率")), wantErr: grpcerr.ErrInvalidArgument},
		{name: "parameter_id_space", req: parameterCase(namedParam("learning rate")), wantErr: grpcerr.ErrInvalidArgument},
		{name: "parameter_id_flag_prefix", req: parameterCase(namedParam("--lr")), wantErr: grpcerr.ErrInvalidArgument},
		{name: "parameter_id_equals", req: parameterCase(namedParam("lr=1")), wantErr: grpcerr.ErrInvalidArgument},
		{name: "parameter_id_leading_digit", req: parameterCase(namedParam("1lr")), wantErr: grpcerr.ErrInvalidArgument},
		{
			name:    "parameter_value_spec_unset",
			req:     parameterCase(&essmsgpb.ParameterSpec{ParameterId: "x"}),
			wantErr: grpcerr.ErrInvalidArgument,
		},
		{
			name:    "parameter_scale_unknown",
			req:     parameterCase(doubleParam(0, 1, essmsgpb.ParameterSpec_ScaleType(99))),
			wantErr: grpcerr.ErrInvalidArgument,
		},
		{name: "double_negative_linear", req: parameterCase(doubleParam(-1, 1, linear))},
		{name: "double_reverse_log", req: parameterCase(doubleParam(0.5, 1, reverseLog))},
		{name: "double_fixed", req: parameterCase(doubleParam(1, 1, unspecified))},
		{name: "double_min_above_max", req: parameterCase(doubleParam(2, 1, unspecified)), wantErr: grpcerr.ErrInvalidArgument},
		{name: "double_nan", req: parameterCase(doubleParam(math.NaN(), 1, unspecified)), wantErr: grpcerr.ErrInvalidArgument},
		{name: "double_infinite", req: parameterCase(doubleParam(0, math.Inf(1), unspecified)), wantErr: grpcerr.ErrInvalidArgument},
		{name: "double_log_zero_min", req: parameterCase(doubleParam(0, 1, logScale)), wantErr: grpcerr.ErrInvalidArgument},
		{name: "double_reverse_log_negative_min", req: parameterCase(doubleParam(-1, 1, reverseLog)), wantErr: grpcerr.ErrInvalidArgument},
		{name: "integer_log", req: parameterCase(integerParam(1, 1024, logScale))},
		{name: "integer_fixed", req: parameterCase(integerParam(3, 3, unspecified))},
		{name: "integer_min_above_max", req: parameterCase(integerParam(4, 3, unspecified)), wantErr: grpcerr.ErrInvalidArgument},
		{name: "integer_log_zero_min", req: parameterCase(integerParam(0, 8, logScale)), wantErr: grpcerr.ErrInvalidArgument},
		{name: "categorical_multibyte", req: parameterCase(categoricalParam(unspecified, "café", "日本語"))},
		{name: "categorical_empty", req: parameterCase(categoricalParam(unspecified)), wantErr: grpcerr.ErrInvalidArgument},
		{name: "categorical_empty_value", req: parameterCase(categoricalParam(unspecified, "adam", "")), wantErr: grpcerr.ErrInvalidArgument},
		{name: "categorical_duplicate", req: parameterCase(categoricalParam(unspecified, "adam", "adam")), wantErr: grpcerr.ErrInvalidArgument},
		{name: "categorical_scale_set", req: parameterCase(categoricalParam(linear, "adam")), wantErr: grpcerr.ErrInvalidArgument},
		{name: "discrete_log", req: parameterCase(discreteParam(logScale, 0.01, 0.1, 1))},
		{name: "discrete_empty", req: parameterCase(discreteParam(unspecified)), wantErr: grpcerr.ErrInvalidArgument},
		{name: "discrete_nan", req: parameterCase(discreteParam(unspecified, 1, math.NaN())), wantErr: grpcerr.ErrInvalidArgument},
		{name: "discrete_duplicate", req: parameterCase(discreteParam(unspecified, 1, 2, 1)), wantErr: grpcerr.ErrInvalidArgument},
		{name: "discrete_log_zero_value", req: parameterCase(discreteParam(logScale, 1, 0)), wantErr: grpcerr.ErrInvalidArgument},
		{name: "metric_nil", req: studyConfigCase(func(cfg *essmsgpb.StudyConfig) { cfg.Metrics = []*essmsgpb.MetricSpec{nil} }), wantErr: grpcerr.ErrInvalidArgument},
		{name: "parameter_nil", req: studyConfigCase(func(cfg *essmsgpb.StudyConfig) { cfg.Parameters = []*essmsgpb.ParameterSpec{nil} }), wantErr: grpcerr.ErrInvalidArgument},
		// A nil inner value spec is wire-equivalent to an empty message: a nil double or integer spec
		// is the fixed range [0, 0], and a nil categorical or discrete spec has no values.
		{name: "double_spec_nil_is_fixed_zero", req: parameterCase(&essmsgpb.ParameterSpec{
			ParameterId: "x", ParameterValueSpec: &essmsgpb.ParameterSpec_DoubleValueSpec{DoubleValueSpec: nil}, ScaleType: linear})},
		{name: "integer_spec_nil_is_fixed_zero", req: parameterCase(&essmsgpb.ParameterSpec{
			ParameterId: "x", ParameterValueSpec: &essmsgpb.ParameterSpec_IntegerValueSpec{IntegerValueSpec: nil}, ScaleType: linear})},
		{name: "categorical_spec_nil", req: parameterCase(&essmsgpb.ParameterSpec{
			ParameterId: "x", ParameterValueSpec: &essmsgpb.ParameterSpec_CategoricalValueSpec{CategoricalValueSpec: nil}}), wantErr: grpcerr.ErrInvalidArgument},
		{name: "discrete_spec_nil", req: parameterCase(&essmsgpb.ParameterSpec{
			ParameterId: "x", ParameterValueSpec: &essmsgpb.ParameterSpec_DiscreteValueSpec{DiscreteValueSpec: nil}}), wantErr: grpcerr.ErrInvalidArgument},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateCreateExperimentRequest(tc.req)
			if !errors.Is(err, tc.wantErr) {
				t.Errorf("ValidateCreateExperimentRequest() = %v, want %v", err, tc.wantErr)
			}
		})
	}
}
