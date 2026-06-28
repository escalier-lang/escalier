package solver

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// --- M6 PR7: if-let / let-else refutable narrowing ---

// TestInferIfLetAndLetElse drives the refutable-binding forms through inferSource.
// Each case either infers a binding type, asserted against want, or reports errors,
// asserted in full against wantErrs. A type-annotated identifier pattern narrows a
// union to one member via the union-super exists rule; subsumption at finalization
// then drops a literal alternate such as 0 into a primitive sibling like number.
func TestInferIfLetAndLetElse(t *testing.T) {
	tests := []struct {
		name string
		src  string
		// binding is the name whose inferred type is checked; defaults to "f".
		binding string
		// want is the expected printed type of binding, checked when wantErrs is nil.
		want string
		// wantErrs, when non-nil, is the full set of error messages expected; the
		// binding type is not checked in that case.
		wantErrs []string
	}{
		{
			// The consequent binds x at number; the alternate's 0 is subsumed into it.
			name: "if-let narrows union to member",
			src: `fn f(u: number | string) {
				return if let x: number = u { x } else { 0 }
			}`,
			want: "fn (u: number | string) -> number",
		},
		{
			// The alternate reads the whole union, so x's narrowing does not reach it.
			name: "if-let alternate sees scrutinee unchanged",
			src: `fn f(u: number | string) {
				return if let x: number = u { 0 } else { u }
			}`,
			want: "fn (u: number | string) -> number | string",
		},
		{
			// A bare identifier pattern carries no annotation, so it binds the union.
			name: "if-let bare ident binds whole scrutinee",
			src: `fn f(u: number | string) {
				return if let x = u { x } else { 0 }
			}`,
			want: "fn (u: number | string) -> number | string",
		},
		{
			// A union annotation picks the matching sub-union.
			name: "if-let narrows to sub-union",
			src: `fn f(u: number | string | boolean) {
				return if let x: number | string = u { x } else { 0 }
			}`,
			want: "fn (u: number | string | boolean) -> number | string",
		},
		{
			// No else contributes Void on the non-matching path.
			name: "if-let without else joins with void",
			src: `fn f(u: number | string) {
				return if let x: number = u { x }
			}`,
			want: "fn (u: number | string) -> number | void",
		},
		{
			// Narrowing introduced a fresh binding; the scrutinee keeps its type.
			name: "if-let leaves scrutinee type unchanged",
			src: `fn f(u: number | string) {
				val r = if let x: number = u { x } else { 0 }
				return u
			}`,
			want: "fn (u: number | string) -> number | string",
		},
		{
			// An annotation that is no member of the union has no branch to pick.
			name: "if-let narrow rejects non-member",
			src: `fn f(u: number | string) {
				return if let x: boolean = u { x } else { 0 }
			}`,
			wantErrs: []string{"2:22-2:29: cannot constrain boolean <: number | string"},
		},
		{
			// `mut {x: number}` picks the matching borrow branch; the write checks
			// against it and the scrutinee keeps its full borrow-union type.
			name: "if-let narrows borrow union for write",
			src: `fn f(p: &mut {x: number}, q: &mut {x: string}) {
				val r = if true { p } else { q }
				if let r2: mut {x: number} = r {
					r2.x = 5
				}
				return r
			}`,
			want: "fn <'a, 'b>(p: &'a mut {x: number}, q: &'b mut {x: string}) -> &'a mut {x: number} | &'b mut {x: string}",
		},
		{
			// r2 binds at mut {x: number}, so a string write to r2.x is rejected.
			name: "if-let narrowed write is type-checked",
			src: `fn f(p: &mut {x: number}, q: &mut {x: string}) {
				val r = if true { p } else { q }
				if let r2: mut {x: number} = r {
					r2.x = "hi"
				}
			}`,
			wantErrs: []string{
				"4:6-4:17: cannot constrain number <: string",
				"4:6-4:17: cannot constrain string <: number",
			},
		},
		{
			// The else diverges, so the body past it reads x at the narrowed type.
			name: "let-else narrows and binds for the rest of the block",
			src: `fn f(u: number | string) {
				val x: number = u else { return "no" }
				return x
			}`,
			want: `fn (u: number | string) -> number | "no"`,
		},
		{
			// The else runs in the enclosing scope, so it reads the outer `fallback`.
			name: "let-else else reads outer binding",
			src: `fn f(u: number | string, fallback: number) {
				val x: number = u else { return fallback }
				return x
			}`,
			want: "fn (u: number | string, fallback: number) -> number",
		},
		{
			// The else binds nothing of the pattern, so referencing x there fails.
			name: "let-else else cannot see the pattern binding",
			src: `fn f(u: number | string) {
				val x: number = u else { return x }
				return x
			}`,
			wantErrs: []string{"2:37-2:38: Unknown identifier: x"},
		},
		{
			// A structural pattern binds its leaves for the rest of the block.
			name: "let-else structural pattern binds leaves",
			src: `fn f(u: {x: number, y: string}) {
				val {x, y} = u else { return [0, ""] }
				return [x, y]
			}`,
			want: `fn (u: {x: number, y: string}) -> [number, string]`,
		},
		{
			// A non-diverging else supplies a fallback. The annotation pins x to number,
			// and the fallback 0 fits, so x is number on both the match and no-match path.
			name: "let-else non-diverging else supplies a fallback",
			src: `fn f(u: number | string) {
				val x: number = u else { 0 }
				return x
			}`,
			want: "fn (u: number | string) -> number",
		},
		{
			// A non-diverging else's fallback must fit the annotated binding type.
			name: "let-else fallback must fit the annotation",
			src: `fn f(u: number | string) {
				val x: number = u else { "no" }
				return x
			}`,
			wantErrs: []string{`2:30-2:34: cannot constrain "no" <: number`},
		},
		{
			// With no annotation the binding's type joins the initializer with the
			// fallback. Subsumption then drops the literal 0 into number.
			name: "let-else unannotated joins init and fallback",
			src: `fn f(u: number | string) {
				val n = u else { 0 }
				return n
			}`,
			want: "fn (u: number | string) -> number | string",
		},
		{
			// A let-else is a body-level form; at module top level it is rejected.
			name:     "let-else at module top level is rejected",
			src:      `val x: number = u else { 0 }`,
			wantErrs: []string{"1:1-1:29: Unsupported: `let`-`else` binding at module top level"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			values, _, errs := inferSource(t, tt.src)
			if tt.wantErrs != nil {
				require.Equal(t, tt.wantErrs, messagesWithSpan(errs))
				return
			}
			require.Empty(t, errs)
			binding := tt.binding
			if binding == "" {
				binding = "f"
			}
			require.Equal(t, tt.want, values[binding])
		})
	}
}
