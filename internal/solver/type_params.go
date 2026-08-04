package solver

import (
	"slices"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/set"
	"github.com/escalier-lang/escalier/internal/soltype"
)

// resolveTypeParams resolves a `<…>` type-parameter list to soltype TypeParams in four passes,
// since a default and a bound need opposite visibility. Pass 1 mints one fresh var per parameter.
// Pass 2 resolves each parameter's default and then declares that parameter, so a default reads
// only the earlier siblings, the ones instantiation can substitute for it. Pass 3 resolves each
// constraint into its var's upper bound against the full list, so a forward `<T: U, U>`, a mutual
// `<T: U, U: T>`, and an F-bound `<T: Foo<T>>` all resolve. Pass 4 checks each default against
// its own parameter's constraint. The result stays in declaration order, and the alias, class,
// enum, and function-annotation paths all route through here.
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
	// Pass 4: check each default against its own parameter's bound, so `<T: string = number>`
	// is rejected at the declaration. A default fills the argument at every use site that
	// omits it, so a default outside the bound would supply an argument the bound forbids.
	// This runs as its own pass because a bound is not resolved until pass 3, after the
	// default it is compared against.
	for i, p := range params {
		if out[i].Default == nil || out[i].Constraint == nil {
			continue
		}
		// Trial the comparison under a probe rather than running it live, so any bound it
		// appends is rolled back. A default is a fully resolved type with nothing left to
		// infer, so a live comparison would gain it nothing. It would cost something when the
		// bound names a sibling parameter. Running `<T, U: T = number>` live compares number
		// against T's var and leaves T carrying number as a lower bound, a claim that T must
		// accept number that the source never wrote.
		c.blameConstraintErrors(p.Default, c.ctx.trialUnderProbe(out[i].Default, out[i].Constraint))
	}
	return out
}

// typeParamArity is how many type arguments a reference to a declaration may write, anywhere
// from Required to Total. It is carried separately from the resolved parameters because a
// class registers its identity, and so its count, before its parameters resolve.
type typeParamArity struct {
	Required int
	Total    int
}

// requiredArgCount returns how many arguments a reference must write: one past the last
// parameter with no default, not the number lacking one. Arguments bind positionally, so a
// default before a required parameter can never be omitted, which resolveTypeParams reports.
// hasDefault is a callback because a parameter list reaches this as either sort of TypeParam.
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
// declaration it names, returning one per parameter or nil when there are none. Shared by the
// alias, enum, and class paths, it reports an out-of-range count, fills an omitted argument
// from its default, and recovers anything left to a fresh var. A class whose parameters have
// not resolved yet passes params shorter than arity.Total, so those positions recover too.
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

// reportRequiredAfterDefault reports each default a later parameter with no default makes
// unusable, the `T = number` of `<T = number, U>`. One report per such default, blaming the
// `= …` annotation and naming the first required parameter after it, so the reports name
// exactly the annotations that have to change. The default is kept rather than dropped, so a
// reference that does write every argument still resolves against a full parameter list.
func (c *checker) reportRequiredAfterDefault(params []*ast.TypeParam) {
	required := arityOfParamDecls(params).Required
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

// checkTypeArgBounds reports a type argument that does not satisfy its parameter's declared
// bound, so `class Box<T: string>` and `type Box<T: string>` both reject `Box<number>`. Every
// generic class, enum, and alias reference routes through here, so the rule reads the same on
// all three. Arguments are substituted into the bound first, which lets a bound name a sibling
// as the `B: A` of `<A, B: A>` does. The comparison is live rather than a discarded trial, so
// an argument that is itself a variable carries the bound to its instantiation the way a
// function call's does.
func (c *checker) checkTypeArgBounds(
	params []*soltype.TypeParam,
	args []soltype.Type,
	ltParams []*soltype.LifetimeParam,
	ltArgs []soltype.Lifetime,
	ref *ast.TypeRefTypeAnn,
) {
	bounded := func(p *soltype.TypeParam) bool { return p.Constraint != nil }
	if !slices.ContainsFunc(params, bounded) {
		// Every parameter is unbounded, so there is nothing to compare and no substitution to
		// build. This is the common shape for a generic alias.
		return
	}
	subst := newTypeSubst(params, args, ltParams, ltArgs)
	for i, p := range params {
		if !bounded(p) {
			continue
		}
		// Read the declared constraint from the parameter rather than from its var's upper
		// bounds. A `<T: A & B>` bound resolves to one IntersectionType, so this is the whole
		// of what the source wrote, and it cannot be displaced by a bound solving inferred.
		bound := p.Constraint.Accept(subst, soltype.Positive)
		if i >= len(ref.TypeArgs) && bound == p.Constraint && args[i] == p.Default {
			// This argument came from the parameter's default rather than from the reference, and
			// substitution changed neither the bound nor the default. Both therefore read here
			// exactly as they do at the declaration, where resolveTypeParams already compared
			// them. Repeating the comparison would file one copy of the same diagnostic per
			// reference. The check still runs whenever substitution moved either side, since then
			// only the reference knows what the comparison is really between. `<A, B: A = number>`
			// is the moved-bound case and `<T, U: string = T>` the moved-default case.
			continue
		}
		// Blame the written argument. A trailing argument filled from its parameter's default
		// has no node of its own, so the blame falls back to the whole reference.
		var site ast.Node = ref
		if i < len(ref.TypeArgs) {
			site = ref.TypeArgs[i]
		}
		if c.deferArgBounds {
			c.deferredArgBounds = append(c.deferredArgBounds, deferredArgBound{
				arg: args[i], bound: bound, site: site,
			})
			continue
		}
		c.constrain(site, args[i], bound)
	}
}

// runDeferredArgBounds replays and clears the checks checkTypeArgBounds queued while the
// component's bodies were nil, since an unfilled alias argument expands to ErrorType and absorbs.
func (c *checker) runDeferredArgBounds() {
	pending := c.deferredArgBounds
	c.deferredArgBounds = nil
	for _, p := range pending {
		c.constrain(p.site, p.arg, p.bound)
	}
}
