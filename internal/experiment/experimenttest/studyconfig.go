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

// Package experimenttest provides experiment test fixtures shared across packages.
package experimenttest

import essmsgpb "xmanager_cloud/api/protos/experiment"

// StudyConfig returns a new valid single-objective study config that uses every parameter type.
// Derive variants with prototest.Modify.
func StudyConfig() *essmsgpb.StudyConfig {
	return &essmsgpb.StudyConfig{
		Parameters: []*essmsgpb.ParameterSpec{
			{
				ParameterId:        "learning_rate",
				ParameterValueSpec: &essmsgpb.ParameterSpec_DoubleValueSpec{DoubleValueSpec: &essmsgpb.DoubleValueSpec{MinValue: 1e-5, MaxValue: 1e-1}},
				ScaleType:          essmsgpb.ParameterSpec_LOG,
			},
			{
				ParameterId:        "batch_size",
				ParameterValueSpec: &essmsgpb.ParameterSpec_IntegerValueSpec{IntegerValueSpec: &essmsgpb.IntegerValueSpec{MinValue: 16, MaxValue: 256}},
			},
			{
				ParameterId:        "optimizer",
				ParameterValueSpec: &essmsgpb.ParameterSpec_CategoricalValueSpec{CategoricalValueSpec: &essmsgpb.CategoricalValueSpec{Values: []string{"adam", "sgd"}}},
			},
			{
				ParameterId:        "dropout",
				ParameterValueSpec: &essmsgpb.ParameterSpec_DiscreteValueSpec{DiscreteValueSpec: &essmsgpb.DiscreteValueSpec{Values: []float64{0.1, 0.2, 0.5}}},
				ScaleType:          essmsgpb.ParameterSpec_LINEAR,
			},
		},
		Metrics:           []*essmsgpb.MetricSpec{{MetricId: "accuracy", Goal: essmsgpb.MetricSpec_MAXIMIZE}},
		MaxTrialBudget:    10,
		Algorithm:         "RANDOM_SEARCH",
		MaxParallelTrials: 2,
	}
}
