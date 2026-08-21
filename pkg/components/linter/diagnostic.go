// Copyright 2026 Google LLC
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

package linter

// DiagnosticSeverity defines the severity level of a diagnostic result.
type DiagnosticSeverity string

const (
	// SeverityError indicates a blocking validation failure.
	SeverityError DiagnosticSeverity = "ERROR"
	// SeverityWarning indicates a non-blocking diagnostic recommendation.
	SeverityWarning DiagnosticSeverity = "WARNING"
	// SeverityInfo provides informational context on manifest validation.
	SeverityInfo DiagnosticSeverity = "INFO"
)

// Diagnostic represents an individual lint issue located within a file.
type Diagnostic struct {
	File     string             `json:"file" yaml:"file"`
	Line     int                `json:"line" yaml:"line"`
	Severity DiagnosticSeverity `json:"severity" yaml:"severity"`
	Rule     string             `json:"rule" yaml:"rule"`
	Message  string             `json:"message" yaml:"message"`
}

// ValidationResult aggregates diagnostics, errors, and overall validity of linted manifests.
type ValidationResult struct {
	Valid       bool         `json:"valid" yaml:"valid"`
	Diagnostics []Diagnostic `json:"diagnostics" yaml:"diagnostics"`
	Errors      []string     `json:"errors" yaml:"errors"`
	Warnings    []string     `json:"warnings" yaml:"warnings"`
}

// NewValidationResult initializes a clean ValidationResult.
func NewValidationResult() *ValidationResult {
	return &ValidationResult{
		Valid:       true,
		Diagnostics: []Diagnostic{},
		Errors:      []string{},
		Warnings:    []string{},
	}
}

// AddDiagnostic appends a diagnostic to the result and updates validity status.
func (vr *ValidationResult) AddDiagnostic(d Diagnostic) {
	vr.Diagnostics = append(vr.Diagnostics, d)
	if d.Severity == SeverityError {
		vr.Valid = false
		vr.Errors = append(vr.Errors, d.Message)
		return
	}
	if d.Severity == SeverityWarning {
		vr.Warnings = append(vr.Warnings, d.Message)
	}
}

// AddError records an ERROR severity diagnostic.
func (vr *ValidationResult) AddError(file string, line int, rule string, msg string) {
	vr.AddDiagnostic(Diagnostic{
		File:     file,
		Line:     line,
		Severity: SeverityError,
		Rule:     rule,
		Message:  msg,
	})
}

// AddWarning records a WARNING severity diagnostic.
func (vr *ValidationResult) AddWarning(file string, line int, rule string, msg string) {
	vr.AddDiagnostic(Diagnostic{
		File:     file,
		Line:     line,
		Severity: SeverityWarning,
		Rule:     rule,
		Message:  msg,
	})
}

// HasErrors returns true if the validation result contains any errors.
func (vr *ValidationResult) HasErrors() bool {
	return !vr.Valid || len(vr.Errors) > 0
}
