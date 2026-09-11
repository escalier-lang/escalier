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
		// A rest parameter gathers every argument from its own position onward. The
		// dispatch member declares a fixed number of positional parameters, so the tail
		// past the last of them is reachable only through `arguments`. That binding
		// exists in every function this backend emits, which writes no arrow functions.
		if rest, isRest := pattern.(*ast.RestPat); isRest {
			_, patternStmts := b.buildPattern(
				rest.Pattern, argumentsFrom(i), false, ast.ValKind, "")
			stmts = slices.Concat(stmts, patternStmts)
			continue
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

// guardFor builds the test that decides whether arm takes a call: how many arguments it
// accepts, the `typeof` checks for its annotated parameters, and for a rest parameter the
// element type its tail must hold. The clauses join with `&&`, and an arm with nothing to
// test takes every call that reaches it.
//
// The count is tested because the chain is first-match over arms sorted by parameter
// count. Without it an arm of two parameters takes `f(1, 2, 3)` and drops the third,
// leaving a rest arm that should have taken the call unreachable.
//
// A parameter the caller may leave out — one written `x?` or one carrying a default —
// also accepts an absent slot. The slot holds `undefined` there, which no type guard
// admits, so without this an arm such as `fn f(x: number = 5)` would be passed over for
// the call `f()` that is exactly its own, and the dispatch would fall through to the
// TypeError. The binding bindWithDefault emits for that parameter would then never run.
func guardFor(b *Builder, arm overloadArm) Expr {
	restIdx, elemAnn, hasRest := restParam(arm.params)
	guards := arityGuards(len(arm.params), requiredCount(arm.params), hasRest)
	for i, param := range arm.params {
		if hasRest && i == restIdx {
			continue
		}
		if param.TypeAnn == nil {
			continue
		}
		slot := NewIdentExpr(fmt.Sprintf("param%d", i), "", nil)
		guard := b.buildTypeGuard(slot, param.TypeAnn)
		// A type with no runtime test builds `true`, which the count test beside it
		// already implies.
		if isTrueLiteral(guard) {
			continue
		}
		if _, def := splitIdentDefault(param.Pattern); param.Optional || def != nil {
			guard = NewBinaryExpr(absentSlot(slot), LogicalOr, guard, nil)
		}
		guards = append(guards, guard)
	}
	if hasRest && elemAnn != nil {
		if elemGuard := b.restTailGuard(restIdx, elemAnn); elemGuard != nil {
			guards = append(guards, elemGuard)
		}
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

// restParam returns the position of arm's rest parameter, the element type its tail
// holds, and whether the arm has one. The element type comes from an `Array<T>`
// annotation and is nil for a rest parameter annotated any other way.
func restParam(params []*ast.Param) (int, ast.TypeAnn, bool) {
	for i, param := range params {
		if _, isRest := param.Pattern.(*ast.RestPat); !isRest {
			continue
		}
		return i, restElemTypeAnn(param.TypeAnn), true
	}
	return 0, nil, false
}

// restElemTypeAnn returns the element type of a rest parameter's `Array<T>`
// annotation, and nil when the annotation is anything else.
func restElemTypeAnn(typeAnn ast.TypeAnn) ast.TypeAnn {
	if mut, isMut := typeAnn.(*ast.MutableTypeAnn); isMut {
		typeAnn = mut.Target
	}
	ref, isRef := typeAnn.(*ast.TypeRefTypeAnn)
	if !isRef || !isArrayTypeRef(ref) || len(ref.TypeArgs) != 1 {
		return nil
	}
	return ref.TypeArgs[0]
}

// requiredCount counts the leading parameters a caller has to pass. A parameter
// written `x?` or carrying a default may be left out, and a rest parameter ends the
// count because it accepts any number of arguments including none.
func requiredCount(params []*ast.Param) int {
	count := 0
	for _, param := range params {
		if _, isRest := param.Pattern.(*ast.RestPat); isRest {
			break
		}
		if _, def := splitIdentDefault(param.Pattern); param.Optional || def != nil {
			break
		}
		count++
	}
	return count
}

// arityGuards builds the tests on `arguments.length` for an arm of n parameters, of
// which the first `required` have to be passed. An arm with a rest parameter has no
// upper bound, so only the lower one is tested, and an arm whose parameters are all
// required collapses to a single equality test. A bound every call already meets is
// left out.
func arityGuards(n int, required int, hasRest bool) []Expr {
	length := NewMemberExpr(
		NewIdentExpr("arguments", "", nil), NewIdentifier("length", nil), false, nil)
	count := func(value int) Expr {
		return NewLitExpr(NewNumLit(float64(value), nil), nil)
	}
	if hasRest {
		if required == 0 {
			return nil
		}
		return []Expr{NewBinaryExpr(length, GreaterThanEqual, count(required), nil)}
	}
	if required == n {
		return []Expr{NewBinaryExpr(length, StrictEqual, count(n), nil)}
	}
	guards := []Expr{NewBinaryExpr(length, LessThanEqual, count(n), nil)}
	if required > 0 {
		lower := NewBinaryExpr(length, GreaterThanEqual, count(required), nil)
		guards = append([]Expr{lower}, guards...)
	}
	return guards
}

// restTailGuard builds the test that every argument a rest parameter gathers holds
// its element type, as `Array.prototype.every.call(arguments, ...)`. `prefix` is the
// rest parameter's position, and the arguments before it are exempted by index since
// their own guards already cover them. The result is nil when the element type has no
// runtime test, which would otherwise emit a callback that always returns `true`.
func (b *Builder) restTailGuard(prefix int, elemAnn ast.TypeAnn) Expr {
	elem := NewIdentExpr("elem", "", nil)
	test := b.buildTypeGuard(elem, elemAnn)
	if isTrueLiteral(test) {
		return nil
	}
	params := []*Param{{
		Pattern:  NewIdentPat("elem", nil, nil),
		Optional: false,
		TypeAnn:  nil,
	}}
	if prefix > 0 {
		params = append(params, &Param{
			Pattern:  NewIdentPat("index", nil, nil),
			Optional: false,
			TypeAnn:  nil,
		})
		before := NewBinaryExpr(
			NewIdentExpr("index", "", nil),
			LessThan,
			NewLitExpr(NewNumLit(float64(prefix), nil), nil),
			nil,
		)
		test = NewBinaryExpr(before, LogicalOr, test, nil)
	}
	body := []Stmt{&ReturnStmt{Expr: test, span: nil, source: nil}}
	callback := NewFuncExpr(params, body, FuncExprOptions{Async: false, Generator: false}, nil)
	return arrayProtoCall("every", []Expr{NewIdentExpr("arguments", "", nil), callback})
}

// argumentsFrom builds the array of every argument from position i onward, which is
// what a rest parameter binds. `Array.prototype.slice` is generic, so it reads the
// array-like `arguments` without a copy first.
func argumentsFrom(i int) Expr {
	args := []Expr{NewIdentExpr("arguments", "", nil)}
	if i > 0 {
		args = append(args, NewLitExpr(NewNumLit(float64(i), nil), nil))
	}
	return arrayProtoCall("slice", args)
}

// arrayProtoCall builds `Array.prototype.<method>.call(...)`, which runs an array
// method on the array-like `arguments`.
func arrayProtoCall(method string, args []Expr) Expr {
	arrayProto := NewMemberExpr(
		NewIdentExpr("Array", "", nil), NewIdentifier("prototype", nil), false, nil)
	fn := NewMemberExpr(arrayProto, NewIdentifier(method, nil), false, nil)
	callee := NewMemberExpr(fn, NewIdentifier("call", nil), false, nil)
	return NewCallExpr(callee, args, false, nil)
}
