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

import (
	"fmt"
	"strings"

	"xmanager_cloud/pkg/components/store"
)

// MatchConstraint evaluates whether the candidate SemVer version string satisfies constraintStr.
func MatchConstraint(versionStr, constraintStr string) (bool, error) {
	v, err := store.Parse(versionStr)
	if err != nil {
		return false, fmt.Errorf("invalid candidate version %q: %w", versionStr, err)
	}
	trimmed := strings.TrimSpace(constraintStr)
	if isWildcardTerm(trimmed) {
		return true, nil
	}
	disjunctions := strings.Split(trimmed, "||")
	anyMatches := false
	for _, disj := range disjunctions {
		disjTrimmed := strings.TrimSpace(disj)
		if disjTrimmed == "" {
			return false, fmt.Errorf("empty disjunction term in constraint %q", constraintStr)
		}
		matches, matchErr := matchConjunction(v, disjTrimmed)
		if matchErr != nil {
			return false, matchErr
		}
		if matches {
			anyMatches = true
		}
	}
	return anyMatches, nil
}

// matchConjunction evaluates a conjunction clause where all space/comma-separated terms must match.
func matchConjunction(v *store.Version, conj string) (bool, error) {
	if strings.Contains(conj, " - ") {
		return matchHyphenRange(v, conj)
	}
	terms := tokenizeConstraintTerms(conj)
	matched := true
	for _, term := range terms {
		ok, err := matchSingleTerm(v, term)
		if err != nil {
			return false, err
		}
		if !ok {
			matched = false
		}
	}
	return matched, nil
}

// matchHyphenRange evaluates an inclusive SemVer hyphen range expression (e.g. "1.0.0 - 2.0.0").
func matchHyphenRange(v *store.Version, conj string) (bool, error) {
	parts := strings.Split(conj, " - ")
	if len(parts) != 2 {
		return false, fmt.Errorf("invalid hyphen range constraint %q", conj)
	}
	vMin, err := parseConstraintVersion(parts[0])
	if err != nil {
		return false, err
	}
	vMax, err := parseConstraintVersion(parts[1])
	if err != nil {
		return false, err
	}
	return !v.LessThan(vMin) && !v.GreaterThan(vMax), nil
}

// tokenizeConstraintTerms splits a conjunction clause into terms and recombines separated operators.
func tokenizeConstraintTerms(s string) []string {
	rawTerms := strings.Fields(strings.ReplaceAll(s, ",", " "))
	var terms []string
	for i := 0; i < len(rawTerms); i++ {
		term := rawTerms[i]
		if isOperator(term) && i+1 < len(rawTerms) {
			terms = append(terms, term+rawTerms[i+1])
			i++
		} else {
			terms = append(terms, term)
		}
	}
	return terms
}

// isOperator reports whether s is a standalone comparison or range operator token.
func isOperator(s string) bool {
	switch s {
	case "==", ">=", "<=", ">", "<", "^", "~", "=":
		return true
	}
	return false
}

// matchSingleTerm evaluates a single constraint term against candidate version v.
func matchSingleTerm(v *store.Version, term string) (bool, error) {
	term = strings.TrimSpace(term)
	if isWildcardTerm(term) {
		return true, nil
	}
	if strings.HasPrefix(term, "^") {
		return matchCaretConstraint(v, term)
	}
	if strings.HasPrefix(term, "~") {
		return matchTildeConstraint(v, term)
	}
	if isRelationalOperator(term) {
		return matchRelationalConstraint(v, term)
	}
	if strings.HasPrefix(term, "==") || strings.HasPrefix(term, "=") {
		return matchEqualityConstraint(v, strings.TrimLeft(term, "="))
	}
	if strings.HasSuffix(term, ".*") || strings.HasSuffix(strings.ToLower(term), ".x") {
		return matchWildcard(v, term)
	}
	return matchEqualityConstraint(v, term)
}

// isWildcardTerm reports whether term represents a wildcard ("*", "x", "X", or empty).
func isWildcardTerm(term string) bool {
	return term == "" || term == "*" || strings.EqualFold(term, "x")
}

// isRelationalOperator reports whether term begins with a comparison operator (">=", ">", "<=", "<").
func isRelationalOperator(term string) bool {
	return strings.HasPrefix(term, ">=") || strings.HasPrefix(term, ">") || strings.HasPrefix(term, "<=") || strings.HasPrefix(term, "<")
}

// matchEqualityConstraint evaluates whether candidate version v is mathematically equal to term.
func matchEqualityConstraint(v *store.Version, term string) (bool, error) {
	base, err := parseConstraintVersion(term)
	if err != nil {
		return false, err
	}
	return v.Equal(base), nil
}

// matchRelationalConstraint evaluates comparison operators (">=", ">", "<=", "<") against version v.
func matchRelationalConstraint(v *store.Version, term string) (bool, error) {
	for _, op := range []string{">=", "<=", ">", "<"} {
		if strings.HasPrefix(term, op) {
			base, err := parseConstraintVersion(strings.TrimPrefix(term, op))
			if err != nil {
				return false, err
			}
			switch op {
			case ">=":
				return !v.LessThan(base), nil
			case "<=":
				return !v.GreaterThan(base), nil
			case ">":
				return v.GreaterThan(base), nil
			case "<":
				return v.LessThan(base), nil
			}
		}
	}
	return false, fmt.Errorf("invalid relational constraint term %q", term)
}

// matchCaretConstraint evaluates SemVer caret (^) compatibility preserving the leftmost non-zero digit.
func matchCaretConstraint(v *store.Version, term string) (bool, error) {
	constraintStr := strings.TrimPrefix(term, "^")
	base, err := parseConstraintVersion(constraintStr)
	if err != nil {
		return false, err
	}
	if v.LessThan(base) {
		return false, nil
	}
	if base.Major > 0 {
		return v.Major == base.Major, nil
	}
	return matchZeroMajorCaret(v, base, constraintStr), nil
}

// matchZeroMajorCaret handles caret range matching when base.Major is 0.
func matchZeroMajorCaret(v, base *store.Version, constraintStr string) bool {
	parts := getCoreVersionParts(constraintStr)
	if len(parts) == 1 {
		return v.Major == 0
	}
	if len(parts) == 2 || base.Minor > 0 {
		return v.Major == 0 && v.Minor == base.Minor
	}
	return v.Major == 0 && v.Minor == 0 && v.Patch == base.Patch
}

// matchTildeConstraint evaluates SemVer tilde (~) patch-level range constraints.
func matchTildeConstraint(v *store.Version, term string) (bool, error) {
	constraintStr := strings.TrimPrefix(term, "~")
	base, err := parseConstraintVersion(constraintStr)
	if err != nil {
		return false, err
	}
	if v.LessThan(base) {
		return false, nil
	}
	parts := getCoreVersionParts(constraintStr)
	if len(parts) == 1 {
		return v.Major == base.Major, nil
	}
	return v.Major == base.Major && v.Minor == base.Minor, nil
}

// getCoreVersionParts extracts the dot-separated numeric segments, stripping 'v' and suffixes.
func getCoreVersionParts(versionStr string) []string {
	s := strings.TrimSpace(versionStr)
	s = strings.TrimPrefix(s, "v")
	s = strings.TrimPrefix(s, "V")
	core := s
	if idx := strings.IndexAny(s, "-+"); idx != -1 {
		core = s[:idx]
	}
	return strings.Split(core, ".")
}

// matchWildcard evaluates wildcard prefix matching (e.g., "1.*", "1.2.*", "1.x", "1.2.x").
func matchWildcard(v *store.Version, term string) (bool, error) {
	clean := strings.TrimPrefix(term, "v")
	clean = strings.TrimPrefix(clean, "V")
	parts := strings.Split(clean, ".")
	wildcardIdx := -1
	for i, p := range parts {
		if p == "*" || strings.EqualFold(p, "x") {
			wildcardIdx = i
			break
		}
	}
	if wildcardIdx == 0 {
		return true, nil
	}
	if wildcardIdx == 1 {
		base, err := parseConstraintVersion(parts[0])
		if err != nil {
			return false, err
		}
		return v.Major == base.Major, nil
	}
	if wildcardIdx == 2 && len(parts) >= 2 {
		base, err := parseConstraintVersion(parts[0] + "." + parts[1])
		if err != nil {
			return false, err
		}
		return v.Major == base.Major && v.Minor == base.Minor, nil
	}
	return false, fmt.Errorf("unsupported wildcard format %q", term)
}

// normalizeConstraintVersion normalizes a user-specified constraint version string into a canonical
// 3-part SemVer 2.0.0 format (MAJOR.MINOR.PATCH[-PRERELEASE][+BUILD]) required by store.Parse.
//
// Specifically, it performs the following transformations:
//  1. Trims leading and trailing whitespace.
//  2. Strips any leading 'v' or 'V' release prefix (e.g., "v1.2" -> "1.2").
//  3. Isolates any pre-release or build metadata suffix beginning with '-' or '+' (e.g., "-alpha.1", "+build.42").
//  4. Expands partial numeric core versions into full 3-segment triples:
//     - 1-segment: "1"   -> "1.0.0"
//     - 2-segment: "1.2" -> "1.2.0"
//     - 3-segment: "1.2.3" -> preserved as "1.2.3"
//  5. Re-attaches the pre-release or build metadata suffix to the padded core version (e.g., "v1.2-rc1" -> "1.2.0-rc1").
//
// Returns an empty string if the input string is empty or contains only whitespace.
func normalizeConstraintVersion(s string) string {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "v")
	s = strings.TrimPrefix(s, "V")
	if s == "" {
		return ""
	}
	core := s
	rest := ""
	if idx := strings.IndexAny(s, "-+"); idx != -1 {
		core = s[:idx]
		rest = s[idx:]
	}
	parts := strings.Split(core, ".")
	switch len(parts) {
	case 1:
		core = parts[0] + ".0.0"
	case 2:
		core = parts[0] + "." + parts[1] + ".0"
	}
	return core + rest
}

// parseConstraintVersion normalizes and parses a constraint version string into a *store.Version.
func parseConstraintVersion(s string) (*store.Version, error) {
	norm := normalizeConstraintVersion(s)
	if norm == "" {
		return nil, fmt.Errorf("empty constraint version string")
	}
	return store.Parse(norm)
}
