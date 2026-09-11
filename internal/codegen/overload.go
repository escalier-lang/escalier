package codegen

import (
	"fmt"
	"slices"

	"github.com/escalier-lang/escalier/internal/ast"
)

// overloadArm is one arm of an overload set as codegen sees it. The three kinds of
// overloadable declaration — a top-level function, a method, and a constructor —
// differ in where their parameters and body hang off the AST, so each collects them
// into this shape and the dispatch below is written once.
//
// prelude runs ahead of the arm's parameter bindings. Only a derived class's
// constructor uses it, for the synthesized `super()` that has to lead the body.
type overloadArm struct {
	params  []*ast.Param
	body    *ast.Block
	async   bool
	gen     bool
	prelude []Stmt
	source  ast.Node
}

// overloadDispatch is the JS one overload set lowers to: a single member taking
// positional `param0…paramN` and a body that tests each arm's guards in turn.
type overloadDispatch struct {
	params    []*Param
	body      []Stmt
	async     bool
	generator bool
}

// buildOverloadDispatch lowers a set of overload arms to one JS member. The member
// takes as many positional parameters as the widest arm, and its body is an if-else
// chain testing each arm's parameter types with `typeof`. An arm that matches binds
// the names it wrote from those positions and runs its body. `describe` names the
// member in the `TypeError` the chain falls through to, as in "function 'area'".
//
// Arms are tried most specific first. Without an arity test, function subtyping lets
// a less specific arm match a more specific call, so a two-parameter arm has to be
// tested before a one-parameter arm that would also accept the call.
func (b *Builder) buildOverloadDispatch(arms []overloadArm, describe string) overloadDispatch {
	// A stable sort, so two arms the comparison calls equal keep the order they were
	// written in. The chain is first-match, and two unannotated arms both build the guard
	// `true`, so reordering a tied pair would change which body a call runs.
	sorted := slices.Clone(arms)
	slices.SortStableFunc(sorted, func(a, b overloadArm) int {
		if len(a.params) != len(b.params) {
			return len(b.params) - len(a.params)
		}
		return totalSpecificity(b.params) - totalSpecificity(a.params)
	})

	maxParams := 0
	for _, arm := range sorted {
		maxParams = max(maxParams, len(arm.params))
	}
	params := make([]*Param, 0, maxParams)
	for i := range maxParams {
		params = append(params, &Param{
			Pattern:  NewIdentPat(fmt.Sprintf("param%d", i), nil, nil),
			Optional: false,
			TypeAnn:  nil,
		})
	}

	var chain func(int) Stmt
	chain = func(idx int) Stmt {
		if idx >= len(sorted) {
			msg := "No overload matches the provided arguments for " + describe
			return NewThrowStmt(
				NewNewExpr(
					NewIdentExpr("TypeError", "", nil),
					[]Expr{NewLitExpr(NewStrLit(msg, nil), nil)},
					nil,
				),
				nil,
			)
		}
		arm := sorted[idx]
		armBody := slices.Concat(arm.prelude, b.buildArmBindings(arm))
		if len(arm.params) == 0 {
			// An arm taking nothing has no guard to write, so it always matches and
			// every later arm is unreachable.
			return NewBlockStmt(armBody, arm.source)
		}
		return NewIfStmt(guardFor(b, arm), NewBlockStmt(armBody, arm.source), chain(idx+1), arm.source)
	}

	dispatch := overloadDispatch{params: params, body: []Stmt{chain(0)}}
	for _, arm := range sorted {
		dispatch.async = dispatch.async || arm.async
		dispatch.generator = dispatch.generator ||
			arm.gen || (arm.body != nil && containsYield(arm.body.Stmts))
	}
	return dispatch
}

// buildArmBindings returns the statements one arm runs: the bindings that give its
// own parameter names the values at `param0…paramN`, followed by its body.
func (b *Builder) buildArmBindings(arm overloadArm) []Stmt {
	prevInBlockScope := b.inBlockScope
	b.inBlockScope = true
	defer func() { b.inBlockScope = prevInBlockScope }()

	var stmts []Stmt
	for i, param := range arm.params {
		slot := NewIdentExpr(fmt.Sprintf("param%d", i), "", nil)
		pattern, def := splitIdentDefault(param.Pattern)
		if def != nil {
			stmts = slices.Concat(stmts, b.bindWithDefault(pattern.(*ast.IdentPat), slot, def))
			continue
		}
		// A rest parameter binds the whole slot rather than gathering the slots after
		// it. The dispatch member takes a fixed number of positional parameters, so
		// there is nothing to gather from, and the guard the arm is chosen by already
		// tests that one slot for an array. escalier-lang/escalier#1545 is what gives
		// the dispatch an arity of its own so a rest arm can take a spread call.
		if rest, isRest := pattern.(*ast.RestPat); isRest {
			pattern = rest.Pattern
		}
		// buildPattern rather than a plain assignment, so a destructuring parameter
		// binds the same way it does outside an overload set.
		_, patternStmts := b.buildPattern(pattern, slot, false, ast.ValKind, "")
		stmts = slices.Concat(stmts, patternStmts)
	}
	if arm.body == nil {
		return stmts
	}
	return slices.Concat(stmts, b.buildStmts(arm.body.Stmts))
}

// splitIdentDefault separates a parameter's `= expr` default from the pattern that
// binds it, and returns a nil default for a pattern carrying none. The returned
// pattern is the same one with the default removed, so binding it does not re-emit
// the default expression where an assignment target belongs.
func splitIdentDefault(pattern ast.Pat) (ast.Pat, ast.Expr) {
	ident, isIdent := pattern.(*ast.IdentPat)
	if !isIdent || ident.Default == nil {
		return pattern, nil
	}
	bare := ast.NewIdentPat(ident.Name, ident.Mutable, ident.TypeAnn, nil, ident.Span())
	return bare, ident.Default
}

// bindWithDefault binds ident to slot, falling back to def when the caller left that
// position out. It is written as a conditional rather than a JS parameter default so
// the dispatch member's own parameter names stay free for the rename pass, which is
// what buildParams does for an ordinary parameter.
func (b *Builder) bindWithDefault(ident *ast.IdentPat, slot Expr, def ast.Expr) []Stmt {
	defExpr, defStmts := b.buildExpr(def, nil)
	present := NewBinaryExpr(
		NewUnaryExpr(TypeOf, slot, nil),
		StrictNotEqual,
		NewLitExpr(NewStrLit("undefined", nil), nil),
		nil,
	)
	decl := &VarDecl{
		Kind: ValKind,
		Decls: []*Declarator{{
			Pattern: NewIdentPat(ident.Name, nil, ident),
			TypeAnn: nil,
			Init:    NewCondExpr(present, slot, defExpr, nil),
		}},
		declare: false,
		export:  false,
		span:    nil,
		source:  ident,
	}
	return slices.Concat(defStmts, []Stmt{&DeclStmt{Decl: decl, span: nil, source: ident}})
}

// guardFor builds the test that decides whether arm takes a call, the `typeof` checks
// for its annotated parameters joined by `&&`. An arm whose parameters carry no
// annotation has nothing to test and takes every call that reaches it.
//
// A parameter the caller may leave out — one written `x?` or one carrying a default —
// also accepts an absent slot. The slot holds `undefined` there, which no type guard
// admits, so without this an arm such as `fn f(x: number = 5)` would be passed over for
// the call `f()` that is exactly its own, and the dispatch would fall through to the
// TypeError. The binding bindWithDefault emits for that parameter would then never run.
func guardFor(b *Builder, arm overloadArm) Expr {
	var guards []Expr
	for i, param := range arm.params {
		if param.TypeAnn == nil {
			continue
		}
		slot := NewIdentExpr(fmt.Sprintf("param%d", i), "", nil)
		guard := b.buildTypeGuard(slot, param.TypeAnn)
		if _, def := splitIdentDefault(param.Pattern); param.Optional || def != nil {
			guard = NewBinaryExpr(absentSlot(slot), LogicalOr, guard, nil)
		}
		guards = append(guards, guard)
	}
	if len(guards) == 0 {
		return NewLitExpr(NewBoolLit(true, nil), nil)
	}
	guard := guards[0]
	for _, g := range guards[1:] {
		guard = NewBinaryExpr(guard, LogicalAnd, g, nil)
	}
	return guard
}

// absentSlot builds the test for a dispatch slot the caller left out, which holds
// `undefined`.
func absentSlot(slot Expr) Expr {
	return NewBinaryExpr(
		NewUnaryExpr(TypeOf, slot, nil),
		StrictEqual,
		NewLitExpr(NewStrLit("undefined", nil), nil),
		nil,
	)
}

// totalSpecificity scores a parameter list for the sort above. An object type scores
// its required properties, since an object with more of them is the harder of two to
// satisfy. Every other annotation scores one, and an unannotated parameter zero.
func totalSpecificity(params []*ast.Param) int {
	total := 0
	for _, param := range params {
		if param.TypeAnn == nil {
			continue
		}
		obj, isObj := param.TypeAnn.(*ast.ObjectTypeAnn)
		if !isObj {
			total++
			continue
		}
		for _, elem := range obj.Elems {
			if prop, isProp := elem.(*ast.PropertyTypeAnn); isProp && !prop.Optional {
				total++
			}
		}
	}
	return total
}

// constructorGroupKey is the grouping key every ConstructorElem in a class shares. A
// constructor has no name of its own, so one class has at most one such group.
const constructorGroupKey = "constructor"

// methodGroupKey returns the key a method's overload set is collected under. Two
// methods overload each other when they agree on name and on whether they are static,
// so `f` and `static f` are separate members and separate groups.
//
// A computed key gets a key of its own per declaration, since what it names is not
// known until the expression runs and two of them cannot be shown to collide.
func methodGroupKey(m *ast.MethodElem) string {
	prefix := "method:"
	if m.Static {
		prefix = "static method:"
	}
	switch name := m.Name.(type) {
	case *ast.IdentExpr:
		return prefix + name.Name
	case *ast.StrLit:
		return prefix + name.Value
	case *ast.NumLit:
		return fmt.Sprintf("%s%v", prefix, name.Value)
	default:
		return fmt.Sprintf("%s%p", prefix, m)
	}
}

// groupOverloadableElems collects a class body's methods and constructors by the key
// they overload under, keeping each group in source order. A group of more than one is
// an overload set the caller emits as a single JS member. It emits at the group's first
// element, so the member lands where the set was first written.
func groupOverloadableElems(inElems []ast.ClassElem) map[string][]ast.ClassElem {
	groups := map[string][]ast.ClassElem{}
	for _, elem := range inElems {
		switch e := elem.(type) {
		case *ast.MethodElem:
			if e.Fn != nil {
				groups[methodGroupKey(e)] = append(groups[methodGroupKey(e)], elem)
			}
		case *ast.ConstructorElem:
			if e.Fn != nil {
				groups[constructorGroupKey] = append(groups[constructorGroupKey], elem)
			}
		}
	}
	return groups
}

// methodArms collects one method overload set into dispatch arms. A method's receiver
// lives on MethodElem.Receiver rather than in Fn.Params, so every parameter here is a
// value parameter and `this` needs no dispatch position.
//
// A body-less arm contributes a signature and no code, so it gets no branch. Giving it
// one would put a guard in front of a body that returns undefined, ahead of an arm that
// has code to run.
func methodArms(siblings []ast.ClassElem) []overloadArm {
	arms := make([]overloadArm, 0, len(siblings))
	for _, sibling := range siblings {
		m := sibling.(*ast.MethodElem)
		if m.Fn.Body == nil {
			continue
		}
		arms = append(arms, overloadArm{
			params: m.Fn.Params,
			body:   m.Fn.Body,
			async:  m.Fn.Async,
			gen:    m.Fn.Gen,
			source: m,
		})
	}
	return arms
}

// constructorArms collects one constructor overload set into dispatch arms. Unlike a
// method, a constructor carries its `mut self` receiver as Fn.Params[0], which is
// `this` at the JS level and not a callable parameter, so it is dropped here.
//
// Each arm gets its own synthesized `super()` when the class is derived and that arm
// writes none itself, which keeps the call inside the branch that runs. A body-less arm
// is dropped for the reason methodArms gives.
func constructorArms(siblings []ast.ClassElem, derived bool) []overloadArm {
	arms := make([]overloadArm, 0, len(siblings))
	for _, sibling := range siblings {
		ctor := sibling.(*ast.ConstructorElem)
		if ctor.Fn.Body == nil {
			continue
		}
		params := ctor.Fn.Params
		if len(params) > 0 {
			params = params[1:]
		}
		var prelude []Stmt
		if derived && !hasSuperCall(ctor.Fn.Body) {
			prelude = []Stmt{&ExprStmt{
				Expr:   NewCallExpr(NewIdentExpr("super", "", ctor), nil, false, ctor),
				span:   nil,
				source: ctor,
			}}
		}
		arms = append(arms, overloadArm{
			params:  params,
			body:    ctor.Fn.Body,
			async:   ctor.Fn.Async,
			gen:     ctor.Fn.Gen,
			prelude: prelude,
			source:  ctor,
		})
	}
	return arms
}

// buildOverloadedMethod lowers a method overload set to one JS method, or nil for a
// set whose arms all lack a body and so emit no code. first is the arm the member's
// modifiers are taken from; every arm in a set agrees on them, since the key they
// group under carries `static` and the checker rejects a set that disagrees on the
// rest.
func (b *Builder) buildOverloadedMethod(name ObjKey, first *ast.MethodElem, arms []overloadArm) ClassElem {
	if len(arms) == 0 {
		return nil
	}
	dispatch := b.buildOverloadDispatch(arms, "method '"+objKeyName(name)+"'")
	return NewMethodElem(
		name,
		dispatch.params,
		dispatch.body,
		MethodElemOptions{
			Static:    first.Static,
			Private:   first.Private,
			Async:     dispatch.async,
			Generator: dispatch.generator,
		},
		first,
	)
}

// buildOverloadedConstructor lowers a constructor overload set to one JS constructor,
// or nil for a set whose arms all lack a body.
func (b *Builder) buildOverloadedConstructor(first *ast.ConstructorElem, arms []overloadArm) ClassElem {
	if len(arms) == 0 {
		return nil
	}
	dispatch := b.buildOverloadDispatch(arms, "constructor")
	return NewMethodElem(
		NewIdentExpr("constructor", "", first),
		dispatch.params,
		dispatch.body,
		MethodElemOptions{},
		first,
	)
}

// objKeyName renders a member key for the TypeError message the dispatch chain falls
// through to. A computed key has no name until the expression runs, so the message
// says only that a member was reached with arguments no arm takes.
func objKeyName(key ObjKey) string {
	switch k := key.(type) {
	case *IdentExpr:
		return k.Name
	case *StrLit:
		return k.Value
	case *NumLit:
		return fmt.Sprintf("%v", k.Value)
	default:
		return "<computed>"
	}
}
