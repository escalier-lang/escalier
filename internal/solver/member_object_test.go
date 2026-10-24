package solver

import (
	"testing"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/soltype"
	"github.com/stretchr/testify/require"
)

// lastReadOf returns the type info recorded for the last read of name.
func lastReadOf(t *testing.T, info *Info, name string) soltype.Type {
	t.Helper()
	var read *ast.IdentExpr
	for n := range info.types {
		ident, ok := n.(*ast.IdentExpr)
		if !ok || ident.Name != name {
			continue
		}
		if read == nil || ident.Span().Start.Offset > read.Span().Start.Offset {
			read = ident
		}
	}
	require.NotNil(t, read, "no read of %s was recorded", name)
	return info.TypeOf(read)
}

func TestMemberObject(t *testing.T) {
	tests := []struct {
		name string
		// src reads v last. MemberObject is asked about the type recorded for that read.
		src string
		// expected is the returned object rendered by soltype.Print, or "" when
		// MemberObject returns false.
		expected string
	}{
		{
			name: "ClassInstanceWithInheritedMembers",
			src: `
				class Animal<T> {
					name: T,
					constructor(&mut self, name: T) { self.name = name },
					speak(&self) -> T { return self.name },
				}
				class Dog extends Animal<string> {
					constructor(&mut self) { super("rex") },
					bark(&self) -> string { return "woof" },
				}
				val v = Dog()
				val probe = v
			`,
			expected: "{bark(&self) -> string, name: string, speak(&self) -> string, ...}",
		},
		{
			name: "ClassValueStatics",
			src: `
				class Counter {
					n: number,
					static zero() -> number { return 0 },
				}
				val v = Counter
				val probe = v
			`,
			expected: "{new (n: number) -> Counter, zero() -> number}",
		},
		{
			name: "Alias",
			src: `
				type Point = {x: number, y: number}
				declare val v: Point
				val probe = v
			`,
			expected: "{x: number, y: number}",
		},
		{
			name: "Interface",
			src: `
				interface Named { name: string }
				declare val v: Named
				val probe = v
			`,
			expected: "{name: string, ...}",
		},
		{
			name: "ObjectLiteral",
			src: `
				val v = {a: 1, b: "two"}
				val probe = v
			`,
			expected: `{a: 1, b: "two"}`,
		},
		{
			name: "Borrow",
			src: `
				declare val v: &{a: number}
				val probe = v
			`,
			expected: "{a: number}",
		},
		{
			name: "Tuple",
			src: `
				val v = [1, "two"]
				val probe = v
			`,
			expected: `{length: number, at(&self, index: number) -> 1 | "two" | undefined, push(&mut self, item: 1 | "two") -> number, [Symbol.iterator](&self) -> Iterator<1 | "two", undefined, unknown>, ...}`,
		},
		{
			name: "AliasOfTuple",
			src: `
				type Pair = [number, string]
				declare val v: Pair
				val probe = v
			`,
			expected: `{length: number, at(&self, index: number) -> number | string | undefined, push(&mut self, item: number | string) -> number, [Symbol.iterator](&self) -> Iterator<number | string, undefined, unknown>, ...}`,
		},
		{
			name: "VariadicTuple",
			src: `
				declare val v: [number, ...Array<string>]
				val probe = v
			`,
			expected: `{length: number, at(&self, index: number) -> number | string | undefined, push(&mut self, item: number | string) -> number, [Symbol.iterator](&self) -> Iterator<number | string, undefined, unknown>, ...}`,
		},
		{
			name: "Typeof",
			src: `
				declare val point: {x: number}
				declare val v: typeof point
				val probe = v
			`,
			expected: "{x: number}",
		},
		{
			name: "UnreducedMappedType",
			src: `
				fn f<T>(v: {[P]: T[P] for P in keyof T}) {
					val probe = v
				}
			`,
			expected: "",
		},
		{
			name: "Union",
			src: `
				declare val v: {a: number} | {b: string}
				val probe = v
			`,
			expected: "{a?: number, b?: string}",
		},
		{
			name: "UnionCommonKey",
			src: `
				declare val v: {kind: "a", x: number} | {kind: "b", y: string}
				val probe = v
			`,
			expected: `{kind: "a" | "b", x?: number, y?: string}`,
		},
		{
			name: "UnionWithUndefined",
			src: `
				declare val v: {a: number} | undefined
				val probe = v
			`,
			expected: "{a: number}",
		},
		{
			name: "UnionOfMethods",
			src: `
				declare val v: {m(&self) -> number, n(&self) -> number} | {m(&self) -> string}
				val probe = v
			`,
			expected: "{m() -> number; m() -> string, n?() -> number}",
		},
		{
			name: "UnionOfReadonlyAndWritable",
			src: `
				declare val v: {readonly a: number} | {a: string}
				val probe = v
			`,
			expected: "{readonly a: number | string}",
		},
		{
			name: "UnionOfAccessors",
			src: `
				declare val v: {get a(&self) -> number, set a(&mut self, v: number)} | {get a(&self) -> string}
				val probe = v
			`,
			expected: "{readonly a: number | string}",
		},
		{
			name: "UnionOfSetterAndProperty",
			src: `
				declare val v: {set a(&mut self, v: number)} | {a: string}
				val probe = v
			`,
			expected: "{a?: string}",
		},
		{
			name: "UnionOfSetters",
			src: `
				declare val v: {set a(&mut self, v: number)} | {set a(&mut self, v: string)}
				val probe = v
			`,
			expected: "{set a(v: number)}",
		},
		{
			name: "UnionOfMethodAndProperty",
			src: `
				declare val v: {m(&self) -> number} | {m: string}
				val probe = v
			`,
			expected: "{readonly m: (fn () -> number) | string}",
		},
		{
			name: "UnionOfOverloadedMethodAndProperty",
			src: `
				declare val v: {m(&self, x: number) -> number, m(&self, x: string) -> string} | {m: boolean}
				val probe = v
			`,
			expected: "{readonly m: (fn (x: number) -> number) & (fn (x: string) -> string) | boolean}",
		},
		{
			name: "UnionWithIndexSignature",
			src: `
				declare val v: {[K]: number for K in string} | {a: number}
				val probe = v
			`,
			expected: "",
		},
		{
			name: "UnionOfNullAndUndefined",
			src: `
				declare val v: null | undefined
				val probe = v
			`,
			expected: "",
		},
		{
			name: "UnionWithUnreducedMember",
			src: `
				fn f<T>(v: {[P]: T[P] for P in keyof T} | {a: number}) {
					val probe = v
				}
			`,
			expected: "",
		},
		{
			name: "Intersection",
			src: `
				declare val v: {a: number} & {b: string}
				val probe = v
			`,
			expected: "",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res := InferModuleWithSource(parseModule(t, tt.src), testStdlibSource())
			require.Empty(t, res.Errors)

			obj, ok := res.Info.MemberObject(lastReadOf(t, res.Info, "v"))
			if tt.expected == "" {
				require.False(t, ok)
				return
			}
			require.True(t, ok)
			require.Equal(t, tt.expected, soltype.Print(obj))
		})
	}
}

// TestMemberObjectStdlibClass covers the types that read their members off a class the
// standard library declares. A primitive or a literal reads its wrapper class, and a
// function reads `Function`. Those classes declare dozens of members, so each case
// asserts a few names rather than the whole list.
func TestMemberObjectStdlibClass(t *testing.T) {
	tests := []struct {
		name     string
		src      string
		expected []string
	}{
		{
			name:     "StringLiteral",
			src:      "val v = \"abc\"\nval probe = v",
			expected: []string{"length", "toUpperCase", "slice"},
		},
		{
			name:     "AliasOfString",
			src:      "type S = string\ndeclare val v: S\nval probe = v",
			expected: []string{"length", "toUpperCase"},
		},
		{
			name:     "DeclaredNumber",
			src:      "declare val v: number\nval probe = v",
			expected: []string{"toFixed", "toString"},
		},
		{
			name:     "Function",
			src:      "fn v() -> number { return 1 }\nval probe = v",
			expected: []string{"name", "apply", "call", "bind"},
		},
		{
			name:     "UnionOfPrimitives",
			src:      "declare val v: number | string\nval probe = v",
			expected: []string{"toFixed", "toUpperCase", "toString"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res := InferModuleAgainstStdlib(parseModule(t, tt.src), benchTree, Options{})
			require.Empty(t, messagesWithSpan(t, res.Errors))

			obj, ok := res.Info.MemberObject(lastReadOf(t, res.Info, "v"))
			require.True(t, ok)
			names := memberNames(obj)
			for _, name := range tt.expected {
				require.True(t, names.Contains(name), "missing member %s", name)
			}
		})
	}
}

func TestMemberObjectOnNewInfo(t *testing.T) {
	_, ok := NewInfo().MemberObject(&soltype.ObjectType{})
	require.False(t, ok)
}

func TestInfoExpand(t *testing.T) {
	tests := []struct {
		name string
		// src reads v last. Expand is asked about the type v was declared at.
		src      string
		expected string
	}{
		{
			name:     "AliasOfUnion",
			src:      "type Shape = {a: number} | {b: string}\ndeclare val v: Shape\nval probe = v",
			expected: "{a: number} | {b: string}",
		},
		{
			name:     "BorrowOfAlias",
			src:      "type Point = {x: number}\ndeclare val v: &Point\nval probe = v",
			expected: "{x: number}",
		},
		{
			name:     "PlainObject",
			src:      "declare val v: {x: number}\nval probe = v",
			expected: "{x: number}",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			res := InferModuleWithSource(parseModule(t, tt.src), testStdlibSource())
			require.Empty(t, res.Errors)

			binding, ok := res.Scope.OwnValue("v")
			require.True(t, ok)
			require.Equal(t, tt.expected, soltype.Print(res.Info.Expand(binding.DisplayType())))
		})
	}
}

func TestInfoExpandOnNewInfo(t *testing.T) {
	alias := &soltype.AliasType{Name: "Point"}
	require.Same(t, alias, NewInfo().Expand(alias))
}

func TestNeedsMutReceiver(t *testing.T) {
	recv := func(mut bool) *soltype.FuncParam {
		return &soltype.FuncParam{Type: &soltype.RefType{Mut: mut, Inner: &soltype.ObjectType{}}}
	}
	method := func(muts ...bool) *soltype.MethodElem {
		m := &soltype.MethodElem{Name: "m"}
		for _, mut := range muts {
			m.Signatures = append(m.Signatures, &soltype.FuncType{SelfParam: recv(mut)})
		}
		return m
	}
	tests := []struct {
		name     string
		member   soltype.ObjTypeElem
		expected bool
	}{
		{name: "MutMethod", member: method(true), expected: true},
		{name: "ImmMethod", member: method(false), expected: false},
		{name: "AllArmsMut", member: method(true, true), expected: true},
		{name: "MixedArmsImmFirst", member: method(false, true), expected: false},
		{name: "MixedArmsMutFirst", member: method(true, false), expected: false},
		{name: "NoArms", member: method(), expected: false},
		{name: "Setter", member: &soltype.SetterElem{Name: "s"}, expected: true},
		{name: "MutGetter", member: &soltype.GetterElem{Name: "g", SelfParam: recv(true)}, expected: true},
		{name: "Property", member: &soltype.PropertyElem{Name: "p"}, expected: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.expected, NeedsMutReceiver(tt.member))
		})
	}
}
