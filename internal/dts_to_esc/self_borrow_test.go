package dts_to_esc

import (
	"testing"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/printer"
	"github.com/stretchr/testify/require"
)

// TestBorrowSelfInCallbacks covers which callback parameters convert to `&Self`. A parameter
// typed as the class at its own type parameters does, whether TypeScript writes it by name,
// as the mutable `T[]` an array's callbacks take, or as `this`, and whether the callback is
// the parameter's type or sits inside an object type. A `this` parameter, the class at other
// arguments, the class at a method type parameter shadowing the class's, and a direct method
// parameter keep the type they convert to.
func TestBorrowSelfInCallbacks(t *testing.T) {
	t.Parallel()

	lib := parseLib(t, "lib.coll.d.ts", `
interface Coll<T> {
    each(callbackfn: (value: T, coll: Coll<T>) => void): void;
    eachOrNull(callbackfn: ((value: T, coll: Coll<T>) => void) | null): void;
    eachThis(callbackfn: (value: T, coll: this) => void): void;
    eachOther(callbackfn: (coll: Coll<number>) => void): void;
    eachShadowed<T>(callbackfn: (value: T, coll: Coll<T>) => void): void;
    eachInOptions(options: { callbackfn: (coll: Coll<T>) => void }): void;
    on(listener: (this: Coll<T>, ev: number) => void): void;
    merge(other: Coll<T>): void;
}
interface CollConstructor {
    new <T>(): Coll<T>;
    readonly prototype: Coll<any>;
}
declare var Coll: CollConstructor;
interface ReadonlyArray<T> {
    readonly length: number;
}
interface Array<T> {
    length: number;
    forEach(callbackfn: (value: T, index: number, array: T[]) => void): void;
}
interface ArrayConstructor {
    new <T>(): Array<T>;
    readonly prototype: Array<any>;
}
declare var Array: ArrayConstructor;
`)
	mod, err := ConvertBucket(mergeDecls(lib.Module.Statements), nil)
	require.NoError(t, err)

	rootNS, _ := mod.Module.Namespaces.Get("")
	got := map[string]string{}
	for _, decl := range rootNS.Decls {
		class, ok := decl.(*ast.ClassDecl)
		if !ok {
			continue
		}
		for _, elem := range class.Body {
			method, ok := elem.(*ast.MethodElem)
			if !ok || method.Static {
				continue
			}
			printed, err := printer.PrintClassElem(method, printer.DefaultOptions())
			require.NoError(t, err)
			got[class.Name.Name+"."+classElemName(method.Name)] = printed
		}
	}
	require.Equal(t, map[string]string{
		"Array.forEach":      "forEach(&self, callbackfn: fn (value: T, index: number, array: &Self) -> unknown) -> undefined",
		"Coll.each":          "each(&mut self, callbackfn: fn (value: T, coll: &Self) -> unknown) -> undefined",
		"Coll.eachOrNull":    "eachOrNull(&mut self, callbackfn: (fn (value: T, coll: &Self) -> unknown) | null) -> undefined",
		"Coll.eachOther":     "eachOther(&mut self, callbackfn: fn (coll: Coll<number>) -> unknown) -> undefined",
		"Coll.eachShadowed":  "eachShadowed<T>(&mut self, callbackfn: fn (value: T, coll: Coll<T>) -> unknown) -> undefined",
		"Coll.eachInOptions": "eachInOptions(&mut self, options: {\n    callbackfn: fn (coll: &Self) -> unknown\n}) -> undefined",
		"Coll.eachThis":      "eachThis(&mut self, callbackfn: fn (value: T, coll: &Self) -> unknown) -> undefined",
		"Coll.merge":         "merge(&mut self, other: Coll<T>) -> undefined",
		"Coll.on":            "on(&mut self, listener: fn (this: Coll<T>, ev: number) -> unknown) -> undefined",
	}, got)
}
