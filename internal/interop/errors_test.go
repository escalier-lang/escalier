package interop

import (
	"testing"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/stretchr/testify/require"
)

// originIn builds an origin pointing at one line of an override file, the way
// an extraction stamps one. The span covers the whole of the given line.
func originIn(t *testing.T, source *ast.Source, line int) Origin {
	t.Helper()
	lineMap := source.LineMap()
	start := lineMap.Offset(line, 1, ast.CodePointColumns)
	end := start + len(lineMap.LineText(line))
	return NewOriginSite(source.Path, source).originAt(
		ast.NewSpan(ast.Location{Offset: start}, ast.Location{Offset: end}, source.ID))
}

// The merge diagnostics name the line each origin sits on. An origin holds a
// byte offset, so the line comes from the file it carries.
func TestMergeErrorsNameTheOriginLine(t *testing.T) {
	t.Parallel()
	//nolint: exhaustruct // The memoized line map is built on first use.
	source := &ast.Source{
		ID:       0,
		Path:     "builtin:/lodash.esc",
		Contents: "declare val a: number\ndeclare val b: number\ndeclare val c: number\n",
	}
	path := Path{Module: "lodash", Name: ident("map"), Kind: KindFree, Owner: nil, Static: false}

	tests := []struct {
		name string
		err  error
		want string
	}{
		{
			name: "duplicate member",
			err: &ErrDuplicateMember{
				Path:   path,
				First:  originIn(t, source, 1),
				Second: originIn(t, source, 3),
			},
			want: "duplicate override entry for module \"lodash\"::map\n" +
				"  first defined at builtin:/lodash.esc:1\n" +
				"  redefined at builtin:/lodash.esc:3",
		},
		{
			name: "shape conflict",
			err: &ErrShapeConflict{
				Path:   path,
				First:  originIn(t, source, 2),
				Second: originIn(t, source, 3),
			},
			want: "shape conflict for module \"lodash\"::map\n" +
				"  first defined at builtin:/lodash.esc:2\n" +
				"  redefined at builtin:/lodash.esc:3",
		},
		{
			name: "unknown member without suggestions",
			err: &ErrUnknownMember{
				Path:      path,
				Override:  originIn(t, source, 2),
				Available: nil,
			},
			want: "override target module \"lodash\"::map not found on original declaration\n" +
				"  override at builtin:/lodash.esc:2",
		},
		{
			name: "unknown member with suggestions",
			err: &ErrUnknownMember{
				Path:      path,
				Override:  originIn(t, source, 1),
				Available: []string{"mapKeys", "mapValues"},
			},
			want: "override target module \"lodash\"::map not found on original declaration\n" +
				"  override at builtin:/lodash.esc:1\n" +
				"  available: mapKeys, mapValues",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tc.want, tc.err.Error())
		})
	}
}

// An origin built without a source has no text to resolve its offset against,
// so the diagnostic names line 0 rather than inventing one.
func TestMergeErrorNamesLineZeroWithoutASource(t *testing.T) {
	t.Parallel()
	//nolint: exhaustruct // Kind defaults to the zero value, unused here.
	origin := Origin{
		FilePath: "builtin:/lodash.esc",
		Span:     ast.NewSpan(ast.Location{Offset: 40}, ast.Location{Offset: 44}, 0),
	}
	err := &ErrUnknownMember{
		Path:      Path{Module: "lodash", Name: ident("map"), Kind: KindFree, Owner: nil, Static: false},
		Override:  origin,
		Available: nil,
	}
	require.Equal(t,
		"override target module \"lodash\"::map not found on original declaration\n"+
			"  override at builtin:/lodash.esc:0",
		err.Error())
}
