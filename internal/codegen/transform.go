package codegen

import (
	"github.com/escalier-lang/escalier/internal/ast"
)

func TransformExprs(exprs []ast.Expr) []Expr {
	var res []Expr
	for _, e := range exprs {
		res = append(res, TransformExpr(e))
	}
	return res
}

func TransformIdentifier(ident *ast.Ident) *Identifier {
	if ident == nil {
		return nil
	}
	return &Identifier{
		Name:   ident.Name,
		span:   nil,
		source: ident,
	}
}

func TransformPattern(pattern ast.Pat) Pat {
	switch p := pattern.(type) {
	case *ast.IdentPat:
		return &IdentPat{
			Name:   p.Name,
			span:   nil,
			source: p,
		}
	case *ast.ObjectPat:
		var elems []ObjPatElem
		for _, elem := range p.Elems {
			switch e := elem.(type) {
			case *ast.ObjKeyValuePat:
				elems = append(elems, &ObjKeyValuePat{
					Key:     e.Key,
					Value:   TransformPattern(e.Value),
					Default: TransformExpr(e.Default),
					span:    nil,
					source:  e,
				})
			case *ast.ObjShorthandPat:
				elems = append(elems, &ObjShorthandPat{
					Key:     e.Key,
					Default: TransformExpr(e.Default),
					span:    nil,
					source:  e,
				})
			case *ast.ObjRestPat:
				elems = append(elems, &ObjRestPat{
					Pattern: TransformPattern(e.Pattern),
					span:    nil,
					source:  e,
				})
			}
		}
		return &ObjectPat{
			Elems:  elems,
			span:   nil,
			source: p,
		}
	case *ast.TuplePat:
		var elems []TuplePatElem
		for _, elem := range p.Elems {
			switch e := elem.(type) {
			case *ast.TupleElemPat:
				elems = append(elems, &TupleElemPat{
					Pattern: TransformPattern(e.Pattern),
					Default: TransformExpr(e.Default),
					span:    nil,
					source:  e,
				})
			case *ast.TupleRestPat:
				elems = append(elems, &TupleRestPat{
					Pattern: TransformPattern(e.Pattern),
					span:    nil,
					source:  e,
				})
			}
		}
		return &TuplePat{
			Elems:  elems,
			span:   nil,
			source: p,
		}
	default:
		panic("TODO")
	}
}

func TransformStmt(stmt ast.Stmt) Stmt {
	switch s := stmt.(type) {
	case *ast.ExprStmt:
		return &ExprStmt{
			Expr:   TransformExpr(s.Expr),
			span:   nil,
			source: stmt,
		}
	case *ast.DeclStmt:
		return &DeclStmt{
			Decl:   TransformDecl(s.Decl),
			span:   nil,
			source: stmt,
		}
	case *ast.ReturnStmt:
		return &ReturnStmt{
			Expr:   TransformExpr(s.Expr),
			span:   nil,
			source: stmt,
		}
	default:
		panic("TransformStmt - default case should never happen")
	}
}

func TransformModule(mod *ast.Module) *Module {
	var stmts []Stmt
	for _, s := range mod.Stmts {
		stmts = append(stmts, TransformStmt(s))
	}
	return &Module{
		Stmts: stmts,
	}
}

func TransformStmts(stmts []ast.Stmt) []Stmt {
	var res []Stmt
	for _, s := range stmts {
		res = append(res, TransformStmt(s))
	}
	return res
}

func TransformDecl(decl ast.Decl) Decl {
	switch d := decl.(type) {
	case *ast.VarDecl:
		return &VarDecl{
			Kind:    VariableKind(d.Kind),
			Pattern: TransformPattern(d.Pattern),
			Init:    TransformExpr(d.Init),
			declare: decl.Declare(),
			export:  decl.Export(),
			span:    nil,
			source:  decl,
		}
	case *ast.FuncDecl:
		var params []*Param
		for _, p := range d.Params {
			params = append(params, &Param{
				Pattern: TransformPattern(p.Pattern),
			})
		}
		return &FuncDecl{
			Name:    TransformIdentifier(d.Name),
			Params:  params,
			Body:    TransformStmts(d.Body.Stmts),
			declare: decl.Declare(),
			export:  decl.Export(),
			span:    nil,
			source:  decl,
		}
	default:
		panic("TODO - TransformDecl - default case")
	}
}

func TransformExpr(expr ast.Expr) Expr {
	if expr == nil {
		return nil
	}

	switch e := expr.(type) {
	case *ast.LiteralExpr:
		switch lit := e.Lit.(type) {
		case *ast.BoolLit:
			return NewBoolExpr(lit.Value, expr)
		case *ast.NumLit:
			return NewNumExpr(lit.Value, expr)
		case *ast.StrLit:
			return NewStrExpr(lit.Value, expr)
		case *ast.BigIntLit:
			panic("TODO: big int literal")
		case *ast.NullLit:
			panic("TODO: null literal")
		case *ast.UndefinedLit:
			panic("TODO: undefined literal")
		default:
			panic("TODO: literal type")
		}
	case *ast.BinaryExpr:
		return NewBinaryExpr(
			TransformExpr(e.Left),
			BinaryOp(e.Op),
			TransformExpr(e.Right),
			expr,
		)
	case *ast.UnaryExpr:
		return NewUnaryExpr(
			UnaryOp(e.Op),
			TransformExpr(e.Arg),
			expr,
		)
	case *ast.IdentExpr:
		return NewIdentExpr(e.Name, expr)
	case *ast.CallExpr:
		return NewCallExpr(
			TransformExpr(e.Callee),
			TransformExprs(e.Args),
			e.OptChain,
			expr,
		)
	case *ast.IndexExpr:
		return NewIndexExpr(
			TransformExpr(e.Object),
			TransformExpr(e.Index),
			e.OptChain,
			expr,
		)
	case *ast.MemberExpr:
		return NewMemberExpr(
			TransformExpr(e.Object),
			TransformIdentifier(e.Prop),
			e.OptChain,
			expr,
		)
	case *ast.TupleExpr:
		return NewArrayExpr(
			TransformExprs(e.Elems),
			expr,
		)
	case *ast.FuncExpr:
		var params []*Param
		for _, p := range e.Params {
			params = append(params, &Param{
				Pattern: TransformPattern(p.Pattern),
			})
		}
		return NewFuncExpr(
			params,
			TransformStmts(e.Body.Stmts),
			expr,
		)
	case *ast.IgnoreExpr:
		panic("TODO - TransformExpr - IgnoreExpr")
	case *ast.EmptyExpr:
		panic("TODO - TransformExpr - EmptyExpr")
	default:
		panic("TODO - TransformExpr - default case")
	}
}
