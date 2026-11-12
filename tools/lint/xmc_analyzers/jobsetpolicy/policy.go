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
		(*ast.IncDecStmt)(nil),
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
		case *ast.IncDecStmt:
			checkIncDecStmt(pass, node)
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

func checkIncDecStmt(pass *analysis.Pass, stmt *ast.IncDecStmt) {
	sel, ok := ast.Unparen(stmt.X).(*ast.SelectorExpr)
	if !ok {
		return
	}
	recvType := derefType(pass.TypesInfo.TypeOf(sel.X))
	if sel.Sel.Name == "MaxRestarts" && isNamedType(recvType, jobSetPkgPath, "FailurePolicy") {
		pass.Reportf(stmt.Pos(), maxRestartsErr, stmt.Tok.String())
	}
}

func checkAssignTarget(pass *analysis.Pass, tok token.Token, pos token.Pos, lhs, rhs ast.Expr) {
	sel, ok := ast.Unparen(lhs).(*ast.SelectorExpr)
	if !ok {
		return
	}
	recvType := derefType(pass.TypesInfo.TypeOf(sel.X))
	if sel.Sel.Name == "MaxRestarts" && isNamedType(recvType, jobSetPkgPath, "FailurePolicy") {
		if tok != token.ASSIGN {
			pass.Reportf(pos, maxRestartsErr, tok.String())
			return
		}
		if rhs != nil {
			checkMaxRestartsExpr(pass, rhs)
		}
		return
	}
	if sel.Sel.Name == "RestartPolicy" && isNamedType(recvType, coreV1PkgPath, "PodSpec") {
		if tok != token.ASSIGN {
			pass.Reportf(pos, "%s", podRestartErr)
			return
		}
		if rhs != nil {
			checkPodRestartPolicyExpr(pass, rhs)
		}
	}
}

func checkAssignStmt(pass *analysis.Pass, stmt *ast.AssignStmt) {
	for i, lhs := range stmt.Lhs {
		var rhs ast.Expr
		if i < len(stmt.Rhs) {
			rhs = stmt.Rhs[i]
		}
		checkAssignTarget(pass, stmt.Tok, stmt.Pos(), lhs, rhs)
	}
}

func checkMaxRestartsExpr(pass *analysis.Pass, expr ast.Expr) {
	expr = ast.Unparen(expr)
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
	expr = ast.Unparen(expr)
	if isAlwaysRestartConstant(pass.TypesInfo, expr) {
		pass.Reportf(expr.Pos(), "%s", podRestartErr)
	}
}

func isAlwaysRestartConstant(info *types.Info, expr ast.Expr) bool {
	expr = ast.Unparen(expr)
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
		if t == nil {
			return nil
		}
		t = types.Unalias(t)
		ptr, ok := t.(*types.Pointer)
		if !ok {
			return t
		}
		t = ptr.Elem()
	}
}

func isNamedType(t types.Type, pkgPath, typeName string) bool {
	if t == nil {
		return false
	}
	named, ok := types.Unalias(t).(*types.Named)
	if !ok {
		return false
	}
	obj := named.Obj()
	return obj != nil && obj.Pkg() != nil && obj.Pkg().Path() == pkgPath && obj.Name() == typeName
}
