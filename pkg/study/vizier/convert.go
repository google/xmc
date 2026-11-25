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

package vizier

import (
	"fmt"
	"math"
	"slices"

	spb "xmanager_cloud/api/protos/experiment"
	"xmanager_cloud/pkg/study"
	"xmanager_cloud/pkg/study/vizierpb"
)

func invalidParamErr(idx int, msg string) error {
	return fmt.Errorf("%w: parameter[%d] %s", study.ErrInvalidStudyConfig, idx, msg)
}

// ToStudySpec converts an XMC StudyConfig to an OSS Vizier StudySpec without mutating cfg.
// All validation errors wrap study.ErrInvalidStudyConfig and omit user-supplied payload strings.
func ToStudySpec(cfg *spb.StudyConfig) (*vizierpb.StudySpec, error) {
	if cfg == nil || cfg.GetAlgorithm() == "" || len(cfg.GetParameters()) == 0 {
		return nil, fmt.Errorf("%w: study config requires non-empty algorithm and parameters", study.ErrInvalidStudyConfig)
	}
	metrics, err := convertMetrics(cfg.GetMetrics())
	if err != nil {
		return nil, err
	}
	seenParams := make(map[string]struct{}, len(cfg.GetParameters()))
	params := make([]*vizierpb.StudySpec_ParameterSpec, 0, len(cfg.GetParameters()))
	for i, p := range cfg.GetParameters() {
		converted, err := convertParameter(i, p, seenParams)
		if err != nil {
			return nil, err
		}
		params = append(params, converted)
	}
	return &vizierpb.StudySpec{Parameters: params, Metrics: metrics, Algorithm: cfg.GetAlgorithm()}, nil
}

func convertMetrics(metrics []*spb.MetricSpec) ([]*vizierpb.StudySpec_MetricSpec, error) {
	if len(metrics) != 1 || metrics[0] == nil || metrics[0].GetMetricId() == "" {
		return nil, fmt.Errorf("%w: expected 1 metric with non-empty metric_id", study.ErrInvalidStudyConfig)
	}
	var goal vizierpb.StudySpec_MetricSpec_GoalType
	switch metrics[0].GetGoal() {
	case spb.MetricSpec_MAXIMIZE:
		goal = vizierpb.StudySpec_MetricSpec_MAXIMIZE
	case spb.MetricSpec_MINIMIZE:
		goal = vizierpb.StudySpec_MetricSpec_MINIMIZE
	default:
		return nil, fmt.Errorf("%w: metric[0] unsupported goal %d", study.ErrInvalidStudyConfig, int32(metrics[0].GetGoal()))
	}
	return []*vizierpb.StudySpec_MetricSpec{{MetricId: metrics[0].GetMetricId(), Goal: goal}}, nil
}

func convertParameter(idx int, p *spb.ParameterSpec, seen map[string]struct{}) (*vizierpb.StudySpec_ParameterSpec, error) {
	if p == nil || p.GetParameterId() == "" {
		return nil, invalidParamErr(idx, "must have a non-empty parameter_id")
	}
	if _, dup := seen[p.GetParameterId()]; dup {
		return nil, invalidParamErr(idx, "duplicate parameter_id")
	}
	seen[p.GetParameterId()] = struct{}{}
	scale, err := convertScaleType(idx, p.GetScaleType())
	if err != nil {
		return nil, err
	}
	out := &vizierpb.StudySpec_ParameterSpec{ParameterId: p.GetParameterId(), ScaleType: scale}
	isLog := scale == vizierpb.StudySpec_ParameterSpec_UNIT_LOG_SCALE || scale == vizierpb.StudySpec_ParameterSpec_UNIT_REVERSE_LOG_SCALE
	switch v := p.GetParameterValueSpec().(type) {
	case *spb.ParameterSpec_DoubleValueSpec:
		s := v.DoubleValueSpec
		if s == nil || !isFinite(s.GetMinValue()) || !isFinite(s.GetMaxValue()) || s.GetMinValue() > s.GetMaxValue() || (isLog && s.GetMinValue() <= 0) {
			return nil, invalidParamErr(idx, "invalid double bounds")
		}
		out.ParameterValueSpec = &vizierpb.StudySpec_ParameterSpec_DoubleValueSpec_{DoubleValueSpec: &vizierpb.StudySpec_ParameterSpec_DoubleValueSpec{MinValue: s.GetMinValue(), MaxValue: s.GetMaxValue()}}
	case *spb.ParameterSpec_IntegerValueSpec:
		s := v.IntegerValueSpec
		if s == nil || s.GetMinValue() > s.GetMaxValue() || (isLog && s.GetMinValue() <= 0) {
			return nil, invalidParamErr(idx, "invalid integer bounds")
		}
		out.ParameterValueSpec = &vizierpb.StudySpec_ParameterSpec_IntegerValueSpec_{IntegerValueSpec: &vizierpb.StudySpec_ParameterSpec_IntegerValueSpec{MinValue: s.GetMinValue(), MaxValue: s.GetMaxValue()}}
	case *spb.ParameterSpec_CategoricalValueSpec:
		spec, err := convertCategoricalSpec(idx, v.CategoricalValueSpec, scale)
		if err != nil {
			return nil, err
		}
		out.ParameterValueSpec = &vizierpb.StudySpec_ParameterSpec_CategoricalValueSpec_{CategoricalValueSpec: spec}
	case *spb.ParameterSpec_DiscreteValueSpec:
		spec, err := convertDiscreteSpec(idx, v.DiscreteValueSpec, isLog)
		if err != nil {
			return nil, err
		}
		out.ParameterValueSpec = &vizierpb.StudySpec_ParameterSpec_DiscreteValueSpec_{DiscreteValueSpec: spec}
	default:
		return nil, invalidParamErr(idx, "missing parameter_value_spec")
	}
	return out, nil
}

func convertScaleType(idx int, s spb.ParameterSpec_ScaleType) (vizierpb.StudySpec_ParameterSpec_ScaleType, error) {
	switch s {
	case spb.ParameterSpec_SCALE_TYPE_UNSPECIFIED:
		return vizierpb.StudySpec_ParameterSpec_SCALE_TYPE_UNSPECIFIED, nil
	case spb.ParameterSpec_LINEAR:
		return vizierpb.StudySpec_ParameterSpec_UNIT_LINEAR_SCALE, nil
	case spb.ParameterSpec_LOG:
		return vizierpb.StudySpec_ParameterSpec_UNIT_LOG_SCALE, nil
	case spb.ParameterSpec_REVERSE_LOG:
		return vizierpb.StudySpec_ParameterSpec_UNIT_REVERSE_LOG_SCALE, nil
	default:
		return 0, fmt.Errorf("%w: parameter[%d] unsupported scale_type %d", study.ErrInvalidStudyConfig, idx, int32(s))
	}
}

func convertCategoricalSpec(idx int, s *spb.CategoricalValueSpec, scale vizierpb.StudySpec_ParameterSpec_ScaleType) (*vizierpb.StudySpec_ParameterSpec_CategoricalValueSpec, error) {
	if s == nil || len(s.GetValues()) == 0 {
		return nil, invalidParamErr(idx, "categorical values must not be empty")
	}
	if scale != vizierpb.StudySpec_ParameterSpec_SCALE_TYPE_UNSPECIFIED {
		return nil, invalidParamErr(idx, "categorical parameter must not set scale_type")
	}
	seen := make(map[string]struct{}, len(s.GetValues()))
	for _, v := range s.GetValues() {
		if v == "" {
			return nil, invalidParamErr(idx, "categorical values must not contain empty string")
		}
		if _, dup := seen[v]; dup {
			return nil, invalidParamErr(idx, "categorical values must be unique")
		}
		seen[v] = struct{}{}
	}
	return &vizierpb.StudySpec_ParameterSpec_CategoricalValueSpec{Values: slices.Clone(s.GetValues())}, nil
}

func convertDiscreteSpec(idx int, s *spb.DiscreteValueSpec, isLog bool) (*vizierpb.StudySpec_ParameterSpec_DiscreteValueSpec, error) {
	if s == nil || len(s.GetValues()) == 0 {
		return nil, invalidParamErr(idx, "discrete values must not be empty")
	}
	for _, v := range s.GetValues() {
		if !isFinite(v) {
			return nil, invalidParamErr(idx, "discrete values must be finite")
		}
	}
	sorted := slices.Clone(s.GetValues())
	slices.Sort(sorted)
	for i := 1; i < len(sorted); i++ {
		if sorted[i] == sorted[i-1] {
			return nil, invalidParamErr(idx, "discrete values must be unique")
		}
	}
	if isLog && sorted[0] <= 0 {
		return nil, invalidParamErr(idx, "log-scaled discrete values must be positive")
	}
	return &vizierpb.StudySpec_ParameterSpec_DiscreteValueSpec{Values: sorted}, nil
}

func isFinite(v float64) bool { return !math.IsNaN(v) && !math.IsInf(v, 0) }
