package dts_to_esc

import (
	"context"
	"strings"
	"testing"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/dts_parser"
	"github.com/escalier-lang/escalier/internal/parser"
	"github.com/escalier-lang/escalier/internal/printer"
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

	standalone, err := ConvertToStandaloneModule(dtsModule)
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
`).Module)
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

// arrayShorthandTrio is ArrayConstructor's shape: `new` signatures that
// return `T[]` rather than a TypeReference naming the instance. `T[]` is
// `Array<T>` written in shorthand, so the trio is a trio.
const arrayShorthandTrio = `
interface Array<T> {
    length: number;
}
interface ArrayConstructor {
    new (arrayLength?: number): any[];
    new <T>(...items: T[]): T[];
    isArray(arg: any): boolean;
}
declare var Array: ArrayConstructor;
`

// TestStandalone_ArrayShorthandTrio covers the return shape that keeps a
// trio from fusing. `Array`, `BigInt`, and `Symbol` are the three the
// pinned lib set writes without a `new (): Foo` signature, and `Array`
// is the one that is still constructible: `ArrayConstructor` returns
// `any[]` and `T[]`. Reading only a TypeReference leaves the three
// halves unfused, so `std:array` emits an interface, a constructor
// interface, and a var where a class belongs.
func TestStandalone_ArrayShorthandTrio(t *testing.T) {
	astModule, _ := convertSlice(t, arrayShorthandTrio)

	rootNS, ok := astModule.Module.Namespaces.Get("")
	require.True(t, ok, "root namespace exists")

	var classes []*ast.ClassDecl
	var varCount, interfaceCount int
	for _, d := range rootNS.Decls {
		switch decl := d.(type) {
		case *ast.ClassDecl:
			classes = append(classes, decl)
		case *ast.VarDecl:
			varCount++
		case *ast.InterfaceDecl:
			interfaceCount++
		}
	}
	require.Len(t, classes, 1, "exactly one fused ClassDecl")
	require.Equal(t, "Array", classes[0].Name.Name)
	require.Equal(t, 0, varCount, "trio var consumed")
	require.Equal(t, 0, interfaceCount, "trio interfaces consumed")
}

// immutableOwnerTrio is a String-shaped trio carrying the two member
// kinds the owner rule has to separate: instance methods, which cannot
// mutate a primitive, and a constructor, which initializes the object it
// builds.
const immutableOwnerTrio = `
interface String {
    strike(): string;
    normalize(form?: string): string;
    readonly length: number;
}
interface StringConstructor {
    new (value?: any): String;
    fromCharCode(...codes: number[]): string;
}
declare var String: StringConstructor;
`

// TestStandalone_ImmutableOwnerReceivers pins the receivers on a
// primitive wrapper. `strike` matches no heuristic prefix and `normalize`
// matches a mutating one, so both reach `mut self` on the name-only
// tiers. Neither can mutate a string. The constructor keeps `mut self`
// because it initializes the object it builds, and it reaches the class
// through constructSignatureToCtorElem rather than the method path.
func TestStandalone_ImmutableOwnerReceivers(t *testing.T) {
	astModule, printed := convertSlice(t, immutableOwnerTrio)
	require.NotEmpty(t, printed)

	rootNS, ok := astModule.Module.Namespaces.Get("")
	require.True(t, ok)
	var cls *ast.ClassDecl
	for _, d := range rootNS.Decls {
		if cd, ok := d.(*ast.ClassDecl); ok && cd.Name.Name == "String" {
			cls = cd
		}
	}
	require.NotNil(t, cls, "String should be trio-fused into a class")

	got := map[string]bool{} // member name → receiver is mut
	for _, elem := range cls.Body {
		switch e := elem.(type) {
		case *ast.MethodElem:
			if e.Static || e.Receiver == nil {
				continue
			}
			got[classElemName(e.Name)] = e.Receiver.Mut
		case *ast.ConstructorElem:
			require.NotNil(t, e.Receiver)
			got["constructor"] = e.Receiver.Mut
		}
	}

	require.Equal(t, map[string]bool{
		"strike":      false,
		"normalize":   false,
		"constructor": true,
	}, got)
}

// raiseParamTrio is Promise's shape: a trio whose instance members
// return the type being declared, alongside a static that returns it
// too.
const raiseParamTrio = `
interface Promise<T> {
    then<R>(onfulfilled?: (value: T) => R): Promise<R>;
}
interface PromiseConstructor {
    new <T>(executor: (resolve: (value: T) => void) => void): Promise<T>;
    readonly prototype: Promise<any>;
    resolve<T>(value: T): Promise<T>;
}
declare var Promise: PromiseConstructor;
`

// TestStandalone_RaiseParamOnAFusedClass covers where the raise
// parameter lands. Escalier's `Promise` takes one where the TypeScript
// declaration has no slot for it, and a trio fuses into a class, so the
// TypeDecl and InterfaceDecl paths in decl.go never see it.
//
// It threads through the instance members and stops at the statics and
// the prototype: a static has no binding for the class's own type
// parameters, so naming `E` there would reference nothing.
func TestStandalone_RaiseParamOnAFusedClass(t *testing.T) {
	astModule, _ := convertSlice(t, raiseParamTrio)

	rootNS, ok := astModule.Module.Namespaces.Get("")
	require.True(t, ok)
	var cls *ast.ClassDecl
	for _, d := range rootNS.Decls {
		if cd, ok := d.(*ast.ClassDecl); ok && cd.Name.Name == "Promise" {
			cls = cd
		}
	}
	require.NotNil(t, cls)

	printed, err := printer.Print(cls, printer.DefaultOptions())
	require.NoError(t, err)
	snaps.MatchInlineSnapshot(t, printed, snaps.Inline(`@js("Promise")
export declare class Promise<T, E = never> {
    then<R>(mut self, onfulfilled?: fn (value: T) -> R) -> Promise<R, E>,
    constructor(mut self, executor: fn (resolve: fn (value: T) -> unknown) -> unknown),
    static readonly prototype: Promise<any>,
    static resolve<T>(value: T) -> Promise<T>
}`))
}

// An interface carrying `new (...)` describes the constructor side of
// a class: the object you call `new` on, not the instances it builds.
// Recognising a class needs a separate instance interface too, which is
// what the trio idiom supplies through `interface Foo` beside
// `interface FooConstructor`. On its own such an interface is preserved
// as written, never flattened and never fused.
//
// The flattening half is the one that used to go wrong. flattenSingleton
// gives each member its own top-level decl and has no such form for a
// `new`, so it dropped the construct signature and, for an interface
// whose only member is a `new`, emitted nothing at all.
func TestStandalone_InterfaceWithConstructSignatureIsPreserved(t *testing.T) {
	tests := []struct {
		name       string
		input      string
		interfaces int
		vars       int
		contains   []string
	}{
		{
			// Verbatim from lib.scripthost.d.ts. It is the only
			// interface in TypeScript's shipped lib files that carries
			// a construct signature alongside a `declare var` of the
			// same name. Its sole member is the `new`, so before
			// construct signatures excluded an interface from singleton
			// detection both declarations vanished.
			name: "ActiveXObject",
			input: `
interface ActiveXObject {
    new (s: string): any;
}
declare var ActiveXObject: ActiveXObject;
`,
			interfaces: 1,
			vars:       1,
			contains: []string{
				"new (s: string) -> any",
				"export declare var ActiveXObject: ActiveXObject",
			},
		},
		{
			// Verbatim from lib.dom.d.ts. Other declarations reference
			// it structurally, and it has no `declare var`, so it is a
			// constructor-shaped type rather than a class.
			name: "CustomElementConstructor",
			input: `
interface CustomElementConstructor {
    new (...params: any[]): HTMLElement;
}
`,
			interfaces: 1,
			contains:   []string{"new (...params: Array<any>) -> HTMLElement"},
		},
		{
			// A `new` returning the interface's own name says either
			// that constructing yields the constructor object again or
			// that it yields an instance which is itself constructible.
			// Neither describes a class, so this is preserved like any
			// other constructor-side interface.
			name: "construct signature returning the interface itself",
			input: `
interface Foo {
    new (s: string): Foo;
    bar(): void;
}
declare var Foo: Foo;
`,
			interfaces: 1,
			vars:       1,
			contains:   []string{"new (s: string) -> Foo", "bar() -> unknown"},
		},
		{
			// TypeScript merges the two declarations, and a map keyed
			// by name keeps only the last. The construct signature sits
			// on the first, so reading one declaration per name misses
			// it and flattens `bar` to a top-level decl.
			//
			// The `new` returns `HTMLElement` rather than `Foo` so that
			// detectSingletons' own reference count does not decline
			// the pair for an unrelated reason. This case has to fail
			// when the construct-signature scan reads a single
			// declaration, not merely when it is absent.
			name: "construct signature on a merged declaration",
			input: `
interface Foo {
    new (s: string): HTMLElement;
}
interface Foo {
    bar(): void;
}
declare var Foo: Foo;
`,
			interfaces: 2,
			vars:       1,
			contains:   []string{"new (s: string) -> HTMLElement"},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			astModule, printed := convertSlice(t, test.input)
			rootNS, ok := astModule.Module.Namespaces.Get("")
			require.True(t, ok, "root namespace exists")

			var classes, interfaces, vars, funcs int
			for _, d := range rootNS.Decls {
				switch d.(type) {
				case *ast.ClassDecl:
					classes++
				case *ast.InterfaceDecl:
					interfaces++
				case *ast.VarDecl:
					vars++
				case *ast.FuncDecl:
					funcs++
				}
			}
			require.Equal(t, 0, classes, "no class is synthesized")
			require.Equal(t, 0, funcs, "no member is flattened to a top-level decl")
			require.Equal(t, test.interfaces, interfaces, "InterfaceDecl count")
			require.Equal(t, test.vars, vars, "VarDecl count")

			for _, want := range test.contains {
				require.Contains(t, printed, want)
			}

			parsedDecls, parseErrs := parser.ParseDecls(context.Background(),
				&ast.Source{Path: "out.esc", Contents: printed, ID: 1})
			require.Empty(t, parseErrs, "printed output parses")
			require.NotEmpty(t, parsedDecls)
		})
	}
}

// Recognition of the trio idiom reads the three names and the var's
// type annotation, never the constructor interface's members. These are
// the two pinned-lib shapes that depend on it: a `FooConstructor` with
// no `new` at all, and one whose `new` returns an array type rather
// than a reference to the instance name.
func TestStandalone_TrioFusesWhateverTheConstructorDeclares(t *testing.T) {
	tests := []struct {
		name string
		// input is the trio, trimmed from the pinned lib file named in
		// each case's comment.
		input string
		// ctors is the number of ConstructorElem members the fused
		// class should carry.
		ctors    int
		statics  []string
		instance []string
	}{
		{
			// lib.esnext.iterator.d.ts gives IteratorConstructor
			// statics and no `new`. A class with no constructor is what
			// makes constructing it unrepresentable rather than merely
			// discouraged.
			name: "constructor interface with no new",
			input: `
interface Iterator<T> {
    next(): T;
}

interface IteratorConstructor {
    readonly prototype: Iterator<any>;
    from<T>(value: T): Iterator<T>;
}

declare var Iterator: IteratorConstructor;
`,
			ctors:    0,
			statics:  []string{"static readonly prototype: Iterator<any>", "static from<T>(value: T) -> Iterator<T>"},
			instance: []string{"next(mut self) -> T"},
		},
		{
			// lib.es5.d.ts. `any[]` and `T[]` parse as an array type,
			// not as a TypeReference named `Array`, so a rule reading
			// the return type rejects the one name the
			// planning/interop_mutability/ workstream is about.
			name: "construct signatures returning an array type",
			input: `
interface Array<T> {
    push(...items: T[]): number;
    readonly length: number;
}

interface ArrayConstructor {
    new (arrayLength?: number): any[];
    new <T>(arrayLength: number): T[];
    isArray(arg: any): boolean;
    readonly prototype: any[];
}

declare var Array: ArrayConstructor;
`,
			ctors:    2,
			statics:  []string{"static isArray(arg: any) -> boolean"},
			instance: []string{"push(mut self, ...items: Array<T>) -> number"},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			astModule, printed := convertSlice(t, test.input)
			rootNS, ok := astModule.Module.Namespaces.Get("")
			require.True(t, ok, "root namespace exists")

			var classes []*ast.ClassDecl
			var interfaces, vars int
			for _, d := range rootNS.Decls {
				switch dd := d.(type) {
				case *ast.ClassDecl:
					classes = append(classes, dd)
				case *ast.InterfaceDecl:
					interfaces++
				case *ast.VarDecl:
					vars++
				}
			}
			require.Len(t, classes, 1, "the trio fused into one ClassDecl")
			require.Equal(t, 0, interfaces, "both trio interfaces consumed")
			require.Equal(t, 0, vars, "the trio var consumed")

			ctors := 0
			for _, elem := range classes[0].Body {
				if _, ok := elem.(*ast.ConstructorElem); ok {
					ctors++
				}
			}
			require.Equal(t, test.ctors, ctors, "ConstructorElem count")

			for _, want := range test.statics {
				require.Contains(t, printed, want)
			}
			for _, want := range test.instance {
				require.Contains(t, printed, want)
			}

			parsedDecls, parseErrs := parser.ParseDecls(context.Background(),
				&ast.Source{Path: "out.esc", Contents: printed, ID: 1})
			require.Empty(t, parseErrs, "printed output parses")
			require.NotEmpty(t, parsedDecls)
		})
	}
}

// A constructor interface with a call signature and no `new` describes
// something callable and not constructible. fuseTrio has no class elem
// for a call signature, so the fused class could be neither called nor
// constructed. The trio passes through instead, keeping the call
// signature on the interface, until #1412 gives a class somewhere to
// hold one.
//
// `SymbolConstructor` below is verbatim from lib.es2015.symbol.d.ts.
// `BigIntConstructor` is the same shape and the only other one in the
// pinned lib set.
func TestStandalone_TrioNotFusedWhenTheConstructorIsOnlyCallable(t *testing.T) {
	const slice = `
interface Symbol {
    toString(): string;
}

interface SymbolConstructor {
    readonly prototype: Symbol;
    (description?: string | number): symbol;
    for(key: string): symbol;
}

declare var Symbol: SymbolConstructor;
`
	astModule, printed := convertSlice(t, slice)
	rootNS, ok := astModule.Module.Namespaces.Get("")
	require.True(t, ok, "root namespace exists")

	var classes, interfaces, vars int
	for _, d := range rootNS.Decls {
		switch d.(type) {
		case *ast.ClassDecl:
			classes++
		case *ast.InterfaceDecl:
			interfaces++
		case *ast.VarDecl:
			vars++
		}
	}
	require.Equal(t, 0, classes, "no class synthesized")
	require.Equal(t, 2, interfaces, "both interfaces survive")
	require.Equal(t, 1, vars, "the binding survives")

	require.Contains(t, printed, "fn (description?: string | number) -> symbol",
		"the call signature survives, which is the whole point of holding back")

	parsedDecls, parseErrs := parser.ParseDecls(context.Background(),
		&ast.Source{Path: "out.esc", Contents: printed, ID: 1})
	require.Empty(t, parseErrs, "printed output parses")
	require.NotEmpty(t, parsedDecls)
}

// ConvertToStandaloneModule converts every declaration a file holds,
// with each `declare global { ... }` block lifted so its contents are
// converted as if written beside it. Whether those declarations are
// actually global is a question only the global tree asks, and
// PartitionLibWithOverlay answers it through globalStatements. Most
// real `.d.ts` files are modules, so a single-file conversion that
// emitted only their blocks would emit almost nothing.
//
// The snapshot carries what the counts and substring checks used to
// assert separately: the file's own interface converts alongside the
// block's, the JSDoc inside the block survives, and a lifted decl is
// addressed by its bare name with no block prefix.
func TestStandalone_DeclareGlobalIsLifted(t *testing.T) {
	const slice = `
export {};

interface Gadget {
    tick(): void;
}

declare global {
    /** A global interface. */
    interface Widget {
        spin(): void;
    }

    var widgetCount: number;
}
`
	_, printed := convertSlice(t, slice)
	snaps.MatchInlineSnapshot(t, printed, snaps.Inline(`export declare interface Gadget {
    tick() -> unknown
}

/** A global interface. */
export declare interface Widget {
    spin() -> unknown
}

@js("widgetCount")
export declare var widgetCount: number
`))

	parsedDecls, parseErrs := parser.ParseDecls(context.Background(),
		&ast.Source{Path: "out.esc", Contents: printed, ID: 1})
	require.Empty(t, parseErrs, "printed output parses")
	require.Len(t, parsedDecls, 3)
}

// A trio whose instance name is already declared by a `declare class`
// is left alone, so the converter does not add a second class beside
// the one the source spells out. `lib.esnext.iterator.d.ts` writes
// `Iterator` that way: an abstract class at module scope, and
// `IteratorConstructor` plus the binding inside its `declare global`
// block.
//
// The snapshot pins current behaviour, not the right answer. TypeScript
// merges `interface Foo` into `class Foo`, so what this input means is
// one class carrying `next` and `peek` as instance members and `from`
// as a static. mergeDecls folds an interface into an interface and not
// into a class, so the pair stays split whether or not the trio fuses,
// and declining only keeps the split from getting wider. #1430 covers
// the merge and takes this guard out with it.
func TestStandalone_TrioDeclinedWhenNameIsAlreadyAClass(t *testing.T) {
	const slice = `
declare abstract class Foo {
    next(): string;
}

interface Foo {
    peek(): string;
}

interface FooConstructor {
    from(s: string): Foo;
}

declare var Foo: FooConstructor;
`
	_, printed := convertSlice(t, slice)
	snaps.MatchInlineSnapshot(t, printed, snaps.Inline(`@js("Foo")
export declare class Foo {
    next(mut self) -> string
}

export declare interface Foo {
    peek() -> string
}

export declare interface FooConstructor {
    from(s: string) -> Foo
}

@js("Foo")
export declare var Foo: FooConstructor
`))

	parsedDecls, parseErrs := parser.ParseDecls(context.Background(),
		&ast.Source{Path: "out.esc", Contents: printed, ID: 1})
	require.Empty(t, parseErrs, "printed output parses")
	require.Len(t, parsedDecls, 4)
}
