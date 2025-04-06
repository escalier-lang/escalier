package parser

func parseDelimSeq[T any](
	p *Parser,
	terminator TokenType,
	separator TokenType,
	parserCombinator func() T,
) []T {
	items := []T{}

	token := p.lexer.peek()
	if token.Type == terminator {
		return items
	}

	item := parserCombinator()
	if any(item) == nil {
		return items
	}
	items = append(items, item)

	for {
		token = p.lexer.peek()
		if token.Type == separator {
			p.lexer.consume() // consume separator
			item := parserCombinator()
			if any(item) == nil {
				return items
			}
			items = append(items, item)
		} else {
			return items
		}
	}
}
