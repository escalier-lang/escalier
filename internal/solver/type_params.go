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
// lower bound into its var's lower bound and each upper bound into its var's upper bound against
// the full list, so a forward `<T: U, U>`, an F-bound `<T: Foo<T>>` and a `<B> where Box<T>: B` all
// resolve. A bound chain that reaches its own parameter, as `<T: U, U: T>` does, resolves too
// and is then reported by reportBoundCycles. Pass 4 checks each default and each lower bound
// against its own parameter's upper bound. The result stays in declaration order, and the alias,
// class, enum, and function-annotation paths all route through here.
func (c *checker) resolveTypeParams(scope *Scope, lvl int, params []*ast.TypeParam) []*soltype.TypeParam {
	c.reportRequiredAfterDefault(params)
	c.reportDuplicateTypeParams(params)
	// Resolve one parameter per distinct name, so a reference's type arguments are matched
	// against the count the names allow rather than the count of binders written.
	params = ast.DistinctTypeParams(params)
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
	// Pass 3: resolve each lower bound into its var's lower bound and each upper bound into
	// its var's upper bound, now that every sibling name is in scope. Each is also kept on
	// its own field, where later solving cannot overwrite it. The var's bound lists grow as
	// constraints flow in, so a reader that wants what the source wrote reads the field.
	for i, p := range params {
		if p.LowerBound != nil {
			if lt, ok := c.resolveTypeAnn(scope, p.LowerBound, lvl); ok {
				c.ctx.addLowerBound(out[i].Var, lt)
				out[i].LowerBound = lt
			}
		}
		if p.UpperBound != nil {
			if ct, ok := c.resolveTypeAnn(scope, p.UpperBound, lvl); ok {
				c.ctx.addUpperBound(out[i].Var, ct)
				out[i].UpperBound = ct
			}
		}
	}
	c.reportBoundCycles(params, out)
	// Pass 4: check each default and each lower bound against its own parameter's
	// upper bound, and each default against its lower bound. `<T: string = number>`,
	// `<B: number> where string: B` and `<B = string> where number: B` are each rejected at the
	// declaration. A default fills the argument at every use site that omits it, so a
	// default outside either bound would supply an argument the bound forbids. A lower bound
	// above the upper bound leaves no type the parameter could be. This runs as its own pass
	// because a bound is not resolved until pass 3, after the default it is compared against.
	//
	// Each comparison is trialed under a probe rather than run live, so any bound it appends
	// is rolled back. A default is a fully resolved type with nothing left to infer, so a
	// live comparison would gain it nothing. It would cost something when the bound names a
	// sibling parameter. Running `<T, U: T = number>` live compares number against T's var
	// and leaves T carrying number as a lower bound, a claim that T must accept number that
	// the source never wrote.
	for i, p := range params {
		if out[i].Default != nil && out[i].UpperBound != nil {
			c.blameConstraintErrors(p.Default, c.ctx.trialUnderProbe(out[i].Default, out[i].UpperBound))
		}
		if out[i].Default != nil && out[i].LowerBound != nil {
			c.blameConstraintErrors(p.Default, c.ctx.trialUnderProbe(out[i].LowerBound, out[i].Default))
		}
		if out[i].LowerBound != nil && out[i].UpperBound != nil {
			c.blameConstraintErrors(p.LowerBound, c.ctx.trialUnderProbe(out[i].LowerBound, out[i].UpperBound))
		}
	}
	return out
}

// reportBoundCycles reports each type parameter whose bound chain reaches the parameter
// itself through bare parameters of the same list, in either direction. `<T: U, U: T>` is
// one such chain and `<B> where B | 1: B` another. A chain steps from a parameter to the
// parameters its bound is. That is the bound itself when it is a parameter, or each parameter among the
// members of a union or an intersection at any depth, since `constrain` reaches each member
// of either on its own. A transparent alias is read as its body, so `type Same<X> = X`
// steps through `Same<U>`. `<T: Foo<T>>` over a class or object is not a cycle, as the chain
// stops at Foo.
//
// Such a chain has no bound to end on. The body check reads a rigid parameter as a skolem
// and follows its bounds, so it would reach the pair it started from and close it the way
// it closes a recursive type. That lets `x > 1` check with `x: T` under `<T: U, U: T>`. Each
// cycle is reported once, at its first parameter in declaration order, naming the
// parameters it runs through. decls and params are the same list, as declared and as
// resolved.
func (c *checker) reportBoundCycles(decls []*ast.TypeParam, params []*soltype.TypeParam) {
	index := make(map[*soltype.TypeVarType]int, len(params))
	for i, p := range params {
		index[p.Var] = i
	}
	upper := func(p *soltype.TypeParam) []int { return c.boundSteps(p.UpperBound, index) }
	lower := func(p *soltype.TypeParam) []int { return c.boundSteps(p.LowerBound, index) }
	for _, direction := range []struct {
		steps func(*soltype.TypeParam) []int
		lower bool
	}{{upper, false}, {lower, true}} {
		// reported is per direction, so a cycle found in the upper pass does not hide one
		// through the same parameters in the lower pass.
		reported := make([]bool, len(params))
		for i := range params {
			if reported[i] {
				continue
			}
			through, found := boundCycle(i, params, direction.steps)
			if !found {
				continue
			}
			reported[i] = true
			throughDecls := make([]*ast.TypeParam, len(through))
			for k, j := range through {
				reported[j] = true
				throughDecls[k] = decls[j]
			}
			c.report(&TypeParamBoundCycleError{Param: decls[i], Through: throughDecls, Lower: direction.lower})
		}
	}
}

// boundSteps returns the positions of the parameters bound steps to: the bound's own
// position when it is a parameter, else the position of each parameter among the members
// of a union or an intersection, nested to any depth, with a transparent alias read as its
// body. A nil bound or any other type steps nowhere. An alias already read on the walk is
// not read again, so `type A = A | U` contributes `U` once and stops.
func (c *checker) boundSteps(bound soltype.Type, index map[*soltype.TypeVarType]int) []int {
	var out []int
	aliases := set.NewSet[string]()
	var walk func(t soltype.Type)
	walk = func(t soltype.Type) {
		switch b := t.(type) {
		case *soltype.TypeVarType:
			if i, isParam := index[b]; isParam {
				out = append(out, i)
			}
		case *soltype.UnionType:
			for _, m := range b.Types {
				walk(m)
			}
		case *soltype.IntersectionType:
			for _, m := range b.Types {
				walk(m)
			}
		case *soltype.AliasType:
			if aliases.Contains(b.Name) {
				return
			}
			aliases.Add(b.Name)
			if body, ok := c.ctx.aliasBody(b); ok {
				walk(body)
			}
		}
	}
	if bound != nil {
		walk(bound)
	}
	return out
}

// boundCycle reports whether following steps from params[start] returns to start, and
// returns the parameters a found cycle passes through on the way, in order and excluding
// start. A cycle that never returns to start, as the `U` of `<T: U, U: U>` forms, is left for
// its own first parameter to report.
func boundCycle(start int, params []*soltype.TypeParam, steps func(*soltype.TypeParam) []int) ([]int, bool) {
	visited := set.NewSet[int]()
	var path []int
	var walk func(i int) bool
	walk = func(i int) bool {
		for _, j := range steps(params[i]) {
			if j == start {
				return true
			}
			if visited.Contains(j) {
				continue
			}
			visited.Add(j)
			path = append(path, j)
			if walk(j) {
				return true
			}
			path = path[:len(path)-1]
		}
		return false
	}
	if walk(start) {
		return path, true
	}
	return nil, false
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
// optional and resolving the clause does not change whether it is there. It counts one
// parameter per distinct name, which is what resolveTypeParams resolves.
func arityOfParamDecls(params []*ast.TypeParam) typeParamArity {
	distinct := ast.DistinctTypeParams(params)
	return typeParamArity{
		Required: requiredArgCount(len(distinct), func(i int) bool { return distinct[i].Default != nil }),
		Total:    len(distinct),
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
	// Count over the list as written rather than through arityOfParamDecls, since the
	// slicing below indexes that same list.
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

// reportDuplicateTypeParams reports each parameter whose name an earlier one already took.
// A reference to the name can only mean one of them, so the later binder is unreachable and
// a caller has no way to say which parameter an argument fills.
//
// Only the later binder is reported, so `<T, T, T>` raises two errors rather than three.
func (c *checker) reportDuplicateTypeParams(params []*ast.TypeParam) {
	first := map[string]*ast.TypeParam{}
	for _, p := range params {
		if kept, taken := first[p.Name]; taken {
			c.report(&DuplicateTypeParamError{Name: p.Name, Param: p, First: kept})
			continue
		}
		first[p.Name] = p
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
	reported := set.NewSet[string]()
	for _, ref := range ast.FreeTypeRefs(params[i].Default) {
		name := ast.QualIdentToString(ref.Name)
		if !forbidden.Contains(name) || reported.Contains(name) {
			continue
		}
		reported.Add(name)
		c.report(&TypeParamDefaultForwardRefError{Ref: ref, Param: params[i].Name, Target: name})
	}
	return reported.Len() > 0
}

// checkTypeArgBounds reports a type argument that does not satisfy its parameter's declared
// bounds, so `class Box<T: string>` and `type Box<T: string>` both reject `Box<number>`, and
// `type Widen<B> where string: B` rejects `Widen<number>`. Every generic class, enum, and alias
// reference routes through here. Arguments are substituted into each bound first, which lets a
// bound name a sibling as the `B: A` of `<A, B: A>` does. The comparison is live rather than a
// discarded trial, so a variable argument carries the bound to its instantiation.
func (c *checker) checkTypeArgBounds(
	params []*soltype.TypeParam,
	args []soltype.Type,
	ltParams []*soltype.LifetimeParam,
	ltArgs []soltype.Lifetime,
	ref *ast.TypeRefTypeAnn,
) {
	bounded := func(p *soltype.TypeParam) bool { return p.UpperBound != nil || p.LowerBound != nil }
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
		// Blame the written argument. A trailing argument filled from its parameter's default
		// has no node of its own, so the blame falls back to the whole reference.
		var site ast.Node = ref
		if i < len(ref.TypeArgs) {
			site = ref.TypeArgs[i]
		}
		// An argument that came from the parameter's default is skipped when substitution
		// moved neither the bound nor the default, since both then read here exactly as they
		// do at the declaration where resolveTypeParams already compared them. Repeating it
		// would file the same diagnostic once per reference. A moved bound, as in
		// `<A, B: A = number>`, or a moved default, as in `<T, U: string = T>`, is still
		// checked, since only the reference knows what the comparison is between.
		fromDefault := i >= len(ref.TypeArgs) && args[i] == p.Default
		// Read each declared bound from the parameter rather than from its var's lists. A
		// `<T: A & B>` bound resolves to one IntersectionType, so this is the whole of what
		// the source wrote, and it cannot be displaced by a bound solving inferred.
		if p.UpperBound != nil {
			bound := p.UpperBound.Accept(subst, soltype.Positive)
			if !fromDefault || bound != p.UpperBound {
				c.constrainTypeArg(site, args[i], bound)
			}
		}
		if p.LowerBound != nil {
			lower := p.LowerBound.Accept(subst, soltype.Positive)
			if !fromDefault || lower != p.LowerBound {
				c.constrainTypeArg(site, lower, args[i])
			}
		}
	}
}

// constrainTypeArg runs one of checkTypeArgBounds' comparisons, sub <: super, or queues it
// while the component's bodies are nil.
func (c *checker) constrainTypeArg(site ast.Node, sub, super soltype.Type) {
	if c.deferArgBounds {
		c.deferredArgBounds = append(c.deferredArgBounds, deferredArgBound{sub: sub, super: super, site: site})
		return
	}
	c.constrain(site, sub, super)
}

// runDeferredArgBounds replays and clears the checks checkTypeArgBounds queued while the
// component's bodies were nil, since an unfilled alias argument expands to ErrorType and absorbs.
func (c *checker) runDeferredArgBounds() {
	pending := c.deferredArgBounds
	c.deferredArgBounds = nil
	for _, p := range pending {
		c.constrain(p.site, p.sub, p.super)
	}
}
