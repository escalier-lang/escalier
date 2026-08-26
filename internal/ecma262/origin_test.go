package ecma262

import (
	"sort"
	"testing"

	"github.com/escalier-lang/escalier/internal/set"
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
	// Origin{} is the zero value, whose kind is originUnset. That is the bottom
	// of the lattice, a name no definition has bound yet. Joining it with an
	// origin yields that origin, which is what lets a definition the walk has
	// not evaluated yet contribute nothing to the name it defines instead of
	// pinning it at Unknown. The three origins above the bottom have
	// package-level names, so only this one is written as a literal.
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
	require.Equal(t, "Interior(Receiver)", interiorOf(Receiver).String())
	require.Equal(t, "Interior(Param(2))", interiorOf(Param(2)).String())
	require.Equal(t, "Fresh(captures)", Origin{Kind: OriginFresh, Captures: true}.String())

	// A kind outside the lattice renders as its number rather than as one of
	// the names above, so an origin the walk could not produce is still legible
	// in a failing assertion.
	require.Equal(t, "Origin(99)", Origin{Kind: 99}.String())
}

// A receiver's or a parameter's interior is marked and stays placed. A fresh
// object's interior is fresh too, since the algorithm made its contents as
// well, unless the allocator captured one of its arguments.
func TestInteriorOf(t *testing.T) {
	require.Equal(t, Origin{Kind: OriginReceiver, Interior: true}, interiorOf(Receiver))
	require.Equal(t, Origin{Kind: OriginParam, Index: 1, Interior: true}, interiorOf(Param(1)))
	require.Equal(t, Fresh, interiorOf(Fresh))
	require.Equal(t, Unknown, interiorOf(Origin{Kind: OriginFresh, Captures: true}))
	require.Equal(t, Unknown, interiorOf(Unknown))

	// The lattice bottom stays at the bottom. A name whose definition the walk
	// has not reached yet takes its origin on the next pass, and answering
	// `Unknown` here would pin it for good.
	require.Equal(t, Origin{}, interiorOf(Origin{}))

	// Reading a backing-store slot off an interior value keeps the same base.
	require.Equal(t, interiorOf(Receiver), interiorOf(interiorOf(Receiver)))
}

// An interior value is not the object that holds it, so the two never join into
// one origin. §4.3 reads a return alias off this map, and returning
// `M.[[MapData]]` is not returning `M`.
func TestInteriorIsNotItsHolder(t *testing.T) {
	require.NotEqual(t, Receiver, interiorOf(Receiver))
	require.Equal(t, Unknown, Receiver.join(interiorOf(Receiver)))
}

// A property read keeps an interior origin and breaks every other chain. The
// entries of a List a collection keeps in a backing-store slot are inside that
// collection, so a write to one is a write to the collection.
func TestInsideOf(t *testing.T) {
	require.Equal(t, interiorOf(Receiver), insideOf(interiorOf(Receiver)))
	require.Equal(t, interiorOf(Param(1)), insideOf(interiorOf(Param(1))))

	// Reading a property off the object itself yields a different object.
	require.Equal(t, Unknown, insideOf(Receiver))
	require.Equal(t, Unknown, insideOf(Param(1)))
	require.Equal(t, Unknown, insideOf(Fresh))
	require.Equal(t, Unknown, insideOf(Unknown))

	// The lattice bottom stays at the bottom, for the reason TestInteriorOf
	// gives.
	require.Equal(t, Origin{}, insideOf(Origin{}))
}

// Reading a backing-store slot off a name the walk has not bound yet resolves
// on a later pass rather than sticking at `Unknown`, so the result does not
// depend on the order the serializer emitted the nodes in. Here `buf` reads the
// receiver's payload one node before `O` is bound to the receiver.
func TestOriginMapInteriorIsOrderIndependent(t *testing.T) {
	cfg, err := ParseCFG([]byte(
		`{"specTarget":"abc","funcs":[{"name":"Demo","kind":"builtin-method","params":[],"nodes":[` +
			`{"kind":"let","target":"buf","source":{"kind":"slot","object":{"kind":"var","var":"O"},"slot":"MapData"}},` +
			`{"kind":"let","target":"O","source":{"kind":"this"}}]}]}`))
	require.NoError(t, err)

	m := NewOriginMap(cfg.Builtin("Demo"))
	require.Equal(t, Receiver, m.Of("O"))
	require.Equal(t, interiorOf(Receiver), m.Of("buf"))
}

// A value read out of a backing-store slot is charged to the object holding it,
// even when a name binds it first. `SetTypedArrayFromTypedArray` opens with
// `Let targetBuffer be target.[[ViewedArrayBuffer]]` and passes `targetBuffer`
// to the byte store several steps later.
func TestOriginMapInteriorThroughALetBinding(t *testing.T) {
	m := originsOf(t, "SetTypedArrayFromTypedArray")

	require.Equal(t, Param(0), m.Of("target"))
	require.Equal(t, interiorOf(Param(0)), m.Of("targetBuffer"))
}

func TestOriginMapSampleFunctions(t *testing.T) {
	tests := map[string]struct {
		fn string
		// origins names the values each case is about, not every value the
		// function binds. TestOriginMapString below prints a whole map.
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
		// does not. `S` is the new string ToString built, so it is `Fresh` and
		// the receiver is not in it. That is what makes every
		// `String.prototype` method come out non-mutating.
		"ValueCoercionsBreakTheChain": {
			fn: "String.prototype.toLowerCase",
			origins: map[string]string{
				"O": "Receiver",
				"S": "Fresh",
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

	// A backing-store slot holds the receiver's own payload, so the value read
	// out of one is the receiver's interior rather than an unplaceable value.
	readData := &SlotExpr{Object: &VarExpr{Var: "O"}, Slot: "MapData"}
	require.Equal(t, Origin{Kind: OriginReceiver, Interior: true}, m.Eval(readData))

	// Any other slot read is a different value from the receiver.
	readOther := &SlotExpr{Object: &VarExpr{Var: "O"}, Slot: "Prototype"}
	require.Equal(t, Unknown, m.Eval(readOther))

	// A property read off that payload stays inside the receiver, while one off
	// the receiver itself yields a different object.
	readEntry := &PropExpr{Object: readData}
	require.Equal(t, Origin{Kind: OriginReceiver, Interior: true}, m.Eval(readEntry))
	readProp := &PropExpr{Object: &VarExpr{Var: "O"}}
	require.Equal(t, Unknown, m.Eval(readProp))

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

// A name the function reads but never binds is Unknown from the first pass, not
// a value that drops silently out of a join. The closure behind
// `Iterator.prototype.drop` opens with `Let remaining be integerLimit`, where
// `integerLimit` belongs to the algorithm that built the closure, and a later
// step assigns `remaining` a literal. Treating the captured name as nothing to
// join would leave `remaining` at Fresh and make a mutation of it read as
// unobservable.
func TestOriginMapFreeNames(t *testing.T) {
	m := originsOf(t, "INTRINSICS.Iterator.prototype.drop:clo0")

	require.Equal(t, Unknown, m.Of("integerLimit"))
	require.Equal(t, Unknown, m.Of("remaining"))
}

// capturingAllocators is derived from the graph, so this recomputes it. An
// allocator captures when one of its parameters reaches a place interiorOf
// would read it back from: the operands of an allocation it builds, or the
// value it writes into a backing-store slot.
func TestCapturingAllocatorsMatchTheGraph(t *testing.T) {
	cfg := testCFG(t)

	derived := set.NewSet[string]()
	for _, name := range allocators.ToSlice() {
		fn := cfg.AbstractOp(name)
		require.NotNil(t, fn, "no abstract operation named %s", name)
		if capturesAnArgument(fn) {
			derived.Add(name)
		}
	}

	require.Equal(t, sorted(capturingAllocators), sorted(derived))
}

// capturesAnArgument reports whether one of fn's parameters can be read back
// out of the value fn allocates.
//
// A name is at a parameter when fn's own origin map says so, which is how a
// parameter reached through an intermediate binding still counts. Matching a
// parameter's spelling alone would miss `Let b be obj` followed by an
// allocation over `b`.
func capturesAnArgument(fn *Func) bool {
	origins := NewOriginMap(fn)
	var reaches func(Expr) bool
	reaches = func(e Expr) bool {
		switch e := e.(type) {
		case *VarExpr:
			return origins.Of(e.Var).Kind == OriginParam
		case *AllocExpr:
			for _, arg := range e.Args {
				if reaches(arg) {
					return true
				}
			}
		case *CallExpr:
			for _, arg := range e.Args {
				if reaches(arg) {
					return true
				}
			}
		case *SlotExpr:
			return reaches(e.Object)
		case *PropExpr:
			return reaches(e.Object)
		}
		return false
	}

	for _, node := range fn.Nodes {
		switch node := node.(type) {
		case *ReturnNode:
			if alloc, ok := node.Value.(*AllocExpr); ok && reaches(alloc) {
				return true
			}
		case *LetNode:
			if alloc, ok := node.Source.(*AllocExpr); ok && reaches(alloc) {
				return true
			}
		case *SlotWriteNode:
			if backingStoreSlots.Contains(node.Slot) && reaches(node.Value) {
				return true
			}
		}
	}
	return false
}

func sorted(s set.Set[string]) []string {
	names := s.ToSlice()
	sort.Strings(names)
	return names
}

// A write to the buffer behind a freshly allocated typed array is invisible to
// the caller, which is what makes `TypedArray.prototype.slice` non-mutating.
// `A` comes from `TypedArraySpeciesCreate`, which captures nothing, so
// `A.[[ViewedArrayBuffer]]` stays fresh while `O.[[ViewedArrayBuffer]]` is the
// receiver's interior.
func TestOriginMapInteriorOfAFreshAllocation(t *testing.T) {
	m := originsOf(t, "TypedArray.prototype.slice")

	require.Equal(t, Fresh, m.Of("A"))
	require.Equal(t, Fresh, m.Of("targetBuffer"))
	require.Equal(t, interiorOf(Receiver), m.Of("srcBuffer"))
}

// A record read out of a backing store keeps the holder's interior origin, so
// §4.1 can charge a write to that record to the holder. `Map.prototype.delete`
// binds `p` from a read of `M.[[MapData]]` and then empties it with `Set
// p.[[Key]] to EMPTY`.
func TestOriginMapRecordReadOutOfABackingStore(t *testing.T) {
	m := originsOf(t, "Map.prototype.delete")

	require.Equal(t, Receiver, m.Of("M"))
	require.Equal(t, interiorOf(Receiver), m.Of("p"))
}

func TestOriginMapString(t *testing.T) {
	m := originsOf(t, "Map.prototype.set")
	snaps.MatchInlineSnapshot(t, m.String(), snaps.Inline(`%0: Unknown
%1: Param(0)
%2: Fresh
%3: Interior(Receiver)
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
