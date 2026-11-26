package solver

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// objectPatternBorrowDecls declares the types each case in this file destructures. Box and
// Nest are aliases and Holder is a class, so a case reaches the fields the alias body or the
// class body declares rather than an object type written inline.
const objectPatternBorrowDecls = `
	type Flat = {x: number}
	type Box = {inner: Flat, n: number}
	type Nest = {box: Box, m: number}
	type Settings = {theme: "light" | "dark", inner: Flat}
	class Holder { inner: Flat, n: number }
	declare fn keep(f: Flat) -> undefined
`

// TestInferObjectPatternBorrowsThroughBorrowedSource covers what each leaf of an object
// pattern binds when the source is a borrow. A field of an object type binds as a borrow at
// the source's lifetime and with the source's mutability, the same as a member read such as
// `b.inner`. A value-type field such as `n: number` or `theme: "light" | "dark"` binds as an
// owned copy. Each function returns its leaves in a tuple, so the inferred return type shows
// the type each leaf bound at.
func TestInferObjectPatternBorrowsThroughBorrowedSource(t *testing.T) {
	tests := map[string]struct {
		src  string
		want string
	}{
		"ValShorthandImm": {
			src: `
				fn f(b: &Box) {
					val {inner, n} = b
					return [inner, n]
				}
			`,
			want: "fn <'a>(b: &'a Box) -> [&'a Flat, number]",
		},
		"ValShorthandMut": {
			src: `
				fn f(b: &mut Box) {
					val {inner, n} = b
					return [inner, n]
				}
			`,
			want: "fn <'a>(b: &'a mut Box) -> [&'a mut Flat, number]",
		},
		"ValKeyValueImm": {
			src: `
				fn f(b: &Box) {
					val {inner: i, n: k} = b
					return [i, k]
				}
			`,
			want: "fn <'a>(b: &'a Box) -> [&'a Flat, number]",
		},
		"ValKeyValueMut": {
			src: `
				fn f(b: &mut Box) {
					val {inner: i, n: k} = b
					return [i, k]
				}
			`,
			want: "fn <'a>(b: &'a mut Box) -> [&'a mut Flat, number]",
		},
		"ValNestedImm": {
			src: `
				fn f(b: &Nest) {
					val {box: {inner, n}, m} = b
					return [inner, n, m]
				}
			`,
			want: "fn <'a>(b: &'a Nest) -> [&'a Flat, number, number]",
		},
		"ValNestedMut": {
			src: `
				fn f(b: &mut Nest) {
					val {box: {inner, n}, m} = b
					return [inner, n, m]
				}
			`,
			want: "fn <'a>(b: &'a mut Nest) -> [&'a mut Flat, number, number]",
		},
		"ValRestImm": {
			src: `
				fn f(b: &Box) {
					val {n, ...rest} = b
					return [n, rest]
				}
			`,
			want: "fn <'a>(b: &'a Box) -> [number, &'a {inner: Flat}]",
		},
		"ValRestMut": {
			src: `
				fn f(b: &mut Box) {
					val {n, ...rest} = b
					return [n, rest]
				}
			`,
			want: "fn <'a>(b: &'a mut Box) -> [number, &'a mut {inner: Flat}]",
		},
		"ValValueTypeUnionFieldImm": {
			src: `
				fn f(s: &Settings) {
					val {theme, inner} = s
					return [theme, inner]
				}
			`,
			want: "fn <'a>(s: &'a Settings) -> [\"light\" | \"dark\", &'a Flat]",
		},
		"ValValueTypeUnionFieldMut": {
			src: `
				fn f(s: &mut Settings) {
					val {theme, inner} = s
					return [theme, inner]
				}
			`,
			want: "fn <'a>(s: &'a mut Settings) -> [\"light\" | \"dark\", &'a mut Flat]",
		},
		"ValInlineValueTypeUnionFieldImm": {
			src: `
				fn f(s: &{theme: "light" | "dark", inner: Flat}) {
					val {theme, inner} = s
					return [theme, inner]
				}
			`,
			want: "fn <'a>(s: &'a {theme: \"light\" | \"dark\", inner: Flat}) -> [\"light\" | \"dark\", &'a Flat]",
		},
		"TupleValueTypeUnionElemImm": {
			src: `
				fn f(s: &["light" | "dark", Flat]) {
					val [theme, inner] = s
					return [theme, inner]
				}
			`,
			want: "fn <'a>(s: &'a [\"light\" | \"dark\", Flat]) -> [\"light\" | \"dark\", &'a Flat]",
		},
		"ValClassInstanceImm": {
			src: `
				fn f(h: &Holder) {
					val {inner, n} = h
					return [inner, n]
				}
			`,
			want: "fn <'a>(h: &'a Holder) -> [&'a Flat, number]",
		},
		"ValClassInstanceMut": {
			src: `
				fn f(h: &mut Holder) {
					val {inner, n} = h
					return [inner, n]
				}
			`,
			want: "fn <'a>(h: &'a mut Holder) -> [&'a mut Flat, number]",
		},
		"ParamShorthandImm": {
			src:  `fn f({inner, n}: &Box) { return [inner, n] }`,
			want: "fn <'a>({inner, n}: &'a Box) -> [&'a Flat, number]",
		},
		"MatchShorthandImm": {
			src:  `fn f(b: &Box) { return match b { {inner, n} => [inner, n] } }`,
			want: "fn <'a>(b: &'a Box) -> [&'a Flat, number]",
		},
		"MatchShorthandMut": {
			src:  `fn f(b: &mut Box) { return match b { {inner, n} => [inner, n] } }`,
			want: "fn <'a>(b: &'a mut Box) -> [&'a mut Flat, number]",
		},
		"MatchKeyValueImm": {
			src:  `fn f(b: &Box) { return match b { {inner: i, n: k} => [i, k] } }`,
			want: "fn <'a>(b: &'a Box) -> [&'a Flat, number]",
		},
		"MatchKeyValueMut": {
			src:  `fn f(b: &mut Box) { return match b { {inner: i, n: k} => [i, k] } }`,
			want: "fn <'a>(b: &'a mut Box) -> [&'a mut Flat, number]",
		},
		"MatchNestedMut": {
			src:  `fn f(b: &mut Nest) { return match b { {box: {inner}, m} => [inner, m] } }`,
			want: "fn <'a>(b: &'a mut Nest) -> [&'a mut Flat, number]",
		},
		"MatchRestImm": {
			src:  `fn f(b: &Box) { return match b { {n, ...rest} => [n, rest] } }`,
			want: "fn <'a>(b: &'a Box) -> [number, &'a {inner: Flat}]",
		},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			values, _, errs := inferSource(t, objectPatternBorrowDecls+tc.src)
			require.Empty(t, errs)
			require.Equal(t, tc.want, values["f"])
		})
	}
}

// TestObjectPatternLeafCannotMoveOutOfBorrow covers each way a body can try to move a leaf
// bound through a borrowed source to a new owner. Each reports the error a member read
// such as `return b.inner` reports. The span is the declaration of Flat, the type the
// borrowed value has.
func TestObjectPatternLeafCannotMoveOutOfBorrow(t *testing.T) {
	tests := map[string]struct {
		src  string
		want []string
	}{
		"MemberReadReturnedMut": {
			src:  `fn f(b: &mut Box) -> Flat { return b.inner }`,
			want: []string{"2:14-2:25: cannot use borrowed &mut Flat as owned object"},
		},
		"ValReturnedMut": {
			src: `
				fn f(b: &mut Box) -> Flat {
					val {inner} = b
					return inner
				}
			`,
			want: []string{"2:14-2:25: cannot use borrowed &mut Flat as owned object"},
		},
		"ValReturnedImm": {
			src: `
				fn f(b: &Box) -> Flat {
					val {inner} = b
					return inner
				}
			`,
			want: []string{"2:14-2:25: cannot use borrowed &Flat as owned object"},
		},
		"ValKeyValueReturnedMut": {
			src: `
				fn f(b: &mut Box) -> Flat {
					val {inner: i} = b
					return i
				}
			`,
			want: []string{"2:14-2:25: cannot use borrowed &mut Flat as owned object"},
		},
		"ValNestedReturnedMut": {
			src: `
				fn f(b: &mut Nest) -> Flat {
					val {box: {inner}} = b
					return inner
				}
			`,
			want: []string{"2:14-2:25: cannot use borrowed &mut Flat as owned object"},
		},
		"ValPassedToKeepMut": {
			src: `
				fn f(b: &mut Box) -> undefined {
					val {inner} = b
					keep(inner)
				}
			`,
			want: []string{"2:14-2:25: cannot use borrowed &mut Flat as owned object"},
		},
		"ValPassedToKeepImm": {
			src: `
				fn f(b: &Box) -> undefined {
					val {inner} = b
					keep(inner)
				}
			`,
			want: []string{"2:14-2:25: cannot use borrowed &Flat as owned object"},
		},
		"ValPushedMut": {
			src: `
				fn f(b: &mut Box, mut stash: Array<Flat>) -> undefined {
					val {inner} = b
					stash.push(inner)
				}
			`,
			want: []string{"2:14-2:25: cannot use borrowed &mut Flat as owned object"},
		},
		"ValPushedImm": {
			src: `
				fn f(b: &Box, mut stash: Array<Flat>) -> undefined {
					val {inner} = b
					stash.push(inner)
				}
			`,
			want: []string{"2:14-2:25: cannot use borrowed &Flat as owned object"},
		},
		"MatchPassedToKeepMut": {
			src:  `fn f(b: &mut Box) -> undefined { match b { {inner, n} => keep(inner) } }`,
			want: []string{"2:14-2:25: cannot use borrowed &mut Flat as owned object"},
		},
		"MatchReturnedImm": {
			src:  `fn f(b: &Box) -> Flat { return match b { {inner, n} => inner } }`,
			want: []string{"2:14-2:25: cannot use borrowed &Flat as owned object"},
		},
		"ValueTypeFieldReturnedOk": {
			src: `
				fn f(b: &mut Box) -> number {
					val {n} = b
					return n
				}
			`,
			want: nil,
		},
		"MutLeafThroughImmutableBorrow": {
			src:  `fn f(b: &Box) -> undefined { val {mut inner} = b }`,
			want: []string{"8:35-8:44: cannot bind a `mut` leaf through an immutable borrow; the scrutinee must be owned or a `&mut` borrow"},
		},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			_, _, errs := inferSource(t, objectPatternBorrowDecls+tc.src)
			require.Equal(t, tc.want, messagesWithSpan(t, errs))
		})
	}
}
