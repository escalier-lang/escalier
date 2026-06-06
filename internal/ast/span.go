package ast

import "strconv"

type Source struct {
	Path     string
	Contents string
	ID       int
}

type Location struct {
	Line   int `json:"line"`
	Column int `json:"column"`
}

func (l Location) String() string {
	return strconv.Itoa(l.Line) + ":" + strconv.Itoa(l.Column)
}

type Span struct {
	Start    Location `json:"start"`
	End      Location `json:"end"`
	SourceID int
}

func (s Span) String() string {
	return s.Start.String() + "-" + s.End.String()
}

func (s Span) Contains(loc Location) bool {
	return (s.Start.Line < loc.Line || (s.Start.Line == loc.Line && s.Start.Column <= loc.Column)) &&
		(s.End.Line > loc.Line || (s.End.Line == loc.Line && s.End.Column >= loc.Column))
}

// ContainsSpan reports whether inner lies entirely within s: the same source, and
// both of inner's endpoints are contained in s. Used to decide whether a
// finer-grained span (e.g. an operand's source node) sits inside a coarser one
// (e.g. a constraint site) before preferring it for blame.
func (s Span) ContainsSpan(inner Span) bool {
	return s.SourceID == inner.SourceID && s.Contains(inner.Start) && s.Contains(inner.End)
}

func NewSpan(start, end Location, sourceID int) Span {
	return Span{Start: start, End: end, SourceID: sourceID}
}

func MergeSpans(a, b Span) Span {
	if a.Start.Line < b.Start.Line || (a.Start.Line == b.Start.Line && a.Start.Column < b.Start.Column) {
		return Span{Start: a.Start, End: b.End, SourceID: a.SourceID}
	}
	return Span{Start: b.Start, End: a.End, SourceID: a.SourceID}
}
