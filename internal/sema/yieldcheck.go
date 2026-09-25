package sema

import "ic10go/internal/ast"

// warnLoopWithoutYield warns about an unbounded `for` loop whose body never
// pauses the chip. IC10 executes a bounded number of instructions per tick and
// then pauses automatically, but a loop with no yield()/sleep() still runs as
// fast as the chip allows: it burns power/heat and reacts at a fixed tick rate.
//
// Only `for { ... }` (no condition) is reported, so the warning stays focused
// on the main/long-running loops instead of every bounded helper loop.
func (c *typeChecker) warnLoopWithoutYield(s *ast.ForStmt) {
	if s.Cond != nil {
		return
	}
	if c.pauses([]ast.Stmt{s.Body}, map[string]bool{}) {
		return
	}
	c.diags.WarnfCode("loop-without-yield", s.Pos(),
		"loop without yield(); add yield() inside the loop so the chip pauses each tick")
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
