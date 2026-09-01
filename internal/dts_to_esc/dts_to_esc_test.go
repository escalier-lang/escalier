package dts_to_esc

import (
	"context"
	"strings"
	"testing"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/dts_parser"
	"github.com/escalier-lang/escalier/internal/parser"
	"github.com/gkampitakis/go-snaps/snaps"
	"github.com/stretchr/testify/require"
)

// booleanSlice is the trio-idiom slice from lib.es5.d.ts (§5 work item 1).
// The instance interface's JSDoc anchors the fused class's doc comment.
// The constructor interface's own JSDoc is *dropped* per the trio fusion
// contract (the instance side wins); its members' docs do flow through
// onto the static side of the synthesised class.
const booleanSlice = `
/** A boolean wrapper around the primitive boolean type. */
interface Boolean {
    /** Returns the primitive value of the specified object. */
    valueOf(): boolean;
}

/** The static side of Boolean — dropped by trio fusion. */
interface BooleanConstructor {
    /** Constructs a new Boolean wrapper around a value. */
    new (value?: any): Boolean;
    /** Coerces a value to a primitive boolean. */
    <T>(value?: T): boolean;
    /** The Boolean prototype object. */
    readonly prototype: Boolean;
}

declare var Boolean: BooleanConstructor;
`

// jsonNamespaceSlice is a small `declare namespace` slice. JSON in
// lib.es5.d.ts is actually an interface + var (not a namespace) — for
// the namespace-flattening MVP gate (§5 work item 2) we use a synthetic
// JSON-shaped namespace that exercises the same surface.
const jsonNamespaceSlice = `
declare namespace JSON {
    /** Parses a JSON string. */
    function parse(text: string, reviver?: (this: any, key: string, value: any) => any): any;
    /** Serializes a value to JSON. */
    function stringify(value: any, replacer?: (this: any, key: string, value: any) => any, space?: string | number): string;
}
`

func convertSlice(t *testing.T, input string) (*StandaloneModule, string) {
	t.Helper()
	source := &ast.Source{Path: "test.d.ts", Contents: input, ID: 0}
	dtsModule, errs := dts_parser.NewDtsParser(source).ParseModule()
	require.Empty(t, errs, "dts parse errors")

	standalone, err := ConvertToStandaloneModule(dtsModule, nil)
	require.NoError(t, err)

	out, err := RenderStandaloneModule(standalone)
	require.NoError(t, err)
	return standalone, out
}

func TestStandalone_BooleanTrio(t *testing.T) {
	astModule, printed := convertSlice(t, booleanSlice)

	// Gate: exactly one ClassDecl and zero VarDecls — the trio fused.
	rootNS, ok := astModule.Module.Namespaces.Get("")
	require.True(t, ok, "root namespace exists")
	var classCount, varCount, interfaceCount int
	for _, d := range rootNS.Decls {
		switch d.(type) {
		case *ast.ClassDecl:
			classCount++
		case *ast.VarDecl:
			varCount++
		case *ast.InterfaceDecl:
			interfaceCount++
		}
	}
	require.Equal(t, 1, classCount, "exactly one fused ClassDecl")
	require.Equal(t, 0, varCount, "trio var consumed")
	require.Equal(t, 0, interfaceCount, "trio interfaces consumed")

	// Gate: output parses.
	parsedDecls, parseErrs := parser.ParseDecls(context.Background(),
		&ast.Source{Path: "out.esc", Contents: printed, ID: 1})
	require.Empty(t, parseErrs, "printed output parses")
	require.NotEmpty(t, parsedDecls)

	// Gate: full round trip — every elem on the parser-roundtripped
	// class body carries the same Doc that the converter emitted, in
	// the same order. Per-elem parity, not a blanket NotEmpty: if the
	// converter ever emits a synthesized member without a JSDoc, this
	// test should pin doc-parity, not require every member to have a
	// doc.
	var converterClass, parsedClass *ast.ClassDecl
	for _, d := range rootNS.Decls {
		if c, ok := d.(*ast.ClassDecl); ok {
			converterClass = c
			break
		}
	}
	for _, d := range parsedDecls {
		if c, ok := d.(*ast.ClassDecl); ok {
			parsedClass = c
			break
		}
	}
	require.NotNil(t, converterClass, "converter emitted a fused class")
	require.NotNil(t, parsedClass, "parser-roundtripped output contains the fused class")
	require.Equal(t, len(converterClass.Body), len(parsedClass.Body),
		"converter and parser-roundtripped classes have the same number of elems")
	for i, before := range converterClass.Body {
		after := parsedClass.Body[i]
		require.Equal(t, before.Doc(), after.Doc(),
			"elem[%d] (%T) Doc parity broken on parse — #663 regression", i, after)
	}

	// Gate: idempotent — converting and re-printing the parser-roundtripped
	// dts module yields the same string.
	_, printed2 := convertSlice(t, booleanSlice)
	require.Equal(t, printed, printed2, "two conversions produce byte-identical output")

	snaps.MatchSnapshot(t, printed)
}

func TestStandalone_JSONNamespaceFlattened(t *testing.T) {
	astModule, printed := convertSlice(t, jsonNamespaceSlice)

	// Gate: zero nested ast.Namespace entries — flattened to root.
	var nonRootKeys []string
	astModule.Module.Namespaces.Scan(func(key string, _ *ast.Namespace) bool {
		if key != "" {
			nonRootKeys = append(nonRootKeys, key)
		}
		return true
	})
	require.Empty(t, nonRootKeys, "no nested namespaces in output")

	// Gate: printed output contains no `namespace ` keyword block — the
	// flattener leaves no `declare namespace JSON { ... }` in the source.
	require.NotContains(t, printed, "namespace JSON",
		"namespace block should be flattened away")

	// Every emitted decl carries @js("JSON.<name>").
	rootNS, _ := astModule.Module.Namespaces.Get("")
	require.Len(t, rootNS.Decls, 2, "two flattened FuncDecls")
	for _, d := range rootNS.Decls {
		fn, ok := d.(*ast.FuncDecl)
		require.True(t, ok, "namespace member is a FuncDecl")
		require.Len(t, fn.Decorators, 1)
		require.Equal(t, "js", fn.Decorators[0].Name.Name)
		require.True(t, strings.HasPrefix(printDecoratorArg(t, fn.Decorators[0]), "JSON."),
			"@js arg starts with JSON.")
		require.True(t, fn.Export(), "flattened member is exported")
		require.True(t, fn.Declare(), "flattened member is declare")
	}

	// Gate: output parses.
	_, parseErrs := parser.ParseDecls(context.Background(),
		&ast.Source{Path: "out.esc", Contents: printed, ID: 1})
	require.Empty(t, parseErrs, "printed output parses")

	// Gate: idempotent.
	_, printed2 := convertSlice(t, jsonNamespaceSlice)
	require.Equal(t, printed, printed2, "two conversions produce byte-identical output")

	snaps.MatchSnapshot(t, printed)
}

// unclassifiedTrio exercises the fallback in interfaceMemberToClassElem
// where ClassifyMethodByName returns ok=false (the method name hits no
// tier of the name-only classifier). The synthesised receiver must
// default to `mut self` to mirror Classify's tier-7 default — otherwise
// trio-fused methods with novel names would silently get a non-mutating
// receiver, which classifyMember would not.
const unclassifiedTrio = `
interface Frob {
    frobnicate(): void;
}

interface FrobConstructor {
    new (): Frob;
}

declare var Frob: FrobConstructor;
`

func TestStandalone_UnclassifiedMethodDefaultsToMut(t *testing.T) {
	astModule, _ := convertSlice(t, unclassifiedTrio)
	rootNS, ok := astModule.Module.Namespaces.Get("")
	require.True(t, ok)
	require.Len(t, rootNS.Decls, 1)
	cls, ok := rootNS.Decls[0].(*ast.ClassDecl)
	require.True(t, ok, "trio fused to a class")

	var method *ast.MethodElem
	for _, elem := range cls.Body {
		m, isMethod := elem.(*ast.MethodElem)
		if !isMethod {
			continue
		}
		key, isIdent := m.Name.(*ast.IdentExpr)
		if !isIdent || key.Name != "frobnicate" {
			continue
		}
		method = m
		break
	}
	require.NotNil(t, method, "frobnicate method present")
	require.False(t, method.Static, "instance-side method")
	require.NotNil(t, method.Receiver, "instance method has a receiver")
	require.True(t, method.Receiver.Mut,
		"name-classifier miss defaults to mut self (tier-7 parity)")
}

// readonlyThisTrio declares a method whose name the mutating `push` prefix
// claims, under an explicit `this: Readonly<T>`. The two answers disagree, so
// the emitted receiver says which tier decided.
const readonlyThisTrio = `
interface Frob {
    push(this: Readonly<Frob>, item: number): number;
}

interface FrobConstructor {
    new (): Frob;
}

declare var Frob: FrobConstructor;
`

// A `this: Readonly<T>` parameter is a tier-3 author signal, so it outranks
// the name tiers on a class fused from interface signatures the same way it
// does on a `declare class` member.
func TestStandalone_ReadonlyThisParamOutranksTheNameTiers(t *testing.T) {
	astModule, _ := convertSlice(t, readonlyThisTrio)
	rootNS, ok := astModule.Module.Namespaces.Get("")
	require.True(t, ok)
	require.Len(t, rootNS.Decls, 1)
	cls, ok := rootNS.Decls[0].(*ast.ClassDecl)
	require.True(t, ok, "trio fused to a class")

	var method *ast.MethodElem
	for _, elem := range cls.Body {
		if m, isMethod := elem.(*ast.MethodElem); isMethod {
			method = m
			break
		}
	}
	require.NotNil(t, method, "push method present")
	require.NotNil(t, method.Receiver, "instance method has a receiver")
	require.False(t, method.Receiver.Mut,
		"`this: Readonly<T>` outranks the mutating `push` prefix")
}

// qualifiedTrioBinding pins that a `declare var` whose type annotation
// uses a *qualified* name (e.g. `SomeNs.FrobConstructor`) does not
// participate in trio detection — even when a local `FrobConstructor`
// interface exists by the same trailing identifier. The two are
// different types; fusing them would conflate unrelated declarations.
const qualifiedTrioBinding = `
interface Frob {
    valueOf(): boolean;
}

interface FrobConstructor {
    new (): Frob;
}

declare var Frob: SomeNs.FrobConstructor;
`

func TestStandalone_QualifiedBindingSkipsTrio(t *testing.T) {
	astModule, _ := convertSlice(t, qualifiedTrioBinding)
	rootNS, ok := astModule.Module.Namespaces.Get("")
	require.True(t, ok)

	var classCount, varCount, interfaceCount int
	for _, d := range rootNS.Decls {
		switch d.(type) {
		case *ast.ClassDecl:
			classCount++
		case *ast.VarDecl:
			varCount++
		case *ast.InterfaceDecl:
			interfaceCount++
		}
	}
	require.Equal(t, 0, classCount, "qualified binding must not trigger trio fusion")
	require.Equal(t, 2, interfaceCount, "both interfaces pass through unfused")
	require.Equal(t, 1, varCount, "var passes through unfused")
}

// typeAliasSlice exercises convertStandaloneStmt's TypeDecl path. The
// converter exports the alias but attachJSDecorator is intentionally a
// no-op for TypeDecl: per §3.3, @js("...") applies only to decls that
// lower to a JS reference, and a type alias erases at codegen with no
// runtime form to point at (the parser likewise rejects decorators on
// `declare type`). This test is the regression guard for that design
// decision — see #664.
const typeAliasSlice = `
type Id = string;
`

func TestStandalone_TypeAliasExportedNoDecorator(t *testing.T) {
	astModule, printed := convertSlice(t, typeAliasSlice)
	rootNS, ok := astModule.Module.Namespaces.Get("")
	require.True(t, ok)
	require.Len(t, rootNS.Decls, 1)
	td, ok := rootNS.Decls[0].(*ast.TypeDecl)
	require.True(t, ok, "decl is a TypeDecl")
	require.True(t, td.Export(), "type alias is exported")
	require.NotContains(t, printed, "@js(",
		"type aliases lower to no JS reference, so no @js decorator")
}

// singletonSlice exercises the interface+var-singleton idiom (e.g. real
// `lib.es5.d.ts` JSON). The converter must collapse the pair to a flat
// set of top-level decls each carrying @js("<Name>.<member>"), the same
// shape as the `declare namespace` flattening — because the runtime
// surface is one singleton object, not a shared type.
const singletonSlice = `
interface JSON {
    /** Parses a JSON string. */
    parse(text: string): any;
    /** Stringifies a value. */
    stringify(value: any): string;
}

declare var JSON: JSON;
`

func TestStandalone_InterfaceVarSingletonFlattened(t *testing.T) {
	astModule, printed := convertSlice(t, singletonSlice)
	rootNS, ok := astModule.Module.Namespaces.Get("")
	require.True(t, ok)

	var fnDecls []*ast.FuncDecl
	var classCount, varCount, interfaceCount int
	for _, d := range rootNS.Decls {
		switch dd := d.(type) {
		case *ast.FuncDecl:
			fnDecls = append(fnDecls, dd)
		case *ast.ClassDecl:
			classCount++
		case *ast.VarDecl:
			varCount++
		case *ast.InterfaceDecl:
			interfaceCount++
		}
	}
	require.Equal(t, 0, interfaceCount, "singleton interface consumed")
	require.Equal(t, 0, varCount, "singleton var consumed")
	require.Equal(t, 0, classCount, "no class synthesized — this is not a trio")
	require.Len(t, fnDecls, 2, "two FuncDecls flattened out of the interface")

	for _, fn := range fnDecls {
		require.Len(t, fn.Decorators, 1, "@js decorator attached")
		require.Equal(t, "js", fn.Decorators[0].Name.Name)
		arg := printDecoratorArg(t, fn.Decorators[0])
		require.True(t, strings.HasPrefix(arg, "JSON."),
			"@js arg starts with JSON. (got %q)", arg)
		require.True(t, fn.Export(), "exported")
		require.True(t, fn.Declare(), "declare")
	}

	require.Contains(t, printed, "/** Parses a JSON string. */",
		"member JSDoc preserved on flattened FuncDecl")
	require.Contains(t, printed, "/** Stringifies a value. */",
		"member JSDoc preserved on flattened FuncDecl")
	require.NotContains(t, printed, "interface JSON",
		"no surviving interface in output")
}

// symbolKeyedSingletonSlice mirrors the shape lib.es2015.symbol.wellknown.d.ts
// gives `Math`, `JSON`, and `Atomics`: a singleton interface carrying a
// `[Symbol.toStringTag]` member alongside its named ones. The symbol-keyed
// method has no counterpart in the pinned lib set and covers the other
// half of the skip.
const symbolKeyedSingletonSlice = `
interface Math {
    /** Returns the absolute value. */
    abs(x: number): number;
    readonly [Symbol.toStringTag]: string;
    [Symbol.iterator](): number;
}

declare var Math: Math;
`

func TestStandalone_SingletonSkipsSymbolKeyedMember(t *testing.T) {
	astModule, printed := convertSlice(t, symbolKeyedSingletonSlice)
	rootNS, ok := astModule.Module.Namespaces.Get("")
	require.True(t, ok)

	// Flattening names each member at the top level and in its
	// `@js(...)` path, and a computed key has no name to use. Both
	// symbol-keyed members are dropped, leaving `abs` as the only decl.
	require.Len(t, rootNS.Decls, 1)
	fn, ok := rootNS.Decls[0].(*ast.FuncDecl)
	require.True(t, ok, "decl is a FuncDecl")
	require.Equal(t, "abs", fn.Name.Name)
	require.Len(t, fn.Decorators, 1)
	require.Equal(t, "Math.abs", printDecoratorArg(t, fn.Decorators[0]))
	require.NotContains(t, printed, "toStringTag")
	require.NotContains(t, printed, "Symbol.iterator")

	// Both skips are recorded in source order. Whether either one is
	// expected is ReportSingletonKeyDrops's call.
	require.Equal(t, []SingletonMember{
		{Singleton: "Math", Key: "Symbol.toStringTag"},
		{Singleton: "Math", Key: "Symbol.iterator"},
	}, astModule.KeyDrops)
}

// numberKeyedSingletonSlice covers a key that is neither a plain name
// nor a symbol. A numeric key has no plain-name form either, so the
// member is dropped and reported under its literal text.
const numberKeyedSingletonSlice = `
interface Widget {
    resize(n: number): void;
    readonly 0: string;
}

declare var Widget: Widget;
`

func TestStandalone_SingletonNumericKeyDropIsRecorded(t *testing.T) {
	astModule, _ := convertSlice(t, numberKeyedSingletonSlice)
	require.Equal(t, []SingletonMember{
		{Singleton: "Widget", Key: "0"},
	}, astModule.KeyDrops)
}

// literalKeyedSingletonSlice covers the computed-key shapes the dts
// parser accepts that are not a `Symbol.*` member access. A literal in
// the brackets names the same member a bare key would, so the report
// renders it the same way rather than as a node type. The empty string
// key is the one plain key with no name to borrow.
const literalKeyedSingletonSlice = `
interface Widget {
    resize(n: number): void;
    readonly ["ready"]: boolean;
    readonly [0]: string;
    readonly "": string;
}

declare var Widget: Widget;
`

func TestStandalone_SingletonLiteralKeyDropsAreRecorded(t *testing.T) {
	astModule, _ := convertSlice(t, literalKeyedSingletonSlice)
	require.Equal(t, []SingletonMember{
		{Singleton: "Widget", Key: `"ready"`},
		{Singleton: "Widget", Key: "0"},
		{Singleton: "Widget", Key: `""`},
	}, astModule.KeyDrops)
}

// nestedSingletonSlice puts a singleton inside a namespace, the shape
// `declare namespace Intl { ... }` produces. A drop has to reach the
// caller from the recursive conversion, not just from the module root.
const nestedSingletonSlice = `
declare namespace Intl {
    interface Collators {
        compare(a: string, b: string): number;
        [Symbol.dispose](): void;
    }

    var Collators: Collators;
}
`

func TestStandalone_NestedSingletonKeyDropIsRecorded(t *testing.T) {
	astModule, _ := convertSlice(t, nestedSingletonSlice)
	// The singleton is named by its dotted runtime path, so a nested
	// `Collators` cannot be mistaken for a top-level one of the same
	// name.
	require.Equal(t, []SingletonMember{
		{Singleton: "Intl.Collators", Key: "Symbol.dispose"},
	}, astModule.KeyDrops)
}

// accessorKeyedSingletonSlice covers the accessor member kinds. A
// getter and a setter have no top-level lowering whatever their key.
// The named pair is dropped without a note, and only the symbol-keyed
// pair is recorded.
const accessorKeyedSingletonSlice = `
interface Atomics {
    add(x: number): number;
    get size(): number;
    set size(n: number);
    get [Symbol.toStringTag](): string;
    set [Symbol.dispose](fn: () => void);
}

declare var Atomics: Atomics;
`

func TestStandalone_SingletonAccessorKeyDropsAreRecorded(t *testing.T) {
	astModule, _ := convertSlice(t, accessorKeyedSingletonSlice)
	require.Equal(t, []SingletonMember{
		{Singleton: "Atomics", Key: "Symbol.toStringTag"},
		{Singleton: "Atomics", Key: "Symbol.dispose"},
	}, astModule.KeyDrops)
}

// TestSingletonKeyLabel covers every shape the label can take. The
// contract the drop report rests on is that no key renders as the
// empty string, so a member is never named by a blank key.
func TestSingletonKeyLabel(t *testing.T) {
	t.Parallel()
	symbolIterator := &dts_parser.MemberExpr{
		Object: &dts_parser.IdentExpr{Name: "Symbol"},
		Prop:   dts_parser.NewIdent("iterator", ast.Span{}),
	}
	tests := []struct {
		name string
		key  dts_parser.PropertyKey
		want string
	}{
		{
			name: "well-known symbol",
			key:  &dts_parser.ComputedKey{Expr: symbolIterator},
			want: "Symbol.iterator",
		},
		{
			name: "computed string literal",
			key: &dts_parser.ComputedKey{Expr: &dts_parser.LitExpr{
				Lit: &dts_parser.StringLiteral{Value: "ready"},
			}},
			want: `"ready"`,
		},
		{
			name: "computed number literal",
			key: &dts_parser.ComputedKey{Expr: &dts_parser.LitExpr{
				Lit: &dts_parser.NumberLiteral{Value: 2},
			}},
			want: "2",
		},
		{
			// A member access rooted at a literal reduces to no dotted
			// chain. The dts parser cannot produce one today, so this
			// is the placeholder a future expression shape would take.
			name: "computed expression with no dotted form",
			key: &dts_parser.ComputedKey{Expr: &dts_parser.MemberExpr{
				Object: &dts_parser.LitExpr{
					Lit: &dts_parser.StringLiteral{Value: "x"},
				},
				Prop: dts_parser.NewIdent("y", ast.Span{}),
			}},
			want: "<computed *dts_parser.MemberExpr>",
		},
		{
			name: "empty string key",
			key:  &dts_parser.StringLiteral{Value: ""},
			want: `""`,
		},
		{
			name: "numeric key",
			key:  &dts_parser.NumberLiteral{Value: 0},
			want: "0",
		},
		{
			// An identifier only reaches the label when its name is
			// empty, since propertyKeyName returns any other name.
			name: "identifier with no name",
			key:  dts_parser.NewIdent("", ast.Span{}),
			want: "<*dts_parser.Ident>",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tt.want, singletonKeyLabel(tt.key))
		})
	}
}

// sharedInterfaceSlice pins the negative case for the singleton flattener:
// an interface referenced as a type by multiple vars is a shared shape,
// not a singleton structure, and must NOT be flattened. The interface
// survives as an InterfaceDecl carrying its member docs (issue #2 above).
const sharedInterfaceSlice = `
interface Foo {
    /** does a thing */
    bar(): void;
}

declare var x: Foo;
declare var y: Foo;
`

func TestStandalone_SharedInterfaceNotFlattened(t *testing.T) {
	astModule, printed := convertSlice(t, sharedInterfaceSlice)
	rootNS, ok := astModule.Module.Namespaces.Get("")
	require.True(t, ok)

	var interfaceCount, varCount int
	for _, d := range rootNS.Decls {
		switch d.(type) {
		case *ast.InterfaceDecl:
			interfaceCount++
		case *ast.VarDecl:
			varCount++
		}
	}
	require.Equal(t, 1, interfaceCount, "shared interface survives")
	require.Equal(t, 2, varCount, "both vars survive")
	require.Contains(t, printed, "/** does a thing */",
		"interface member JSDoc preserved on the surviving interface")

	// Full round-trip: the printed output parses, and the surviving
	// interface's member carries its Doc on the parsed AST. Pins the
	// objTypeAnnElem half of #663 — without the parser-side fix, leading
	// JSDoc inside an interface body silently lost its Doc field (and
	// in fact failed to parse at all).
	parsedDecls, parseErrs := parser.ParseDecls(context.Background(),
		&ast.Source{Path: "out.esc", Contents: printed, ID: 1})
	require.Empty(t, parseErrs, "printed output parses")
	var parsedIface *ast.InterfaceDecl
	for _, d := range parsedDecls {
		if i, ok := d.(*ast.InterfaceDecl); ok {
			parsedIface = i
			break
		}
	}
	require.NotNil(t, parsedIface, "parsed output contains the interface")
	require.NotEmpty(t, parsedIface.TypeAnn.Elems, "interface has members")
	for i, elem := range parsedIface.TypeAnn.Elems {
		require.NotEmpty(t, elem.Doc(),
			"elem[%d] (%T) lost its JSDoc on parse — #663 regression", i, elem)
	}
}

// multilineDocSlice mirrors the real lib.es5.d.ts JSON shape: an
// interface-and-singleton idiom with overloaded methods, each carrying
// a multi-line JSDoc block with @param tags. Exercises three things
// jointly:
//
//  1. Multi-line JSDoc continuation lines are re-indented to column 1
//     of the destination (here, top-level — column 0 for `/**`, column 1
//     for ` *`). The source's interior indent (4 spaces inside the
//     interface body) must not leak.
//  2. Every overload's own JSDoc survives — overloads do not share or
//     clobber a single doc.
//  3. @param tags pass through verbatim. Their information is the whole
//     point of preserving the doc; stripping them would be wrong.
const multilineDocSlice = `
interface JSON {
    /**
     * Parses text as JSON.
     * @param text A valid JSON string.
     * @param reviver Optional transform.
     */
    parse(text: string, reviver?: (key: string, value: any) => any): any;
    /**
     * Stringifies a value (function replacer).
     * @param value Value to convert.
     * @param replacer Function transform.
     */
    stringify(value: any, replacer?: (key: string, value: any) => any): string;
    /**
     * Stringifies a value (array replacer).
     * @param value Value to convert.
     * @param replacer Property allow-list.
     */
    stringify(value: any, replacer?: (number | string)[] | null): string;
}

declare var JSON: JSON;
`

func TestStandalone_MultilineDocsAndParamsPreserved(t *testing.T) {
	_, printed := convertSlice(t, multilineDocSlice)

	// Gate (3): every @param survives verbatim. Six total: parse has
	// 2, each stringify overload has 2 → 2 + 2 + 2.
	require.Equal(t, 6, strings.Count(printed, "@param "),
		"every @param tag preserved (got output:\n%s)", printed)

	// Gate (2): every overload gets its own doc. The summary lines are
	// distinct, so each must appear exactly once.
	for _, summary := range []string{
		"Parses text as JSON.",
		"Stringifies a value (function replacer).",
		"Stringifies a value (array replacer).",
	} {
		require.Equal(t, 1, strings.Count(printed, summary),
			"each overload carries its own summary line: %q", summary)
	}

	// Gate (1): continuation lines are at column 1 (` *`), not column 5
	// (`     *`) as in the source. The 5-space form would mean the
	// source's interior indent leaked into the hoisted top-level doc.
	require.NotContains(t, printed, "     *",
		"continuation lines must not carry the source's interior indent")
	require.Contains(t, printed, "\n * @param ",
		"@param continuation lines align to column 1 after normalization")
	// And the doc opener sits at column 0 (line-start) — pinning the
	// re-indent target.
	require.Contains(t, printed, "\n/**\n",
		"/** sits at column 0 for top-level decls")
}

func printDecoratorArg(t *testing.T, dec *ast.Decorator) string {
	t.Helper()
	require.Len(t, dec.Args, 1)
	lit, ok := dec.Args[0].(*ast.LiteralExpr)
	require.True(t, ok, "decorator arg is a LiteralExpr")
	str, ok := lit.Lit.(*ast.StrLit)
	require.True(t, ok, "decorator arg literal is a StrLit")
	return str.Value
}

// TypeScript's `void` has no single Escalier counterpart, so the converter reads it by
// position. This pins all three readings on one converted declaration.
func TestStandalone_VoidLowersByPosition(t *testing.T) {
	_, printed := convertSlice(t, `
declare function f(x: void, p: Promise<void>, cb: (v: number) => void): void;
`)
	require.Contains(t, printed,
		"fn f(x: never, p: Promise<undefined>, cb: fn (v: number) -> unknown) -> unknown")
}

// The converter records a runtime path for every declaration it emits,
// including the ones that carry no `@js` decorator because they erase at
// codegen. `InterfaceDecl` and `TypeDecl` have no Decorators field, so the
// side map is the only place their path survives.
func TestStandaloneModuleRecordsPaths(t *testing.T) {
	t.Parallel()

	mod, err := ConvertToStandaloneModule(parseLib(t, "lib.test.d.ts", `
declare namespace Intl {
    interface Collator { compare(x: string): number; }
    function getCanonicalLocales(locales?: string): string[];
}
interface ConcatArray<T> { join(separator?: string): string; }
declare function parseInt(s: string): number;
`).Module, nil)
	require.NoError(t, err)

	paths := map[string]string{}
	mod.Module.Namespaces.Scan(func(_ string, ns *ast.Namespace) bool {
		for _, decl := range ns.Decls {
			paths[declName(decl)] = mod.Paths[decl]
		}
		return true
	})
	require.Equal(t, map[string]string{
		"Collator":            "Intl.Collator",
		"getCanonicalLocales": "Intl.getCanonicalLocales",
		"ConcatArray":         "ConcatArray",
		"parseInt":            "parseInt",
	}, paths)
}

func declName(decl ast.Decl) string {
	switch d := decl.(type) {
	case *ast.InterfaceDecl:
		return d.Name.Name
	case *ast.FuncDecl:
		return d.Name.Name
	case *ast.ClassDecl:
		return d.Name.Name
	}
	return ""
}
