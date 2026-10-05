package solver

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// indexCase is one row of the index, computed-key, and assignment tables below. want
// maps a top-level binding to its rendered type, and wantErrs lists every diagnostic
// with its span, in report order.
type indexCase struct {
	name     string
	src      string
	want     map[string]string
	wantErrs []string
}

func runIndexCases(t *testing.T, tests []indexCase) {
	t.Helper()
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

// TestInferIndexRead covers `recv[k]` in value position. An array yields its element
// type, a tuple yields the element at a literal position, and an index signature yields
// its value for a key the object does not name. A key whose type is one string literal
// reads that property the way dot access does.
func TestInferIndexRead(t *testing.T) {
	runIndexCases(t, []indexCase{
		{
			name: "ArrayAtALiteral",
			src:  "declare val a: Array<number>\nval first = a[0]",
			want: map[string]string{"first": "number"},
		},
		{
			name: "ArrayAtANumber",
			src:  "fn f(a: Array<string>, i: number) { return a[i] }",
			want: map[string]string{"f": "fn (a: Array<string>, i: number) -> string"},
		},
		{
			name: "BorrowedArray",
			src:  "fn f(a: &Array<string>) { return a[0] }",
			want: map[string]string{"f": "fn (a: &Array<string>) -> string"},
		},
		{
			name: "TupleAtALiteral",
			src:  "declare val t: [number, string]\nval x = t[0]\nval y = t[1]",
			want: map[string]string{"x": "number", "y": "string"},
		},
		{
			name:     "TupleOutOfRange",
			src:      "declare val t: [number, string]\nval z = t[2]",
			wantErrs: []string{"2:9-2:13: index 2 is out of range for tuple [number, string]"},
		},
		{
			name: "IndexSignature",
			src:  "declare val d: {[K: string]?: number}\nfn f(k: string) { return d[k] }",
			want: map[string]string{"f": "fn (k: string) -> number | undefined"},
		},
		{
			name: "IndexSignatureBesideANamedProperty",
			src:  "declare val d: {a: string, [K: string]?: number}\nval a = d[\"a\"]\nval b = d[\"b\"]",
			want: map[string]string{"a": "string", "b": "number | undefined"},
		},
		{
			name: "KeyTypedByOneStringLiteral",
			src:  "val k = \"a\"\nval o = {a: 5}\nval r = o[k]",
			want: map[string]string{"r": "5"},
		},
		{
			// `k` holds "a" only until `g` reassigns it, so the key reads as `string` rather
			// than naming `a`.
			name:     "KeyFromAVarInitializedThroughABinding",
			src:      "val x = \"a\"\nvar k = x\nval o = {a: 5, b: true}\nval r = o[k]\nfn g() { k = \"b\" }",
			wantErrs: []string{"4:9-4:13: object {a: 5, b: true} has no index signature to read a key of type string"},
		},
		{
			name:     "NoIndexSignature",
			src:      "declare val o: {a: number}\nfn f(k: string) { return o[k] }",
			wantErrs: []string{"2:26-2:30: object {a: number} has no index signature to read a key of type string"},
		},
		{
			// A receiver inferred from its uses has no lower bound to read an element
			// type off.
			name:     "ReceiverInferredFromUse",
			src:      "fn f(o) { return o[0] }",
			wantErrs: []string{"1:18-1:22: Unsupported: IndexExpr"},
		},
		{
			// A caller may pass any key, so the default does not decide which property the
			// read names.
			name:     "KeyIsAParameterWithADefault",
			src:      "val o = {a: 5, b: \"x\"}\nfn f(k = \"a\") { return o[k] }",
			wantErrs: []string{"2:24-2:28: Unsupported: IndexExpr"},
		},
		{
			name:     "KeyIsADestructuredParameterWithADefault",
			src:      "val o = {a: 5, b: \"x\"}\nfn f({k = \"a\"}) { return o[k] }",
			wantErrs: []string{"2:26-2:30: Unsupported: IndexExpr"},
		},
		{
			name:     "KeyIsADestructuredLocalWithADefault",
			src:      "val o = {a: 5, b: \"x\"}\nfn f(p) { val {k = \"a\"} = p\n return o[k] }",
			wantErrs: []string{"3:9-3:13: Unsupported: IndexExpr"},
		},
		{
			name: "NumberLiteralKeyOnAnObject",
			src:  "val o = {[1]: \"one\"}\nval r = o[1]",
			want: map[string]string{"r": `"one"`},
		},
	})
}

// TestInferComputedKey covers a computed key `{[k]: v}` in an object literal. A key
// typed by one string or number literal names a property, and any other legal key adds
// to the literal's index signature.
func TestInferComputedKey(t *testing.T) {
	runIndexCases(t, []indexCase{
		{
			name: "StringLiteralKeyNamesAProperty",
			src:  "val k = \"x\"\nval o = {a: true, [k]: 5}\nval r = o[k]",
			want: map[string]string{"o": "{a: true, x: 5}", "r": "5"},
		},
		{
			// `k` holds "a" only until `g` reassigns it, so the key adds an index signature
			// rather than naming `a`.
			name: "KeyFromAVarInitializedThroughABinding",
			src:  "val x = \"a\"\nvar k = x\nval o = {[k]: 5}\nfn g() { k = \"b\" }",
			want: map[string]string{"o": "{[K: string]?: 5}"},
		},
		{
			name: "NumberLiteralKeyNamesAProperty",
			src:  "val o = {[1]: \"one\"}",
			want: map[string]string{"o": `{"1": "one"}`},
		},
		{
			name: "StringKeyAddsAnIndexSignature",
			src:  "val k: string = \"x\"\nval o = {[k]: 5}\nval r = o[k]\nval s = o.other",
			want: map[string]string{
				"o": "{[K: string]?: 5}",
				"r": "5 | undefined",
				"s": "5 | undefined",
			},
		},
		{
			name: "SeveralKeysShareOneSignature",
			src:  "declare val s: string\ndeclare val n: number\nval o = {[s]: 5, [n]: \"x\"}",
			want: map[string]string{"o": `{[K: number | string]?: 5 | "x"}`},
		},
		{
			name: "UniqueSymbolKeyWidensToSymbol",
			src:  "declare val sym: unique symbol\nval o = {[sym]: 1}",
			want: map[string]string{"o": "{[K: symbol]?: 1}"},
		},
		{
			name:     "KeyThatCannotKeyAProperty",
			src:      "val o = {[true]: 5}",
			want:     map[string]string{"o": "{}"},
			wantErrs: []string{"1:11-1:15: Invalid object key: true"},
		},
		{
			name: "ParameterKey",
			src:  "fn f(k: string) { return {[k]: 1} }",
			want: map[string]string{"f": "fn (k: string) -> {[K: string]?: 1}"},
		},
		{
			// The key may replace the property written before it, so the property holds
			// either value.
			name: "KeyAfterAPropertyItMayReplace",
			src:  "fn f(s: string) { val o = {a: \"x\", [s]: 1}\n return o.a }",
			want: map[string]string{"f": `fn (s: string) -> 1 | "x"`},
		},
		{
			// A property written after the key replaces whatever the key stored there.
			name: "PropertyAfterAKey",
			src:  "fn f(s: string) { val o = {[s]: 1, a: \"x\"}\n return o.a }",
			want: map[string]string{"f": `fn (s: string) -> "x"`},
		},
		{
			// A `number` key reaches only a property whose name spells a number.
			name: "NumberKeyAfterProperties",
			src:  "fn f(n: number) { return {a: \"x\", 1: true, [n]: 2} }",
			want: map[string]string{"f": `fn (n: number) -> {a: "x", "1": 2 | true, [K: number]?: 2}`},
		},
		{
			// A parameter's default is one value it may hold, not the only one, so the key
			// names no single property.
			name:     "UnannotatedParameterWithADefault",
			src:      "fn f(k = \"a\") { return {[k]: 1} }",
			wantErrs: []string{"1:26-1:27: Unsupported: ComputedKey"},
		},
	})
}

// TestInferClassComputedKey covers a class member keyed by `[k]` where k's type is one
// string or number literal. The member takes that name, so it is read back through
// `obj[k]` and initialized through `self[k]`.
func TestInferClassComputedKey(t *testing.T) {
	runIndexCases(t, []indexCase{
		{
			name: "FieldAndMethod",
			src: `val bar = "bar"
val baz = "baz"
class Foo {
    [bar]: number,
    [baz](&self) { return self[bar] },
    constructor(&mut self, v: number) { self[bar] = v }
}
val foo = Foo(5)
val fooBar = foo[bar]
val fooBaz = foo[baz]()`,
			want: map[string]string{"fooBar": "number", "fooBaz": "number"},
		},
		{
			name: "FieldLeftUninitialized",
			src: `val bar = "bar"
class Foo {
    [bar]: number,
    constructor(&mut self) {}
}`,
			wantErrs: []string{"4:28-4:30: Field 'bar' is not initialized on every path through the constructor."},
		},
		{
			name: "FieldReadBeforeItIsInitialized",
			src: `val bar = "bar"
class Foo {
    [bar]: number,
    constructor(&mut self) { self[bar] = self[bar] }
}`,
			wantErrs: []string{"4:42-4:51: Field 'self.bar' is read before it has been initialized."},
		},
		{
			name: "NumberLiteralKey",
			src: `val k = 0
class Foo {
    [k]: number,
    constructor(&mut self, v: number) { self[k] = v }
}
val foo = Foo(5)
val r = foo[k]`,
			want: map[string]string{"r": "number"},
		},
		{
			name:     "KeyOfAWideType",
			src:      "declare val k: string\nclass Foo { [k]: number }",
			wantErrs: []string{"2:14-2:15: Unsupported: ComputedKey"},
		},
	})
}

// TestInferAssignToMemberOrIndex covers `o.x = v` and `a[i] = v`. Both need a mutable
// receiver and a slot that is not `readonly`, and the stored value has to fit the slot.
func TestInferAssignToMemberOrIndex(t *testing.T) {
	runIndexCases(t, []indexCase{
		{
			name: "MemberOnAMutableReceiver",
			src:  "fn f(o: mut {x: number}) { o.x = 5 }",
			want: map[string]string{"f": "fn (o: mut {x: number}) -> undefined"},
		},
		{
			name:     "MemberOnAnImmutableReceiver",
			src:      "fn f(o: {x: number}) { o.x = 5 }",
			wantErrs: []string{"1:24-1:31: cannot constrain immutable object <: mutable object"},
		},
		{
			name: "ArrayElementOnAMutableReceiver",
			src:  "fn f(a: mut Array<number>) { a[0] = 5 }",
			want: map[string]string{"f": "fn (a: mut Array<number>) -> undefined"},
		},
		{
			name:     "ArrayElementOnAnImmutableReceiver",
			src:      "fn f(a: Array<number>) { a[0] = 5 }",
			wantErrs: []string{"1:26-1:34: cannot constrain immutable Array<number> <: mutable Array<number>"},
		},
		{
			name:     "ArrayElementOfTheWrongType",
			src:      "fn f(a: mut Array<number>) { a[0] = \"x\" }",
			wantErrs: []string{`1:37-1:40: cannot constrain "x" <: number`},
		},
		{
			name: "AssignmentEvaluatesToTheStoredValue",
			src:  "fn f(a: mut Array<number>, i: number) { val r = (a[i] = 3)\n return r }",
			want: map[string]string{"f": "fn (a: mut Array<number>, i: number) -> number"},
		},
		{
			name: "TupleElementAtALiteral",
			src:  "fn f(t: mut [number, string]) { t[1] = \"x\"\n t[0] = 1 }",
			want: map[string]string{"f": "fn (t: mut [number, string]) -> undefined"},
		},
		{
			name:     "TupleElementOutOfRange",
			src:      "fn f(t: mut [number, string]) { t[2] = \"x\" }",
			wantErrs: []string{"1:33-1:37: index 2 is out of range for tuple [number, string]"},
		},
		{
			// A `number` key may land on either position, so the value has to fit both.
			name:     "TupleElementAtANumber",
			src:      "fn f(t: mut [number, string], i: number) { t[i] = 1 }",
			wantErrs: []string{"1:51-1:52: cannot constrain 1 <: string"},
		},
		{
			name: "IndexSignatureSlot",
			src:  "fn f(d: mut {[K: string]?: number}, k: string) { d[k] = 1 }",
			want: map[string]string{"f": "fn (d: mut {[K: string]?: number}, k: string) -> undefined"},
		},
		{
			name:     "IndexSignatureSlotOfTheWrongType",
			src:      "fn f(d: mut {[K: string]?: number}, k: string) { d[k] = \"s\" }",
			wantErrs: []string{`1:57-1:60: cannot constrain "s" <: number`},
		},
		{
			// A `string` key may name `a` as well as a key the signature covers.
			name:     "IndexSignatureKeyThatMayNameAProperty",
			src:      "fn f(o: mut {[K: string]?: number, a: string}, k: string) { o[k] = 1 }",
			wantErrs: []string{"1:68-1:69: cannot constrain 1 <: string"},
		},
		{
			name:     "ReadonlyIndexSignature",
			src:      "fn f(d: mut {readonly [K: string]?: number}, k: string) { d[k] = 1 }",
			wantErrs: []string{"1:59-1:67: cannot assign to readonly property: [string]"},
		},
		{
			name: "KeyTypedByOneStringLiteralWritesTheField",
			src:  "val k = \"x\"\nfn f(o: mut {x: number}) { o[k] = 1 }",
			want: map[string]string{"f": "fn (o: mut {x: number}) -> undefined"},
		},
		{
			name:     "KeyTypedByOneStringLiteralOnAnImmutableReceiver",
			src:      "val k = \"x\"\nfn f(o: {x: number}) { o[k] = 1 }",
			wantErrs: []string{"2:24-2:32: cannot constrain immutable object <: mutable object"},
		},
		{
			name:     "ReadonlyFieldThroughABracket",
			src:      "fn f(o: mut {readonly x: number}) { o[\"x\"] = 1 }",
			wantErrs: []string{"1:37-1:47: cannot assign to readonly property: x"},
		},
		{
			name:     "ReceiverInferredFromUse",
			src:      "fn f(o) { o[0] = 1 }",
			wantErrs: []string{"1:11-1:15: Unsupported: assignment to a member or index"},
		},
	})
}

// TestInferParamDefault covers a parameter written `x = value`. The default makes the
// parameter optional and has to fit the parameter's type.
func TestInferParamDefault(t *testing.T) {
	runIndexCases(t, []indexCase{
		{
			name: "AnnotatedParameter",
			src:  "fn g(v: number = 4) { return v }\nval r = g()",
			want: map[string]string{"g": "fn (v?: number) -> number", "r": "number"},
		},
		{
			// The caller may pass any value, and the body sees either that value or the default.
			name: "UnannotatedParameter",
			src:  "fn g(v = 4) { return v }",
			want: map[string]string{"g": "fn <T0>(v?: T0) -> T0 | 4"},
		},
		{
			name:     "DefaultOfTheWrongType",
			src:      "fn g(v: number = \"x\") { return v }",
			wantErrs: []string{`1:18-1:21: cannot constrain "x" <: number`},
		},
		{
			name: "DefaultReadsAnEarlierParameter",
			src:  "fn g(a: number, b: number = a) { return b }",
			want: map[string]string{"g": "fn (a: number, b?: number) -> number"},
		},
		{
			name:     "DefaultDoesNotSeeALaterParameter",
			src:      "fn g(a: number, b: number = c, c: number = 1) { return b }",
			wantErrs: []string{"1:29-1:30: Unknown identifier: c"},
		},
		{
			// The default runs when `g` is called, so its raise is one of `g`'s.
			name: "DefaultRaisesThroughTheFunction",
			src:  "declare fn boom() -> number throws string\nfn g(v: number = boom()) throws string { return v }",
			want: map[string]string{"g": "fn (v?: number) -> number throws string"},
		},
		{
			name:     "DefaultRaisesFromAFunctionThatDeclaresNone",
			src:      "declare fn boom() -> number throws string\nfn g(v: number = boom()) { return v }",
			wantErrs: []string{"2:18-2:24: cannot constrain string <: never"},
		},
	})
}
