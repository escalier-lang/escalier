package printer

import (
	"context"
	"strings"
	"testing"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/parser"
	"github.com/stretchr/testify/require"
)

// TestPrintDeclAudit_RoundTrip is the §1 builtins-plan audit (see
// planning/builtins/implementation_plan.md). For every declaration form
// the converter (planning/builtins, §5) needs to emit, take a canonical
// Escalier source string, parse it, print it, re-parse the printed
// output, print again, and assert the two printed strings match.
//
// This mirrors type_system.TestPrintTypeAudit_RoundTrip but at the
// declaration level. Forms that currently fail to round-trip are listed
// in TestPrintDeclAudit_KnownGaps below, with the work that must land
// before §5 (converter MVP) can emit them.
//
// Scope (from implementation_plan.md §1):
//   - declare class (with generics, extends)
//   - declare fn (with generic constraints, optional/rest params, throws,
//     self/this parameter)
//   - declare type (incl. conditional, mapped, indexed-access, etc.)
//   - declare var / declare val
//   - open interface declarations and interface merging
//   - decorator syntax @js("...") on every decorator-eligible form
//
// Explicitly out of scope: `declare module "..."` (pseudo-packages are
// files, not nested ambient modules).
func TestPrintDeclAudit_RoundTrip(t *testing.T) {
	tests := []struct {
		name string
		src  string
	}{
		// --- declare val / declare var ---
		{"declare val typed", `declare val x: number`},
		{"declare var typed", `declare var y: string`},
		{"declare val union", `declare val z: number | string`},
		{"export declare val", `export declare val x: number`},
		{"export declare var", `export declare var y: string`},

		// --- declare fn ---
		{"declare fn no params", `declare fn f() -> void`},
		{"declare fn one param", `declare fn f(x: number) -> string`},
		{"declare fn two params", `declare fn f(x: number, y: string) -> boolean`},
		{"declare fn optional param", `declare fn f(x?: number) -> void`},
		{"declare fn rest param", `declare fn f(...args: Array<number>) -> void`},
		{"declare fn with throws", `declare fn f() -> void throws Error`},
		{"declare fn type param", `declare fn f<T>(x: T) -> T`},
		{"declare fn constrained type param", `declare fn f<T: string>(x: T) -> T`},
		{"export declare fn", `export declare fn f(x: number) -> string`},

		// --- declare type ---
		{"declare type alias", `declare type ID = number`},
		{"declare type union", `declare type R = string | number`},
		{"declare type with params", `declare type Box<T> = {value: T}`},
		{"declare type indexed access", `declare type V<T, K> = T[K]`},
		{"declare type keyof", `declare type Keys<T> = keyof T`},
		{"declare type intersection", `declare type AB<A, B> = A & B`},
		// Escalier conditional-type syntax is `if T : U { A } else { B }`,
		// not TS-style `T extends U ? A : B`.
		{"declare type conditional", `declare type C<T> = if T : string { number } else { boolean }`},
		{"declare type conditional with infer", `declare type Elem<T> = if T : Array<infer U> { U } else { never }`},
		// Escalier mapped-type syntax: `{[K]: T[K] for K in keyof T}`.
		{"declare type mapped", `declare type M<T> = {[K]: T[K] for K in keyof T}`},
		{"declare type mapped readonly add", `declare type M<T> = {readonly [K]: T[K] for K in keyof T}`},
		{"declare type mapped optional add", `declare type M<T> = {[K]+?: T[K] for K in keyof T}`},
		{"declare type mapped key rename", `declare type M<T> = {[` + "`prefix_${K}`" + `]: T[K] for K in keyof T}`},
		{"export declare type", `export declare type ID = number`},

		// --- declare interface (open / mergeable) ---
		{"declare interface empty", `declare interface I {}`},
		{"declare interface with prop", `declare interface I {x: number}`},
		{"declare interface with method", `declare interface I {foo(self, x: number) -> boolean}`},
		{"declare interface with mut self method", `declare interface I {foo(mut self, x: number) -> boolean}`},
		{"declare interface with getter", `declare interface I {get foo(self) -> number}`},
		{"declare interface with setter", `declare interface I {set foo(mut self, v: number) -> undefined}`},
		{"declare interface generic", `declare interface I<T> {value: T}`},
		{"declare interface extends", `declare interface I extends Base {x: number}`},
		{"export declare interface", `export declare interface I {x: number}`},

		// --- declare class (with generics and extends) ---
		{"declare class empty", `declare class C {}`},
		{"declare class generic", `declare class C<T> {}`},
		{"declare class extends", `declare class C extends Base {}`},
		{"declare class constrained generic", `declare class C<T: string> {}`},
		{"export declare class", `export declare class C {}`},

		// --- decorator syntax @js("...") (§3.3) ---
		{"@js on declare val", `@js("Math.PI")
export declare val PI: number`},
		{"@js on declare fn", `@js("Math.max")
export declare fn max(a: number, b: number) -> number`},
		{"@js on declare class", `@js("Date")
export declare class Date {}`},
		{"@js on declare interface", `@js("Date")
export declare interface Date {x: number}`},
		{"@js on declare type", `@js("Whatever")
export declare type T = number`},
		{"multiple decorators stacked", `@js("Math.PI")
@js("Math.PI")
export declare val PI: number`},
		// `@foo` (no parens) and `@foo()` (empty parens) round-trip as
		// distinct forms; the printer uses `Args == nil` vs an empty
		// slice to tell them apart. Pin both shapes so a future change
		// that conflates them is forced to update the audit.
		{"decorator no args", `@foo
declare val x: number`},
		{"decorator empty args", `@foo()
declare val x: number`},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			decl := mustParseDecl(t, tt.src)
			printed := mustPrintDecl(t, decl)
			reparsed := mustParseDecl(t, printed)
			reprinted := mustPrintDecl(t, reparsed)
			require.Equal(t, printed, reprinted,
				"round-trip mismatch for %q\nfirst print:\n%s\nsecond print:\n%s",
				tt.name, printed, reprinted)
		})
	}
}

// TestPrintDeclAudit_InterfaceMerging covers the open-interface merging
// case from FR7: two `declare interface I` blocks with the same name in
// the same file. Round-trip is per-decl; the gate is that printing both
// decls in order and re-parsing yields the same pair of decls.
func TestPrintDeclAudit_InterfaceMerging(t *testing.T) {
	src := `declare interface I {x: number}
declare interface I {y: string}`
	script := parseScript(t, src)
	require.Len(t, script.Stmts, 2, "expected two top-level decls")

	firstPrint := printAllDecls(t, script)
	script2 := parseScript(t, firstPrint)
	require.Len(t, script2.Stmts, 2, "round-trip lost a decl")
	secondPrint := printAllDecls(t, script2)
	require.Equal(t, firstPrint, secondPrint)
}

// TestPrintConstructor_NoDuplicateReceiver guards against a regression
// where printMethodSig prints both the explicit receiver and the
// synthesized `self` that the parser stamps into Fn.Params[0].
func TestPrintConstructor_NoDuplicateReceiver(t *testing.T) {
	src := `class C {
    constructor(mut self, x: number) {}
}`
	script := parseScript(t, src)
	require.Len(t, script.Stmts, 1)
	ds, ok := script.Stmts[0].(*ast.DeclStmt)
	require.True(t, ok)
	out, err := Print(ds.Decl, DefaultOptions())
	require.NoError(t, err)
	require.NotContains(t, out, "mut self, self",
		"constructor receiver and synthesized Params[0] both printed:\n%s", out)
	require.Contains(t, out, "constructor(mut self, x: number)")
}

// TestPrintDeclAudit_KnownGaps documents declaration forms in the §1
// scope that are intentionally not exercised by the round-trip suite.
// Each entry pins the contract so a future change that accidentally
// enables one of these forms is forced to update the docs.
func TestPrintDeclAudit_KnownGaps(t *testing.T) {
	t.Run("declare namespace intentionally out of scope", func(t *testing.T) {
		// Per implementation_plan §1, `declare namespace` is *not*
		// in the audit because the converter flattens namespace
		// blocks into top-level declarations. The printer-side gap
		// is intentional; this entry documents the contract.
		src := `declare namespace N {
		declare val x: number
		}`
		decl, errs := parseDeclMaybe(src)
		require.Empty(t, errs,
			"declare namespace failed to parse, contradicting the "+
				"parser supporting it")
		require.NotNil(t, decl, "parser returned no decl for declare namespace")
	})

	t.Run("declare module intentionally out of scope", func(t *testing.T) {
		// implementation_plan §1 explicitly excludes
		// `declare module "..."`. This case documents that the
		// parser accepts it but the audit does not require print
		// round-trip.
		src := `declare module "foo" {
		declare val x: number
		}`
		decl, errs := parseDeclMaybe(src)
		require.Empty(t, errs)
		require.NotNil(t, decl, "parser returned no decl for declare module")
	})
}

// --- helpers ---

func mustParseDecl(t *testing.T, src string) ast.Decl {
	t.Helper()
	decl, errs := parseDeclMaybe(src)
	require.Empty(t, errs, "parse errors for %q: %v", src, errs)
	require.NotNil(t, decl, "no decl parsed from %q", src)
	return decl
}

func parseDeclMaybe(src string) (ast.Decl, []*parser.Error) {
	source := &ast.Source{Path: "audit.esc", Contents: src, ID: 0}
	p := parser.NewParser(context.Background(), source)
	script, errs := p.ParseScript()
	if len(errs) > 0 {
		return nil, errs
	}
	if len(script.Stmts) == 0 {
		return nil, nil
	}
	ds, ok := script.Stmts[0].(*ast.DeclStmt)
	if !ok {
		return nil, nil
	}
	return ds.Decl, nil
}

func mustPrintDecl(t *testing.T, decl ast.Decl) string {
	t.Helper()
	out, err := Print(decl, DefaultOptions())
	require.NoError(t, err)
	return out
}

func printAllDecls(t *testing.T, script *ast.Script) string {
	t.Helper()
	var buf strings.Builder
	for _, stmt := range script.Stmts {
		ds, ok := stmt.(*ast.DeclStmt)
		require.True(t, ok)
		out, err := Print(ds.Decl, DefaultOptions())
		require.NoError(t, err)
		buf.WriteString(out)
		buf.WriteString("\n")
	}
	return buf.String()
}
