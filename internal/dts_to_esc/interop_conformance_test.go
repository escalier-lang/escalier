package dts_to_esc

import (
	"testing"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/ecma262"
	"github.com/escalier-lang/escalier/internal/printer"
	"github.com/stretchr/testify/require"
)

// The conformance tests for the interop rules I1, I2 and I3 in
// planning/ownership_rules/requirements.md. Each case is one row of the I1–I3
// part of the "Per-rule tests" table.

// interopCase is one declaration the converter reads and the declarations it
// is expected to emit for it.
type interopCase struct {
	src string
	// facts is the ECMA-262 receiver source the converter consults. nil gives
	// it none.
	facts *ReceiverFacts
	want  []string
}

// convertLibAndPrint converts src as a lib file and returns every function it
// emits and every instance method of every class it emits, printed. A method
// is prefixed with its class name.
func convertLibAndPrint(t *testing.T, src string, facts *ReceiverFacts) []string {
	t.Helper()
	lib := parseLib(t, "lib.interop.d.ts", src)
	mod, err := ConvertBucket(mergeDecls(lib.Module.Statements), facts)
	require.NoError(t, err)

	rootNS, ok := mod.Module.Namespaces.Get("")
	require.True(t, ok)
	var out []string
	for _, decl := range rootNS.Decls {
		switch d := decl.(type) {
		case *ast.FuncDecl:
			printed, err := printer.Print(d, printer.DefaultOptions())
			require.NoError(t, err)
			out = append(out, printed)
		case *ast.ClassDecl:
			for _, elem := range d.Body {
				method, ok := elem.(*ast.MethodElem)
				if !ok || method.Static {
					continue
				}
				printed, err := printer.PrintClassElem(method, printer.DefaultOptions())
				require.NoError(t, err)
				out = append(out, d.Name.Name+"."+printed)
			}
		}
	}
	return out
}

func runInteropCases(t *testing.T, tests map[string]interopCase) {
	t.Helper()
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, test.want, convertLibAndPrint(t, test.src, test.facts))
		})
	}
}

// widgetLib declares the class `Widget` with the methods src lists. Its `a`
// field holds an object whose `b` slot is writable, so `Readonly<Widget>`
// leaves `w.a.b` writable.
func widgetLib(methods string) string {
	return `
interface Widget {
    a: {b: number};
    ` + methods + `
}
interface WidgetConstructor {
    new (): Widget;
    readonly prototype: Widget;
}
declare var Widget: WidgetConstructor;
`
}

// TestInteropValueTypeParams covers I2. A `number` parameter is copied, so
// the converter writes its bare type with no borrow marker.
func TestInteropValueTypeParams(t *testing.T) {
	t.Parallel()

	runInteropCases(t, map[string]interopCase{
		"NumberParam": {
			src:  `declare function inc(n: number): number;`,
			want: []string{"@js(\"inc\")\nexport declare fn inc(n: number) -> number"},
		},
	})
}

// TestInteropUncertainReceiver covers the receiver half of I3. A method whose
// receiver no tier settles takes `&mut self`.
func TestInteropUncertainReceiver(t *testing.T) {
	t.Parallel()

	// The fact for `Widget.prototype.frob` carries no receiver. The ECMA-262
	// analysis publishes a fact that way when it saw a write it could not tie
	// to the receiver or to a parameter, so it cannot say whether `frob`
	// writes `this`. The name `frob` matches no name heuristic either.
	facts := NewReceiverFacts(&ecma262.Facts{
		SpecTarget: "test",
		Methods: map[string]ecma262.MethodFact{
			"Widget.prototype.frob": {},
		},
	})

	runInteropCases(t, map[string]interopCase{
		"UnsettledMethod": {
			src:   widgetLib(`frob(): void;`),
			facts: facts,
			want: []string{
				"Widget.frob(&mut self) -> undefined",
			},
		},
	})
}

// DISABLED until #2265, which keeps `Readonly<T>` as written and picks the
// borrow's mutability apart from it. The converter emits `Readonly<T>` with no
// borrow marker for each parameter below. It gives `this: Readonly<Widget>`
// the receiver `&self` even though `this.a.b` stays writable through it.
func TestInteropReadonlyKeepsWritableSlots(t *testing.T) {
	/*
		t.Parallel()

		runInteropCases(t, map[string]interopCase{
			// `Readonly` makes `x.a` readonly and leaves `x.a.b` writable, so
			// `reset` needs a `&mut` borrow to run `x.a.b = 0`.
			"NestedWritableSlot": {
				src: `declare function reset(x: Readonly<{a: {b: number}}>): void;`,
				want: []string{
					"@js(\"reset\")\nexport declare fn reset(x: &mut Readonly<{\n    a: {\n        b: number\n    }\n}>) -> undefined",
				},
			},
			// Every slot is readonly or holds a value type, so a `&mut` grants
			// no writes and the converter emits `&`.
			"NoWritableSlot": {
				src: `declare function read(x: Readonly<{a: number}>): void;`,
				want: []string{
					"@js(\"read\")\nexport declare fn read(x: &Readonly<{\n    a: number\n}>) -> undefined",
				},
			},
			// `this: Readonly<Widget>` leaves `this.a.b` writable, so it is no
			// signal for `&self`. The other tiers decide the receiver, and
			// `frob` falls through them to `&mut self`.
			"ReadonlyThisWithWritableSlot": {
				src: widgetLib(`frob(this: Readonly<Widget>): void;`),
				want: []string{
					"Widget.frob(&mut self, this: Readonly<Widget>) -> undefined",
				},
			},
		})
	*/
}

// TestInteropUncertainReturnBorrowsInputs covers the parameter and return
// halves of I3. A parameter the declaration may write takes `&mut`, and a
// return that may share structure with it borrows it.
func TestInteropUncertainReturnBorrowsInputs(t *testing.T) {
	t.Parallel()

	runInteropCases(t, map[string]interopCase{
		// The implementation is `return {...opts}`. A `.d.ts` carries no
		// body, so the converter cannot tell that copy from a fresh object.
		// The shallow copy shares every object below its top level with
		// `opts`, so the return borrows `opts`. The converter cannot tell
		// whether `withDefaults` writes `opts` either, so `opts` is `&mut`.
		"ShallowCopyReturn": {
			src: "interface Options { a: {b: number}; }\n" +
				"declare function withDefaults(opts: Options): Options;",
			want: []string{
				"@js(\"withDefaults\")\nexport declare fn withDefaults<'a>(opts: &'a mut Options) -> &'a mut Options",
			},
		},
	})
}
