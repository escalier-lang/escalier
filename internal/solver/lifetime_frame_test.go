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
		// holds.
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
		// closure's frame and the returned &q takes its lifetime.
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
		// The literal's borrow of b keeps its type when module-level state also reads b.
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
			want:    "fn () -> {f: &{x: 1}}",
		},
		// The caller may still hold the value of a parameter.
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
		// The module keeps the value gref borrows.
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
				"12:12-12:13: cannot constrain 5 <: 1",
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
				"13:12-13:13: cannot constrain 5 <: 1",
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
