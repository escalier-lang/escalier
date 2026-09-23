package codegen

import (
	"testing"

	"github.com/escalier-lang/escalier/internal/soltype"
	"github.com/escalier-lang/escalier/internal/solver"
	type_sys "github.com/escalier-lang/escalier/internal/type_system"
	"github.com/stretchr/testify/require"
)

// solPreludePrefix is the package key `std:prelude` is registered under, which
// TestResolveTypeAnnForTestPreludeName in internal/solver pins. A renderer that
// matched on the bare last name component instead would fail the cases below.
const solPreludePrefix = "import:std:prelude"

// renderSol renders a solver type the way BuildDefinitions emits it: through the
// soltype renderer and then the .d.ts printer. Asserting on the printed
// TypeScript keeps each case readable as the declaration a reader would see.
func renderSol(t *testing.T, ty soltype.Type) string {
	t.Helper()
	return renderSolWithParams(t, ty, nil)
}

// renderSolWithParams renders ty with a declaration's own type parameters in scope.
func renderSolWithParams(t *testing.T, ty soltype.Type, typeParams []*soltype.TypeParam) string {
	t.Helper()
	printer := NewPrinter()
	printer.PrintTypeAnn(newSolTypeAnnBuilder(solPreludePrefix, typeParams).render(ty))
	return printer.Output
}

func solNum() soltype.Type  { return &soltype.PrimType{Prim: soltype.NumPrim} }
func solStr() soltype.Type  { return &soltype.PrimType{Prim: soltype.StrPrim} }
func solBool() soltype.Type { return &soltype.PrimType{Prim: soltype.BoolPrim} }

// solParam is the ordinary `name: T` parameter, the shape most signatures below need.
func solParam(name string, ty soltype.Type) *soltype.FuncParam {
	return &soltype.FuncParam{
		Pattern:  &soltype.IdentPat{Name: name},
		Type:     ty,
		Optional: false,
		Rest:     false,
	}
}

// solFn builds a monomorphic, non-throwing signature.
func solFn(params []*soltype.FuncParam, ret soltype.Type) *soltype.FuncType {
	return &soltype.FuncType{
		SelfParam:      nil,
		Params:         params,
		Ret:            ret,
		Throws:         nil,
		Inexact:        false,
		TypeParams:     nil,
		LifetimeParams: nil,
	}
}

func solObj(elems ...soltype.ObjTypeElem) *soltype.ObjectType {
	return &soltype.ObjectType{Elems: elems, Inexact: false}
}

func solProp(name string, ty soltype.Type) *soltype.PropertyElem {
	return &soltype.PropertyElem{Name: name, Type: ty, Optional: false, Readonly: false}
}

func solClass(name string, args ...soltype.Type) *soltype.ClassType {
	return &soltype.ClassType{
		Name: name, TypeArgs: args, Defaults: nil, LifetimeArgs: nil,
		Lt: nil, Final: false, Variant: false,
	}
}

func solAlias(name string, args ...soltype.Type) *soltype.AliasType {
	return &soltype.AliasType{Name: name, TypeArgs: args, Defaults: nil, LifetimeArgs: nil}
}

// solSelf is a `Self` written inside a member of the named class.
func solSelf(className string) *soltype.SelfType {
	return &soltype.SelfType{Class: solClass(className)}
}

func TestBuildTypeAnnFromSolAtoms(t *testing.T) {
	tests := map[string]struct {
		ty       soltype.Type
		expected string
	}{
		"Number":       {solNum(), "number"},
		"String":       {solStr(), "string"},
		"Boolean":      {solBool(), "boolean"},
		"Symbol":       {&soltype.PrimType{Prim: soltype.SymPrim}, "symbol"},
		"NumLit":       {&soltype.LitType{Lit: &soltype.NumLit{Value: 5}}, "5"},
		"StrLit":       {&soltype.LitType{Lit: &soltype.StrLit{Value: "hello"}}, `"hello"`},
		"BoolLit":      {&soltype.LitType{Lit: &soltype.BoolLit{Value: true}}, "true"},
		"Null":         {&soltype.NullType{}, "null"},
		"Undefined":    {&soltype.UndefinedType{}, "undefined"},
		"Never":        {&soltype.NeverType{}, "never"},
		"Unknown":      {&soltype.UnknownType{}, "unknown"},
		"UniqueSymbol": {&soltype.UniqueSymbolType{ID: 3}, "unique symbol"},
		// The error sentinel absorbs every constraint, which is what `any` does.
		"Error": {&soltype.ErrorType{}, "any"},
		// A skolem is a rigid type parameter and renders under its source name.
		"Skolem": {&soltype.SkolemType{ID: 1, Name: "T", Upper: nil}, "T"},
		// `Self` denotes the receiver's class, which TypeScript spells `this`.
		"Self": {solSelf("Point"), "this"},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			require.Equal(t, test.expected, renderSol(t, test.ty))
		})
	}
}

func TestBuildTypeAnnFromSolUnresolvedTypeVar(t *testing.T) {
	// Generalization inlines every variable into its bounds, so one that survives
	// to emission is unresolved and falls back to `unknown`.
	v := &soltype.TypeVarType{
		ID: 7, Level: 0, LowerBounds: nil, UpperBounds: nil,
		Open: false, Widenable: false,
	}
	require.Equal(t, "unknown", renderSol(t, v))
}

func TestBuildTypeAnnFromSolNominalRefs(t *testing.T) {
	tests := map[string]struct {
		ty       soltype.Type
		expected string
	}{
		"Class":        {solClass("Point"), "Point"},
		"GenericClass": {solClass("Box", solNum()), "Box<number>"},
		// A same-module namespace path is what TypeScript writes too, so it
		// survives. An enum variant keeps its enum's name the same way.
		"QualifiedClass": {solClass("Geometry.Point"), "Geometry.Point"},
		"EnumVariant": {&soltype.ClassType{
			Name: "Color.RGB", TypeArgs: nil, Defaults: nil, LifetimeArgs: nil,
			Lt: nil, Final: false, Variant: true,
		}, "Color.RGB"},
		// A class from another package carries the `import:<uri>.` key prefix its
		// declaration is registered under, which TypeScript cannot write.
		"ImportedClass":        {solClass("import:std:array.Foo"), "Foo"},
		"ImportedNestedClass":  {solClass("import:npm:a%2Eb.Geometry.Point"), "Geometry.Point"},
		"ImportedGenericClass": {solClass("import:lodash.Box", solNum()), "Box<number>"},
		"Alias":                {solAlias("Point"), "Point"},
		"GenericAlias":         {solAlias("Box", solStr()), "Box<string>"},
		"ImportedAlias":        {solAlias("import:std:array.Elem"), "Elem"},
		// Four prelude declarations take one more type parameter than TypeScript's,
		// the trailing `E` for what the value may raise. A reference to one is
		// trimmed to the arity TypeScript declares.
		"Promise": {
			solClass(solPreludePrefix+".Promise", solNum(), solStr()),
			"Promise<number>",
		},
		"PromiseLike": {
			solAlias(solPreludePrefix+".PromiseLike", solNum(), solStr()),
			"PromiseLike<number>",
		},
		"Generator": {
			solAlias(solPreludePrefix+".Generator", solNum(), solStr(), solBool(), solStr()),
			"Generator<number, string, boolean>",
		},
		"AsyncGenerator": {
			solAlias(solPreludePrefix+".AsyncGenerator", solNum(), solStr(), solBool(), solStr()),
			"AsyncGenerator<number, string, boolean>",
		},
		// The prelude's iteration types declare the parameters TypeScript does, so
		// a reference to one keeps every argument it was given.
		"PreludeIterator": {
			solAlias(solPreludePrefix+".Iterator", solNum(), solStr(), solBool()),
			"Iterator<number, string, boolean>",
		},
		"PreludeIterable": {
			solAlias(solPreludePrefix+".Iterable", solNum(), solStr(), solBool()),
			"Iterable<number, string, boolean>",
		},
		// A user type whose last name component is also `Promise` is a different
		// type and keeps every argument it declared.
		"LookalikePromise": {solClass("app.Promise", solNum(), solStr()), "app.Promise<number, string>"},
		// So is one in a package other than the prelude.
		"LookalikeGenerator": {
			solAlias("import:npm:rxjs.Generator", solNum(), solStr(), solBool(), solStr()),
			"Generator<number, string, boolean, string>",
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			require.Equal(t, test.expected, renderSol(t, test.ty))
		})
	}
}

// TestBuildTypeAnnFromSolInferredTypeParams pins the binders for a generic
// function that declares none.
//
// An un-annotated `fn f(x) { return x }` coalesces to `fn (x: t1) -> t1`, a
// signature whose TypeParams list is empty and whose parameter and return share
// one retained variable. Rendering each occurrence on its own would emit
// `(x: unknown) => unknown` and lose the link the variable carries.
func TestBuildTypeAnnFromSolInferredTypeParams(t *testing.T) {
	freshVar := func(id int) *soltype.TypeVarType {
		return &soltype.TypeVarType{
			ID: id, Level: 1, LowerBounds: nil, UpperBounds: nil,
			Open: false, Widenable: false,
		}
	}

	t.Run("OneVariableTwoPositions", func(t *testing.T) {
		v := freshVar(1)
		sig := solFn([]*soltype.FuncParam{solParam("x", v)}, v)
		require.Equal(t, "<T0>(x: T0) => T0", renderSol(t, sig))
	})

	t.Run("TwoVariables", func(t *testing.T) {
		a, b := freshVar(1), freshVar(2)
		sig := solFn([]*soltype.FuncParam{solParam("a", a), solParam("b", b)},
			&soltype.TupleType{Elems: []soltype.Type{a, b}, Inexact: false})
		require.Equal(t, "<T0, T1>(a: T0, b: T1) => [T0, T1]", renderSol(t, sig))
	})

	t.Run("UnderAStructuralPosition", func(t *testing.T) {
		v := freshVar(1)
		sig := solFn([]*soltype.FuncParam{solParam("x", v)}, solObj(solProp("value", v)))
		require.Equal(t, "<T0>(x: T0) => {value: T0}", renderSol(t, sig))
	})

	t.Run("BoundVariableRendersAnExtendsClause", func(t *testing.T) {
		v := freshVar(1)
		v.UpperBounds = []soltype.Type{solStr()}
		sig := solFn([]*soltype.FuncParam{solParam("x", v)}, v)
		require.Equal(t, "<T0 extends string>(x: T0) => T0", renderSol(t, sig))
	})

	// Several bounds would meet to an intersection, a shape the type_system twin
	// never emitted and P4.3 has no golden for, so the binder renders unbounded.
	t.Run("SeveralBoundsRenderNoClause", func(t *testing.T) {
		v := freshVar(1)
		v.UpperBounds = []soltype.Type{solObj(solProp("a", solNum())), solObj(solProp("b", solStr()))}
		sig := solFn([]*soltype.FuncParam{solParam("x", v)}, v)
		require.Equal(t, "<T0>(x: T0) => T0", renderSol(t, sig))
	})

	// A variable free in a nested signature is free in the enclosing one too, and
	// the enclosing signature renders first, so that is where the binder lands.
	t.Run("NestedSignatureBindsAtTheOutermost", func(t *testing.T) {
		arg, ret := freshVar(1), freshVar(2)
		callback := solFn([]*soltype.FuncParam{solParam("a", arg)}, ret)
		sig := solFn([]*soltype.FuncParam{solParam("f", callback), solParam("x", arg)}, ret)
		require.Equal(t, "<T0, T1>(f: (a: T0) => T1, x: T0) => T1", renderSol(t, sig))
	})

	// A declared parameter keeps the name its binder wrote, and a retained one
	// beside it takes the next free name rather than colliding with it.
	t.Run("BesideADeclaredParameter", func(t *testing.T) {
		declared, retained := freshVar(1), freshVar(2)
		sig := solFn([]*soltype.FuncParam{solParam("x", declared), solParam("y", retained)}, retained)
		sig.TypeParams = []*soltype.TypeParam{
			{Name: "T", Var: declared, Default: nil, Constraint: nil},
		}
		require.Equal(t, "<T, T0>(x: T, y: T0) => T0", renderSol(t, sig))
	})

	t.Run("DeclaredNameCollision", func(t *testing.T) {
		declared, retained := freshVar(1), freshVar(2)
		sig := solFn([]*soltype.FuncParam{solParam("x", declared), solParam("y", retained)}, retained)
		sig.TypeParams = []*soltype.TypeParam{
			{Name: "T0", Var: declared, Default: nil, Constraint: nil},
		}
		require.Equal(t, "<T0, T1>(x: T0, y: T1) => T1", renderSol(t, sig))
	})

	// A variable outside every signature has nothing to hang a binder on.
	t.Run("OutsideASignature", func(t *testing.T) {
		require.Equal(t, "{value: unknown}", renderSol(t, solObj(solProp("value", freshVar(1)))))
	})

	// Neither sibling holds every occurrence, so neither may bind it. Naming it on
	// the first would leave the second referencing a name TypeScript cannot see.
	t.Run("SharedBetweenSiblingSignatures", func(t *testing.T) {
		v := freshVar(1)
		obj := solObj(
			solProp("push", solFn([]*soltype.FuncParam{solParam("v", v)}, &soltype.UndefinedType{})),
			solProp("pop", solFn(nil, v)),
		)
		require.Equal(t, "{push: (v: unknown) => undefined, pop: () => unknown}", renderSol(t, obj))
	})

	// Every variable of a batch is named before any bound renders, so a bound
	// naming a sibling of the same batch reads as that sibling's name.
	t.Run("BoundNamesAnotherOfTheSameBatch", func(t *testing.T) {
		bounded, other := freshVar(1), freshVar(2)
		bounded.UpperBounds = []soltype.Type{solObj(solProp("k", other))}
		sig := solFn([]*soltype.FuncParam{solParam("x", bounded), solParam("y", other)}, bounded)
		require.Equal(t, "<T0 extends {k: T1}, T1>(x: T0, y: T1) => T0", renderSol(t, sig))
	})

	// What a call raises and a method's receiver have no TypeScript form, so a
	// variable reachable only through one earns no binder. An unused `<T0>` on a
	// signature that never mentions it reads as a mistake.
	t.Run("VariableOnlyInThrows", func(t *testing.T) {
		sig := solFn([]*soltype.FuncParam{solParam("x", solNum())}, solNum())
		sig.Throws = freshVar(1)
		require.Equal(t, "(x: number) => number", renderSol(t, sig))
	})

	t.Run("VariableOnlyOnTheSelfReceiver", func(t *testing.T) {
		sig := solFn([]*soltype.FuncParam{solParam("x", solNum())}, solNum())
		sig.SelfParam = solParam("self", freshVar(1))
		require.Equal(t, "(x: number) => number", renderSol(t, sig))
	})
}

// TestBuildTypeAnnFromSolFromSource renders types the checker built from real
// Escalier source, so each case reads as the annotation a user would write beside
// the declaration it emits.
//
// It covers every former source can spell.
// TestBuildTypeAnnFromSolFormersSourceCannotReach covers the four it cannot, each
// for a reason stated on the case. The hand-built tests around them cover what no
// declaration produces either: the error sentinel, a skolem, an unresolved
// inference variable, an `import:`-prefixed name from another package, a
// signature-less overload set, and a parameter pattern that binds no name.
func TestBuildTypeAnnFromSolFromSource(t *testing.T) {
	tests := map[string]struct {
		decls    string
		ann      string
		expected string
	}{
		"Number":       {"", "number", "number"},
		"String":       {"", "string", "string"},
		"Boolean":      {"", "boolean", "boolean"},
		"Symbol":       {"", "symbol", "symbol"},
		"BigInt":       {"", "bigint", "bigint"},
		"UniqueSymbol": {"", "unique symbol", "unique symbol"},
		"NumLit":       {"", "5", "5"},
		"StrLit":       {"", `"hi"`, `"hi"`},
		"BoolLit":      {"", "true", "true"},
		"Null":         {"", "null", "null"},
		"Undefined":    {"", "undefined", "undefined"},
		"Never":        {"", "never", "never"},
		"Unknown":      {"", "unknown", "unknown"},

		"Tuple":        {"", "[number, string]", "[number, string]"},
		"Union":        {"", "string | number", "number | string"},
		"Intersection": {"", "{a: number} & {b: string}", "{a: number} & {b: string}"},
		"TemplateLit":  {"", "`on${string}`", "`on${string}`"},
		// TypeScript ships the string intrinsics as generic aliases.
		"StringIntrinsic": {"", "Uppercase<string>", "Uppercase<string>"},
		// Ownership has no TypeScript form, so a `mut` renders as what it wraps.
		"Mutable": {"", "mut {x: number}", "{x: number}"},
		// TypeScript has no complement type. Rendering one as `unknown` reduces the
		// `A & ~B` a narrowed match remainder produces to `A`, dropping the
		// refinement and keeping the type it refines.
		"Negation":          {"", "~string", "unknown"},
		"NegationUnderMeet": {"", "{x: number} & ~string", "{x: number} & unknown"},
		// The exactness operators set and clear a trailing `...` marker, and the
		// marker itself has no TypeScript form either, so all four erase to the
		// shape they mark. M10 owns carrying exactness across.
		"Exact":         {"", "Exact<{x: number}>", "{x: number}"},
		"Inexact":       {"", "Inexact<{x: number}>", "{x: number}"},
		"InexactTuple":  {"", "[number, ...]", "[number]"},
		"InexactObject": {"", "{x: number, ...}", "{x: number}"},
		// A TypeScript overload set is one sibling declaration per arm.
		"OverloadedMethod": {
			"", "{m(self, x: number) -> number, m(self, x: string) -> string}",
			"{m(x: number): number, m(x: string): string}",
		},
		"OverloadedCallable": {
			"", "{(x: number) -> number, (x: string) -> string}",
			"{(x: number): number, (x: string): string}",
		},

		"Keyof": {"type T = {a: number}", "keyof T", "keyof T"},
		"Index": {"type T = {a: number}", `T["a"]`, `T["a"]`},
		"Cond": {
			"", "if number : string { boolean } else { number }",
			"number extends string ? boolean : number",
		},

		// The `?` and `readonly` markers are set from separate fields, so each
		// corner of the pair is pinned. With only the neither and both cases a
		// renderer that swapped the two would still pass.
		"Property":         {"", "{x: number}", "{x: number}"},
		"OptionalOnly":     {"", "{x?: number}", "{x?: number}"},
		"ReadonlyOnly":     {"", "{readonly x: number}", "{readonly x: number}"},
		"OptionalReadonly": {"", "{readonly x?: number}", "{readonly x?: number}"},
		// A name that is not a valid identifier is quoted by the printer.
		"QuotedProperty": {"", `{"a-b": number}`, `{"a-b": number}`},
		// A member keyed off a well-known symbol is stored under a reserved `@@name`
		// spelling, which renders back as a computed key.
		"SymbolProperty": {"", "{[Symbol.iterator]: number}", "{[Symbol.iterator]: number}"},
		// A method's `self` receiver is implicit in TypeScript, so it has no slot.
		"Method": {"", "{m(self, x: number) -> string}", "{m(x: number): string}"},
		// TypeScript carries a method's receiver implicitly and has no throws
		// clause, so neither reaches the emitted signature.
		"MethodSelfAndThrows": {
			"", "{m(self, x: number) -> number throws string}", "{m(x: number): number}",
		},
		"Getter": {"", "{get x(self) -> number}", "{get x(): number}"},
		// TypeScript forbids a return type on a setter.
		"Setter":         {"", "{set x(self, value: number)}", "{set x(value: number)}"},
		"OptionalMethod": {"", "{m?(self) -> string}", "{m?(): string}"},
		"Callable":       {"", "{(x: number) -> string}", "{(x: number): string}"},
		"Constructor":    {"", "{new (x: number) -> string}", "{new (x: number): string}"},
		"ObjectSpread":   {"type A = {x: number}", "{...A, y: string}", "{...A, y: string}"},

		"Func":          {"", "fn (x: number) -> string", "(x: number) => string"},
		"SeveralParams": {"", "fn (x: number, y: string) -> boolean", "(x: number, y: string) => boolean"},
		"GenericFunc":   {"", "fn <T>(x: T) -> T", "<T>(x: T) => T"},
		"ConstrainedAndDefaultedTypeParam": {
			"", `fn <T: string = "a">(x: T) -> T`, `<T extends string = "a">(x: T) => T`,
		},
		// A rest element inside a destructuring pattern, and an object pattern's
		// own rest, which soltype carries in a field of its own rather than among
		// the named fields.
		"DestructuredParams": {
			"", "fn ([a, ...b]: [number, ...], {x, ...other}: {x: number}) -> number",
			"([a, ...b]: [number], {x: x, ...other}: {x: number}) => number",
		},
		"RestParam": {"", "fn (...xs: Array<number>) -> number", "(...xs: Array<number>) => number"},
		"TuplePatParam": {
			"", "fn ([a, b]: [number, number]) -> number",
			"([a, b]: [number, number]) => number",
		},
		"ObjectPatParam": {"", "fn ({x}: {x: number}) -> number", "({x: x}: {x: number}) => number"},
		// What a call raises has no TypeScript form.
		"Throws": {"", "fn () -> number throws string", "() => number"},

		// An index signature is a settled mapped member in `soltype`. TypeScript
		// allows one beside ordinary members, so it needs no intersection split.
		"IndexSignature":   {"", "{[K: string]?: number}", "{[key: string]: number}"},
		"NumericIndexSig":  {"", "{[index: number]?: string}", "{[index: number]: string}"},
		"SymbolIndexSig":   {"", "{[K: symbol]?: string}", "{[sym: symbol]: string}"},
		"ReadonlyIndexSig": {"", "{readonly [K: string]?: number}", "{readonly [key: string]: number}"},
		// `-readonly` removes the marker, and an index signature inherits none, so
		// it emits without one the way an unmarked signature does. Only ModAdd puts
		// `readonly` on the emitted form.
		"MinusReadonlyIndexSig": {"", "{-readonly [K: string]?: number}", "{[key: string]: number}"},
		// A union key set has no single primitive to name the key after.
		"UnionIndexSig":        {"", "{[K: string | number]?: number}", "{[key: number | string]: number}"},
		"IndexSigBesideMember": {"", "{name: string, [K: string]?: number}", "{name: string, [key: string]: number}"},
		// A member that names its key in the value it computes keeps the source's
		// name, since the conventional one would leave `T[K]` dangling.
		"IndexSigKeyInValue": {"type T = {a: number}", "{[K: string]?: T[K]}", "{[K: string]: T[K]}"},

		"Mapped": {"type T = {a: number}", "{[K]: T[K] for K in keyof T}", "{[K in keyof T]: T[K]}"},
		"MappedAddModifiers": {
			"type T = {a: number}", "{readonly [K]?: T[K] for K in keyof T}",
			"{readonly [K in keyof T]?: T[K]}",
		},
		"MappedRemoveModifiers": {
			"type T = {a: number}", "{-readonly [K]-?: T[K] for K in keyof T}",
			"{-readonly [K in keyof T]-?: T[K]}",
		},
		// A mapped member's two modifiers come from separate fields, so one case
		// sets each on its own. With only the cases that set both to the same
		// value a renderer that swapped them would still pass.
		"MappedReadonlyOnly": {
			"type T = {a: number}", "{readonly [K]: T[K] for K in keyof T}",
			"{readonly [K in keyof T]: T[K]}",
		},
		"MappedOptionalOnly": {
			"type T = {a: number}", "{[K]?: T[K] for K in keyof T}",
			"{[K in keyof T]?: T[K]}",
		},
		// A key-remapping expression becomes TypeScript's `as` clause.
		"MappedKeyRemapping": {
			"type T = {a: number}", "{[Uppercase<K>]: T[K] for K in keyof T}",
			"{[K in keyof T as Uppercase<K>]: T[K]}",
		},
		// TypeScript requires a mapped type to be the sole member of its type
		// literal (TS7061), so an object carrying one beside anything else splits
		// into an intersection.
		"MappedBesideMember": {
			"type T = {a: number}", "{name: string, [K]: T[K] for K in keyof T}",
			"{[K in keyof T]: T[K]} & {name: string}",
		},

		// TypeScript's `Promise` takes one type argument, so the error type Escalier
		// tracks second is dropped. The name is the prelude's own registry key, whose
		// `import:` prefix TypeScript cannot write.
		"Promise": {"", "Promise<number, string>", "Promise<number>"},
		// The trim keys off that prefix, so a type the caller declares under one of
		// the same names is a different type and keeps every argument. This stdlib
		// declares no `PromiseLike`, `Generator`, or `AsyncGenerator`, so the trim
		// over a declared one of those is pinned by
		// TestBuildTypeAnnFromSolNominalRefs. The generator rows below reach the
		// built-in former instead, which is the other way one is written.
		"UserGenerator": {
			"declare interface Generator<T, TReturn, TNext, E = never> { next(self) -> T }",
			"Generator<number, string, boolean, string>",
			"Generator<number, string, boolean, string>",
		},
		// A class or alias from another package drops that prefix too. Resolution
		// fills in the arguments the declaration defaults, and every one is
		// emitted: `Iterable<T, TReturn, TNext>` reads back with all three.
		"PreludeAlias": {"", "Iterable<number>", "Iterable<number, undefined, unknown>"},
		"UserAlias":    {"type Box<T> = {value: T}", "Box<number>", "Box<number>"},
		"UserClass":    {"class Point { x: number }", "Point", "Point"},
		// This stdlib declares neither generator, so both reach the built-in
		// GeneratorType former rather than a prelude declaration. TypeScript's
		// `Generator` and `AsyncGenerator` take three type arguments, and the
		// fourth Escalier tracks, what advancing the generator may raise, has no
		// slot. Each renders the same whether or not it was written.
		"Generator": {
			"", "Generator<number, string, boolean>",
			"Generator<number, string, boolean>",
		},
		"GeneratorThrows": {
			"", "Generator<number, string, boolean, string>",
			"Generator<number, string, boolean>",
		},
		"AsyncGenerator": {
			"", "AsyncGenerator<number, string, boolean>",
			"AsyncGenerator<number, string, boolean>",
		},
		"AsyncGeneratorThrows": {
			"", "AsyncGenerator<number, string, boolean, string>",
			"AsyncGenerator<number, string, boolean>",
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			ty, diagnostics, err := solver.ResolveTypeAnnForTest(test.decls, test.ann)
			require.NoError(t, err)
			require.Empty(t, diagnostics)
			require.Equal(t, test.expected, renderSol(t, ty))
		})
	}
}

// TestBuildTypeAnnFromSolFormersSourceCannotReach covers the formers that
// TestBuildTypeAnnFromSolFromSource cannot exercise, each for its own reason,
// stated on the case. Every other former is spellable and is covered there
// against real solver output.
func TestBuildTypeAnnFromSolFormersSourceCannotReach(t *testing.T) {
	tests := map[string]struct {
		ty       soltype.Type
		expected string
		why      string
	}{
		// `{new (n: number) -> string, new () -> string}` is rejected with "An
		// object type may declare at most one `new` signature." Only class
		// inference builds an overloaded constructor, which `Array` needs because
		// its length form and its element-list form mean different things.
		"OverloadedConstructor": {
			solObj(&soltype.ConstructorElem{
				Signatures: []*soltype.FuncType{
					solFn([]*soltype.FuncParam{solParam("n", solNum())}, solStr()),
					solFn(nil, solStr()),
				},
			}),
			"{new (n: number): string, new (): string}",
			"an object type annotation admits one `new` signature",
		},
		// A reference to an `infer` binder is spellable, but only inside the
		// conditional that declares it, and the .d.ts printer has no InferTypeAnn
		// case and panics on the binder. TestBuildTypeAnnFromSolInferBinder pins
		// the binder itself without going through the printer.
		"InferReference": {
			&soltype.InferType{ID: 1, Name: "U", Binder: false},
			"U",
			"its binder cannot be printed",
		},
		// A mapped type's key variable has no form outside the member that binds
		// it. Inside one it renders through the source-driven mapped cases.
		"MappedKeyReference": {
			&soltype.MappedKeyType{ID: 1, Name: "K"},
			"K",
			"no standalone form",
		},
		// `[number, ...P]` over an abstract operand is rejected with "cannot
		// spread P into a tuple", so no annotation puts one in front of the
		// renderer.
		"TupleRestSpread": {
			&soltype.TupleType{
				Elems: []soltype.Type{solNum(), &soltype.RestSpreadType{
					Operand: solAlias("P"),
				}},
				Inexact: false,
			},
			"[number, ...P]",
			"a spread over an abstract operand is rejected",
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			require.Equal(t, test.expected, renderSol(t, test.ty), "reason it is hand-built: %s", test.why)
		})
	}
}

// TestBuildTypeAnnFromSolInferBinder pins the binder form separately, because the
// .d.ts printer has no InferTypeAnn case yet and panics on one.
func TestBuildTypeAnnFromSolInferBinder(t *testing.T) {
	ann := newSolTypeAnnBuilder(solPreludePrefix, nil).
		render(&soltype.InferType{ID: 1, Name: "U", Binder: true})
	infer, ok := ann.(*InferTypeAnn)
	require.True(t, ok, "an infer binder renders as InferTypeAnn, got %T", ann)
	require.Equal(t, "U", infer.Name)
}

// TestBuildTypeAnnFromSolRecursive pins the μ-knot lowering. TypeScript names a
// recursive type through an interface, so an inline annotation renders one level
// of the unfolding with `any` at the binder.
func TestBuildTypeAnnFromSolRecursive(t *testing.T) {
	binder := &soltype.RecursiveVarType{ID: 0, Name: "X0"}
	knot := &soltype.RecursiveType{
		Binder: binder,
		Body:   solObj(solProp("next", binder)),
	}
	require.Equal(t, "{next: any}", renderSol(t, knot))
}

func TestBuildTypeAnnFromSolWithParams(t *testing.T) {
	// A generic class or alias declares its parameters on the declaration, so
	// nothing inside the body binds them.
	v := &soltype.TypeVarType{
		ID: 1, Level: 1, LowerBounds: nil, UpperBounds: nil, Open: false, Widenable: false,
	}
	tp := &soltype.TypeParam{Name: "T", Var: v, Default: nil, Constraint: nil}
	body := solObj(solProp("value", v))

	require.Equal(t, "{value: T}", renderSolWithParams(t, body, []*soltype.TypeParam{tp}))

	// Without the declaration's parameters in scope the same variable is
	// unresolved and falls back to `unknown`.
	require.Equal(t, "{value: unknown}", renderSol(t, body))
}

func TestContainsSelfTypeFromSol(t *testing.T) {
	self := solSelf("Point")

	tests := map[string]struct {
		ty    soltype.Type
		found bool
	}{
		"Bare":           {self, true},
		"UnderObject":    {solObj(solProp("me", self)), true},
		"UnderSignature": {solFn([]*soltype.FuncParam{solParam("x", solNum())}, self), true},
		"UnderUnion":     {&soltype.UnionType{Types: []soltype.Type{solNum(), self}}, true},
		"Absent":         {solObj(solProp("x", solNum())), false},
		// The class `Self` was declared in is an ordinary nominal reference.
		"ClassAlone": {self.Class, false},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			require.Equal(t, test.found, containsSelfTypeFromSol(test.ty))
		})
	}
}

func TestBuildTypeAnnObjKeyFromSol(t *testing.T) {
	t.Run("OrdinaryName", func(t *testing.T) {
		key, ok := buildTypeAnnObjKeyFromSol("x").(*StrLit)
		require.True(t, ok, "an ordinary name renders as a string literal")
		require.Equal(t, "x", key.Value)
	})

	t.Run("WellKnownSymbol", func(t *testing.T) {
		key, ok := buildTypeAnnObjKeyFromSol(soltype.AsyncIteratorSymbolMember).(*ComputedKey)
		require.True(t, ok, "a symbol-keyed member renders as a computed key")
		printer := NewPrinter()
		printer.PrintExpr(key.Expr)
		require.Equal(t, "Symbol.asyncIterator", printer.Output)
	})
}

func TestConvertQualIdentFromSol(t *testing.T) {
	tests := map[string]struct {
		ident    string
		expected string
	}{
		"Bare":   {"x", "x"},
		"Member": {"p.inner", "p.inner"},
		"Nested": {"a.b.c", "a.b.c"},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			printer := NewPrinter()
			printer.PrintTypeAnn(NewTypeOfTypeAnn(convertQualIdentFromSol(test.ident)))
			require.Equal(t, "typeof "+test.expected, printer.Output)
		})
	}
}

// TestBuildTypeAnnFromSolUnnameableParams pins the recovery for a parameter that
// binds no name. TypeScript needs one at every binding position, so a pattern
// that names nothing gets a positional one rather than an empty slot.
func TestBuildTypeAnnFromSolUnnameableParams(t *testing.T) {
	tests := map[string]struct {
		pattern  soltype.Pat
		expected string
	}{
		"Wildcard": {&soltype.WildcardPat{}, "(arg0: number) => number"},
		"Literal":  {&soltype.LitPat{Lit: &soltype.NumLit{Value: 1}}, "(arg0: number) => number"},
		"Null":     {&soltype.NullPat{}, "(arg0: number) => number"},
		// A function type annotation may write a constructor or class pattern in a
		// parameter position, and the solver mirrors it onto the parameter.
		"Extractor": {&soltype.ExtractorPat{
			Name: "Some", Args: []soltype.Pat{&soltype.IdentPat{Name: "v"}},
		}, "(arg0: number) => number"},
		"Instance": {&soltype.InstancePat{
			ClassName: "Point",
			Object:    &soltype.ObjectPat{Fields: nil, Rest: nil},
		}, "(arg0: number) => number"},
		// The solver leaves a pattern it has no counterpart for nil.
		"Absent": {nil, "(arg0: number) => number"},
		// A rest element keeps its `...` when the sub-pattern names nothing.
		// Dropping it would bind one element where the source bound the tail.
		"RestInsideTuple": {&soltype.TuplePat{Elems: []soltype.Pat{
			&soltype.IdentPat{Name: "a"},
			&soltype.RestPat{Pattern: &soltype.WildcardPat{}},
		}}, "([a, ...arg0]: number) => number"},
		// The solver leaves a rest whose sub-pattern it has no counterpart for nil.
		"RestOverAbsentSubPattern": {&soltype.TuplePat{Elems: []soltype.Pat{
			&soltype.RestPat{Pattern: nil},
		}}, "([...arg0]: number) => number"},
		// A sub-pattern that names nothing draws from the same namer as a
		// parameter, so a nested position never reuses an outer name.
		"InsideTuple": {&soltype.TuplePat{Elems: []soltype.Pat{
			&soltype.IdentPat{Name: "a"}, &soltype.WildcardPat{},
		}}, "([a, arg0]: number) => number"},
		"InsideObject": {&soltype.ObjectPat{
			Fields: []*soltype.ObjectPatField{{Name: "x", Value: &soltype.WildcardPat{}}},
			Rest:   &soltype.WildcardPat{},
		}, "({x: arg0, ...arg1}: number) => number"},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			sig := solFn([]*soltype.FuncParam{{
				Pattern: test.pattern, Type: solNum(), Optional: false, Rest: false,
			}}, solNum())
			require.Equal(t, test.expected, renderSol(t, sig))
		})
	}
}

// TestBuildTypeAnnFromSolUnnameableParamsAreDistinct pins that one namer serves a
// whole signature. A name reused across two binding positions would be a
// duplicate-identifier error in the emitted declaration.
func TestBuildTypeAnnFromSolUnnameableParamsAreDistinct(t *testing.T) {
	unnamed := func(ty soltype.Type, pat soltype.Pat) *soltype.FuncParam {
		return &soltype.FuncParam{Pattern: pat, Type: ty, Optional: false, Rest: false}
	}
	sig := solFn([]*soltype.FuncParam{
		unnamed(solNum(), &soltype.WildcardPat{}),
		unnamed(solStr(), &soltype.TuplePat{Elems: []soltype.Pat{&soltype.WildcardPat{}}}),
		unnamed(solBool(), &soltype.WildcardPat{}),
	}, solNum())
	require.Equal(t,
		"(arg0: number, [arg1]: string, arg2: boolean) => number",
		renderSol(t, sig))
}

// TestBuildTypeAnnFromSolEmptyOverloadSets pins what an element carrying no
// signature emits. It describes no callable, so it contributes no member.
func TestBuildTypeAnnFromSolEmptyOverloadSets(t *testing.T) {
	tests := map[string]soltype.ObjTypeElem{
		"Method": &soltype.MethodElem{
			Name: "m", Signatures: nil, Static: false, Optional: false,
		},
		"Constructor": &soltype.ConstructorElem{Signatures: nil},
		"Callable":    &soltype.CallableElem{Signatures: nil},
	}

	for name, elem := range tests {
		t.Run(name, func(t *testing.T) {
			require.Equal(t, "{x: number}", renderSol(t, solObj(solProp("x", solNum()), elem)))
		})
	}
}

// solIndexSig builds the settled mapped member `soltype` stores an index signature
// as: an uncountable key set, no key remapping, no filter, and the `?` marker set.
func solIndexSig(keys, value soltype.Type) *soltype.MappedElem {
	return &soltype.MappedElem{
		Key:      &soltype.MappedKeyType{ID: 1, Name: "K"},
		Keys:     keys,
		Value:    value,
		Name:     nil,
		Check:    nil,
		Extends:  nil,
		Optional: soltype.ModAdd,
		Readonly: soltype.ModNone,
	}
}

// TestRefNameFromSol pins how a registry key becomes a name TypeScript can write.
func TestRefNameFromSol(t *testing.T) {
	tests := map[string]struct {
		qualifiedName string
		expected      string
	}{
		"Bare":           {"Point", "Point"},
		"Namespaced":     {"Geometry.Point", "Geometry.Point"},
		"Imported":       {"import:std:array.Foo", "Foo"},
		"ImportedNested": {"import:npm:a%2Eb.Geometry.Point", "Geometry.Point"},
		// A key that is a package prefix and nothing else names no declaration, so
		// there is no path to strip down to and it is returned whole.
		"PrefixWithoutName": {"import:std:prelude", "import:std:prelude"},
		// A name that merely starts with the marker's letters is not a package key.
		"NotAPackageKey": {"imported.Thing", "imported.Thing"},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			require.Equal(t, test.expected, refNameFromSol(test.qualifiedName))
		})
	}
}

// TestBuildTypeAnnFromSolWithoutPreludePrefix pins what a renderer built without
// a settled prelude does. It trims no reference, rather than trimming by bare
// name and mangling a user's own `Promise`.
func TestBuildTypeAnnFromSolWithoutPreludePrefix(t *testing.T) {
	promise := solClass(solPreludePrefix+".Promise", solNum(), solStr())

	printer := NewPrinter()
	printer.PrintTypeAnn(newSolTypeAnnBuilder("", nil).render(promise))
	require.Equal(t, "Promise<number, string>", printer.Output)

	// The same reference through a builder that knows the prelude is trimmed.
	require.Equal(t, "Promise<number>", renderSol(t, promise))
}

// TestBuildTypeAnnFromSolUnnamedTypeParam pins a type parameter its binder left
// unnamed. Nothing can render a reference to it, so it falls back to `unknown`
// rather than binding the empty name.
func TestBuildTypeAnnFromSolUnnamedTypeParam(t *testing.T) {
	v := &soltype.TypeVarType{
		ID: 1, Level: 1, LowerBounds: nil, UpperBounds: nil, Open: false, Widenable: false,
	}
	unnamed := &soltype.TypeParam{Name: "", Var: v, Default: nil, Constraint: nil}
	require.Equal(t, "{value: unknown}",
		renderSolWithParams(t, solObj(solProp("value", v)), []*soltype.TypeParam{unnamed}))
}

// TestReferencesMappedKey pins the walk that decides whether an index signature
// keeps the key name its source wrote.
func TestReferencesMappedKey(t *testing.T) {
	key := &soltype.MappedKeyType{ID: 7, Name: "K"}
	indexed := &soltype.IndexType{
		Target:  solAlias("T"),
		Index:   &soltype.MappedKeyType{ID: 7, Name: "K"},
		Inexact: false,
	}

	require.True(t, referencesMappedKey(indexed, key))
	require.False(t, referencesMappedKey(solNum(), key))
	// A nested mapped member writing the same name draws its own id, which is what
	// keeps its binding separate from this one.
	require.False(t, referencesMappedKey(&soltype.MappedKeyType{ID: 8, Name: "K"}, key))
	// A mapped member always carries a key, and Value is never nil, so neither nil
	// arises from a well-formed member. The walk answers rather than faulting.
	require.False(t, referencesMappedKey(nil, key))
	require.False(t, referencesMappedKey(indexed, nil))
}

// TestBuildTypeAnnFromSolParity pairs each solver type with the type_system type
// the checker builds for the same source and asserts both render to the same
// TypeScript. It is what makes the port a twin rather than a second renderer that
// happens to work: a case that drifts fails here rather than in a golden .d.ts.
//
// Three pairings need a word.
//
//   - `Self` reaches the type_system renderer as a type reference named Self, which
//     buildDeclStmt rewrites to `this` before rendering. soltype carries Self as a
//     kind of its own, so the rewrite folds into the renderer and the pairing runs
//     the rewrite on the type_system side to compare like with like.
//   - A getter and a setter carry a signature in type_system and the value type
//     alone in soltype, so the pairing builds the signature the getter implies.
//   - An index signature is its own element kind in type_system and a settled
//     mapped member in soltype.
//
// The prelude types that carry a trailing raise parameter are left out. The twin
// trims only a reference whose whole name is the literal `Promise`, where the port
// trims each of the four against the prelude's own package key.
// TestBuildTypeAnnFromSolNominalRefs covers them instead.
func TestBuildTypeAnnFromSolParity(t *testing.T) {
	b := &Builder{tempId: 0, depGraph: nil}
	tsNum := type_sys.NewNumPrimType(nil)
	tsStr := type_sys.NewStrPrimType(nil)
	tsBool := type_sys.NewBoolPrimType(nil)
	tsSelfRef := type_sys.NewTypeRefType(nil, "Self", nil)

	tsFn := func(params []*type_sys.FuncParam, ret type_sys.Type) *type_sys.FuncType {
		return type_sys.NewFuncType(nil, nil, params, ret, nil)
	}
	tsParam := func(name string, ty type_sys.Type) *type_sys.FuncParam {
		return type_sys.NewFuncParam(type_sys.NewIdentPat(name), ty)
	}

	tests := map[string]struct {
		typeSys type_sys.Type
		sol     soltype.Type
	}{
		"Number":    {tsNum, solNum()},
		"String":    {tsStr, solStr()},
		"Boolean":   {tsBool, solBool()},
		"Symbol":    {type_sys.NewSymPrimType(nil), &soltype.PrimType{Prim: soltype.SymPrim}},
		"NumLit":    {type_sys.NewNumLitType(nil, 5), &soltype.LitType{Lit: &soltype.NumLit{Value: 5}}},
		"StrLit":    {type_sys.NewStrLitType(nil, "hi"), &soltype.LitType{Lit: &soltype.StrLit{Value: "hi"}}},
		"BoolLit":   {type_sys.NewBoolLitType(nil, true), &soltype.LitType{Lit: &soltype.BoolLit{Value: true}}},
		"Null":      {type_sys.NewNullType(nil), &soltype.NullType{}},
		"Undefined": {type_sys.NewUndefinedType(nil), &soltype.UndefinedType{}},
		"Never":     {type_sys.NewNeverType(nil), &soltype.NeverType{}},
		"Unknown":   {type_sys.NewUnknownType(nil), &soltype.UnknownType{}},
		"UniqueSymbol": {
			type_sys.NewUniqueSymbolType(nil, 1),
			&soltype.UniqueSymbolType{ID: 1},
		},
		"Self": {
			type_sys.Prune(replaceSelfWithThis(tsSelfRef)),
			solSelf("Point"),
		},
		"NominalRef": {
			type_sys.NewTypeRefType(nil, "Point", nil),
			solClass("Point"),
		},
		"GenericRef": {
			type_sys.NewTypeRefType(nil, "Box", nil, tsNum),
			solAlias("Box", solNum()),
		},
		"Generator": {
			type_sys.NewTypeRefType(nil, "Generator", nil, tsNum, tsStr, tsBool),
			&soltype.GeneratorType{Yield: solNum(), Ret: solStr(), Next: solBool(), Throws: nil, Async: false},
		},
		"Tuple": {
			type_sys.NewTupleType(nil, tsNum, tsStr),
			&soltype.TupleType{Elems: []soltype.Type{solNum(), solStr()}, Inexact: false},
		},
		"Union": {
			type_sys.NewUnionType(nil, tsNum, tsStr),
			&soltype.UnionType{Types: []soltype.Type{solNum(), solStr()}},
		},
		// The members are objects rather than primitives because
		// NewIntersectionType normalizes an empty meet such as `number & string`
		// to `never` before the renderer ever sees it.
		"Intersection": {
			type_sys.NewIntersectionType(nil,
				type_sys.NewObjectType(nil, []type_sys.ObjTypeElem{
					type_sys.NewPropertyElem(type_sys.NewStrKey("x"), tsNum),
				}),
				type_sys.NewObjectType(nil, []type_sys.ObjTypeElem{
					type_sys.NewPropertyElem(type_sys.NewStrKey("y"), tsStr),
				}),
			),
			&soltype.IntersectionType{Types: []soltype.Type{
				solObj(solProp("x", solNum())),
				solObj(solProp("y", solStr())),
			}},
		},
		// A `mut` wrapper in type_system and a reference in soltype both render as
		// the value they wrap.
		"Mutable": {
			type_sys.NewMutType(nil, type_sys.NewObjectType(nil, []type_sys.ObjTypeElem{
				type_sys.NewPropertyElem(type_sys.NewStrKey("x"), tsNum),
			})),
			&soltype.RefType{Mut: true, Lt: nil, Inner: solObj(solProp("x", solNum()))},
		},
		"KeyOf": {
			type_sys.NewKeyOfType(nil, type_sys.NewTypeRefType(nil, "T", nil)),
			&soltype.KeyofType{
				Operand: solAlias("T"),
				Inexact: false,
			},
		},
		"Index": {
			type_sys.NewIndexType(nil, type_sys.NewTypeRefType(nil, "T", nil), type_sys.NewStrLitType(nil, "x")),
			&soltype.IndexType{
				Target:  solAlias("T"),
				Index:   &soltype.LitType{Lit: &soltype.StrLit{Value: "x"}},
				Inexact: false,
			},
		},
		"TypeOf": {
			type_sys.NewTypeOfType(nil, &type_sys.Member{
				Left: type_sys.NewIdent("p"), Right: type_sys.NewIdent("inner"),
			}),
			&soltype.TypeofType{Ident: "p.inner", Ty: solNum()},
		},
		"Cond": {
			type_sys.NewCondType(nil, tsNum, tsStr, tsBool, tsNum),
			&soltype.CondType{
				Check: solNum(), Extends: solStr(), Then: solBool(), Else: solNum(), Distribute: false,
			},
		},
		"TemplateLit": {
			type_sys.NewTemplateLitType(nil,
				[]*type_sys.Quasi{{Value: "on"}, {Value: ""}},
				[]type_sys.Type{tsStr},
			),
			&soltype.TemplateLitType{Quasis: []string{"on", ""}, Interps: []soltype.Type{solStr()}},
		},
		"Func": {
			tsFn([]*type_sys.FuncParam{tsParam("x", tsNum)}, tsStr),
			solFn([]*soltype.FuncParam{solParam("x", solNum())}, solStr()),
		},
		"Property": {
			type_sys.NewObjectType(nil, []type_sys.ObjTypeElem{
				type_sys.NewPropertyElem(type_sys.NewStrKey("x"), tsNum),
			}),
			solObj(solProp("x", solNum())),
		},
		"Method": {
			type_sys.NewObjectType(nil, []type_sys.ObjTypeElem{
				type_sys.NewMethodElem(type_sys.NewStrKey("m"), tsFn([]*type_sys.FuncParam{tsParam("x", tsNum)}, tsStr)),
			}),
			solObj(&soltype.MethodElem{
				Name:       "m",
				Signatures: []*soltype.FuncType{solFn([]*soltype.FuncParam{solParam("x", solNum())}, solStr())},
				Static:     false,
				Optional:   false,
			}),
		},
		"Getter": {
			type_sys.NewObjectType(nil, []type_sys.ObjTypeElem{
				type_sys.NewGetterElem(type_sys.NewStrKey("x"), tsFn(nil, tsNum)),
			}),
			solObj(&soltype.GetterElem{Name: "x", SelfParam: nil, Type: solNum(), Throws: nil}),
		},
		"Setter": {
			type_sys.NewObjectType(nil, []type_sys.ObjTypeElem{
				type_sys.NewSetterElem(type_sys.NewStrKey("x"), tsFn([]*type_sys.FuncParam{tsParam("value", tsNum)}, type_sys.NewVoidType(nil))),
			}),
			solObj(&soltype.SetterElem{Name: "x", SelfParam: nil, Param: solNum(), Throws: nil}),
		},
		"IndexSignature": {
			type_sys.NewObjectType(nil, []type_sys.ObjTypeElem{
				type_sys.NewIndexSignatureElem(tsStr, tsNum, false),
			}),
			solObj(solIndexSig(solStr(), solNum())),
		},
		"ObjectSpread": {
			type_sys.NewObjectType(nil, []type_sys.ObjTypeElem{
				type_sys.NewRestSpreadElem(type_sys.NewTypeRefType(nil, "A", nil)),
			}),
			solObj(&soltype.SpreadElem{
				Type: solAlias("A"),
			}),
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			twin := NewPrinter()
			twin.PrintTypeAnn(b.buildTypeAnn(test.typeSys))
			require.Equal(t, twin.Output, renderSol(t, test.sol))
		})
	}
}
