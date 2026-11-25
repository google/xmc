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

// ToStudySpec converts an XMC StudyConfig to an OSS Vizier StudySpec without mutating cfg.
// All validation errors wrap study.ErrInvalidStudyConfig and omit user-supplied payload strings.
func ToStudySpec(cfg *spb.StudyConfig) (*vizierpb.StudySpec, error) {
	if cfg == nil {
		return nil, fmt.Errorf("%w: study config must not be nil", study.ErrInvalidStudyConfig)
	}
	if cfg.GetAlgorithm() == "" {
		return nil, fmt.Errorf("%w: algorithm must not be empty", study.ErrInvalidStudyConfig)
	}
	if len(cfg.GetParameters()) == 0 {
		return nil, fmt.Errorf("%w: parameters must not be empty", study.ErrInvalidStudyConfig)
	}
	metrics, err := convertMetrics(cfg.GetMetrics())
	if err != nil {
		return nil, err
	}
	params := make([]*vizierpb.StudySpec_ParameterSpec, 0, len(cfg.GetParameters()))
	for i, p := range cfg.GetParameters() {
		converted, err := convertParameter(i, p)
		if err != nil {
			return nil, err
		}
		params = append(params, converted)
	}
	return &vizierpb.StudySpec{Parameters: params, Metrics: metrics, Algorithm: cfg.GetAlgorithm()}, nil
}

func convertMetrics(metrics []*spb.MetricSpec) ([]*vizierpb.StudySpec_MetricSpec, error) {
	if len(metrics) != 1 {
		return nil, fmt.Errorf("%w: expected 1 metric, got %d", study.ErrInvalidStudyConfig, len(metrics))
	}
	m := metrics[0]
	if m == nil || m.GetMetricId() == "" {
		return nil, fmt.Errorf("%w: metric[0] must have a non-empty metric_id", study.ErrInvalidStudyConfig)
	}
	var goal vizierpb.StudySpec_MetricSpec_GoalType
	switch m.GetGoal() {
	case spb.MetricSpec_MAXIMIZE:
		goal = vizierpb.StudySpec_MetricSpec_MAXIMIZE
	case spb.MetricSpec_MINIMIZE:
		goal = vizierpb.StudySpec_MetricSpec_MINIMIZE
	default:
		return nil, fmt.Errorf("%w: metric[0] unsupported goal %d", study.ErrInvalidStudyConfig, int32(m.GetGoal()))
	}
	return []*vizierpb.StudySpec_MetricSpec{{MetricId: m.GetMetricId(), Goal: goal}}, nil
}

func convertParameter(idx int, p *spb.ParameterSpec) (*vizierpb.StudySpec_ParameterSpec, error) {
	if p == nil || p.GetParameterId() == "" {
		return nil, fmt.Errorf("%w: parameter[%d] must have a non-empty parameter_id", study.ErrInvalidStudyConfig, idx)
	}
	scale, err := convertScaleType(idx, p.GetScaleType())
	if err != nil {
		return nil, err
	}
	out := &vizierpb.StudySpec_ParameterSpec{ParameterId: p.GetParameterId(), ScaleType: scale}
	isLog := scale == vizierpb.StudySpec_ParameterSpec_UNIT_LOG_SCALE || scale == vizierpb.StudySpec_ParameterSpec_UNIT_REVERSE_LOG_SCALE
	switch v := p.GetParameterValueSpec().(type) {
	case *spb.ParameterSpec_DoubleValueSpec:
		spec, err := convertDoubleSpec(idx, v.DoubleValueSpec, isLog)
		if err != nil {
			return nil, err
		}
		out.ParameterValueSpec = &vizierpb.StudySpec_ParameterSpec_DoubleValueSpec_{DoubleValueSpec: spec}
	case *spb.ParameterSpec_IntegerValueSpec:
		spec, err := convertIntegerSpec(idx, v.IntegerValueSpec, isLog)
		if err != nil {
			return nil, err
		}
		out.ParameterValueSpec = &vizierpb.StudySpec_ParameterSpec_IntegerValueSpec_{IntegerValueSpec: spec}
	case *spb.ParameterSpec_CategoricalValueSpec:
		if v.CategoricalValueSpec == nil || len(v.CategoricalValueSpec.GetValues()) == 0 {
			return nil, fmt.Errorf("%w: parameter[%d] categorical values must not be empty", study.ErrInvalidStudyConfig, idx)
		}
		if scale != vizierpb.StudySpec_ParameterSpec_SCALE_TYPE_UNSPECIFIED {
			return nil, fmt.Errorf("%w: parameter[%d] categorical parameter must not set scale_type", study.ErrInvalidStudyConfig, idx)
		}
		out.ParameterValueSpec = &vizierpb.StudySpec_ParameterSpec_CategoricalValueSpec_{
			CategoricalValueSpec: &vizierpb.StudySpec_ParameterSpec_CategoricalValueSpec{Values: slices.Clone(v.CategoricalValueSpec.GetValues())},
		}
	case *spb.ParameterSpec_DiscreteValueSpec:
		spec, err := convertDiscreteSpec(idx, v.DiscreteValueSpec, isLog)
		if err != nil {
			return nil, err
		}
		out.ParameterValueSpec = &vizierpb.StudySpec_ParameterSpec_DiscreteValueSpec_{DiscreteValueSpec: spec}
	default:
		return nil, fmt.Errorf("%w: parameter[%d] missing parameter_value_spec", study.ErrInvalidStudyConfig, idx)
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

func convertDoubleSpec(idx int, s *spb.DoubleValueSpec, isLog bool) (*vizierpb.StudySpec_ParameterSpec_DoubleValueSpec, error) {
	if s == nil {
		return nil, fmt.Errorf("%w: parameter[%d] double_value_spec must not be nil", study.ErrInvalidStudyConfig, idx)
	}
	minVal, maxVal := s.GetMinValue(), s.GetMaxValue()
	if !isFinite(minVal) || !isFinite(maxVal) || minVal > maxVal || (isLog && minVal <= 0) {
		return nil, fmt.Errorf("%w: parameter[%d] invalid double bounds", study.ErrInvalidStudyConfig, idx)
	}
	return &vizierpb.StudySpec_ParameterSpec_DoubleValueSpec{MinValue: minVal, MaxValue: maxVal}, nil
}

func convertIntegerSpec(idx int, s *spb.IntegerValueSpec, isLog bool) (*vizierpb.StudySpec_ParameterSpec_IntegerValueSpec, error) {
	if s == nil {
		return nil, fmt.Errorf("%w: parameter[%d] integer_value_spec must not be nil", study.ErrInvalidStudyConfig, idx)
	}
	minVal, maxVal := s.GetMinValue(), s.GetMaxValue()
	if minVal > maxVal || (isLog && minVal <= 0) {
		return nil, fmt.Errorf("%w: parameter[%d] invalid integer bounds", study.ErrInvalidStudyConfig, idx)
	}
	return &vizierpb.StudySpec_ParameterSpec_IntegerValueSpec{MinValue: minVal, MaxValue: maxVal}, nil
}

func convertDiscreteSpec(idx int, s *spb.DiscreteValueSpec, isLog bool) (*vizierpb.StudySpec_ParameterSpec_DiscreteValueSpec, error) {
	if s == nil || len(s.GetValues()) == 0 {
		return nil, fmt.Errorf("%w: parameter[%d] discrete values must not be empty", study.ErrInvalidStudyConfig, idx)
	}
	for _, v := range s.GetValues() {
		if !isFinite(v) {
			return nil, fmt.Errorf("%w: parameter[%d] discrete values must be finite", study.ErrInvalidStudyConfig, idx)
		}
	}
	sorted := slices.Clone(s.GetValues())
	slices.Sort(sorted)
	if isLog && sorted[0] <= 0 {
		return nil, fmt.Errorf("%w: parameter[%d] log-scaled discrete values must be positive", study.ErrInvalidStudyConfig, idx)
	}
	return &vizierpb.StudySpec_ParameterSpec_DiscreteValueSpec{Values: sorted}, nil
}

func isFinite(v float64) bool { return !math.IsNaN(v) && !math.IsInf(v, 0) }
