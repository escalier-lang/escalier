package ecma262

import (
	"testing"

	"github.com/gkampitakis/go-snaps/snaps"
	"github.com/stretchr/testify/require"
)

// originsOf computes the origin map of one builtin or abstract operation.
func originsOf(t *testing.T, name string) *OriginMap {
	t.Helper()
	cfg := testCFG(t)
	fn := cfg.Builtin(name)
	if fn == nil {
		fn = cfg.AbstractOp(name)
	}
	require.NotNil(t, fn, "no function named %s", name)
	return NewOriginMap(fn)
}

func TestOriginJoin(t *testing.T) {
	tests := map[string]struct {
		left, right, want Origin
	}{
		"UnsetTakesTheOther":       {Origin{}, Receiver, Receiver},
		"OtherTakesUnset":          {Fresh, Origin{}, Fresh},
		"EqualOriginsSurvive":      {Param(1), Param(1), Param(1)},
		"DifferentIndicesCollapse": {Param(0), Param(1), Unknown},
		"DifferentKindsCollapse":   {Receiver, Fresh, Unknown},
		"UnknownAbsorbs":           {Unknown, Receiver, Unknown},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			require.Equal(t, test.want, test.left.join(test.right))
			require.Equal(t, test.want, test.right.join(test.left))
		})
	}
}

func TestOriginString(t *testing.T) {
	require.Equal(t, "Receiver", Receiver.String())
	require.Equal(t, "Param(2)", Param(2).String())
	require.Equal(t, "Fresh", Fresh.String())
	require.Equal(t, "Unknown", Unknown.String())
	require.Equal(t, "unset", Origin{}.String())
}

func TestOriginMapSampleFunctions(t *testing.T) {
	tests := map[string]struct {
		fn      string
		origins map[string]string
	}{
		// `Let O be ? ToObject(this value)` carries the receiver into `O`. That
		// is what makes push come out as a receiver mutation once §4.1 reads
		// its `Set(O, ...)` call.
		"ReceiverThroughToObject": {
			fn: "Array.prototype.push",
			origins: map[string]string{
				"O":     "Receiver",
				"items": "Param(0)",
				"E":     "Unknown", // read out of the argument list
				"len":   "Unknown", // ? LengthOfArrayLike(O)
			},
		},
		// slice writes only into the array ArraySpeciesCreate handed it, so
		// every write it makes lands on a `Fresh` value and none of them
		// reaches the receiver.
		"AllocatorsAreFresh": {
			fn: "Array.prototype.slice",
			origins: map[string]string{
				"O":     "Receiver",
				"A":     "Fresh", // ? ArraySpeciesCreate(O, count)
				"start": "Param(0)",
				"end":   "Param(1)",
			},
		},
		// A String method reaches its receiver through RequireObjectCoercible,
		// which preserves identity, and then coerces it with ToString, which
		// does not. `S` is a fresh string the algorithm never writes back
		// through, which is why `String.prototype` methods come out
		// non-mutating.
		"ValueCoercionsBreakTheChain": {
			fn: "String.prototype.toLowerCase",
			origins: map[string]string{
				"O": "Receiver",
				"S": "Unknown",
			},
		},
		// A static has no receiver. Its `this` is the constructor object, so
		// nothing here is `Receiver`, and the object assign writes into sits
		// at a real parameter position.
		"StaticHasNoReceiver": {
			fn: "Object.assign",
			origins: map[string]string{
				"target":  "Param(0)",
				"to":      "Param(0)", // ? ToObject(target)
				"sources": "Param(1)",
				"from":    "Unknown", // ? ToObject(nextSource), off a list read
			},
		},
		// Map.prototype.set reaches its receiver straight off `this` rather
		// than through a coercion, and CanonicalizeKeyedCollectionKey passes
		// the key through untouched.
		"ReceiverStraightFromThis": {
			fn: "Map.prototype.set",
			origins: map[string]string{
				"M":     "Receiver",
				"key":   "Param(0)",
				"value": "Param(1)",
			},
		},
		// An abstract operation has no receiver either. An ExprThis inside one
		// resolves to `Unknown` rather than to a value its caller owns.
		"AbstractOperationHasNoReceiver": {
			fn: "OrdinaryToPrimitive",
			origins: map[string]string{
				"O":    "Param(0)",
				"hint": "Param(1)",
			},
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			m := originsOf(t, test.fn)
			for value, want := range test.origins {
				require.Equal(t, want, m.Of(value).String(), "origin of %s", value)
			}
		})
	}
}

// A name bound on two branches takes the join of both definitions. In
// `Map.prototype.set`, `p` is the entry holding the key. One branch reads it
// out of the receiver's [[MapData]] list and the other allocates it as a fresh
// record. The two disagree, so `p` collapses to `Unknown`. §4.1 then reads a
// write to `p` as a mutation it cannot attribute, which leaves the method
// unclassified rather than claiming an effect it cannot place.
func TestOriginMapJoinsBranches(t *testing.T) {
	m := originsOf(t, "Map.prototype.set")
	require.Equal(t, Unknown, m.Of("p"))
}

// A name the function never binds reads back as `Unknown`, not as the lattice
// bottom.
func TestOriginMapUnboundName(t *testing.T) {
	m := originsOf(t, "Array.prototype.push")
	require.Equal(t, Unknown, m.Of("nosuchvalue"))
}

func TestOriginMapEval(t *testing.T) {
	m := originsOf(t, "Array.prototype.push")

	require.Equal(t, Receiver, m.Eval(&ThisExpr{}))
	require.Equal(t, Receiver, m.Eval(&VarExpr{Var: "O"}))
	require.Equal(t, Fresh, m.Eval(&LitExpr{}))
	require.Equal(t, Fresh, m.Eval(&AllocExpr{Args: nil}))
	require.Equal(t, Unknown, m.Eval(nil))

	// A read off the receiver is a different value from the receiver.
	readO := &SlotExpr{Object: &VarExpr{Var: "O"}, Slot: "MapData"}
	require.Equal(t, Unknown, m.Eval(readO))

	// A nested call resolves through the same lists a call node does.
	toObject := &CallExpr{Callee: "ToObject", Args: []Expr{&ThisExpr{}}}
	require.Equal(t, Receiver, m.Eval(toObject))
	arrayCreate := &CallExpr{Callee: "ArrayCreate", Args: []Expr{&LitExpr{}}}
	require.Equal(t, Fresh, m.Eval(arrayCreate))
	get := &CallExpr{Callee: "Get", Args: []Expr{&VarExpr{Var: "O"}}}
	require.Equal(t, Unknown, m.Eval(get))
}

// The walk repeats until nothing moves, so a name a loop's back edge redefines
// takes the join of both definitions rather than whichever the serializer
// emitted first. ForBodyEvaluation binds `%4` from a literal ahead of its loop
// and from a value read inside it. A single walk in node order would leave `%4`
// at `Fresh`, which tells §4.1 that a mutation of it is invisible to the
// caller.
func TestOriginMapReachesFixpointAcrossBackEdges(t *testing.T) {
	m := originsOf(t, "ForBodyEvaluation")
	require.Equal(t, Unknown, m.Of("%4"))
}

func TestOriginMapString(t *testing.T) {
	m := originsOf(t, "Map.prototype.set")
	snaps.MatchInlineSnapshot(t, m.String(), snaps.Inline(`%0: Unknown
%1: Param(0)
%2: Fresh
%3: Unknown
%4: Fresh
%5: Unknown
%6: Receiver
%7: Receiver
M: Receiver
key: Param(0)
p: Unknown
value: Param(1)
`))
}

// Every function in the committed graph analyzes, and two invariants hold
// across all of them. No name is left at the lattice bottom, so every name the
// walk bound reached a definition it could evaluate. A declared parameter's own
// name reads back as its own position or as `Unknown`. It can never read back
// as an origin the walk invented, because the only other definitions of that
// name are assignments the algorithm makes to it, and those join to
// `Unknown`.
func TestOriginMapCoversEveryFunction(t *testing.T) {
	cfg := testCFG(t)

	for _, fn := range cfg.Funcs {
		m := NewOriginMap(fn)
		require.Same(t, fn, m.Func())
		for _, name := range m.Names() {
			require.NotEqual(t, originUnset, m.origins[name].Kind, "%s: %s left unset", fn.Name, name)
		}
		for i, param := range fn.Params {
			require.Contains(t, []Origin{Param(i), Unknown}, m.Of(param), "%s: %s", fn.Name, param)
		}
	}
}
