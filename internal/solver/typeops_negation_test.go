package solver

import (
	"testing"

	"github.com/escalier-lang/escalier/internal/set"
	"github.com/escalier-lang/escalier/internal/soltype"
	"github.com/stretchr/testify/require"
)

// A complement has no annotation syntax, so a test that needs one builds it as a solver type
// through normal.go's negate, the way constrain_nf_rules_test.go does. A case that builds its
// operands leads with the type it built, written in annotation syntax with `¬` for the complement,
// since the constructor calls are harder to read than the type they stand for.

// reduceType reduces one type against an empty alias environment, the reduction constrain performs
// when it checks a constraint against a residual operator.
func reduceType(t soltype.Type) soltype.Type {
	return newTypeEvaluator(&Context{}, newSeenPairs()).reduce(t)
}

// meet builds an unnormalized intersection, so a test states the members it means rather than the
// canonical order newIntersection would put them in.
func meet(types ...soltype.Type) soltype.Type {
	return &soltype.IntersectionType{Types: types}
}

// join builds an exact union of the given members.
func join(types ...soltype.Type) soltype.Type {
	return &soltype.UnionType{Types: types}
}

// A meet carrying a complement is a set difference, and reduction settles each member of its
// positive side against each excluded type. Every row states the difference as a solver type and
// asserts what it reduces to.
//
// The rows cover the four outcomes a member can meet: it is excluded outright, the exclusion is
// disjoint from it, the exclusion cuts into it and the complement stays, or an operand is not ground
// enough to decide any of those.
func TestReduceSetDifference(t *testing.T) {
	tests := []struct {
		name string
		diff soltype.Type
		want string
	}{
		{
			// `("a" | "b" | "c") & ¬"b"`
			//
			// The key set an `Omit<T, "b">` maps over. Each key is a literal, so each is either
			// the excluded one or disjoint from it, and the difference is a plain key union the
			// mapped type can iterate.
			name: "KeySetDropsOneKey",
			diff: meet(join(strLit("a"), strLit("b"), strLit("c")), negate(strLit("b"))),
			want: `"a" | "c"`,
		},
		{
			// `("a" | "b" | "c") & ¬"a" & ¬"c"`
			//
			// Two exclusions each drop their own member.
			name: "KeySetDropsTwoKeys",
			diff: meet(join(strLit("a"), strLit("b"), strLit("c")), negate(strLit("a")), negate(strLit("c"))),
			want: `"b"`,
		},
		{
			// `("a" | "b") & ¬("a" | "b")`
			//
			// Excluding the whole positive side leaves the empty type.
			name: "EverythingExcluded",
			diff: meet(join(strLit("a"), strLit("b")), negate(join(strLit("a"), strLit("b")))),
			want: "never",
		},
		{
			// `number & ¬string`
			//
			// No number is a string, so the exclusion removes nothing and the complement goes.
			name: "DisjointExclusionDrops",
			diff: meet(num(), negate(str())),
			want: "number",
		},
		{
			// `string & ¬"a"`
			//
			// `"a"` is one of the strings the exclusion names, and the strings that are left are
			// not a union of anything at hand, so the complement stays. This is the answer
			// TypeScript's distributive `Exclude<string, "a">` cannot express, where it yields
			// `string` instead.
			name: "PartialOverlapKeepsComplement",
			diff: meet(str(), negate(strLit("a"))),
			want: `string & ¬"a"`,
		},
		{
			// `(string | number) & ¬"a"`
			//
			// The exclusion cuts into one member and misses the other, so only the member it cuts
			// into keeps the complement.
			name: "PartialOverlapOnOneMemberOnly",
			diff: meet(join(str(), num()), negate(strLit("a"))),
			want: `number | string & ¬"a"`,
		},
		{
			// `("a" | "b" | ...) & ¬"a"`
			//
			// An open tail names members the reduction cannot enumerate, so which of them the
			// exclusion removes is undecided and the tail survives.
			name: "InexactPositiveSideKeepsTail",
			diff: meet(&soltype.UnionType{Types: []soltype.Type{strLit("a"), strLit("b")}, Inexact: true}, negate(strLit("a"))),
			want: `"b" | ...`,
		},
		{
			// `(string | null) & ¬(null | undefined)`
			//
			// `NonNullable<string | null>` written natively. Each member is settled against the
			// exclusion union: `null` is inside it and goes, and `string` is disjoint from both
			// absence markers, so it stays bare. The filter reading answers the same, which
			// TestGroundSetDifferenceKeepsFilterReading asserts.
			name: "NonNullableDropsTheAbsenceMarkers",
			diff: meet(join(str(), &soltype.NullType{}), negate(join(&soltype.NullType{}, &soltype.UndefinedType{}))),
			want: "string",
		},
		{
			// `("a" | ...) & ¬"a"`
			//
			// Every named member is excluded and the tail is undecided, so the difference stays
			// as it stands. Building the union would answer `never`, which claims the tail is
			// empty too.
			name: "InexactPositiveSideWithNoSurvivorsStays",
			diff: meet(&soltype.UnionType{Types: []soltype.Type{strLit("a")}, Inexact: true}, negate(strLit("a"))),
			want: `("a" | ...) & ¬"a"`,
		},
		{
			// `("a" | "b" | ...string) & ¬"a"`
			//
			// A bounded tail draws its members from `string`, so the exclusion applies to that
			// bound the same way it applies to a named member. The tail comes through narrowed
			// rather than undecided.
			name: "BoundedTailNarrowsWithTheNamedMembers",
			diff: meet(newBoundedUnion(nil, []soltype.Type{strLit("a"), strLit("b")}, str()), negate(strLit("a"))),
			want: `"b" | ... : (string & ¬"a")`,
		},
		{
			// `("a" | ...string) & ¬"a"`
			//
			// The case InexactPositiveSideWithNoSurvivorsStays leaves standing, settled. The
			// answer is the string keys other than "a", a union naming no member and drawing
			// every one it has from `string & ¬"a"`.
			name: "BoundedTailSurvivesWithNoNamedMember",
			diff: meet(newBoundedUnion(nil, []soltype.Type{strLit("a")}, str()), negate(strLit("a"))),
			want: `... : (string & ¬"a")`,
		},
		{
			// `("a" | ...string) & ¬string`
			//
			// The exclusion empties the bound, so the tail contributes nothing, and it excludes
			// the one named member too. Nothing is left.
			name: "BoundedTailEmptiedByItsOwnBound",
			diff: meet(newBoundedUnion(nil, []soltype.Type{strLit("a")}, str()), negate(str())),
			want: "never",
		},
		{
			// `(("a" | "b") & ¬"a") & ¬"b"`
			//
			// Nested complements reduce as one difference, since newIntersection flattens the
			// meet before the members are settled.
			name: "NestedMeetFlattens",
			diff: meet(meet(join(strLit("a"), strLit("b")), negate(strLit("a"))), negate(strLit("b"))),
			want: "never",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, soltype.Print(reduceType(tt.diff)))
		})
	}
}

// A meet with no complement is not a difference, but its members still reduce, so an operator
// written as one of them reaches its value. `"a" & keyof {…}` is the shape keyofUnion mints for a
// union operand it could not read every member of, and it has to ground the same way any other key
// set does.
func TestReduceIntersectionMembers(t *testing.T) {
	// `"a" & keyof {a: number, b: string}`
	t.Run("OperatorMemberReduces", func(t *testing.T) {
		keys := &soltype.KeyofType{Operand: exactObj(propElem("a", num()), propElem("b", str()))}
		require.Equal(t, `"a"`, soltype.Print(reduceType(meet(strLit("a"), keys))))
	})

	// `number & string`
	t.Run("NothingToReduceKeepsThePointer", func(t *testing.T) {
		concrete := meet(num(), str())
		require.Same(t, concrete, reduceType(concrete))
	})

	// `("a" | "b" | 1) & string`, which is `Extract<"a" | "b" | 1, string>` written natively.
	//
	// The meet stays as it stands. A difference settles each member of its positive side against
	// what is excluded, and a plain meet has no such rule, so nothing here distributes `string`
	// over the union or drops the member no value inhabits. The filter reading is what answers
	// this one, and TestUtilityTypeReductions/Extract asserts `"a" | "b"` for it.
	//
	// The two are only ever asked of different operands. A meet is minted for `Extract` when the
	// operand does not ground, and an operand that does ground takes the filter instead, so the
	// concrete meet above is reachable only by building it.
	t.Run("ExtractMeetDoesNotDistribute", func(t *testing.T) {
		extract := meet(join(strLit("a"), strLit("b"), numLit(1)), str())
		require.Same(t, extract, reduceType(extract))
	})
}

// `{[K: string]: number} & ¬T`
//
// Every operand of a difference is reduced once, so a diagnostic the reduction records is recorded
// once. The positive side is a required index signature over a key set with no keys to enumerate,
// which draws one diagnostic on its own. The whole meet is the shape
// `Exclude<{[K: string]: number}, T>` mints, and the evaluator's diagnostics reach the user through
// the constraint site that reduced the residual.
func TestReduceSetDifferenceRecordsOneDiagnostic(t *testing.T) {
	required := &soltype.ObjectType{Elems: []soltype.ObjTypeElem{&soltype.MappedElem{
		Key:   &soltype.MappedKeyType{ID: 0, Name: "K"},
		Keys:  str(),
		Value: num(),
	}}}
	c := &Context{}
	e := newTypeEvaluator(c, newSeenPairs())
	e.reduce(meet(required, negate(c.freshVar(0))))
	require.Equal(t, []string{
		"no object has a field at every key of string, so [K: string]: number is uninhabited; " +
			"write [K: string]?: number instead",
	}, Messages(e.errs))
}

// A difference over an operand that is not ground stays a difference. The `∩ ¬` form is itself the
// answer, so the reduction hands back a type the caller can store and reduce again later, rather
// than a stuck operator. Each row builds its variable at level 0, the level a ground operand sits
// at.
func TestReduceSetDifferenceStaysSymbolic(t *testing.T) {
	tests := []struct {
		name string
		diff func(v *soltype.TypeVarType) soltype.Type
		want string
	}{
		{
			// `T & ¬string`
			//
			// The `Exclude<T, string>` residual. Nothing is known about T's members, so nothing
			// can be dropped from it.
			name: "VariablePositiveSide",
			diff: func(v *soltype.TypeVarType) soltype.Type { return meet(v, negate(str())) },
			want: "t0 & ¬string",
		},
		{
			// `("a" | "b") & ¬T`
			//
			// The exclusion is the variable this time, so no member of the positive side can be
			// settled against it.
			name: "VariableExclusion",
			diff: func(v *soltype.TypeVarType) soltype.Type {
				return meet(join(strLit("a"), strLit("b")), negate(v))
			},
			want: `("a" | "b") & ¬t0`,
		},
		{
			// `keyof T & ¬"b"`
			//
			// A `keyof T` operand is not ground either, and the difference keeps it whole so the
			// key set reduces once T does.
			name: "ResidualOperatorPositiveSide",
			diff: func(v *soltype.TypeVarType) soltype.Type {
				return meet(&soltype.KeyofType{Operand: v}, negate(strLit("b")))
			},
			want: `¬"b" & keyof t0`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := &Context{}
			require.Equal(t, tt.want, soltype.Print(reduceType(tt.diff(c.freshVar(0)))))
		})
	}
}

// nativeDifferenceDecls writes the set-difference utilities the way TypeScript's library does, as
// distributive conditionals that filter their own operand. Reduction rewrites one whose operand it
// cannot filter into the meet it denotes, so each utility below has an answer over a type variable.
//
// `MapKeys<T, Ks>` maps T's value types over the key set Ks. `Omit<T, Ks>` is that mapped type over
// the key set `Exclude<keyof T, Ks>`, which is the native form of the utility — the keys of T less
// the ones Ks names — rather than the per-key filter clause the suite in utility_types_test.go
// writes it with.
const nativeDifferenceDecls = `
	type Exclude<U, V> = if U : V { never } else { U }
	type Extract<U, V> = if U : V { U } else { never }
	type NonNullable<T> = if T : null | undefined { never } else { T }
	type MapKeys<T, Ks> = {[K]: T[K] for K in Ks}
	type Point = {x: number, y: string}
	type PointKeys = "x" | "y"
`

// The set-difference family over an operand that does not ground. Each row applies one utility to a
// type parameter and asserts the `∩ ¬` form it reduces to, the answer the distributive filter has
// none of.
//
// The type variables render as `t<id>`, and the ids are those the declarations above draw, so a
// declaration added to nativeDifferenceDecls shifts them.
func TestNativeSetDifferenceOverTypeVariable(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want string
	}{
		{
			// `Exclude<T, V>` is `T ∩ ¬V`.
			name: "Exclude",
			src:  `type Result<T> = Exclude<T, string>`,
			want: "t16 & ¬string",
		},
		{
			// `Extract<T, V>` is the meet itself, with no complement to take.
			name: "Extract",
			src:  `type Result<T> = Extract<T, string>`,
			want: "t16 & string",
		},
		{
			// `NonNullable<T>` excludes the two absence markers together, since the conditional
			// names them as one union.
			name: "NonNullable",
			src:  `type Result<T> = NonNullable<T>`,
			want: "t16 & ¬(null | undefined)",
		},
		{
			// Both operands may be variables. The difference is still representable, and it is
			// what makes the rule total rather than only more precise.
			name: "ExcludeOverTwoVariables",
			src:  `type Result<T, U> = Exclude<T, U>`,
			want: "t16 & ¬t17",
		},
		{
			// Excluding the empty type removes nothing. `¬never` is `unknown`, the identity of a
			// meet, so the difference is the operand itself.
			name: "ExcludeOfNever",
			src:  `type Result<T> = Exclude<T, never>`,
			want: "t16",
		},
		{
			// Excluding every value leaves nothing, and `¬unknown` is `never`. The meet does not
			// collapse on that `never` yet, which is the uninhabited-intersection gap #927 tracks.
			name: "ExcludeOfUnknown",
			src:  `type Result<T> = Exclude<T, unknown>`,
			want: "never & t16",
		},
		{
			// `Omit`'s key set over a type parameter: the keys of T other than "x". Neither half
			// grounds, so the whole key set stays a difference.
			name: "OmitKeySetOverVariable",
			src:  `type Result<T> = Exclude<keyof T, "x">`,
			want: `¬"x" & keyof t16`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			nodes, ctx, errs := inferTypeNodes(t, nativeDifferenceDecls+tt.src)
			require.Empty(t, errs)
			require.Equal(t, tt.want, soltype.Print(expandAliasResidual(ctx, nodes["Result"])))
		})
	}
}

// `Exclude` rewrites to a difference whatever the excluded operand names, including a borrow.
// The residual `T & ¬(&'a Point)` is the complement of one particular borrow, so it admits a
// borrow of another type and a borrow of Point under a different lifetime. The assertions
// pass the alias's declared lifetimes to the printer, so that `'a` is visible rather than
// collapsing to the bare `&` an un-named lifetime falls back to.
//
// Two distinct borrows are not disjoint, so a residual naming one does not reduce further.
// That matches an object, which is absent from the value families for the same reason.
func TestSetDifferenceOverABorrow(t *testing.T) {
	const decls = `
		type Exclude<U, V> = if U : V { never } else { U }
		type Point = {x: number}
		type Handle = &Point
	`
	tests := []struct {
		name string
		src  string
		want string
	}{
		{
			name: "BorrowExclusion",
			src:  `type Result<T, 'a> = Exclude<T, &'a Point>`,
			want: "t6 & ¬&'a Point",
		},
		{
			// De Morgan puts the borrow in a negated part of its own once the complement
			// distributes, which is an ordinary meet of complements.
			name: "BorrowInExcludedUnion",
			src:  `type Result<T, 'a> = Exclude<T, &'a Point | string>`,
			want: "t6 & ¬(string | &'a Point)",
		},
		{
			// An alias naming a borrow reduces the same way. The residual keeps the alias
			// name, since the difference is taken over what the name stands for.
			name: "AliasNamingABorrow",
			src:  `type Result<T> = Exclude<T, Handle>`,
			want: "t6 & ¬Handle",
		},
		{
			// A borrow inside an atom is a field of the object the negated part names.
			name: "BorrowInsideAnExcludedAtom",
			src:  `type Result<T, 'a> = Exclude<T, {a: &'a Point}>`,
			want: "t6 & ¬{a: &'a Point}",
		},
		{
			// A borrow on the positive side rides through as a member, since the
			// complement names `string`.
			name: "BorrowOnThePositiveSide",
			src:  `type Result<T, 'a> = Exclude<T | &'a Point, string>`,
			want: "(t6 | &'a Point) & ¬string",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			nodes, ctx, errs := inferTypeNodes(t, decls+tt.src)
			require.Empty(t, errs)
			residual := expandAliasResidual(ctx, nodes["Result"])
			got := soltype.PrintWithDeclaredParams(residual, nil, aliasLifetimeParams(ctx, "Result"))
			require.Equal(t, tt.want, got)
		})
	}
}

// A conditional keeps the filter reading whenever it can run it, so a ground operand reduces the
// way TypeScript's does and the two readings only ever differ where the filter had no answer. The
// rows below are the ground counterparts of the ones above.
func TestGroundSetDifferenceKeepsFilterReading(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want string
	}{
		{
			// The filter drops the members that are subtypes of the excluded type and keeps the
			// rest whole.
			name: "ExcludeOverGroundUnion",
			src:  `type Result = Exclude<"a" | "b" | 1, string>`,
			want: "1",
		},
		{
			// The divergence between the two readings, from the side the filter decides.
			// `string` is not a subtype of `"a"`, so the filter keeps it whole where the
			// difference would answer `string ∩ ¬"a"`.
			name: "ExcludeOfLiteralFromPrimitive",
			src:  `type Result = Exclude<string, "a">`,
			want: "string",
		},
		{
			name: "NonNullableOverGroundUnion",
			src:  `type Result = NonNullable<string | null>`,
			want: "string",
		},
		{
			// `Omit<Point, "x">` written natively, as a mapped type over the key set
			// `Exclude<keyof Point, "x">`. Every operand is ground, so the key set reduces to
			// `"y"` and the mapped type emits that one field.
			name: "OmitOverGroundObject",
			src:  `type Result = MapKeys<Point, Exclude<keyof Point, "x">>`,
			want: "{y: string}",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			nodes, ctx, errs := inferTypeNodes(t, nativeDifferenceDecls+tt.src)
			require.Empty(t, errs)
			require.Equal(t, tt.want, soltype.Print(expandAliasResidual(ctx, nodes["Result"])))
		})
	}
}

// A mapped type iterates a key set that is a Boolean combination rather than a plain literal union.
// The key set reduces to the keys it names before the mapped type splits it, so a difference
// enumerates the same way a union does and the mapped type emits one field per surviving key.
//
// Each case reduces `MapKeys<Point, Keys>` over the key set its row names, built as a solver type
// since only a reduction that began before its operands ground produces a difference. `Point` is
// `{x: number, y: string}` and `PointKeys` is `"x" | "y"`, both from nativeDifferenceDecls.
func TestMappedTypeIteratesBooleanKeySet(t *testing.T) {
	point := &soltype.AliasType{Name: "Point"}
	keyofPoint := &soltype.KeyofType{Operand: point}

	tests := []struct {
		name string
		keys soltype.Type
		want string
	}{
		{
			// `keyof Point & ¬"x"`, which is `Omit<Point, "x">`'s key set. `keyof Point` grounds
			// to `"x" | "y"`, the difference drops "x", and the mapped type emits the one field
			// left.
			name: "KeyofLessOneKey",
			keys: meet(keyofPoint, negate(strLit("x"))),
			want: "{y: string}",
		},
		{
			// `keyof Point & ¬("x" | "y")`. Excluding every key leaves nothing to iterate, so the
			// mapped type emits an empty object rather than staying symbolic.
			name: "KeyofLessEveryKey",
			keys: meet(keyofPoint, negate(join(strLit("x"), strLit("y")))),
			want: "{}",
		},
		{
			// `keyof Point & ¬"z"`. An exclusion naming no key of the source removes nothing.
			name: "ExclusionMissesEveryKey",
			keys: meet(keyofPoint, negate(strLit("z"))),
			want: "{x: number, y: string}",
		},
		{
			// `PointKeys & ¬"x"`. A named key set on the positive side expands to the keys it
			// stands for, the way every other operand of a reduction does.
			name: "AliasKeySetLessOneKey",
			keys: meet(&soltype.AliasType{Name: "PointKeys"}, negate(strLit("x"))),
			want: "{y: string}",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, ctx, errs := inferTypeNodes(t, nativeDifferenceDecls)
			require.Empty(t, errs)
			inst := &soltype.AliasType{Name: "MapKeys", TypeArgs: []soltype.Type{point, tt.keys}}
			require.Equal(t, tt.want, soltype.Print(expandAliasResidual(ctx, inst)))
		})
	}
}

// `keyof` over a union is the meet of its members' key sets, since a value of the union is one of
// its members and only a key every member carries can be read from it. A member with no enumerable
// key set contributes its own `keyof` residual to that meet rather than stopping the reduction, so
// the law holds over a type parameter too.
func TestReduceKeyofDistributesOverUnion(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want string
	}{
		{
			// One member's keys are known and the other's are not, so the meet names both.
			name: "OneVariableMember",
			src:  `type Result<T> = keyof (T | {a: number})`,
			want: `"a" & keyof t16`,
		},
		{
			// Neither member has a key set to read, and the meet is still the answer.
			name: "TwoVariableMembers",
			src:  `type Result<T, U> = keyof (T | U)`,
			want: "keyof t16 & keyof t17",
		},
		{
			// The readable members share no key, so the meet is empty whatever the variable's
			// keys turn out to be, and the reduction stops before consulting it.
			name: "DisjointReadableMembers",
			src:  `type Result<T> = keyof ({a: number} | {b: number} | T)`,
			want: "never",
		},
		{
			// With every member readable the meet is the shared keys alone.
			name: "AllMembersReadable",
			src:  `type Result = keyof ({a: number, shared: string} | {b: boolean, shared: string})`,
			want: `"shared"`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			nodes, ctx, errs := inferTypeNodes(t, nativeDifferenceDecls+tt.src)
			require.Empty(t, errs)
			require.Equal(t, tt.want, soltype.Print(expandAliasResidual(ctx, nodes["Result"])))
		})
	}
}

// `keyof ¬{x: number}`
//
// `keyof ¬T` is the empty key set. A complement admits every value its operand rejects, and no key
// can be read from all of them.
func TestReduceKeyofComplement(t *testing.T) {
	require.Equal(t, "never", soltype.Print(reduceType(&soltype.KeyofType{Operand: negate(exactObj(propElem("x", num())))})))
}

// A conditional decides its branch with the solver's subtyping relation, so the arrow-intersection
// rules that relation carries are observable through a conditional type. These are the worked
// examples of planning/ml_struct/04-type-level-operators.md.
//
// Both examples ask whether a value carrying two arrow types is callable with the union of their
// domains. TypeScript answers "not" to both, reading the intersection as an overload table and
// trying each signature on its own. Escalier reads it set-theoretically and answers each example on
// its own terms.
func TestConditionalOverArrowIntersection(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want string
	}{
		{
			// Example A. Both arms return a boolean, so a value carrying both returns a boolean
			// for every input in `number | string` and genuinely has the target type. Escalier
			// diverges from TypeScript here and is sound in doing so.
			name: "CodomainsAgree",
			src: `
				type Fn = (fn (x: number) -> boolean) & (fn (x: string) -> boolean)
				type Result = if Fn : fn (x: number | string) -> boolean { "callable" } else { "not" }
			`,
			want: `"callable"`,
		},
		{
			// Example B. The string arm returns null, so feeding the value a string yields a
			// null rather than a boolean and the target type is a false claim. Escalier rejects
			// it, reconverging with TypeScript. MLstruct accepts it instead, since it merges the
			// two arms into `(number | string) -> (boolean & null)` and compares that one arrow.
			// Escalier keeps the arms apart and decides them by the Frisch-Castagna-Benzaken
			// decomposition. See internal/solver/constrain_nf.go.
			name: "CodomainsConflict",
			src: `
				type Fn = (fn (x: number) -> boolean) & (fn (x: string) -> null)
				type Result = if Fn : fn (x: number | string) -> boolean { "callable" } else { "not" }
			`,
			want: `"not"`,
		},
		{
			// The `infer` face of example A. The two arms fuse into one arrow over the union of
			// their domains, so the capture binds that union where TypeScript's `Parameters`
			// binds the last overload's parameter alone.
			name: "ParametersBindsTheUnionDomain",
			src: `
				type Fn = (fn (x: number) -> boolean) & (fn (x: string) -> boolean)
				type Result = if Fn : fn (...args: infer P) -> _ { P } else { never }
			`,
			want: "[number | string]",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			nodes, ctx, errs := inferTypeNodes(t, tt.src)
			require.Empty(t, errs)
			require.Equal(t, tt.want, soltype.Print(expandAliasResidual(ctx, nodes["Result"])))
		})
	}
}

// An `infer` pattern binds a capture by aligning against structure the scrutinee carries. A
// complement names the values its operand rejects rather than any structure, so it takes no part in
// the match. Each case applies a capturing conditional to an operand built as a solver type, since
// a complement has no annotation syntax.
func TestInferMatchesPositiveSkeleton(t *testing.T) {
	const src = `
		type Elem<T> = if T : Array<infer R> { R } else { never }
	`
	tests := []struct {
		name  string
		scrut soltype.Type
		want  string
	}{
		{
			// `Elem<Array<number> & ¬string>`. The positive member is what the pattern aligns
			// with, so the capture binds the element type of the array the meet carries.
			name:  "ComplementMemberIsSkipped",
			scrut: meet(&soltype.ArrayType{Elem: num()}, negate(str())),
			want:  "number",
		},
		{
			// `Elem<¬Array<number>>`. A scrutinee that is nothing but a complement has no
			// skeleton to align, so the match fails and the conditional takes its Else branch.
			name:  "BareComplementMatchesNothing",
			scrut: negate(&soltype.ArrayType{Elem: num()}),
			want:  "never",
		},
		{
			// `Elem<number & ¬string>`. The positive part is what decides a failed match too. No
			// array is a number, so the pattern rejects the meet whatever it excludes.
			name:  "PositivePartStillDecidesAFailedMatch",
			scrut: meet(num(), negate(str())),
			want:  "never",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, ctx, errs := inferTypeNodes(t, src)
			require.Empty(t, errs)
			inst := &soltype.AliasType{Name: "Elem", TypeArgs: []soltype.Type{tt.scrut}}
			require.Equal(t, tt.want, soltype.Print(expandAliasResidual(ctx, inst)))
		})
	}
}

// Contractivity: a recursion that returns to its own alias through a complement emits no structure,
// so `type T = ¬T` names no type. It is the equation `T = ¬T`, which no set satisfies, and the
// productivity check rejects it for the same reason it rejects `type T = T`.
//
// A complement has no annotation syntax, so the bodies are built as solver types and handed to the
// collector checkProductive builds its reference graph with.
func TestNegationGuardsNoRecursion(t *testing.T) {
	tests := []struct {
		name string
		body soltype.Type
		want []string
	}{
		{
			// `type T = ¬T`. The reference is reached with no constructor in between.
			name: "SelfComplement",
			body: negate(&soltype.AliasType{Name: "T"}),
			want: []string{"T"},
		},
		{
			// `type T = ¬¬T`. Two complements are no more of a constructor than one.
			name: "DoubleComplement",
			body: negate(negate(&soltype.AliasType{Name: "T"})),
			want: []string{"T"},
		},
		{
			// `type T = {a: ¬T}`. The object is the constructor, and a complement under one is
			// as productive as any other member type.
			name: "ComplementUnderAnObject",
			body: exactObj(propElem("a", negate(&soltype.AliasType{Name: "T"}))),
			want: []string{},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			collector := &unguardedRefCollector{
				within: set.FromSlice([]string{"T"}),
				found:  set.NewSet[string](),
			}
			tt.body.Accept(collector, soltype.Positive)
			require.Equal(t, tt.want, collector.found.ToSlice())
		})
	}
}
