package ast

type Location struct {
	Line   int `json:"line"`
	Column int `json:"column"`
}

type Span struct {
	Start Location `json:"start"`
	End   Location `json:"end"`
}

func (s Span) Contains(loc Location) bool {
	return (s.Start.Line < loc.Line || (s.Start.Line == loc.Line && s.Start.Column <= loc.Column)) &&
		(s.End.Line > loc.Line || (s.End.Line == loc.Line && s.End.Column >= loc.Column))
}

func NewSpan(start, end Location) Span {
	return Span{Start: start, End: end}
}

func MergeSpans(a, b Span) Span {
	if a.Start.Line < b.Start.Line || (a.Start.Line == b.Start.Line && a.Start.Column < b.Start.Column) {
		return Span{Start: a.Start, End: b.End}
	}
	return Span{Start: b.Start, End: a.End}
}
