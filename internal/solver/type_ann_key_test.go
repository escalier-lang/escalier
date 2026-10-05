package solver

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// annKeyCase is one row of a table over computed keys in object type annotations. `values`
// and `types` map a binding's name to its rendered type, and `wantErrs` lists every
// diagnostic with its span.
type annKeyCase struct {
	name     string
	src      string
	values   map[string]string
	types    map[string]string
	wantErrs []string
}

func runAnnKeyCases(t *testing.T, tests []annKeyCase) {
	t.Helper()
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			values, types, errs := inferSource(t, tt.src)
			require.Equal(t, tt.wantErrs, messagesWithSpan(t, errs))
			for name, want := range tt.values {
				require.Equal(t, want, values[name], name)
			}
			for name, want := range tt.types {
				require.Equal(t, want, types[name], name)
			}
		})
	}
}

// TestAnnKeyNamesOneMember covers a computed key in an object type annotation whose type is
// one string literal, one number literal, or one unique symbol. Each names the one member
// that key selects, the same member `o[k]` reads.
func TestAnnKeyNamesOneMember(t *testing.T) {
	runAnnKeyCases(t, []annKeyCase{
		{
			name:   "UniqueSymbol",
			src:    "declare val sym: unique symbol\nfn f(o: &{[sym]: number}) { return o[sym] }",
			values: map[string]string{"f": "fn (o: &{[unique symbol#0]: number}) -> number"},
		},
		{
			name:  "StringLiteral",
			src:   "val k = \"a\"\ntype T = {[k]: number}",
			types: map[string]string{"T": "{a: number}"},
		},
		{
			name:  "NumberLiteral",
			src:   "val k = 1\ntype T = {[k]: number}",
			types: map[string]string{"T": `{"1": number}`},
		},
		{
			name:  "LiteralsWrittenInTheKey",
			src:   "type T = {[\"a\"]: number, [2]: string}",
			types: map[string]string{"T": `{a: number, "2": string}`},
		},
		{
			name:  "WellKnownSymbol",
			src:   "type T = {[Symbol.iterator]: number}",
			types: map[string]string{"T": "{[Symbol.iterator]: number}"},
		},
		{
			// A value holding a well-known symbol keys the same member the symbol's written
			// form does.
			name:  "WellKnownSymbolThroughAVal",
			src:   "val it = Symbol.iterator\ntype T = {[it]: number}",
			types: map[string]string{"T": "{[Symbol.iterator]: number}"},
		},
		{
			name:  "NamespaceMember",
			src:   "namespace N { declare val s: unique symbol }\ntype T = {[N.s]: number}",
			types: map[string]string{"T": "{[unique symbol#0]: number}"},
		},
		{
			name:  "MemberOfAValue",
			src:   "declare val o: {k: \"x\"}\ntype T = {[o.k]: number}",
			types: map[string]string{"T": "{x: number}"},
		},
		{
			// The dep graph orders `k` before the alias that names it.
			name:  "TypeAliasBeforeTheVal",
			src:   "type T = {[k]: number}\nval k = \"a\"",
			types: map[string]string{"T": "{a: number}"},
		},
		{
			name:   "ValAnnotation",
			src:    "val k = \"a\"\nval o: {[k]: number} = {a: 1}",
			values: map[string]string{"o": "{a: number}"},
		},
		{
			name:   "LocalValInAFunctionBody",
			src:    "fn f() {\n  val k = \"a\"\n  val o: {[k]: number} = {a: 1}\n  return o\n}",
			values: map[string]string{"f": "fn () -> {a: number}"},
		},
		{
			name:   "ParamInAFunctionBody",
			src:    "fn f(k: \"a\") {\n  val o: {[k]: number} = {a: 1}\n  return o\n}",
			values: map[string]string{"f": "fn (k: \"a\") -> {a: number}"},
		},
		{
			// The key's declared type is expanded through the alias it names.
			name:  "KeyTypedThroughAnAlias",
			src:   "type K = \"a\"\ndeclare val k: K\ntype T = {[k]: number}",
			types: map[string]string{"T": "{a: number}"},
		},
		{
			name: "KeyTypedThroughATypeofQuery",
			src: "declare val sym: unique symbol\n" +
				"fn f(s: typeof sym) {\n  val o: {[s]: number} = {[sym]: 1}\n  return o\n}",
			values: map[string]string{"f": "fn (s: typeof sym) -> {[unique symbol#0]: number}"},
		},
		{
			name:  "MethodKey",
			src:   "declare val sym: unique symbol\ntype T = {[sym](&self) -> number}",
			types: map[string]string{"T": "{[unique symbol#0]() -> number}"},
		},
		{
			name:  "GetterKey",
			src:   "val k = \"a\"\ntype T = {get [k](&self) -> number}",
			types: map[string]string{"T": "{get a() -> number}"},
		},
		{
			name: "InterfaceMember",
			src: "declare val sym: unique symbol\ninterface I {[sym]: number}\n" +
				"fn f(i: &I) { return i[sym] }",
			values: map[string]string{"f": "fn (i: &I) -> number"},
		},
	})
}

// TestAnnKeyRejectsAKeyNamingNoSingleMember covers a computed key in an object type
// annotation that names no single member. TypeScript rejects such a key in a type literal
// rather than reading it as an index signature, and so does the solver. The member is
// dropped and the rest of the object resolves.
func TestAnnKeyRejectsAKeyNamingNoSingleMember(t *testing.T) {
	runAnnKeyCases(t, []annKeyCase{
		{
			name:  "String",
			src:   "declare val s: string\ntype T = {[s]: number, b: string}",
			types: map[string]string{"T": "{b: string}"},
			wantErrs: []string{
				"2:12-2:13: A computed key in an object type must be a string literal, number literal, or unique symbol, not string",
			},
		},
		{
			name:  "Number",
			src:   "declare val n: number\ntype T = {[n]: number}",
			types: map[string]string{"T": "{}"},
			wantErrs: []string{
				"2:12-2:13: A computed key in an object type must be a string literal, number literal, or unique symbol, not number",
			},
		},
		{
			name:  "Symbol",
			src:   "declare val s: symbol\ntype T = {[s]: number}",
			types: map[string]string{"T": "{}"},
			wantErrs: []string{
				"2:12-2:13: A computed key in an object type must be a string literal, number literal, or unique symbol, not symbol",
			},
		},
		{
			name:  "Union",
			src:   "declare val s: \"a\" | \"b\"\ntype T = {[s]: number}",
			types: map[string]string{"T": "{}"},
			wantErrs: []string{
				"2:12-2:13: A computed key in an object type must be a string literal, number literal, or unique symbol, not \"a\" | \"b\"",
			},
		},
		{
			// A `var` binding widens its literal, so the key may hold any string.
			name:  "VarBinding",
			src:   "var k = \"a\"\ntype T = {[k]: number}",
			types: map[string]string{"T": "{}"},
			wantErrs: []string{
				"2:12-2:13: A computed key in an object type must be a string literal, number literal, or unique symbol, not string",
			},
		},
		{
			name:  "Boolean",
			src:   "type T = {[true]: number}",
			types: map[string]string{"T": "{}"},
			wantErrs: []string{
				"1:12-1:16: A computed key in an object type must be a string literal, number literal, or unique symbol, not true",
			},
		},
		{
			name:     "UnknownIdentifier",
			src:      "type T = {[nope]: number}",
			types:    map[string]string{"T": "{}"},
			wantErrs: []string{"1:12-1:16: Unknown identifier: nope"},
		},
		{
			name:     "UnknownReceiver",
			src:      "type T = {[Other.iterator](&self) -> number}",
			types:    map[string]string{"T": "{}"},
			wantErrs: []string{"1:12-1:17: Unknown identifier: Other"},
		},
		{
			// A call is not a name, so its type is not read.
			name:  "Call",
			src:   "declare fn f() -> string\ntype T = {[f()]: number}",
			types: map[string]string{"T": "{}"},
			wantErrs: []string{
				"2:12-2:15: A computed key in an object type must be a string literal, number literal, or unique symbol",
			},
		},
		{
			name:  "MissingMemberOfAValue",
			src:   "declare val o: {k: \"x\"}\ntype T = {[o.j]: number}",
			types: map[string]string{"T": "{}"},
			wantErrs: []string{
				"2:12-2:15: A computed key in an object type must be a string literal, number literal, or unique symbol",
			},
		},
		{
			name:     "MissingNamespaceMember",
			src:      "namespace N { val k = \"x\" }\ntype T = {[N.j]: number}",
			types:    map[string]string{"T": "{}"},
			wantErrs: []string{"2:12-2:15: Namespace N has no member: j"},
		},
		{
			name:     "Namespace",
			src:      "namespace N { val k = \"x\" }\ntype T = {[N]: number}",
			types:    map[string]string{"T": "{}"},
			wantErrs: []string{"2:12-2:13: Namespace used as a value: N"},
		},
		{
			// A string spelling a reserved symbol member name is declined, as the same
			// string written as the key is.
			name:     "StringSpellingAReservedName",
			src:      "val k = \"@@iterator\"\ntype T = {[k]: number}",
			types:    map[string]string{"T": "{}"},
			wantErrs: []string{"2:12-2:13: Unsupported: ComputedKey"},
		},
		{
			// `k` and `T` form one declaration group, so `k` has no type yet when `T`
			// resolves.
			name:  "KeyWhoseTypeIsNotYetKnown",
			src:   "type T = {[k]: number}\nval k: T[\"a\"] = 1",
			types: map[string]string{"T": "{}"},
			wantErrs: []string{
				"1:12-1:13: A computed key in an object type must be a string literal, number literal, or unique symbol",
				"2:17-2:18: object {} has no property \"a\"",
			},
		},
	})
}

// TestAnnKeyAcrossFiles asserts that a key naming a `val` declared in another file of the
// module resolves, since the dep graph spans the files.
func TestAnnKeyAcrossFiles(t *testing.T) {
	_, types, errs := inferSources(t, map[string]string{
		"a.esc": "type T = {[k]: number}",
		"b.esc": "val k = \"z\"",
	})
	require.Empty(t, messagesWithSpan(t, errs))
	require.Equal(t, "{z: number}", types["T"])
}
