package parser

type Error struct {
	Span    Span
	Message string
}
