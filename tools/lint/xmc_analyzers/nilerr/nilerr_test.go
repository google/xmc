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

package nilerr

import (
	"go/ast"
	"go/parser"
	"go/token"
	"go/types"
	"strings"
	"testing"

	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/go/analysis/passes/inspect"
)

type stubImporter struct{}

func (*stubImporter) Import(path string) (*types.Package, error) {
	pkg := types.NewPackage(path, path)
	errType := types.Universe.Lookup("error").Type()
	switch path {
	case "errors":
		pkg.Scope().Insert(types.NewFunc(token.NoPos, pkg, "Is", types.NewSignatureType(nil, nil, nil,
			types.NewTuple(types.NewParam(token.NoPos, pkg, "err", errType), types.NewParam(token.NoPos, pkg, "target", errType)),
			types.NewTuple(types.NewParam(token.NoPos, pkg, "", types.Typ[types.Bool])), false)))
		pkg.Scope().Insert(types.NewFunc(token.NoPos, pkg, "New", types.NewSignatureType(nil, nil, nil,
			types.NewTuple(types.NewParam(token.NoPos, pkg, "text", types.Typ[types.String])),
			types.NewTuple(types.NewParam(token.NoPos, pkg, "", errType)), false)))
	case "logger":
		pkg.Scope().Insert(types.NewFunc(token.NoPos, pkg, "Warn", types.NewSignatureType(nil, nil, nil,
			types.NewTuple(types.NewParam(token.NoPos, pkg, "msg", types.Typ[types.String]), types.NewParam(token.NoPos, pkg, "err", errType)),
			nil, false)))
	case "context":
		ctxIface := types.NewInterfaceType([]*types.Func{
			types.NewFunc(token.NoPos, pkg, "Err", types.NewSignatureType(nil, nil, nil, nil, types.NewTuple(types.NewParam(token.NoPos, pkg, "", errType)), false)),
		}, nil)
		ctxIface.Complete()
		ctxObj := types.NewTypeName(token.NoPos, pkg, "Context", nil)
		types.NewNamed(ctxObj, ctxIface, nil)
		pkg.Scope().Insert(ctxObj)
	}
	pkg.MarkComplete()
	return pkg, nil
}

func runNilErrOnSource(t *testing.T, src string) []analysis.Diagnostic {
	t.Helper()
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "sample.go", src, parser.ParseComments)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	info := &types.Info{
		Types:  make(map[ast.Expr]types.TypeAndValue),
		Defs:   make(map[*ast.Ident]types.Object),
		Uses:   make(map[*ast.Ident]types.Object),
		Scopes: make(map[ast.Node]*types.Scope),
	}
	cfg := types.Config{Importer: &stubImporter{}}
	pkg, err := cfg.Check("sample", fset, []*ast.File{file}, info)
	if err != nil {
		t.Fatalf("typecheck: %v", err)
	}
	inspectPass := &analysis.Pass{
		Analyzer:  inspect.Analyzer,
		Fset:      fset,
		Files:     []*ast.File{file},
		Pkg:       pkg,
		TypesInfo: info,
	}
	inspRes, err := inspect.Analyzer.Run(inspectPass)
	if err != nil {
		t.Fatalf("inspect: %v", err)
	}
	var diags []analysis.Diagnostic
	pass := &analysis.Pass{
		Analyzer:  Analyzer,
		Fset:      fset,
		Files:     []*ast.File{file},
		Pkg:       pkg,
		TypesInfo: info,
		ResultOf:  map[*analysis.Analyzer]any{inspect.Analyzer: inspRes},
		Report:    func(d analysis.Diagnostic) { diags = append(diags, d) },
	}
	if _, err := Analyzer.Run(pass); err != nil {
		t.Fatalf("analyzer: %v", err)
	}
	return diags
}

func TestNilErrAnalyzer(t *testing.T) {
	tests := []struct {
		name     string
		src      string
		wantHits int
	}{
		{
			name: "flags_unhandled_nil_error_return",
			src: `package sample
import "errors"
func f() (int, error) {
	err := errors.New("boom")
	if err != nil {
		return 0, nil
	}
	return 1, nil
}`,
			wantHits: 1,
		},
		{
			name: "flags_blank_nilerr_directive_without_reason",
			src: `package sample
import "errors"
func f() (int, error) {
	if err := errors.New("boom"); err != nil {
		return 0, nil //nilerr:
	}
	return 1, nil
}`,
			wantHits: 1,
		},
		{
			name: "flags_unhandled_false_and_nil_error_return",
			src: `package sample
import "errors"
func f() (bool, error) {
	err := errors.New("boom")
	if err != nil {
		return false, nil
	}
	return true, nil
}`,
			wantHits: 1,
		},
		{
			name: "flags_nil_return_after_unlogged_err_error_call",
			src: `package sample
import "errors"
func f() error {
	err := errors.New("boom")
	if err != nil {
		_ = err.Error()
		return nil
	}
	return nil
}`,
			wantHits: 1,
		},
		{
			name: "allows_intentional_patterns_and_reasoned_suppressions",
			src: `package sample
import (
	"context"
	"errors"
	"logger"
)
type ctxAlias = context.Context
var errNotFound = errors.New("not found")
func handleErr(err error) error { return nil }
func f(ctx ctxAlias, cond bool) (string, bool, error) {
	if err := errors.New("is_not_found"); err != nil {
		if errors.Is(err, errNotFound) {
			return "", false, nil
		}
		return "", false, err
	}
	if err := errors.New("ctx_canceled"); err != nil {
		if (ctx).Err() != nil {
			return "", false, nil
		}
		return "", false, err
	}
	if err := errors.New("fallback"); err != nil {
		return "msg:" + err.Error(), false, nil
	}
	if err := errors.New("handled"); err != nil {
		if handleErr := handleErr(err); handleErr != nil {
			return "", false, handleErr
		}
		return "", true, nil
	}
	if err := errors.New("warn"); err != nil {
		if cond {
			logger.Warn("fallback", err)
		}
		return "", true, nil
	}
	if err := errors.New("suppressed_preceding_line"); err != nil {
		// nilerr: preceding line suppression comment
		return "literal", true, nil
	}
	if err := errors.New("suppressed_same_line"); err != nil {
		return "literal", true, nil // nilerr: treat missing path as literal
	}
	if err := errors.New("suppressed_block"); err != nil {
		return "literal", true, nil /* nilerr: block suppression syntax */
	}
	if err := errors.New("retry"); err != nil {
		err = nil
		return "", true, nil
	}
	return "ok", true, nil
}`,
			wantHits: 0,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			diags := runNilErrOnSource(t, tc.src)
			if len(diags) != tc.wantHits {
				t.Fatalf("got %d diagnostics %v, want %d", len(diags), diags, tc.wantHits)
			}
			for _, d := range diags {
				if !strings.Contains(d.Message, "returning nil error") {
					t.Errorf("unexpected diagnostic: %q", d.Message)
				}
			}
		})
	}
}
