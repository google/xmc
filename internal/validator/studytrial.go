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
	"slices"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	structpb "google.golang.org/protobuf/types/known/structpb"
	essmsgpb "xmanager_cloud/api/protos/experiment"
	"xmanager_cloud/internal/grpcerr"
)

// maxTrialIDLen is the maximum length of a work unit trial_id, in bytes.
const maxTrialIDLen = 128

// ValidateWorkUnitTrialFields validates the study trial fields of wu against cfg, the study_config of
// its experiment. When cfg is nil (the experiment is not a study), parameters and trial_id must be
// unset. Otherwise trial_id must be set and parameters must hold exactly one feasible value for each
// parameter of cfg.
func ValidateWorkUnitTrialFields(wu *essmsgpb.WorkUnit, cfg *essmsgpb.StudyConfig) error {
	if cfg == nil {
		if wu.GetParameters() != nil || wu.GetTrialId() != "" {
			return fmt.Errorf("work_unit.parameters and work_unit.trial_id are set only through BatchCreateWorkUnits on a STUDY experiment: %w", grpcerr.ErrInvalidArgument)
		}
		return nil
	}
	if err := validateTrialID(wu.GetTrialId()); err != nil {
		return err
	}
	return validateTrialParameters(wu.GetParameters(), cfg.GetParameters())
}

// validateTrialID requires a non-empty ID of at most maxTrialIDLen bytes of valid UTF-8 printable
// characters.
func validateTrialID(id string) error {
	if id == "" || len(id) > maxTrialIDLen {
		return fmt.Errorf("work_unit.trial_id must be set and at most %d bytes long: %w", maxTrialIDLen, grpcerr.ErrInvalidArgument)
	}
	if !utf8.ValidString(id) {
		return fmt.Errorf("work_unit.trial_id %q is not valid UTF-8: %w", id, grpcerr.ErrInvalidArgument)
	}
	if strings.ContainsFunc(id, func(r rune) bool { return !unicode.IsPrint(r) }) {
		return fmt.Errorf("work_unit.trial_id %q cannot contain non-printable characters: %w", id, grpcerr.ErrInvalidArgument)
	}
	return nil
}

// validateTrialParameters requires params to hold a feasible value for every spec and no other key.
func validateTrialParameters(params *structpb.Struct, specs []*essmsgpb.ParameterSpec) error {
	if params == nil {
		return fmt.Errorf("work_unit.parameters is required on work units of STUDY experiments: %w", grpcerr.ErrInvalidArgument)
	}
	fields := params.GetFields()
	for _, spec := range specs {
		id := spec.GetParameterId()
		value, ok := fields[id]
		if !ok {
			return fmt.Errorf("work_unit.parameters is missing study parameter %q: %w", id, grpcerr.ErrInvalidArgument)
		}
		if err := validateParameterValue(spec, value); err != nil {
			return fmt.Errorf("work_unit.parameters[%q] %w", id, err)
		}
	}
	// Parameter IDs are unique, so when every parameter is present an equal key count leaves no
	// other key.
	if len(fields) == len(specs) {
		return nil
	}
	if key, ok := firstUnknownKey(fields, specs); ok {
		return fmt.Errorf("work_unit.parameters key %s is not a study parameter: %w", quoteParameterKey(key), grpcerr.ErrInvalidArgument)
	}
	return nil
}

// validateParameterValue checks that value is feasible for spec.
func validateParameterValue(spec *essmsgpb.ParameterSpec, value *structpb.Value) error {
	switch s := spec.GetParameterValueSpec().(type) {
	case *essmsgpb.ParameterSpec_DoubleValueSpec:
		return validateNumberInRange(value, s.DoubleValueSpec.GetMinValue(), s.DoubleValueSpec.GetMaxValue(), false)
	case *essmsgpb.ParameterSpec_IntegerValueSpec:
		return validateNumberInRange(value, float64(s.IntegerValueSpec.GetMinValue()), float64(s.IntegerValueSpec.GetMaxValue()), true)
	case *essmsgpb.ParameterSpec_DiscreteValueSpec:
		n, err := finiteNumber(value)
		if err == nil && !slices.Contains(s.DiscreteValueSpec.GetValues(), n) {
			err = fmt.Errorf("must be one of the feasible values, got %v: %w", n, grpcerr.ErrInvalidArgument)
		}
		return err
	case *essmsgpb.ParameterSpec_CategoricalValueSpec:
		str, ok := value.GetKind().(*structpb.Value_StringValue)
		if !ok || !slices.Contains(s.CategoricalValueSpec.GetValues(), str.StringValue) {
			return fmt.Errorf("must be one of the categories as a string: %w", grpcerr.ErrInvalidArgument)
		}
		return nil
	default:
		return fmt.Errorf("has no feasible values in the study config: %w", grpcerr.ErrInvalidArgument)
	}
}

// validateNumberInRange requires value to be a finite number in [minValue, maxValue], and integral
// when integral is set.
func validateNumberInRange(value *structpb.Value, minValue, maxValue float64, integral bool) error {
	n, err := finiteNumber(value)
	if err != nil {
		return err
	}
	if integral && n != math.Trunc(n) {
		return fmt.Errorf("must be an integer, got %v: %w", n, grpcerr.ErrInvalidArgument)
	}
	if n < minValue || n > maxValue {
		return fmt.Errorf("must be in [%v, %v], got %v: %w", minValue, maxValue, n, grpcerr.ErrInvalidArgument)
	}
	return nil
}

// finiteNumber returns the number held by value, failing for other kinds, NaN and infinities.
func finiteNumber(value *structpb.Value) (float64, error) {
	num, ok := value.GetKind().(*structpb.Value_NumberValue)
	if !ok || !isFinite(num.NumberValue) {
		return 0, fmt.Errorf("must be a finite number: %w", grpcerr.ErrInvalidArgument)
	}
	return num.NumberValue, nil
}

// firstUnknownKey returns the smallest key of fields that names no parameter of specs, and whether
// there is one.
func firstUnknownKey(fields map[string]*structpb.Value, specs []*essmsgpb.ParameterSpec) (string, bool) {
	known := make(map[string]bool, len(specs))
	for _, spec := range specs {
		known[spec.GetParameterId()] = true
	}
	var unknown []string
	for key := range fields {
		if !known[key] {
			unknown = append(unknown, key)
		}
	}
	if len(unknown) == 0 {
		return "", false
	}
	sort.Strings(unknown)
	return unknown[0], true
}

// quoteParameterKey quotes key for an error message, truncated to maxStudyParameterIDLen bytes
// because longer keys never name a parameter.
func quoteParameterKey(key string) string {
	if len(key) > maxStudyParameterIDLen {
		return fmt.Sprintf("%q...", key[:maxStudyParameterIDLen])
	}
	return fmt.Sprintf("%q", key)
}
