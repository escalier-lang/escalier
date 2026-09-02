package ast

import (
	"strconv"
	"sync"
)

type Source struct {
	Path     string
	Contents string
	ID       int

	lineMapOnce sync.Once
	lineMap     *LineMap
}

// LineMap returns a map over this source's contents, building it on the first
// call and reusing it after. Use it to turn the byte offsets on a Span into
// the line and column a diagnostic or an editor needs.
func (s *Source) LineMap() *LineMap {
	s.lineMapOnce.Do(func() {
		s.lineMap = NewLineMap(s.Contents)
	})
	return s.lineMap
}

// Location is a position in a source file, held as a 0-based byte offset. It
// is the only position the lexer records, so nothing has to keep a second
// coordinate in step with it.
//
// Line and column are derived rather than stored, because a column depends on
// the unit it counts. A diagnostic wants code points and an LSP client wants
// UTF-16 code units, and one stored number cannot be both. Convert with
// Source.LineMap and a ColumnEncoding at the point the position is displayed.
//
// Only the lexer fills Offset in. A span the checker or a converter
// synthesized carries 0, so code that slices source text by Offset has to
// know its span came from parsing.
type Location struct {
	Offset int `json:"offset"`
}

func (l Location) String() string {
	return strconv.Itoa(l.Offset)
}

type Span struct {
	Start    Location `json:"start"`
	End      Location `json:"end"`
	SourceID int
}

func (s Span) String() string {
	return s.Start.String() + "-" + s.End.String()
}

// Contains reports whether loc falls within s. Both endpoints count, so a
// location at the very end of a span is inside it. Hover relies on that,
// where the cursor sits just past the last character of the expression it
// asks about.
//
// The source is not compared, so a caller holding locations from more than
// one file has to narrow by SourceID itself. ContainsSpan does compare it.
func (s Span) Contains(loc Location) bool {
	return s.Start.Offset <= loc.Offset && loc.Offset <= s.End.Offset
}

// ContainsSpan reports whether inner lies entirely within s, meaning the same
// source and both of inner's endpoints contained in s. Used to decide whether
// a finer-grained span such as an operand's source node sits inside a coarser
// one such as a constraint site before preferring it for blame.
func (s Span) ContainsSpan(inner Span) bool {
	return s.SourceID == inner.SourceID && s.Contains(inner.Start) && s.Contains(inner.End)
}

// SpanString renders a span as "line:column-line:column", the form a
// diagnostic shows a person. source must be the file the span indexes into.
// It falls back to Span.String, which renders byte offsets, when source is nil.
func SpanString(source *Source, span Span) string {
	// A span reaching past the end of the file did not come from it, which is
	// what a synthesized span looks like when a caller pairs it with a source.
	// Byte offsets say so plainly; a clamped line and column would not.
	if source == nil || span.End.Offset > len(source.Contents) {
		return span.String()
	}
	lineMap := source.LineMap()
	startLine, startColumn := lineMap.Position(span.Start.Offset, CodePointColumns)
	endLine, endColumn := lineMap.Position(span.End.Offset, CodePointColumns)
	return strconv.Itoa(startLine) + ":" + strconv.Itoa(startColumn) +
		"-" + strconv.Itoa(endLine) + ":" + strconv.Itoa(endColumn)
}

func NewSpan(start, end Location, sourceID int) Span {
	return Span{Start: start, End: end, SourceID: sourceID}
}

func MergeSpans(a, b Span) Span {
	if a.Start.Offset < b.Start.Offset {
		return Span{Start: a.Start, End: b.End, SourceID: a.SourceID}
	}
	return Span{Start: b.Start, End: a.End, SourceID: a.SourceID}
}
