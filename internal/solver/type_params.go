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
	// omits it, so a default outside the bound would smuggle in an argument the bound forbids.
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

// reportDefaultForwardRef reports each parameter params[i]'s default names that it may not,
// params[i] itself or one declared after it, and returns whether it reported. A reference fills an
// omitted argument from the default with the earlier arguments substituted in, as
// buildAliasInstance does for the `U = T` of `type Pair<T, U = T>`. A later or self reference has
// nothing to substitute, so it stays the declaration's own var, shared by every reference to the
// type. A rejected default is dropped, which leaves the parameter required. Neither instance
// builder reports the follow-on arity error in every case, which PR18's shared helper settles.
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

// typeArgRange returns the type-argument counts a reference to a generic class or alias may
// supply. total is the parameter count. required is the position just past the last parameter
// with no default, so a reference may omit a trailing run of defaulted parameters and nothing
// else. Any count between the two is accepted, and a count outside the range is an arity
// mismatch each caller reports under its own error type.
//
// Reading required as a position rather than as a count of defaultless parameters is what makes
// a default followed by a required parameter behave. `<T = number, U>` cannot fill T from its
// default without leaving U unsupplied, so required is 2 and `Pair<string>` is rejected instead
// of silently binding U to a fresh var. This matches how a default function parameter followed
// by a required one behaves at a call site.
func typeArgRange(params []*soltype.TypeParam) (required, total int) {
	for i, p := range params {
		if p.Default == nil {
			required = i + 1
		}
	}
	return required, len(params)
}

// resolveTypeArgsWithDefaults produces one type argument per declared parameter, so a class or
// alias instance always carries a full argument list for substitution to zip against. Each
// parameter draws its argument from one of three sources:
//
//  1. The annotation the reference wrote at that position, resolved in scope.
//  2. The parameter's own default, for a parameter past the last written argument, with the
//     sibling arguments substituted in by fillOmittedFromDefaults.
//  3. A fresh var, when the written annotation does not resolve or the reference omitted a
//     parameter that has no default. Recovering keeps the argument list full. The omission
//     itself is reported by the caller's arity check.
//
// Every written annotation is resolved, including any past the last declared parameter. A
// surplus argument is dropped from the result, but resolving it first is what lets an error
// inside it surface: `Point<Nope>` on a non-generic Point reports the unresolvable `Nope`
// alongside the arity mismatch, rather than only the count.
func (c *checker) resolveTypeArgsWithDefaults(
	scope *Scope,
	params []*soltype.TypeParam,
	ref *ast.TypeRefTypeAnn,
	lvl int,
) []soltype.Type {
	written := make([]soltype.Type, len(ref.TypeArgs))
	for i, arg := range ref.TypeArgs {
		if resolved, ok := c.resolveTypeAnn(scope, arg, lvl); ok {
			written[i] = resolved
		} else {
			written[i] = c.freshAt(lvl)
		}
	}
	args := make([]soltype.Type, len(params))
	copy(args, written)
	if len(written) < len(params) {
		c.fillOmittedFromDefaults(params, args, len(written), lvl)
	}
	return args
}

// fillOmittedFromDefaults fills the positions a reference left out, in place. args[:got] already
// holds the written arguments, and args[got:] is what this writes. A parameter with no default
// there gets a fresh var, since the caller's arity check has reported the omission.
//
// A default may name an earlier sibling, as the `U = T` of `<T, U = T>` does, and the var that
// name resolved to is the declaration's own, one shared object every reference would otherwise
// constrain through. Substituting the arguments already in place for the earlier parameters
// keeps each reference independent. Filling left to right makes one substitution per position
// enough: by the time a default is filled, every argument it can name is final, because
// resolveTypeParams rejects a default naming a later sibling or itself, so a chain like
// `<T, U = T, V = U>` resolves position by position.
func (c *checker) fillOmittedFromDefaults(params []*soltype.TypeParam, args []soltype.Type, got, lvl int) {
	for i := got; i < len(params); i++ {
		if params[i].Default == nil {
			args[i] = c.freshAt(lvl)
			continue
		}
		subst := newTypeSubst(params[:i], args[:i], nil, nil)
		args[i] = params[i].Default.Accept(subst, soltype.Positive)
	}
}

// checkTypeArgBounds reports a type argument that does not satisfy its parameter's declared
// bound, so `class Box<T: string>` and `type Box<T: string>` both reject `Box<number>`. Every
// generic class and alias reference routes through here, so the rule reads the same on both.
// Arguments are substituted into the bound first, which lets a bound name a sibling as the `B: A`
// of `<A, B: A>` does. The comparison is live rather than a discarded trial, so an argument that
// is itself a variable carries the bound to its instantiation the way a function call's does.
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
		// build. This is the common shape for a generic class or alias.
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
