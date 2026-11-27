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

package dataengine

import (
	"slices"

	dpb "xmanager_cloud/api/protos/dashboard"
	essmsgpb "xmanager_cloud/api/protos/experiment"
)

// The dashboard enums mirror their experiment counterparts value for value (the dashboard protos
// cannot import the experiment protos), so the conversions below keep the enum numbers. A test
// checks that the mirrored enums stay identical.

// axesFromStudyConfig returns one axis per study parameter, in the order of the study configuration.
func axesFromStudyConfig(config *essmsgpb.StudyConfig) []*dpb.ParallelCoordinatesData_Axis {
	axes := make([]*dpb.ParallelCoordinatesData_Axis, 0, len(config.GetParameters()))
	for _, parameter := range config.GetParameters() {
		axes = append(axes, axisFromParameter(parameter))
	}
	return axes
}

// axisFromParameter returns the axis spanned by the feasible values of a study parameter.
func axisFromParameter(parameter *essmsgpb.ParameterSpec) *dpb.ParallelCoordinatesData_Axis {
	axis := &dpb.ParallelCoordinatesData_Axis{
		ParameterId: parameter.GetParameterId(),
		ScaleType:   dpb.ParallelCoordinatesData_Axis_ScaleType(parameter.GetScaleType()),
	}
	switch spec := parameter.GetParameterValueSpec().(type) {
	case *essmsgpb.ParameterSpec_DoubleValueSpec:
		axis.Values = &dpb.ParallelCoordinatesData_Axis_DoubleRange_{DoubleRange: &dpb.ParallelCoordinatesData_Axis_DoubleRange{
			MinValue: spec.DoubleValueSpec.GetMinValue(),
			MaxValue: spec.DoubleValueSpec.GetMaxValue(),
		}}
	case *essmsgpb.ParameterSpec_IntegerValueSpec:
		axis.Values = &dpb.ParallelCoordinatesData_Axis_IntegerRange_{IntegerRange: &dpb.ParallelCoordinatesData_Axis_IntegerRange{
			MinValue: spec.IntegerValueSpec.GetMinValue(),
			MaxValue: spec.IntegerValueSpec.GetMaxValue(),
		}}
	case *essmsgpb.ParameterSpec_CategoricalValueSpec:
		axis.Values = &dpb.ParallelCoordinatesData_Axis_CategoricalValues_{CategoricalValues: &dpb.ParallelCoordinatesData_Axis_CategoricalValues{
			Values: slices.Clone(spec.CategoricalValueSpec.GetValues()),
		}}
	case *essmsgpb.ParameterSpec_DiscreteValueSpec:
		axis.Values = &dpb.ParallelCoordinatesData_Axis_DiscreteValues_{DiscreteValues: &dpb.ParallelCoordinatesData_Axis_DiscreteValues{
			Values: slices.Sorted(slices.Values(spec.DiscreteValueSpec.GetValues())),
		}}
	}
	return axis
}

// objectiveFromStudyConfig returns the objective metric of the study, which has exactly one.
func objectiveFromStudyConfig(config *essmsgpb.StudyConfig) *dpb.ParallelCoordinatesData_Objective {
	metrics := config.GetMetrics()
	if len(metrics) == 0 {
		return nil
	}
	return &dpb.ParallelCoordinatesData_Objective{
		MetricId: metrics[0].GetMetricId(),
		Goal:     dpb.ParallelCoordinatesData_Objective_GoalType(metrics[0].GetGoal()),
	}
}
