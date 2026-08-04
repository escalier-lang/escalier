package solver

import (
	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/set"
	"github.com/escalier-lang/escalier/internal/soltype"
)

// resolveTypeParams resolves a `<…>` type-parameter list to soltype TypeParams in three passes,
// since a default and a bound need opposite visibility. Pass 1 mints one fresh var per parameter.
// Pass 2 resolves each parameter's default and then declares that parameter, so a default reads
// only the earlier siblings, the ones instantiation can substitute for it. Pass 3 resolves each
// constraint into its var's upper bound against the full list, so a forward `<T: U, U>`, a mutual
// `<T: U, U: T>`, and an F-bound `<T: Foo<T>>` all resolve. The result stays in declaration order,
// and the alias, class, enum, and function-annotation paths all route through here.
func (c *checker) resolveTypeParams(scope *Scope, lvl int, params []*ast.TypeParam) []*soltype.TypeParam {
	c.reportRequiredAfterDefault(params)
	out := make([]*soltype.TypeParam, len(params))
	// Pass 1: mint each parameter's var. Nothing is declared yet, so pass 2 controls which
	// siblings each default can see.
	for i, p := range params {
		out[i] = &soltype.TypeParam{Name: p.Name, Var: c.freshAt(lvl)}
	}
	// Pass 2: resolve each default under the names declared so far, then declare this
	// parameter. A default naming a later sibling or itself finds the name undeclared.
	for i, p := range params {
		if p.Default != nil && !c.reportDefaultForwardRef(params, i) {
			if dt, ok := c.resolveTypeAnn(scope, p.Default, lvl); ok {
				out[i].Default = dt
			}
		}
		scope.defineType(p.Name, TypeBinding{Type: out[i].Var})
	}
	// Pass 3: resolve each constraint into its var's upper bound, now that every sibling name
	// is in scope.
	for i, p := range params {
		if p.Constraint == nil {
			continue
		}
		if ct, ok := c.resolveTypeAnn(scope, p.Constraint, lvl); ok {
			c.ctx.addUpperBound(out[i].Var, ct)
			// Keep the declared constraint where later solving cannot overwrite it. The var's
			// upper-bound list grows as constraints flow in, so a reader that wants what the
			// source wrote reads this field instead.
			out[i].Constraint = ct
		}
	}
	return out
}

// typeParamArity is how many type arguments a reference to a declaration may write. Total is
// the whole parameter list and Required counts the parameters with no default, so a valid
// reference writes anywhere from Required to Total arguments.
//
// It is carried separately from the resolved parameters because the two become available at
// different times on the class path. A class's identity is registered before its parameters
// are resolved, and a reference reaching it in that window still has to be counted, so the
// arity is read off the declaration's `<…>` clause when the identity is registered.
type typeParamArity struct {
	Required int
	Total    int
}

// requiredArgCount returns how many arguments a reference must write for a parameter list of
// length total, where hasDefault reports whether the parameter at an index carries one.
//
// This is one past the last parameter with no default, NOT the number of parameters that lack
// one. Arguments bind positionally, so an argument can be omitted only when every parameter
// from that position on has a default to fill it. A default written before a required
// parameter, the `T = number` of `<T = number, U>`, can therefore never be omitted, and
// counting it as optional would let `Pair<string>` pass while leaving `U` a fresh variable that
// coalesces to `never`. resolveTypeParams reports such a declaration.
func requiredArgCount(total int, hasDefault func(int) bool) int {
	for i := total - 1; i >= 0; i-- {
		if !hasDefault(i) {
			return i + 1
		}
	}
	return 0
}

// arityOfParams reads the argument-count range off a resolved parameter list.
func arityOfParams(params []*soltype.TypeParam) typeParamArity {
	return typeParamArity{
		Required: requiredArgCount(len(params), func(i int) bool { return params[i].Default != nil }),
		Total:    len(params),
	}
}

// arityOfParamDecls reads the argument-count range straight off a declaration's `<…>` clause,
// before the parameters themselves are resolved. It counts the same two numbers
// arityOfParams reads off the resolved list, since a parameter's `= …` clause is what makes it
// optional and resolving the clause does not change whether it is there.
func arityOfParamDecls(params []*ast.TypeParam) typeParamArity {
	return typeParamArity{
		Required: requiredArgCount(len(params), func(i int) bool { return params[i].Default != nil }),
		Total:    len(params),
	}
}

// resolveTypeArgs pairs the `<…>` type arguments a reference wrote with the parameters of the
// declaration it names, and returns one argument per parameter. It is the instantiation twin
// of resolveTypeParams, and the alias, enum, and class reference paths all route through it,
// so a defaulted or miscounted reference resolves the same way whichever sort it names.
//
// Three things happen here. The written count is checked against arity, and an out-of-range
// count is reported. Every written argument is resolved, so an unresolvable annotation reports
// even in a position past the parameter count. An argument the reference omitted is filled
// from its parameter's default, with the arguments already resolved substituted in, so the
// `U = T` of `type Pair<T, U = T>` resolves `Pair<number>` to `Pair<number, number>`.
//
// An omitted argument with no default to fill it recovers to a fresh var, and so does one
// whose annotation failed to resolve, so a downstream reference always has one argument per
// parameter to substitute and never picks up a var belonging to the declaration.
//
// params may be shorter than arity.Total, which is how a class whose parameters are not
// resolved yet reaches here. Those positions have no default to read, so each one recovers to
// a fresh var. The result is nil when arity.Total is zero, and any arguments the reference
// wrote past the parameter count are resolved, reported, and then dropped.
func (c *checker) resolveTypeArgs(
	scope *Scope,
	ref *ast.TypeRefTypeAnn,
	kind TypeDeclKind,
	params []*soltype.TypeParam,
	arity typeParamArity,
	lvl int,
) []soltype.Type {
	got := len(ref.TypeArgs)
	if got < arity.Required || got > arity.Total {
		c.report(&TypeArgArityMismatchError{
			Ref:      ref,
			Kind:     kind,
			Name:     ast.QualIdentToString(ref.Name),
			Required: arity.Required,
			Total:    arity.Total,
			Got:      got,
		})
	}
	// Resolve every written argument, including any past the parameter count. A surplus
	// argument is dropped below, but resolving it first is what reports an unresolvable name
	// inside it rather than swallowing the diagnostic along with the argument.
	written := make([]soltype.Type, got)
	for i, arg := range ref.TypeArgs {
		if resolved, ok := c.resolveTypeAnn(scope, arg, lvl); ok {
			written[i] = resolved
		} else {
			written[i] = c.freshAt(lvl)
		}
	}
	if arity.Total == 0 {
		return nil
	}
	args := make([]soltype.Type, arity.Total)
	for i := range arity.Total {
		switch {
		case i < got:
			args[i] = written[i]
		case i < len(params) && params[i].Default != nil:
			// The default may reference an earlier parameter, as `U = T` does, so substitute
			// the arguments already resolved for parameters before this one.
			subst := newTypeSubst(params[:i], args[:i], nil, nil)
			args[i] = params[i].Default.Accept(subst, soltype.Positive)
		default:
			// A required argument was omitted, already reported as an arity mismatch, or the
			// parameter list is not resolved yet so there is no default to read. Recover to a
			// fresh var so every parameter has an argument to substitute.
			args[i] = c.freshAt(lvl)
		}
	}
	return args
}

// reportRequiredAfterDefault reports each default that a later parameter with no default makes
// unusable, the `T = number` of `<T = number, U>`. Arguments bind positionally, so omitting the
// argument for `T` would leave `U` reading the argument written for `T`. A reference therefore
// has to write every argument up to the last required parameter, which is what requiredArgCount
// computes, and a default before that point can never be reached.
//
// One report per such default, blaming the `= …` annotation and naming the first required
// parameter that follows it. Blaming each default rather than each required parameter names
// exactly the annotations that have to change, and the fix is to drop the default or to give
// every parameter after it one.
//
// The default is kept rather than dropped, so a reference that does write every argument still
// resolves against a full parameter list.
func (c *checker) reportRequiredAfterDefault(params []*ast.TypeParam) {
	required := requiredArgCount(len(params), func(i int) bool { return params[i].Default != nil })
	for i, p := range params[:required] {
		if p.Default == nil {
			continue
		}
		// params[required-1] has no default, so a later one always exists to name.
		target := params[required-1]
		for _, later := range params[i+1 : required] {
			if later.Default == nil {
				target = later
				break
			}
		}
		c.report(&TypeParamRequiredAfterDefaultError{
			Default: p.Default,
			Param:   p.Name,
			Target:  target.Name,
		})
	}
}

// reportDefaultForwardRef reports each parameter params[i]'s default names that it may not,
// params[i] itself or one declared after it, and returns whether it reported. A reference fills an
// omitted argument from the default with the earlier arguments substituted in, as
// resolveTypeArgs does for the `U = T` of `type Pair<T, U = T>`. A later or self reference has
// nothing to substitute, so it stays the declaration's own var, shared by every reference to the
// type. A rejected default is dropped, which leaves the parameter required, so a reference that
// omits it reports an arity mismatch alongside this error.
//
// One report per offending name, blaming its leftmost reference. Naming each one lets a default
// that reaches two later parameters be fixed in a single pass, while a name written twice reports
// once, since both references need the same fix.
func (c *checker) reportDefaultForwardRef(params []*ast.TypeParam, i int) bool {
	forbidden := set.NewSet[string]()
	for _, p := range params[i:] {
		forbidden.Add(p.Name)
	}
	var scan typeParamRefScan
	params[i].Default.Accept(&scan)
	reported := set.NewSet[string]()
	for _, ref := range scan.free {
		name := ast.QualIdentToString(ref.Name)
		if !forbidden.Contains(name) || reported.Contains(name) {
			continue
		}
		reported.Add(name)
		c.report(&TypeParamDefaultForwardRefError{Ref: ref, Param: params[i].Name, Target: name})
	}
	return reported.Len() > 0
}

// typeParamRefScan collects a type annotation's free references, the `Name` and `Name<…>`
// references that no binder written inside the annotation declares. It tracks the region each
// binder covers: a `fn <U>(…)` quantifier covers its function annotation, a mapped key covers the
// object annotation holding it, and an `infer U` covers its conditional's Extends and Then
// operands, matching where resolveCondTypeAnn declares the name.
type typeParamRefScan struct {
	ast.DefaultVisitor
	scopes []set.Set[string]     // one name set per region the walk is inside, innermost last
	free   []*ast.TypeRefTypeAnn // in traversal order, so a caller reports the leftmost
}

func (v *typeParamRefScan) EnterTypeAnn(t ast.TypeAnn) bool {
	switch n := t.(type) {
	case *ast.TypeRefTypeAnn:
		if !v.shadowed(ast.QualIdentToString(n.Name)) {
			v.free = append(v.free, n)
		}
	case *ast.FuncTypeAnn:
		names := set.NewSet[string]()
		for _, tp := range n.TypeParams {
			names.Add(tp.Name)
		}
		v.push(names)
	case *ast.ObjectTypeAnn:
		// A mapped type is an object element rather than a type annotation of its own, so the
		// walk reaches its key constraint, name, and value without entering the mapped node.
		// Read the key binder off the element here and scope it to the object annotation.
		names := set.NewSet[string]()
		for _, elem := range n.Elems {
			if mapped, ok := elem.(*ast.MappedTypeAnn); ok {
				names.Add(mapped.TypeParam.Name)
			}
		}
		v.push(names)
	case *ast.CondTypeAnn:
		// The four operands are walked here rather than by the shared descent, since an
		// `infer U` clause covers only two of them. Check reads no capture, and a capture
		// named again in Else is a free reference.
		n.Check.Accept(v)
		v.push(set.FromSlice(inferAnnNames(n.Extends)))
		n.Extends.Accept(v)
		n.Then.Accept(v)
		v.pop()
		n.Else.Accept(v)
		return false
	}
	return true
}

// ExitTypeAnn closes the region opened on entering a function or object annotation. A
// conditional pops its own region inside EnterTypeAnn, since it drives its operands itself.
func (v *typeParamRefScan) ExitTypeAnn(t ast.TypeAnn) {
	switch t.(type) {
	case *ast.FuncTypeAnn, *ast.ObjectTypeAnn:
		v.pop()
	}
}

func (v *typeParamRefScan) push(names set.Set[string]) {
	v.scopes = append(v.scopes, names)
}

func (v *typeParamRefScan) pop() {
	v.scopes = v.scopes[:len(v.scopes)-1]
}

// shadowed reports whether any region the walk is inside declares name.
func (v *typeParamRefScan) shadowed(name string) bool {
	for _, names := range v.scopes {
		if names.Contains(name) {
			return true
		}
	}
	return false
}
