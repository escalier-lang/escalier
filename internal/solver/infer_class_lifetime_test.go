package solver

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// TestInferClassLifetimeParams covers a class that quantifies lifetimes. `class Holder<'a>`
// binds 'a for the whole declaration, so a field or a member signature writing `&'a` shares
// the variable the parameter carries, a reference supplies one argument per parameter, and
// the instance's own handle carries its parameters as its arguments.
//
// This is the class twin of what a lifetime-generic alias already does. The two resolve their
// parameters and their reference arguments through the same helpers, so a class reference is
// checked for arity the way an alias reference is.
func TestInferClassLifetimeParams(t *testing.T) {
	tests := map[string]struct {
		src   string
		want  []string
		types map[string]string
	}{
		// The parameter reaches the field, so reading the field off an instance yields a
		// borrow at the argument the reference supplied. Without the parameter binding, the
		// field's `&'a` would mint a lifetime of its own that no argument could reach.
		"FieldCarriesTheClassLifetime": {
			src: `
				class Holder<'a> { peer: &'a mut {value: number} }
				fn read<'x>(h: Holder<'x>) -> &'x mut {value: number} { return h.peer }
			`,
			want: nil,
			types: map[string]string{
				"Holder": "<'a> {new (peer: &'a mut {value: number}) -> Holder<'a>}",
				"read":   "fn <'a>(h: Holder<'a>) -> &'a mut {value: number}",
			},
		},
		// Returning the field at a DIFFERENT lifetime relates the two rather than passing
		// silently, so the field's borrow is a real obligation on the caller. This is the
		// case that fails vacuously when the class parameter is erased from the frozen body:
		// with nothing there to relate, the function infers no bound at all. The structural
		// twin `h: {peer: &'x mut B}` infers the identical signature.
		"FieldAtAnotherLifetimeInfersABound": {
			src: `
				class Holder<'a> { peer: &'a mut {value: number} }
				fn read<'x, 'y>(h: Holder<'x>, other: &'y mut {value: number}) -> &'y mut {value: number} {
					return h.peer
				}
			`,
			want: nil,
			types: map[string]string{
				"Holder": "<'a> {new (peer: &'a mut {value: number}) -> Holder<'a>}",
				"read": "fn <'a, 'b: 'a>(h: Holder<'a>, other: &'b mut {value: number}) " +
					"-> &'b mut {value: number}",
			},
		},
		// A member's `&'a` is the class's 'a, not one of its own, so a call unifies the
		// argument and the result at the instance's lifetime argument.
		"MemberLifetimeIsTheClassLifetime": {
			src: `
				class Holder<'a> {
					peer: &'a mut {value: number},
					swap(mut self, p: &'a mut {value: number}) -> &'a mut {value: number} {
						self.peer = p
						return self.peer
					},
				}
				fn call<'x>(h: mut Holder<'x>, p: &'x mut {value: number}) -> &'x mut {value: number} {
					return h.swap(p)
				}
			`,
			want: nil,
			types: map[string]string{
				"Holder": "<'a> {new (peer: &'a mut {value: number}) -> Holder<'a>}",
				"call": "fn <'a>(h: mut Holder<'a>, p: &'a mut {value: number}) " +
					"-> &'a mut {value: number}",
			},
		},
		// A member signature writes the class's 'a without a clause of its own. The binder
		// sits on the class, so the undeclared-lifetime check reads it as declared.
		"MemberWritesTheClassLifetime": {
			src: `
				class Holder<'a> {
					peer: &'a mut {value: number},
					put(mut self, p: &'a mut {value: number}) -> undefined { self.peer = p },
				}
			`,
			want: nil,
			types: map[string]string{
				"Holder": "<'a> {new (peer: &'a mut {value: number}) -> Holder<'a>}",
			},
		},
		// A name neither the class nor the member binds is still undeclared, so the class
		// binder widens the scope rather than disabling the check.
		"MemberWritesAnUnboundLifetime": {
			src: `
				class Holder<'a> {
					peer: &'a mut {value: number},
					put(mut self, p: &'z mut {value: number}) -> undefined { },
				}
			`,
			want: []string{
				"4:24-4:26: lifetime 'z is used but not declared; add `<'z>` to the enclosing function signature",
			},
			types: map[string]string{
				"Holder": "<'a> {new (peer: &'a mut {value: number}) -> Holder<'a>}",
			},
		},
		// A reference supplies one argument per parameter, and a count mismatch reports the
		// same arity error an alias reference reports.
		"ReferenceMustSupplyEveryLifetimeArgument": {
			src: `
				class Holder<'a> { peer: &'a mut {value: number} }
				declare fn f(h: Holder) -> undefined
			`,
			want: []string{"3:21-3:27: class `Holder` expects 1 lifetime arguments but got 0"},
			types: map[string]string{
				"Holder": "<'a> {new (peer: &'a mut {value: number}) -> Holder<'a>}",
				"f":      "fn <'a>(h: Holder<'a>) -> undefined",
			},
		},
	}

	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			values, types, errs := inferSource(t, tc.src)
			require.Equal(t, tc.want, messagesWithSpan(errs))
			for name, want := range tc.types {
				require.Equal(t, want, values[name], "value binding %s", name)
			}
			require.Equal(t, "Holder<'a>", types["Holder"])
		})
	}
}

// TestClassLifetimeParamsRenderUnderSourceNames covers the names a class's two bindings
// render its lifetime parameters under. Both keep what the source wrote, so the value binding
// and the type binding read the same, and neither takes the generated 'a, 'b the quantifier
// would otherwise assign.
func TestClassLifetimeParamsRenderUnderSourceNames(t *testing.T) {
	src := `
		class Pair<'x, 'y> {
			a: &'x mut {value: number},
			b: &'y mut {value: number},
		}
	`
	values, types, errs := inferSource(t, src)
	require.Empty(t, messagesWithSpan(errs))
	require.Equal(t,
		"<'x, 'y> {new (a: &'x mut {value: number}, b: &'y mut {value: number}) -> Pair<'x, 'y>}",
		values["Pair"],
	)
	require.Equal(t, "Pair<'x, 'y>", types["Pair"])
}

// TestClassLifetimeSubtyping covers what a class's lifetime arguments oblige at the nominal
// subtype rule. A class records no per-parameter variance for the lifetime sort, so each
// argument is invariant: reaching `Holder<'static>` forces the source's region to 'static
// rather than laundering a short region into a long one through the nominal name.
func TestClassLifetimeSubtyping(t *testing.T) {
	tests := map[string]struct {
		src  string
		want string
	}{
		// Returning a Holder at a longer region forces the parameter's own argument, so the
		// caller has to supply a 'static Holder rather than any Holder at all.
		"ReturningAtALongerRegionForcesTheArgument": {
			src: `
				class Holder<'a> { peer: &'a mut {value: number} }
				fn launder<'x>(h: Holder<'x>) -> Holder<'static> { return h }
			`,
			want: "fn (h: Holder<'static>) -> Holder<'static>",
		},
		// Constructing one from a borrow forces that borrow the same way, so the region
		// reaches the constructor's argument through the class's parameter.
		"ConstructingAtALongerRegionForcesTheBorrow": {
			src: `
				class Holder<'a> { peer: &'a mut {value: number} }
				fn launder<'x>(p: &'x mut {value: number}) -> Holder<'static> { return Holder(p) }
			`,
			want: "fn (p: &'static mut {value: number}) -> Holder<'static>",
		},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			values, _, errs := inferSource(t, tc.src)
			require.Empty(t, messagesWithSpan(errs))
			require.Equal(t, tc.want, values["launder"])
		})
	}
}

// TestClassLifetimeScopeIsTheDeclaredParameters covers what a member may write without a
// clause of its own: the names the class's `<…>` binds, and nothing a field happened to
// write. Both writes of the unbound `'z` are reported, the field's against the class
// declaration it would have to be added to and the member's against its own signature.
//
// Interning a field's `'z` as a class binder instead would suppress the member's report and
// unify the two borrows, leaving the class quantified over a lifetime no reference supplies.
func TestClassLifetimeScopeIsTheDeclaredParameters(t *testing.T) {
	src := `
		class Holder<'a> {
			peer: &'a mut {value: number},
			other: &'z mut {value: number},
			put(mut self, p: &'z mut {value: number}) -> undefined { },
		}
	`
	_, _, errs := inferSource(t, src)
	require.Equal(t, []string{
		"4:12-4:14: lifetime 'z is used but not declared; did you mean 'a?",
		"5:22-5:24: lifetime 'z is used but not declared; add `<'z>` to the enclosing function signature",
	}, messagesWithSpan(errs))
}

// TestClassFieldLifetimeIsChecked covers the field scan on its own. A class with no `<…>`
// clause has no lifetime a field may write, and the hint names the class rather than a
// function signature, since the class declaration is where the binder would go.
func TestClassFieldLifetimeIsChecked(t *testing.T) {
	src := `
		class Bad {
			f: &'z mut {value: number},
			g: &'z mut {value: number},
		}
	`
	_, _, errs := inferSource(t, src)
	require.Equal(t, []string{
		"3:8-3:10: lifetime 'z is used but not declared; add `<'z>` to the enclosing class declaration",
		"4:8-4:10: lifetime 'z is used but not declared; add `<'z>` to the enclosing class declaration",
	}, messagesWithSpan(errs))
}

// TestClassLifetimeClauseErrors covers what the class's own `<…>` clause and the body
// outside a member signature can get wrong. Each name a class writes has to reach a binder
// the clause declares, and each binder has to bind something new.
func TestClassLifetimeClauseErrors(t *testing.T) {
	tests := map[string]struct {
		src  string
		want []string
	}{
		// A lifetime argument on a type reference is a written use like a borrow's, so a
		// field annotated `Cell<'z>` names 'z the way `&'z B` does.
		"ReferenceArgumentOnAField": {
			src: `
				type Cell<'c> = {v: &'c mut {value: number}}
				class Holder<'a> { peer: &'a mut {value: number}, x: Cell<'z> }
			`,
			want: []string{"3:63-3:65: lifetime 'z is used but not declared; did you mean 'a?"},
		},
		// An implements reference is a type reference too, so a lifetime argument on it is
		// checked against the same binders. So is an extends reference, which the same scan
		// walks.
		"ReferenceArgumentOnImplements": {
			src: `
				class Shape<'b> { v: &'b mut {value: number} }
				class Sub<'a> implements Shape<'z> { peer: &'a mut {value: number} }
			`,
			want: []string{"3:36-3:38: lifetime 'z is used but not declared; did you mean 'a?"},
		},
		// A name bound twice binds nothing new. Without the report the two would share one
		// variable, silently equating a caller's two distinct arguments.
		"DuplicateBinder": {
			src:  `class Holder<'a, 'a> { peer: &'a mut {value: number} }`,
			want: []string{"1:18-1:20: lifetime parameter 'a is declared more than once"},
		},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			_, _, errs := inferSource(t, tc.src)
			require.Equal(t, tc.want, messagesWithSpan(errs))
		})
	}
}

// TestClassTypeParamBoundSeesTheClassLifetime covers the order the two parameter sorts
// resolve in. A type parameter's bound may write the class's `'a`, so the type parameters
// resolve inside the class's named-lifetime scope and the bound reaches the variable the
// lifetime parameter carries rather than minting one of its own, which would render a bare
// `&` with no name.
func TestClassTypeParamBoundSeesTheClassLifetime(t *testing.T) {
	src := `class Holder<'a, T: &'a {value: number}> { peer: T }`
	values, _, errs := inferSource(t, src)
	require.Empty(t, messagesWithSpan(errs))
	require.Equal(t,
		"<T, 'a> {new (peer: T & &'a {value: number}) -> Holder<'a, T>}",
		values["Holder"],
	)
}

// TestClassLifetimeBinderOrderFollowsTheDeclaration covers the order the quantifier prefix
// names a class's lifetime parameters in. It follows the `<…>` clause rather than the order
// the fields happen to mention them, so the prefix and the instance's argument list agree.
func TestClassLifetimeBinderOrderFollowsTheDeclaration(t *testing.T) {
	src := `
		class Pair<'x, 'y> {
			b: &'y mut {value: number},
			a: &'x mut {value: number},
		}
	`
	values, _, errs := inferSource(t, src)
	require.Empty(t, messagesWithSpan(errs))
	require.Equal(t,
		"<'x, 'y> {new (b: &'y mut {value: number}, a: &'x mut {value: number}) -> Pair<'x, 'y>}",
		values["Pair"],
	)
}

// TestClassLifetimeBoundNameIsNotABinder covers the other name a class's clause mentions
// without binding: the right-hand side of an outlives bound. `<'a: 'b>` constrains 'a but
// binds only 'a, so a member writing `&'b` names a lifetime no reference can supply and is
// reported undeclared.
func TestClassLifetimeBoundNameIsNotABinder(t *testing.T) {
	src := `
		class Holder<'a: 'b> {
			peer: &'a mut {value: number},
			put(mut self, p: &'b mut {value: number}) -> undefined { },
		}
	`
	_, _, errs := inferSource(t, src)
	require.Equal(t, []string{
		"4:22-4:24: lifetime 'b is used but not declared; add `<'b>` to the enclosing function signature",
	}, messagesWithSpan(errs))
}

// TestClassLifetimeShadowing covers a member signature written against the class's lifetime
// scope. The first two cases rebind a name the class already binds: the nested binder wins,
// so `pick<'a>` and `pick<'b>` are alpha-equivalent and a call infers the same type from
// either. Seeding the member's scope from the class without dropping the rebound names would
// capture the member's `'a` as the class's, forcing a caller's argument to the class's
// region.
//
// The last two cases run the other interaction, a member that names the class's `'a` and
// binds its own `'b` in one signature. The two must stay separate, and must still admit one
// caller lifetime at both positions.
func TestClassLifetimeShadowing(t *testing.T) {
	const pickCall = `
		fn g<'y>(h: Holder<'static>, o: &'y mut {value: number}) -> undefined { h.pick(o) }
	`
	// `swap` names the class's 'a at its first parameter and binds its own 'b at the second, so
	// a call fills the two from different places: 'a from the receiver's lifetime argument, 'b
	// from the argument passed at the call.
	const swapClass = `
		class Holder<'a> {
			peer: &'a mut {value: number},
			swap<'b>(mut self, p: &'a mut {value: number}, q: &'b mut {value: number}) -> &'b mut {value: number} {
				self.peer = p
				return q
			},
		}
	`
	tests := map[string]struct {
		src  string
		want string
	}{
		"MemberRebindsTheClassName": {
			src: `
				class Holder<'a> {
					peer: &'a mut {value: number},
					pick<'a>(self, p: &'a mut {value: number}) -> &'a mut {value: number} { return p },
				}
			` + pickCall,
			want: "fn (h: Holder<'static>, o: &mut {value: number}) -> undefined",
		},
		"MemberBindsAFreshName": {
			src: `
				class Holder<'a> {
					peer: &'a mut {value: number},
					pick<'b>(self, p: &'b mut {value: number}) -> &'b mut {value: number} { return p },
				}
			` + pickCall,
			want: "fn (h: Holder<'static>, o: &mut {value: number}) -> undefined",
		},
		// The receiver fills 'a and the last argument fills 'b, so the caller quantifies two
		// lifetimes. Capturing 'b as the class's would collapse them into one and tie the
		// returned borrow to the receiver's region, which the caller never asked for.
		"MemberBindsItsOwnBesideTheClassLifetime": {
			src: swapClass + `
				fn g<'x, 'y>(h: mut Holder<'x>, p: &'x mut {value: number}, q: &'y mut {value: number}) -> &'y mut {value: number} {
					return h.swap(p, q)
				}
			`,
			want: "fn <'a, 'b>(h: mut Holder<'a>, p: &'a mut {value: number}, " +
				"q: &'b mut {value: number}) -> &'b mut {value: number}",
		},
		// Separate binders do not force separate arguments. One caller lifetime passed at both
		// positions fills 'a and 'b alike, so the caller quantifies a single lifetime.
		"ClassLifetimeFillsTheMemberBinder": {
			src: swapClass + `
				fn g<'x>(h: mut Holder<'x>, p: &'x mut {value: number}, q: &'x mut {value: number}) -> &'x mut {value: number} {
					return h.swap(p, q)
				}
			`,
			want: "fn <'a>(h: mut Holder<'a>, p: &'a mut {value: number}, " +
				"q: &'a mut {value: number}) -> &'a mut {value: number}",
		},
	}
	for name, tc := range tests {
		t.Run(name, func(t *testing.T) {
			values, _, errs := inferSource(t, tc.src)
			require.Empty(t, messagesWithSpan(errs))
			require.Equal(t, tc.want, values["g"])
		})
	}
}

// TestFuncAnnLifetimeShadowing is the annotation twin of TestClassLifetimeShadowing: a `fn`
// type written in a class body binds its own lifetimes, so one named after the class's still
// shadows it rather than being captured.
func TestFuncAnnLifetimeShadowing(t *testing.T) {
	src := `
		class Holder<'a> {
			peer: &'a mut {value: number},
			cb: fn <'a>(x: &'a mut {value: number}) -> &'a mut {value: number},
		}
		fn g<'y>(h: Holder<'static>, o: &'y mut {value: number}) -> undefined { h.cb(o) }
	`
	values, _, errs := inferSource(t, src)
	require.Empty(t, messagesWithSpan(errs))
	require.Equal(t, "fn (h: Holder<'static>, o: &mut {value: number}) -> undefined", values["g"])
}

// TestClassLifetimeStoreEdge covers the payoff: a container whose element borrow is tied to
// the receiver by a class lifetime parameter. The store lands at the whole `Holder`, since a
// class lifetime argument names no field, so a borrow written into a Holder the caller owns
// escapes the frame.
func TestClassLifetimeStoreEdge(t *testing.T) {
	const decls = `
		class Holder<'a> { peer: &'a mut {value: number} }
		declare fn store<'a, 'c>(target: &'c mut Holder<'a>, item: &'a mut {value: number}) -> undefined
	`
	src := decls + `
		fn build(h: mut Holder<'static>) -> undefined {
			val mut b = {value: 2}
			store(&mut h, &mut b)
		}
	`
	values, _, errs := inferSource(t, src)
	require.Equal(t, []string{
		"7:18-7:24: borrowed value 'b' does not live long enough to escape the function",
	}, messagesWithSpan(errs))
	require.Equal(t,
		"fn <'a>(target: &mut Holder<'a>, item: &'a mut {value: number}) -> undefined",
		values["store"],
	)
}
