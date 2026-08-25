package ecma262

import (
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
	// strings, and none writes an object or a slot. Reading that off the prose
	// is what keeping it is for.
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
