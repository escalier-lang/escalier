package codegen

import (
	"sort"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/type_system"
)

// The order the generated dispatcher tests overload arms in.
//
// buildOverloadedFunc compiles an overload set to one function whose if-else chain
// runs each arm's guard in turn, so the order decides which arm answers a call two
// arms both accept. The checker answers the same question statically in
// internal/solver's resolveOverload. The two have to agree: a call the checker types
// with one arm's return type must reach that arm at runtime. This file is where the
// dispatcher's order is derived so it matches the checker's.
//
// The order has two keys.
//
//  1. Parameter count, descending. A guard only tests the parameters its own arm
//     declares, so a one-parameter arm placed first would answer a two-argument call
//     that the two-parameter arm was written for. The checker has no matching rule
//     because it gates arity separately, in tryOverloadArm, before an arm is ranked at
//     all. So this key is invisible to the checker rather than in conflict with it.
//  2. Specificity, most specific first, mirroring the checker's specificityOrder. An
//     arm is more specific than another when each of its parameters admits no more
//     values than the matching parameter, and at least one admits strictly fewer. So
//     `(x: 5)` is tested before `(x: number)` and `({x, y})` before `({x})`.
//
// Arms neither ranking separates keep their declaration order, which is the tiebreak
// the checker's stable sort also falls back on.

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
		return dominators[a] < dominators[b]
	})
	sorted := make([]*ast.FuncDecl, len(ordered))
	for i, idx := range order {
		sorted[i] = ordered[idx]
	}
	return sorted
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
	aSubB, bSubA := true, true
	for i := range a.Params {
		if aSubB && !annSubsumes(a.Params[i].TypeAnn, b.Params[i].TypeAnn) {
			aSubB = false
		}
		if bSubA && !annSubsumes(b.Params[i].TypeAnn, a.Params[i].TypeAnn) {
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

// annSubsumes reports whether the values a's guard accepts are a subset of those b's
// guard accepts. It is the dispatch-order counterpart of structuralSubtype in
// internal/solver, written over the annotations buildTypeGuard reads rather than over
// inferred types, and it is deliberately partial in the same way: a pair it cannot rank
// returns false in both directions, which armSpecificity reads as a tie and
// DispatchOrder resolves in declaration order.
//
// An annotation buildTypeGuard cannot test emits a bare `true`, so it accepts every
// value. It is the top of this ordering: everything is a subset of it and it is a
// subset of nothing else. A missing annotation is the same case, since it emits no
// guard at all.
func annSubsumes(a, b ast.TypeAnn) bool {
	if guardAcceptsAnything(b) {
		return true
	}
	if guardAcceptsAnything(a) {
		return false
	}
	if sameGuardedType(a, b) {
		return true
	}
	// A literal is one value out of its primitive's set, so `5` is more specific than
	// `number`. The checker ranks a LitType under its PrimType the same way.
	if lit, ok := a.(*ast.LitTypeAnn); ok {
		return litUnderPrimitive(lit.Lit, b)
	}
	if ao, ok := a.(*ast.ObjectTypeAnn); ok {
		if bo, ok := b.(*ast.ObjectTypeAnn); ok {
			return objectAnnSubsumes(ao, bo)
		}
	}
	return false
}

// sameGuardedType reports whether two annotations produce guards that accept exactly
// the same values, so neither arm outranks the other. It covers the annotation kinds
// buildTypeGuard can actually test.
func sameGuardedType(a, b ast.TypeAnn) bool {
	switch at := a.(type) {
	case *ast.NumberTypeAnn:
		_, ok := b.(*ast.NumberTypeAnn)
		return ok
	case *ast.StringTypeAnn:
		_, ok := b.(*ast.StringTypeAnn)
		return ok
	case *ast.BooleanTypeAnn:
		_, ok := b.(*ast.BooleanTypeAnn)
		return ok
	case *ast.LitTypeAnn:
		bt, ok := b.(*ast.LitTypeAnn)
		return ok && sameLitValue(at.Lit, bt.Lit)
	case *ast.TupleTypeAnn:
		// Every tuple guard is `Array.isArray`, so two tuple annotations are
		// indistinguishable at runtime however their elements differ.
		_, ok := b.(*ast.TupleTypeAnn)
		return ok
	case *ast.TypeRefTypeAnn:
		bt, ok := b.(*ast.TypeRefTypeAnn)
		if !ok {
			return false
		}
		if aName, aNominal := nominalGuardName(at); aNominal {
			bName, bNominal := nominalGuardName(bt)
			return bNominal && aName == bName
		}
		// Both reach here as `Array.isArray` guards, since guardAcceptsAnything already
		// removed every other type reference.
		return isArrayTypeRef(at) && isArrayTypeRef(bt)
	case *ast.ObjectTypeAnn:
		bt, ok := b.(*ast.ObjectTypeAnn)
		return ok && sameObjectAnn(at, bt)
	}
	return false
}

// sameObjectAnn reports whether two object annotations describe the same shape: the
// same property names, each required on both sides or optional on both, with property
// types that guard the same values, and the same trailing `...` marker. It stands in
// for the alpha-equality the checker's structuralSubtype tests first.
func sameObjectAnn(a, b *ast.ObjectTypeAnn) bool {
	if a.Inexact != b.Inexact || len(a.Elems) != len(b.Elems) {
		return false
	}
	for _, elem := range a.Elems {
		prop, ok := elem.(*ast.PropertyTypeAnn)
		if !ok {
			return false // a non-property member is outside what the guard tests
		}
		match, found := lookupProp(b, propAnnName(prop))
		if !found || match.Optional != prop.Optional || !sameGuardedType(match.Value, prop.Value) {
			return false
		}
	}
	return true
}

// sameLitValue reports whether two literal annotations name the same value, which is
// what the `===` guard tests.
func sameLitValue(a, b ast.Lit) bool {
	switch al := a.(type) {
	case *ast.NumLit:
		bl, ok := b.(*ast.NumLit)
		return ok && al.Value == bl.Value
	case *ast.StrLit:
		bl, ok := b.(*ast.StrLit)
		return ok && al.Value == bl.Value
	case *ast.BoolLit:
		bl, ok := b.(*ast.BoolLit)
		return ok && al.Value == bl.Value
	case *ast.BigIntLit:
		bl, ok := b.(*ast.BigIntLit)
		return ok && al.Value.Cmp(&bl.Value) == 0
	case *ast.NullLit:
		_, ok := b.(*ast.NullLit)
		return ok
	case *ast.UndefinedLit:
		_, ok := b.(*ast.UndefinedLit)
		return ok
	}
	return false
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
// accepts, ranking the two axes the emitted guard can tell apart. It mirrors
// objectSubsumes in internal/solver.
//
// It ranks two axes and ignores a third, exactly as objectSubsumes does.
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
// here and fall back to declaration order.
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
		if !found || match.Optional || !sameGuardedType(match.Value, prop.Value) {
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

// guardAcceptsAnything reports whether buildTypeGuard emits a bare `true` for typeAnn,
// which is what it falls back to for an annotation it cannot test at runtime — a union,
// a type parameter, a function type. Such an arm accepts every argument that reaches
// it, so DispatchOrder has to sort it after every arm that tests something.
func guardAcceptsAnything(typeAnn ast.TypeAnn) bool {
	switch t := typeAnn.(type) {
	case nil:
		return true // an un-annotated parameter contributes no guard at all
	case *ast.NumberTypeAnn, *ast.StringTypeAnn, *ast.BooleanTypeAnn,
		*ast.LitTypeAnn, *ast.ObjectTypeAnn, *ast.TupleTypeAnn:
		return false
	case *ast.TypeRefTypeAnn:
		if _, nominal := nominalGuardName(t); nominal {
			return false
		}
		return !isArrayTypeRef(t)
	default:
		return true
	}
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
