package solver

import (
	"fmt"
	"testing"

	"github.com/escalier-lang/escalier/internal/soltype"
	"github.com/stretchr/testify/require"
)

// nonReturningMsg builds the diagnostic checkCanReturn reports, so a test spells out the return type
// it expects to see rather than the whole sentence around it. span is the blamed range, name is what
// the source called the function, and ret is the rendered return type. An empty name is a function
// expression, which the message calls "this function".
func nonReturningMsg(span, name, ret string) string {
	subject := "this function"
	if name != "" {
		subject = "`" + name + "`"
	}
	return fmt.Sprintf("%s: %s returns `%s`, which no finite value inhabits, so a call to it never "+
		"returns; give the recursion a base case, make the recursive property optional, or defer the "+
		"recursive call behind a function or a Promise", span, subject, ret)
}

// TestNonReturningRecursionReported pins the diagnostic on the shapes that cannot return. In each
// source every path to a leaf of the returned value runs through the recursive call again, so the
// call overflows the stack instead of producing a value.
func TestNonReturningRecursionReported(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want []string
	}{
		{
			// The issue's headline case. Building `tail` calls `cons` again before the object
			// literal is finished.
			name: "recursion through an object property",
			src:  `fn cons(x: number) { return {head: x, tail: cons(x)} }`,
			want: []string{nonReturningMsg("1:4-1:8", "cons",
				"{head: number, tail: μX0.{head: number, tail: X0}}")},
		},
		{
			name: "recursion through a tuple element",
			src:  `fn f() { return [f()] }`,
			want: []string{nonReturningMsg("1:4-1:5", "f", "[μX0.[X0]]")},
		},
		{
			// Mutual recursion draws one diagnostic per participant. coalesce closes the cycle at
			// whichever binding is being rendered, so each one sees the knot in its own return type.
			name: "mutual recursion reports both participants",
			src: `
				fn expr() { return {kind: "call", args: [stmt()]} }
				fn stmt() { return {kind: "exprStmt", inner: expr()} }
			`,
			want: []string{
				nonReturningMsg("3:8-3:12", "stmt",
					`{kind: "exprStmt", inner: μX0.{kind: "call", args: [{kind: "exprStmt", inner: X0}]}}`),
				nonReturningMsg("2:8-2:12", "expr",
					`{kind: "call", args: [μX0.{kind: "exprStmt", inner: {kind: "call", args: [X0]}}]}`),
			},
		},
		{
			// A caller of a non-returning function cannot return either, and its own return type
			// carries the same knot, so it draws its own diagnostic beside the one on `f`.
			name: "a caller of a non-returning function is reported too",
			src: `
				fn f() { return {next: f()} }
				fn g() { return f() }
			`,
			want: []string{
				nonReturningMsg("2:8-2:9", "f", "{next: μX0.{next: X0}}"),
				nonReturningMsg("3:8-3:9", "g", "{next: μX0.{next: X0}}"),
			},
		},
		{
			// The blame is per function body, not per binding. `f` itself returns fine, since its
			// `go` property holds a lambda, and the lambda inside it is what cannot return. A
			// function expression has no name, so the message says "this function".
			name: "a lambda that cannot return is blamed on the lambda",
			src:  `fn f() { return {go: fn () { return {next: f().go()} }} }`,
			want: []string{nonReturningMsg("1:22-1:55", "", "{next: μX0.{next: X0}}")},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, errs := inferSource(t, tt.src)
			require.Equal(t, tt.want, messagesWithSpan(errs))
		})
	}
}

// TestNonReturningRecursionAccepts pins the shapes checkCanReturn must leave alone. Each one
// either returns a finite value or fails to return for a reason this check is not about.
func TestNonReturningRecursionAccepts(t *testing.T) {
	tests := []struct {
		name string
		src  string
	}{
		{
			// A base case gives one union arm that reaches a leaf without the binder, so the value
			// stops there.
			name: "a base case terminates the recursion",
			src: `
				declare fn done(n: number) -> boolean
				declare fn dec(n: number) -> number
				fn count(n: number) {
					return if done(n) { {head: n, tail: undefined} } else { {head: n, tail: count(dec(n))} }
				}
			`,
		},
		{
			// A thunk's body does not run while the value around it is built, so the stream is
			// finite until something calls `rest`.
			name: "a thunk defers the recursive call",
			src: `
				declare fn inc(n: number) -> number
				fn stream(n: number) { return {value: n, rest: fn () { return stream(inc(n)) }} }
			`,
		},
		{
			// The issue's event-loop case. `serve` is `fn () -> Promise<μX0.Promise<X0>>`, a real
			// μ-knot, and every `await` hands control back to the event loop, so it is not a hang.
			name: "a Promise defers the recursive call",
			src: `
				declare fn accept() -> Promise<number>
				declare fn handle(n: number)
				async fn serve() {
					val req = await accept()
					handle(req)
					return serve()
				}
			`,
		},
		{
			// A generator body does not run at the call either, so a `gen fn` that returns itself
			// yields a value per `next()` rather than recursing while the generator is obtained.
			name: "a generator defers the recursive call",
			src: `
				gen fn walk(n: number) {
					yield n
					return walk(n)
				}
			`,
		},
		{
			// The related-but-different shape the issue leaves open. This recursion emits no
			// structure, so no knot forms and the return type is `never` rather than a knot with no
			// base case. It is a tight infinite loop and wants its own diagnostic.
			name: "a recursion that emits no structure is out of scope",
			src:  `fn serve() { return serve() }`,
		},
		{
			// The statement-position twin of the case above, whose return type is `void`.
			name: "a discarded recursive call is out of scope",
			src:  `fn serve() { serve() }`,
		},
		{
			// A function that only throws returns `never`, which nothing inhabits, but it diverges
			// rather than recursing. Rejecting it would flag every not-yet-implemented stub.
			name: "a throwing stub is not a recursion",
			src:  `fn todo() -> never throws string { throw "todo" }`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, errs := inferSource(t, tt.src)
			require.Empty(t, Messages(errs))
		})
	}
}

// TestFinitelyInhabited pins the predicate directly on hand-built types, which is the only way to
// reach the shapes no inference path mints. An object literal never produces an optional property,
// and no inferred function returns a borrow of a knot, so those two arms are covered here rather
// than through source.
func TestFinitelyInhabited(t *testing.T) {
	// selfProp builds `μX0.{<name>: X0}` with the property optional or required, the two readings
	// the object arm has to tell apart.
	selfProp := func(name string, optional bool) soltype.Type {
		return muKnot(0, "X0", func(ref *soltype.RecursiveVarType) soltype.Type {
			return exactObj(&soltype.PropertyElem{Name: name, Type: ref, Optional: optional})
		})
	}
	tests := []struct {
		name string
		t    soltype.Type
		want bool
	}{
		{
			name: "a knot whose only property is required has no finite value",
			t:    selfProp("tail", false),
			want: false,
		},
		{
			name: "an optional property lets the value stop",
			t:    selfProp("tail", true),
			want: true,
		},
		{
			name: "a required property carrying a knot with no base case has no finite value",
			t:    exactObj(propElem("tail", selfProp("tail", false))),
			want: false,
		},
		{
			// The base case as a union arm: one arm reaches a leaf without the binder.
			name: "a union arm without the binder lets the value stop",
			t: muKnot(0, "X0", func(ref *soltype.RecursiveVarType) soltype.Type {
				return &soltype.UnionType{Types: []soltype.Type{
					&soltype.UndefinedType{},
					exactObj(propElem("tail", ref)),
				}}
			}),
			want: true,
		},
		{
			// Every arm of the union comes back to the binder, so no arm terminates.
			name: "a union whose every arm reaches the binder has no finite value",
			t: muKnot(0, "X0", func(ref *soltype.RecursiveVarType) soltype.Type {
				return &soltype.UnionType{Types: []soltype.Type{
					exactObj(propElem("head", ref)),
					exactObj(propElem("tail", ref)),
				}}
			}),
			want: false,
		},
		{
			name: "a thunk's return is not built with the value around it",
			t: muKnot(0, "X0", func(ref *soltype.RecursiveVarType) soltype.Type {
				return exactObj(propElem("rest", &soltype.FuncType{Ret: ref}))
			}),
			want: true,
		},
		{
			name: "a Promise's payload is not built with the value around it",
			t: muKnot(0, "X0", func(ref *soltype.RecursiveVarType) soltype.Type {
				return &soltype.PromiseType{Inner: ref}
			}),
			want: true,
		},
		{
			// An array type is inhabited by the empty array, so a recursive element type imposes
			// nothing on the value.
			name: "an array of the binder is inhabited by the empty array",
			t: muKnot(0, "X0", func(ref *soltype.RecursiveVarType) soltype.Type {
				return &soltype.ArrayType{Elem: ref}
			}),
			want: true,
		},
		{
			// A tuple has a fixed arity, so its positions are all built.
			name: "a one-element tuple of the binder has no finite value",
			t: muKnot(0, "X0", func(ref *soltype.RecursiveVarType) soltype.Type {
				return &soltype.TupleType{Elems: []soltype.Type{ref}}
			}),
			want: false,
		},
		{
			// A borrow points at a value someone else built, so it terminates exactly when its
			// pointee does.
			name: "a borrow of a knot with no base case has no finite value",
			t:    &soltype.RefType{Inner: selfProp("tail", false).(*soltype.RecursiveType)},
			want: false,
		},
		{
			// `never` has no values at all, but nothing about it says a recursion runs forever, and
			// a throwing stub returns it. The predicate reads it as inhabited so such a stub is not
			// flagged.
			name: "never reads as inhabited",
			t:    &soltype.NeverType{},
			want: true,
		},
		{
			// An alias reference would have to be expanded to be decided, so it reads as inhabited.
			name: "an alias reference reads as inhabited",
			t:    &soltype.AliasType{Name: "List"},
			want: true,
		},
		{
			name: "an object with no members is inhabited",
			t:    exactObj(),
			want: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, finitelyInhabited(tt.t))
		})
	}
}
