package codegen

import (
	"slices"
	"strconv"

	"github.com/escalier-lang/escalier/internal/ast"
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
			return &IdentPat{
				Name:   p.Name,
				span:   nil,
				source: p,
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
							NewIdentifier(e.Key, nil), // TODO: replace with Prop
							false,
							nil,
						)
					}

					elems = append(elems, NewObjKeyValuePat(
						e.Key,
						buildPatternRec(e.Value, newTarget),
						b.buildExpr(e.Default),
						e,
					))
				case *ast.ObjShorthandPat:
					elems = append(elems, NewObjShorthandPat(
						e.Key,
						b.buildExpr(e.Default),
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
			var elems []TuplePatElem
			for i, elem := range p.Elems {
				switch e := elem.(type) {
				case *ast.TupleElemPat:
					var newTarget Expr
					if target != nil {
						newTarget = NewIndexExpr(target, NewNumExpr(float64(i), nil), false, nil)

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
					}

					elems = append(
						elems,
						NewTupleElemPat(
							buildPatternRec(e.Pattern, newTarget),
							b.buildExpr(e.Default),
							e,
						),
					)
				case *ast.TupleRestPat:
					elems = append(elems, &TupleRestPat{
						Pattern: buildPatternRec(e.Pattern, target),
						span:    nil,
						source:  e,
					})
				}
			}
			return NewTuplePat(elems, p)
		case *ast.ExtractPat:
			tempVars := []Expr{}
			tempVarPats := []TuplePatElem{}

			for _, arg := range p.Args {
				tempId := b.NewTempId()
				tempVar := NewIdentExpr(tempId, nil)
				switch arg := arg.(type) {
				case *ast.ExtractArgPat:
					var init Expr
					if arg.Default != nil {
						init = b.buildExpr(arg.Default)
					}
					tempVarPat := NewTupleElemPat(
						&IdentPat{
							Name:   tempId,
							span:   nil,
							source: nil,
						},
						init,
						nil, // TODO: source
					)
					tempVarPats = append(tempVarPats, tempVarPat)
				case *ast.ExtractRestArgPat:
					tempVarPat := NewTupleRestPat(
						&IdentPat{
							Name:   tempId,
							span:   nil,
							source: nil,
						},
						nil, // TODO: source
					)
					tempVarPats = append(tempVarPats, tempVarPat)
				}

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

				var argChecks []Expr
				var argStmts []Stmt

				switch arg := arg.(type) {
				case *ast.ExtractArgPat:
					argChecks, argStmts = b.buildPattern(arg.Pattern, temp)
				case *ast.ExtractRestArgPat:
					argChecks, argStmts = b.buildPattern(arg.Pattern, temp)
				}

				checks = slices.Concat(checks, argChecks)
				stmts = slices.Concat(stmts, argStmts)
			}
			return nil
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
		stmt := &ExprStmt{
			Expr:   b.buildExpr(s.Expr),
			span:   nil,
			source: stmt,
		}
		return []Stmt{stmt}
	case *ast.DeclStmt:
		return b.buildDecl(s.Decl)
	case *ast.ReturnStmt:
		stmt := &ReturnStmt{
			Expr:   b.buildExpr(s.Expr),
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
		init := b.buildExpr(d.Init)
		_, stmts := b.buildPattern(d.Pattern, init)
		// varDecl := &VarDecl{
		// 	Kind:    VariableKind(d.Kind),
		// 	Pattern: b.buildPattern(d.Pattern),
		// 	Init:    b.buildExpr(d.Init),
		// 	declare: decl.Declare(),
		// 	export:  decl.Export(),
		// 	span:    nil,
		// 	source:  decl,
		// }
		// stmt := &DeclStmt{
		// 	Decl:   varDecl,
		// 	span:   nil,
		// 	source: decl,
		// }
		return stmts
	case *ast.FuncDecl:
		var params []*Param
		var allParamStmts []Stmt
		for _, p := range d.Params {
			id := b.NewTempId()
			_, paramStmts := b.buildPattern(p.Pattern, NewIdentExpr(id, nil))
			allParamStmts = slices.Concat(allParamStmts, paramStmts)
			params = append(params, &Param{
				Pattern: NewIdentPat(id, p.Pattern),
			})
		}
		fnDecl := &FuncDecl{
			Name:    buildIdent(d.Name),
			Params:  params,
			Body:    slices.Concat(allParamStmts, b.buildStmts(d.Body.Stmts)),
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
			panic("TODO: null literal")
		case *ast.UndefinedLit:
			panic("TODO: undefined literal")
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
		var params []*Param
		var allParamStmts []Stmt
		for _, p := range e.Params {
			id := b.NewTempId()
			_, paramStmts := b.buildPattern(p.Pattern, NewIdentExpr(id, nil))
			allParamStmts = slices.Concat(allParamStmts, paramStmts)
			params = append(params, &Param{
				Pattern: NewIdentPat(id, nil),
			})
		}
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
		panic("TODO - buildExpr - default case")
	}
}
