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

// Package jobsetpolicy enforces the XMC Non-Restarting JobSets contract via go/analysis.
package jobsetpolicy

import (
	"go/ast"
	"go/constant"
	"go/token"
	"go/types"
	"strings"

	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/go/analysis/passes/inspect"
	"golang.org/x/tools/go/ast/inspector"
)

const (
	jobSetPkgPath  = "sigs.k8s.io/jobset/api/jobset/v1alpha2"
	coreV1PkgPath  = "k8s.io/api/core/v1"
	maxRestartsErr = "Non-Restarting JobSets contract: FailurePolicy.MaxRestarts must be 0, got %s"
	podRestartErr  = "Non-Restarting JobSets contract: PodSpec.RestartPolicy must not be Always"
)

// Analyzer flags non-zero FailurePolicy.MaxRestarts and PodSpec.RestartPolicy == Always in production Go code.
var Analyzer = &analysis.Analyzer{
	Name:     "jobsetpolicy",
	Doc:      "enforces the XMC Non-Restarting JobSets architectural contract (MaxRestarts == 0, PodSpec.RestartPolicy != Always)",
	Requires: []*analysis.Analyzer{inspect.Analyzer},
	Run:      run,
}

func run(pass *analysis.Pass) (any, error) {
	insp, _ := pass.ResultOf[inspect.Analyzer].(*inspector.Inspector)
	if insp == nil {
		return nil, nil
	}

	nodeFilter := []ast.Node{
		(*ast.CompositeLit)(nil),
		(*ast.AssignStmt)(nil),
	}
	insp.Preorder(nodeFilter, func(n ast.Node) {
		filename := pass.Fset.Position(n.Pos()).Filename
		if strings.HasSuffix(filename, "_test.go") {
			return
		}
		switch node := n.(type) {
		case *ast.CompositeLit:
			checkCompositeLit(pass, node)
		case *ast.AssignStmt:
			checkAssignStmt(pass, node)
		}
	})
	return nil, nil
}

func checkCompositeLit(pass *analysis.Pass, lit *ast.CompositeLit) {
	t := derefType(pass.TypesInfo.TypeOf(lit))
	switch {
	case isNamedType(t, jobSetPkgPath, "FailurePolicy"):
		if expr, ok := keyedFieldExpr(lit, "MaxRestarts"); ok {
			checkMaxRestartsExpr(pass, expr)
		}
	case isNamedType(t, coreV1PkgPath, "PodSpec"):
		if expr, ok := keyedFieldExpr(lit, "RestartPolicy"); ok {
			checkPodRestartPolicyExpr(pass, expr)
		}
	}
}

func checkAssignStmt(pass *analysis.Pass, stmt *ast.AssignStmt) {
	if stmt.Tok != token.ASSIGN {
		return
	}
	for i, lhs := range stmt.Lhs {
		if i >= len(stmt.Rhs) {
			continue
		}
		sel, ok := lhs.(*ast.SelectorExpr)
		if !ok {
			continue
		}
		recvType := derefType(pass.TypesInfo.TypeOf(sel.X))
		switch {
		case sel.Sel.Name == "MaxRestarts" && isNamedType(recvType, jobSetPkgPath, "FailurePolicy"):
			checkMaxRestartsExpr(pass, stmt.Rhs[i])
		case sel.Sel.Name == "RestartPolicy" && isNamedType(recvType, coreV1PkgPath, "PodSpec"):
			checkPodRestartPolicyExpr(pass, stmt.Rhs[i])
		}
	}
}

func checkMaxRestartsExpr(pass *analysis.Pass, expr ast.Expr) {
	tv, ok := pass.TypesInfo.Types[expr]
	if !ok || tv.Value == nil || tv.Value.Kind() != constant.Int {
		return
	}
	val, valid := constant.Int64Val(tv.Value)
	if !valid || val != 0 {
		pass.Reportf(expr.Pos(), maxRestartsErr, tv.Value.ExactString())
	}
}

func checkPodRestartPolicyExpr(pass *analysis.Pass, expr ast.Expr) {
	if isAlwaysRestartConstant(pass.TypesInfo, expr) {
		pass.Reportf(expr.Pos(), "%s", podRestartErr)
	}
}

func isAlwaysRestartConstant(info *types.Info, expr ast.Expr) bool {
	if tv, ok := info.Types[expr]; ok && tv.Value != nil && tv.Value.Kind() == constant.String {
		return constant.StringVal(tv.Value) == "Always"
	}
	if sel, ok := expr.(*ast.SelectorExpr); ok && sel.Sel.Name == "RestartPolicyAlways" {
		if obj := info.ObjectOf(sel.Sel); obj != nil && obj.Pkg() != nil {
			return obj.Pkg().Path() == coreV1PkgPath
		}
	}
	return false
}

func keyedFieldExpr(lit *ast.CompositeLit, fieldName string) (ast.Expr, bool) {
	for _, elt := range lit.Elts {
		kv, ok := elt.(*ast.KeyValueExpr)
		if !ok {
			continue
		}
		ident, ok := kv.Key.(*ast.Ident)
		if ok && ident.Name == fieldName {
			return kv.Value, true
		}
	}
	return nil, false
}

func derefType(t types.Type) types.Type {
	for {
		ptr, ok := t.(*types.Pointer)
		if !ok {
			return t
		}
		t = ptr.Elem()
	}
}

func isNamedType(t types.Type, pkgPath, typeName string) bool {
	named, ok := t.(*types.Named)
	if !ok {
		return false
	}
	obj := named.Obj()
	return obj != nil && obj.Pkg() != nil && obj.Pkg().Path() == pkgPath && obj.Name() == typeName
}
