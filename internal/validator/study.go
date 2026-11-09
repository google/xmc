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
	"fmt"
	"math"
	"regexp"
	"slices"
	"strings"
	"unicode"

	essmsgpb "xmanager_cloud/api/protos/experiment"
	"xmanager_cloud/internal/grpcerr"
)

const (
	// maxStudyParameterIDLen is the maximum length of a study parameter ID.
	maxStudyParameterIDLen = 128
	// maxStudyMetricIDLen is the maximum length of a study metric ID, in bytes.
	maxStudyMetricIDLen = 256
	// maxStudyParameters is the maximum number of parameters in a study.
	maxStudyParameters = 100
	// maxStudyFeasibleValues is the maximum number of values of a categorical or discrete parameter.
	maxStudyFeasibleValues = 1000
)

var (
	// supportedStudyAlgorithms lists the search algorithms that studies may use, named as in the
	// OSS Vizier StudySpec.
	supportedStudyAlgorithms = []string{"RANDOM_SEARCH", "QUASI_RANDOM_SEARCH", "GRID_SEARCH"}
	// studyParameterIDRegex matches parameter IDs that are safe to pass to trials as command-line
	// flag names.
	studyParameterIDRegex = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_.-]*$`)
)

// validateExperimentStudyConfig requires a study_config on STUDY experiments, rejects it on any
// other experiment type (including UNSPECIFIED, which is stored as STANDARD), and validates it.
func validateExperimentStudyConfig(exp *essmsgpb.Experiment) error {
	cfg := exp.GetStudyConfig()
	if exp.GetExperimentType() != essmsgpb.Experiment_STUDY {
		if cfg != nil {
			return fmt.Errorf("experiment.study_config must be unset unless experiment.experiment_type is STUDY: %w", grpcerr.ErrInvalidArgument)
		}
		return nil
	}
	if cfg == nil {
		return fmt.Errorf("experiment.study_config is required when experiment.experiment_type is STUDY: %w", grpcerr.ErrInvalidArgument)
	}
	if err := validateStudyConfig(cfg); err != nil {
		return fmt.Errorf("experiment.study_config: %w", err)
	}
	return nil
}

// validateStudyConfig validates the search space, objective, algorithm, and limits of a study.
func validateStudyConfig(cfg *essmsgpb.StudyConfig) error {
	if err := validateStudyParameters(cfg.GetParameters()); err != nil {
		return err
	}
	if err := validateStudyMetrics(cfg.GetMetrics()); err != nil {
		return err
	}
	if !slices.Contains(supportedStudyAlgorithms, cfg.GetAlgorithm()) {
		return fmt.Errorf("algorithm %q is not supported, use one of %s: %w", cfg.GetAlgorithm(), strings.Join(supportedStudyAlgorithms, ", "), grpcerr.ErrInvalidArgument)
	}
	if cfg.GetMaxTrialBudget() <= 0 {
		return fmt.Errorf("max_trial_budget must be positive, got %d: %w", cfg.GetMaxTrialBudget(), grpcerr.ErrInvalidArgument)
	}
	if cfg.GetMaxParallelTrials() <= 0 {
		return fmt.Errorf("max_parallel_trials must be positive, got %d: %w", cfg.GetMaxParallelTrials(), grpcerr.ErrInvalidArgument)
	}
	return nil
}

// validateStudyMetrics requires exactly one metric, because studies support a single objective.
func validateStudyMetrics(metrics []*essmsgpb.MetricSpec) error {
	if len(metrics) != 1 {
		return fmt.Errorf("metrics must contain exactly one metric (single objective), got %d: %w", len(metrics), grpcerr.ErrInvalidArgument)
	}
	metric := metrics[0]
	if err := validateMetricID(metric.GetMetricId()); err != nil {
		return fmt.Errorf("metrics[0].%w", err)
	}
	switch metric.GetGoal() {
	case essmsgpb.MetricSpec_MAXIMIZE, essmsgpb.MetricSpec_MINIMIZE:
		return nil
	default:
		return fmt.Errorf("metrics[0].goal must be MAXIMIZE or MINIMIZE, got %v: %w", metric.GetGoal(), grpcerr.ErrInvalidArgument)
	}
}

// validateMetricID requires a non-blank ID of at most maxStudyMetricIDLen bytes, without surrounding
// whitespace or non-printable runes.
func validateMetricID(id string) error {
	if id == "" || len(id) > maxStudyMetricIDLen || strings.TrimSpace(id) != id {
		return fmt.Errorf("metric_id %q must be set, at most %d bytes long, and have no surrounding whitespace: %w", id, maxStudyMetricIDLen, grpcerr.ErrInvalidArgument)
	}
	if strings.ContainsFunc(id, func(r rune) bool { return !unicode.IsPrint(r) }) {
		return fmt.Errorf("metric_id %q cannot contain non-printable characters: %w", id, grpcerr.ErrInvalidArgument)
	}
	return nil
}

// validateStudyParameters requires between one and maxStudyParameters parameters, each valid and
// with a unique ID.
func validateStudyParameters(params []*essmsgpb.ParameterSpec) error {
	if len(params) == 0 || len(params) > maxStudyParameters {
		return fmt.Errorf("parameters must contain between 1 and %d parameters, got %d: %w", maxStudyParameters, len(params), grpcerr.ErrInvalidArgument)
	}
	ids := make(map[string]struct{}, len(params))
	for i, param := range params {
		if err := validateParameterSpec(param); err != nil {
			return fmt.Errorf("parameters[%d]: %w", i, err)
		}
		if _, ok := ids[param.GetParameterId()]; ok {
			return fmt.Errorf("parameters[%d].parameter_id %q is not unique: %w", i, param.GetParameterId(), grpcerr.ErrInvalidArgument)
		}
		ids[param.GetParameterId()] = struct{}{}
	}
	return nil
}

// validateParameterSpec validates the ID, the feasible values, and the scale of a parameter.
func validateParameterSpec(param *essmsgpb.ParameterSpec) error {
	id := param.GetParameterId()
	if len(id) > maxStudyParameterIDLen || !studyParameterIDRegex.MatchString(id) {
		return fmt.Errorf("parameter_id %q must match %s and be at most %d characters long: %w", id, studyParameterIDRegex, maxStudyParameterIDLen, grpcerr.ErrInvalidArgument)
	}
	scale := param.GetScaleType()
	if !isKnownEnumValue(scale) {
		return fmt.Errorf("scale_type %d is not a known scale: %w", scale, grpcerr.ErrInvalidArgument)
	}
	switch spec := param.GetParameterValueSpec().(type) {
	case *essmsgpb.ParameterSpec_DoubleValueSpec:
		return validateDoubleValueSpec(spec.DoubleValueSpec, scale)
	case *essmsgpb.ParameterSpec_IntegerValueSpec:
		return validateIntegerValueSpec(spec.IntegerValueSpec, scale)
	case *essmsgpb.ParameterSpec_CategoricalValueSpec:
		return validateCategoricalValueSpec(spec.CategoricalValueSpec, scale)
	case *essmsgpb.ParameterSpec_DiscreteValueSpec:
		return validateDiscreteValueSpec(spec.DiscreteValueSpec, scale)
	default:
		return fmt.Errorf("one of double_value_spec, integer_value_spec, categorical_value_spec or discrete_value_spec must be set: %w", grpcerr.ErrInvalidArgument)
	}
}

// validateDoubleValueSpec requires finite bounds with min_value <= max_value; equal bounds fix the
// parameter.
func validateDoubleValueSpec(spec *essmsgpb.DoubleValueSpec, scale essmsgpb.ParameterSpec_ScaleType) error {
	minValue, maxValue := spec.GetMinValue(), spec.GetMaxValue()
	if !isFinite(minValue) || !isFinite(maxValue) || minValue > maxValue {
		return fmt.Errorf("double_value_spec bounds must be finite with min_value <= max_value, got [%v, %v]: %w", minValue, maxValue, grpcerr.ErrInvalidArgument)
	}
	return validateLogScaleMin(scale, minValue)
}

// validateIntegerValueSpec requires min_value <= max_value; equal bounds fix the parameter.
func validateIntegerValueSpec(spec *essmsgpb.IntegerValueSpec, scale essmsgpb.ParameterSpec_ScaleType) error {
	if spec.GetMinValue() > spec.GetMaxValue() {
		return fmt.Errorf("integer_value_spec bounds must have min_value <= max_value, got [%d, %d]: %w", spec.GetMinValue(), spec.GetMaxValue(), grpcerr.ErrInvalidArgument)
	}
	return validateLogScaleMin(scale, float64(spec.GetMinValue()))
}

func validateCategoricalValueSpec(spec *essmsgpb.CategoricalValueSpec, scale essmsgpb.ParameterSpec_ScaleType) error {
	if scale != essmsgpb.ParameterSpec_SCALE_TYPE_UNSPECIFIED {
		return fmt.Errorf("scale_type must be unset for a categorical parameter, got %v: %w", scale, grpcerr.ErrInvalidArgument)
	}
	values := spec.GetValues()
	if err := validateFeasibleValueCount("categorical_value_spec", len(values)); err != nil {
		return err
	}
	if slices.Contains(values, "") {
		return fmt.Errorf("categorical_value_spec.values cannot contain an empty value: %w", grpcerr.ErrInvalidArgument)
	}
	if hasDuplicates(values) {
		return fmt.Errorf("categorical_value_spec.values must be unique: %w", grpcerr.ErrInvalidArgument)
	}
	return nil
}

func validateDiscreteValueSpec(spec *essmsgpb.DiscreteValueSpec, scale essmsgpb.ParameterSpec_ScaleType) error {
	values := spec.GetValues()
	if err := validateFeasibleValueCount("discrete_value_spec", len(values)); err != nil {
		return err
	}
	if slices.ContainsFunc(values, func(v float64) bool { return !isFinite(v) }) {
		return fmt.Errorf("discrete_value_spec.values must be finite: %w", grpcerr.ErrInvalidArgument)
	}
	if hasDuplicates(values) {
		return fmt.Errorf("discrete_value_spec.values must be unique: %w", grpcerr.ErrInvalidArgument)
	}
	return validateLogScaleMin(scale, slices.Min(values))
}

// validateFeasibleValueCount requires between one and maxStudyFeasibleValues values.
func validateFeasibleValueCount(spec string, count int) error {
	if count == 0 || count > maxStudyFeasibleValues {
		return fmt.Errorf("%s.values must contain between 1 and %d values, got %d: %w", spec, maxStudyFeasibleValues, count, grpcerr.ErrInvalidArgument)
	}
	return nil
}

// validateLogScaleMin rejects a logarithmic scale unless every feasible value, and therefore the
// smallest one, is positive.
func validateLogScaleMin(scale essmsgpb.ParameterSpec_ScaleType, minValue float64) error {
	isLog := scale == essmsgpb.ParameterSpec_LOG || scale == essmsgpb.ParameterSpec_REVERSE_LOG
	if isLog && minValue <= 0 {
		return fmt.Errorf("scale_type %v requires positive values, got minimum %v: %w", scale, minValue, grpcerr.ErrInvalidArgument)
	}
	return nil
}

func isFinite(v float64) bool {
	return !math.IsNaN(v) && !math.IsInf(v, 0)
}

func hasDuplicates[T comparable](values []T) bool {
	seen := make(map[T]struct{}, len(values))
	for _, v := range values {
		if _, ok := seen[v]; ok {
			return true
		}
		seen[v] = struct{}{}
	}
	return false
}
