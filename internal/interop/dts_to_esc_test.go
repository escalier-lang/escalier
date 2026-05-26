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

// typeAliasSlice exercises convertStandaloneStmt's TypeDecl path. The
// converter exports the alias but attachJSDecorator is a no-op for
// TypeDecl (the AST has no Decorators field on type aliases). This test
// pins that asymmetry — see #664 for the decision on whether to widen
// the AST so type aliases carry @js("...") too, in which case this test
// should be updated to assert the decorator's emission.
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

func printDecoratorArg(t *testing.T, dec *ast.Decorator) string {
	t.Helper()
	require.Len(t, dec.Args, 1)
	lit, ok := dec.Args[0].(*ast.LiteralExpr)
	require.True(t, ok, "decorator arg is a LiteralExpr")
	str, ok := lit.Lit.(*ast.StrLit)
	require.True(t, ok, "decorator arg literal is a StrLit")
	return str.Value
}
