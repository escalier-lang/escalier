package solver

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestASuperLifetimeBinderIsHeldRigid covers a value checked against a signature that
// quantifies a lifetime. The signature's caller chooses the lifetime, so the value has to
// accept every choice. One that does passes, and one that requires the lifetime to be
// 'static, to be tied to a lifetime the signature does not quantify, or to outlive a sibling
// the bounds do not declare is reported where it met the signature.
func TestASuperLifetimeBinderIsHeldRigid(t *testing.T) {
	t.Parallel()

	const cb = "type Cb = fn <'a>(q: &'a {x: number}) -> &'a {x: number}\n"
	tests := []struct {
		name string
		src  string
		want []string
	}{
		{
			name: "an identity closure accepts every lifetime",
			src:  cb + `fn mk() -> Cb { return fn (q) { return q } }`,
		},
		{
			name: "a closure returning a captured borrow is rejected",
			src:  cb + `fn mk(seed: &{x: number}) -> Cb { return fn (q) { return seed } }`,
			want: []string{"2:42-2:64: the caller of this signature chooses 'a, but the value requires 'a to be outlived by a lifetime the signature does not quantify"},
		},
		{
			name: "a closure demanding a static argument is rejected",
			src:  cb + `fn mk() -> Cb { return fn (q: &'static {x: number}) { return q } }`,
			want: []string{"2:24-2:65: the caller of this signature chooses 'a, but the value requires 'a to be 'static"},
		},
		{
			name: "a closure relating two lifetimes the bounds do not declare is rejected",
			src: `type Pick = fn <'a, 'b>(x: &'a {x: number}, y: &'b {x: number}) -> &'b {x: number}
				fn mk() -> Pick { return fn (x, y) { return x } }`,
			want: []string{"2:30-2:52: the caller of this signature chooses 'a, but the value requires 'a to outlive 'b, which the signature does not declare"},
		},
		{
			name: "a declared bound allows the relation it declares",
			src: `type Pick = fn <'a: 'b, 'b>(x: &'a {x: number}, y: &'b {x: number}) -> &'b {x: number}
				fn mk() -> Pick { return fn (x, y) { return x } }`,
		},
		{
			name: "a closure with its own annotated borrow accepts every lifetime",
			src:  cb + `fn mk() -> Cb { return fn (q: &{x: number}) { return q } }`,
		},
		{
			name: "a generalized function value accepts every lifetime",
			src: cb + `fn id<'x>(q: &'x {x: number}) -> &'x {x: number} { return q }
				fn mk() -> Cb { return id }`,
		},
		{
			// The annotation fixes the result at 'static, so `id` would have to return a static
			// borrow from any 'a the caller picks.
			name: "an annotation fixing the result at 'static is rejected",
			src: `fn id<'x>(q: &'x mut {x: number}) -> &'x mut {x: number} { return q }
				val f: fn <'a>(p: &'a mut {x: number}) -> &'static mut {x: number} = id`,
			want: []string{"2:74-2:76: the caller of this signature chooses 'a, but the value requires 'a to be 'static"},
		},
		{
			// `g` generalizes over its own lifetime, and the reference instantiates it afresh.
			name: "a bound identity closure accepts every lifetime",
			src: cb + `fn mk() -> Cb {
				val g = fn (q) { return q }
				return g
			}`,
		},
		{
			name: "a bound closure returning a captured borrow is rejected",
			src: cb + `fn mk(seed: &{x: number}) -> Cb {
				val g = fn (q) { return seed }
				return g
			}`,
			want: []string{"4:12-4:13: the caller of this signature chooses 'a, but the value requires 'a to be outlived by a lifetime the signature does not quantify"},
		},
		{
			// 'a is 'static by declaration, so every choice of it outlives 'b.
			name: "a static bound declares every relation from the lifetime",
			src: `type Pick = fn <'a: 'static, 'b>(x: &'a {x: number}, y: &'b {x: number}) -> &'b {x: number}
				fn mk() -> Pick { return fn (x, y) { return x } }`,
		},
		{
			name: "two values at one site report once",
			src:  cb + `fn mk(seed: &{x: number}) -> [Cb, Cb] { return [fn (q) { return seed }, fn (q) { return seed }] }`,
			want: []string{"2:48-2:96: the caller of this signature chooses 'a, but the value requires 'a to be outlived by a lifetime the signature does not quantify"},
		},
		{
			name: "a callback argument to an overloaded function is checked",
			src: cb + `declare fn pick(f: Cb) -> number
				declare fn pick(s: string) -> number
				fn go(seed: &{x: number}) -> number { return pick(fn (q) { return seed }) }`,
			want: []string{"4:50-4:78: the caller of this signature chooses 'a, but the value requires 'a to be outlived by a lifetime the signature does not quantify"},
		},
		{
			name: "a callback argument is checked at the call",
			src: cb + `fn use(f: Cb) -> number { return 1 }
				fn go(seed: &{x: number}) -> number { return use(fn (q) { return seed }) }`,
			want: []string{"3:50-3:77: the caller of this signature chooses 'a, but the value requires 'a to be outlived by a lifetime the signature does not quantify"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			_, _, errs := inferSource(t, tt.src)
			if tt.want == nil {
				require.Empty(t, messagesWithSpan(t, errs))
				return
			}
			require.Equal(t, tt.want, messagesWithSpan(t, errs))
		})
	}
}

// TestAnOverrideIsCheckedAgainstARigidBinder covers the override check, which compares an
// override against the base signature as its supertype. A base method quantifying a lifetime
// holds it rigid, so an override that keeps the binder passes and one that fixes the lifetime
// is reported as incompatible.
func TestAnOverrideIsCheckedAgainstARigidBinder(t *testing.T) {
	t.Parallel()

	const base = `
		class Base {
			p: {x: number},
			id<'a>(&self, q: &'a {x: number}) -> &'a {x: number} { return q },
		}
	`
	tests := []struct {
		name string
		src  string
		want []string
	}{
		{
			name: "an override keeping the binder is allowed",
			src: base + `
				class Derived extends Base {
					constructor(&mut self) { super({x: 1}) },
					id<'b>(&self, q: &'b {x: number}) -> &'b {x: number} { return q },
				}
			`,
		},
		{
			name: "an override fixing the lifetime is rejected",
			src: base + `
				class Derived extends Base {
					constructor(&mut self) { super({x: 1}) },
					id(&self, q: &'static {x: number}) -> &'static {x: number} { return q },
				}
			`,
			want: []string{"class `Derived` redeclares inherited member `id` with type `fn (q: &'static {x: number}) -> &'static {x: number}`, which is not compatible with `fn <'a>(q: &'a {x: number}) -> &'a {x: number}` declared by `Base`"},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			_, _, errs := inferSource(t, tt.src)
			if tt.want == nil {
				require.Empty(t, errorMessagesOf(errs))
				return
			}
			require.Equal(t, tt.want, errorMessagesOf(errs))
		})
	}
}
