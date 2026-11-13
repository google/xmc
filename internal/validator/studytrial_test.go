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
	"math"
	"strings"
	"testing"

	structpb "google.golang.org/protobuf/types/known/structpb"
	essmsgpb "xmanager_cloud/api/protos/experiment"
	"xmanager_cloud/internal/experiment/experimenttest"
	"xmanager_cloud/internal/grpcerr"
	"xmanager_cloud/internal/prototest"
)

// trialWorkUnit returns a work unit with valid trial fields for experimenttest.StudyConfig, mutated
// by fn.
func trialWorkUnit(fn func(wu *essmsgpb.WorkUnit)) *essmsgpb.WorkUnit {
	return prototest.Modify(&essmsgpb.WorkUnit{Parameters: experimenttest.TrialParameters(), TrialId: "trial-1"}, fn)
}

// withParameter returns a mutation that sets parameter key to value.
func withParameter(key string, value *structpb.Value) func(wu *essmsgpb.WorkUnit) {
	return func(wu *essmsgpb.WorkUnit) { wu.Parameters.Fields[key] = value }
}

func TestValidateWorkUnitTrialFields_Valid(t *testing.T) {
	tests := []struct {
		name string
		wu   *essmsgpb.WorkUnit
		cfg  *essmsgpb.StudyConfig
	}{
		{name: "standard work unit without trial fields", wu: &essmsgpb.WorkUnit{}},
		{name: "baseline trial", wu: trialWorkUnit(nil), cfg: experimenttest.StudyConfig()},
		{name: "double at min", wu: trialWorkUnit(withParameter("learning_rate", structpb.NewNumberValue(1e-5))), cfg: experimenttest.StudyConfig()},
		{name: "double at max", wu: trialWorkUnit(withParameter("learning_rate", structpb.NewNumberValue(1e-1))), cfg: experimenttest.StudyConfig()},
		{name: "integer at bounds", wu: trialWorkUnit(withParameter("batch_size", structpb.NewNumberValue(256))), cfg: experimenttest.StudyConfig()},
		{name: "discrete feasible value", wu: trialWorkUnit(withParameter("dropout", structpb.NewNumberValue(0.5))), cfg: experimenttest.StudyConfig()},
		{name: "categorical feasible value", wu: trialWorkUnit(withParameter("optimizer", structpb.NewStringValue("sgd"))), cfg: experimenttest.StudyConfig()},
		{name: "multibyte printable trial ID", wu: trialWorkUnit(func(wu *essmsgpb.WorkUnit) { wu.TrialId = "试验-७" }), cfg: experimenttest.StudyConfig()},
		{name: "trial ID at max length", wu: trialWorkUnit(func(wu *essmsgpb.WorkUnit) { wu.TrialId = strings.Repeat("a", maxTrialIDLen) }), cfg: experimenttest.StudyConfig()},
		// IDs are validated unique when a study is created; a duplicated spec must not panic.
		{name: "duplicated parameter spec", wu: trialWorkUnit(nil), cfg: prototest.Modify(experimenttest.StudyConfig(), func(cfg *essmsgpb.StudyConfig) {
			cfg.Parameters = append(cfg.Parameters, cfg.Parameters[0])
		})},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := ValidateWorkUnitTrialFields(tt.wu, tt.cfg); err != nil {
				t.Errorf("ValidateWorkUnitTrialFields() = %v, want nil", err)
			}
		})
	}
}

func TestValidateWorkUnitTrialFields_Invalid(t *testing.T) {
	longKey := strings.Repeat("k", maxStudyParameterIDLen+10)
	listValue, err := structpb.NewList([]any{0.01})
	if err != nil {
		t.Fatalf("structpb.NewList() failed: %v", err)
	}
	tests := []struct {
		name    string
		wu      *essmsgpb.WorkUnit
		cfg     *essmsgpb.StudyConfig
		wantMsg string
	}{
		{name: "parameters on standard work unit", wu: &essmsgpb.WorkUnit{Parameters: experimenttest.TrialParameters()}, wantMsg: "set only through BatchCreateWorkUnits"},
		{name: "empty parameters on standard work unit", wu: &essmsgpb.WorkUnit{Parameters: &structpb.Struct{}}, wantMsg: "set only through BatchCreateWorkUnits"},
		{name: "trial ID on standard work unit", wu: &essmsgpb.WorkUnit{TrialId: "trial-1"}, wantMsg: "set only through BatchCreateWorkUnits"},
		{name: "missing trial ID", wu: trialWorkUnit(func(wu *essmsgpb.WorkUnit) { wu.TrialId = "" }), cfg: experimenttest.StudyConfig(), wantMsg: "trial_id must be set"},
		{name: "trial ID too long", wu: trialWorkUnit(func(wu *essmsgpb.WorkUnit) { wu.TrialId = strings.Repeat("a", maxTrialIDLen+1) }), cfg: experimenttest.StudyConfig(), wantMsg: "at most 128 bytes"},
		{name: "multibyte trial ID too long in bytes", wu: trialWorkUnit(func(wu *essmsgpb.WorkUnit) { wu.TrialId = strings.Repeat("é", maxTrialIDLen/2+1) }), cfg: experimenttest.StudyConfig(), wantMsg: "at most 128 bytes"},
		{name: "non-printable trial ID", wu: trialWorkUnit(func(wu *essmsgpb.WorkUnit) { wu.TrialId = "trial\n1" }), cfg: experimenttest.StudyConfig(), wantMsg: "non-printable"},
		{name: "invalid UTF-8 trial ID", wu: trialWorkUnit(func(wu *essmsgpb.WorkUnit) { wu.TrialId = "trial-\xff" }), cfg: experimenttest.StudyConfig(), wantMsg: "not valid UTF-8"},
		{name: "missing parameters", wu: trialWorkUnit(func(wu *essmsgpb.WorkUnit) { wu.Parameters = nil }), cfg: experimenttest.StudyConfig(), wantMsg: "parameters is required"},
		{name: "empty parameters", wu: trialWorkUnit(func(wu *essmsgpb.WorkUnit) { wu.Parameters = &structpb.Struct{} }), cfg: experimenttest.StudyConfig(), wantMsg: `missing study parameter "learning_rate"`},
		{name: "missing one parameter", wu: trialWorkUnit(func(wu *essmsgpb.WorkUnit) { delete(wu.Parameters.Fields, "dropout") }), cfg: experimenttest.StudyConfig(), wantMsg: `missing study parameter "dropout"`},
		{name: "extra key", wu: trialWorkUnit(withParameter("momentum", structpb.NewNumberValue(0.9))), cfg: experimenttest.StudyConfig(), wantMsg: `key "momentum" is not a study parameter`},
		{name: "smallest of several extra keys reported", wu: trialWorkUnit(func(wu *essmsgpb.WorkUnit) {
			wu.Parameters.Fields["zeta"] = structpb.NewNullValue()
			wu.Parameters.Fields["alpha"] = structpb.NewNullValue()
		}), cfg: experimenttest.StudyConfig(), wantMsg: `key "alpha" is not a study parameter`},
		{name: "multibyte extra key", wu: trialWorkUnit(withParameter("学习率", structpb.NewNumberValue(0.1))), cfg: experimenttest.StudyConfig(), wantMsg: `key "学习率" is not a study parameter`},
		{name: "invalid UTF-8 extra key", wu: trialWorkUnit(withParameter("\xff", structpb.NewNumberValue(0.1))), cfg: experimenttest.StudyConfig(), wantMsg: `key "\xff" is not a study parameter`},
		{name: "long extra key truncated", wu: trialWorkUnit(withParameter(longKey, structpb.NewNumberValue(0.1))), cfg: experimenttest.StudyConfig(), wantMsg: `"` + longKey[:maxStudyParameterIDLen] + `"...`},
		{name: "case-different key", wu: trialWorkUnit(func(wu *essmsgpb.WorkUnit) {
			wu.Parameters.Fields["Dropout"] = wu.Parameters.Fields["dropout"]
			delete(wu.Parameters.Fields, "dropout")
		}), cfg: experimenttest.StudyConfig(), wantMsg: `missing study parameter "dropout"`},
		{name: "double below min", wu: trialWorkUnit(withParameter("learning_rate", structpb.NewNumberValue(1e-6))), cfg: experimenttest.StudyConfig(), wantMsg: "must be in [1e-05, 0.1]"},
		{name: "double above max", wu: trialWorkUnit(withParameter("learning_rate", structpb.NewNumberValue(0.2))), cfg: experimenttest.StudyConfig(), wantMsg: "must be in [1e-05, 0.1]"},
		{name: "double NaN", wu: trialWorkUnit(withParameter("learning_rate", structpb.NewNumberValue(math.NaN()))), cfg: experimenttest.StudyConfig(), wantMsg: "must be a finite number"},
		{name: "double +Inf", wu: trialWorkUnit(withParameter("learning_rate", structpb.NewNumberValue(math.Inf(1)))), cfg: experimenttest.StudyConfig(), wantMsg: "must be a finite number"},
		{name: "double as string", wu: trialWorkUnit(withParameter("learning_rate", structpb.NewStringValue("0.01"))), cfg: experimenttest.StudyConfig(), wantMsg: "must be a finite number"},
		{name: "double as list", wu: trialWorkUnit(withParameter("learning_rate", structpb.NewListValue(listValue))), cfg: experimenttest.StudyConfig(), wantMsg: "must be a finite number"},
		{name: "double as null", wu: trialWorkUnit(withParameter("learning_rate", structpb.NewNullValue())), cfg: experimenttest.StudyConfig(), wantMsg: "must be a finite number"},
		{name: "double unset kind", wu: trialWorkUnit(withParameter("learning_rate", &structpb.Value{})), cfg: experimenttest.StudyConfig(), wantMsg: "must be a finite number"},
		{name: "integer not integral", wu: trialWorkUnit(withParameter("batch_size", structpb.NewNumberValue(32.5))), cfg: experimenttest.StudyConfig(), wantMsg: "must be an integer"},
		{name: "integer below min", wu: trialWorkUnit(withParameter("batch_size", structpb.NewNumberValue(8))), cfg: experimenttest.StudyConfig(), wantMsg: "must be in [16, 256]"},
		{name: "integer -Inf", wu: trialWorkUnit(withParameter("batch_size", structpb.NewNumberValue(math.Inf(-1)))), cfg: experimenttest.StudyConfig(), wantMsg: "must be a finite number"},
		{name: "integer as bool", wu: trialWorkUnit(withParameter("batch_size", structpb.NewBoolValue(true))), cfg: experimenttest.StudyConfig(), wantMsg: "must be a finite number"},
		{name: "discrete infeasible", wu: trialWorkUnit(withParameter("dropout", structpb.NewNumberValue(0.3))), cfg: experimenttest.StudyConfig(), wantMsg: "must be one of the feasible values"},
		{name: "discrete NaN", wu: trialWorkUnit(withParameter("dropout", structpb.NewNumberValue(math.NaN()))), cfg: experimenttest.StudyConfig(), wantMsg: "must be a finite number"},
		{name: "categorical unknown", wu: trialWorkUnit(withParameter("optimizer", structpb.NewStringValue("rmsprop"))), cfg: experimenttest.StudyConfig(), wantMsg: "must be one of the categories"},
		{name: "categorical wrong case", wu: trialWorkUnit(withParameter("optimizer", structpb.NewStringValue("Adam"))), cfg: experimenttest.StudyConfig(), wantMsg: "must be one of the categories"},
		{name: "categorical as number", wu: trialWorkUnit(withParameter("optimizer", structpb.NewNumberValue(1))), cfg: experimenttest.StudyConfig(), wantMsg: "must be one of the categories"},
		{name: "parameter without value spec", wu: trialWorkUnit(nil), cfg: prototest.Modify(experimenttest.StudyConfig(), func(cfg *essmsgpb.StudyConfig) {
			cfg.Parameters[0].ParameterValueSpec = nil
		}), wantMsg: "has no feasible values"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateWorkUnitTrialFields(tt.wu, tt.cfg)
			if !errors.Is(err, grpcerr.ErrInvalidArgument) {
				t.Fatalf("ValidateWorkUnitTrialFields() = %v, want InvalidArgument", err)
			}
			if !strings.Contains(err.Error(), tt.wantMsg) {
				t.Errorf("ValidateWorkUnitTrialFields() = %q, want it to contain %q", err, tt.wantMsg)
			}
		})
	}
}
