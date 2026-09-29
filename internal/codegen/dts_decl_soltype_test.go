package codegen

import (
	"testing"

	"github.com/escalier-lang/escalier/internal/soltype"
	"github.com/stretchr/testify/require"
)

// fakeSolNamespace answers the walk from maps a test fills, so each case states the
// types inference would have produced without running it.
type fakeSolNamespace struct {
	values   map[string]soltype.Type
	declared map[string]fakeDeclaredType
	nested   map[string]*fakeSolNamespace
}

// fakeDeclaredType is what a name's type declaration stands for, the pair
// DeclaredType returns.
type fakeDeclaredType struct {
	body       soltype.Type
	typeParams []*soltype.TypeParam
}

func (f *fakeSolNamespace) ValueType(name string) (soltype.Type, bool) {
	t, ok := f.values[name]
	return t, ok
}

func (f *fakeSolNamespace) DeclaredType(name string) (soltype.Type, []*soltype.TypeParam, bool) {
	d, ok := f.declared[name]
	if !ok {
		return nil, nil, false
	}
	return d.body, d.typeParams, true
}

func (f *fakeSolNamespace) Namespace(name string) (SolNamespace, bool) {
	ns, ok := f.nested[name]
	if !ok {
		return nil, false
	}
	return ns, true
}

// renderDeclFromSol emits decl's statements and prints them, so a case reads as the
// declarations a consumer of the `.d.ts` would see.
func renderDeclFromSol(t *testing.T, source string, ns *fakeSolNamespace, isTopLevel bool) string {
	t.Helper()
	builder := &Builder{}
	stmts := builder.buildDeclStmtFromSol(parseDecl(t, source), ns, solPreludePrefix, isTopLevel)
	printer := NewPrinter()
	printer.PrintModule(&Module{Stmts: stmts})
	return printer.Output
}

// TestBuildDeclStmtFromSol checks what each declaration kind emits from solver types.
// The types come from a fake namespace rather than a run, so each case names exactly
// the inference result it depends on.
func TestBuildDeclStmtFromSol(t *testing.T) {
	tests := []struct {
		name       string
		source     string
		ns         *fakeSolNamespace
		isTopLevel bool
		want       string
	}{
		{
			name:   "ValIsDeclaredAtTheTopLevel",
			source: `export val greeting = "hello"`,
			ns: &fakeSolNamespace{
				values: map[string]soltype.Type{"greeting": &soltype.LitType{Lit: &soltype.StrLit{Value: "hello"}}},
			},
			isTopLevel: true,
			want:       "export declare const greeting: \"hello\";\n",
		},
		{
			name:   "ValInsideANamespaceCarriesNoDeclare",
			source: `export val greeting = "hello"`,
			ns: &fakeSolNamespace{
				values: map[string]soltype.Type{"greeting": solStr()},
			},
			isTopLevel: false,
			want:       "export const greeting: string;\n",
		},
		{
			name:   "DestructuringEmitsOneDeclarationPerName",
			source: `val {b, a} = point`,
			ns: &fakeSolNamespace{
				values: map[string]soltype.Type{"a": solNum(), "b": solStr()},
			},
			isTopLevel: true,
			// Sorted rather than in the order the pattern writes them, so one source
			// emits the same output on every run.
			want: "declare const a: number;\ndeclare const b: string;\n",
		},
		{
			name:   "NameTheNamespaceDoesNotBindEmitsNothing",
			source: `val missing = 1`,
			ns:     &fakeSolNamespace{},
			want:   "",
		},
		{
			name:   "FnDeclaresItsSignature",
			source: `export fn add(a: number, b: number) -> number { return a + b }`,
			ns: &fakeSolNamespace{
				values: map[string]soltype.Type{
					"add": solFn([]*soltype.FuncParam{solParam("a", solNum()), solParam("b", solNum())}, solNum()),
				},
			},
			isTopLevel: true,
			want:       "export declare function add(a: number, b: number): number;\n",
		},
		{
			name:   "FnNamesTheTypeParametersGeneralizationRetained",
			source: `export fn id(x) { return x }`,
			ns: func() *fakeSolNamespace {
				v := &soltype.TypeVarType{ID: 1}
				return &fakeSolNamespace{
					values: map[string]soltype.Type{
						"id": solFn([]*soltype.FuncParam{solParam("x", v)}, v),
					},
				}
			}(),
			isTopLevel: true,
			// The signature holds both occurrences of the variable, so it binds it.
			// Rendering each alone would emit `(x: unknown) => unknown` and lose the
			// link between the parameter and the return.
			want: "export declare function id<T0>(x: T0): T0;\n",
		},
		{
			name:   "TypeAliasEmitsTheRegisteredBody",
			source: `export type Alias = string`,
			ns: &fakeSolNamespace{
				declared: map[string]fakeDeclaredType{"Alias": {body: solStr()}},
			},
			isTopLevel: true,
			want:       "export declare type Alias = string;\n",
		},
		{
			name:   "InterfaceEmitsItsMembers",
			source: "export interface Person {\n\tname: string,\n}",
			ns: &fakeSolNamespace{
				declared: map[string]fakeDeclaredType{
					"Person": {body: solObj(solProp("name", solStr()))},
				},
			},
			isTopLevel: true,
			want:       "export declare interface Person {\n  name: string;\n}\n",
		},
		{
			name:   "InterfaceExtendingAnotherSplitsTheIntersection",
			source: "export interface Employee extends Person {\n\tid: number,\n}",
			ns: &fakeSolNamespace{
				declared: map[string]fakeDeclaredType{
					// An interface that extends another is registered as the parent
					// intersected with the members this one adds.
					"Employee": {body: &soltype.IntersectionType{Types: []soltype.Type{
						solClass("Person"),
						solObj(solProp("id", solNum())),
					}}},
				},
			},
			isTopLevel: true,
			// The object arm becomes the body and the named arm the `extends` clause.
			// Emitting the intersection whole would leave the interface with no members
			// at all, its own included.
			want: "export declare interface Employee extends Person {\n  id: number;\n}\n",
		},
		{
			name:   "ClassEmitsAnInstanceTypeAndAStaticValue",
			source: "export class Point {\n\tx: number,\n}",
			ns: &fakeSolNamespace{
				declared: map[string]fakeDeclaredType{
					"Point": {body: solObj(solProp("x", solNum()))},
				},
				values: map[string]soltype.Type{
					"Point": solObj(&soltype.ConstructorElem{Signatures: []*soltype.FuncType{
						solFn([]*soltype.FuncParam{solParam("x", solNum())}, solClass("Point")),
					}}),
				},
			},
			isTopLevel: true,
			want: "export declare type Point = {x: number};\n" +
				"export declare const Point: {new (x: number): Point};\n",
		},
		{
			name:   "ClassWithNoStaticSideEmitsNothing",
			source: "class Point {\n\tx: number,\n}",
			ns: &fakeSolNamespace{
				declared: map[string]fakeDeclaredType{"Point": {body: solObj()}},
			},
			isTopLevel: true,
			want:       "",
		},
		{
			name:   "EnumEmitsANamespaceOfVariantsAndTheirUnion",
			source: "export enum Color {\n\tRed,\n\tHex(string),\n}",
			ns: &fakeSolNamespace{
				declared: map[string]fakeDeclaredType{
					"Color": {body: &soltype.UnionType{Types: []soltype.Type{
						solClass("Color.Red"),
						solClass("Color.Hex"),
					}}},
				},
				nested: map[string]*fakeSolNamespace{
					"Color": {
						declared: map[string]fakeDeclaredType{
							"Red": {body: solObj()},
							"Hex": {body: solObj()},
						},
						values: map[string]soltype.Type{
							"Red": solFn(nil, solClass("Color")),
							"Hex": solFn([]*soltype.FuncParam{solParam("code", solStr())}, solClass("Color")),
						},
					},
				},
			},
			isTopLevel: true,
			// The variants emit in the order the declaration writes them, and each is
			// exported from the namespace without `declare`, which a namespace member
			// does not take.
			want: "export declare namespace Color {\n" +
				"  export type Red = {};\n" +
				"  export const Red: () => Color;\n" +
				"  export type Hex = {};\n" +
				"  export const Hex: (code: string) => Color;\n" +
				"}\n" +
				"export declare type Color = Color.Red | Color.Hex;\n",
		},
		{
			name:   "EnumWithNoVariantNamespaceEmitsNothing",
			source: "enum Color {\n\tRed,\n}",
			ns: &fakeSolNamespace{
				declared: map[string]fakeDeclaredType{
					"Color": {body: &soltype.UnionType{Types: []soltype.Type{solClass("Color.Red")}}},
				},
			},
			isTopLevel: true,
			// The union names its variants through the namespace, so emitting the alias
			// alone would declare `type Color = Color.Red` against a `Color.Red` nothing
			// declares.
			want: "",
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			require.Equal(t, test.want, renderDeclFromSol(t, test.source, test.ns, test.isTopLevel))
		})
	}
}

// TestBuildDeclStmtFromSolSelfType checks that a binding whose type mentions `Self`
// moves into an interface. TypeScript spells `Self` as `this` and accepts it only
// inside one, so `declare const inc: (n: number) => this` would not compile.
func TestBuildDeclStmtFromSolSelfType(t *testing.T) {
	ns := &fakeSolNamespace{
		values: map[string]soltype.Type{
			"inc": solFn([]*soltype.FuncParam{solParam("n", solNum())}, solSelf("Counter")),
		},
	}
	require.Equal(t,
		"interface __inc_self__ {(n: number): this}\ndeclare const inc: __inc_self__;\n",
		renderDeclFromSol(t, `val inc = counter.increment`, ns, true))
}

// TestFindNamespaceFromSol checks the walk's namespace lookup, which resolves a dotted
// path one segment at a time.
func TestFindNamespaceFromSol(t *testing.T) {
	leaf := &fakeSolNamespace{}
	root := &fakeSolNamespace{nested: map[string]*fakeSolNamespace{
		"outer": {nested: map[string]*fakeSolNamespace{"inner": leaf}},
	}}

	tests := []struct {
		name  string
		path  string
		want  SolNamespace
		found bool
	}{
		{name: "EmptyPathIsTheRoot", path: "", want: root, found: true},
		{name: "OneSegment", path: "outer", want: root.nested["outer"], found: true},
		{name: "TwoSegments", path: "outer.inner", want: leaf, found: true},
		{name: "UnknownFirstSegment", path: "missing", want: nil, found: false},
		{name: "UnknownLastSegment", path: "outer.missing", want: nil, found: false},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, found := findNamespaceFromSol(root, test.path)
			require.Equal(t, test.found, found)
			require.Equal(t, test.want, got)
		})
	}
}
