package codegen

// type Mapping struct {
// 	GeneratedLine   int32 // 0-based
// 	GeneratedColumn int32 // 0-based count of UTF-16 code units

// 	SourceIndex    int32       // 0-based
// 	OriginalLine   int32       // 0-based
// 	OriginalColumn int32       // 0-based count of UTF-16 code units
// 	OriginalName   ast.Index32 // 0-based, optional
// }

type SourceMap struct {
	Version        int    // this should always be the number 3
	File           string `json:"omitempty"` //
	Sources        []string
	SourcesContent []string
	Names          []string
	Mappings       string
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
	prevGenStartCol := -1  // TODO: reset after each line
	prevSrcStartLine := -1 // TODO: don't reset after each line
	prevSrcStartCol := -1  // TODO: don't reset after each line

	for j, g := range groups {
		if j > 0 {
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
