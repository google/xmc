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

// Package main implements an AST-based static analyzer for Go test determinism.
// It parses Go syntax trees directly without requiring full Go typechecking,
// allowing ultra-fast execution (<50ms) in local pre-commit hooks and CI environments
// without depending on Bazel-generated code.
//
// It checks Go test files for:
// 1. os.Setenv usage (must use t.Setenv to prevent test state leakage).
// 2. Arbitrary second-level time.Sleep calls (must use short TTLs, synctest, or polling).
package main

import (
	"flag"
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
)

// excludedDirs defines directory names that are excluded from test file scanning.
var excludedDirs = map[string]bool{
	"vendor":         true,
	"third_party":    true,
	".git":           true,
	"node_modules":   true,
	"bazel-bin":      true,
	"bazel-out":      true,
	"bazel-testlogs": true,
	"bazel-xmc":      true,
}

// normalizeRelativePath normalizes path relative to baseDir.
// If baseDir is empty or if path resolves outside baseDir, it returns the cleaned path.
// This ensures path filtering functions reliably when repositories are cloned
// under parent directories containing excluded directory names (e.g. /home/user/vendor/xmc).
func normalizeRelativePath(path, baseDir string) string {
	cleaned := filepath.Clean(path)
	if baseDir == "" {
		return cleaned
	}
	abs, err := filepath.Abs(cleaned)
	if err != nil {
		return cleaned
	}
	baseAbs, err := filepath.Abs(baseDir)
	if err != nil {
		return cleaned
	}
	rel, err := filepath.Rel(baseAbs, abs)
	if err != nil || strings.HasPrefix(rel, "..") {
		return cleaned
	}
	return rel
}

// isExcludedFrom determines whether a file or directory path should be excluded
// from determinism scanning, evaluated relative to baseDir.
// It ignores directory components matching excludedDirs or starting with "bazel-",
// while preserving test files whose file basenames happen to start with "bazel-".
func isExcludedFrom(path, baseDir string) bool {
	cleaned := normalizeRelativePath(path, baseDir)
	parts := strings.Split(cleaned, string(filepath.Separator))
	for i, part := range parts {
		isLast := i == len(parts)-1
		if isLast && strings.HasSuffix(part, ".go") {
			continue
		}
		if excludedDirs[part] || strings.HasPrefix(part, "bazel-") {
			return true
		}
	}
	return false
}

// isExcluded determines whether a file or directory path should be excluded,
// evaluated relative to the current working directory.
func isExcluded(path string) bool {
	cwd, _ := os.Getwd()
	return isExcludedFrom(path, cwd)
}

// isTimeUnit returns true if the AST expression represents a second-or-greater time unit:
// time.Second, time.Minute, or time.Hour.
func isTimeUnit(expr ast.Expr) bool {
	sel, ok := expr.(*ast.SelectorExpr)
	if !ok {
		return false
	}
	pkg, ok := sel.X.(*ast.Ident)
	if !ok || pkg.Name != "time" {
		return false
	}
	switch sel.Sel.Name {
	case "Second", "Minute", "Hour":
		return true
	default:
		return false
	}
}

// isLongSleepExpr inspects an AST expression passed to time.Sleep and returns true
// if it evaluates to a second-level or greater sleep duration.
// It recursively analyzes arithmetic expressions (MUL, ADD, SUB, QUO), parenthesized
// expressions, and type conversions (e.g. time.Duration(...)).
// For division (QUO), only the numerator (e.X) is evaluated to prevent false positives
// when dividing a duration by time units.
func isLongSleepExpr(expr ast.Expr) bool {
	switch e := expr.(type) {
	case *ast.SelectorExpr:
		return isTimeUnit(e)
	case *ast.BinaryExpr:
		if e.Op == token.MUL || e.Op == token.ADD || e.Op == token.SUB {
			return isLongSleepExpr(e.X) || isLongSleepExpr(e.Y)
		}
		if e.Op == token.QUO {
			return isLongSleepExpr(e.X)
		}
	case *ast.ParenExpr:
		return isLongSleepExpr(e.X)
	case *ast.CallExpr:
		for _, arg := range e.Args {
			if isLongSleepExpr(arg) {
				return true
			}
		}
	}
	return false
}

// isSuppressed returns true if any line from startLine-1 to endLine has an active
// nolint directive recorded in nolintLines.
func isSuppressed(startLine, endLine int, nolintLines map[int]bool) bool {
	for l := startLine - 1; l <= endLine; l++ {
		if nolintLines[l] {
			return true
		}
	}
	return false
}

// extractSuppressionLines extracts line numbers where nolint comments appear in the AST.
// "nolint:determinism" suppresses both os.Setenv and time.Sleep checks.
// "nolint:sleep" specifically suppresses time.Sleep checks.
func extractSuppressionLines(node *ast.File, fset *token.FileSet) (map[int]bool, map[int]bool) {
	nolintDetLines := make(map[int]bool)
	nolintSleepLines := make(map[int]bool)
	for _, cg := range node.Comments {
		for _, c := range cg.List {
			startLine := fset.Position(c.Pos()).Line
			endLine := fset.Position(c.End()).Line
			if strings.Contains(c.Text, "nolint:determinism") {
				for l := startLine; l <= endLine; l++ {
					nolintDetLines[l] = true
					nolintSleepLines[l] = true
				}
			}
			if strings.Contains(c.Text, "nolint:sleep") {
				for l := startLine; l <= endLine; l++ {
					nolintSleepLines[l] = true
				}
			}
		}
	}
	return nolintDetLines, nolintSleepLines
}

// checkSetenvCall inspects a function call AST node and returns a formatted violation string
// if it represents an unsuppressed os.Setenv call in a test file.
func checkSetenvCall(filename string, call *ast.CallExpr, sel *ast.SelectorExpr, fset *token.FileSet, suppressed bool) (string, bool) {
	pkgIdent, ok := sel.X.(*ast.Ident)
	if !ok || pkgIdent.Name != "os" || sel.Sel.Name != "Setenv" || suppressed {
		return "", false
	}
	line := fset.Position(call.Pos()).Line
	return fmt.Sprintf("%s:%d: os.Setenv used in test. Use t.Setenv(...) to guarantee cleanup and prevent environment pollution.", filename, line), true
}

// checkSleepCall inspects a function call AST node and returns a formatted violation string
// if it represents an unsuppressed arbitrary second-level time.Sleep call in a test file.
func checkSleepCall(filename string, call *ast.CallExpr, sel *ast.SelectorExpr, fset *token.FileSet, suppressed bool) (string, bool) {
	pkgIdent, ok := sel.X.(*ast.Ident)
	if !ok || pkgIdent.Name != "time" || sel.Sel.Name != "Sleep" || suppressed {
		return "", false
	}
	if len(call.Args) == 0 || !isLongSleepExpr(call.Args[0]) {
		return "", false
	}
	line := fset.Position(call.Pos()).Line
	return fmt.Sprintf("%s:%d: arbitrary second-level time.Sleep in test. Inject millisecond TTLs, use synctest, or use wait.Poll.", filename, line), true
}

// CheckSource parses Go source code from src and returns all determinism violations found.
// src can be a string, []byte, io.Reader, or nil (reading from filename).
func CheckSource(filename string, src any) ([]string, error) {
	fset := token.NewFileSet()
	node, err := parser.ParseFile(fset, filename, src, parser.ParseComments)
	if err != nil {
		return nil, fmt.Errorf("failed to parse %s: %w", filename, err)
	}

	detLines, sleepLines := extractSuppressionLines(node, fset)
	var violations []string

	ast.Inspect(node, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		sel, ok := call.Fun.(*ast.SelectorExpr)
		if !ok {
			return true
		}

		start := fset.Position(call.Pos()).Line
		end := fset.Position(call.End()).Line

		if v, flagged := checkSetenvCall(filename, call, sel, fset, isSuppressed(start, end, detLines)); flagged {
			violations = append(violations, v)
		}
		if v, flagged := checkSleepCall(filename, call, sel, fset, isSuppressed(start, end, sleepLines)); flagged {
			violations = append(violations, v)
		}
		return true
	})

	return violations, nil
}

// CheckFile reads a Go source file from disk and returns any determinism violations found.
func CheckFile(filePath string) ([]string, error) {
	return CheckSource(filePath, nil)
}

// findTestFiles walks a directory tree rooted at root and returns all non-excluded *_test.go files.
func findTestFiles(root string) ([]string, error) {
	var files []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if isExcluded(path) {
				return filepath.SkipDir
			}
			return nil
		}
		if strings.HasSuffix(d.Name(), "_test.go") && !isExcluded(path) {
			files = append(files, path)
		}
		return nil
	})
	return files, err
}

// collectTargetFiles resolves CLI arguments into a flat list of test files to inspect.
// If no arguments are provided, it discovers all test files across the repository.
func collectTargetFiles(args []string) ([]string, error) {
	if len(args) == 0 {
		return findTestFiles(".")
	}
	var files []string
	for _, arg := range args {
		if isExcluded(arg) {
			continue
		}
		info, err := os.Stat(arg)
		if err != nil {
			return nil, fmt.Errorf("failed to stat %s: %w", arg, err)
		}
		if info.IsDir() {
			discovered, err := findTestFiles(arg)
			if err != nil {
				return nil, err
			}
			files = append(files, discovered...)
			continue
		}
		if strings.HasSuffix(info.Name(), "_test.go") {
			files = append(files, arg)
		}
	}
	return files, nil
}

func main() {
	flag.Parse()
	filesToCheck, err := collectTargetFiles(flag.Args())
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error discovering test files: %v\n", err)
		os.Exit(1)
	}

	var allViolations []string
	for _, f := range filesToCheck {
		violations, err := CheckFile(f)
		if err != nil {
			fmt.Fprintf(os.Stderr, "Error: %v\n", err)
			os.Exit(1)
		}
		allViolations = append(allViolations, violations...)
	}

	if len(allViolations) > 0 {
		fmt.Println("❌ Go test determinism violations found:")
		for _, v := range allViolations {
			fmt.Printf("  %s\n", v)
		}
		os.Exit(1)
	}

	fmt.Printf("✅ Go test determinism check passed (%d files checked).\n", len(filesToCheck))
}
