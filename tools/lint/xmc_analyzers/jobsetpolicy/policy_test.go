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

package jobsetpolicy

import (
	"go/ast"
	"go/constant"
	"go/parser"
	"go/token"
	"go/types"
	"strings"
	"testing"

	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/go/analysis/passes/inspect"
)

type stubImporter struct {
	pkgs map[string]*types.Package
}

func (s *stubImporter) Import(path string) (*types.Package, error) {
	if pkg, ok := s.pkgs[path]; ok {
		return pkg, nil
	}
	return types.NewPackage(path, ""), nil
}

func buildStubPackages() map[string]*types.Package {
	jobsetPkg := types.NewPackage(jobSetPkgPath, "jobsetv1alpha2")
	fpObj := types.NewTypeName(token.NoPos, jobsetPkg, "FailurePolicy", nil)
	types.NewNamed(fpObj, types.NewStruct([]*types.Var{
		types.NewField(token.NoPos, jobsetPkg, "MaxRestarts", types.Typ[types.Int32], false),
	}, nil), nil)
	jobsetPkg.Scope().Insert(fpObj)
	jobsetPkg.MarkComplete()

	corePkg := types.NewPackage(coreV1PkgPath, "corev1")
	rpObj := types.NewTypeName(token.NoPos, corePkg, "RestartPolicy", nil)
	rpNamed := types.NewNamed(rpObj, types.Typ[types.String], nil)
	corePkg.Scope().Insert(rpObj)

	crpObj := types.NewTypeName(token.NoPos, corePkg, "ContainerRestartPolicy", nil)
	crpNamed := types.NewNamed(crpObj, types.Typ[types.String], nil)
	corePkg.Scope().Insert(crpObj)

	corePkg.Scope().Insert(types.NewConst(token.NoPos, corePkg, "RestartPolicyNever", rpNamed, constant.MakeString("Never")))
	corePkg.Scope().Insert(types.NewConst(token.NoPos, corePkg, "RestartPolicyOnFailure", rpNamed, constant.MakeString("OnFailure")))
	corePkg.Scope().Insert(types.NewConst(token.NoPos, corePkg, "RestartPolicyAlways", rpNamed, constant.MakeString("Always")))
	corePkg.Scope().Insert(types.NewConst(token.NoPos, corePkg, "ContainerRestartPolicyAlways", crpNamed, constant.MakeString("Always")))

	psObj := types.NewTypeName(token.NoPos, corePkg, "PodSpec", nil)
	types.NewNamed(psObj, types.NewStruct([]*types.Var{
		types.NewField(token.NoPos, corePkg, "RestartPolicy", rpNamed, false),
	}, nil), nil)
	corePkg.Scope().Insert(psObj)

	cObj := types.NewTypeName(token.NoPos, corePkg, "Container", nil)
	types.NewNamed(cObj, types.NewStruct([]*types.Var{
		types.NewField(token.NoPos, corePkg, "RestartPolicy", types.NewPointer(crpNamed), false),
	}, nil), nil)
	corePkg.Scope().Insert(cObj)
	corePkg.MarkComplete()

	return map[string]*types.Package{
		jobSetPkgPath: jobsetPkg,
		coreV1PkgPath: corePkg,
	}
}

func runAnalyzerOnSource(t *testing.T, filename, src string) []analysis.Diagnostic {
	t.Helper()
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, filename, src, parser.ParseComments)
	if err != nil {
		t.Fatalf("parse %s: %v", filename, err)
	}
	info := &types.Info{
		Types:  make(map[ast.Expr]types.TypeAndValue),
		Defs:   make(map[*ast.Ident]types.Object),
		Uses:   make(map[*ast.Ident]types.Object),
		Scopes: make(map[ast.Node]*types.Scope),
	}
	cfg := types.Config{Importer: &stubImporter{pkgs: buildStubPackages()}}
	pkg, err := cfg.Check("sample", fset, []*ast.File{file}, info)
	if err != nil {
		t.Fatalf("typecheck %s: %v", filename, err)
	}

	var diags []analysis.Diagnostic
	inspectPass := &analysis.Pass{
		Analyzer:  inspect.Analyzer,
		Fset:      fset,
		Files:     []*ast.File{file},
		Pkg:       pkg,
		TypesInfo: info,
	}
	inspRes, err := inspect.Analyzer.Run(inspectPass)
	if err != nil {
		t.Fatalf("inspect pass: %v", err)
	}
	pass := &analysis.Pass{
		Analyzer:  Analyzer,
		Fset:      fset,
		Files:     []*ast.File{file},
		Pkg:       pkg,
		TypesInfo: info,
		ResultOf:  map[*analysis.Analyzer]any{inspect.Analyzer: inspRes},
		Report: func(d analysis.Diagnostic) {
			diags = append(diags, d)
		},
	}
	if _, err := Analyzer.Run(pass); err != nil {
		t.Fatalf("analyzer run: %v", err)
	}
	return diags
}

func TestJobSetPolicyAnalyzer(t *testing.T) {
	tests := []struct {
		name     string
		filename string
		src      string
		wantMsgs []string
	}{
		{
			name:     "valid_zero_max_restarts_and_never_restart",
			filename: "builder.go",
			src: `package sample
import (
	jobsetv1alpha2 "sigs.k8s.io/jobset/api/jobset/v1alpha2"
	corev1 "k8s.io/api/core/v1"
)
func build() {
	_ = jobsetv1alpha2.FailurePolicy{MaxRestarts: 0}
	_ = corev1.PodSpec{RestartPolicy: corev1.RestartPolicyNever}
	_ = corev1.PodSpec{RestartPolicy: corev1.RestartPolicyOnFailure}
	p := corev1.ContainerRestartPolicyAlways
	_ = corev1.Container{RestartPolicy: &p}
}`,
		},
		{
			name:     "non_zero_max_restarts_in_literal_and_assign",
			filename: "builder.go",
			src: `package sample
import jobsetv1alpha2 "sigs.k8s.io/jobset/api/jobset/v1alpha2"
func build() {
	fp := &jobsetv1alpha2.FailurePolicy{MaxRestarts: 3}
	fp.MaxRestarts = 2
}`,
			wantMsgs: []string{"MaxRestarts must be 0, got 3", "MaxRestarts must be 0, got 2"},
		},
		{
			name:     "pod_spec_restart_policy_always_in_literal_and_assign",
			filename: "builder.go",
			src: `package sample
import corev1 "k8s.io/api/core/v1"
func build() {
	ps := &corev1.PodSpec{RestartPolicy: corev1.RestartPolicyAlways}
	ps.RestartPolicy = "Always"
}`,
			wantMsgs: []string{"PodSpec.RestartPolicy must not be Always", "PodSpec.RestartPolicy must not be Always"},
		},
		{
			name:     "parenthesized_expressions_compound_incdec_and_type_aliases",
			filename: "builder.go",
			src: `package sample
import (
	jobsetv1alpha2 "sigs.k8s.io/jobset/api/jobset/v1alpha2"
	corev1 "k8s.io/api/core/v1"
)
type fpAlias = jobsetv1alpha2.FailurePolicy
type psAlias = corev1.PodSpec
const badMax = 2
func build() {
	fp := &fpAlias{MaxRestarts: (1)}
	fp.MaxRestarts++
	fp.MaxRestarts--
	fp.MaxRestarts += 1
	(fp.MaxRestarts) = 2
	(*fp).MaxRestarts = badMax
	_ = []*jobsetv1alpha2.FailurePolicy{{MaxRestarts: 4}}
	ps := &psAlias{RestartPolicy: (corev1.RestartPolicyAlways)}
	ps.RestartPolicy = corev1.RestartPolicy("Always")
	ps.RestartPolicy += ""
}`,
			wantMsgs: []string{
				"MaxRestarts must be 0, got 1",
				"MaxRestarts must be 0, got ++",
				"MaxRestarts must be 0, got --",
				"MaxRestarts must be 0, got +=",
				"MaxRestarts must be 0, got 2",
				"MaxRestarts must be 0, got 2",
				"MaxRestarts must be 0, got 4",
				"PodSpec.RestartPolicy must not be Always",
				"PodSpec.RestartPolicy must not be Always",
				"PodSpec.RestartPolicy must not be Always",
			},
		},
		{
			name:     "test_files_are_exempt",
			filename: "builder_test.go",
			src: `package sample
import (
	jobsetv1alpha2 "sigs.k8s.io/jobset/api/jobset/v1alpha2"
	corev1 "k8s.io/api/core/v1"
)
func testOnly() {
	_ = jobsetv1alpha2.FailurePolicy{MaxRestarts: 5}
	_ = corev1.PodSpec{RestartPolicy: corev1.RestartPolicyAlways}
}`,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			diags := runAnalyzerOnSource(t, tc.filename, tc.src)
			if len(diags) != len(tc.wantMsgs) {
				t.Fatalf("got %d diagnostics %v, want %d %v", len(diags), diags, len(tc.wantMsgs), tc.wantMsgs)
			}
			for i, want := range tc.wantMsgs {
				if !strings.Contains(diags[i].Message, want) {
					t.Errorf("diag[%d] = %q, want substring %q", i, diags[i].Message, want)
				}
			}
		})
	}
}
