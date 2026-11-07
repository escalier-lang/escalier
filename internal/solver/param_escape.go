package solver

import (
	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/liveness"
	"github.com/escalier-lang/escalier/internal/set"
	"github.com/escalier-lang/escalier/internal/soltype"
)

// paramOnlyReadThrough reports whether body only reads through the parameter whose pattern is
// pat. That holds when every use of the parameter is the root of a member or element read, as
// in `p.x` or `p[i].y`, and the read's value goes to one of these places:
//   - a `return`, as in `return p.x`
//   - an expression statement that discards it, which excludes the last statement of a nested
//     block, since that statement is the value of an `if`, `do`, or `try` block
//   - an operand of an arithmetic, comparison, or unary operator, as in `p.x + 1`
//   - an operand of `&&`, `||`, or `??` whose own value goes to one of these places
//   - an immutable borrow, as in `&p.x`
//   - an `if` condition or a template literal interpolation
//   - the initializer of an alias
//
// An alias is a name that stands for a read through the parameter. A `val` that binds a single
// name to a read through the parameter or through an alias binds an alias, as `q` does in
// `val q = p.x`. A destructuring `val` whose initializer is the parameter, an alias, or a read
// through either binds each leaf of its pattern as an alias, as `y` does in `val {y} = p`. Each
// leaf of a destructuring parameter pattern is an alias too, as `x` is in `fn f({x})`. A `var`,
// a binding with a type annotation, a `mut` leaf, and a leaf with a default bind no alias. A
// destructuring `val` with a rest element binds no alias when its initializer is the parameter
// or an alias itself, since the rest then holds what is left of the whole value. A use of an
// alias is either a read through it, which goes to one of the places above, or a
// bare use whose own value goes to one of those places, as in `return q` or `q + 1`.
//
// It returns false for a rest, optional, or defaulted parameter. It returns false when the body
// never uses the parameter, and when the body binds the name of the parameter or of an alias
// again anywhere, since a use may then name the inner binding. Any use of either inside a
// nested function also returns false, since the nested body may capture it.
func paramOnlyReadThrough(pat ast.Pat, rest, optional bool, body *ast.Block) bool {
	if rest || optional {
		return false
	}
	v := &paramReadScanner{
		tracked:     set.NewSet[string](),
		aliasPats:   set.NewSet[ast.Node](),
		consumed:    set.NewSet[ast.Expr](),
		readThrough: set.NewSet[*ast.IdentExpr](),
		tails:       set.NewSet[ast.Stmt](),
	}
	switch p := pat.(type) {
	case *ast.IdentPat:
		if p.Default != nil {
			return false
		}
		v.param = p.Name
		v.tracked.Add(p.Name)
	case *ast.ObjectPat, *ast.TuplePat:
		leaves, ok := aliasLeaves(pat)
		if !ok {
			return false
		}
		v.tracked = leaves.names
	default:
		return false
	}
	body.Accept(v)
	return !v.other && v.uses > 0
}

// paramReadScanner walks a function body for paramOnlyReadThrough. The walk enters a node
// before its children, so each node marks its children before the scanner reaches them.
//
// param is the parameter's name, and is empty for a destructuring parameter. tracked holds
// param and the name of each alias the walk has bound. aliasPats holds each pattern node that
// binds an alias, so the check for a name bound again skips the alias's own binding.
//
// consumed holds each expression whose value goes to one of the places paramOnlyReadThrough
// lists. A member or element read passes the mark on to its object. readThrough holds each use
// of a tracked name that is the object of a consumed read, or the initializer of a
// destructuring alias. tails holds the last statement of each block nested in the body, and
// inBody records that the walk has entered the body itself. uses counts the uses of tracked
// names, and other records a use that is not a read through one.
type paramReadScanner struct {
	ast.DefaultVisitor
	param       string
	tracked     set.Set[string]
	aliasPats   set.Set[ast.Node]
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

func (v *paramReadScanner) EnterDecl(d ast.Decl) bool {
	if vd, ok := d.(*ast.VarDecl); ok {
		v.bindAliases(vd)
	}
	return !v.other
}

func (v *paramReadScanner) EnterExpr(e ast.Expr) bool {
	switch e := e.(type) {
	case *ast.IdentExpr:
		if v.tracked.Contains(e.Name) {
			v.uses++
			bareAlias := e.Name != v.param && v.consumed.Contains(e)
			if !v.readThrough.Contains(e) && !bareAlias {
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
		v.other = v.other || mentionsAny(v.tracked, e)
		return false
	}
	return !v.other
}

func (v *paramReadScanner) EnterObjExprElem(e ast.ObjExprElem) bool {
	if name, ok := shorthandName(e); ok && v.tracked.Contains(name) {
		v.other = true
	}
	return !v.other
}

func (v *paramReadScanner) EnterPat(p ast.Pat) bool {
	switch p := p.(type) {
	case *ast.IdentPat:
		v.other = v.other || (!v.aliasPats.Contains(p) && v.tracked.Contains(p.Name))
	case *ast.ObjectPat:
		for _, elem := range p.Elems {
			if sh, ok := elem.(*ast.ObjShorthandPat); ok && !v.aliasPats.Contains(sh) && v.tracked.Contains(sh.Key.Name) {
				v.other = true
			}
		}
	}
	return !v.other
}

// bindAliases adds the names d binds to tracked when d binds aliases, as paramOnlyReadThrough
// defines them, and marks d's initializer as consumed. A leaf that binds a tracked name again
// sets other instead. A declaration that binds no alias changes nothing.
func (v *paramReadScanner) bindAliases(d *ast.VarDecl) {
	if d.Kind != ast.ValKind || d.TypeAnn != nil || d.Else != nil || d.Init == nil {
		return
	}
	root, isRead := readRoot(d.Init)
	if root == nil || !v.tracked.Contains(root.Name) {
		return
	}
	if _, single := d.Pattern.(*ast.IdentPat); single && !isRead {
		return
	}
	leaves, ok := aliasLeaves(d.Pattern)
	// A rest element over a bare tracked name binds what is left of the whole value, so
	// returning it hands out the parameter itself, as `return p` does.
	if !ok || (leaves.hasRest && !isRead) {
		return
	}
	for _, name := range leaves.names.ToSlice() {
		if v.tracked.Contains(name) {
			v.other = true
			return
		}
	}
	v.tracked = v.tracked.Union(leaves.names)
	v.aliasPats = v.aliasPats.Union(leaves.nodes)
	if isRead {
		v.consume(d.Init)
	} else {
		v.readThrough.Add(root)
	}
}

// consume marks e as an expression whose value goes to a place that does not let it escape.
// A nil e, such as a bare `return`, marks nothing.
func (v *paramReadScanner) consume(e ast.Expr) {
	if e != nil {
		v.consumed.Add(e)
	}
}

// readObject passes the mark of the consumed read on to obj, the read's object. A use of a
// tracked name as that object is a read through it.
func (v *paramReadScanner) readObject(read, obj ast.Expr) {
	if !v.consumed.Contains(read) {
		return
	}
	v.consume(obj)
	if id, ok := obj.(*ast.IdentExpr); ok && v.tracked.Contains(id.Name) {
		v.readThrough.Add(id)
	}
}

// readRoot returns the identifier at the root of e and whether e reads through it. For a chain
// of member and element reads such as `p.x[0].y`, it returns `p` and true. For a bare
// identifier, it returns the identifier and false. For any other expression, it returns nil.
func readRoot(e ast.Expr) (*ast.IdentExpr, bool) {
	isRead := false
	for {
		switch ex := e.(type) {
		case *ast.MemberExpr:
			e = ex.Object
		case *ast.IndexExpr:
			e = ex.Object
		case *ast.IdentExpr:
			return ex, isRead
		default:
			return nil, false
		}
		isRead = true
	}
}

// patLeaves holds the names a pattern binds and the pattern node that binds each one, which is
// an IdentPat or an ObjShorthandPat. hasRest is true when the pattern holds a rest element.
type patLeaves struct {
	names   set.Set[string]
	nodes   set.Set[ast.Node]
	hasRest bool
}

// aliasLeaves returns the leaves pat binds. It reports false when pat holds a `mut` leaf, a leaf
// with a type annotation or a default, or a pattern other than a name, an object, a tuple, a
// rest element, or a wildcard.
func aliasLeaves(pat ast.Pat) (patLeaves, bool) {
	v := &aliasLeafCollector{
		leaves: patLeaves{names: set.NewSet[string](), nodes: set.NewSet[ast.Node]()},
		ok:     true,
	}
	pat.Accept(v)
	return v.leaves, v.ok
}

// aliasLeafCollector walks a pattern for aliasLeaves.
type aliasLeafCollector struct {
	ast.DefaultVisitor
	leaves patLeaves
	ok     bool
}

func (v *aliasLeafCollector) EnterPat(p ast.Pat) bool {
	switch p := p.(type) {
	case *ast.IdentPat:
		v.add(p.Name, p, p.Mutable || p.TypeAnn != nil || p.Default != nil)
	case *ast.ObjectPat:
		for _, elem := range p.Elems {
			switch elem := elem.(type) {
			case *ast.ObjShorthandPat:
				v.add(elem.Key.Name, elem, elem.Mutable || elem.TypeAnn != nil || elem.Default != nil)
			case *ast.ObjRestPat:
				v.leaves.hasRest = true
			case *ast.ObjKeyValuePat:
			default:
				v.ok = false
			}
		}
	case *ast.RestPat:
		v.leaves.hasRest = true
	case *ast.TuplePat, *ast.WildcardPat:
	default:
		v.ok = false
	}
	return v.ok
}

// add records a leaf named name that node binds. A leaf whose binding has extras, such as a
// `mut` marker, binds no alias, so it clears ok.
func (v *aliasLeafCollector) add(name string, node ast.Node, extras bool) {
	if extras {
		v.ok = false
		return
	}
	v.leaves.names.Add(name)
	v.leaves.nodes.Add(node)
}

// mentionsAny reports whether node reads a variable whose name is in names, either as an
// identifier or as a shorthand object property. It does not track scope, so a nested binding of
// the same name counts as a mention.
func mentionsAny(names set.Set[string], node interface{ Accept(ast.Visitor) }) bool {
	v := &nameMentionScanner{names: names}
	node.Accept(v)
	return v.found
}

type nameMentionScanner struct {
	ast.DefaultVisitor
	names set.Set[string]
	found bool
}

func (v *nameMentionScanner) EnterExpr(e ast.Expr) bool {
	if id, ok := e.(*ast.IdentExpr); ok && v.names.Contains(id.Name) {
		v.found = true
	}
	return !v.found
}

func (v *nameMentionScanner) EnterObjExprElem(e ast.ObjExprElem) bool {
	if name, ok := shorthandName(e); ok && v.names.Contains(name) {
		v.found = true
	}
	return !v.found
}

// shorthandName returns the name a shorthand property `{name}` reads, and false when e is not a
// shorthand property.
func shorthandName(e ast.ObjExprElem) (string, bool) {
	prop, ok := e.(*ast.PropertyExpr)
	if !ok || prop.Value != nil {
		return "", false
	}
	key, ok := prop.Name.(*ast.IdentExpr)
	if !ok {
		return "", false
	}
	return key.Name, true
}

// noteOpenEscapes adds to openEscapes the VarID of each argument of the call e that is a bare
// name passed to an open destination. An open destination is a parameter of a method called
// on a module-level binding, where the parameter's type is an inference variable that the
// receiver's own type reaches. fn is the signature the call resolved to, and nil when it
// resolved to none.
//
// In the module below, `seen.set("last", value)` is an open escape. `set`'s second parameter
// has type V, an inference variable that `seen`'s own type `Map<K, V>` reaches, and `seen` is
// a module-level binding. The call records `value`. The other two calls are not open escapes.
//
//	val mut seen = Map()
//	val m = {id: fn <T>(x: T) -> T { return x }}
//
//	fn remember(value) {
//	    seen.set("last", value) // open escape, which records value
//	    m.id(value)             // id's T is fresh for this call, so m's type does not reach it
//	    val mut local = Map()
//	    local.set("k", value)   // local is not a module-level binding
//	}
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
		// The body already passed v to the open destination, so the borrow flows into v
		// rather than replacing it. The destination then holds the borrow the caller lends.
		ref := soltype.NewRef(false, soltype.Static, c.freshAt(v.Level))
		c.constrain(ip, ref, v)
		fp.Type = ref
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
