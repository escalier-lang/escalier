package codegen

import (
	"testing"

	"github.com/gkampitakis/go-snaps/snaps"
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
