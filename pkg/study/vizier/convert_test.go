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
	"errors"
	"math"
	"strings"
	"testing"

	"github.com/google/go-cmp/cmp"
	"google.golang.org/protobuf/testing/protocmp"
	spb "xmanager_cloud/api/protos/experiment"
	"xmanager_cloud/pkg/study"
	"xmanager_cloud/pkg/study/vizierpb"
)

func validStudyConfig() *spb.StudyConfig {
	return &spb.StudyConfig{
		Algorithm:         "RANDOM_SEARCH",
		MaxTrialBudget:    20,
		MaxParallelTrials: 4,
		Metrics:           []*spb.MetricSpec{{MetricId: "accuracy", Goal: spb.MetricSpec_MAXIMIZE}},
		Parameters: []*spb.ParameterSpec{{
			ParameterId:        "lr",
			ScaleType:          spb.ParameterSpec_LOG,
			ParameterValueSpec: &spb.ParameterSpec_DoubleValueSpec{DoubleValueSpec: &spb.DoubleValueSpec{MinValue: 1e-4, MaxValue: 1e-1}},
		}},
	}
}

func TestToStudySpec_ValidConversions(t *testing.T) {
	t.Parallel()
	unsortedDiscrete := []float64{3.0, 1.0, 2.5}
	tests := []struct {
		name string
		cfg  *spb.StudyConfig
		want *vizierpb.StudySpec
	}{
		{
			name: "all parameter specs and scales",
			cfg: &spb.StudyConfig{
				Algorithm: "QUASI_RANDOM_SEARCH",
				Metrics:   []*spb.MetricSpec{{MetricId: "loss", Goal: spb.MetricSpec_MINIMIZE}},
				Parameters: []*spb.ParameterSpec{
					{ParameterId: "lr", ScaleType: spb.ParameterSpec_LOG, ParameterValueSpec: &spb.ParameterSpec_DoubleValueSpec{DoubleValueSpec: &spb.DoubleValueSpec{MinValue: 0.001, MaxValue: 0.1}}},
					{ParameterId: "layers", ScaleType: spb.ParameterSpec_LINEAR, ParameterValueSpec: &spb.ParameterSpec_IntegerValueSpec{IntegerValueSpec: &spb.IntegerValueSpec{MinValue: 1, MaxValue: 8}}},
					{ParameterId: "optimizer", ParameterValueSpec: &spb.ParameterSpec_CategoricalValueSpec{CategoricalValueSpec: &spb.CategoricalValueSpec{Values: []string{"adam", "sgd"}}}},
					{ParameterId: "batch", ScaleType: spb.ParameterSpec_REVERSE_LOG, ParameterValueSpec: &spb.ParameterSpec_DiscreteValueSpec{DiscreteValueSpec: &spb.DiscreteValueSpec{Values: unsortedDiscrete}}},
				},
			},
			want: &vizierpb.StudySpec{
				Algorithm: "QUASI_RANDOM_SEARCH",
				Metrics:   []*vizierpb.StudySpec_MetricSpec{{MetricId: "loss", Goal: vizierpb.StudySpec_MetricSpec_MINIMIZE}},
				Parameters: []*vizierpb.StudySpec_ParameterSpec{
					{ParameterId: "lr", ScaleType: vizierpb.StudySpec_ParameterSpec_UNIT_LOG_SCALE, ParameterValueSpec: &vizierpb.StudySpec_ParameterSpec_DoubleValueSpec_{DoubleValueSpec: &vizierpb.StudySpec_ParameterSpec_DoubleValueSpec{MinValue: 0.001, MaxValue: 0.1}}},
					{ParameterId: "layers", ScaleType: vizierpb.StudySpec_ParameterSpec_UNIT_LINEAR_SCALE, ParameterValueSpec: &vizierpb.StudySpec_ParameterSpec_IntegerValueSpec_{IntegerValueSpec: &vizierpb.StudySpec_ParameterSpec_IntegerValueSpec{MinValue: 1, MaxValue: 8}}},
					{ParameterId: "optimizer", ScaleType: vizierpb.StudySpec_ParameterSpec_SCALE_TYPE_UNSPECIFIED, ParameterValueSpec: &vizierpb.StudySpec_ParameterSpec_CategoricalValueSpec_{CategoricalValueSpec: &vizierpb.StudySpec_ParameterSpec_CategoricalValueSpec{Values: []string{"adam", "sgd"}}}},
					{ParameterId: "batch", ScaleType: vizierpb.StudySpec_ParameterSpec_UNIT_REVERSE_LOG_SCALE, ParameterValueSpec: &vizierpb.StudySpec_ParameterSpec_DiscreteValueSpec_{DiscreteValueSpec: &vizierpb.StudySpec_ParameterSpec_DiscreteValueSpec{Values: []float64{1.0, 2.5, 3.0}}}},
				},
			},
		},
		{
			name: "unspecified scale, maximize goal, and grid search",
			cfg: &spb.StudyConfig{
				Algorithm:  "GRID_SEARCH",
				Metrics:    []*spb.MetricSpec{{MetricId: "acc", Goal: spb.MetricSpec_MAXIMIZE}},
				Parameters: []*spb.ParameterSpec{{ParameterId: "x", ParameterValueSpec: &spb.ParameterSpec_DoubleValueSpec{DoubleValueSpec: &spb.DoubleValueSpec{MinValue: -5, MaxValue: 5}}}},
			},
			want: &vizierpb.StudySpec{
				Algorithm:  "GRID_SEARCH",
				Metrics:    []*vizierpb.StudySpec_MetricSpec{{MetricId: "acc", Goal: vizierpb.StudySpec_MetricSpec_MAXIMIZE}},
				Parameters: []*vizierpb.StudySpec_ParameterSpec{{ParameterId: "x", ParameterValueSpec: &vizierpb.StudySpec_ParameterSpec_DoubleValueSpec_{DoubleValueSpec: &vizierpb.StudySpec_ParameterSpec_DoubleValueSpec{MinValue: -5, MaxValue: 5}}}},
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			got, err := ToStudySpec(tc.cfg)
			if err != nil {
				t.Fatalf("ToStudySpec() error = %v", err)
			}
			if diff := cmp.Diff(tc.want, got, protocmp.Transform()); diff != "" {
				t.Errorf("ToStudySpec() mismatch (-want +got):\n%s", diff)
			}
		})
	}
	if diff := cmp.Diff([]float64{3.0, 1.0, 2.5}, unsortedDiscrete); diff != "" {
		t.Errorf("ToStudySpec() mutated caller discrete slice (-want +got):\n%s", diff)
	}
}

func TestToStudySpec_InvalidInputsAndPayloadFreeErrors(t *testing.T) {
	t.Parallel()
	const secret = "SECRET_USER_TOKEN_XYZ"
	withParam := func(p *spb.ParameterSpec) *spb.StudyConfig {
		return &spb.StudyConfig{Algorithm: "RANDOM_SEARCH", Metrics: []*spb.MetricSpec{{MetricId: secret, Goal: spb.MetricSpec_MAXIMIZE}}, Parameters: []*spb.ParameterSpec{p}}
	}
	tests := []struct {
		name string
		cfg  *spb.StudyConfig
	}{
		{name: "nil config", cfg: nil},
		{name: "empty algorithm", cfg: &spb.StudyConfig{Metrics: []*spb.MetricSpec{{MetricId: secret, Goal: spb.MetricSpec_MAXIMIZE}}, Parameters: validStudyConfig().Parameters}},
		{name: "empty parameters", cfg: &spb.StudyConfig{Algorithm: secret, Metrics: []*spb.MetricSpec{{MetricId: secret, Goal: spb.MetricSpec_MAXIMIZE}}}},
		{name: "zero metrics", cfg: &spb.StudyConfig{Algorithm: "RANDOM_SEARCH", Parameters: validStudyConfig().Parameters}},
		{name: "two metrics", cfg: &spb.StudyConfig{Algorithm: "RANDOM_SEARCH", Metrics: []*spb.MetricSpec{{MetricId: secret, Goal: spb.MetricSpec_MAXIMIZE}, {MetricId: "m2", Goal: spb.MetricSpec_MINIMIZE}}, Parameters: validStudyConfig().Parameters}},
		{name: "unspecified goal", cfg: &spb.StudyConfig{Algorithm: "RANDOM_SEARCH", Metrics: []*spb.MetricSpec{{MetricId: secret}}, Parameters: validStudyConfig().Parameters}},
		{name: "unknown goal enum", cfg: &spb.StudyConfig{Algorithm: "RANDOM_SEARCH", Metrics: []*spb.MetricSpec{{MetricId: secret, Goal: spb.MetricSpec_GoalType(99)}}, Parameters: validStudyConfig().Parameters}},
		{name: "empty param id", cfg: withParam(&spb.ParameterSpec{})},
		{name: "unset value spec", cfg: withParam(&spb.ParameterSpec{ParameterId: secret})},
		{name: "unknown scale type", cfg: withParam(&spb.ParameterSpec{ParameterId: secret, ScaleType: spb.ParameterSpec_ScaleType(42), ParameterValueSpec: &spb.ParameterSpec_DoubleValueSpec{DoubleValueSpec: &spb.DoubleValueSpec{MinValue: 1, MaxValue: 2}}})},
		{name: "double NaN", cfg: withParam(&spb.ParameterSpec{ParameterId: secret, ParameterValueSpec: &spb.ParameterSpec_DoubleValueSpec{DoubleValueSpec: &spb.DoubleValueSpec{MinValue: math.NaN(), MaxValue: 1}}})},
		{name: "double +Inf", cfg: withParam(&spb.ParameterSpec{ParameterId: secret, ParameterValueSpec: &spb.ParameterSpec_DoubleValueSpec{DoubleValueSpec: &spb.DoubleValueSpec{MinValue: 0, MaxValue: math.Inf(1)}}})},
		{name: "double min > max", cfg: withParam(&spb.ParameterSpec{ParameterId: secret, ParameterValueSpec: &spb.ParameterSpec_DoubleValueSpec{DoubleValueSpec: &spb.DoubleValueSpec{MinValue: 5, MaxValue: 1}}})},
		{name: "double log min <= 0", cfg: withParam(&spb.ParameterSpec{ParameterId: secret, ScaleType: spb.ParameterSpec_LOG, ParameterValueSpec: &spb.ParameterSpec_DoubleValueSpec{DoubleValueSpec: &spb.DoubleValueSpec{MinValue: 0, MaxValue: 1}}})},
		{name: "integer min > max", cfg: withParam(&spb.ParameterSpec{ParameterId: secret, ParameterValueSpec: &spb.ParameterSpec_IntegerValueSpec{IntegerValueSpec: &spb.IntegerValueSpec{MinValue: 10, MaxValue: 2}}})},
		{name: "integer reverse_log min <= 0", cfg: withParam(&spb.ParameterSpec{ParameterId: secret, ScaleType: spb.ParameterSpec_REVERSE_LOG, ParameterValueSpec: &spb.ParameterSpec_IntegerValueSpec{IntegerValueSpec: &spb.IntegerValueSpec{MinValue: -1, MaxValue: 2}}})},
		{name: "categorical empty", cfg: withParam(&spb.ParameterSpec{ParameterId: secret, ParameterValueSpec: &spb.ParameterSpec_CategoricalValueSpec{CategoricalValueSpec: &spb.CategoricalValueSpec{}}})},
		{name: "categorical scaled", cfg: withParam(&spb.ParameterSpec{ParameterId: secret, ScaleType: spb.ParameterSpec_LINEAR, ParameterValueSpec: &spb.ParameterSpec_CategoricalValueSpec{CategoricalValueSpec: &spb.CategoricalValueSpec{Values: []string{secret}}}})},
		{name: "discrete empty", cfg: withParam(&spb.ParameterSpec{ParameterId: secret, ParameterValueSpec: &spb.ParameterSpec_DiscreteValueSpec{DiscreteValueSpec: &spb.DiscreteValueSpec{}}})},
		{name: "discrete non-finite", cfg: withParam(&spb.ParameterSpec{ParameterId: secret, ParameterValueSpec: &spb.ParameterSpec_DiscreteValueSpec{DiscreteValueSpec: &spb.DiscreteValueSpec{Values: []float64{1, math.Inf(-1)}}}})},
		{name: "discrete log <= 0", cfg: withParam(&spb.ParameterSpec{ParameterId: secret, ScaleType: spb.ParameterSpec_LOG, ParameterValueSpec: &spb.ParameterSpec_DiscreteValueSpec{DiscreteValueSpec: &spb.DiscreteValueSpec{Values: []float64{2.0, 0.0}}}})},
		{name: "discrete duplicates", cfg: withParam(&spb.ParameterSpec{ParameterId: secret, ParameterValueSpec: &spb.ParameterSpec_DiscreteValueSpec{DiscreteValueSpec: &spb.DiscreteValueSpec{Values: []float64{3.0, 1.0, 3.0}}}})},
		{name: "discrete signed zero duplicates", cfg: withParam(&spb.ParameterSpec{ParameterId: secret, ParameterValueSpec: &spb.ParameterSpec_DiscreteValueSpec{DiscreteValueSpec: &spb.DiscreteValueSpec{Values: []float64{math.Copysign(0, -1), 0.0}}}})},
		{name: "categorical empty string", cfg: withParam(&spb.ParameterSpec{ParameterId: secret, ParameterValueSpec: &spb.ParameterSpec_CategoricalValueSpec{CategoricalValueSpec: &spb.CategoricalValueSpec{Values: []string{"a", ""}}}})},
		{name: "categorical duplicate", cfg: withParam(&spb.ParameterSpec{ParameterId: secret, ParameterValueSpec: &spb.ParameterSpec_CategoricalValueSpec{CategoricalValueSpec: &spb.CategoricalValueSpec{Values: []string{secret, secret}}}})},
		{name: "duplicate parameter_id", cfg: &spb.StudyConfig{
			Algorithm: "RANDOM_SEARCH",
			Metrics:   []*spb.MetricSpec{{MetricId: "m", Goal: spb.MetricSpec_MAXIMIZE}},
			Parameters: []*spb.ParameterSpec{
				{ParameterId: secret, ParameterValueSpec: &spb.ParameterSpec_IntegerValueSpec{IntegerValueSpec: &spb.IntegerValueSpec{MinValue: 1, MaxValue: 2}}},
				{ParameterId: secret, ParameterValueSpec: &spb.ParameterSpec_IntegerValueSpec{IntegerValueSpec: &spb.IntegerValueSpec{MinValue: 3, MaxValue: 4}}},
			},
		}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := ToStudySpec(tc.cfg)
			if !errors.Is(err, study.ErrInvalidStudyConfig) {
				t.Fatalf("ToStudySpec() error = %v, want ErrInvalidStudyConfig", err)
			}
			if strings.Contains(err.Error(), secret) {
				t.Errorf("error %q leaked user payload %q", err.Error(), secret)
			}
		})
	}
}
