package ecma262

import (
	"fmt"
	"slices"
	"sort"
	"strings"
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
		// Fresh on one path and shallow-fresh on the other is the one pair of
		// origins that disagrees without collapsing. Writing the value is
		// invisible to the caller on both paths, so only what it holds is in
		// doubt.
		"FreshAndShallowFreshMeetAtShallow": {Fresh, ShallowFresh, ShallowFresh},
		"UnknownAbsorbs":                    {Unknown, Receiver, Unknown},
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
	require.Equal(t, "Fresh(shallow)", ShallowFresh.String())

	// A kind outside the lattice renders as its number rather than as one of
	// the names above, so an origin the walk could not produce is still legible
	// in a failing assertion.
	require.Equal(t, "Origin(99)", Origin{Kind: 99}.String())
}

// A receiver's or a parameter's interior is marked and stays placed. A fresh
// object's interior is fresh too, since the algorithm made its contents as
// well, unless the allocation is shallow.
func TestInteriorOf(t *testing.T) {
	require.Equal(t, Origin{Kind: OriginReceiver, Interior: true}, interiorOf(Receiver))
	require.Equal(t, Origin{Kind: OriginParam, Index: 1, Interior: true}, interiorOf(Param(1)))
	require.Equal(t, Fresh, interiorOf(Fresh))
	require.Equal(t, Unknown, interiorOf(ShallowFresh))
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
				"O": "Receiver",
				// The rest parameter is the List the call built, not a value
				// the caller passed. TestOriginMapRestParameter covers it.
				"items": "Fresh(shallow)",
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
				"to":      "Param(0)",       // ? ToObject(target)
				"sources": "Fresh(shallow)", // the rest parameter's List
				"from":    "Unknown",        // ? ToObject(nextSource), off a list read
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

// A rest parameter names the List the call builds out of the arguments the
// head does not name one by one, so it is fresh and holds values the caller
// owns. `Array.prototype.concat` prepends its receiver onto that List, and
// §4.1 discards the write instead of losing track of what it reached.
//
// `Object.assign ( target, ...sources )` declares its rest parameter after an
// ordinary one, so the seed reads the position off the head rather than
// assuming 0.
func TestOriginMapRestParameter(t *testing.T) {
	concat := originsOf(t, "Array.prototype.concat")
	require.Equal(t, ShallowFresh, concat.Of("items"))
	require.Equal(t, Receiver, concat.Of("O"))

	assign := originsOf(t, "Object.assign")
	require.Equal(t, Param(0), assign.Of("target"))
	require.Equal(t, ShallowFresh, assign.Of("sources"))
}

// A value read out of a rest parameter is one of the caller's arguments, which
// Appendix B has no way to name, so it resolves to `Unknown` rather than to the
// List's own position. `Math.max` hands back one of those values and comes out
// with no return alias.
func TestOriginMapReadOutOfARestParameter(t *testing.T) {
	m := originsOf(t, "Math.max")

	require.Equal(t, ShallowFresh, m.Of("args"))
	require.Equal(t, Unknown, m.Eval(&PropExpr{Object: &VarExpr{Var: "args"}}))
	require.Equal(t, Unknown, m.Eval(&SlotExpr{Object: &VarExpr{Var: "args"}, Slot: "MapData"}))
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

	// A property read off that payload is still the receiver's own state, while
	// one off the receiver reaches a separate object it only references. The
	// Origin doc comment spells out the difference.
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

// A constructor-running operation's result is fresh only when the constructor
// was handed nothing the caller passed in. The callee alone cannot answer, so
// the rule is settled one call site at a time. See constructedOrigin.
func TestOriginMapConstructResult(t *testing.T) {
	tests := map[string]struct {
		fn, value string
		want      Origin
	}{
		// `Let A be ? Construct(C, « lenNumber »)`. `C` is the `this` value of a
		// static, which resolves to `Unknown`, and `lenNumber` is a length
		// `Array.of` computed. The array is the algorithm's own.
		"NothingOfTheCallersReachesTheConstructor": {"Array.of", "A", Fresh},
		// `Let matcher be ? Construct(C, « R, flags »)` hands the receiver to
		// the constructor inside the argument list.
		"TheReceiverIsAnArgument": {"RegExp.prototype [ @@matchAll ]", "matcher", Unknown},
		// The same shape one algorithm over, with `Let splitter be ?
		// Construct(C, « rx, newFlags »)` over the receiver `rx`.
		"TheReceiverIsAnArgumentOfSplit": {"RegExp.prototype [ @@split ]", "splitter", Unknown},
		// `Return ? Construct(target, args, newTarget)`, where every argument is
		// a parameter of the algorithm itself.
		"EveryArgumentIsAParameter": {"Reflect.construct", "%7", Unknown},
		// The same rule through a forwarding wrapper. `TypedArray.prototype.
		// subarray` calls `TypedArraySpeciesCreate(O, argumentsList)` with an
		// `argumentsList` built over `O.[[ViewedArrayBuffer]]`, so a `@@species`
		// constructor can hand back the receiver's own buffer.
		"AWrapperForwardsTheReceiversBuffer": {"TypedArray.prototype.subarray", "%7", Unknown},
		// The same wrapper called with a length the algorithm computed.
		// `TypedArray.prototype.slice` passes `« 𝔽(count) »`.
		"AWrapperForwardsALength": {"TypedArray.prototype.slice", "A", Fresh},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			require.Equal(t, test.want, originsOf(t, test.fn).Of(test.value))
		})
	}
}

// The argument list of a `Construct` has to be a value the algorithm made
// whole, since the constructor receives what is inside it rather than the list.
// The constructor itself is held to the looser half of the rule, which is what
// lets `Array.of` run the `Unknown` its `this` value resolves to.
func TestOriginMapConstructReadsTheArgumentList(t *testing.T) {
	m := originsOf(t, "Array.prototype.push")
	require.Equal(t, Receiver, m.Of("O"))
	unplaceable := &CallExpr{Callee: "Get", Args: []Expr{&VarExpr{Var: "O"}}}
	require.Equal(t, Unknown, m.Eval(unplaceable))

	tests := map[string]struct {
		args []Expr
		want Origin
	}{
		// A constructor the analysis could not place is one value rather than a
		// list of them, so a result equal to it is a value the analysis already
		// could not place.
		"UnplaceableConstructor": {[]Expr{unplaceable}, Fresh},
		// The receiver as the constructor. `Construct(O)` can hand `O` back.
		"TheReceiverIsTheConstructor": {[]Expr{&VarExpr{Var: "O"}}, Unknown},
		// An argument list the algorithm built over its own values.
		"ListOfTheAlgorithmsOwnValues": {[]Expr{unplaceable, &AllocExpr{Args: []Expr{&LitExpr{}}}}, Fresh},
		// The receiver inside the list, which is what `« R, flags »` marks
		// shallow.
		"TheReceiverIsInTheList": {[]Expr{unplaceable, &AllocExpr{Args: []Expr{&VarExpr{Var: "O"}}}}, Unknown},
		// A list the analysis could not place hides its elements, so one of
		// them can be the caller's.
		"UnplaceableList": {[]Expr{unplaceable, unplaceable}, Unknown},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			require.Equal(t, test.want, m.Eval(&CallExpr{Callee: "Construct", Args: test.args}))
		})
	}
}

// A value the algorithm allocates in place is shallow when it holds one of the
// caller's, the same split that shallowAllocators and paramOrigin mark. Writing the
// allocation is invisible to the caller either way, and reading inside one that
// holds the receiver reaches the receiver.
func TestOriginMapInPlaceAllocation(t *testing.T) {
	m := originsOf(t, "Array.prototype.push")

	require.Equal(t, Fresh, m.Eval(&AllocExpr{Args: nil}))
	require.Equal(t, Fresh, m.Eval(&AllocExpr{Args: []Expr{&LitExpr{}}}))
	require.Equal(t, ShallowFresh, m.Eval(&AllocExpr{Args: []Expr{&VarExpr{Var: "O"}}}))

	// Holding a shallow value makes the allocation shallow too, so nesting one
	// list inside another does not launder what the inner one holds.
	nested := &AllocExpr{Args: []Expr{&AllocExpr{Args: []Expr{&VarExpr{Var: "O"}}}}}
	require.Equal(t, ShallowFresh, m.Eval(nested))

	// An operand the analysis could not place is not one the algorithm made
	// either, so an allocation holding it is shallow as well.
	unplaceable := &CallExpr{Callee: "Get", Args: []Expr{&VarExpr{Var: "O"}}}
	require.Equal(t, Unknown, m.Eval(unplaceable))
	require.Equal(t, ShallowFresh, m.Eval(&AllocExpr{Args: []Expr{unplaceable}}))

	// Reading inside a shallow allocation reaches the value it was built over.
	require.Equal(t, Fresh, interiorOf(m.Eval(&AllocExpr{Args: []Expr{&LitExpr{}}})))
	require.Equal(t, Unknown, interiorOf(m.Eval(&AllocExpr{Args: []Expr{&VarExpr{Var: "O"}}})))
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

// shallowAllocators is derived from the graph, so this recomputes it. An
// allocation is shallow when one of the allocator's parameters reaches a place
// interiorOf would read it back from: the operands of an allocation it builds,
// or the value it writes into a backing-store slot.
func TestShallowAllocatorsMatchTheGraph(t *testing.T) {
	cfg := testCFG(t)

	derived := set.NewSet[string]()
	for _, name := range allocators.ToSlice() {
		fn := cfg.AbstractOp(name)
		require.NotNil(t, fn, "no abstract operation named %s", name)
		if allocatesShallowly(fn) {
			derived.Add(name)
		}
	}

	require.Equal(t, sorted(shallowAllocators), sorted(derived))
}

// allocatesShallowly reports whether one of fn's parameters can be read back
// out of the value fn allocates.
//
// A name is at a parameter when fn's own origin map says so, which is how a
// parameter reached through an intermediate binding still counts. Matching a
// parameter's spelling alone would miss `Let b be obj` followed by an
// allocation over `b`.
func allocatesShallowly(fn *Func) bool {
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

// Each role renders as its own word, and a role outside the three renders as
// its number. TestConstructorCalleesMatchTheGraph compares whole role slices,
// so a rendering that lost the names would cost a failing run its diagnosis.
func TestArgRoleString(t *testing.T) {
	require.Equal(t, "held", argHeld.String())
	require.Equal(t, "value", argValue.String())
	require.Equal(t, "list", argList.String())

	// A role outside the three renders as its number, so a failing derivation
	// stays legible.
	require.Equal(t, "argRole(9)", argRole(9).String())
}

// An argument past the roles the table spells takes the strictest of them, so
// an operation the spec grows an argument refuses rather than admits it.
func TestRoleAtBeyondTheTable(t *testing.T) {
	require.Equal(t, argValue, roleAt("Construct", 0))
	require.Equal(t, argList, roleAt("Construct", 1))
	require.Equal(t, argValue, roleAt("Construct", 2))
	require.Equal(t, argList, roleAt("Construct", 3))
}

// A definition the walk has not reached leaves the reader unset for that pass
// rather than pinning it at `Unknown`, which is what lets an argument defined
// after its use still place the value built from it. Both constructedOrigin and
// allocated read an operand that way.
//
// The graph the serializer emits puts these definitions first, so the shape is
// written by hand.
func TestOriginMapReadsAnOperandDefinedLater(t *testing.T) {
	tests := map[string]struct {
		nodes []Node
		name  string
	}{
		// `Construct` over an argument list the next node binds.
		"ConstructArgumentList": {[]Node{
			&CallNode{Target: "%0", Callee: "Construct", Args: []Expr{&LitExpr{}, &VarExpr{Var: "list"}}},
			&LetNode{Target: "list", Source: &AllocExpr{Args: []Expr{&LitExpr{}}}},
		}, "%0"},
		// An allocation over a value the next node binds.
		"AllocationOperand": {[]Node{
			&LetNode{Target: "outer", Source: &AllocExpr{Args: []Expr{&VarExpr{Var: "inner"}}}},
			&LetNode{Target: "inner", Source: &AllocExpr{Args: []Expr{&LitExpr{}}}},
		}, "outer"},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			m := NewOriginMap(&Func{Name: name, Kind: AbstractOp, Nodes: test.nodes})
			require.Equal(t, Fresh, m.Of(test.name))
		})
	}
}

// constructorCallees is derived from the graph, so this recomputes it. An
// operation forwards one of its arguments when that argument reaches the
// constructor of a call whose result the operation hands back, either a
// `Construct` or another forwarding operation.
//
// Every other allocator has to forward nothing. An allocator that forwards one
// of its arguments can be handed that argument straight back by the
// constructor, and calling its result fresh would then discard a write its
// caller can see.
func TestConstructorCalleesMatchTheGraph(t *testing.T) {
	cfg := testCFG(t)

	derived := map[string][]argRole{}
	for _, name := range append(sorted(allocators), sortedKeys(constructorCallees)...) {
		fn := cfg.AbstractOp(name)
		require.NotNil(t, fn, "no abstract operation named %s", name)
		if roles := forwardedArgs(fn); slices.Contains(roles, argValue) || slices.Contains(roles, argList) {
			derived[name] = roles
		}
	}

	require.Equal(t, constructorCallees, derived)
}

// forwardedArgs returns the role each of fn's parameters takes at a constructor
// fn runs and whose result fn hands back. A parameter that reaches no such
// constructor takes argHeld.
//
// A parameter is matched by its own name and by fn's origin map, so one reached
// through an intermediate binding still counts. Neither route alone is enough.
// `Construct` rebinds `argumentsList` to an empty List on one path, which joins
// its seed to `Unknown` and leaves only the name, while a parameter the
// algorithm binds to a fresh name first has only the origin map to place it.
//
// A read off a parameter counts as that parameter, the way allocatesShallowly
// treats one. `Map.groupBy` passes its constructor as a slot chain rather than
// a name, and an operation that forwarded such a chain would otherwise go
// unnoticed.
func forwardedArgs(fn *Func) []argRole {
	origins := NewOriginMap(fn)
	returned := returnedNames(fn)
	roles := make([]argRole, len(fn.Params))

	// at raises the role of every parameter an argument expression names. An
	// element of an argument List is one value the constructor receives, so a
	// parameter inside an allocation takes argValue rather than argList.
	var at func(Expr, argRole)
	at = func(e Expr, role argRole) {
		switch e := e.(type) {
		case *AllocExpr:
			for _, operand := range e.Args {
				at(operand, argValue)
			}
		case *SlotExpr:
			at(e.Object, role)
		case *PropExpr:
			at(e.Object, role)
		case *VarExpr:
			for i, param := range fn.Params {
				o := origins.Of(e.Var)
				named := e.Var == param
				placed := o.Kind == OriginParam && o.Index == i
				if (named || placed) && role > roles[i] {
					roles[i] = role
				}
			}
		}
	}

	for _, node := range fn.Nodes {
		call, ok := node.(*CallNode)
		if !ok || len(constructorCallees[call.Callee]) == 0 || !returned.Contains(call.Target) {
			continue
		}
		for i, arg := range call.Args {
			if role := roleAt(call.Callee, i); role != argHeld {
				at(arg, role)
			}
		}
	}
	return roles
}

// returnedNames returns the value names fn hands back to its caller. A Let
// carries the return to the name it was bound from, and so does a completion
// wrap, whose caller-side unwrap §3 drops.
//
// A value held inside an allocation fn returns is not one of them.
// `NewPromiseCapability` ends in `« promise, resolve, reject »`, a record of
// its own whatever `Construct(C, « executor »)` handed it, which is why it
// stays an ordinary allocator.
func returnedNames(fn *Func) set.Set[string] {
	names := set.NewSet[string]()
	for _, node := range fn.Nodes {
		ret, ok := node.(*ReturnNode)
		if !ok {
			continue
		}
		if read, ok := ret.Value.(*VarExpr); ok {
			names.Add(read.Var)
		}
	}

	for {
		grew := false
		carry := func(source Expr) {
			read, ok := source.(*VarExpr)
			if ok && !names.Contains(read.Var) {
				names.Add(read.Var)
				grew = true
			}
		}
		for _, node := range fn.Nodes {
			switch node := node.(type) {
			case *LetNode:
				if names.Contains(node.Target) {
					carry(node.Source)
				}
			case *CallNode:
				if names.Contains(node.Target) && identityCoercions.Contains(node.Callee) && len(node.Args) > 0 {
					carry(node.Args[0])
				}
			}
		}
		if !grew {
			return names
		}
	}
}

// sortedKeys returns the operations a role table names, sorted, so the
// derivation visits them in an order that does not depend on map iteration.
func sortedKeys(roles map[string][]argRole) []string {
	names := make([]string, 0, len(roles))
	for name := range roles {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

func sorted(s set.Set[string]) []string {
	names := s.ToSlice()
	sort.Strings(names)
	return names
}

// A write to the buffer behind a freshly allocated typed array is invisible to
// the caller, which is what makes `TypedArray.prototype.slice` non-mutating.
// `A` comes from `TypedArraySpeciesCreate`, which holds nothing it was given,
// so `A.[[ViewedArrayBuffer]]` stays fresh while `O.[[ViewedArrayBuffer]]` is
// the receiver's interior.
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
// name reads back as the origin it was seeded with or as `Unknown`. It can
// never read back as an origin the walk invented, because the only other
// definitions of that name are assignments the algorithm makes to it, and those
// join to `Unknown`.
func TestOriginMapCoversEveryFunction(t *testing.T) {
	cfg := testCFG(t)

	for _, fn := range cfg.Funcs {
		m := NewOriginMap(fn)
		require.Same(t, fn, m.Func())
		for _, name := range m.Names() {
			require.NotEqual(t, originUnset, m.origins[name].Kind, "%s: %s left unset", fn.Name, name)
		}
		for i, param := range fn.Params {
			require.Contains(t, []Origin{paramOrigin(fn, i), Unknown}, m.Of(param), "%s: %s", fn.Name, param)
		}
	}
}

// Every builtin the committed graph keys a rest parameter for, as `name:
// position/parameters`. A rest parameter need not come last, and `Function (
// ...parameterArgs, bodyArg )` is where that shows. The last argument to that
// constructor is the function body and every argument before it is a formal
// parameter. Only a builtin declares a rest parameter, because one is spelled
// in an algorithm head and an abstract operation has none.
func TestGraphRestParameters(t *testing.T) {
	cfg := testCFG(t)

	var declared []string
	for _, fn := range cfg.Funcs {
		if fn.Variadic == nil {
			continue
		}
		require.NotEqual(t, AbstractOp, fn.Kind, "%s is not a builtin", fn.Name)
		declared = append(declared, fmt.Sprintf("%s: %d/%d", fn.Name, *fn.Variadic, len(fn.Params)))
	}
	sort.Strings(declared)

	snaps.MatchInlineSnapshot(t, strings.Join(declared, "\n"), snaps.Inline(`Array.of: 0/1
Array.prototype.concat: 0/1
Array.prototype.push: 0/1
Array.prototype.splice: 2/3
Array.prototype.toSpliced: 2/3
Array.prototype.unshift: 0/1
Array: 0/1
AsyncFunction: 0/2
AsyncGeneratorFunction: 0/2
BigInt64Array: 0/1
BigUint64Array: 0/1
Date: 0/1
Float16Array: 0/1
Float32Array: 0/1
Float64Array: 0/1
Function.prototype.bind: 1/2
Function.prototype.call: 1/2
Function: 0/2
GeneratorFunction: 0/2
Int16Array: 0/1
Int32Array: 0/1
Int8Array: 0/1
Math.hypot: 0/1
Math.max: 0/1
Math.min: 0/1
Object.assign: 1/2
Promise.try: 1/2
String.fromCharCode: 0/1
String.fromCodePoint: 0/1
String.prototype.concat: 0/1
String.raw: 1/2
TypedArray.of: 0/1
Uint16Array: 0/1
Uint32Array: 0/1
Uint8Array: 0/1
Uint8ClampedArray: 0/1`))
}
