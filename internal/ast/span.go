package ast

type Location struct {
	Line   int
	Column int
}

type Span struct {
	Start Location
	End   Location
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
