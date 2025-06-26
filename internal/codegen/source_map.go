package codegen

import (
	"encoding/json"
)

type SourceMap struct {
	Version        int       `json:"version"`        // this should always be the number 3
	File           string    `json:"file"`           // the generated file
	Sources        []string  `json:"sources"`        // the original files
	SourcesContent []*string `json:"sourcesContent"` // TODO: check that omitting this works
	Names          []string  `json:"names"`          // TODO: investigate using this
	Mappings       string    `json:"mappings"`
}

func SerializeSourceMap(sourcemap SourceMap) string {
	bytes, err := json.Marshal(sourcemap)
	if err != nil {
		panic(err)
	}
	return string(bytes)
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

func (s *SourceMapGenerator) TraverseStmt(stmt Stmt) {
	// TODO: check if stmt.Span() is nil.  If it is, we should return an error
	switch sk := stmt.(type) {
	case *ExprStmt:
		s.TraverseExpr(sk.Expr)
	case *DeclStmt:
		s.TraverseDecl(sk.Decl)
	case *ReturnStmt:
		s.AddSegmentForNode(stmt)
		if sk.Expr != nil {
			s.TraverseExpr(sk.Expr)
		}
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
	if source == nil {
		return
	}

	sourceSpan := source.Span()
	if sourceSpan.Start.Line == 0 && sourceSpan.Start.Column == 0 {
		// this is a special case where the source is nil
		// so we don't need to add a segment
		return
	}

	segment := &Segment{
		GeneratedStartColumn: generated.Span().Start.Column - 1,
		SourceIndex:          0, // always 0 for now
		SourceStartLine:      sourceSpan.Start.Line - 1,
		SourceStartColumn:    sourceSpan.Start.Column - 1,
		NameIndex:            -1, // not used for now
	}

	s.groups[len(s.groups)-1] = append(s.groups[len(s.groups)-1], segment)
}

func (s *SourceMapGenerator) TraversePattern(pattern Pat) {
	s.AddSegmentForNode(pattern)

	switch pk := pattern.(type) {
	case *IdentPat:
		s.AddSegmentForNode(pk)
	case *TuplePat:
		for _, elem := range pk.Elems {
			s.TraversePattern(elem)
			switch elem := elem.(type) {
			case *IdentPat:
				elem.Default.IfSome(func(e Expr) {
					s.TraverseExpr(e)
				})
			default:
				// TODO: handle defaults for other types of patterns
			}
		}
	case *ObjectPat:
		for _, elem := range pk.Elems {
			switch elem := elem.(type) {
			case *ObjKeyValuePat:
				// s.AddSegmentForNode(elem.Key)
				s.TraversePattern(elem.Value)
				elem.Default.IfSome(func(e Expr) {
					s.TraverseExpr(e)
				})
			case *ObjShorthandPat:
				// s.AddSegmentForNode(elem.Key)
				elem.Default.IfSome(func(e Expr) {
					s.TraverseExpr(e)
				})
			case *ObjRestPat:
				s.TraversePattern(elem.Pattern)
			default:
				panic("TODO - TraversePattern")
			}
		}
	case *RestPat:
		s.TraversePattern(pk.Pattern)
	default:
		panic("TODO - TraversePattern")
	}
}

func (s *SourceMapGenerator) TraverseDecl(decl Decl) {
	// TODO: we need to add segments to groups and new groups whenever
	// the line number changes so we also need to keep track of the
	// current line number

	s.AddSegmentForNode(decl)

	switch dk := decl.(type) {
	case *VarDecl:
		// TODO: traverse the pattern to get more granular segments
		// but for now we just add the segment for the decl's pattern
		// and the init expression
		for _, d := range dk.Decls {
			s.TraversePattern(d.Pattern)
			if d.Init != nil {
				s.TraverseExpr(d.Init)
			}
			// TODO: tranverse the type annotation if it exists
		}
	case *FuncDecl:
		for _, param := range dk.Params {
			s.AddSegmentForNode(param.Pattern)
		}
		dk.Body.IfSome(func(body []Stmt) {
			for _, stmt := range body {
				s.TraverseStmt(stmt)
			}
		})
	}
}

func (s *SourceMapGenerator) TraverseFunc(fn *FuncExpr) {
	s.AddSegmentForNode(fn)

	for _, param := range fn.Params {
		s.AddSegmentForNode(param.Pattern)
	}

	for _, stmt := range fn.Body {
		s.TraverseStmt(stmt)
	}
}

func (s *SourceMapGenerator) TraverseExpr(expr Expr) {
	if expr == nil {
		return
	}

	s.AddSegmentForNode(expr)

	switch ek := expr.(type) {
	case *BinaryExpr:
		s.TraverseExpr(ek.Left)
		s.TraverseExpr(ek.Right)
	case *UnaryExpr:
		s.TraverseExpr(ek.Arg)
	case *CallExpr:
		s.TraverseExpr(ek.Callee)
		for _, arg := range ek.Args {
			s.TraverseExpr(arg)
		}
	case *FuncExpr:
		for _, param := range ek.Params {
			s.AddSegmentForNode(param.Pattern)
		}
		for _, stmt := range ek.Body {
			s.TraverseStmt(stmt)
		}
	case *IndexExpr:
		s.TraverseExpr(ek.Object)
		s.TraverseExpr(ek.Index)
	case *MemberExpr:
		s.TraverseExpr(ek.Object)
		s.AddSegmentForNode(ek.Prop)
	case *ArrayExpr:
		for _, elem := range ek.Elems {
			s.TraverseExpr(elem)
		}
	case *ObjectExpr:
		for _, elem := range ek.Elems {
			switch elem := elem.(type) {
			case *MethodExpr:
				s.AddSegmentForNode(elem.Name)
				for _, param := range elem.Params {
					s.AddSegmentForNode(param.Pattern)
				}
				for _, stmt := range elem.Body {
					s.TraverseStmt(stmt)
				}
			case *GetterExpr:
				s.AddSegmentForNode(elem.Name)
				for _, stmt := range elem.Body {
					s.TraverseStmt(stmt)
				}
			case *SetterExpr:
				s.AddSegmentForNode(elem.Name)
				for _, param := range elem.Params {
					s.AddSegmentForNode(param.Pattern)
				}
				for _, stmt := range elem.Body {
					s.TraverseStmt(stmt)
				}
			case *PropertyExpr:
				s.AddSegmentForNode(elem.Key)
				elem.Value.IfSome(func(value Expr) {
					s.TraverseExpr(value)
				})
			case *RestSpreadExpr:
				s.AddSegmentForNode(elem)
				s.TraverseExpr(elem.Arg)
			}
		}
	case *IdentExpr, *LitExpr:
		// leave nodes are handled by the AddSegmentForNode call at the	top
		// of this function
	default:
		panic("TODO - TraverseExpr")
	}
}

func GenerateSourceMap(srcPath string, srcContent string, jsMod *Module, outName string) string {
	s := &SourceMapGenerator{
		groups: [][]*Segment{},
	}

	s.TraverseModule(jsMod)

	sm := SourceMap{
		Version:        3,
		File:           outName,
		Sources:        []string{srcPath},
		SourcesContent: []*string{&srcContent},
		Names:          []string{},
		Mappings:       EncodeSegments(s.groups),
	}

	return SerializeSourceMap(sm)
}
