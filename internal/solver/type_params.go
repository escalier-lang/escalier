package solver

import (
	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/set"
	"github.com/escalier-lang/escalier/internal/soltype"
)

// resolveTypeParams resolves a `<…>` type-parameter list to soltype TypeParams in three
// passes. Pass 1 mints one fresh var per parameter. Pass 2 resolves each parameter's default
// and then declares that parameter's name, so a default reads only the siblings before it.
// Pass 3 resolves each constraint into its var's upper bound, against the scope that by then
// holds every sibling.
//
// A default and a bound need opposite visibility, which is why they sit in separate passes.
//
// A bound may name any sibling. Declaring every parameter before reading any bound is what
// lets a forward reference `<T: U, U>`, a mutual cycle `<T: U, U: T>`, an F-bound
// `<T: Foo<T>>`, and a mutual F-bound `<T: Cmp<U>, U: Cmp<T>>` all resolve. Resolving a bound
// under a scope that holds only the earlier siblings would leave a later-declared name
// undeclared, so the reference would fall through to general type-ref resolution and report
// `Unsupported: TypeRefTypeAnn`. Because every sibling is in scope by pass 3, a true mutual
// cycle resolves that a topological sort of the parameters cannot order.
//
// A default may name only an earlier sibling, since instantiation substitutes a default
// positionally. reportDefaultForwardRef reports a default that names a later sibling or
// itself, and the caller then skips resolving it, so that check is what a user sees. Growing
// the scope one parameter at a time is the backstop underneath it: a later sibling's name is
// not in scope when the default resolves, so its var cannot reach an instance even if the
// check's idea of where a binder's region ends ever drifts from resolveTypeAnn's. See
// reportDefaultForwardRef for the rest of the rule.
//
// The result stays in declaration order, so instantiation substitutes type arguments
// positionally. The class and, once generic-function inference lands, the function and
// method paths both route their `<…>` lists through here so bound resolution never forks.
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
	return out
}

// reportDefaultForwardRef reports the first reference params[i]'s default makes to a parameter
// it may not name, meaning params[i] itself or one declared after it. It returns whether it
// reported.
//
// A reference that omits a trailing argument fills it from that parameter's default,
// substituting the arguments before it. buildAliasInstance does exactly that for the `U = T`
// of `type Pair<T, U = T>`. Only a parameter earlier in the list has an argument by then, so
// only an earlier one is replaced by that substitution.
//
// A later or self reference is left untouched, so it stays the declaration's own var, and
// every reference to the type shares that one var. In `type Pair<T = U, U = number> = {a: T, b: U}`,
// `T`'s default resolves to `U`'s var, so admitting it would let `val q: Pair = {a: "s", b: 3}`
// widen a second `val p: Pair = {a: 1, b: 2}` to `Pair<1 | "s", number>`. Rejecting the default
// keeps that var out of every instance.
//
// The caller drops a rejected default rather than replacing it, so the parameter becomes
// required. Whether omitting that argument then reports a second diagnostic is up to the
// instance builder, and neither builder reports it in every case today. buildAliasInstance
// counts its required arguments as the number of parameters carrying no default, with no
// regard for where those parameters sit. Writing `Tri<string>` against
// `type Tri<T = number, U = V, V = number>` passes that count, so the omitted `U` becomes a
// fresh var with only the ordering error reported. An arity mismatch is reported when no
// defaulted parameter precedes the rejected one. buildClassInstance checks no arity at all.
// PR18 of planning/simple_sub/m9-implementation-plan.md factors both builders into one
// helper, which is where a positional count belongs.
//
// The restriction covers the default position alone. A bound may name any sibling, since
// `<T: U, U: T>` is a legal mutual F-bound and `<T: Foo<T>>` names the parameter being
// declared. Neither is substituted positionally, so neither needs the ordering a default does.
func (c *checker) reportDefaultForwardRef(params []*ast.TypeParam, i int) bool {
	forbidden := set.NewSet[string]()
	for _, p := range params[i:] {
		forbidden.Add(p.Name)
	}
	var scan typeParamRefScan
	params[i].Default.Accept(&scan)
	for _, ref := range scan.free {
		name := ast.QualIdentToString(ref.Name)
		if !forbidden.Contains(name) {
			continue
		}
		c.report(&TypeParamDefaultForwardRefError{Ref: ref, Param: params[i].Name, Target: name})
		return true
	}
	return false
}

// typeParamRefScan walks a type annotation and collects the references that read a name from
// outside it, so a caller can tell a reference to an enclosing `<…>` parameter from one a
// binder written inside the annotation introduces.
//
// free holds those references in traversal order, so a caller reports the leftmost offending
// one. A reference whose name an enclosing binder declares is left out, since it reads that
// binder's name rather than a `<…>` parameter's. Three binders can appear inside an
// annotation, and the scan tracks the region each one covers:
//
//   - A `fn <U>(…)` quantifier covers the whole function annotation.
//   - A mapped type's key binder, the `K` of `{[K: Keys]: V}`, covers the object annotation
//     that holds it. That is wider than the binder's own region by the key constraint and any
//     sibling element, so `{[K: Keys]: V, other: K}` reads `other: K` as the key binder.
//   - An `infer U` clause covers the Extends and Then operands of the conditional whose
//     Extends declares it, matching where resolveCondTypeAnn declares the name. The same name
//     in the Else operand is a free reference.
//
// scopes holds one name set per region the walk is currently inside, innermost last.
type typeParamRefScan struct {
	ast.DefaultVisitor
	scopes []set.Set[string]
	free   []*ast.TypeRefTypeAnn
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
