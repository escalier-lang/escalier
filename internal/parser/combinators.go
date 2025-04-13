package parser

import "github.com/moznion/go-optional"

func isNil[T comparable](arg T) bool {
	var t T
	return arg == t
}

func parseDelimSeq[T comparable](
	p *Parser,
	terminator TokenType,
	separator TokenType,
	parserCombinator func() optional.Option[T],
) []T {
	items := []T{}

	token := p.lexer.peek()
	if token.Type == terminator {
		return items
	}

	item := parserCombinator()
	if item.IsNone() {
		return items
	}
	item.IfSome(func(item T) {
		items = append(items, item)
	})

	for {
		token = p.lexer.peek()
		if token.Type == separator {
			p.lexer.consume() // consume separator
			item := parserCombinator()
			if item.IsNone() {
				return items
			}
			item.IfSome(func(item T) {
				items = append(items, item)
			})
		} else {
			return items
		}
	}
}
