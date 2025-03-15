package codegen

import (
	"fmt"
	"testing"

	"github.com/escalier-lang/escalier/internal/parser"
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
		SourcesContent: []string{"let foo = 5;\nlet bar = 'hello';\nlet baz = true;"},
		Names:          []string{},
		Mappings:       "AAAA,IAAI,IAAM,EACV,IAAI;AAAM,QACV,IAAI,IAAM",
	}
	json, err := SerializeSourceMap(sourcemap)
	if err != nil {
		t.Errorf("GenerateSourceMap() = %s; want nil", err)
	}
	snaps.MatchSnapshot(t, json)
}

func TestGenerateSourceMap(t *testing.T) {
	source := parser.Source{
		Path:     "input2.esc",
		Contents: "val foo = 5\nval bar = \"hello\"\n",
	}

	p := parser.NewParser(source)
	m := p.ParseModule()
	fmt.Printf("Errors: %#v\n", p.Errors)
	if len(p.Errors) > 0 {
		t.Errorf("ParseModule() = %#v; want []*parser.Error{}", p.Errors)
		return
	}
	jsMod := TransformModule(m)

	printer := NewPrinter()
	printer.PrintModule(jsMod)

	snaps.MatchSnapshot(t, printer.Output)

	srcMap, err := GenerateSourceMap(source, jsMod, "output2.js")

	if err != nil {
		t.Errorf("GenerateSourceMap() = %s; want nil", err)
		return
	}
	snaps.MatchSnapshot(t, srcMap)
}

func TestGenerateSourceMapWithFuncDecls(t *testing.T) {
	source := parser.Source{
		Path:     "input3.esc",
		Contents: "fn add(a, b) {\n  return a + b\n}\nfn sub(a, b) { return a - b }\n",
	}

	p := parser.NewParser(source)
	m := p.ParseModule()
	fmt.Printf("Errors: %#v\n", p.Errors)
	if len(p.Errors) > 0 {
		t.Errorf("ParseModule() = %#v; want []*parser.Error{}", p.Errors)
		return
	}
	jsMod := TransformModule(m)

	printer := NewPrinter()
	printer.PrintModule(jsMod)

	snaps.MatchSnapshot(t, printer.Output)

	srcMap, err := GenerateSourceMap(source, jsMod, "output3.js")

	if err != nil {
		t.Errorf("GenerateSourceMap() = %s; want nil", err)
		return
	}
	snaps.MatchSnapshot(t, srcMap)
}
