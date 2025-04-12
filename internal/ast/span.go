package ast

type Location struct {
	Line   int `json:"line"`
	Column int `json:"column"`
}

type Span struct {
	Start Location `json:"start"`
	End   Location `json:"end"`
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
