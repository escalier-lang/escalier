package interop

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
// converter exports the alias but attachJSDecorator is a no-op for
// TypeDecl (the AST has no Decorators field on type aliases). This test
// asserts that asymmetry. See #664 for the decision on whether to widen
// the AST so type aliases carry @js("...") too. If that lands, update
// this test to assert that the converter emits an @js("Id") decorator
// on the type alias.
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
