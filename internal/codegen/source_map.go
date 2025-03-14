package codegen

import "encoding/json"

type SourceMap struct {
	Version        int      `json:"version"`        // this should always be the number 3
	File           string   `json:"file"`           // the generated file
	Sources        []string `json:"sources"`        // the original files
	SourcesContent []string `json:"sourcesContent"` // TODO: check that omitting this works
	Names          []string `json:"names"`          // TODO: investigate using this
	Mappings       string   `json:"mappings"`
}

func GenerateSourceMap(sourcemap SourceMap) (string, error) {
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
