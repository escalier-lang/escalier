package compiler

import (
	"testing"

	"github.com/escalier-lang/escalier/internal/solver"
	"github.com/gkampitakis/go-snaps/snaps"
	"github.com/stretchr/testify/require"
)

// declarationShapes is one small source per shape the `.d.ts` emitter has to render,
// read by TestEachDeclarationShapeEmitsDefinitions. A source here needs no package on
// disk, so covering a shape costs one entry.
//
// Keep each source to the one shape it names, so a change to the snapshot points at one
// cause.
//
// Two kinds of shape are deliberately absent:
//
//   - A source the solver rejects. The output a recovery path produces says nothing
//     about emission, and shapeDiagnostics enforces this rather than leaving it to a
//     reader.
//   - A shape that emits nothing, which the require.NotEmpty below rejects. An
//     overloaded function is one, #1824.
var declarationShapes = map[string]string{
	"AFunction":               `declare fn f(x: number) -> string`,
	"AFunctionWithABody":      "fn f(x: number) -> string {\n\treturn \"a\"\n}",
	"AGenericFunction":        `declare fn f<T>(x: T) -> T`,
	"AGenericFunctionBound":   `declare fn f<T: {value: number}>(x: T) -> number`,
	"AGenericFunctionDefault": `declare fn f<T = string>(x: T) -> T`,
	// The emitter drops the `?`, #1826, so this emits AFunction's text.
	"AnOptionalParam": `declare fn f(x?: number) -> string`,
	"ARestParam":      `declare fn f(...xs: Array<number>) -> string`,
	// TypeScript has no `throws`, so this emits AFunction's text. It records that the
	// emitter invents no rendering for the clause.
	"AThrowsClause":             `declare fn f(x: number) -> string throws number`,
	"ANoValueReturn":            "class C {\n\tv: number,\n}\nfn bump(c: mut C) {\n\tc.v = c.v + 1\n}",
	"AnAnnotatedNoValueBody":    "class C {\n\tv: number,\n}\nfn set(c: mut C) -> undefined {\n\tc.v = 1\n}",
	"ADeclaredUndefinedReturn":  `declare fn g() -> undefined`,
	"AnAnnotatedUndefinedBody":  "fn h() -> undefined {\n\treturn undefined\n}",
	"AReturnOfUndefined":        "fn k() {\n\treturn undefined\n}",
	"AnAsyncNoValueReturn":      "class C {\n\tv: number,\n}\nasync fn bump(c: mut C) {\n\tc.v = c.v + 1\n}",
	"AnAsyncReturnOfUndefined":  "async fn k() {\n\treturn undefined\n}",
	"AnAsyncValueReturn":        "async fn k() {\n\treturn 5\n}",
	"ANoValueMethod":            "class C {\n\tv: number,\n\tbump(&mut self) { self.v = 1 },\n}",
	"AnAsyncNoValueMethod":      "class C {\n\tv: number,\n\tasync bump(&mut self) { self.v = 1 },\n}",
	"ANoValueFuncValue":         "export val k = fn () {\n\tval x = 1\n}",
	"AnAsyncNoValueFuncValue":   "export val k = async fn () {\n\tval x = 1\n}",
	"ACallbackReturnUndefined":  `declare fn each(cb: fn (x: number) -> undefined) -> number`,
	"ACallbackReturnUnknown":    `declare fn each(cb: fn (x: number) -> unknown) -> number`,
	"APropertyHoldingACallback": `declare val o: {cb: fn (x: number) -> undefined}`,

	"AClass":             "class C {\n\tx: number,\n\ty: string,\n}",
	"AGenericClass":      "class Box<T> {\n\tvalue: T,\n}",
	"AClassParamBound":   "class Holder<T: {value: number}> {\n\tpeer: T,\n}",
	"AClassParamDefault": "declare class Task<T, E = never> {\n\trun(&self) -> T,\n\tfail(&self, r: E) -> never,\n}",
	"AClassMethod":       "class C {\n\tv: number,\n\tread(&self) -> number { return self.v },\n}",
	// The snapshot names the setter's parameter `value` rather than `n`, #1823.
	"AClassGetterAndSetter": "class C {\n\tv: number,\n\tget val(&self) -> number { return self.v },\n\tset val(&mut self, n: number) { self.v = n },\n}",
	"AClassStatic":          "class C {\n\tv: number,\n\tstatic make(n: number) -> number { return n },\n}",
	"AReadonlyField":        "class C {\n\treadonly v: number,\n}",
	// In each of the next three, a member with no return annotation returns an instance of its own class.
	"AMethodConstructingItsClass": "class C {\n\tv: number,\n\tclone(&self) { return C(self.v) },\n}",
	"AStaticFactory":              "class C {\n\tv: number,\n\tstatic make(n: number) { return C(n) },\n}",
	"AGenericMethodConstructingItsClass": "class Box<T> {\n\tv: T,\n" +
		"\tmap<U>(&self, f: fn (x: T) -> U) { return Box(f(self.v)) },\n}",
	"AClassConstructor":  "class C {\n\tv: number,\n\tconstructor(&mut self, v: number) { self.v = v },\n}",
	"AGetterOnly":        "class C {\n\tv: number,\n\tget val(&self) -> number { return self.v },\n}",
	"AStaticGetter":      "class C {\n\tstatic get val() -> number { return 1 },\n}",
	"AnOverloadedMethod": "declare class C {\n\tm(&self, x: number) -> number,\n\tm(&self, x: string) -> string,\n}",
	"ASymbolKeyedMember": "class C {\n\tmsg: string,\n\tstatic [Symbol.customMatcher](subject: C) -> [string] {\n" +
		"\t\treturn [subject.msg]\n\t}\n}",
	"AClassImplements": "interface I {\n\tx: number,\n}\nclass C implements I {\n\tx: number,\n}",
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

	"AnInterface":         "interface I {\n\tx: number,\n}",
	"AnInterfaceIndexSig": "interface I {\n\t[K: string]?: number,\n}",
	"AGenericInterface":   "interface I<T> {\n\tv: T,\n}",

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

// TestEachDeclarationShapeEmitsDefinitions records the `.d.ts` emitted for each source in
// declarationShapes as a snapshot.
func TestEachDeclarationShapeEmitsDefinitions(t *testing.T) {
	for name, src := range declarationShapes {
		t.Run(name, func(t *testing.T) {
			out := CompilePackage(libSources(src), PackageConfig{})
			dts := out.CompUnits["lib/index"].DTS

			require.Empty(t, shapeDiagnostics(out), "internal/solver rejects this shape")
			require.NotEmpty(t, dts, "internal/solver emits nothing for this shape")
			snaps.MatchSnapshot(t, dts)
		})
	}
}

// shapeDiagnostics returns what a compile reported about the source, dropping the
// prelude's own diagnostics, which ride along on every solver run in this package.
func shapeDiagnostics(out CompilerOutput) []string {
	var reported []string
	for _, diagnostic := range out.TypeErrors {
		if pkg, fromPackage := diagnostic.(*solver.PackageInferenceError); fromPackage && pkg.URI == preludePackageURI {
			continue
		}
		reported = append(reported, diagnostic.Message())
	}
	return reported
}

// preludePackageURI is the package every run loads whether or not the source imports it.
// internal/solver keeps the same constant unexported.
const preludePackageURI = "std:prelude"
