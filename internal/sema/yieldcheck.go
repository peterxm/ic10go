package sema

import "ic10go/internal/ast"

// warnLoopWithoutYield warns about an unbounded `for` loop whose body never
// pauses the chip. IC10 executes a bounded number of instructions per tick and
// then pauses automatically, but a loop with no yield()/sleep() still runs as
// fast as the chip allows: it burns power/heat and reacts at a fixed tick rate.
//
// Only `for { ... }` (no condition) is reported, so the warning stays focused
// on the main/long-running loops instead of every bounded helper loop. A body
// that can leave the loop (a `break` targeting it, or a `return`) is bounded
// too — most often a decompiled do/while, which the decompiler writes as
// `for { ...; if cond { break } }` — so it is not reported either.
func (c *typeChecker) warnLoopWithoutYield(s *ast.ForStmt) {
	if s.Cond != nil {
		return
	}
	if c.pauses([]ast.Stmt{s.Body}, map[string]bool{}) {
		return
	}
	if c.loopExits(s.Body) {
		return
	}
	c.diags.WarnfCode("loop-without-yield", s.Pos(),
		"loop without yield(); add yield() inside the loop so the chip pauses each tick")
}

// loopExits reports whether the loop body can leave the loop: a `break`
// targeting it (not a nested loop/switch), a `goto` to a label declared outside
// it, or a `return`/`ret`.
func (c *typeChecker) loopExits(body *ast.BlockStmt) bool {
	inner := map[string]bool{}
	collectLabels(body.List, inner)
	return c.exitsLoopBody(body.List, false, inner)
}

// collectLabels records every label declared in stmts; a goto to a label outside
// the set leaves the loop.
func collectLabels(stmts []ast.Stmt, into map[string]bool) {
	for _, s := range stmts {
		switch v := s.(type) {
		case *ast.BlockStmt:
			collectLabels(v.List, into)
		case *ast.IfStmt:
			collectLabels([]ast.Stmt{v.Then, v.Else}, into)
		case *ast.ForStmt:
			collectLabels(v.Body.List, into)
		case *ast.RangeStmt:
			collectLabels(v.Body.List, into)
		case *ast.SwitchStmt:
			for _, cc := range v.Cases {
				collectLabels(cc.Body, into)
			}
		case *ast.LabelStmt:
			if v.Name != nil {
				into[v.Name.Name] = true
			}
		}
	}
}

func (c *typeChecker) exitsLoopBody(stmts []ast.Stmt, nested bool, inner map[string]bool) bool {
	for _, s := range stmts {
		if c.stmtExitsLoop(s, nested, inner) {
			return true
		}
	}
	return false
}

func (c *typeChecker) stmtExitsLoop(s ast.Stmt, nested bool, inner map[string]bool) bool {
	switch v := s.(type) {
	case nil:
		return false
	case *ast.BlockStmt:
		return c.exitsLoopBody(v.List, nested, inner)
	case *ast.IfStmt:
		return c.stmtExitsLoop(v.Then, nested, inner) || c.stmtExitsLoop(v.Else, nested, inner)
	case *ast.BreakStmt:
		// A labelled break targets an outer construct, so it still exits.
		return !nested || v.Label != nil
	case *ast.ReturnStmt, *ast.RetStmt:
		return true
	case *ast.GotoStmt:
		// The structured decompiler emits a goto to an outer label when it
		// cannot turn a loop exit into a break.
		return v.Name == nil || !inner[v.Name.Name]
	case *ast.ForStmt:
		return c.stmtExitsLoop(v.Body, true, inner)
	case *ast.RangeStmt:
		return c.stmtExitsLoop(v.Body, true, inner)
	case *ast.SwitchStmt:
		// A `break` inside a case leaves the switch, not the loop.
		for _, cc := range v.Cases {
			if c.exitsLoopBody(cc.Body, true, inner) {
				return true
			}
		}
		return false
	}
	return false
}

// pauses reports whether any statement in stmts calls yield()/sleep(), directly
// or through a user function that does.
func (c *typeChecker) pauses(stmts []ast.Stmt, seen map[string]bool) bool {
	for _, s := range stmts {
		if c.stmtPauses(s, seen) {
			return true
		}
	}
	return false
}

func (c *typeChecker) stmtPauses(s ast.Stmt, seen map[string]bool) bool {
	switch v := s.(type) {
	case nil:
		return false
	case *ast.BlockStmt:
		return c.pauses(v.List, seen)
	case *ast.ExprStmt:
		return c.exprPauses(v.X, seen)
	case *ast.AssignStmt:
		return c.exprPauses(v.Rhs, seen)
	case *ast.IncDecStmt:
		return c.exprPauses(v.X, seen)
	case *ast.DeclStmt:
		switch d := v.Decl.(type) {
		case *ast.VarDecl:
			return c.exprPauses(d.Value, seen)
		case *ast.ConstDecl:
			return c.exprPauses(d.Value, seen)
		}
		return false
	case *ast.IfStmt:
		return c.stmtPauses(v.Init, seen) || c.exprPauses(v.Cond, seen) ||
			c.stmtPauses(v.Then, seen) || c.stmtPauses(v.Else, seen)
	case *ast.ForStmt:
		return c.stmtPauses(v.Init, seen) || c.exprPauses(v.Cond, seen) ||
			c.stmtPauses(v.Post, seen) || c.stmtPauses(v.Body, seen)
	case *ast.RangeStmt:
		return c.exprPauses(v.X, seen) || c.stmtPauses(v.Body, seen)
	case *ast.SwitchStmt:
		if c.stmtPauses(v.Init, seen) || c.exprPauses(v.Tag, seen) {
			return true
		}
		for _, cc := range v.Cases {
			if c.pauses(cc.Body, seen) {
				return true
			}
		}
		return false
	case *ast.ReturnStmt:
		for _, r := range v.Results {
			if c.exprPauses(r, seen) {
				return true
			}
		}
		return c.exprPauses(v.Result, seen)
	}
	return false
}

func (c *typeChecker) exprPauses(e ast.Expr, seen map[string]bool) bool {
	switch v := e.(type) {
	case nil:
		return false
	case *ast.CallExpr:
		if id, ok := v.Fun.(*ast.Ident); ok {
			if id.Name == "yield" || id.Name == "sleep" {
				return true
			}
			if !seen[id.Name] {
				if fi := c.info.Funcs[id.Name]; fi != nil && fi.Decl != nil && fi.Decl.Body != nil {
					seen[id.Name] = true
					if c.pauses(fi.Decl.Body.List, seen) {
						return true
					}
				}
			}
		}
		for _, a := range v.Args {
			if c.exprPauses(a, seen) {
				return true
			}
		}
		return false
	case *ast.ParenExpr:
		return c.exprPauses(v.X, seen)
	case *ast.UnaryExpr:
		return c.exprPauses(v.X, seen)
	case *ast.BinaryExpr:
		return c.exprPauses(v.X, seen) || c.exprPauses(v.Y, seen)
	case *ast.TernaryExpr:
		return c.exprPauses(v.Cond, seen) || c.exprPauses(v.Then, seen) || c.exprPauses(v.Else, seen)
	case *ast.IndexExpr:
		return c.exprPauses(v.X, seen) || c.exprPauses(v.Index, seen)
	case *ast.SelectorExpr:
		return c.exprPauses(v.X, seen)
	case *ast.TupleExpr:
		for _, el := range v.Elems {
			if c.exprPauses(el, seen) {
				return true
			}
		}
		return false
	}
	return false
}
