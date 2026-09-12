package lower

import (
	"os"

	"ic10go/internal/ast"
	"ic10go/internal/ir"
	"ic10go/internal/sema"
)

// outlinedFunc is a user function emitted once as an IC10 subroutine and called
// with jal / j ra instead of being inlined at every call site.
type outlinedFunc struct {
	fi       *sema.FuncInfo
	entry    *ir.Block
	epilogue *ir.Block
	params   []*ir.Reg
	result   *ir.Reg
}

// planOutlines chooses which user functions to outline. Inlining duplicates a
// function body at every call site; outlining costs a `jal` plus argument moves
// per call but emits the body once. That trade favours outlining when a
// function is called several times and has a non-trivial body.
//
// Only leaf functions (no calls to other user functions) without low-level
// labels are eligible: nesting outlined functions would clobber IC10's single
// return-address register, and labels are global.
func PlanOutlines(info *sema.Info) map[string]bool {
	if os.Getenv("IC10C_NO_OUTLINE") != "" {
		return nil
	}
	calls := map[string]int{}
	// info.Funcs includes main, so counting every function body covers the
	// whole program exactly once.
	for _, fi := range info.Funcs {
		forEachCall(fi.Decl.Body, func(c *ast.CallExpr) {
			if id, ok := c.Fun.(*ast.Ident); ok {
				if _, isFunc := info.Funcs[id.Name]; isFunc {
					calls[id.Name]++
				}
			}
		})
	}

	out := map[string]bool{}
	for name, fi := range info.Funcs {
		if calls[name] < 2 || !outlinable(info, fi) {
			continue
		}
		out[name] = true
	}
	return out
}

// outlinable reports whether a function can safely be emitted as a subroutine.
func outlinable(info *sema.Info, fi *sema.FuncInfo) bool {
	leaf, low := true, false
	forEachCall(fi.Decl.Body, func(c *ast.CallExpr) {
		if id, ok := c.Fun.(*ast.Ident); ok {
			if _, isFunc := info.Funcs[id.Name]; isFunc {
				leaf = false
			}
		}
	})
	walkStmt(fi.Decl.Body, func(s ast.Stmt) {
		switch s.(type) {
		case *ast.LabelStmt, *ast.GotoStmt, *ast.CallStmt, *ast.RetStmt:
			low = true
		}
	})
	return leaf && !low && countStatements(fi.Decl.Body) >= 3
}

// countStatements counts the statements in a function body, depth first.
func countStatements(body *ast.BlockStmt) int {
	n := 0
	walkStmt(body, func(ast.Stmt) { n++ })
	return n
}

// forEachCall visits every call expression in a function body, including calls
// nested in conditions, assignments and returns.
func forEachCall(body *ast.BlockStmt, visit func(*ast.CallExpr)) {
	expr := func(e ast.Expr) {
		walkExpr(e, func(x ast.Expr) {
			if c, ok := x.(*ast.CallExpr); ok {
				visit(c)
			}
		})
	}
	var stmt func(ast.Stmt)
	stmt = func(s ast.Stmt) {
		if s == nil {
			return
		}
		switch v := s.(type) {
		case *ast.BlockStmt:
			for _, st := range v.List {
				stmt(st)
			}
		case *ast.ExprStmt:
			expr(v.X)
		case *ast.AssignStmt:
			expr(v.Lhs)
			expr(v.Rhs)
		case *ast.IncDecStmt:
			expr(v.X)
		case *ast.IfStmt:
			expr(v.Cond)
			stmt(v.Then)
			stmt(v.Else)
		case *ast.ForStmt:
			stmt(v.Init)
			if v.Cond != nil {
				expr(v.Cond)
			}
			stmt(v.Post)
			stmt(v.Body)
		case *ast.SwitchStmt:
			if v.Tag != nil {
				expr(v.Tag)
			}
			for _, c := range v.Cases {
				for _, e := range c.Exprs {
					expr(e)
				}
				for _, st := range c.Body {
					stmt(st)
				}
			}
		case *ast.ReturnStmt:
			if v.Result != nil {
				expr(v.Result)
			}
		case *ast.DeclStmt:
			switch d := v.Decl.(type) {
			case *ast.VarDecl:
				if d.Value != nil {
					expr(d.Value)
				}
			case *ast.ConstDecl:
				if d.Value != nil {
					expr(d.Value)
				}
			}
		}
	}
	stmt(body)
}

// walkStmt visits every statement in a tree, depth first.
func walkStmt(s ast.Stmt, visit func(ast.Stmt)) {
	if s == nil {
		return
	}
	visit(s)
	switch v := s.(type) {
	case *ast.BlockStmt:
		for _, st := range v.List {
			walkStmt(st, visit)
		}
	case *ast.IfStmt:
		walkStmt(v.Then, visit)
		walkStmt(v.Else, visit)
	case *ast.ForStmt:
		walkStmt(v.Init, visit)
		walkStmt(v.Post, visit)
		walkStmt(v.Body, visit)
	case *ast.SwitchStmt:
		for _, c := range v.Cases {
			for _, st := range c.Body {
				walkStmt(st, visit)
			}
		}
	}
}

// walkExpr visits every expression in a tree, depth first.
func walkExpr(e ast.Expr, visit func(ast.Expr)) {
	if e == nil {
		return
	}
	visit(e)
	switch v := e.(type) {
	case *ast.UnaryExpr:
		walkExpr(v.X, visit)
	case *ast.BinaryExpr:
		walkExpr(v.X, visit)
		walkExpr(v.Y, visit)
	case *ast.ParenExpr:
		walkExpr(v.X, visit)
	case *ast.CallExpr:
		walkExpr(v.Fun, visit)
		for _, a := range v.Args {
			walkExpr(a, visit)
		}
	case *ast.SelectorExpr:
		walkExpr(v.X, visit)
	case *ast.IndexExpr:
		walkExpr(v.X, visit)
		walkExpr(v.Index, visit)
	case *ast.TernaryExpr:
		walkExpr(v.Cond, visit)
		walkExpr(v.Then, visit)
		walkExpr(v.Else, visit)
	}
}
