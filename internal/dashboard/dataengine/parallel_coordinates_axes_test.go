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
	"testing"

	"github.com/google/go-cmp/cmp"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/testing/protocmp"

	dpb "xmanager_cloud/api/protos/dashboard"
	essmsgpb "xmanager_cloud/api/protos/experiment"
)

func TestAxisFromParameter(t *testing.T) {
	tests := []struct {
		name      string
		parameter *essmsgpb.ParameterSpec
		want      *dpb.ParallelCoordinatesData_Axis
	}{
		{
			name: "double with log scale",
			parameter: &essmsgpb.ParameterSpec{
				ParameterId:        "learning_rate",
				ParameterValueSpec: &essmsgpb.ParameterSpec_DoubleValueSpec{DoubleValueSpec: &essmsgpb.DoubleValueSpec{MinValue: 1e-5, MaxValue: 0.1}},
				ScaleType:          essmsgpb.ParameterSpec_LOG,
			},
			want: &dpb.ParallelCoordinatesData_Axis{
				ParameterId: "learning_rate",
				Values:      &dpb.ParallelCoordinatesData_Axis_DoubleRange_{DoubleRange: &dpb.ParallelCoordinatesData_Axis_DoubleRange{MinValue: 1e-5, MaxValue: 0.1}},
				ScaleType:   dpb.ParallelCoordinatesData_Axis_LOG,
			},
		},
		{
			name: "integer with linear scale",
			parameter: &essmsgpb.ParameterSpec{
				ParameterId:        "layers",
				ParameterValueSpec: &essmsgpb.ParameterSpec_IntegerValueSpec{IntegerValueSpec: &essmsgpb.IntegerValueSpec{MinValue: 1, MaxValue: 1 << 53}},
				ScaleType:          essmsgpb.ParameterSpec_LINEAR,
			},
			want: &dpb.ParallelCoordinatesData_Axis{
				ParameterId: "layers",
				Values:      &dpb.ParallelCoordinatesData_Axis_IntegerRange_{IntegerRange: &dpb.ParallelCoordinatesData_Axis_IntegerRange{MinValue: 1, MaxValue: 1 << 53}},
				ScaleType:   dpb.ParallelCoordinatesData_Axis_LINEAR,
			},
		},
		{
			name: "categorical keeps the configured order and has no scale",
			parameter: &essmsgpb.ParameterSpec{
				ParameterId:        "optimizer",
				ParameterValueSpec: &essmsgpb.ParameterSpec_CategoricalValueSpec{CategoricalValueSpec: &essmsgpb.CategoricalValueSpec{Values: []string{"sgd", "adam", "日本語"}}},
			},
			want: &dpb.ParallelCoordinatesData_Axis{
				ParameterId: "optimizer",
				Values:      &dpb.ParallelCoordinatesData_Axis_CategoricalValues_{CategoricalValues: &dpb.ParallelCoordinatesData_Axis_CategoricalValues{Values: []string{"sgd", "adam", "日本語"}}},
			},
		},
		{
			name: "discrete values are sorted ascending with reverse log scale",
			parameter: &essmsgpb.ParameterSpec{
				ParameterId:        "batch_size",
				ParameterValueSpec: &essmsgpb.ParameterSpec_DiscreteValueSpec{DiscreteValueSpec: &essmsgpb.DiscreteValueSpec{Values: []float64{64, 16, 32}}},
				ScaleType:          essmsgpb.ParameterSpec_REVERSE_LOG,
			},
			want: &dpb.ParallelCoordinatesData_Axis{
				ParameterId: "batch_size",
				Values:      &dpb.ParallelCoordinatesData_Axis_DiscreteValues_{DiscreteValues: &dpb.ParallelCoordinatesData_Axis_DiscreteValues{Values: []float64{16, 32, 64}}},
				ScaleType:   dpb.ParallelCoordinatesData_Axis_REVERSE_LOG,
			},
		},
		{
			name:      "no feasible values and unspecified scale",
			parameter: &essmsgpb.ParameterSpec{ParameterId: "empty"},
			want:      &dpb.ParallelCoordinatesData_Axis{ParameterId: "empty"},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := axisFromParameter(tc.parameter)
			if diff := cmp.Diff(tc.want, got, protocmp.Transform()); diff != "" {
				t.Errorf("axisFromParameter() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestAxisFromParameterDoesNotAliasTheStudyConfig(t *testing.T) {
	discrete := []float64{3, 1, 2}
	categorical := []string{"a", "b"}
	axisFromParameter(&essmsgpb.ParameterSpec{ParameterValueSpec: &essmsgpb.ParameterSpec_DiscreteValueSpec{DiscreteValueSpec: &essmsgpb.DiscreteValueSpec{Values: discrete}}})
	axis := axisFromParameter(&essmsgpb.ParameterSpec{ParameterValueSpec: &essmsgpb.ParameterSpec_CategoricalValueSpec{CategoricalValueSpec: &essmsgpb.CategoricalValueSpec{Values: categorical}}})
	axis.GetCategoricalValues().Values[0] = "changed"

	if diff := cmp.Diff([]float64{3, 1, 2}, discrete); diff != "" {
		t.Errorf("study config discrete values were modified (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff([]string{"a", "b"}, categorical); diff != "" {
		t.Errorf("study config categorical values were modified (-want +got):\n%s", diff)
	}
}

func TestAxesFromStudyConfigKeepsParameterOrder(t *testing.T) {
	config := &essmsgpb.StudyConfig{Parameters: []*essmsgpb.ParameterSpec{{ParameterId: "b"}, {ParameterId: "a"}, {ParameterId: "c"}}}

	var got []string
	for _, axis := range axesFromStudyConfig(config) {
		got = append(got, axis.GetParameterId())
	}
	if diff := cmp.Diff([]string{"b", "a", "c"}, got); diff != "" {
		t.Errorf("axesFromStudyConfig() parameter order mismatch (-want +got):\n%s", diff)
	}
	if axes := axesFromStudyConfig(&essmsgpb.StudyConfig{}); axes == nil || len(axes) != 0 {
		t.Errorf("axesFromStudyConfig(no parameters) = %v, want an empty non-nil slice", axes)
	}
}

func TestObjectiveFromStudyConfig(t *testing.T) {
	tests := []struct {
		name   string
		config *essmsgpb.StudyConfig
		want   *dpb.ParallelCoordinatesData_Objective
	}{
		{
			name:   "maximize",
			config: &essmsgpb.StudyConfig{Metrics: []*essmsgpb.MetricSpec{{MetricId: "accuracy", Goal: essmsgpb.MetricSpec_MAXIMIZE}}},
			want:   &dpb.ParallelCoordinatesData_Objective{MetricId: "accuracy", Goal: dpb.ParallelCoordinatesData_Objective_MAXIMIZE},
		},
		{
			name:   "minimize",
			config: &essmsgpb.StudyConfig{Metrics: []*essmsgpb.MetricSpec{{MetricId: "val/loss", Goal: essmsgpb.MetricSpec_MINIMIZE}}},
			want:   &dpb.ParallelCoordinatesData_Objective{MetricId: "val/loss", Goal: dpb.ParallelCoordinatesData_Objective_MINIMIZE},
		},
		{name: "no metric", config: &essmsgpb.StudyConfig{}, want: nil},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if diff := cmp.Diff(tc.want, objectiveFromStudyConfig(tc.config), protocmp.Transform()); diff != "" {
				t.Errorf("objectiveFromStudyConfig() mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

// TestMirroredEnumsMatchTheirSource guards the number-preserving enum conversions: a value added to
// an experiment enum must be mirrored in the dashboard enum with the same name and number.
func TestMirroredEnumsMatchTheirSource(t *testing.T) {
	tests := []struct {
		name   string
		source protoreflect.EnumDescriptor
		mirror protoreflect.EnumDescriptor
	}{
		{
			name:   "work unit state",
			source: essmsgpb.WorkUnitStatus_WORK_UNIT_STATE_UNSPECIFIED.Descriptor(),
			mirror: dpb.ParallelCoordinatesData_RunTrajectory_WORK_UNIT_STATE_UNSPECIFIED.Descriptor(),
		},
		{
			name:   "scale type",
			source: essmsgpb.ParameterSpec_SCALE_TYPE_UNSPECIFIED.Descriptor(),
			mirror: dpb.ParallelCoordinatesData_Axis_SCALE_TYPE_UNSPECIFIED.Descriptor(),
		},
		{
			name:   "goal type",
			source: essmsgpb.MetricSpec_GOAL_TYPE_UNSPECIFIED.Descriptor(),
			mirror: dpb.ParallelCoordinatesData_Objective_GOAL_TYPE_UNSPECIFIED.Descriptor(),
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if diff := cmp.Diff(enumValues(tc.source), enumValues(tc.mirror)); diff != "" {
				t.Errorf("%s does not mirror %s (-source +mirror):\n%s", tc.mirror.FullName(), tc.source.FullName(), diff)
			}
		})
	}
}

func enumValues(enum protoreflect.EnumDescriptor) map[string]protoreflect.EnumNumber {
	values := make(map[string]protoreflect.EnumNumber, enum.Values().Len())
	for i := range enum.Values().Len() {
		value := enum.Values().Get(i)
		values[string(value.Name())] = value.Number()
	}
	return values
}
