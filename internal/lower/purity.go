package lower

import (
	"strings"

	"ic10go/internal/ast"
	"ic10go/internal/builtin"
	"ic10go/internal/sema"
)

// computePureFuncs marks user functions whose evaluation has no observable side
// effects (no device writes, no side-effecting builtins, no low-level control).
// Calls to such functions may be evaluated unconditionally, so `&&`/`||` over
// them lowers to min/max instead of branches.
//
// The analysis is conservative: anything uncertain is treated as impure.
func computePureFuncs(info *sema.Info) map[string]bool {
	pure := map[string]bool{}
	for name := range info.Funcs {
		pure[name] = true
	}
	for changed := true; changed; {
		changed = false
		for name, fi := range info.Funcs {
			if pure[name] && bodyImpure(fi.Decl.Body, pure) {
				pure[name] = false
				changed = true
			}
		}
	}
	return pure
}

func bodyImpure(body *ast.BlockStmt, pure map[string]bool) bool {
	impure := false
	walkStmt(body, func(s ast.Stmt) {
		switch v := s.(type) {
		case *ast.AssignStmt:
			if isDeviceTarget(v.Lhs) || exprImpure(v.Rhs, pure) {
				impure = true
			}
		case *ast.IncDecStmt:
			if isDeviceTarget(v.X) {
				impure = true
			}
		case *ast.ExprStmt:
			if exprImpure(v.X, pure) {
				impure = true
			}
		case *ast.ReturnStmt:
			if v.Result != nil && exprImpure(v.Result, pure) {
				impure = true
			}
		case *ast.LabelStmt, *ast.GotoStmt, *ast.CallStmt, *ast.RetStmt:
			impure = true
		}
	})
	return impure
}

func exprImpure(e ast.Expr, pure map[string]bool) bool {
	impure := false
	walkExpr(e, func(x ast.Expr) {
		call, ok := x.(*ast.CallExpr)
		if !ok {
			return
		}
		switch fun := call.Fun.(type) {
		case *ast.Ident:
			if builtin.SemOf(fun.Name).SideEffect {
				impure = true
				return
			}
			if p, isFunc := pure[fun.Name]; isFunc && !p {
				impure = true
			}
		case *ast.SelectorExpr:
			// batch.write / batch.writeName / batch.writeSlot ...
			if strings.HasPrefix(fun.Sel.Name, "write") {
				impure = true
			}
		}
	})
	return impure
}

// isDeviceTarget reports whether an assignment target writes a device or a
// device slot (as opposed to a local variable).
func isDeviceTarget(e ast.Expr) bool {
	switch x := e.(type) {
	case *ast.DeviceLit:
		return true
	case *ast.SelectorExpr:
		switch x.X.(type) {
		case *ast.DeviceLit, *ast.Ident, *ast.IndexExpr:
			return true
		}
	case *ast.IndexExpr:
		if _, ok := x.X.(*ast.SelectorExpr); ok {
			return true
		}
	}
	return false
}
