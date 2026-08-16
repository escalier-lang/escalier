package codegen

import (
	"sort"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/printer"
	"github.com/escalier-lang/escalier/internal/set"
	"github.com/escalier-lang/escalier/internal/type_system"
)

// The order the generated dispatcher tests overload arms in.
//
// buildOverloadedFunc compiles an overload set to one function whose if-else chain
// runs each arm's guard in turn, so the order decides which arm answers a call two
// arms both accept. The checker answers the same question statically in
// internal/solver's resolveOverload. The two have to agree: a call the checker types
// with one arm's return type must reach that arm at runtime. This file derives the
// dispatcher's order so it matches the checker's.
//
// The order has three keys, applied in turn.
//
//  1. Parameter count, descending. A guard tests only the parameters its own arm
//     declares, so a one-parameter arm placed first would answer a two-argument call
//     the two-parameter arm was written for. The checker has no matching rule because
//     it gates arity separately, in tryOverloadArm, before an arm is ranked at all. So
//     this key is invisible to the checker rather than in conflict with it.
//  2. Specificity, most specific first, mirroring the checker's specificityOrder. An
//     arm is more specific than another when each of its parameters admits no more
//     values than the matching parameter, and at least one admits strictly fewer. So
//     `(x: 5)` is tested before `(x: number)`, and `({x, y})` before `({x})`.
//  3. Source position — source id, then line, then column. This is the declaration
//     order the checker's armPosLess pins, and it settles every pair the first two keys
//     leave tied. Sorting the arms rather than taking them as the dep graph happens to
//     hold them is what keeps the two tiebreaks the same.
//
// # Where the two orders can still differ
//
// Both keys 2 and 3 are derived from what the arm WROTE, while the checker derives its
// ranking from what it INFERRED. Two gaps follow, both narrow and both documented at
// the code that leaves them.
//
//   - A parameter annotated with a type alias. The checker ranks the type the alias
//     expands to; annSubsumes sees only the name and ranks the pair as a tie. An alias
//     naming a class is the exception, since nominalGuardName resolves it.
//   - Key 3 orders by source id where armPosLess orders by file path. The compiler
//     assigns ids by walking the source tree in lexical order, so the two agree for
//     every program it builds; a caller that hands the parser sources in some other
//     order can separate them.
//
// Key 1 has a gap of its own, and it predates this ordering. An optional or rest
// parameter widens an arm's accepted argument counts, so `(x: string, y?: number|string)`
// and `(x: string)` both accept a one-argument call and the checker ranks them by
// declaration order. Key 1 tests the longer arm first, and its `y` guard is a bare
// `true` because a union is untestable, so the longer arm answers a call the checker
// gave to the shorter one. Closing this needs the dispatcher to test how many arguments
// it was called with, which it does not do today.
//
// One further divergence lives on the checker's side rather than here. When a call
// argument is a still-unconstrained variable, overloadOrder cannot rank the arms and
// falls back to plain declaration order, so `fn g(y) { return f(y) }` pins y to the
// FIRST arm rather than to the one specificity would choose. The dispatcher always
// ranks by specificity, so a call through g can reach a different arm than the one g
// was typed with. That fallback is the MVP limitation #723 tracks, and deferred
// resolution retires this divergence with it.

// DispatchOrder returns overloads in the order the generated if-else chain tests them.
// The input is left untouched.
//
// It is exported so the checker's conformance corpus can measure its own resolution
// order against this one. Nothing outside that check and buildOverloadedFunc should
// need it.
func DispatchOrder(overloads []*ast.FuncDecl) []*ast.FuncDecl {
	ordered := make([]*ast.FuncDecl, len(overloads))
	copy(ordered, overloads)
	// Rank each arm by its DOMINATION COUNT — how many other arms are strictly more
	// specific than it. Sorting on that integer rather than on the specificity relation
	// itself is what keeps the sort well-defined. Specificity is only a partial order:
	// `(x: number)` and `(x: string)` are incomparable, and incomparability is not
	// transitive, so feeding the relation to a sort would break the strict-weak-ordering
	// contract sort.SliceStable requires. A count is a total order on integers. An arm
	// nothing dominates gets 0 and sorts first; the catch-all every other arm beats sorts
	// last. internal/solver's specificityOrder ranks the same way.
	dominators := make([]int, len(ordered))
	for i := range ordered {
		for j := range ordered {
			if i == j || len(ordered[i].Params) != len(ordered[j].Params) {
				continue
			}
			if armSpecificity(ordered[j], ordered[i]) < 0 {
				dominators[i]++
			}
		}
	}
	order := make([]int, len(ordered))
	for i := range order {
		order[i] = i
	}
	sort.SliceStable(order, func(x, y int) bool {
		a, b := order[x], order[y]
		if len(ordered[a].Params) != len(ordered[b].Params) {
			return len(ordered[a].Params) > len(ordered[b].Params)
		}
		if dominators[a] != dominators[b] {
			return dominators[a] < dominators[b]
		}
		return earlierInSource(ordered[a], ordered[b])
	})
	sorted := make([]*ast.FuncDecl, len(ordered))
	for i, idx := range order {
		sorted[i] = ordered[idx]
	}
	return sorted
}

// earlierInSource reports whether a is declared before b, by source id, then line, then
// column. It is the tiebreak internal/solver's armPosLess applies, with one difference:
// armPosLess orders by file path where this orders by source id. The compiler assigns
// ids by walking the source tree in lexical order, so id order is path order for every
// program it builds. Ordering here at all is what matters — the dep graph hands its
// declarations back in the order they were registered, which no rule pins.
func earlierInSource(a, b *ast.FuncDecl) bool {
	as, bs := a.Span(), b.Span()
	if as.SourceID != bs.SourceID {
		return as.SourceID < bs.SourceID
	}
	if as.Start.Line != bs.Start.Line {
		return as.Start.Line < bs.Start.Line
	}
	return as.Start.Column < bs.Start.Column
}

// armSpecificity compares two overload arms parameter by parameter. It returns -1 when
// a is strictly more specific than b, +1 when b is, and 0 when neither is — either
// because they are incomparable or because they admit the same arguments. Arms of
// different arity are a 0 here; DispatchOrder separates those by parameter count
// instead. This mirrors moreSpecific in internal/solver.
func armSpecificity(a, b *ast.FuncDecl) int {
	if len(a.Params) != len(b.Params) {
		return 0
	}
	aParams := armParams(a)
	bParams := armParams(b)
	aSubB, bSubA := true, true
	for i := range aParams {
		if aSubB && !annSubsumes(aParams[i], bParams[i]) {
			aSubB = false
		}
		if bSubA && !annSubsumes(bParams[i], aParams[i]) {
			bSubA = false
		}
		if !aSubB && !bSubA {
			return 0
		}
	}
	if aSubB && !bSubA {
		return -1
	}
	if bSubA && !aSubB {
		return 1
	}
	return 0
}

// armParam is one parameter of an arm as the ordering reads it: the written annotation
// plus whether that annotation admits every value.
type armParam struct {
	ann ast.TypeAnn
	// top marks a parameter that accepts anything, so every other parameter is at least
	// as specific as it. Two cases qualify, and they are the two the checker's
	// structuralSubtype treats as the top of its own ordering by seeing a type variable:
	// a parameter with no annotation, whose type is a fresh inference variable, and one
	// annotated with the arm's own type parameter.
	top bool
}

// armParams describes each of decl's parameters for the ordering.
func armParams(decl *ast.FuncDecl) []armParam {
	// The arm's own type parameters are the names that stand for a type variable rather
	// than for a type. A signature with no `<…>` list allocates nothing.
	var vars set.Set[string]
	if len(decl.TypeParams) > 0 {
		vars = set.NewSet[string]()
		for _, tp := range decl.TypeParams {
			vars.Add(tp.Name)
		}
	}
	params := make([]armParam, len(decl.Params))
	for i, p := range decl.Params {
		params[i] = armParam{ann: p.TypeAnn, top: isTopAnn(p.TypeAnn, vars)}
	}
	return params
}

// isTopAnn reports whether ann admits every value: either there is no annotation, or it
// names one of vars, the enclosing arm's own type parameters.
func isTopAnn(ann ast.TypeAnn, vars set.Set[string]) bool {
	if ann == nil {
		return true
	}
	ref, ok := ann.(*ast.TypeRefTypeAnn)
	if !ok || ref.Name == nil || vars == nil {
		return false
	}
	return vars.Contains(ast.QualIdentToString(ref.Name))
}

// annSubsumes reports whether the values a admits are a subset of those b admits. It is
// the dispatch-order counterpart of structuralSubtype in internal/solver, written over
// the annotations the source wrote rather than over inferred types, and it is
// deliberately partial in the same way: a pair it cannot rank returns false in both
// directions, which armSpecificity reads as a tie and DispatchOrder settles by source
// position.
//
// An annotation buildTypeGuard cannot test at runtime — a union, a function type — is
// NOT treated as top here, because the checker does not treat it as top either. Such an
// arm ties with everything, so it keeps its declared place. Its guard is still a bare
// `true`, which means an untestable arm declared first answers every call that reaches
// it; that is what the checker's resolution does too, so the two agree on the arm even
// where the program is a poor one.
func annSubsumes(a, b armParam) bool {
	if b.top {
		return true
	}
	if a.top {
		return false
	}
	if sameAnn(a.ann, b.ann) {
		return true
	}
	// A literal is one value out of its primitive's set, so `5` is more specific than
	// `number`. The checker ranks a LitType under its PrimType the same way.
	if lit, ok := a.ann.(*ast.LitTypeAnn); ok {
		return litUnderPrimitive(lit.Lit, b.ann)
	}
	if ao, ok := a.ann.(*ast.ObjectTypeAnn); ok {
		if bo, ok := b.ann.(*ast.ObjectTypeAnn); ok {
			return objectAnnSubsumes(ao, bo)
		}
	}
	return false
}

// sameAnn reports whether two annotations were written the same way, by rendering each
// back to source and comparing the text. It stands in for the alpha-equality the
// checker's structuralSubtype tests first, and covers every annotation form rather than
// the handful the guard builder can test, so `{x: number | string}` compares equal to
// itself even though no guard tests a union.
//
// Two annotations naming the same type through different spellings — an alias and its
// expansion, a qualified and an unqualified reference — read as different here and rank
// as a tie. The checker sees one type and ranks them equal, which is also a tie.
func sameAnn(a, b ast.TypeAnn) bool {
	aText, aOK := annText(a)
	bText, bOK := annText(b)
	return aOK && bOK && aText == bText
}

// annText renders an annotation back to Escalier source, reporting false for a nil
// annotation or one the printer cannot render.
func annText(ann ast.TypeAnn) (string, bool) {
	if ann == nil {
		return "", false
	}
	text, err := printer.Print(ann, printer.CompactOptions())
	if err != nil {
		return "", false
	}
	return text, true
}

// litUnderPrimitive reports whether lit is one of the values the primitive annotation
// prim admits, the `5` under `number` case.
func litUnderPrimitive(lit ast.Lit, prim ast.TypeAnn) bool {
	switch lit.(type) {
	case *ast.NumLit:
		_, ok := prim.(*ast.NumberTypeAnn)
		return ok
	case *ast.StrLit:
		_, ok := prim.(*ast.StringTypeAnn)
		return ok
	case *ast.BoolLit:
		_, ok := prim.(*ast.BooleanTypeAnn)
		return ok
	}
	return false
}

// objectAnnSubsumes reports whether object annotation a accepts a subset of what b
// accepts. It ranks two axes and ignores a third, exactly as objectSubsumes does.
//
//   - Required-property count. The guard tests that each required property is present
//     and that its value passes the property's own guard, so a is narrower when its
//     required properties are a strict superset of b's. `{x: number, y: number}` is
//     more specific than `{x: number}`. Every required property of b must appear in a
//     as required and with the same property type; a property a is missing, has as
//     optional, or types differently leaves the two incomparable and returns false.
//   - The trailing `...` marker, consulted only when the required sets match. An exact
//     `{x: number}` is more specific than an inexact `{x: number, ...}`, since it
//     accepts no extra properties. The two emit the SAME guard, so this is the ranking
//     that decides which of them answers a call both accept.
//
// Property types are NOT ranked. Two arms whose shared properties differ in type tie
// here and fall back to source position.
//
// An optional property is never tested and widens the arm rather than narrowing it, so
// it is not counted: `{x, y?}` accepts everything `{x}` accepts.
func objectAnnSubsumes(a, b *ast.ObjectTypeAnn) bool {
	requiredB := 0
	for _, elem := range b.Elems {
		prop, ok := elem.(*ast.PropertyTypeAnn)
		if !ok {
			return false // a non-property member is outside what the guard tests
		}
		if prop.Optional {
			continue
		}
		requiredB++
		match, found := lookupProp(a, propAnnName(prop))
		if !found || match.Optional || !sameAnn(match.Value, prop.Value) {
			return false
		}
	}
	requiredA := 0
	for _, elem := range a.Elems {
		prop, ok := elem.(*ast.PropertyTypeAnn)
		if !ok {
			return false
		}
		if !prop.Optional {
			requiredA++
		}
	}
	if requiredA > requiredB {
		return true
	}
	return !a.Inexact && b.Inexact
}

// lookupProp returns obj's property named name. A computed key has no name the guard
// can test, so it never matches.
func lookupProp(obj *ast.ObjectTypeAnn, name string) (*ast.PropertyTypeAnn, bool) {
	if name == "" {
		return nil, false
	}
	for _, elem := range obj.Elems {
		prop, ok := elem.(*ast.PropertyTypeAnn)
		if !ok {
			continue
		}
		if propAnnName(prop) == name {
			return prop, true
		}
	}
	return nil, false
}

// propAnnName returns the property name the `"k" in o` guard tests, or "" for a
// computed key buildTypeGuard skips.
func propAnnName(prop *ast.PropertyTypeAnn) string {
	switch key := prop.Name.(type) {
	case *ast.IdentExpr:
		return key.Name
	case *ast.StrLit:
		return key.Value
	}
	return ""
}

// nominalGuardName returns the class name an `x instanceof C` guard tests for a
// reference to a nominal type, and reports whether the reference is one. A reference
// resolves through its inferred type, either directly or through a type alias.
func nominalGuardName(t *ast.TypeRefTypeAnn) (string, bool) {
	inferred := t.InferredType()
	if inferred == nil {
		return "", false
	}
	pruned := type_system.Prune(inferred)
	if typeRef, ok := pruned.(*type_system.TypeRefType); ok && typeRef.TypeAlias != nil {
		if obj, ok := type_system.Prune(typeRef.TypeAlias.Type).(*type_system.ObjectType); ok && obj.Nominal {
			return ast.QualIdentToString(t.Name), true
		}
	}
	if obj, ok := pruned.(*type_system.ObjectType); ok && obj.Nominal {
		return ast.QualIdentToString(t.Name), true
	}
	return "", false
}

// isArrayTypeRef reports whether a reference is the `Array` the guard tests with
// `Array.isArray`.
func isArrayTypeRef(t *ast.TypeRefTypeAnn) bool {
	return t.Name != nil && ast.QualIdentToString(t.Name) == "Array"
}
