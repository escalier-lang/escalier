package compiler

import (
	"github.com/escalier-lang/escalier/internal/codegen"
	"github.com/escalier-lang/escalier/internal/parser"
)

func Compile(input string) string {

	source := parser.Source{
		Path:     "input.esc",
		Contents: input,
	}
	p1 := parser.NewParser(source)
	escMod := p1.ParseModule()
	jsMod := codegen.TransformModule(escMod)

	p2 := codegen.NewPrinter()
	p2.PrintModule(jsMod)

	output := p2.Output

	// TODO: generate sourcemap

	return output
}

type SourceMapGenerator struct {
	groups [][]*codegen.Segment
}

func (s *SourceMapGenerator) TraverseModule(module *codegen.Module) {
	for _, stmt := range module.Stmts {
		s.TraverseStmt(stmt)
	}
}

func (s *SourceMapGenerator) TraverseStmt(stmt *codegen.Stmt) {
	switch sk := stmt.Kind.(type) {
	case *codegen.SExpr:
		s.TraverseExpr(sk.Expr)
	case *codegen.SDecl:
		s.TraverseDecl(sk.Decl)
	case *codegen.SReturn:
		s.AddSegmentForNode(stmt)
		s.TraverseExpr(sk.Expr)
	}
}

func (s *SourceMapGenerator) AddSegmentForNode(generated codegen.Node) {
	generatedLine := generated.Source().Span().Start.Line

	if generatedLine > len(s.groups) {
		// add new groups
		for i := len(s.groups); i < generatedLine; i++ {
			s.groups = append(s.groups, []*codegen.Segment{})
		}
	}

	segment := &codegen.Segment{
		GeneratedStartColumn: generated.Span().Start.Column,
		SourceIndex:          0, // always 0 for now
		SourceStartLine:      generated.Source().Span().Start.Line,
		SourceStartColumn:    generated.Source().Span().Start.Column,
		NameIndex:            -1, // not used for now
	}

	s.groups[len(s.groups)-1] = append(s.groups[len(s.groups)-1], segment)
}

func (s *SourceMapGenerator) TraverseDecl(decl *codegen.Decl) {
	// TODO: we need to add segments to groups and new groups whenever
	// the line number changes so we also need to keep track of the
	// current line number

	s.AddSegmentForNode(decl)

	switch dk := decl.Kind.(type) {
	case *codegen.DVariable:
		if dk.Init != nil {
			s.TraverseExpr(dk.Init)
		}
	case *codegen.DFunction:
		for _, stmt := range dk.Body {
			s.TraverseStmt(stmt)
		}
	}
}

func (s *SourceMapGenerator) TraverseExpr(expr *codegen.Expr) {
	s.AddSegmentForNode(expr)
}

func GenerateSourceMap(jsMod *codegen.Module) string {

	// TODO:
	// - traverse the AST
	// - output a bunch of Segment objects
	// - we'll need to keep track of the grouping of the segments

	return ""
}
