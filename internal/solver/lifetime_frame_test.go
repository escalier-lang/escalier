package solver

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestFrameOwnedReturnSignatures covers the signatures D16's two rules give a function that
// returns an immutable borrow. A return is owned exactly when its lifetime ends with the
// function's frame. Each case checks without errors.
//
// The issue's table writes the owned result of `val b = {x: 1}` as `{x: number}`. A `val`
// keeps the literal type of its initializer, so `return b` already renders `{x: 1}`, and the
// owned form of `return &b` renders the same.
func TestFrameOwnedReturnSignatures(t *testing.T) {
	tests := []struct {
		name    string
		src     string
		binding string
		want    string
	}{
		// Nothing outside the call reaches b, so the caller owns the result.
		{
			name: "borrow of a local nothing else reaches",
			src: `
				fn h() {
					val b = {x: 1}
					return &b
				}
			`,
			binding: "h",
			want:    "fn () -> {x: 1}",
		},
		// The borrow stored in cache outlives the frame, so the returned &b takes its
		// `'static` lifetime under the first rule.
		{
			name: "borrow of a local module-level state also reads",
			src: `
				var cache: &{x: number} = &{x: 0}
				fn h() {
					val b = {x: 1}
					cache = &b
					return &b
				}
			`,
			binding: "h",
			want:    "fn () -> &'static {x: 1}",
		},
		// The result carries the copy of 'a the parameter carries, so the callee may return
		// any value that outlives the argument.
		{
			name: "call whose signature ties the result to the argument",
			src: `
				declare fn lookup<'a>(p: &'a {x: number}) -> &'a {x: number}
				fn h() {
					val b = {x: 1}
					return lookup(&b)
				}
			`,
			binding: "h",
			want:    "fn () -> &{x: number}",
		},
		// The result's lifetime ties to nothing in the signature. L2 rejects a body that returns
		// p under this signature, so make returns a value nothing outside the call reaches. D16's
		// second rule then makes h's return owned.
		{
			name: "call whose signature ties the result to nothing",
			src: `
				declare fn make(p: &{x: number}) -> &{x: number}
				fn h() {
					val b = {x: 1}
					return make(&b)
				}
			`,
			binding: "h",
			want:    "fn () -> {x: number}",
		},
		// r borrows q, which only cache reads, and cache is a local of outer. Once outer
		// returns nothing else reaches q.
		{
			name: "result of a closure whose borrow a local of the frame holds",
			src: `
				fn outer() {
					val cfg = {x: 0}
					var cache: &{x: number} = &{x: 0}

					val getCfg = fn (p: &{x: number}) {
						return &cfg
					}
					val stash = fn (p: &{x: number}) {
						val q = {x: 1}
						cache = &q
						return &q
					}

					val r = stash(&cfg)
					return r
				}
			`,
			binding: "outer",
			want:    "fn () -> {x: 1}",
		},
		{
			name: "borrow of module-level state",
			src: `
				var cfg = {x: 0}
				fn getCfg(p: &{x: number}) {
					return &cfg
				}
			`,
			binding: "getCfg",
			want:    "fn (p: &{x: number}) -> &'static {x: number}",
		},
		{
			name: "borrow of a local module-level state also reads, beside a parameter",
			src: `
				var cache: &{x: number} = &{x: 0}
				fn stash(p: &{x: number}) {
					val q = {x: 1}
					cache = &q
					return &q
				}
			`,
			binding: "stash",
			want:    "fn (p: &{x: number}) -> &'static {x: 1}",
		},
		// Inside the closure, cfg is a capture, so the result borrows a value outer still
		// holds. Every call of getCfg returns a borrow of that one value, so no caller can own
		// it.
		{
			name: "closure returning a borrow of a capture",
			src: `
				fn outer() {
					val cfg = {x: 0}
					val getCfg = fn (p: &{x: number}) {
						return &cfg
					}
					return getCfg
				}
			`,
			binding: "outer",
			want:    "fn () -> fn (p: &{x: number}) -> &{x: 0}",
		},
		// Inside the closure, cache belongs to outer, so the borrow stored there outlives the
		// closure's frame and the returned &q takes its lifetime. Each call builds a new q, but
		// cache keeps reading it after the call returns. The first rule does not ask whether
		// anything reads cache later, so the result stays a borrow.
		{
			name: "closure returning a borrow a capture also holds",
			src: `
				fn outer() {
					var cache: &{x: number} = &{x: 0}
					val stash = fn (p: &{x: number}) {
						val q = {x: 1}
						cache = &q
						return &q
					}
					return stash
				}
			`,
			binding: "outer",
			want:    "fn () -> fn (p: &{x: number}) -> &{x: 1}",
		},
		// Each read of t copies its borrow's lifetime, and cache holds one of the copies.
		{
			name: "borrow module-level state reads through a copy",
			src: `
				var cache: &{x: number} = &{x: 0}
				fn h() {
					val b = {x: 1}
					val t = &b
					cache = t
					return t
				}
			`,
			binding: "h",
			want:    "fn () -> &'static {x: 1}",
		},
		// The first rule does not depend on which borrow the source takes first.
		{
			name: "borrow taken before module-level state reads the place",
			src: `
				var cache: &{x: number} = &{x: 0}
				fn h() {
					val b = {x: 1}
					val t = &b
					cache = &b
					return t
				}
			`,
			binding: "h",
			want:    "fn () -> &'static {x: 1}",
		},
		// A borrow of an optional field is typed `&T | undefined`. The read of t returns a copy
		// of its lifetime inside that union, and the copy takes the `'static` lifetime of the
		// borrow cache holds. The bare `&b` cases above render the same `&'static`.
		{
			name: "borrow of an optional field taken before module-level state reads it",
			src: `
				var cache: &{y: number} | undefined = undefined
				fn h() {
					val b: {x?: {y: number}} = {x: {y: 1}}
					val t = &b.x
					cache = &b.x
					return t
				}
			`,
			binding: "h",
			want:    "fn () -> &'static {y: number} | undefined",
		},
		{
			name: "borrow of an optional field module-level state reads through a copy",
			src: `
				var cache: &{y: number} | undefined = undefined
				fn h() {
					val b: {x?: {y: number}} = {x: {y: 1}}
					val t = &b.x
					cache = t
					return t
				}
			`,
			binding: "h",
			want:    "fn () -> &'static {y: number} | undefined",
		},
		// A borrow of b.x overlaps a borrow of b, so the returned &b reads what cache reads.
		{
			name: "borrow of a place overlapping one module-level state reads",
			src: `
				var cache: &{y: number} = &{y: 0}
				fn h() {
					val b = {x: {y: 1}}
					cache = &b.x
					return &b
				}
			`,
			binding: "h",
			want:    "fn () -> &'static {x: {y: 1}}",
		},
		// The literal's borrow of b stays a borrow when module-level state also reads b. It
		// reads a copy of the borrow's lifetime, which is tied to the one in `cache`, so it renders
		// `&'static`.
		{
			name: "borrow inside a returned literal module-level state also reads",
			src: `
				var cache: &{x: number} = &{x: 0}
				fn h() {
					val b = {x: 1}
					val o = {f: &b}
					cache = &b
					return o
				}
			`,
			binding: "h",
			want:    "fn () -> {f: &'static {x: 1}}",
		},
		// Every borrow of a parameter is foreign, so the return stays a borrow. A call to an
		// unannotated parameter inferred as owned records no move, as B5 and B6 in
		// requirements.md describe, so the caller may still hold that argument. An annotated
		// owned parameter such as f moves its argument, so here the borrow is conservative.
		{
			name: "borrow of a parameter",
			src: `
				fn h(f: {v: number}) {
					val r = &f
					return r
				}
			`,
			binding: "h",
			want:    "fn (f: {v: number}) -> &{v: number}",
		},
		// The module keeps the value gref borrows, so the read of gref is foreign and the
		// return stays a borrow. A module-level initializer does not constrain its borrow to
		// `'static`, so gref renders `&{x: 1}`, and the copy h returns renders the same.
		{
			name: "read of a module-level borrow",
			src: `
				val cfg = {x: 1}
				val gref = &cfg
				fn h() {
					return gref
				}
			`,
			binding: "h",
			want:    "fn () -> &{x: 1}",
		},
		// The method's signature ties the result to the receiver.
		{
			name: "method call whose signature ties the result to the receiver",
			src: `
				class View {
					p: {x: number},
					peek<'a>(&'a self) -> &'a {x: number} { return &self.p },
				}
				fn h() {
					val v = View({x: 1})
					return v.peek()
				}
			`,
			binding: "h",
			want:    "fn () -> &{x: number}",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			values, _, errs := inferSource(t, tt.src)
			require.Empty(t, errs)
			require.Equal(t, tt.want, values[tt.binding])
		})
	}
}

// TestFrameOwnedReturnWrites covers writes through a returned borrow that D16's rules keep a
// borrow. Each write reports where it happens.
func TestFrameOwnedReturnWrites(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want []string
	}{
		// cache still reads the value h returns, so k cannot write it.
		{
			name: "write through the result of a function module-level state also reads",
			src: `
				var cache: &{x: number} = &{x: 0}

				fn h() {
					val b = {x: 1}
					cache = &b
					return &b
				}

				fn k() {
					val mut r = h()
					r.x = 5
				}
			`,
			want: []string{
				// r holds h's `&'static {x: 1}`. `val b = {x: 1}` keeps the literal type of x,
				// and T9 does not widen a type behind a borrow, so 5 does not fit x's type 1.
				"12:12-12:13: cannot constrain 5 <: 1",
				// The write needs a mutable object, and r is an immutable borrow. This is the
				// write D16's first rule rejects by keeping h's result a borrow.
				"12:6-12:13: cannot constrain immutable object <: mutable object",
			},
		},
		// Inside outer, cache still reads the value stash returns.
		{
			name: "write through the result of a closure a local of the frame also reads",
			src: `
				fn outer() {
					val cfg = {x: 0}
					var cache: &{x: number} = &{x: 0}

					val stash = fn (p: &{x: number}) {
						val q = {x: 1}
						cache = &q
						return &q
					}

					val mut r = stash(&cfg)
					r.x = 5
					return r
				}
			`,
			want: []string{
				// r holds stash's `&{x: 1}`. `val q = {x: 1}` keeps the literal type of x, and
				// T9 does not widen a type behind a borrow, so 5 does not fit x's type 1.
				"13:12-13:13: cannot constrain 5 <: 1",
				// The write needs a mutable object, and r is an immutable borrow. The first rule
				// keeps stash's result a borrow, since cache still reads q inside outer.
				"13:6-13:13: cannot constrain immutable object <: mutable object",
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, errs := inferSource(t, tt.src)
			require.Equal(t, tt.want, messagesWithSpan(t, errs))
		})
	}
}

// TestFrameOwnedMutReturnSignatures covers the signatures D16 gives a function that returns a
// `&mut` or a value holding borrows. A borrow is owned exactly when its lifetime ends with the
// function's frame and no other borrow in the returned value reaches an overlapping place. Each
// case checks without errors.
func TestFrameOwnedMutReturnSignatures(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want string
	}{
		// `keep` holds a `'static` `&mut b`, so the returned `&mut b` takes its lifetime under the
		// first rule and stays a borrow.
		{
			name: "mutable borrow of a local a static mutable borrow also reaches",
			src: `
				declare fn keep(x: &'static mut {x: number}) -> undefined

				fn h() {
					val mut b = {x: 1}
					keep(&mut b)
					return &mut b
				}
			`,
			want: "fn () -> &'static mut {x: number}",
		},
		// The borrow nested in the returned literal takes the same lifetime.
		{
			name: "nested mutable borrow of a local a static mutable borrow also reaches",
			src: `
				declare fn keep(x: &'static mut {x: number}) -> undefined

				fn h() {
					val mut b = {x: 1}
					keep(&mut b)
					return {p: &mut b}
				}
			`,
			want: "fn () -> {p: &'static mut {x: number}}",
		},
		// r reads a copy of the lifetime of the `&mut b` it holds. The first rule ties that
		// borrow to the one `keep` holds, and the copy is tied too.
		{
			name: "copy of a mutable borrow taken after a static one",
			src: `
				declare fn keep(x: &'static mut {x: number}) -> undefined

				fn h() {
					val mut b = {x: 1}
					keep(&mut b)
					val r = &mut b
					return r
				}
			`,
			want: "fn () -> &'static mut {x: number}",
		},
		// A lifetime covers every use of its borrow, so a borrow taken before `keep`'s is tied
		// to it too.
		{
			name: "copy of a mutable borrow taken before a static one",
			src: `
				declare fn keep(x: &'static mut {x: number}) -> undefined

				fn h() {
					val mut b = {x: 1}
					val r = &mut b
					keep(&mut b)
					return r
				}
			`,
			want: "fn () -> &'static mut {x: number}",
		},
		// Tying r to `keep`'s borrow adds no rejection. Writes to b and through r stay accepted.
		{
			name: "writes after a mutable borrow tied to a static one",
			src: `
				declare fn keep(x: &'static mut {x: number}) -> undefined

				fn h() {
					val mut b = {x: 1}
					keep(&mut b)
					val r = &mut b
					r.x = 2
					b.x = 3
					return r
				}
			`,
			want: "fn () -> &'static mut {x: number}",
		},
		// The store puts a borrow of b in the caller's object, so the returned `&mut b` takes
		// its lifetime and the signature ties the result to the field of `out`.
		{
			name: "mutable borrow of a local stored into a parameter",
			src: `
				fn h(out: &mut {s: &mut {x: number}}) {
					val mut b = {x: 1}
					out.s = &mut b
					return &mut b
				}
			`,
			want: "fn <'a>(out: &mut {s: &'a mut {x: number}}) -> &'a mut {x: number}",
		},
		// Nothing outside the call reaches b, so the caller owns the field.
		{
			name: "nested mutable borrow of a local nothing else reaches",
			src: `
				fn h() {
					val mut b = {x: 1}
					return {p: &mut b}
				}
			`,
			want: "fn () -> {p: {x: number}}",
		},
		// Both borrows reach b, so owning either would hide the other path.
		{
			name: "two mutable borrows of one local in a returned literal",
			src: `
				fn h() {
					val mut b = {x: 1}
					return {l: &mut b, r: &mut b}
				}
			`,
			want: "fn () -> {l: &mut {x: number}, r: &mut {x: number}}",
		},
		// The borrows reach different locals, so the caller owns both.
		{
			name: "mutable borrows of two locals in a returned literal",
			src: `
				fn h() {
					val mut b = {x: 1}
					val mut c = {x: 2}
					return {l: &mut b, r: &mut c}
				}
			`,
			want: "fn () -> {l: {x: number}, r: {x: number}}",
		},
		// The returned `&mut d` is the only path to d, so the caller owns d. The two borrows
		// inside d both reach b, so they stay borrows.
		{
			name: "mutable borrow of a local holding two borrows of one local",
			src: `
				fn h() {
					val mut b = {x: 1}
					val mut d = {l: &mut b, r: &mut b}
					return &mut d
				}
			`,
			want: "fn () -> {l: &mut {x: number}, r: &mut {x: number}}",
		},
		// A tuple element is owned the way an object field is.
		{
			name: "mutable borrow of a local in a returned tuple",
			src: `
				fn h() {
					val mut b = {x: 1}
					return [&mut b]
				}
			`,
			want: "fn () -> [{x: number}]",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			values, _, errs := inferSource(t, tt.src)
			require.Empty(t, errs)
			require.Equal(t, tt.want, values["h"])
		})
	}
}
