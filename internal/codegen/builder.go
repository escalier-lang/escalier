package codegen

import (
	"fmt"
	"slices"
	"strconv"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/moznion/go-optional"
)

type Builder struct {
	tempId int
}

func (b *Builder) NewTempId() string {
	b.tempId += 1
	return "temp" + strconv.Itoa(b.tempId)
}

func (b *Builder) buildExprs(exprs []ast.Expr) []Expr {
	var res []Expr
	for _, e := range exprs {
		res = append(res, b.buildExpr(e))
	}
	return res
}

func buildIdent(ident *ast.Ident) *Identifier {
	if ident == nil {
		return nil
	}
	return &Identifier{
		Name:   ident.Name,
		span:   nil,
		source: ident,
	}
}

type Pair[T, U any] struct {
	First  T
	Second U
}

// TODO: dedupe with checker/infer.go
func Zip[T, U any](ts []T, us []U) []Pair[T, U] {
	if len(ts) != len(us) {
		panic("slices have different length")
	}
	pairs := make([]Pair[T, U], len(ts))
	for i := range ts {
		pairs[i] = Pair[T, U]{ts[i], us[i]}
	}
	return pairs
}

func (b *Builder) buildPattern(p ast.Pat, target Expr) ([]Expr, []Stmt) {

	var checks []Expr
	var stmts []Stmt

	var buildPatternRec func(p ast.Pat, target Expr) Pat

	buildPatternRec = func(p ast.Pat, target Expr) Pat {
		switch p := p.(type) {
		case *ast.IdentPat:
			_default := optional.Map(p.Default, func(e ast.Expr) Expr {
				return b.buildExpr(e)
			})
			return &IdentPat{
				Name:    p.Name,
				Default: _default,
				span:    nil,
				source:  p,
			}
		case *ast.ObjectPat:
			var elems []ObjPatElem
			for _, elem := range p.Elems {
				checks = append(checks,
					NewBinaryExpr(
						NewUnaryExpr(TypeOf, target, nil),
						EqualEqual,
						NewStrExpr("object", nil),
						nil,
					),
				)

				switch e := elem.(type) {
				case *ast.ObjKeyValuePat:
					var newTarget Expr
					if target != nil {
						newTarget = NewMemberExpr(
							target,
							NewIdentifier(e.Key.Name, e), // TODO: replace with Prop
							false,
							nil,
						)
					}

					elems = append(elems, NewObjKeyValuePat(
						e.Key.Name,
						buildPatternRec(e.Value, newTarget),
						optional.Map(e.Default, func(e ast.Expr) Expr {
							return b.buildExpr(e)
						}),
						e,
					))
				case *ast.ObjShorthandPat:
					elems = append(elems, NewObjShorthandPat(
						e.Key.Name,
						optional.Map(e.Default, func(e ast.Expr) Expr {
							return b.buildExpr(e)
						}),
						e,
					))
				case *ast.ObjRestPat:
					elems = append(elems, NewObjRestPat(
						buildPatternRec(e.Pattern, target),
						e,
					))
				}
			}
			return NewObjectPat(elems, p)
		case *ast.TuplePat:
			// TODO: replace with Prop
			length := NewIdentifier("length", nil)

			checks = append(
				checks,
				NewBinaryExpr(
					NewMemberExpr(target, length, false, nil),
					EqualEqual,
					NewNumExpr(float64(len(p.Elems)), nil),
					nil,
				),
			)

			var elems []Pat
			for i, elem := range p.Elems {
				var newTarget Expr
				if target != nil {
					newTarget = NewIndexExpr(target, NewNumExpr(float64(i), nil), false, nil)
				}
				elems = append(elems, buildPatternRec(elem, newTarget))
			}

			return NewTuplePat(elems, p)
		case *ast.ExtractorPat:
			tempVars := []Expr{}
			tempVarPats := []Pat{}

			for _, arg := range p.Args {
				tempId := b.NewTempId()
				tempVar := NewIdentExpr(tempId, nil)

				init := optional.None[Expr]()
				switch arg := arg.(type) {
				case *ast.IdentPat:
					init = optional.Map(arg.Default, func(e ast.Expr) Expr {
						return b.buildExpr(e)
					})
				}
				tempVarPat := NewIdentPat(tempId, init, p)

				tempVarPats = append(tempVarPats, tempVarPat)
				tempVars = append(tempVars, tempVar)
			}
			extractor := NewIdentExpr(p.Name, p)
			subject := target
			receiver := NewIdentExpr("undefined", nil)

			call := NewCallExpr(
				NewIdentExpr("InvokeCustomMatcherOrThrow", nil),
				[]Expr{extractor, subject, receiver},
				false,
				nil, // TODO: source
			)

			decl := &VarDecl{
				Kind:    VariableKind(ast.ValKind),
				Pattern: NewTuplePat(tempVarPats, nil),
				Init:    call,
				declare: false,
				export:  false,
				span:    nil,
				source:  nil, // TODO
			}

			stmts = append(stmts, &DeclStmt{
				Decl:   decl,
				span:   nil,
				source: nil,
			})

			for _, pair := range Zip(tempVars, p.Args) {
				temp := pair.First
				arg := pair.Second
				argChecks, argStmts := b.buildPattern(arg, temp)
				checks = slices.Concat(checks, argChecks)
				stmts = slices.Concat(stmts, argStmts)
			}
			return nil
		case *ast.RestPat:
			return &RestPat{
				Pattern: buildPatternRec(p.Pattern, target),
				span:    nil,
				source:  p,
			}
		case *ast.LitPat:
			// TODO
		case *ast.IsPat:
			// TODO
		case *ast.WildcardPat:
			// TODO
		default:
			// TODO
		}
		panic("TODO - buildPattern - default case")
	}

	// TODO: Assign the target to a temp variable and pass the temp variable
	// to the buildPatternRec function as the target.  This is necessary because
	// the target may be a complex expression that needs to be evaluated only
	// once.
	pat := buildPatternRec(p, target)

	if pat != nil {
		decl := &VarDecl{
			Kind:    VariableKind(ast.ValKind),
			Pattern: pat,
			Init:    target,
			declare: false, // TODO
			export:  false, // TODO
			span:    nil,
			source:  nil,
		}
		stmts = append(stmts, &DeclStmt{
			Decl:   decl,
			span:   nil,
			source: nil,
		})
	}

	return checks, stmts
}

func (b *Builder) buildStmt(stmt ast.Stmt) []Stmt {
	switch s := stmt.(type) {
	case *ast.ExprStmt:
		switch s.Expr.(type) {
		case *ast.EmptyExpr:
			// Ignore empty expressions.
			return []Stmt{}
		default:
			stmt := &ExprStmt{
				Expr:   b.buildExpr(s.Expr),
				span:   nil,
				source: stmt,
			}
			return []Stmt{stmt}
		}
	case *ast.DeclStmt:
		return b.buildDecl(s.Decl)
	case *ast.ReturnStmt:
		expr := optional.Map(s.Expr, func(e ast.Expr) Expr {
			return b.buildExpr(e)
		})
		stmt := &ReturnStmt{
			Expr:   expr,
			span:   nil,
			source: stmt,
		}
		return []Stmt{stmt}
	default:
		panic("TransformStmt - default case should never happen")
	}
}

func (b *Builder) BuildModule(mod *ast.Module) *Module {
	var stmts []Stmt
	for _, s := range mod.Stmts {
		stmts = slices.Concat(stmts, b.buildStmt(s))
	}
	return &Module{
		Stmts: stmts,
	}
}

func (b *Builder) buildStmts(stmts []ast.Stmt) []Stmt {
	var res []Stmt
	for _, s := range stmts {
		res = slices.Concat(res, b.buildStmt(s))
	}
	return res
}

func (b *Builder) buildDecl(decl ast.Decl) []Stmt {
	switch d := decl.(type) {
	case *ast.VarDecl:
		init := b.buildExpr(d.Init.Unwrap()) // TOOD: handle the case when Init is None
		_, stmts := b.buildPattern(d.Pattern, init)
		return stmts
	case *ast.FuncDecl:
		params, allParamStmts := b.buildParams(d.Params)
		if d.Body.IsNone() {
			return []Stmt{}
		}
		body := d.Body.Unwrap() // okay because we checked IsNone() above
		fnDecl := &FuncDecl{
			Name:    buildIdent(d.Name),
			Params:  params,
			Body:    slices.Concat(allParamStmts, b.buildStmts(body.Stmts)),
			declare: decl.Declare(),
			export:  decl.Export(),
			span:    nil,
			source:  decl,
		}
		stmt := &DeclStmt{
			Decl:   fnDecl,
			span:   nil,
			source: decl,
		}
		return []Stmt{stmt}
	default:
		panic("TODO - TransformDecl - default case")
	}
}

func (b *Builder) buildExpr(expr ast.Expr) Expr {
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
			return NewNullExpr(expr)
		case *ast.UndefinedLit:
			return NewIdentExpr("undefined", expr)
		default:
			panic("TODO: literal type")
		}
	case *ast.BinaryExpr:
		return NewBinaryExpr(
			b.buildExpr(e.Left),
			BinaryOp(e.Op),
			b.buildExpr(e.Right),
			expr,
		)
	case *ast.UnaryExpr:
		return NewUnaryExpr(
			UnaryOp(e.Op),
			b.buildExpr(e.Arg),
			expr,
		)
	case *ast.IdentExpr:
		return NewIdentExpr(e.Name, expr)
	case *ast.CallExpr:
		return NewCallExpr(
			b.buildExpr(e.Callee),
			b.buildExprs(e.Args),
			e.OptChain,
			expr,
		)
	case *ast.IndexExpr:
		return NewIndexExpr(
			b.buildExpr(e.Object),
			b.buildExpr(e.Index),
			e.OptChain,
			expr,
		)
	case *ast.MemberExpr:
		return NewMemberExpr(
			b.buildExpr(e.Object),
			buildIdent(e.Prop),
			e.OptChain,
			expr,
		)
	case *ast.TupleExpr:
		return NewArrayExpr(
			b.buildExprs(e.Elems),
			expr,
		)
	case *ast.FuncExpr:
		params, allParamStmts := b.buildParams(e.Params)
		return NewFuncExpr(
			params,
			slices.Concat(allParamStmts, b.buildStmts(e.Body.Stmts)),
			expr,
		)
	case *ast.IgnoreExpr:
		panic("TODO - buildExpr - IgnoreExpr")
	case *ast.EmptyExpr:
		panic("TODO - buildExpr - EmptyExpr")
	default:
		panic(fmt.Sprintf("TODO - buildExpr - default case: %#v", e))
	}
}

func (b *Builder) buildParams(inParams []*ast.Param) ([]*Param, []Stmt) {
	var outParams []*Param
	var outParamStmts []Stmt
	for _, p := range inParams {
		id := b.NewTempId()
		var paramPat Pat
		paramPat = NewIdentPat(id, nil, p.Pattern)

		switch pat := p.Pattern.(type) {
		case *ast.RestPat:
			_, paramStmts := b.buildPattern(pat.Pattern, NewIdentExpr(id, nil))
			outParamStmts = slices.Concat(outParamStmts, paramStmts)
			paramPat = NewRestPat(paramPat, nil)
		default:
			_, paramStmts := b.buildPattern(pat, NewIdentExpr(id, nil))
			outParamStmts = slices.Concat(outParamStmts, paramStmts)
		}

		outParams = append(outParams, &Param{
			// TODO: handle param defaults
			Pattern: paramPat,
		})
	}
	return outParams, outParamStmts
}
