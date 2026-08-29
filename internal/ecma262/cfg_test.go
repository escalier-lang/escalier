package ecma262

import (
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

// cfgPath is the control-flow graph tools/spec-extract commits.
const cfgPath = "../../tools/spec-extract/cfg.json"

var (
	loadOnce sync.Once
	loaded   *CFG
	loadErr  error
)

// testCFG loads the committed control-flow graph once for the whole package.
func testCFG(t *testing.T) *CFG {
	t.Helper()
	loadOnce.Do(func() {
		loaded, loadErr = LoadCFG(cfgPath)
	})
	require.NoError(t, loadErr)
	return loaded
}

func TestLoadCFG(t *testing.T) {
	cfg := testCFG(t)

	require.NotEmpty(t, cfg.SpecTarget)
	require.NotEmpty(t, cfg.Funcs)
	for _, fn := range cfg.Funcs {
		require.Contains(t, []FuncKind{BuiltinMethod, BuiltinStatic, AbstractOp}, fn.Kind,
			"unexpected kind on %s", fn.Name)
		require.NotEmpty(t, fn.Name)
	}
}

func TestLookupKeepsNameSpacesApart(t *testing.T) {
	cfg := testCFG(t)

	// `Set` names both the property-write abstract operation and the `Set`
	// constructor, so a lookup has to say which space it means.
	setOp := cfg.AbstractOp("Set")
	require.NotNil(t, setOp)
	require.Equal(t, AbstractOp, setOp.Kind)
	require.Equal(t, []string{"O", "P", "V", "Throw"}, setOp.Params)

	require.Nil(t, cfg.Builtin("ToObject"))
	require.Nil(t, cfg.AbstractOp("Array.prototype.push"))

	push := cfg.Builtin("Array.prototype.push")
	require.NotNil(t, push)
	require.Equal(t, BuiltinMethod, push.Kind)
	require.Equal(t, []string{"items"}, push.Params)

	// `Array.prototype.push ( ...items )` declares its one formal as the rest
	// parameter, while `Set(O, P, V, Throw)` declares none.
	require.NotNil(t, push.Variadic)
	require.Equal(t, 0, *push.Variadic)
	require.Nil(t, setOp.Variadic)
}

func TestParseCFGRejects(t *testing.T) {
	tests := map[string]struct {
		json string
		err  string
	}{
		"MalformedJSON": {
			json: `{`,
			err:  "decoding cfg: unexpected end of JSON input",
		},
		// The analysis indexes an abstract operation apart from a builtin and
		// has nowhere to put anything else. Syntax-directed operations are the
		// runtime semantics of the language rather than a library surface, so
		// the serializer drops them.
		// A missing entry would otherwise be a nil dereference here or in the
		// walk that follows.
		"MissingFunc": {
			json: `{"specTarget":"abc","funcs":[null]}`,
			err:  "decoding cfg: funcs[0] is missing",
		},
		"MissingNode": {
			json: `{"specTarget":"abc","funcs":[{"name":"ToObject","kind":"abstract-op",` +
				`"nodes":[{"kind":"branch"},null]}]}`,
			err: "decoding cfg: node 1 of ToObject is missing",
		},
		// The analysis addresses a function only by name, so an unnamed one is
		// unreachable.
		"UnnamedFunc": {
			json: `{"specTarget":"abc","funcs":[{"kind":"abstract-op"}]}`,
			err:  "decoding cfg: funcs[0] has no name",
		},
		// A required operand the graph leaves out would otherwise reach the
		// origin walk as a nil Expr and resolve to Unknown, hiding the gap.
		"MissingOperand": {
			json: `{"specTarget":"abc","funcs":[{"name":"ToObject","kind":"abstract-op",` +
				`"nodes":[{"kind":"let","target":"O"}]}]}`,
			err: "decoding cfg: node 0 of ToObject: the source is missing",
		},
		"MissingArgument": {
			json: `{"specTarget":"abc","funcs":[{"name":"ToObject","kind":"abstract-op",` +
				`"nodes":[{"kind":"call","callee":"Get","args":[{"kind":"this"},null]}]}]}`,
			err: "decoding cfg: node 0 of ToObject: the argument 1 is missing",
		},
		// The serializer omits every field the kind does not carry, so a field
		// on the wrong kind means the schema moved and the reader has to be
		// taught the new shape.
		"StrayNodeField": {
			json: `{"specTarget":"abc","funcs":[{"name":"ToObject","kind":"abstract-op",` +
				`"nodes":[{"kind":"let","target":"O","source":{"kind":"this"},"slot":"MapData"}]}]}`,
			err: `decoding cfg: node 0 of ToObject: a let node carries "slot"`,
		},
		"StrayExprField": {
			json: `{"specTarget":"abc","funcs":[{"name":"ToObject","kind":"abstract-op",` +
				`"nodes":[{"kind":"return","value":{"kind":"var","var":"O","args":[]}}]}]}`,
			err: `decoding cfg: node 0 of ToObject: a var expression carries "args"`,
		},
		// The prose of an unformalized step is the only thing an opaque node
		// carries, so a node without it says nothing about what the analysis
		// gave up on.
		"OpaqueWithoutText": {
			json: `{"specTarget":"abc","funcs":[{"name":"ToObject","kind":"abstract-op",` +
				`"nodes":[{"kind":"opaque"}]}]}`,
			err: "decoding cfg: node 0 of ToObject: the step text is missing",
		},
		"StrayOpaqueField": {
			json: `{"specTarget":"abc","funcs":[{"name":"ToObject","kind":"abstract-op",` +
				`"nodes":[{"kind":"opaque","text":["Let _n_ be ..."],"slot":"MapData"}]}]}`,
			err: `decoding cfg: node 0 of ToObject: a opaque node carries "slot"`,
		},
		"EmptyOpaqueText": {
			json: `{"specTarget":"abc","funcs":[{"name":"ToObject","kind":"abstract-op",` +
				`"nodes":[{"kind":"opaque","text":["Let _n_ be ...",""]}]}]}`,
			err: "decoding cfg: node 0 of ToObject: step text 1 is empty",
		},
		"UnknownNodeTag": {
			json: `{"specTarget":"abc","funcs":[{"name":"ToObject","kind":"abstract-op",` +
				`"nodes":[{"kind":"assign"}]}]}`,
			err: `decoding cfg: node 0 of ToObject: kind "assign" names no node`,
		},
		"UnknownExprTag": {
			json: `{"specTarget":"abc","funcs":[{"name":"ToObject","kind":"abstract-op",` +
				`"nodes":[{"kind":"return","value":{"kind":"regexp"}}]}]}`,
			err: `decoding cfg: node 0 of ToObject: kind "regexp" names no expression`,
		},
		"UnindexableKind": {
			json: `{"specTarget":"abc","funcs":[{"name":"Evaluation","kind":"` +
				string(SyntaxDirected) + `"}]}`,
			err: `decoding cfg: Evaluation has kind "syntax-directed", which the analysis cannot index`,
		},
		"RepeatedName": {
			json: `{"specTarget":"abc","funcs":[{"name":"Set","kind":"abstract-op"},{"name":"Set","kind":"abstract-op"}]}`,
			err:  "decoding cfg: two abstract-op functions named Set",
		},
		// A rest parameter is named by position, so a position past the
		// declared parameters names nothing. Read as absent it would leave the
		// List looking like a value the caller passed.
		"RestPositionPastTheParameters": {
			json: `{"specTarget":"abc","funcs":[{"name":"Math.max","kind":"builtin-static",` +
				`"params":["args"],"variadic":1}]}`,
			err: "decoding cfg: Math.max declares a rest parameter at position 1, outside its 1 parameters",
		},
		"NegativeRestPosition": {
			json: `{"specTarget":"abc","funcs":[{"name":"Math.max","kind":"builtin-static",` +
				`"params":["args"],"variadic":-1}]}`,
			err: "decoding cfg: Math.max declares a rest parameter at position -1, outside its 1 parameters",
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			_, err := ParseCFG([]byte(test.json))
			require.Error(t, err)
			require.Equal(t, test.err, err.Error())
		})
	}
}

// The graph does not always name the value a slot write stores. A closure's
// argument prologue writes an incoming argument the algorithm never named, so
// the node decodes with a nil Value rather than being rejected.
func TestParseCFGAllowsSlotWriteWithoutValue(t *testing.T) {
	cfg := testCFG(t)

	fn := cfg.AbstractOp("NewPromiseCapability:clo0")
	require.NotNil(t, fn)

	var slots []string
	for _, node := range fn.Nodes {
		if write, ok := node.(*SlotWriteNode); ok && write.Value == nil {
			require.Equal(t, &VarExpr{Var: "__args__"}, write.Object)
			slots = append(slots, write.Slot)
		}
	}
	require.Equal(t, []string{"resolve", "reject"}, slots)
}

// One name can name both an abstract operation and a builtin. `Set` is the
// property-write operation and the `Set` constructor, and the two indexes keep
// them apart.
func TestParseCFGIndexesBothNameSpaces(t *testing.T) {
	cfg, err := ParseCFG([]byte(
		`{"specTarget":"abc","funcs":[` +
			`{"name":"Set","kind":"abstract-op"},` +
			`{"name":"Set","kind":"builtin-static"}]}`))
	require.NoError(t, err)

	require.Equal(t, AbstractOp, cfg.AbstractOp("Set").Kind)
	require.Equal(t, BuiltinStatic, cfg.Builtin("Set").Kind)
}

// Every opaque node carries the prose of the step the lowering could not
// formalize. Without it the analysis sees only that a step was lost, and one
// binding a name over numbers looks the same as one replacing the contents of
// a slot.
func TestOpaqueNodesCarryStepText(t *testing.T) {
	cfg := testCFG(t)

	opaque := 0
	for _, fn := range cfg.Funcs {
		for i, node := range fn.Nodes {
			step, ok := node.(*OpaqueNode)
			if !ok {
				continue
			}
			opaque++
			require.NotEmpty(t, step.Text, "node %d of %s", i, fn.Name)
			for _, text := range step.Text {
				require.NotEmpty(t, text, "node %d of %s", i, fn.Name)
			}
		}
	}
	require.NotZero(t, opaque)

	// Number.prototype.toFixed is one of the builtins the analysis gives up on
	// for its opaque steps. Every one of them binds a name over numbers and
	// strings, and none writes an object or a slot. The prose is what shows
	// that.
	toFixed := cfg.Builtin("Number.prototype.toFixed")
	require.NotNil(t, toFixed)

	var text []string
	for _, node := range toFixed.Nodes {
		if step, ok := node.(*OpaqueNode); ok {
			text = append(text, step.Text...)
		}
	}
	require.Equal(t, []string{
		"Let _n_ be an integer for which _n_ / 10<sup>_f_</sup> - _x_ is as close to zero as possible. If there are two such _n_, pick the larger _n_.",
		"let _m_ be the String value consisting of the digits of the decimal representation of _n_ (in order, with no leading zeroes).",
		"Let _z_ be the String value consisting of _f_ + 1 - _k_ occurrences of the code unit 0x0030 (DIGIT ZERO).",
		"Let _a_ be the first _k_ - _f_ code units of _m_.",
		"Let _b_ be the other _f_ code units of _m_.",
	}, text)
}

// The steps the serializer reads out of prose rather than out of the compiled
// IR. ESMeta leaves some algorithm steps unformalized, and tools/spec-extract
// recognizes the few whose wording names the write or the allocation the step
// performs. Each entry names a function one of those steps sits in and the node
// the graph carries in its place.
//
// A regeneration that stopped recognizing a step would put an opaque node back
// here, and the function would report nothing but the fact that a step was
// unreadable. The serializer's own run fails when a phrasing stops matching the
// number of steps it was reviewed against. This pins what the committed graph
// carries, which is what the analysis reads.
func TestGraphCarriesTheStepsReadFromProse(t *testing.T) {
	tests := map[string]struct {
		fn   string
		want string
	}{
		// "Replace the element of _S_.[[SetData]] whose value is _e_ with an
		// element whose value is ~empty~."
		"ElementReplacement": {"Set.prototype.clear", "slotwrite S.SetData = lit"},
		// "Let _resultSetData_ be a copy of _O_.[[SetData]]."
		"BackingStoreCopy": {"Set.prototype.union", "let resultSetData = alloc(O.SetData)"},
		// "Let _add_ be a new read-modify-write modification function with
		// parameters (_xBytes_, _yBytes_) that captures _typedArray_ and
		// performs the following steps atomically when called: ..."
		"ModificationFunction": {"Atomics.add", "let add = alloc(typedArray)"},
		// "Let _rawBytesRead_ be a List of length _elementSize_ whose elements
		// are the sequence of _elementSize_ bytes starting with
		// _block_[_byteIndexInBuffer_]."
		"ByteList": {"Atomics.compareExchange", "let rawBytesRead = alloc()"},
	}

	cfg := testCFG(t)
	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			fn := cfg.Builtin(test.fn)
			require.NotNil(t, fn, "no builtin named %s", test.fn)

			var rendered []string
			for _, node := range fn.Nodes {
				rendered = append(rendered, renderNode(node))
			}
			require.Contains(t, rendered, test.want)
		})
	}
}

// The builtins where a phrasing recognizes every unformalized step. Each
// reported nothing but the fact that a step was unreadable while those steps
// stayed opaque, so an opaque node reappearing here means the specification
// reworded a phrasing.
func TestGraphLeavesNoOpaqueStepInTheRecognizedBuiltins(t *testing.T) {
	cfg := testCFG(t)

	for _, name := range []string{
		"Atomics.add",
		"Atomics.and",
		"Atomics.compareExchange",
		"Atomics.exchange",
		"Atomics.or",
		"Atomics.sub",
		"Atomics.xor",
		"Set.prototype.clear",
		"Set.prototype.delete",
		"Set.prototype.difference",
		"Set.prototype.symmetricDifference",
		"Set.prototype.union",
		"WeakSet.prototype.delete",
	} {
		fn := cfg.Builtin(name)
		require.NotNil(t, fn, "no builtin named %s", name)
		for i, node := range fn.Nodes {
			_, opaque := node.(*OpaqueNode)
			require.False(t, opaque, "node %d of %s is opaque", i, name)
		}
	}
}

// renderNode spells one node on a line, so a test can name the node a step
// lowers to without walking its operands field by field.
func renderNode(node Node) string {
	switch node := node.(type) {
	case *LetNode:
		return "let " + node.Target + " = " + renderExpr(node.Source)
	case *CallNode:
		args := make([]string, 0, len(node.Args))
		for _, arg := range node.Args {
			args = append(args, renderExpr(arg))
		}
		return "call " + node.Target + " = " + node.Callee +
			"(" + strings.Join(args, ", ") + ") " + string(node.Guard)
	case *SlotWriteNode:
		target := renderExpr(node.Object)
		if node.Slot != "" {
			target += "." + node.Slot
		} else {
			target += "[computed]"
		}
		return "slotwrite " + target + " = " + renderExpr(node.Value)
	case *ThrowNode:
		if node.ErrorType != "" {
			return "throw " + node.ErrorType
		}
		return "throw " + renderExpr(node.Value)
	case *ReturnNode:
		return "return " + renderExpr(node.Value)
	case *BranchNode:
		return "branch"
	case *OpaqueNode:
		return "opaque " + strings.Join(node.Text, " | ")
	}
	return ""
}

// renderExpr spells one expression, and an absent operand as "none".
func renderExpr(e Expr) string {
	switch e := e.(type) {
	case *VarExpr:
		return e.Var
	case *ThisExpr:
		return "this"
	case *LitExpr:
		return "lit"
	case *CallExpr:
		args := make([]string, 0, len(e.Args))
		for _, arg := range e.Args {
			args = append(args, renderExpr(arg))
		}
		return e.Callee + "(" + strings.Join(args, ", ") + ")"
	case *SlotExpr:
		return renderExpr(e.Object) + "." + e.Slot
	case *PropExpr:
		return renderExpr(e.Object) + "[computed]"
	case *AllocExpr:
		args := make([]string, 0, len(e.Args))
		for _, arg := range e.Args {
			args = append(args, renderExpr(arg))
		}
		return "alloc(" + strings.Join(args, ", ") + ")"
	}
	return "none"
}

// Two algorithms that differ fingerprint differently, and re-parsing the same
// graph reproduces each fingerprint. Together those are what let a digest stand
// in for "the algorithm this entry was reviewed against".
func TestFuncDigestIdentifiesTheAlgorithm(t *testing.T) {
	t.Parallel()

	first, _ := demoFacts(t)
	second, err := ParseCFG([]byte(demoCFG))
	require.NoError(t, err)

	read := first.Builtin("Demo.prototype.read")
	opaque := first.Builtin("Demo.prototype.opaque")
	require.Len(t, read.Digest, digestLen)
	require.NotEqual(t, read.Digest, opaque.Digest)
	require.Equal(t, read.Digest, second.Builtin("Demo.prototype.read").Digest)

	// Editing one step changes that algorithm's digest and leaves the other's
	// alone, which is what keeps a spec bump from flagging every entry.
	edited, err := ParseCFG([]byte(strings.Replace(demoCFG, "whatever the host decides", "something else", 1)))
	require.NoError(t, err)
	require.Equal(t, read.Digest, edited.Builtin("Demo.prototype.read").Digest)
	require.NotEqual(t, opaque.Digest, edited.Builtin("Demo.prototype.opaque").Digest)
}
