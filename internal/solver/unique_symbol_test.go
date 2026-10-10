package solver

import (
	"testing"

	"github.com/escalier-lang/escalier/internal/soltype"
	"github.com/stretchr/testify/require"
)

// Each written `unique symbol` names one particular symbol. Two of them are the same type
// only when they came from the same declaration, and every one is a subtype of `symbol`.
func TestUniqueSymbol(t *testing.T) {
	const decl = `
		declare class C {
			static readonly a: unique symbol,
			static readonly b: unique symbol,
		}
	`
	t.Run("EveryOneIsASymbol", func(t *testing.T) {
		values, _, errs := inferSource(t, decl+`
			declare fn take(s: symbol) -> number
			fn use() { return take(C.a) }
		`)
		require.Empty(t, errorMessagesOf(errs))
		require.Equal(t, "fn () -> number", values["use"])
	})

	t.Run("OneReachesItself", func(t *testing.T) {
		values, _, errs := inferSource(t, decl+`
			declare fn takeA(s: typeof C.a) -> number
			fn use() { return takeA(C.a) }
		`)
		require.Empty(t, errorMessagesOf(errs))
		require.Equal(t, "fn () -> number", values["use"])
	})

	// Two declarations are two values however they read, so nothing weaker than identity
	// relates them. The report names each under the id that tells them apart.
	t.Run("TwoDistinctOnesAreUnrelated", func(t *testing.T) {
		_, _, errs := inferSource(t, decl+`
			declare fn takeA(s: typeof C.a) -> number
			fn use() { return takeA(C.b) }
		`)
		require.Equal(t,
			[]string{"cannot constrain typeof C.b <: typeof C.a"},
			errorMessagesOf(errs))
	})

	// `symbol` is the whole primitive and a unique symbol is one of its values, so the
	// subtyping runs one way only.
	t.Run("ASymbolIsNotAParticularOne", func(t *testing.T) {
		_, _, errs := inferSource(t, decl+`
			declare fn takeA(s: typeof C.a) -> number
			fn use(s: symbol) { return takeA(s) }
		`)
		require.Equal(t,
			[]string{"cannot constrain symbol <: typeof C.a"},
			errorMessagesOf(errs))
	})
}

// The kind carries an id rather than a type, and the pieces that key on a type read that
// id: equality holds on a match and ordering separates a mismatch. Its leaf visit lives in
// the soltype package, where the identity visitor does.
func TestUniqueSymbolIsALeafKeyedOnItsID(t *testing.T) {
	first := &soltype.UniqueSymbolType{ID: 0}
	same := &soltype.UniqueSymbolType{ID: 0}
	other := &soltype.UniqueSymbolType{ID: 1}

	require.True(t, equalType(first, same))
	require.False(t, equalType(first, other))
	require.Zero(t, compareType(first, same))

	ab, ba := compareType(first, other), compareType(other, first)
	require.NotZero(t, ab)
	require.Equal(t, -ab, ba)

	// Its rank is its own, so the tie-breaker never asserts another kind to it.
	require.NotEqual(t, typeKindOrder(first), typeKindOrder(&soltype.PrimType{Prim: soltype.SymPrim}))
}

// `std:prelude` declares the well-known symbols as `static readonly` fields of `Symbol`, so a
// member read off the class is the particular symbol the field declares rather than the
// whole `symbol` primitive.
func TestAWellKnownSymbolReachesItsOwnType(t *testing.T) {
	values, _, errs := inferSource(t, `
		declare class Symbol {
			static readonly iterator: unique symbol,
			static readonly asyncIterator: unique symbol,
		}
		val it = Symbol.iterator
		val asyncIt = Symbol.asyncIterator
	`)
	require.Empty(t, errorMessagesOf(errs))
	require.Equal(t, "typeof Symbol.iterator", values["it"])
	require.Equal(t, "typeof Symbol.asyncIterator", values["asyncIt"])
}

// A unique symbol names one particular symbol the way a literal names one particular
// value, so the two follow the same rules for a mutable binding and for match coverage.
func TestUniqueSymbolBehavesLikeALiteralOfItsPrimitive(t *testing.T) {
	const decl = `
		declare class C {
			static readonly a: unique symbol,
			static readonly b: unique symbol,
		}
	`

	// A `var` holding one widens to `symbol` so it can later hold another, the way
	// `var n = 5` widens to `number`.
	t.Run("AVarHoldingOneWidensToSymbol", func(t *testing.T) {
		values, _, errs := inferSource(t, decl+`
			fn f() {
				var s = C.a
				s = C.b
				return s
			}
		`)
		require.Empty(t, errorMessagesOf(errs))
		require.Equal(t, "fn () -> symbol", values["f"])
	})

	// A program mints a fresh symbol whenever it likes, so no set of `unique symbol` arms
	// enumerates `symbol` and a match over it needs a catch-all.
	t.Run("AMatchOverSymbolNeedsACatchAll", func(t *testing.T) {
		_, _, errs := inferSource(t, decl+`
			fn f(s: symbol) {
				return match s {
					x: typeof C.a => 1
				}
			}
		`)
		require.Equal(t,
			[]string{"match is not exhaustive; `symbol` admits values no pattern names, so add a catch-all branch"},
			errorMessagesOf(errs))
	})
}

// TestUniqueSymbolRendersByItsDeclaration covers how a unique symbol and a key off it
// render. A symbol renders as `typeof` the path of the declaration that minted it, and a
// key off it as that path in brackets. The path belongs to the declaration, so a read
// through another binding renders the same. A symbol no declaration names renders by
// its id.
func TestUniqueSymbolRendersByItsDeclaration(t *testing.T) {
	tests := []struct {
		name     string
		srcs     map[string]string
		want     map[string]string
		wantErrs []string
	}{
		{
			name: "DeclaredVal",
			srcs: map[string]string{"input.esc": "declare val sym: unique symbol\nval o = {[sym]: 1}"},
			want: map[string]string{"sym": "typeof sym", "o": "{[sym]: 1}"},
		},
		{
			name: "ReadThroughAnotherBinding",
			srcs: map[string]string{"input.esc": "declare val sym: unique symbol\nval s2 = sym\nval o = {[s2]: 1}\nval r = o[sym]"},
			want: map[string]string{"s2": "typeof sym", "o": "{[sym]: 1}", "r": "1"},
		},
		{
			name: "TwoDistinctSymbols",
			srcs: map[string]string{"input.esc": `declare val sym: unique symbol
declare val other: unique symbol
declare fn take(s: typeof sym) -> number
val o = {[sym]: 1, [other]: "x"}
val r = o[other]
val n = take(other)`},
			want:     map[string]string{"o": `{[sym]: 1, [other]: "x"}`, "r": `"x"`},
			wantErrs: []string{"6:9-6:20: cannot constrain typeof other <: typeof sym"},
		},
		{
			name:     "MissingPropertyNamesTheSymbol",
			srcs:     map[string]string{"input.esc": "declare val sym: unique symbol\ndeclare val other: unique symbol\nval o = {[sym]: 1}\nval r = o[other]"},
			wantErrs: []string{"4:11-4:16: object is missing property: [other]"},
		},
		{
			name: "WriteToAReadonlyKey",
			srcs: map[string]string{"input.esc": `declare val sym: unique symbol
declare class C {
    readonly [sym]: number,
}
fn go(c: &mut C) { c[sym] = 5 }`},
			wantErrs: []string{"5:20-5:30: readonly field [sym] cannot satisfy a writable field requirement"},
		},
		{
			name: "WellKnownSymbol",
			srcs: map[string]string{"input.esc": "val o = {[Symbol.iterator]: 1}"},
			want: map[string]string{"o": "{[Symbol.iterator]: 1}"},
		},
		{
			name: "ClassStatic",
			srcs: map[string]string{"input.esc": `declare class C {
    static readonly key: unique symbol,
}
val k = C.key
val o = {[C.key]: 1}`},
			want: map[string]string{"k": "typeof C.key", "o": "{[C.key]: 1}"},
		},
		{
			name: "Namespace",
			srcs: map[string]string{
				"keys/sym.esc": "export declare val sym: unique symbol",
				"input.esc":    "val k = keys.sym\nval o = {[keys.sym]: 1}",
			},
			want: map[string]string{"k": "typeof keys.sym", "o": "{[keys.sym]: 1}"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			values, _, errs := inferSources(t, tt.srcs)
			require.Equal(t, tt.wantErrs, messagesWithSpan(t, errs))
			for name, want := range tt.want {
				require.Equal(t, want, values[name], name)
			}
		})
	}
}

// TestUniqueSymbolPosition covers where a `unique symbol` annotation may be written. It
// names one symbol, so it is allowed only where exactly one value carries it: a `val`
// declaration and a `static readonly` class field. Anywhere else the annotation is
// reported and the value is typed `symbol`.
func TestUniqueSymbolPosition(t *testing.T) {
	const elsewhere = "A `unique symbol` type is only allowed on a `val` declaration or a `static readonly` class field."
	const field = "A class field whose type is `unique symbol` must be `static` and `readonly`."
	tests := []struct {
		name     string
		src      string
		want     map[string]string
		wantErrs []string
	}{
		{
			name: "DeclaredVal",
			src:  "declare val sym: unique symbol",
			want: map[string]string{"sym": "typeof sym"},
		},
		{
			name: "StaticReadonlyField",
			src:  "declare class C { static readonly key: unique symbol }\nval k = C.key",
			want: map[string]string{"k": "typeof C.key"},
		},
		{
			name:     "Var",
			src:      "declare var sym: unique symbol",
			want:     map[string]string{"sym": "symbol"},
			wantErrs: []string{"1:18-1:31: A binding whose type is `unique symbol` must be declared with `val`."},
		},
		{
			// Each instance can hold a different symbol, so no one symbol is the field's type.
			name:     "InstanceField",
			src:      "declare class D { readonly key: unique symbol }\ndeclare val d: &D\nval k = d.key",
			want:     map[string]string{"k": "symbol"},
			wantErrs: []string{"1:33-1:46: " + field},
		},
		{
			name:     "StaticFieldThatIsNotReadonly",
			src:      "declare class D { static key: unique symbol }\nval k = D.key",
			want:     map[string]string{"k": "symbol"},
			wantErrs: []string{"1:31-1:44: " + field},
		},
		{
			name:     "Parameter",
			src:      "fn f(s: unique symbol) { return s }",
			want:     map[string]string{"f": "fn (s: symbol) -> symbol"},
			wantErrs: []string{"1:9-1:22: " + elsewhere},
		},
		{
			name:     "ReturnType",
			src:      "declare fn f() -> unique symbol",
			want:     map[string]string{"f": "fn () -> symbol"},
			wantErrs: []string{"1:19-1:32: " + elsewhere},
		},
		{
			name:     "MemberOfAnObjectType",
			src:      "declare val o: {readonly key: unique symbol}",
			want:     map[string]string{"o": "{readonly key: symbol}"},
			wantErrs: []string{"1:31-1:44: " + elsewhere},
		},
		{
			name:     "TypeAlias",
			src:      "type S = unique symbol",
			wantErrs: []string{"1:10-1:23: " + elsewhere},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			values, _, errs := inferSource(t, tt.src)
			require.Equal(t, tt.wantErrs, messagesWithSpan(t, errs))
			for name, want := range tt.want {
				require.Equal(t, want, values[name], name)
			}
		})
	}
}
