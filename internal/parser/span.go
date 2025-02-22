package parser

type Location struct {
	Line   int
	Column int
}

type Span struct {
	Start Location
	End   Location
}
