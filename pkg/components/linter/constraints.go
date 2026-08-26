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

func isOperator(s string) bool {
	switch s {
	case "==", ">=", "<=", ">", "<", "^", "~", "=":
		return true
	}
	return false
}

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

func isWildcardTerm(term string) bool {
	return term == "" || term == "*" || strings.EqualFold(term, "x")
}

func isRelationalOperator(term string) bool {
	return strings.HasPrefix(term, ">=") || strings.HasPrefix(term, ">") || strings.HasPrefix(term, "<=") || strings.HasPrefix(term, "<")
}

func matchEqualityConstraint(v *store.Version, term string) (bool, error) {
	base, err := parseConstraintVersion(term)
	if err != nil {
		return false, err
	}
	return v.Equal(base), nil
}

func matchRelationalConstraint(v *store.Version, term string) (bool, error) {
	if strings.HasPrefix(term, ">=") {
		base, err := parseConstraintVersion(strings.TrimPrefix(term, ">="))
		if err != nil {
			return false, err
		}
		return !v.LessThan(base), nil
	}
	if strings.HasPrefix(term, ">") {
		base, err := parseConstraintVersion(strings.TrimPrefix(term, ">"))
		if err != nil {
			return false, err
		}
		return v.GreaterThan(base), nil
	}
	if strings.HasPrefix(term, "<=") {
		base, err := parseConstraintVersion(strings.TrimPrefix(term, "<="))
		if err != nil {
			return false, err
		}
		return !v.GreaterThan(base), nil
	}
	base, err := parseConstraintVersion(strings.TrimPrefix(term, "<"))
	if err != nil {
		return false, err
	}
	return v.LessThan(base), nil
}

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

func matchTildeConstraint(v *store.Version, term string) (bool, error) {
	constraintStr := strings.TrimPrefix(term, "~")
	base, err := parseConstraintVersion(constraintStr)
	if err != nil {
		return false, err
	}
	if v.LessThan(base) {
		return false, nil
	}

	// Determine original precision of constraint string to handle `~1` vs `~1.2` correctly.
	parts := getCoreVersionParts(constraintStr)
	if len(parts) == 1 {
		return v.Major == base.Major, nil
	}
	return v.Major == base.Major && v.Minor == base.Minor, nil
}

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

func parseConstraintVersion(s string) (*store.Version, error) {
	norm := normalizeConstraintVersion(s)
	if norm == "" {
		return nil, fmt.Errorf("empty constraint version string")
	}
	return store.Parse(norm)
}
