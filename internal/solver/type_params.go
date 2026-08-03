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

// resolveTypeArgs pairs the `<…>` type arguments a reference wrote with the parameters of the
// declaration it names, and returns one argument per parameter. It is the instantiation twin
// of resolveTypeParams, and the alias, enum, and class reference paths all route through it,
// so a defaulted or miscounted reference resolves the same way whichever sort it names.
//
// Three things happen here. The written count is checked against the parameter list, where the
// valid range runs from the parameters with no default up to the whole list. A trailing
// omitted argument is filled from its parameter's default, with the arguments already resolved
// substituted in, so the `U = T` of `type Pair<T, U = T>` resolves `Pair<number>` to
// `Pair<number, number>`. A required argument that the reference omitted, or one whose
// annotation failed to resolve, recovers to a fresh var, so a downstream reference still has
// one argument per parameter to substitute.
//
// The result is nil for a declaration with no type parameters, and any arguments the reference
// wrote past the parameter count are reported and then dropped.
func (c *checker) resolveTypeArgs(
	scope *Scope,
	ref *ast.TypeRefTypeAnn,
	kind TypeDeclKind,
	params []*soltype.TypeParam,
	lvl int,
) []soltype.Type {
	total := len(params)
	required := 0
	for _, p := range params {
		if p.Default == nil {
			required++
		}
	}
	got := len(ref.TypeArgs)
	if got < required || got > total {
		c.report(&TypeArgArityMismatchError{
			Ref:      ref,
			Kind:     kind,
			Name:     ast.QualIdentToString(ref.Name),
			Required: required,
			Total:    total,
			Got:      got,
		})
	}
	if total == 0 {
		return nil
	}
	args := make([]soltype.Type, total)
	for i := range total {
		if i < got {
			if resolved, ok := c.resolveTypeAnn(scope, ref.TypeArgs[i], lvl); ok {
				args[i] = resolved
			} else {
				args[i] = c.freshAt(lvl)
			}
			continue
		}
		if params[i].Default != nil {
			// The default may reference an earlier parameter, as `U = T` does, so substitute
			// the arguments already resolved for parameters before this one.
			subst := newTypeSubst(params[:i], args[:i], nil, nil)
			args[i] = params[i].Default.Accept(subst, soltype.Positive)
		} else {
			// A required argument was omitted, already reported as an arity mismatch. Recover
			// to a fresh var so every parameter has an argument to substitute.
			args[i] = c.freshAt(lvl)
		}
	}
	return args
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
