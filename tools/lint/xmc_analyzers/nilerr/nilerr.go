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

// Package nilerr detects accidental nil error returns inside non-nil error branches.
package nilerr

import (
	"go/ast"
	"go/token"
	"go/types"
	"strings"

	"golang.org/x/tools/go/analysis"
	"golang.org/x/tools/go/analysis/passes/inspect"
	"golang.org/x/tools/go/ast/inspector"
)

var errorInterfaceType = types.Universe.Lookup("error").Type().Underlying().(*types.Interface)

// Analyzer flags literal nil error returns inside `if err != nil` blocks unless handled, logged, or suppressed via `//nilerr: <reason>`.
var Analyzer = &analysis.Analyzer{
	Name:     "nilerr",
	Doc:      "reports suspicious literal nil error returns inside non-nil error guard branches",
	Requires: []*analysis.Analyzer{inspect.Analyzer},
	Run:      run,
}

type fileState struct {
	pass        *analysis.Pass
	nilerrLines map[int]bool
	funcResults []int
}

func run(pass *analysis.Pass) (any, error) {
	if pass.TypesInfo == nil {
		return nil, nil
	}
	insp, _ := pass.ResultOf[inspect.Analyzer].(*inspector.Inspector)
	if insp == nil {
		return nil, nil
	}
	suppByFile := collectNilerrComments(pass)
	insp.Preorder([]ast.Node{(*ast.FuncDecl)(nil), (*ast.FuncLit)(nil)}, func(n ast.Node) {
		var funcType *ast.FuncType
		var body *ast.BlockStmt
		switch fn := n.(type) {
		case *ast.FuncDecl:
			funcType, body = fn.Type, fn.Body
		case *ast.FuncLit:
			funcType, body = fn.Type, fn.Body
		}
		if body == nil || funcType == nil {
			return
		}
		errIdx := errorResultIndices(pass.TypesInfo, funcType.Results)
		if len(errIdx) == 0 {
			return
		}
		file := pass.Fset.File(n.Pos())
		var filename string
		if file != nil {
			filename = file.Name()
		}
		state := &fileState{
			pass:        pass,
			nilerrLines: suppByFile[filename],
			funcResults: errIdx,
		}
		state.scanStmts(body.List, nil)
	})
	return nil, nil
}

func collectNilerrComments(pass *analysis.Pass) map[string]map[int]bool {
	result := make(map[string]map[int]bool)
	for _, file := range pass.Files {
		lines := make(map[int]bool)
		for _, cg := range file.Comments {
			for _, c := range cg.List {
				text := c.Text
				if strings.HasPrefix(text, "//") {
					text = strings.TrimPrefix(text, "//")
				} else if strings.HasPrefix(text, "/*") && strings.HasSuffix(text, "*/") {
					text = strings.TrimSuffix(strings.TrimPrefix(text, "/*"), "*/")
				} else {
					continue
				}
				text = strings.TrimSpace(text)
				if rest, ok := strings.CutPrefix(text, "nilerr:"); ok && strings.TrimSpace(rest) != "" {
					startLine := pass.Fset.Position(c.Pos()).Line
					endLine := pass.Fset.Position(c.End()).Line
					for l := startLine; l <= endLine; l++ {
						lines[l] = true
					}
				}
			}
		}
		if len(lines) > 0 {
			if f := pass.Fset.File(file.Pos()); f != nil {
				result[f.Name()] = lines
			}
		}
	}
	return result
}

func (s *fileState) scanStmts(stmts []ast.Stmt, activeErr types.Object) {
	info := s.pass.TypesInfo
	for _, stmt := range stmts {
		if activeErr != nil && (stmtReassignsErr(info, stmt, activeErr) || stmtInspectsErr(info, stmt, activeErr)) {
			activeErr = nil
		}
		s.scanStmt(stmt, activeErr)
	}
}

func (s *fileState) scanStmt(stmt ast.Stmt, activeErr types.Object) {
	switch node := stmt.(type) {
	case *ast.LabeledStmt:
		s.scanStmt(node.Stmt, activeErr)
	case *ast.BlockStmt:
		s.scanStmts(node.List, activeErr)
	case *ast.IfStmt:
		s.scanIfStmt(node, activeErr)
	case *ast.ReturnStmt:
		if activeErr != nil {
			s.checkReturnNilErr(node, activeErr)
		}
	case *ast.ForStmt:
		s.scanBlock(node.Body, nil)
	case *ast.RangeStmt:
		s.scanBlock(node.Body, nil)
	case *ast.SwitchStmt:
		s.scanSwitchCases(node.Body, node.Init, node.Tag, activeErr)
	case *ast.TypeSwitchStmt:
		s.scanSwitchCases(node.Body, node.Init, node.Assign, activeErr)
	case *ast.SelectStmt:
		if node.Body != nil {
			for _, cc := range node.Body.List {
				if clause, ok := cc.(*ast.CommClause); ok {
					s.scanStmts(clause.Body, nil)
				}
			}
		}
	}
}

func (s *fileState) scanBlock(block *ast.BlockStmt, activeErr types.Object) {
	if block != nil {
		s.scanStmts(block.List, activeErr)
	}
}

func (s *fileState) scanIfStmt(ifStmt *ast.IfStmt, activeErr types.Object) {
	info := s.pass.TypesInfo
	nextErr := activeErr
	if nextErr != nil && ifBranchInspectsErr(info, ifStmt, nextErr) {
		nextErr = nil
	} else if guardErr := nonNilErrorVar(info, ifStmt.Cond); guardErr != nil {
		nextErr = guardErr
	}
	if blockLogsObj(info, ifStmt.Body, nextErr) {
		s.scanBlock(ifStmt.Body, nil)
	} else {
		s.scanBlock(ifStmt.Body, nextErr)
	}
	if ifStmt.Else != nil {
		s.scanStmt(ifStmt.Else, activeErr)
	}
}

func ifBranchInspectsErr(info *types.Info, ifStmt *ast.IfStmt, activeErr types.Object) bool {
	if ifStmt.Init != nil && (stmtReassignsErr(info, ifStmt.Init, activeErr) || nodeReferencesObj(info, ifStmt.Init, activeErr)) {
		return true
	}
	return nodeReferencesObj(info, ifStmt.Cond, activeErr) || isContextErrGuard(info, ifStmt.Cond)
}

func (s *fileState) scanSwitchCases(body *ast.BlockStmt, init ast.Stmt, header ast.Node, activeErr types.Object) {
	if body == nil {
		return
	}
	info := s.pass.TypesInfo
	if activeErr != nil && ((init != nil && (stmtReassignsErr(info, init, activeErr) || nodeReferencesObj(info, init, activeErr))) || (header != nil && nodeReferencesObj(info, header, activeErr))) {
		activeErr = nil
	}
	for _, stmt := range body.List {
		cc, ok := stmt.(*ast.CaseClause)
		if !ok {
			continue
		}
		caseErr := activeErr
		for _, cond := range cc.List {
			if caseErr != nil && nodeReferencesObj(info, cond, caseErr) {
				caseErr = nil
				break
			}
		}
		s.scanStmts(cc.Body, caseErr)
	}
}

func (s *fileState) checkReturnNilErr(ret *ast.ReturnStmt, activeErr types.Object) {
	info := s.pass.TypesInfo
	if len(ret.Results) == 0 || nodeReferencesObj(info, ret, activeErr) {
		return
	}
	line := s.pass.Fset.Position(ret.Pos()).Line
	if s.nilerrLines[line] || s.nilerrLines[line-1] {
		return
	}
	for _, idx := range s.funcResults {
		if idx < len(ret.Results) && isNilIdent(info, ret.Results[idx]) {
			s.pass.Reportf(ret.Pos(), "returning nil error inside `if %s != nil` branch without handling error (add `//nilerr: <reason>` if intentional)", activeErr.Name())
			return
		}
	}
}

func isContextErrGuard(info *types.Info, expr ast.Expr) bool {
	bin, ok := ast.Unparen(expr).(*ast.BinaryExpr)
	if !ok || bin.Op != token.NEQ {
		return false
	}
	isCtxCall := func(e ast.Expr) bool {
		call, ok := ast.Unparen(e).(*ast.CallExpr)
		if !ok || len(call.Args) != 0 {
			return false
		}
		sel, ok := ast.Unparen(call.Fun).(*ast.SelectorExpr)
		if !ok || sel.Sel.Name != "Err" {
			return false
		}
		t := info.TypeOf(ast.Unparen(sel.X))
		if t == nil {
			return false
		}
		named, ok := types.Unalias(t).(*types.Named)
		return ok && named.Obj() != nil && named.Obj().Pkg() != nil &&
			named.Obj().Pkg().Path() == "context" && named.Obj().Name() == "Context"
	}
	return (isCtxCall(bin.X) && isNilIdent(info, bin.Y)) || (isCtxCall(bin.Y) && isNilIdent(info, bin.X))
}

func errorResultIndices(info *types.Info, results *ast.FieldList) []int {
	if results == nil {
		return nil
	}
	var indices []int
	pos := 0
	for _, field := range results.List {
		t := info.TypeOf(field.Type)
		isErr := t != nil && types.Implements(t, errorInterfaceType)
		for range max(len(field.Names), 1) {
			if isErr {
				indices = append(indices, pos)
			}
			pos++
		}
	}
	return indices
}

func nonNilErrorVar(info *types.Info, cond ast.Expr) types.Object {
	bin, ok := cond.(*ast.BinaryExpr)
	if !ok || bin.Op != token.NEQ {
		return nil
	}
	errorIdent := func(expr ast.Expr) types.Object {
		ident, ok := expr.(*ast.Ident)
		if !ok {
			return nil
		}
		obj := info.ObjectOf(ident)
		if obj == nil || obj.Type() == nil || !types.Implements(obj.Type(), errorInterfaceType) {
			return nil
		}
		return obj
	}
	if obj := errorIdent(bin.X); obj != nil && isNilIdent(info, bin.Y) {
		return obj
	}
	if obj := errorIdent(bin.Y); obj != nil && isNilIdent(info, bin.X) {
		return obj
	}
	return nil
}

func isNilIdent(info *types.Info, expr ast.Expr) bool {
	ident, ok := expr.(*ast.Ident)
	return ok && ident.Name == "nil" && (info.ObjectOf(ident) == nil || info.ObjectOf(ident) == types.Universe.Lookup("nil"))
}

func stmtInspectsErr(info *types.Info, stmt ast.Stmt, obj types.Object) bool {
	ifStmt, ok := stmt.(*ast.IfStmt)
	return ok && ((ifStmt.Init != nil && nodeReferencesObj(info, ifStmt.Init, obj)) || nodeReferencesObj(info, ifStmt.Cond, obj))
}

func stmtReassignsErr(info *types.Info, stmt ast.Stmt, obj types.Object) bool {
	assign, ok := stmt.(*ast.AssignStmt)
	if !ok || obj == nil {
		return false
	}
	for _, lhs := range assign.Lhs {
		if ident, ok := lhs.(*ast.Ident); ok && (info.Defs[ident] == obj || info.Uses[ident] == obj) {
			return true
		}
	}
	return false
}

func blockLogsObj(info *types.Info, block *ast.BlockStmt, obj types.Object) bool {
	if obj == nil || block == nil {
		return false
	}
	logged := false
	ast.Inspect(block, func(n ast.Node) bool {
		if logged {
			return false
		}
		call, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		var name string
		switch fn := ast.Unparen(call.Fun).(type) {
		case *ast.SelectorExpr:
			name = fn.Sel.Name
		case *ast.Ident:
			name = fn.Name
		}
		lower := strings.ToLower(name)
		hasKeyword := false
		for _, kw := range []string{"warn", "error", "info", "debug", "log", "print", "fatal", "record", "close", "abort"} {
			if strings.Contains(lower, kw) {
				hasKeyword = true
				break
			}
		}
		if !hasKeyword {
			return true
		}
		for _, arg := range call.Args {
			if nodeReferencesObj(info, arg, obj) {
				logged = true
				return false
			}
		}
		return true
	})
	return logged
}

func nodeReferencesObj(info *types.Info, root ast.Node, obj types.Object) bool {
	if root == nil || obj == nil {
		return false
	}
	found := false
	ast.Inspect(root, func(n ast.Node) bool {
		if found {
			return false
		}
		if ident, ok := n.(*ast.Ident); ok && (info.Uses[ident] == obj || info.Defs[ident] == obj) {
			found = true
			return false
		}
		return true
	})
	return found
}
