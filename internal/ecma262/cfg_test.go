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

func TestParseCFGRejectsMalformedJSON(t *testing.T) {
	_, err := ParseCFG([]byte("{"))
	require.Error(t, err)
	require.Equal(t, "decoding cfg: unexpected end of JSON input", err.Error())
}
