package solver

import (
	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/liveness"
	"github.com/escalier-lang/escalier/internal/set"
	"github.com/escalier-lang/escalier/internal/soltype"
)

// paramOnlyReadThrough reports whether body only reads through the parameter named `name`.
// That holds when every use of the parameter is the root of a member or element read, as in
// `p.x` or `p[i].y`, and the read's value goes to one of these places:
//   - a `return`, as in `return p.x`
//   - an expression statement that discards it, which excludes the last statement of a nested
//     block, since that statement is the value of an `if`, `do`, or `try` block
//   - an operand of an arithmetic, comparison, or unary operator, as in `p.x + 1`
//   - an operand of `&&`, `||`, or `??` whose own value goes to one of these places
//   - an immutable borrow, as in `&p.x`
//   - an `if` condition or a template literal interpolation
//
// It returns false when the body never uses the parameter, and when the body binds the name
// again anywhere, since a use may then name the inner binding. Any use inside a nested function
// also returns false, since the nested body may capture the parameter.
func paramOnlyReadThrough(name string, body *ast.Block) bool {
	v := &paramReadScanner{
		name:        name,
		consumed:    set.NewSet[ast.Expr](),
		readThrough: set.NewSet[*ast.IdentExpr](),
		tails:       set.NewSet[ast.Stmt](),
	}
	body.Accept(v)
	return !v.other && v.uses > 0
}

// paramReadScanner walks a function body for paramOnlyReadThrough. The walk enters a node
// before its children, so each node marks its children before the scanner reaches them.
//
// consumed holds each expression whose value goes to one of the places paramOnlyReadThrough
// lists. A member or element read passes the mark on to its object. readThrough holds each use
// of the parameter that is the object of a consumed read. tails holds the last statement of
// each block nested in the body, and inBody records that the walk has entered the body itself.
// uses counts the uses of the parameter, and other records a use that is not a read through it.
type paramReadScanner struct {
	ast.DefaultVisitor
	name        string
	consumed    set.Set[ast.Expr]
	readThrough set.Set[*ast.IdentExpr]
	tails       set.Set[ast.Stmt]
	inBody      bool
	uses        int
	other       bool
}

func (v *paramReadScanner) EnterBlock(b ast.Block) bool {
	if v.inBody && len(b.Stmts) > 0 {
		v.tails.Add(b.Stmts[len(b.Stmts)-1])
	}
	v.inBody = true
	return !v.other
}

func (v *paramReadScanner) EnterStmt(s ast.Stmt) bool {
	switch st := s.(type) {
	case *ast.ReturnStmt:
		v.consume(st.Expr)
	case *ast.ExprStmt:
		if !v.tails.Contains(s) {
			v.consume(st.Expr)
		}
	}
	return !v.other
}

func (v *paramReadScanner) EnterExpr(e ast.Expr) bool {
	switch e := e.(type) {
	case *ast.IdentExpr:
		if e.Name == v.name {
			v.uses++
			if !v.readThrough.Contains(e) {
				v.other = true
			}
		}
	case *ast.MemberExpr:
		v.readObject(e, e.Object)
	case *ast.IndexExpr:
		v.readObject(e, e.Object)
	case *ast.BinaryExpr:
		switch e.Op {
		case ast.Assign:
		case ast.LogicalAnd, ast.LogicalOr, ast.NullishCoalescing:
			if v.consumed.Contains(e) {
				v.consume(e.Left)
				v.consume(e.Right)
			}
		default:
			v.consume(e.Left)
			v.consume(e.Right)
		}
	case *ast.UnaryExpr:
		v.consume(e.Arg)
	case *ast.BorrowExpr:
		if !e.Mut {
			v.consume(e.Arg)
		}
	case *ast.IfElseExpr:
		v.consume(e.Cond)
	case *ast.TemplateLitExpr:
		for _, interp := range e.Exprs {
			v.consume(interp)
		}
	case *ast.FuncExpr:
		v.other = v.other || mentionsName(v.name, e)
		return false
	}
	return !v.other
}

func (v *paramReadScanner) EnterObjExprElem(e ast.ObjExprElem) bool {
	v.other = v.other || isShorthandFor(v.name, e)
	return !v.other
}

func (v *paramReadScanner) EnterPat(p ast.Pat) bool {
	switch p := p.(type) {
	case *ast.IdentPat:
		v.other = v.other || p.Name == v.name
	case *ast.ObjectPat:
		for _, elem := range p.Elems {
			if sh, ok := elem.(*ast.ObjShorthandPat); ok && sh.Key.Name == v.name {
				v.other = true
			}
		}
	}
	return !v.other
}

// consume marks e as an expression whose value goes to a place that does not let it escape.
// A nil e, such as a bare `return`, marks nothing.
func (v *paramReadScanner) consume(e ast.Expr) {
	if e != nil {
		v.consumed.Add(e)
	}
}

// readObject passes the mark of the consumed read on to obj, the read's object. A use of the
// parameter as that object is a read through it.
func (v *paramReadScanner) readObject(read, obj ast.Expr) {
	if !v.consumed.Contains(read) {
		return
	}
	v.consume(obj)
	if id, ok := obj.(*ast.IdentExpr); ok && id.Name == v.name {
		v.readThrough.Add(id)
	}
}

// mentionsName reports whether node reads a variable named name, either as an identifier or
// as a shorthand object property. It does not track scope, so a nested binding of the same
// name counts as a mention.
func mentionsName(name string, node interface{ Accept(ast.Visitor) }) bool {
	v := &nameMentionScanner{name: name}
	node.Accept(v)
	return v.found
}

type nameMentionScanner struct {
	ast.DefaultVisitor
	name  string
	found bool
}

func (v *nameMentionScanner) EnterExpr(e ast.Expr) bool {
	if id, ok := e.(*ast.IdentExpr); ok && id.Name == v.name {
		v.found = true
	}
	return !v.found
}

func (v *nameMentionScanner) EnterObjExprElem(e ast.ObjExprElem) bool {
	v.found = v.found || isShorthandFor(v.name, e)
	return !v.found
}

// isShorthandFor reports whether e is a shorthand property `{name}`, which reads the variable
// name.
func isShorthandFor(name string, e ast.ObjExprElem) bool {
	prop, ok := e.(*ast.PropertyExpr)
	if !ok || prop.Value != nil {
		return false
	}
	key, ok := prop.Name.(*ast.IdentExpr)
	return ok && key.Name == name
}

// identParamOnlyReadThrough reports whether pat binds a single name that body only reads
// through, as paramOnlyReadThrough decides. A rest, optional, or defaulted parameter, and a
// destructuring one, report false.
func identParamOnlyReadThrough(pat ast.Pat, rest, optional bool, body *ast.Block) bool {
	ip, ok := pat.(*ast.IdentPat)
	if !ok || ip.Default != nil || rest || optional {
		return false
	}
	return paramOnlyReadThrough(ip.Name, body)
}

// noteOpenEscapes adds to openEscapes the VarID of each argument of the call e that is a bare
// name passed to an open destination. An open destination is a parameter of a method called
// on a module-level binding, where the parameter's type is an inference variable that the
// receiver's own type reaches. `set` on a `Map()` written with no type arguments has one, so
// `seen.set("last", value)` records `value`. fn is the signature the call resolved to, and nil
// when it resolved to none.
func (c *checker) noteOpenEscapes(scope *Scope, e *ast.CallExpr, fn *soltype.FuncType) {
	if c.fn == nil || fn == nil {
		return
	}
	var recv ast.Expr
	switch callee := e.Callee.(type) {
	case *ast.MemberExpr:
		recv = callee.Object
	case *ast.IndexExpr:
		recv = callee.Object
	default:
		return
	}
	if _, ok := c.moduleLevelRootInBody(scope, recv); !ok {
		return
	}
	for i, arg := range e.Args {
		if i >= len(fn.Params) || fn.Params[i].Rest {
			return
		}
		id, ok := arg.(*ast.IdentExpr)
		if !ok {
			continue
		}
		dest, open := fn.Params[i].Type.(*soltype.TypeVarType)
		if !open || !typeReachesVar(c.info.TypeOf(recv), dest) {
			continue
		}
		if vid := c.varIDs.Of(id); vid > 0 {
			if c.fn.openEscapes == nil {
				c.fn.openEscapes = set.NewSet[liveness.VarID]()
			}
			c.fn.openEscapes.Add(vid)
		}
	}
}

// markOpenEscapeParams sets the ownership of each parameter in openEscapes that is a bare name
// with no type annotation, no default, and no Moved mark. A `mut` parameter stays owned and is
// marked Moved, so each call moves its argument. Any other such parameter's type T becomes
// `&'static T`, so each call lends its argument for good. params holds the FuncParams built
// from sigParams, one for one. It reads the body's context, so it runs once the body is walked
// and before the context is popped.
func (c *checker) markOpenEscapeParams(sigParams []*ast.Param, params []*soltype.FuncParam) {
	if c.fn == nil || c.fn.openEscapes.Len() == 0 {
		return
	}
	for i, p := range sigParams {
		ip, ok := p.Pattern.(*ast.IdentPat)
		if !ok || p.TypeAnn != nil || ip.Default != nil || i >= len(params) {
			continue
		}
		fp := params[i]
		if fp.Moved || fp.Rest || fp.Optional || !c.fn.openEscapes.Contains(c.varIDs.Of(ip)) {
			continue
		}
		v, isVar := fp.Type.(*soltype.TypeVarType)
		if !isVar {
			continue
		}
		if ip.Mutable {
			fp.Moved = true
			continue
		}
		fp.Type = soltype.NewRef(false, soltype.Static, v)
	}
}

// typeReachesVar reports whether v occurs in t, either in t's structure or in the bounds of a
// variable t reaches.
func typeReachesVar(t soltype.Type, v *soltype.TypeVarType) bool {
	if t == nil {
		return false
	}
	r := &varReacher{target: v, seen: set.NewSet[*soltype.TypeVarType]()}
	t.Accept(r, soltype.Positive)
	return r.found
}

// varReacher walks a type for typeReachesVar. It descends each variable's bounds itself,
// since the bounds are not children the type visitor walks.
type varReacher struct {
	target *soltype.TypeVarType
	seen   set.Set[*soltype.TypeVarType]
	found  bool
}

func (r *varReacher) EnterType(t soltype.Type, pol soltype.Polarity) soltype.EnterResult {
	v, ok := t.(*soltype.TypeVarType)
	if r.found || !ok {
		return soltype.EnterResult{SkipChildren: r.found}
	}
	if v == r.target {
		r.found = true
	}
	if r.seen.Contains(v) || r.found {
		return soltype.EnterResult{SkipChildren: true}
	}
	r.seen.Add(v)
	for _, b := range v.LowerBounds {
		b.Accept(r, pol)
	}
	for _, b := range v.UpperBounds {
		b.Accept(r, pol)
	}
	return soltype.EnterResult{SkipChildren: true}
}

func (r *varReacher) ExitType(t soltype.Type, _ soltype.Polarity) soltype.Type { return t }
