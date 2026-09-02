package codegen

import (
	"testing"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/gkampitakis/go-snaps/snaps"
	"github.com/stretchr/testify/require"
)

// GENERATED: var foo=5;var bar=\n'hello';var baz=true
// SOURCE: let foo = 5;\nlet bar = 'hello';\nlet baz = true;

func TestEncodeSegments(t *testing.T) {
	segments1 := []*Segment{}

	// let
	segments1 = append(segments1, &Segment{
		GeneratedStartColumn: 0,
		SourceIndex:          0,
		SourceStartLine:      0,
		SourceStartColumn:    0,
		NameIndex:            -1,
	})

	// foo
	segments1 = append(segments1, &Segment{
		GeneratedStartColumn: 4,
		SourceIndex:          0,
		SourceStartLine:      0,
		SourceStartColumn:    4,
		NameIndex:            -1,
	})

	// 5
	segments1 = append(segments1, &Segment{
		GeneratedStartColumn: 8,
		SourceIndex:          0,
		SourceStartLine:      0,
		SourceStartColumn:    10,
		NameIndex:            -1,
	})

	// let
	segments1 = append(segments1, &Segment{
		GeneratedStartColumn: 10,
		SourceIndex:          0,
		SourceStartLine:      1,
		SourceStartColumn:    0,
		NameIndex:            -1,
	})

	// bar
	segments1 = append(segments1, &Segment{
		GeneratedStartColumn: 14,
		SourceIndex:          0,
		SourceStartLine:      1,
		SourceStartColumn:    4,
		NameIndex:            -1,
	})

	segments2 := []*Segment{}

	// 'hello'
	segments2 = append(segments2, &Segment{
		GeneratedStartColumn: 0,
		SourceIndex:          0,
		SourceStartLine:      1,
		SourceStartColumn:    10,
		NameIndex:            -1,
	})

	// let
	segments2 = append(segments2, &Segment{
		GeneratedStartColumn: 8,
		SourceIndex:          0,
		SourceStartLine:      2,
		SourceStartColumn:    0,
		NameIndex:            -1,
	})

	// baz
	segments2 = append(segments2, &Segment{
		GeneratedStartColumn: 12,
		SourceIndex:          0,
		SourceStartLine:      2,
		SourceStartColumn:    4,
		NameIndex:            -1,
	})

	// true
	segments2 = append(segments2, &Segment{
		GeneratedStartColumn: 16,
		SourceIndex:          0,
		SourceStartLine:      2,
		SourceStartColumn:    10,
		NameIndex:            -1,
	})

	expected := "AAAA,IAAI,IAAM,EACV,IAAI;AAAM,QACV,IAAI,IAAM"
	encoded := EncodeSegments([][]*Segment{segments1, segments2})

	if encoded != expected {
		t.Errorf("EncodeSegments() = %s; want %s", encoded, expected)
	}

	sourcemap := SourceMap{
		Version:        3,
		File:           "output.js",
		Sources:        []string{"input.esc"},
		SourcesContent: []*string{nil},
		Names:          []string{},
		Mappings:       "AAAA,IAAI,IAAM,EACV,IAAI;AAAM,QACV,IAAI,IAAM",
	}
	json := SerializeSourceMap(sourcemap)
	snaps.MatchSnapshot(t, json)
}

// TODO: write tests for GenerateSourceMap

// A SourceID is assigned across a whole package, so a map generated for a
// single bin/ script names one file under an id that is not 0. The Segment
// records the file's position in the map's own `sources` array, which is what
// a consumer indexes, rather than the id itself.
func TestAddSegmentForNodeIndexesTheSourcesArray(t *testing.T) {
	t.Parallel()
	source := &ast.Source{ID: 3, Path: "bin/main.esc", Contents: "val x = 1\nval y = 2\n"}
	// The generated node's source is the `2` on the second line, at offsets
	// [18, 19).
	sourceNode := ast.NewNumber(2, ast.NewSpan(
		ast.Location{Offset: 18}, ast.Location{Offset: 19}, source.ID))

	generated := NewNumLit(2, sourceNode)
	generated.SetSpan(&Span{
		Start: Location{Line: 1, Column: 1},
		End:   Location{Line: 1, Column: 2},
	})

	gen := &SourceMapGenerator{
		groups:      [][]*Segment{},
		sources:     map[int]*ast.Source{source.ID: source},
		sourceIndex: map[int]int{source.ID: 0},
	}
	gen.AddSegmentForNode(generated)

	require.Len(t, gen.groups, 1)
	require.Len(t, gen.groups[0], 1)
	segment := gen.groups[0][0]
	require.Equal(t, 0, segment.SourceIndex, "the one file is at index 0")
	require.Equal(t, 1, segment.SourceStartLine, "0-based line 1 is the second line")
	require.Equal(t, 8, segment.SourceStartColumn, "0-based column 8 is the `2`")
}

// A node whose source span belongs to a file the map does not list has no
// position to record, so it contributes no segment.
func TestAddSegmentForNodeSkipsAnUnlistedSource(t *testing.T) {
	t.Parallel()
	sourceNode := ast.NewNumber(2, ast.NewSpan(
		ast.Location{Offset: 0}, ast.Location{Offset: 1}, 7))
	generated := NewNumLit(2, sourceNode)
	generated.SetSpan(&Span{
		Start: Location{Line: 1, Column: 1},
		End:   Location{Line: 1, Column: 2},
	})

	gen := &SourceMapGenerator{
		groups:      [][]*Segment{},
		sources:     map[int]*ast.Source{},
		sourceIndex: map[int]int{},
	}
	gen.AddSegmentForNode(generated)

	require.Len(t, gen.groups, 1)
	require.Empty(t, gen.groups[0])
}
