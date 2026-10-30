package solver

import (
	"testing"

	"github.com/escalier-lang/escalier/internal/dep_graph"
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
			wantErrs: []string{"6:14-6:19: cannot constrain typeof other <: typeof sym"},
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
			wantErrs: []string{"5:20-5:26: cannot assign to '[sym]' because it is a readonly property"},
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

// TestUniqueSymbolNamedByAValueOfItsType covers how a symbol a `readonly` property declares
// renders. A `val` whose annotation is the declaring type names the symbol after its own
// path, so `declare val i: I` gives `typeof i.key` and a key off it `[i.key]`. A
// `static readonly` class field does the same through its class, so the field in
// `declare class C { static readonly i: I }` gives `typeof C.i.key`. A value at the module
// root wins over one in a namespace, and otherwise the first in source order wins. Without
// such a value the symbol renders by its id.
func TestUniqueSymbolNamedByAValueOfItsType(t *testing.T) {
	const iface = "declare interface I { readonly key: unique symbol }\n"
	tests := []struct {
		name     string
		srcs     map[string]string
		want     map[string]string
		wantErrs []string
	}{
		{
			name: "ObjectTypeAlias",
			srcs: map[string]string{"input.esc": "type T = {readonly tag: unique symbol}\ndeclare val t: T\nval k = t.tag\nval o = {[t.tag]: 1}"},
			want: map[string]string{"k": "typeof t.tag", "o": "{[t.tag]: 1}"},
		},
		{
			name: "InlineObjectType",
			srcs: map[string]string{"input.esc": "declare val o: {readonly key: unique symbol}\nval k = o.key\nval p = {[o.key]: 1}"},
			want: map[string]string{"k": "typeof o.key", "p": "{[o.key]: 1}"},
		},
		{
			name: "NestedReadonlyProperty",
			srcs: map[string]string{"input.esc": "declare val n: {readonly a: {readonly b: unique symbol}}\nval k = n.a.b"},
			want: map[string]string{"k": "typeof n.a.b"},
		},
		{
			// A property that is not `readonly` cannot declare a symbol, so nothing below it
			// is reached through a fixed path.
			name: "NestedWritableProperty",
			srcs: map[string]string{"input.esc": "declare val n: {a: {readonly b: unique symbol}}\nval k = n.a.b"},
			want: map[string]string{"k": "unique symbol#0"},
		},
		{
			// Reading an optional property can produce `undefined`, so no path names the
			// symbol alone.
			name: "OptionalProperty",
			srcs: map[string]string{"input.esc": "declare val o: {readonly key?: unique symbol}\nval k = o.key"},
			want: map[string]string{"k": "unique symbol#0 | undefined"},
		},
		{
			name: "InterfaceThatExtends",
			srcs: map[string]string{"input.esc": `declare interface Base { x: number }
declare interface I extends Base { readonly key: unique symbol }
declare val i: I
val k = i.key`},
			want: map[string]string{"k": "typeof i.key"},
		},
		{
			// `keys.i` comes first in source order, and the root `r` still wins.
			name: "RootValWinsOverNamespacedVal",
			srcs: map[string]string{
				"input.esc":  iface + "val k = r.key",
				"keys/a.esc": "export declare val i: I",
				"z.esc":      "declare val r: I",
			},
			want: map[string]string{"k": "typeof r.key"},
		},
		{
			name: "QuotedKey",
			srcs: map[string]string{"input.esc": "declare val o: {readonly \"a-b\": unique symbol}\nval k = o[\"a-b\"]"},
			want: map[string]string{"k": "unique symbol#0"},
		},
		{
			name: "FirstValWins",
			srcs: map[string]string{"input.esc": iface + "declare val i: I\ndeclare val j: I\nval k = j.key"},
			want: map[string]string{"k": "typeof i.key"},
		},
		{
			// The function reads the key through its parameter and may be inferred before
			// `i`, which it does not refer to. The name is still the one `i` gives.
			name: "ValDeclaredAfterTheUse",
			srcs: map[string]string{"input.esc": iface + `fn wrap(x: &I) { return {[x.key]: 1} }
fn read(x: &I) { return x.key }
declare val i: I`},
			want: map[string]string{"wrap": "fn (x: &I) -> {[i.key]: 1}", "read": "fn (x: &I) -> typeof i.key"},
		},
		{
			// `make` builds its key through a parameter, and `o[i.key]` reads it through `i`.
			// Both spell one member, so the read finds it.
			name: "KeysBuiltTwoWaysMatch",
			srcs: map[string]string{"input.esc": iface + `fn make(x: &I) { return {[x.key]: 1} }
declare val i: I
val o = make(i)
val r = o[i.key]
val p = {[i.key]: "x"}
fn readP(x: &I) { return p[x.key] }`},
			want: map[string]string{"o": "{[i.key]: 1}", "r": "1", "readP": `fn (x: &I) -> "x"`},
		},
		{
			name: "NamespacedVal",
			srcs: map[string]string{
				"input.esc":  iface + "val k = keys.i.key\nval o = {[keys.i.key]: 1}",
				"keys/i.esc": "export declare val i: I",
			},
			want: map[string]string{"k": "typeof keys.i.key", "o": "{[keys.i.key]: 1}"},
		},
		{
			name: "NoValOfTheType",
			srcs: map[string]string{"input.esc": iface + "fn read(x: &I) { return {[x.key]: 1} }"},
			want: map[string]string{"read": "fn (x: &I) -> {[unique symbol#0]: 1}"},
		},
		{
			name: "StaticReadonlyField",
			srcs: map[string]string{"input.esc": iface + "declare class C { static readonly i: I }\nval k = C.i.key\nval o = {[C.i.key]: 1}"},
			want: map[string]string{"k": "typeof C.i.key", "o": "{[C.i.key]: 1}"},
		},
		{
			name: "NamespacedStaticReadonlyField",
			srcs: map[string]string{
				"input.esc":  iface + "val k = keys.C.i.key",
				"keys/c.esc": "export declare class C { static readonly i: I }",
			},
			want: map[string]string{"k": "typeof keys.C.i.key"},
		},
		{
			// The field comes first in source order, so it names the symbol.
			name: "StaticReadonlyFieldBeforeVal",
			srcs: map[string]string{"input.esc": iface + "declare class C { static readonly i: I }\ndeclare val j: I\nval k = j.key"},
			want: map[string]string{"k": "typeof C.i.key"},
		},
		{
			// A writable field may be reassigned, so no path through it holds one symbol.
			name: "WritableStaticField",
			srcs: map[string]string{"input.esc": iface + "declare class C { static w: I }\nval k = C.w.key"},
			want: map[string]string{"k": "unique symbol#0"},
		},
		{
			// A `var` may be reassigned, so no path through it holds one symbol.
			name: "VarOfTheType",
			srcs: map[string]string{"input.esc": iface + "declare var v: I\nval k = v.key"},
			want: map[string]string{"k": "unique symbol#0"},
		},
		{
			name: "MissingPropertyNamesTheSymbol",
			srcs: map[string]string{"input.esc": `declare interface I { readonly key: unique symbol, readonly other: unique symbol }
declare val i: I
val o = {[i.key]: 1}
val r = o[i.other]`},
			wantErrs: []string{"4:11-4:18: object is missing property: [i.other]"},
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
// names one symbol, so it is allowed only on a declaration that cannot be reassigned: a
// `val` declaration, a `static readonly` class field, and a `readonly` property of an
// interface or object type. Anywhere else the annotation is reported and the value is typed
// `symbol`.
func TestUniqueSymbolPosition(t *testing.T) {
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
			wantErrs: []string{"1:33-1:46: A class field whose type is `unique symbol` must be `static` and `readonly`."},
		},
		{
			name:     "StaticFieldThatIsNotReadonly",
			src:      "declare class D { static key: unique symbol }\nval k = D.key",
			want:     map[string]string{"k": "symbol"},
			wantErrs: []string{"1:31-1:44: A class field whose type is `unique symbol` must be `static` and `readonly`."},
		},
		{
			name:     "Parameter",
			src:      "fn f(s: unique symbol) { return s }",
			want:     map[string]string{"f": "fn (s: symbol) -> symbol"},
			wantErrs: []string{"1:9-1:22: A `unique symbol` type is only allowed on a `val` declaration, a `static readonly` class field, or a `readonly` property."},
		},
		{
			name:     "ReturnType",
			src:      "declare fn f() -> unique symbol",
			want:     map[string]string{"f": "fn () -> symbol"},
			wantErrs: []string{"1:19-1:32: A `unique symbol` type is only allowed on a `val` declaration, a `static readonly` class field, or a `readonly` property."},
		},
		{
			name: "ReadonlyPropertyOfAnObjectType",
			src:  "declare val o: {readonly key: unique symbol}\nval k = o.key",
			want: map[string]string{"k": "typeof o.key"},
		},
		{
			name: "ReadonlyPropertyOfAnInterface",
			src:  "declare interface I { readonly key: unique symbol }\ndeclare val i: I\nval k = i.key\nval o = {[i.key]: 1}",
			want: map[string]string{"k": "typeof i.key", "o": "{[i.key]: 1}"},
		},
		{
			name:     "PropertyThatIsNotReadonly",
			src:      "declare val o: {key: unique symbol}",
			want:     map[string]string{"o": "{key: symbol}"},
			wantErrs: []string{"1:22-1:35: A property whose type is `unique symbol` must be `readonly`."},
		},
		{
			name:     "TypeAlias",
			src:      "type S = unique symbol",
			wantErrs: []string{"1:10-1:23: A `unique symbol` type is only allowed on a `val` declaration, a `static readonly` class field, or a `readonly` property."},
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

// TestModuleSymbols covers the symbols a module run reports for the `.d.ts` emitter. Each
// is named by the path a program reads it through. A symbol a declaration inside a block
// mints is local to that block and is left out.
func TestModuleSymbols(t *testing.T) {
	tests := []struct {
		name       string
		src        string
		wantMinted []string
		wantOwners map[string]string
	}{
		{
			name:       "RootVal",
			src:        "declare val sym: unique symbol",
			wantMinted: []string{"sym"},
			wantOwners: map[string]string{},
		},
		{
			name:       "InterfaceNamedByAVal",
			src:        "declare interface I { readonly key: unique symbol }\ndeclare val i: I",
			wantMinted: []string{"i.key"},
			wantOwners: map[string]string{"I": "i"},
		},
		{
			name:       "StaticReadonlyField",
			src:        "declare class C { static readonly key: unique symbol }",
			wantMinted: []string{"C.key"},
			wantOwners: map[string]string{},
		},
		{
			name:       "DoBlock",
			src:        "val x = do {\n\tdeclare val s: unique symbol\n\ts\n}",
			wantMinted: nil,
			wantOwners: map[string]string{},
		},
		{
			name:       "FunctionBody",
			src:        "fn f() {\n\tdeclare val s: unique symbol\n\treturn s\n}",
			wantMinted: nil,
			wantOwners: map[string]string{},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			module := parseModuleFiles(t, map[string]string{"input.esc": tt.src})
			c := newTestChecker()
			symbols := c.inferDepGraph(c.preludeScope().Child(), 0, module, dep_graph.BuildDepGraph(module))
			require.Empty(t, c.errs)
			var minted []string
			for _, sym := range symbols.Minted {
				minted = append(minted, sym.Name)
			}
			require.Equal(t, tt.wantMinted, minted)
			require.Equal(t, tt.wantOwners, symbols.Owners)
		})
	}
}
