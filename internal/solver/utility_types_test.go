package solver

import (
	"testing"

	"github.com/escalier-lang/escalier/internal/soltype"
	"github.com/stretchr/testify/require"
)

// utilityTypeDecls is the TypeScript utility-type suite written in Escalier. Every test in this
// file prepends it, so one definition of each utility backs every assertion below.
//
// Nothing here is compiler machinery. Each utility is an ordinary generic type alias built from
// the type-level operators, so the suite is the end-to-end check that those operators compose the
// way TypeScript's do. Library resolution is import-only, with no ambient global library, so these
// are also the definitions a `std:*` module would export once the solver resolves such imports.
//
// The three translations from TypeScript's surface syntax to Escalier's:
//
//   - `{[K]?: T[K] for K in keyof T}` is the mapped type TypeScript writes
//     `{[K in keyof T]?: T[K]}`.
//   - `if C : E { A } else { B }` is the conditional TypeScript writes `C extends E ? A : B`.
//   - The trailing `if K : Ks` clause on a mapped type filters its key set, dropping every key
//     that fails the test. TypeScript has no filter clause and expresses the same thing as an `as`
//     remapping to `never`. Escalier has that bracketed form too, which `Getters<T>` in
//     TestUtilityTypeComposition uses to rename rather than to drop.
//
// `Omit<T, Ks>` reads `Exclude`, so the two definitions are coupled. Order in this source does not
// matter, since the dependency graph orders the declarations.
//
// `ReturnType<F>` bounds `F` to `fn () -> unknown`, so an argument that is not a nullary function
// is rejected at the reference. `unknown` is the top of the lattice and a function type's return is
// covariant, so that bound admits every nullary function and no other type. The bound is enforced,
// which TestUtilityTypeAliasParameterConstraint pins directly.
//
// Nullary is not a choice; it is the arity the pattern can match.
// TestUtilityTypeReturnTypeIsAritySpecific records why the arity-agnostic TypeScript definition has
// no Escalier equivalent, and the bound turns what was a silent reduction to `never` for every
// other arity into a diagnostic.
//
// `NonNullable<T>`, `Parameters<F>`, `ConstructorParameters<C>`, and `InstanceType<C>` are the
// four utilities the suite cannot express yet. Each has a disabled test at the end of this file
// naming what it waits on.
//
// Two declarations at the end are not TypeScript utilities. `EventName<K>` is a template-literal
// type that builds a handler name from an event name. `Point` is the sample object several tests
// map over.
const utilityTypeDecls = `
	type Partial<T> = {[K]?: T[K] for K in keyof T}
	type Required<T> = {[K]-?: T[K] for K in keyof T}
	type Readonly<T> = {readonly [K]: T[K] for K in keyof T}
	type Pick<T, Ks> = {[K]: T[K] for K in keyof T if K : Ks}
	type Omit<T, Ks> = {[K]: T[K] for K in keyof T if K : Exclude<K, Ks>}
	type Record<Ks, V> = {[K: Ks]: V}
	type Exclude<U, V> = if U : V { never } else { U }
	type Extract<U, V> = if U : V { U } else { never }
	type ReturnType<F: fn () -> unknown> = if F : fn () -> infer R { R } else { never }
	type Awaited<T> = if T : Promise<infer U> { Awaited<U> } else { T }
	type EventName<K> = ` + "`on${Capitalize<K>}`" + `
	type Point = {x: number, y: string}
`

// utilityReduction is the shape every reduction table in this file uses. src declares a `Result`
// alias over the utilities. wantExpanded is what TypeScript reduces the same application to.
type utilityReduction struct {
	name         string
	src          string
	wantExpanded string
}

// runUtilityReductions infers each case's source with utilityTypeDecls prepended and asserts what
// its `Result` alias reduces to. A stored alias reference stays symbolic, so the assertion runs
// through expandAliasResidual, the expansion constrain performs when it checks a constraint
// against such a reference.
func runUtilityReductions(t *testing.T, tests []utilityReduction) {
	t.Helper()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			nodes, ctx, errs := inferTypeNodes(t, utilityTypeDecls+tt.src)
			require.Empty(t, errs)
			require.Equal(t, tt.wantExpanded, soltype.Print(expandAliasResidual(ctx, nodes["Result"])))
		})
	}
}

// Each case applies one utility to ground arguments and asserts the reduction against what
// TypeScript reduces the same application to.
func TestUtilityTypeReductions(t *testing.T) {
	runUtilityReductions(t, []utilityReduction{
		// `Partial<T>` adds `?` to every field. The mapped type's `?` modifier is what does it.
		{
			name:         "Partial",
			src:          `type Result = Partial<{a: number, b: string}>`,
			wantExpanded: "{a?: number, b?: string}",
		},
		{
			// Applying it twice changes nothing, since the modifier adds a marker a field may
			// already carry.
			name:         "PartialIsIdempotent",
			src:          `type Result = Partial<Partial<{a: number}>>`,
			wantExpanded: "{a?: number}",
		},
		{
			// `keyof {}` is `never`, the empty key set, so the map emits no fields.
			name:         "PartialOfEmptyObject",
			src:          `type Result = Partial<{}>`,
			wantExpanded: "{}",
		},
		// `Required<T>` clears `?` from every field, the `-?` modifier's job. A field that was
		// already required is unaffected.
		{
			name:         "Required",
			src:          `type Result = Required<{a?: number, b: string}>`,
			wantExpanded: "{a: number, b: string}",
		},
		{
			// The two modifiers invert each other, so `Required` undoes `Partial` exactly.
			name:         "RequiredUndoesPartial",
			src:          `type Result = Required<Partial<Point>>`,
			wantExpanded: "{x: number, y: string}",
		},
		// `Readonly<T>` marks every field readonly.
		{
			name:         "Readonly",
			src:          `type Result = Readonly<Point>`,
			wantExpanded: "{readonly x: number, readonly y: string}",
		},
		{
			// The two modifiers are independent, so a readonly map over an optional one keeps both
			// markers on each field.
			name:         "ReadonlyOfPartial",
			src:          `type Result = Readonly<Partial<{a: number}>>`,
			wantExpanded: "{readonly a?: number}",
		},
		// `Pick<T, Ks>` keeps the keys `Ks` names. The `if K : Ks` filter drops every key that is
		// not a subtype of `Ks`, and the value expression `T[K]` reads each kept key's type.
		{
			name:         "Pick",
			src:          `type Result = Pick<{a: number, b: string, c: boolean}, "a" | "c">`,
			wantExpanded: "{a: number, c: boolean}",
		},
		{
			name:         "PickSingleKey",
			src:          `type Result = Pick<Point, "x">`,
			wantExpanded: "{x: number}",
		},
		{
			// The map is homomorphic, written over `keyof T`, so each kept field carries the
			// source member's own `?` and `readonly` markers.
			name:         "PickKeepsMarkers",
			src:          `type Result = Pick<{a?: number, readonly b: string, c: boolean}, "a" | "b">`,
			wantExpanded: "{a?: number, readonly b: string}",
		},
		{
			// `never` is the empty key set, so every key fails the filter.
			name:         "PickNoKeys",
			src:          `type Result = Pick<{a: number}, never>`,
			wantExpanded: "{}",
		},
		// `Omit<T, Ks>` is `Pick`'s complement, and the same filter clause expresses it. `Exclude`
		// sends a key named by `Ks` to `never` and every other key to itself, so `K : Exclude<K,
		// Ks>` reads as "keep `K` when it survives the exclusion". A dropped key tests `"b" <:
		// never`, which fails.
		{
			name:         "Omit",
			src:          `type Result = Omit<{a: number, b: string, c: boolean}, "b">`,
			wantExpanded: "{a: number, c: boolean}",
		},
		{
			name:         "OmitSeveralKeys",
			src:          `type Result = Omit<{a: number, b: string, c: boolean}, "a" | "c">`,
			wantExpanded: "{b: string}",
		},
		{
			name:         "OmitKeepsMarkers",
			src:          `type Result = Omit<{a?: number, readonly b: string}, "b">`,
			wantExpanded: "{a?: number}",
		},
		// `Record<Ks, V>` gives every key in `Ks` the same value type. The value expression never
		// names the key, so no indexed access is involved.
		{
			name:         "Record",
			src:          `type Result = Record<"a" | "b", number>`,
			wantExpanded: "{a: number, b: number}",
		},
		{
			name:         "RecordOverKeyof",
			src:          `type Result = Record<keyof Point, boolean>`,
			wantExpanded: "{x: boolean, y: boolean}",
		},
		{
			// A primitive key constraint has no keys to enumerate, so the map reduces to an index
			// signature rather than to a field list.
			name:         "RecordOverPrimitiveKey",
			src:          `type Result = Record<string, number>`,
			wantExpanded: "{[K: string]: number}",
		},
		// `Exclude<U, V>` drops the members of `U` assignable to `V`. `U` is a naked type
		// parameter, so the conditional distributes over the union and decides each member alone.
		{
			name:         "Exclude",
			src:          `type Result = Exclude<"a" | "b" | "c", "a">`,
			wantExpanded: `"b" | "c"`,
		},
		{
			// Dropping two of the three members leaves the survivor as a lone literal rather than
			// a one-member union.
			name:         "ExcludeSeveralMembers",
			src:          `type Result = Exclude<"a" | "b" | "c", "a" | "b">`,
			wantExpanded: `"c"`,
		},
		{
			// Excluding every member leaves `never`, the empty union.
			name:         "ExcludeEveryMember",
			src:          `type Result = Exclude<"a" | "b", "a" | "b">`,
			wantExpanded: "never",
		},
		{
			name:         "ExcludeMatchesNothing",
			src:          `type Result = Exclude<"a" | "b", "z">`,
			wantExpanded: `"a" | "b"`,
		},
		{
			// The members need not be literals. `string` is assignable to `string` and `number` is
			// not, so the primitive union narrows the same way a literal union does.
			name:         "ExcludeOverPrimitives",
			src:          `type Result = Exclude<string | number, string>`,
			wantExpanded: "number",
		},
		{
			// The key set an operator produced feeds `Exclude` like any other union.
			name:         "ExcludeOverKeyof",
			src:          `type Result = Exclude<keyof Point, "x">`,
			wantExpanded: `"y"`,
		},
		// `Extract<U, V>` is `Exclude`'s complement, keeping the members `Exclude` drops.
		{
			name:         "Extract",
			src:          `type Result = Extract<"a" | "b" | "c", "a" | "c">`,
			wantExpanded: `"a" | "c"`,
		},
		{
			name:         "ExtractMatchesNothing",
			src:          `type Result = Extract<"a" | "b", "z">`,
			wantExpanded: "never",
		},
		{
			name:         "ExtractOverPrimitives",
			src:          `type Result = Extract<string | number | boolean, string | boolean>`,
			wantExpanded: "string | boolean",
		},
		// `ReturnType<F>` reads the return type off a function through an `infer` capture.
		{
			name:         "ReturnType",
			src:          `type Result = ReturnType<fn () -> string>`,
			wantExpanded: "string",
		},
		// A non-function argument is rejected by the bound rather than reduced, so it lives in
		// TestUtilityTypeAliasParameterConstraint with the other rejections.
		// `Awaited<T>` strips every layer of `Promise`, recursing on its own capture.
		{
			name:         "Awaited",
			src:          `type Result = Awaited<Promise<Promise<number>>>`,
			wantExpanded: "number",
		},
		{
			// The composition an async caller writes to name the value a function resolves to. The
			// inner utility grounds first, then the outer one unwraps what it produced.
			name:         "AwaitedOfReturnType",
			src:          `type Result = Awaited<ReturnType<fn () -> Promise<number>>>`,
			wantExpanded: "number",
		},
		// The four string intrinsics reduce over a string-literal operand.
		{
			name:         "Uppercase",
			src:          `type Result = Uppercase<"hello">`,
			wantExpanded: `"HELLO"`,
		},
		{
			name:         "Lowercase",
			src:          `type Result = Lowercase<"HELLO">`,
			wantExpanded: `"hello"`,
		},
		{
			name:         "Capitalize",
			src:          `type Result = Capitalize<"hello">`,
			wantExpanded: `"Hello"`,
		},
		{
			name:         "Uncapitalize",
			src:          `type Result = Uncapitalize<"Hello">`,
			wantExpanded: `"hello"`,
		},
		{
			// An intrinsic maps over a union member-wise.
			name:         "CapitalizeOverUnion",
			src:          `type Result = Capitalize<"click" | "focus">`,
			wantExpanded: `"Click" | "Focus"`,
		},
		{
			// The template-literal case. The interpolation runs an intrinsic over an event-name
			// union, and the template takes the cartesian product over the union it produces.
			name:         "EventName",
			src:          `type Result = EventName<keyof {click: number, focus: string}>`,
			wantExpanded: `"onClick" | "onFocus"`,
		},
	})
}

// One utility applied to another is what the suite is really verifying. Each operator's result is
// an ordinary type, so it feeds the next operator with no special casing. Every expectation below
// matches TypeScript's reduction of the same composition.
func TestUtilityTypeComposition(t *testing.T) {
	runUtilityReductions(t, []utilityReduction{
		{
			// A narrowed key set, then an optional map over what survived.
			name:         "PartialOfPick",
			src:          `type Result = Partial<Pick<{a: number, b: string, c: boolean}, "a" | "b">>`,
			wantExpanded: "{a?: number, b?: string}",
		},
		{
			// Two key-set narrowings in sequence. `Omit` drops `c`, then `Pick` keeps `b` of what
			// is left.
			name:         "PickOfOmit",
			src:          `type Result = Pick<Omit<{a: number, b: string, c: boolean}, "c">, "b">`,
			wantExpanded: "{b: string}",
		},
		{
			// `Omit` reads its key set off the object `Partial` emitted, so the `?` markers
			// `Partial` added survive the drop.
			name:         "OmitOfPartial",
			src:          `type Result = Omit<Partial<Point>, "x">`,
			wantExpanded: "{y?: string}",
		},
		{
			name:         "ReadonlyOfRecord",
			src:          `type Result = Readonly<Record<"a" | "b", number>>`,
			wantExpanded: "{readonly a: number, readonly b: number}",
		},
		{
			// An alias reference in the value position is left symbolic rather than expanded, the
			// same transparent-but-named treatment an alias gets everywhere else. It reduces at the
			// constraint site, which TestUtilityTypeConstraints checks by assigning `{x: {y: 1}}`
			// to this type.
			name:         "RecordOfRecord",
			src:          `type Result = Record<"x", Record<"y", number>>`,
			wantExpanded: `{x: Record<"y", number>}`,
		},
		{
			// A key remapping that renames rather than drops. Each key is capitalized and prefixed
			// through a template literal, and the value expression still reads the original key.
			// This is the `Getters<T>` shape, and it needs no machinery beyond the mapped type, the
			// template literal, and the `Capitalize` intrinsic.
			name: "GettersRenameEveryKey",
			src: `
				type Getters<T> = {[` + "`get${Capitalize<K>}`" + `]: T[K] for K in keyof T}
				type Result = Getters<{name: string, age: number}>
			`,
			wantExpanded: "{getAge: number, getName: string}",
		},
	})
}

// A utility used as a value's annotation is the end-to-end path. constrain reduces the reference
// to check the initializer against it, with no test-only expansion in between. The accepting cases
// prove the reduction is reachable from a real constraint. The rejecting ones prove the diagnostic
// names the type the reduction arrived at rather than the annotation the source wrote.
func TestUtilityTypeConstraints(t *testing.T) {
	tests := []struct {
		name     string
		src      string
		wantErrs []string // empty ⇒ expect no error
	}{
		{
			// Every field is optional, so an object supplying one of the two keys is accepted.
			name: "PartialAcceptsPartialObject",
			src:  `val v: Partial<Point> = {x: 1}`,
		},
		{
			name: "PartialAcceptsEmptyObject",
			src:  `val v: Partial<Point> = {}`,
		},
		{
			// Optional does not mean untyped. The value at a supplied key is still checked, and
			// the message names `number` rather than `Partial<Point>`.
			name:     "PartialChecksSuppliedValue",
			src:      `val v: Partial<Point> = {x: "no"}`,
			wantErrs: []string{`cannot constrain "no" <: number`},
		},
		{
			name: "PickAcceptsKeptKey",
			src:  `val v: Pick<Point, "x"> = {x: 1}`,
		},
		{
			// The reduced object is exact, so a key the filter dropped is not accepted back.
			name:     "PickRejectsDroppedKey",
			src:      `val v: Pick<Point, "x"> = {x: 1, y: "s"}`,
			wantErrs: []string{"object has extra property: y"},
		},
		{
			name:     "PickChecksValue",
			src:      `val v: Pick<Point, "x"> = {x: "s"}`,
			wantErrs: []string{`cannot constrain "s" <: number`},
		},
		{
			name: "OmitAcceptsRemainingKey",
			src:  `val v: Omit<Point, "y"> = {x: 1}`,
		},
		{
			// The omitted key is gone and the kept one is still required, so supplying only the
			// omitted key fails on both counts.
			name:     "OmitRejectsOmittedKey",
			src:      `val v: Omit<Point, "y"> = {y: "s"}`,
			wantErrs: []string{"object is missing property: x", "object has extra property: y"},
		},
		{
			name: "RecordAcceptsEveryKey",
			src:  `val v: Record<"a" | "b", number> = {a: 1, b: 2}`,
		},
		{
			// `Record` emits required fields, so every key in the union must be supplied.
			name:     "RecordRequiresEveryKey",
			src:      `val v: Record<"a" | "b", number> = {a: 1}`,
			wantErrs: []string{"object is missing property: b"},
		},
		{
			name: "RecordOfRecordAcceptsNestedObject",
			src:  `val v: Record<"x", Record<"y", number>> = {x: {y: 1}}`,
		},
		{
			// `Required` cleared the markers `Partial` added, so the object must supply both keys.
			name:     "RequiredRestoresEveryKey",
			src:      `val v: Required<Partial<Point>> = {x: 1}`,
			wantErrs: []string{"object is missing property: y"},
		},
		{
			name: "ReadonlyAcceptsWholeObject",
			src:  `val v: Readonly<Point> = {x: 1, y: "s"}`,
		},
		{
			name: "ExcludeAcceptsSurvivingMember",
			src:  `val v: Exclude<"a" | "b", "a"> = "b"`,
		},
		{
			// The excluded member is gone from the union, and the message names the one member
			// left rather than the annotation.
			name:     "ExcludeRejectsRemovedMember",
			src:      `val v: Exclude<"a" | "b", "a"> = "a"`,
			wantErrs: []string{`cannot constrain "a" <: "b"`},
		},
		{
			name: "ExtractAcceptsKeptMember",
			src:  `val v: Extract<"a" | "b", "a"> = "a"`,
		},
		{
			name: "AwaitedAcceptsUnwrappedPayload",
			src:  `val v: Awaited<Promise<Promise<number>>> = 5`,
		},
		{
			name:     "AwaitedRejectsOtherType",
			src:      `val v: Awaited<Promise<Promise<number>>> = "s"`,
			wantErrs: []string{`cannot constrain "s" <: number`},
		},
		{
			name: "UppercaseAcceptsMappedLiteral",
			src:  `val v: Uppercase<"ab"> = "AB"`,
		},
		{
			name:     "UppercaseRejectsUnmappedLiteral",
			src:      `val v: Uppercase<"ab"> = "ab"`,
			wantErrs: []string{`cannot constrain "ab" <: "AB"`},
		},
		{
			name: "EventNameAcceptsHandlerName",
			src:  `val v: EventName<"click"> = "onClick"`,
		},
		{
			// The intrinsic capitalized the interpolation, so the lowercase spelling is rejected.
			name:     "EventNameRejectsUncapitalizedName",
			src:      `val v: EventName<"click"> = "onclick"`,
			wantErrs: []string{`cannot constrain "onclick" <: "onClick"`},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, errs := inferSource(t, utilityTypeDecls+tt.src)
			messages := make([]string, len(errs))
			for i, err := range errs {
				messages[i] = err.Message()
			}
			if len(tt.wantErrs) == 0 {
				require.Empty(t, messages)
				return
			}
			require.Equal(t, tt.wantErrs, messages)
		})
	}
}

// A utility applied to an unfilled type parameter has no ground operand to reduce, so it stays the
// alias reference the source wrote and renders that way in the enclosing signature. This is what
// lets a utility appear in a generic function's signature at all. The reduction waits for the call
// site that supplies the argument.
func TestUtilityTypeStaysSymbolicOverTypeParameter(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want string
	}{
		{
			name: "Partial",
			src:  `fn f<T>(p: Partial<T>) -> Partial<T> { return p }`,
			want: "fn <T>(p: Partial<T>) -> Partial<T>",
		},
		{
			name: "Required",
			src:  `fn f<T>(p: Required<T>) -> Required<T> { return p }`,
			want: "fn <T>(p: Required<T>) -> Required<T>",
		},
		{
			name: "Pick",
			src:  `fn f<T, Ks>(p: Pick<T, Ks>) -> Pick<T, Ks> { return p }`,
			want: "fn <T, Ks>(p: Pick<T, Ks>) -> Pick<T, Ks>",
		},
		{
			name: "Exclude",
			src:  `fn f<U, V>(p: Exclude<U, V>) -> Exclude<U, V> { return p }`,
			want: "fn <U, V>(p: Exclude<U, V>) -> Exclude<U, V>",
		},
		{
			name: "Record",
			src:  `fn f<V>(p: Record<"a", V>) -> Record<"a", V> { return p }`,
			want: `fn <V>(p: Record<"a", V>) -> Record<"a", V>`,
		},
		{
			name: "Awaited",
			src:  `fn f<T>(p: Awaited<T>) -> Awaited<T> { return p }`,
			want: "fn <T>(p: Awaited<T>) -> Awaited<T>",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			values, _, errs := inferSource(t, utilityTypeDecls+tt.src)
			require.Empty(t, errs)
			require.Equal(t, tt.want, values["f"])
		})
	}
}

// TypeScript writes `ReturnType<F>` as `F extends (...args: any) => infer R ? R : never`, one
// definition that reads the return off a function of any arity. Escalier has no equivalent, so
// `ReturnType<F>` above matches a nullary function and rejects every other arity at the bound.
//
// The blocker is the accept-set rule, which constrain uses to decide function subtyping. A
// function type's accept-set is the range of argument counts it tolerates, and `sub <: super`
// holds only when accept(sub) contains accept(super). Both sides here are exact, so their
// accept-sets are single points and containment forces equal arity. Widening the pattern does not
// help. A rest parameter or the inexact `fn (...)` marker lifts the pattern's upper bound to
// infinity, which a fixed-arity argument then fails to contain.
//
// So the arity-agnostic definition needs a decision about how a conditional's `Check <: Extends`
// probe treats arity, not just a rest parameter in the annotation. M9 PR14 makes that decision and
// replaces these cases.
func TestUtilityTypeReturnTypeIsAritySpecific(t *testing.T) {
	runUtilityReductions(t, []utilityReduction{
		{
			// The arity the pattern was written for.
			name:         "NullaryMatches",
			src:          `type Result = ReturnType<fn () -> string>`,
			wantExpanded: "string",
		},
		{
			// A pattern written for the argument's arity does match, so the `infer` capture itself
			// is not what is missing. The parameter position is `never` because parameters are
			// contravariant, so the bottom type is the one every argument type accepts.
			name: "PatternWrittenForTheArityMatches",
			src: `
				type ReturnType1<F> = if F : fn (a: never) -> infer R { R } else { never }
				type Result = ReturnType1<fn (x: number) -> string>
			`,
			wantExpanded: "string",
		},
	})

	// A one-parameter argument is where the restriction bites. Its accept-set is [1, 1] against the
	// bound's [0, 0], so the bound rejects it and the diagnostic names the two arities. TypeScript
	// reduces the same reference to `string`.
	_, _, errs := inferSource(t, utilityTypeDecls+`type Result = ReturnType<fn (x: number) -> string>`)
	require.Len(t, errs, 1)
	require.Equal(t, "cannot constrain function of arity 1 <: function of arity 0", errs[0].Message())
}

// A bound on an alias's type parameter is checked at the reference, so `ReturnType<F>` rejects an
// argument that is not a nullary function instead of reducing it through the Else branch. The last
// two cases pin the same check on the simplest alias that can carry a bound, so a failure there is
// about the check rather than about anything the operator track added.
//
// The bound is written `fn () -> unknown` because a function type's return is covariant, so only
// the top of the lattice admits every nullary function. A bound written `fn () -> never` would
// instead say `F` returns `never` and would reject `fn () -> string`.
func TestUtilityTypeAliasParameterConstraint(t *testing.T) {
	tests := []struct {
		name    string
		src     string
		wantErr string // "" ⇒ expect no error
	}{
		{
			// The bound admits every nullary function, whatever it returns.
			name: "FunctionArgumentPasses",
			src:  `type Result = ReturnType<fn () -> string>`,
		},
		{
			// A structural return is still a return, so the bound is about the shape of `F` alone.
			name: "ObjectReturningFunctionPasses",
			src:  `type Result = ReturnType<fn () -> {a: number}>`,
		},
		{
			// A non-function is rejected at the reference. The message names `function` rather than
			// the written bound, since that is how the mismatch renders once the argument is not a
			// function at all.
			name:    "NonFunctionArgumentRejected",
			src:     `type Result = ReturnType<number>`,
			wantErr: "cannot constrain number <: function",
		},
		{
			// The bound admits every type at the simplest alias that can carry one.
			name: "PlainAliasAcceptsBoundedArgument",
			src:  `type Box<T: string> = {v: T}` + "\n" + `type Result = Box<"a">`,
		},
		{
			name:    "PlainAliasRejectsUnboundedArgument",
			src:     `type Box<T: string> = {v: T}` + "\n" + `type Result = Box<number>`,
			wantErr: "cannot constrain number <: string",
		},
		{
			// An unbounded parameter still accepts everything, so the check reads each parameter's
			// own bound rather than applying one uniformly.
			name: "UnboundedParameterAcceptsAnything",
			src:  `type Id<T> = T` + "\n" + `type Result = Id<number>`,
		},
		{
			// A bound naming an earlier sibling is checked against the argument that filled it, so
			// `B` is compared against `string` rather than against `A`'s var.
			name: "SiblingBoundAccepts",
			src:  `type P<A: string, B: A> = [A, B]` + "\n" + `type Result = P<string, "a">`,
		},
		{
			name:    "SiblingBoundRejects",
			src:     `type P<A: string, B: A> = [A, B]` + "\n" + `type Result = P<string, number>`,
			wantErr: "cannot constrain number <: string",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, errs := inferSource(t, utilityTypeDecls+tt.src)
			if tt.wantErr == "" {
				require.Empty(t, errs)
				return
			}
			require.Len(t, errs, 1)
			require.Equal(t, tt.wantErr, errs[0].Message())
		})
	}
}

// DISABLED until M9 PR16, which gives `null` and `undefined` a surface. `soltype.NullType` and
// `soltype.UndefinedType` already exist as atoms and `undefined` is already inferred, since reading
// an optional property yields `number | undefined`. What no annotation reaches is either atom, so
// `resolveLitTypeAnn` reports `Unsupported: LitTypeAnn` for both spellings and the `null |
// undefined` operand `NonNullable<T>` tests against cannot be written. `constrain` also carries a
// reflexive arm for `UndefinedType` and none for `NullType`. Re-enable by removing the comment
// wrapper once both exist, and add
//
//	type NonNullable<T> = if T : null | undefined { never } else { T }
//
// to utilityTypeDecls, which is where runUtilityReductions reads each utility's definition from.
func TestUtilityTypeNonNullable(t *testing.T) {
	/*
		runUtilityReductions(t, []utilityReduction{
			{
				// The naked type parameter distributes, so each member is decided alone and the two
				// absence markers drop out.
				name:         "StripsBothMarkers",
				src:          `type Result = NonNullable<string | null | undefined>`,
				wantExpanded: "string",
			},
			{
				name:         "LeavesOtherMembers",
				src:          `type Result = NonNullable<string | number>`,
				wantExpanded: "string | number",
			},
			{
				name:         "EveryMemberStripped",
				src:          `type Result = NonNullable<null | undefined>`,
				wantExpanded: "never",
			},
		})
	*/
}

// DISABLED until a rest parameter can be written in a function type annotation and an `infer`
// clause in that position captures a tuple. Two separate pieces are missing.
//
// The annotation surface is the first. `resolveFuncTypeAnn` reports `Unsupported: rest parameter in
// function type annotation` and recovers the parameter to a positional one, because `acceptSet` and
// `hasRest` assume a rest parameter is last and the parser does not enforce that.
//
// The capture rule is the second, and lifting the rejection does not supply it. `Parameters<F>`
// binds one `infer` name to the whole parameter list, so matching `fn (x: number, y: string) ->
// boolean` has to gather both parameters into `[number, string]`. constrain's function arm walks
// only the positions the two sides share and never collects the surplus ones, so the capture would
// come out as a variable bounded by `number` rather than as a tuple. TypeScript reaches the tuple
// through a variadic-tuple inference rule that has no counterpart here.
//
// The `Array<T>` that M7.5 lands is a different concern. It supplies the element type a typed rest
// parameter checks its trailing *arguments* against at a call site, which is the deferral recorded
// on `FuncParam.Rest` in internal/soltype/type.go. A pattern match over a written function type
// reads no element type, so it needs no `Array`.
//
// Both pieces also need the arity decision TestUtilityTypeReturnTypeIsAritySpecific describes,
// since a rest parameter in the pattern widens its accept-set the same way the inexact marker does.
// M9 PR14 covers all three. `ConstructorParameters<C>` waits on PR14 plus the `new (…)` member M9
// PR15 adds, so it stays disabled until both land.
//
// Re-enable by removing the comment wrapper and adding both definitions to utilityTypeDecls:
//
//	type Parameters<F> = if F : fn (...args: infer P) -> unknown { P } else { never }
//	type ConstructorParameters<C> = if C : {new (...args: infer P) -> unknown} { P } else { never }
func TestUtilityTypeParameters(t *testing.T) {
	/*
		runUtilityReductions(t, []utilityReduction{
			{
				// The capture is the parameter list as a tuple, in source order.
				name:         "ParameterList",
				src:          `type Result = Parameters<fn (x: number, y: string) -> boolean>`,
				wantExpanded: "[number, string]",
			},
			{
				name:         "NullaryParameterList",
				src:          `type Result = Parameters<fn () -> boolean>`,
				wantExpanded: "[]",
			},
			{
				name:         "NonFunction",
				src:          `type Result = Parameters<number>`,
				wantExpanded: "never",
			},
			{
				name:         "ConstructorParameterList",
				src:          `type Result = ConstructorParameters<{new (x: number) -> {a: number}}>`,
				wantExpanded: "[number]",
			},
		})
	*/
}

// DISABLED until M9 PR15, which adds a `new (…)` member to object type annotations.
// `InstanceType<C>` reads the return type off a constructor signature, and `objTypeAnnElemInner`
// has no arm for `new`, so `{new (…) -> infer R}` fails to parse. The representation is not what is
// missing. The printer already renders the form, as internal/solver/infer_class_test.go shows a
// class's static side printing `{new (x: number, y: number) -> Vec, …}`.
//
// Re-enable by removing the comment wrapper and adding
//
//	type InstanceType<C> = if C : {new (a: never) -> infer R} { R } else { never }
//
// to utilityTypeDecls. The pattern is written for a one-parameter constructor, since it inherits
// the arity restriction TestUtilityTypeReturnTypeIsAritySpecific describes.
func TestUtilityTypeInstanceType(t *testing.T) {
	/*
		runUtilityReductions(t, []utilityReduction{
			{
				name:         "ConstructorReturn",
				src:          `type Result = InstanceType<{new (x: number) -> {a: number}}>`,
				wantExpanded: "{a: number}",
			},
			{
				name:         "NonConstructor",
				src:          `type Result = InstanceType<number>`,
				wantExpanded: "never",
			},
		})
	*/
}
