package solver

import (
	"testing"

	"github.com/escalier-lang/escalier/internal/set"
	"github.com/escalier-lang/escalier/internal/soltype"
	"github.com/stretchr/testify/require"
)

// muKnot builds `μ<name>.<body>` where body is produced from the binder, so a test reads as the μ
// form it asserts. The callback receives the binder's reference node, which is the node the body
// must name for the knot to be recursive.
func muKnot(id int, name string, body func(ref *soltype.RecursiveVarType) soltype.Type) *soltype.RecursiveType {
	binder := &soltype.RecursiveVarType{ID: id, Name: name}
	return &soltype.RecursiveType{Binder: binder, Body: body(binder)}
}

// TestInferRecursiveRendersMuKnot is the milestone's headline case. A cyclic bound graph coalesces
// to a μ-knot, so a recursive position names the shape it stands for rather than collapsing to the
// polarity identity, which is never in covariant position and unknown in contravariant.
//
// Each source builds the cycle a different way:
//
//   - the recursive call inside an object literal makes the return variable's own lower bound
//     mention it;
//   - the tuple case does the same through a positional element;
//   - the two-field case carries a retained type parameter beside the recursive field, so the knot
//     and the quantifier coexist;
//   - the mutual pair runs the cycle through a second binding.
//
// The one unrolled level in front of each knot is a monomorphic-recursion artifact. Each call site
// instantiates its own return variable, so the outer shape comes from the call and the knot from the
// variable the body's recursive call flows through.
func TestInferRecursiveRendersMuKnot(t *testing.T) {
	tests := []struct {
		name    string
		src     string
		binding string
		want    string
	}{
		{
			name:    "recursion through an object property",
			src:     `fn f() { return {next: f()} }`,
			binding: "f",
			want:    "fn () -> {next: μX0.{next: X0}}",
		},
		{
			name:    "recursion through a tuple element",
			src:     `fn f() { return [f()] }`,
			binding: "f",
			want:    "fn () -> [μX0.[X0]]",
		},
		{
			name:    "knot beside a retained type parameter",
			src:     `fn f(x) { return {next: f(x), value: x} }`,
			binding: "f",
			want:    "fn <T0>(x: T0) -> {next: μX0.{next: X0, value: T0}, value: T0}",
		},
		{
			// Mutual recursion runs the same cycle through two bindings, so the knot closes one lap
			// out at whichever binding is being rendered.
			name: "mutual recursion closes the knot one lap out",
			src: `
				fn ping() { return {p: pong()} }
				fn pong() { return {q: ping()} }
			`,
			binding: "ping",
			want:    "fn () -> {p: μX0.{q: {p: X0}}}",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			values, _, errs := inferSource(t, tt.src)
			require.Empty(t, Messages(errs))
			require.Equal(t, tt.want, values[tt.binding])
		})
	}
}

// TestInferRecursiveThroughSourcePaths runs a recursive type through the value paths a program
// actually uses. The cases split into two groups, which reach the knot differently.
//
// Reassignment is the one path that feeds a COALESCED knot back into the solver. A reassignable
// binding's stored type is its display type, so inferAssign copies that with freshenAll and
// constrains the new value against the copy, and `a = f()` compares one knot against another through
// constrain's unfolding. Removing the RecursiveType arm from evalTypeOperator fails exactly these
// two cases, with `cannot constrain object <: μX0.object` and its tuple twin. Both carriers are
// covered because constrain decomposes objects and tuples in separate arms.
//
// A read and a generic call run on the RAW bound graph instead. instantiate freshens a scheme's
// Body, not its coalesced display, so no knot exists while the member chain, the destructuring
// pattern, or the type parameter is being solved. Those cases pin that the recursive shape survives
// the round trip and still renders as a knot once the resulting binding is displayed.
func TestInferRecursiveThroughSourcePaths(t *testing.T) {
	tests := []struct {
		name    string
		src     string
		binding string
		want    string
	}{
		{
			name:    "reassigning a recursive binding compares two knots",
			src:     "fn f() { return {next: f()} }\nfn h() { var a = f()\n a = f()\n return a }",
			binding: "h",
			want:    "fn () -> {next: μX0.{next: X0}}",
		},
		{
			name:    "a member chain over a recursive result still renders a knot",
			src:     "fn f() { return {next: f()} }\nval c = f().next.next",
			binding: "c",
			want:    "μX0.{next: X0}",
		},
		{
			name:    "a recursive result through a type parameter still renders a knot",
			src:     "fn f() { return {next: f()} }\nfn id(x) { return x }\nval d = id(f())",
			binding: "d",
			want:    "{next: μX0.{next: X0}}",
		},
		{
			// The tuple shape reaches the same reassignment path, so a coalesced knot over a tuple
			// unfolds and closes the way one over an object does, through constrain's tuple arm.
			name:    "reassigning a recursive tuple binding compares two knots",
			src:     "fn f() { return [f()] }\nfn h() { var a = f()\n a = f()\n return a }",
			binding: "h",
			want:    "fn () -> [μX0.[X0]]",
		},
		{
			// Destructuring is how a recursive tuple is read apart, since a value-level index
			// expression is unsupported for any tuple, recursive or not.
			name:    "destructuring a recursive tuple still renders a knot",
			src:     "fn f() { return [f()] }\nval [inner] = f()",
			binding: "inner",
			want:    "μX0.[X0]",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			values, _, errs := inferSource(t, tt.src)
			require.Empty(t, Messages(errs))
			require.Equal(t, tt.want, values[tt.binding])
		})
	}
}

// TestCoalesceRecursiveVarPolarities pins the two coalescing rules directly on a hand-built cyclic
// bound graph, without the extra variable layer a real call site introduces.
//
// A variable whose bound mentions itself in the SAME polarity it was entered at ties a knot. One
// that only comes back at the OPPOSITE polarity cannot. The body was built from the variable's
// bounds in one direction, and naming it from a position that needs the other direction would claim
// the wrong type. That case keeps the polarity identity.
func TestCoalesceRecursiveVarPolarities(t *testing.T) {
	t.Run("same-polarity cycle ties a knot", func(t *testing.T) {
		c := &Context{}
		v := c.freshVar(0)
		v.LowerBounds = []soltype.Type{exactObj(propElem("next", v))}
		require.Equal(t, "μX0.{next: X0}", soltype.Print(coalesce(v, soltype.Positive)))
	})

	t.Run("negative-position cycle ties a knot too", func(t *testing.T) {
		c := &Context{}
		v := c.freshVar(0)
		v.UpperBounds = []soltype.Type{exactObj(propElem("next", v))}
		require.Equal(t, "μX0.{next: X0}", soltype.Print(coalesce(v, soltype.Negative)))
	})

	t.Run("cycle through a contravariant position keeps the polarity identity", func(t *testing.T) {
		// v's lower bound is `fn (v) -> number`, so the walk enters v covariantly and comes back to
		// it contravariantly at the parameter. Inlining there would need v's UPPER bounds, which the
		// body under construction does not hold, so the parameter renders unknown.
		c := &Context{}
		v := c.freshVar(0)
		v.LowerBounds = []soltype.Type{&soltype.FuncType{
			Params: []*soltype.FuncParam{identParam("x", v)},
			Ret:    num(),
		}}
		require.Equal(t, "fn (x: unknown) -> number", soltype.Print(coalesce(v, soltype.Positive)))
	})

	t.Run("a bare self-edge pins no type and keeps the polarity identity", func(t *testing.T) {
		// `μX0.X0` has no unfolding that mentions a type, so the knot collapses.
		c := &Context{}
		v := c.freshVar(0)
		v.LowerBounds = []soltype.Type{v}
		require.Equal(t, "never", soltype.Print(coalesce(v, soltype.Positive)))
	})
}

// TestEqualTypeRecursive pins the alpha-equivalence rule: two knots are equal when their bodies
// match under a pairing of their binders, so binder ids and names carry no weight. The bijection
// must be consistent, so two knots that name their binders at different positions are not equal.
func TestEqualTypeRecursive(t *testing.T) {
	selfNext := func(id int, name string) soltype.Type {
		return muKnot(id, name, func(ref *soltype.RecursiveVarType) soltype.Type {
			return exactObj(propElem("next", ref))
		})
	}
	tests := []struct {
		name string
		a, b soltype.Type
		want bool
	}{
		{
			name: "alpha-equivalent knots are equal",
			a:    selfNext(0, "X0"),
			b:    selfNext(4, "X1"),
			want: true,
		},
		{
			name: "differing bodies are not equal",
			a:    selfNext(0, "X0"),
			b: muKnot(0, "X0", func(ref *soltype.RecursiveVarType) soltype.Type {
				return exactObj(propElem("prev", ref))
			}),
			want: false,
		},
		{
			name: "a knot never equals its own one-level unfolding",
			a:    selfNext(0, "X0"),
			b:    exactObj(propElem("next", selfNext(0, "X0"))),
			want: false,
		},
		{
			// Both bodies hold two fields naming a binder, but a names its own at `here` where b
			// names its own at `there`, so no consistent pairing covers both.
			name: "an inconsistent binder pairing is not equal",
			a: muKnot(0, "X0", func(outer *soltype.RecursiveVarType) soltype.Type {
				return muKnot(1, "X1", func(inner *soltype.RecursiveVarType) soltype.Type {
					return exactObj(propElem("here", outer), propElem("there", inner))
				})
			}),
			b: muKnot(0, "X0", func(outer *soltype.RecursiveVarType) soltype.Type {
				return muKnot(1, "X1", func(inner *soltype.RecursiveVarType) soltype.Type {
					return exactObj(propElem("here", inner), propElem("there", outer))
				})
			}),
			want: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			require.Equal(t, tt.want, equalType(tt.a, tt.b))
			require.Equal(t, tt.want, equalType(tt.b, tt.a), "equality must be symmetric")
		})
	}
}

// TestConstrainRecursiveUnfolds pins the constrain arm. A knot is transparent, so a constraint on
// one runs against its one-level unfolding. Two knots compared against each other close
// coinductively through the seen-set. Unfolding substitutes the knot's own pointer, so the pair the
// recursion returns to is the pair the seen-set already holds.
func TestConstrainRecursiveUnfolds(t *testing.T) {
	selfNext := func() soltype.Type {
		return muKnot(0, "X0", func(ref *soltype.RecursiveVarType) soltype.Type {
			return exactObj(propElem("next", ref))
		})
	}

	t.Run("two alpha-equivalent knots are mutual subtypes", func(t *testing.T) {
		// The two knots number their binders differently, so a comparison that only succeeded on
		// identical operands would not settle either direction. Each direction runs on its own
		// Context, so neither can be decided by state the other left behind.
		left := muKnot(0, "X0", func(ref *soltype.RecursiveVarType) soltype.Type {
			return exactObj(propElem("next", ref))
		})
		right := muKnot(4, "X1", func(ref *soltype.RecursiveVarType) soltype.Type {
			return exactObj(propElem("next", ref))
		})
		require.Empty(t, Messages((&Context{}).Constrain(left, right)))
		require.Empty(t, Messages((&Context{}).Constrain(right, left)))
	})

	t.Run("a knot satisfies its own unfolding and the reverse", func(t *testing.T) {
		c := &Context{}
		unfolded := exactObj(propElem("next", selfNext()))
		require.Empty(t, Messages(c.Constrain(selfNext(), unfolded)))
		require.Empty(t, Messages(c.Constrain(unfolded, selfNext())))
	})

	t.Run("a knot rejects a structural type its unfolding does not match", func(t *testing.T) {
		// The knot unfolds before the comparison, so the rejection is reported at the property where
		// the unfolded shape and the target disagree, naming the operands describe reached there.
		c := &Context{}
		errs := c.Constrain(selfNext(), exactObj(propElem("next", num())))
		require.Equal(t, []string{"cannot constrain object <: number"}, Messages(errs))
	})

	t.Run("a knot flows into a variable as one lower bound", func(t *testing.T) {
		// The pre-switch only unwraps an operator when the other side is concrete, so a variable
		// super records the whole knot and the binding renders as the μ form rather than its
		// unfolding.
		c := &Context{}
		v := c.freshVar(0)
		require.Empty(t, Messages(c.Constrain(selfNext(), v)))
		require.Equal(t, "μX0.{next: X0}", soltype.Print(coalesce(v, soltype.Positive)))
	})
}

// TestDescribeRecursive pins the raw mid-constrain renderer's knot arms, so a diagnostic naming a
// knot reads as the μ form rather than describe's default `?`. describe is the second per-node type
// renderer beside soltype.Print, and both must carry every kind.
func TestDescribeRecursive(t *testing.T) {
	knot := muKnot(0, "X0", func(ref *soltype.RecursiveVarType) soltype.Type {
		return exactObj(propElem("next", ref), propElem("value", num()))
	})
	require.Equal(t, "μX0.object", describe(knot))
	require.Equal(t, "X0", describe(knot.Binder))
	require.Equal(t, "r7", describe(&soltype.RecursiveVarType{ID: 7}))
}

// TestExtrudeRecursiveKeepsBinder pins that a knot crosses a level boundary intact. LevelOf reads
// the body and skips the binder, so the prune descends to freshen the body's out-of-level variable
// while the binder and every reference to it are left alone. If the binder were an inference
// variable, extrude would freshen it and desync it from its uses in the body.
func TestExtrudeRecursiveKeepsBinder(t *testing.T) {
	c := &Context{}
	inner := c.freshVar(3)
	original := muKnot(0, "X0", func(ref *soltype.RecursiveVarType) soltype.Type {
		return exactObj(propElem("next", ref), propElem("value", inner))
	})

	out := c.extrude(original, soltype.Positive, 0, map[extrudeKey]*soltype.TypeVarType{})
	extruded, ok := out.(*soltype.RecursiveType)
	require.True(t, ok, "extrusion must yield a knot, got %T", out)
	require.Same(t, original.Binder, extruded.Binder)

	body, ok := extruded.Body.(*soltype.ObjectType)
	require.True(t, ok, "the knot's body must stay an object, got %T", extruded.Body)
	next, ok := body.Prop("next")
	require.True(t, ok)
	require.Same(t, original.Binder, next.Type, "the binder reference must stay bound to the binder")

	value, ok := body.Prop("value")
	require.True(t, ok)
	fresh, ok := value.Type.(*soltype.TypeVarType)
	require.True(t, ok, "the out-of-level variable must be freshened, got %T", value.Type)
	require.NotSame(t, inner, fresh)
	require.Equal(t, 0, fresh.Level)
}

// TestUnfoldRecursiveShadowing pins the substitution's binding discipline: a nested knot rebinding
// the same id shadows the outer binding, so its references stay bound to it. Coalescing numbers
// binders per walk, so two walks whose display types are composed into one type can each contribute
// a knot bound to id 0, which is what makes the case worth guarding.
func TestUnfoldRecursiveShadowing(t *testing.T) {
	shadowed := muKnot(0, "X0", func(outer *soltype.RecursiveVarType) soltype.Type {
		return exactObj(
			propElem("outer", outer),
			propElem("shadow", muKnot(0, "X0", func(inner *soltype.RecursiveVarType) soltype.Type {
				return exactObj(propElem("inner", inner))
			})),
		)
	})
	require.Equal(t,
		"{outer: μX0.{outer: X0, shadow: μX0.{inner: X0}}, shadow: μX0.{inner: X0}}",
		soltype.Print(unfoldRecursive(shadowed)))
}

// TestConstrainRecursiveSeenSetCloses pins that the coinductive close is what makes a recursive
// comparison terminate rather than the unwrap budget. A budget cut-off would surface an
// ExpansionLimitError, so an empty error list proves the seen-set closed the derivation.
func TestConstrainRecursiveSeenSetCloses(t *testing.T) {
	c := &Context{}
	left := muKnot(0, "X0", func(ref *soltype.RecursiveVarType) soltype.Type {
		return exactObj(propElem("next", ref), propElem("value", num()))
	})
	right := muKnot(1, "X1", func(ref *soltype.RecursiveVarType) soltype.Type {
		return exactObj(propElem("next", ref), propElem("value", num()))
	})
	require.Empty(t, Messages(c.constrain(left, right, set.NewSet[constraintKey](), false)))
	require.Equal(t, 0, c.unwrapDepth, "the unwrap budget must unwind to zero")
}
