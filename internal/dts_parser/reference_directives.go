package dts_parser

import (
	"regexp"
	"strings"
)

// ReferenceKind is the attribute a `/// <reference … />` directive names its target with.
type ReferenceKind string

const (
	// ReferencePath names a file, relative to the file the directive is in.
	ReferencePath ReferenceKind = "path"
	// ReferenceTypes names a types package, resolved the way an import is.
	ReferenceTypes ReferenceKind = "types"
	// ReferenceLib names one of TypeScript's standard library files, such as `es2015`.
	ReferenceLib ReferenceKind = "lib"
)

// ReferenceDirective is one `/// <reference … />` directive, such as
// `/// <reference path="global.d.ts" />`.
type ReferenceDirective struct {
	Kind ReferenceKind
	// Value is the attribute's value, `global.d.ts` in the example.
	Value string
}

// referenceDirectivePattern matches a directive line and captures its attribute and
// value. The attribute is matched loosely, so a directive that names neither `path`,
// `types` nor `lib` still matches and is dropped afterwards.
var referenceDirectivePattern = regexp.MustCompile(`^///\s*<reference\s+(\w+)\s*=\s*["']([^"']*)["']`)

// ParseReferenceDirectives returns the `/// <reference … />` directives at the top of a
// `.d.ts` file, in source order. TypeScript reads a directive only before the file's
// first statement, so the scan stops at the first line that is not blank or a comment.
// A directive whose attribute is not one of the ReferenceKind values is left out.
func ParseReferenceDirectives(contents string) []ReferenceDirective {
	var directives []ReferenceDirective
	inBlockComment := false
	for _, line := range strings.Split(strings.TrimPrefix(contents, "\uFEFF"), "\n") {
		trimmed := strings.TrimSpace(line)
		// Block comments are stripped from the front of the line, so code after a
		// `/* … */` on the same line ends the scan.
		for {
			if inBlockComment {
				_, after, found := strings.Cut(trimmed, "*/")
				if !found {
					trimmed = ""
					break
				}
				inBlockComment = false
				trimmed = strings.TrimSpace(after)
				continue
			}
			if !strings.HasPrefix(trimmed, "/*") {
				break
			}
			inBlockComment = true
			trimmed = trimmed[2:]
		}
		switch {
		case trimmed == "":
			continue
		case strings.HasPrefix(trimmed, "///"):
			match := referenceDirectivePattern.FindStringSubmatch(trimmed)
			if match == nil {
				continue
			}
			switch kind := ReferenceKind(match[1]); kind {
			case ReferencePath, ReferenceTypes, ReferenceLib:
				directives = append(directives, ReferenceDirective{Kind: kind, Value: match[2]})
			}
		case strings.HasPrefix(trimmed, "//"):
			continue
		default:
			return directives
		}
	}
	return directives
}
