package solver

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestReceiverModes pins what each receiver form does at a call. A `&self` or `&mut self`
// method borrows the instance for the call and leaves it usable. A `self` or `mut self`
// method moves it, so a later use is a use-after-move, and it cannot be reached through a
// borrow, which has no instance to give up.
func TestReceiverModes(t *testing.T) {
	const counter = `
		class C {
			v: number,
			look(&self) -> number { return self.v },
			take(self) -> number { return self.v },
			drain(mut self) -> number {
				self.v = 0
				return self.v
			},
		}
	`
	tests := []struct {
		name string
		src  string
		want []string
	}{
		{
			name: "a borrowing call leaves the receiver usable",
			src: counter + `
				fn f() {
					val c = C(1)
					c.look()
					c.look()
				}
			`,
		},
		{
			name: "a consuming call moves the receiver",
			src: counter + `
				fn f() {
					val c = C(1)
					c.take()
					c.look()
				}
			`,
			want: []string{"15:6-15:12: use of moved value 'c'"},
		},
		{
			name: "a field read after a consuming call is a use of the moved value",
			src: counter + `
				fn f() -> number {
					val c = C(1)
					c.take()
					return c.v
				}
			`,
			want: []string{"15:13-15:16: use of moved value 'c'"},
		},
		{
			name: "a consuming call is the last use, so borrowing calls before it are fine",
			src: counter + `
				fn f() {
					val c = C(1)
					c.look()
					c.take()
				}
			`,
		},
		{
			name: "reading a consuming method as a value moves the receiver",
			src: counter + `
				fn f() -> number {
					val c = C(1)
					val g = c.take
					g()
					return c.v
				}
			`,
			want: []string{"16:13-16:16: use of moved value 'c'"},
		},
		{
			name: "a consuming call moves a receiver typed by an alias",
			src: counter + `
				type B = C
				fn f(b: B) -> number {
					return b.take() + b.take()
				}
			`,
			want: []string{"14:24-14:25: use of moved value 'b'"},
		},
		{
			name: "an overloaded consuming method moves the receiver",
			src: `
				class D {
					v: number,
					take(self, x: number) -> number { return x },
					take(self, x: string) -> string { return x },
				}
				fn f() -> number {
					val d = D(1)
					d.take(1)
					return d.v
				}
			`,
			want: []string{"10:13-10:16: use of moved value 'd'"},
		},
		{
			name: "a mutable consuming receiver takes a mutable owned instance",
			src: counter + `
				fn f() {
					val mut c = C(1)
					c.drain()
				}
			`,
		},
		{
			name: "a mutable consuming receiver takes an immutable owned instance it moves",
			src: counter + `
				fn f() {
					val c = C(1)
					c.drain()
				}
			`,
		},
		{
			name: "an immutable instance a mutable consuming receiver took is moved",
			src: counter + `
				fn f() -> number {
					val c = C(1)
					c.drain()
					return c.v
				}
			`,
			want: []string{"15:13-15:16: use of moved value 'c'"},
		},
		{
			name: "an owned parameter is moved by a mutable consuming receiver",
			src: counter + `
				fn f(c: C) -> number {
					return c.drain()
				}
			`,
		},
		{
			name: "a mutable consuming receiver rejects an instance that names no place",
			src: counter + `
				fn make() -> C { return C(1) }
				fn f() -> number {
					return make().drain()
				}
			`,
			want: []string{"14:13-14:25: cannot constrain immutable C <: mutable C"},
		},
		{
			name: "a consuming call through a borrowed parameter is rejected",
			src: counter + `
				fn f(p: &C) {
					p.take()
				}
			`,
			want: []string{"13:6-13:12: 'take' takes its receiver by value, so it moves the instance and cannot be reached through a borrow."},
		},
		{
			name: "a borrow copied into a local is still a borrow",
			src: counter + `
				fn f(p: &C) {
					val q = p
					q.take()
				}
			`,
			want: []string{"14:6-14:12: 'take' takes its receiver by value, so it moves the instance and cannot be reached through a borrow."},
		},
		{
			name: "a borrowed self cannot reach a consuming sibling",
			src: `
				class C {
					v: number,
					take(self) -> number { return self.v },
					again(&self) -> number { return self.take() },
				}
			`,
			want: []string{"5:38-5:47: 'take' takes its receiver by value, so it moves the instance and cannot be reached through a borrow."},
		},
		{
			name: "a consuming self can reach a consuming sibling",
			src: `
				class C {
					v: number,
					take(self) -> number { return self.v },
					again(self) -> number { return self.take() },
				}
			`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, errs := inferSource(t, tt.src)
			require.Equal(t, tt.want, messagesWithSpan(t, errs))
		})
	}
}

// TestReceiverModeAgreement pins the places two receivers of one member must agree on whether
// they borrow or consume the instance. Every overload arm must take the same receiver, a
// setter must borrow mutably, and an override may not consume where the inherited member
// borrows.
func TestReceiverModeAgreement(t *testing.T) {
	tests := []struct {
		name string
		src  string
		want []string
	}{
		{
			name: "overload arms that borrow and consume are rejected",
			src: `
				class C {
					f(&self, x: number) -> number { return x },
					f(self, x: string) -> string { return x },
				}
			`,
			want: []string{"4:6-4:47: Overloaded method 'f' must use the same `self` receiver in every arm."},
		},
		{
			name: "a setter with a consuming receiver is rejected",
			src: `
				class C {
					v: number,
					set x(mut self, value: number) { self.v = value },
				}
			`,
			want: []string{"4:6-4:55: Setter 'x' must declare a `&mut self` receiver; writing through it mutates the instance."},
		},
		{
			name: "a getter with a consuming receiver is rejected",
			src: `
				class C {
					v: number,
					get x(self) -> number { return self.v },
				}
			`,
			want: []string{"4:6-4:45: Getter 'x' must borrow its receiver with `&self` or `&mut self`; reading through it leaves the instance in place."},
		},
		{
			name: "a constructor cannot consume the instance it returns",
			src: `
				class C {
					v: number,
					constructor(&mut self, v: number) {
						self.v = v
						self.take()
					},
					take(self) -> number { return self.v },
				}
			`,
			want: []string{"6:7-6:16: 'take' takes its receiver by value, so it moves the instance and cannot be reached through a borrow."},
		},
		{
			name: "an override that consumes where the inherited member borrows is rejected",
			src: `
				class Animal {
					constructor(&mut self) {},
					f(&self) -> number { return 1 },
				}
				class Dog extends Animal {
					constructor(&mut self) { super() },
					f(self) -> number { return 2 },
				}
			`,
			want: []string{"8:6-8:36: class `Dog` redeclares inherited member `f` as a method taking `self`, but `Animal` declares it as a method"},
		},
		{
			name: "an override that borrows where the inherited member consumes is accepted",
			src: `
				class Animal {
					constructor(&mut self) {},
					f(self) -> number { return 1 },
				}
				class Dog extends Animal {
					constructor(&mut self) { super() },
					f(&self) -> number { return 2 },
				}
			`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, errs := inferSource(t, tt.src)
			require.Equal(t, tt.want, messagesWithSpan(t, errs))
		})
	}
}
