package compiler

import (
	"testing"

	"github.com/escalier-lang/escalier/internal/solver"
	"github.com/stretchr/testify/require"
)

// declarationShapes is one small source per shape the `.d.ts` emitters have to render,
// read by TestEachDeclarationShapeEmitsTheSameDefinitions.
//
// The fixture harness compares the same artifact, but only for a fixture the solver can
// check end to end, which holds most of them out. Of 74 fixtures, 29 are held back by a
// check-stage gap, 2 are disabled outright, and 13 more record an expected rejection and
// return before emission. A shape no remaining fixture happens to write is compared
// nowhere, which is how the `void` return divergence #1820 landed. A source here needs
// no package on disk and no fixture that checks cleanly, so covering a shape costs one
// entry.
//
// Keep each source to the one shape it names. A source carrying two shapes fails for
// either, and a skip entry then has to name both causes.
//
// Three kinds of shape are deliberately absent:
//
//   - A source one checker rejects. Comparing the degraded output two different recovery
//     paths produce says nothing about emission. `class D extends B` without an explicit
//     constructor is one, which internal/checker rejects and #1720 covers, and
//     `class C implements I` over an interface is another, which internal/solver rejects
//     and #1825 covers. shapeDiagnostics enforces this rather than leaving it to a reader.
//   - A borrow anywhere in a type. internal/checker panics with `unknown type: <error>`
//     when one reaches emission, #1805, and a panic takes the test binary down rather
//     than failing the one case.
//   - A shape both checkers emit nothing for. An empty result compared against an empty
//     result asserts nothing, which the require.NotEmpty below rejects. An overloaded
//     function is one, #1824.
var declarationShapes = map[string]string{
	"AFunction":               `declare fn f(x: number) -> string`,
	"AFunctionWithABody":      "fn f(x: number) -> string {\n\treturn \"a\"\n}",
	"AGenericFunction":        `declare fn f<T>(x: T) -> T`,
	"AGenericFunctionBound":   `declare fn f<T: {value: number}>(x: T) -> number`,
	"AGenericFunctionDefault": `declare fn f<T = string>(x: T) -> T`,
	// Both emitters drop the `?`, #1826, so this agrees on output a consumer cannot
	// tell from AFunction's. It still asserts they drop it the same way.
	"AnOptionalParam": `declare fn f(x?: number) -> string`,
	"ARestParam":      `declare fn f(...xs: Array<number>) -> string`,
	// TypeScript has no `throws`, so this emits AFunction's text. What it asserts is
	// that neither emitter invents a rendering for the clause.
	"AThrowsClause":             `declare fn f(x: number) -> string throws number`,
	"ANoValueReturn":            "class C {\n\tv: number,\n}\nfn bump(c: mut C) {\n\tc.v = c.v + 1\n}",
	"ADeclaredUndefinedReturn":  `declare fn g() -> undefined`,
	"AnAnnotatedUndefinedBody":  "fn h() -> undefined {\n\treturn undefined\n}",
	"AReturnOfUndefined":        "fn k() {\n\treturn undefined\n}",
	"ACallbackReturnUndefined":  `declare fn each(cb: fn (x: number) -> undefined) -> number`,
	"ACallbackReturnUnknown":    `declare fn each(cb: fn (x: number) -> unknown) -> number`,
	"APropertyHoldingACallback": `declare val o: {cb: fn (x: number) -> undefined}`,

	"AClass":                "class C {\n\tx: number,\n\ty: string,\n}",
	"AGenericClass":         "class Box<T> {\n\tvalue: T,\n}",
	"AClassParamBound":      "class Holder<T: {value: number}> {\n\tpeer: T,\n}",
	"AClassParamDefault":    "declare class Task<T, E = never> {\n\trun(&self) -> T,\n\tfail(&self, r: E) -> never,\n}",
	"AClassMethod":          "class C {\n\tv: number,\n\tread(&self) -> number { return self.v },\n}",
	"AClassGetterAndSetter": "class C {\n\tv: number,\n\tget val(&self) -> number { return self.v },\n\tset val(&mut self, n: number) { self.v = n },\n}",
	"AClassStatic":          "class C {\n\tv: number,\n\tstatic make(n: number) -> number { return n },\n}",
	"AReadonlyField":        "class C {\n\treadonly v: number,\n}",
	"AClassConstructor":     "class C {\n\tv: number,\n\tconstructor(&mut self, v: number) { self.v = v },\n}",
	"AGetterOnly":           "class C {\n\tv: number,\n\tget val(&self) -> number { return self.v },\n}",
	"AStaticGetter":         "class C {\n\tstatic get val() -> number { return 1 },\n}",
	"AnOverloadedMethod":    "declare class C {\n\tm(&self, x: number) -> number,\n\tm(&self, x: string) -> string,\n}",
	"ASymbolKeyedMember": "class C {\n\tmsg: string,\n\tstatic [Symbol.customMatcher](subject: C) -> [string] {\n" +
		"\t\treturn [subject.msg]\n\t}\n}",
	"AClassExtends": "class B {\n\tx: number,\n\tconstructor(&mut self, x: number) { self.x = x },\n}\n" +
		"class D extends B {\n\ty: number,\n\tconstructor(&mut self, x: number, y: number) {\n" +
		"\t\tsuper(x)\n\t\tself.y = y\n\t},\n}",

	"AnEnum":       "enum Color {\n\tRed,\n\tGreen,\n}",
	"AGenericEnum": "enum MyOption<T> {\n\tSome(value: T),\n\tNone,\n}",

	"AnAlias":            `type A = {x: number}`,
	"AGenericAlias":      `type A<T> = {v: T}`,
	"AnAliasBound":       `type A<T: {value: number}> = {v: T}`,
	"AnAliasDefault":     `type A<T = string> = {v: T}`,
	"AUnion":             `type A = number | string | undefined`,
	"AnIntersection":     `type A = {x: number} & {y: string}`,
	"ATuple":             `type A = [number, string]`,
	"AKeyof":             `type A = keyof {x: number, y: string}`,
	"AnIndexedAccess":    `type A = {x: number}["x"]`,
	"AConditional":       `type A<T> = if T : number { "num" } else { "other" }`,
	"AnIndexSignature":   `type A = {[K: string]: number}`,
	"AnOptionalIndexSig": `type A = {[K: string]?: number}`,
	"AMappedType":        `type A<T> = {[P]?: T[P] for P in keyof T}`,
	"AnOptionalProp":     `type A = {x?: number}`,
	"ATypeofType":        "declare val v: number\ntype A = typeof v",

	"AnInterface":       "interface I {\n\tx: number,\n}",
	"AGenericInterface": "interface I<T> {\n\tv: T,\n}",

	"ADeclaredVal":       `declare val n: number`,
	"AnInferredVal":      `export val n = 5`,
	"ALiteralVal":        `declare val s: "hello"`,
	"ANegativeLiteral":   `declare val n: -1`,
	"ABigIntVal":         `declare val b: bigint`,
	"AUniqueSymbol":      `declare val s: unique symbol`,
	"ANeverReturn":       `declare fn boom() -> never`,
	"AnObjectWithMethod": `declare val o: {m: fn (x: number) -> string}`,
	"AnExportedFn":       "export fn f(x: number) -> number {\n\treturn x\n}",

	"ANamespace":       "namespace inner {\n\tdeclare val n: number\n}",
	"ANestedNamespace": "namespace outer {\n\tnamespace inner {\n\t\tdeclare val n: number\n\t}\n}",
}

// shapeSkip is why one shape's definitions still differ, and the ticket the difference
// waits on. A shape can wait on more than one cause, in which case ticket names the one
// to clear first and reason names the rest.
type shapeSkip struct {
	reason string
	ticket string
}

// shapeSkips are the shapes whose emitted definitions still differ between the two
// checkers. The entries are seeded from a run rather than predicted, and a shape that
// starts agreeing fails, so the list burns down the way the fixture harness's emitSkips
// does.
var shapeSkips = map[string]shapeSkip{
	"ADeclaredUndefinedReturn": {
		reason: "a written `-> undefined` return emits `void`, where the twin writes `undefined`",
		ticket: "#1820",
	},
	"AnAnnotatedUndefinedBody": {
		reason: "a written `-> undefined` return emits `void`, where the twin writes `undefined`",
		ticket: "#1820",
	},
	"AReturnOfUndefined": {
		reason: "a `return undefined` body emits `void`, where the twin writes `undefined`",
		ticket: "#1820",
	},
	"ACallbackReturnUndefined": {
		reason: "a callback parameter's `-> undefined` return emits `void`, which widens what the slot accepts",
		ticket: "#1820",
	},
	"AGenericClass": {
		reason: "a class's constructor signature renames its declared parameter to `T0`",
		ticket: "#1773",
	},
	"AClassParamBound": {
		reason: "a bounded class parameter reaches the constructor as `T0 & {value: number}` under a renamed binder",
		ticket: "#1773",
	},
	"AClassParamDefault": {
		reason: "a class parameter no constructor argument mentions coalesces to `never` instead of binding",
		ticket: "#1773",
	},
	"AnEnum": {
		reason: "a variant's value emits as a plain function rather than a constructor object, " +
			"and carries no `[Symbol.customMatcher]` signature; #1784 covers the constructor object",
		ticket: "#1772",
	},
	"AGenericEnum": {
		reason: "a variant's value emits as a plain function rather than a constructor object, " +
			"and carries no `[Symbol.customMatcher]` signature; #1784 covers the constructor object",
		ticket: "#1772",
	},
	"AnOptionalIndexSig": {
		reason: "an index signature over an uncountable key set loses its `?`",
		ticket: "#1775",
	},
	"AClassGetterAndSetter": {
		reason: "a setter's parameter name is lost, so it renders as `value`",
		ticket: "#1823",
	},
	"ASymbolKeyedMember": {
		reason: "a class's static side carries no `[Symbol.customMatcher]` signature",
		ticket: "#1772",
	},
}

// TestEachDeclarationShapeEmitsTheSameDefinitions asserts that both checkers emit the
// same `.d.ts` for each source in declarationShapes.
//
// The checker's output is the expectation rather than a committed golden, for the reason
// the fixture harness's requireSameEmittedOutput gives: what is being asked is whether
// the two agree. Neither output is asserted to be correct TypeScript, so a shape both
// render the same way wrongly passes here. #1804 is one such shape, left out because the
// checker emits a type parameter nothing binds and matching it is not the goal.
func TestEachDeclarationShapeEmitsTheSameDefinitions(t *testing.T) {
	for name := range shapeSkips {
		require.Contains(t, declarationShapes, name,
			"shapeSkips names %q, which declarationShapes does not", name)
	}

	for name, src := range declarationShapes {
		t.Run(name, func(t *testing.T) {
			useChecker(t)
			checkerOut := CompilePackage(libSources(src))
			useSolver(t)
			solverOut := CompilePackage(libSources(src))
			want := checkerOut.CompUnits["lib/index"].DTS
			got := solverOut.CompUnits["lib/index"].DTS

			require.Empty(t, shapeDiagnostics(checkerOut), "internal/checker rejects this shape")
			require.Empty(t, shapeDiagnostics(solverOut), "internal/solver rejects this shape")
			require.NotEmpty(t, want, "internal/checker emits something to compare")
			require.NotEmpty(t, got, "internal/solver emits something to compare")

			if skip, held := shapeSkips[name]; held {
				// Re-compare rather than trusting the list, so clearing a cause forces
				// its shapes back into the comparison.
				require.NotEqual(t, want, got,
					"this shape now agrees; drop it from shapeSkips")
				t.Skipf("%s (%s)", skip.reason, skip.ticket)
			}
			require.Equal(t, want, got, "the two checkers emit different definitions")
		})
	}
}

// shapeDiagnostics returns what a compile reported about the source, dropping the two
// things the solver path reports about its own state instead. The prelude's own
// diagnostics ride along on every solver run in this package, and the codegen-gap marker
// says the solver's emitted output is not yet trusted, which is the premise of this test
// rather than a fault in a shape.
func shapeDiagnostics(out CompilerOutput) []string {
	var reported []string
	for _, diagnostic := range out.TypeErrors {
		if pkg, fromPackage := diagnostic.(*solver.PackageInferenceError); fromPackage && pkg.URI == preludePackageURI {
			continue
		}
		if _, isGap := diagnostic.(*codegenGapError); isGap {
			continue
		}
		reported = append(reported, diagnostic.Message())
	}
	return reported
}

// preludePackageURI is the package every run loads whether or not the source imports it.
// internal/solver keeps the same constant unexported.
const preludePackageURI = "std:prelude"
