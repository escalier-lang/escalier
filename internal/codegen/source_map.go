package codegen

import (
	"encoding/json"

	"github.com/escalier-lang/escalier/internal/parser"
)

type SourceMap struct {
	Version        int      `json:"version"`        // this should always be the number 3
	File           string   `json:"file"`           // the generated file
	Sources        []string `json:"sources"`        // the original files
	SourcesContent []string `json:"sourcesContent"` // TODO: check that omitting this works
	Names          []string `json:"names"`          // TODO: investigate using this
	Mappings       string   `json:"mappings"`
}

func SerializeSourceMap(sourcemap SourceMap) (string, error) {
	bytes, err := json.Marshal(sourcemap)
	if err != nil {
		return "", err
	}
	return string(bytes), nil
}

// represents a single segment of a generated line
// separated by commas
type Segment struct {
	GeneratedStartColumn int // 0-based, in generated code
	SourceIndex          int // 0-based, into sources list - if there's only one source, this will always be 0
	SourceStartLine      int // 0-based, in original code
	SourceStartColumn    int // 0-based, in original code
	NameIndex            int // 0-based, into names list
}

// To test this could have a generated file like:
// "var foo=5;var bar='hello';var baz=true" and have that map to a source file like:
// "let foo = 5;\nlet bar = 'hello';\nlet baz = true;"

func EncodeSegments(groups [][]*Segment) string {
	output := ""
	prevGenStartCol := -1
	prevSrcStartLine := -1
	prevSrcStartCol := -1

	for j, g := range groups {
		if j > 0 {
			// This is the only field that is suppoed to be reset after each
			// line in the generated code.
			prevGenStartCol = -1
			output += ";"
		}

		for i, s := range g {
			if i > 0 {
				output += ","
			}

			if prevGenStartCol == -1 {
				output += VLQEncode(s.GeneratedStartColumn)
				prevGenStartCol = s.GeneratedStartColumn
			} else {
				output += VLQEncode(s.GeneratedStartColumn - prevGenStartCol)
				prevGenStartCol = s.GeneratedStartColumn
			}

			output += VLQEncode(s.SourceIndex) // always 0

			if prevSrcStartLine == -1 {
				output += VLQEncode(s.SourceStartLine)
				prevSrcStartLine = s.SourceStartLine
			} else {
				output += VLQEncode(s.SourceStartLine - prevSrcStartLine)
				prevSrcStartLine = s.SourceStartLine
			}

			if prevSrcStartCol == -1 {
				output += VLQEncode(s.SourceStartColumn)
				prevSrcStartCol = s.SourceStartColumn
			} else {
				output += VLQEncode(s.SourceStartColumn - prevSrcStartCol)
				prevSrcStartCol = s.SourceStartColumn
			}

			// TODO: handle NameIndex
		}
	}

	return output
}

type SourceMapGenerator struct {
	groups [][]*Segment
}

func (s *SourceMapGenerator) TraverseModule(module *Module) {
	for _, stmt := range module.Stmts {
		s.TraverseStmt(stmt)
	}
}

func (s *SourceMapGenerator) TraverseStmt(stmt *Stmt) {
	// TODO: check if stmt.Span() is nil.  If it is, we should return an error
	switch sk := stmt.Kind.(type) {
	case *SExpr:
		s.TraverseExpr(sk.Expr)
	case *SDecl:
		s.TraverseDecl(sk.Decl)
	case *SReturn:
		s.AddSegmentForNode(stmt)
		s.TraverseExpr(sk.Expr)
	}
}

func (s *SourceMapGenerator) AddSegmentForNode(generated Node) {
	generatedLine := generated.Span().Start.Line

	if generatedLine > len(s.groups) {
		// add new groups
		for i := len(s.groups); i < generatedLine; i++ {
			s.groups = append(s.groups, []*Segment{})
		}
	}

	source := generated.Source()

	segment := &Segment{
		GeneratedStartColumn: generated.Span().Start.Column - 1,
		SourceIndex:          0, // always 0 for now
		SourceStartLine:      source.Span().Start.Line - 1,
		SourceStartColumn:    source.Span().Start.Column - 1,
		NameIndex:            -1, // not used for now
	}

	s.groups[len(s.groups)-1] = append(s.groups[len(s.groups)-1], segment)
}

func (s *SourceMapGenerator) TraverseDecl(decl *Decl) {
	// TODO: we need to add segments to groups and new groups whenever
	// the line number changes so we also need to keep track of the
	// current line number

	s.AddSegmentForNode(decl)

	switch dk := decl.Kind.(type) {
	case *DVariable:
		if dk.Init != nil {
			s.TraverseExpr(dk.Init)
		}
	case *DFunction:
		for _, stmt := range dk.Body {
			s.TraverseStmt(stmt)
		}
	}
}

func (s *SourceMapGenerator) TraverseExpr(expr *Expr) {
	s.AddSegmentForNode(expr)
}

func GenerateSourceMap(source parser.Source, jsMod *Module, outName string) (string, error) {
	s := &SourceMapGenerator{
		groups: [][]*Segment{},
	}

	s.TraverseModule(jsMod)

	sm := SourceMap{
		Version:        3,
		File:           outName,
		Sources:        []string{source.Path},
		SourcesContent: []string{source.Contents},
		Names:          []string{},
		Mappings:       EncodeSegments(s.groups),
	}

	return SerializeSourceMap(sm)
}
