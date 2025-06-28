package parser

func parseDelimSeq[T interface{}](
	p *Parser,
	terminator TokenType,
	separator TokenType,
	// TODO: update this to return `nil` instead of `optional.None` when there
	// is no item
	parserCombinator func() T,
) []T {
	items := []T{}

	// Empty sequence
	token := p.lexer.peek()
	if token.Type == terminator {
		return items
	}

	item := parserCombinator()
	if interface{}(item) == nil {
		return items
	}
	items = append(items, item)

	for {
		token = p.lexer.peek()
		if token.Type == separator {
			p.lexer.consume() // consume separator

			token = p.lexer.peek()
			if token.Type == terminator {
				return items
			}

			item := parserCombinator()
			if interface{}(item) == nil {
				return items
			}
			items = append(items, item)
		} else {
			return items
		}
	}
}
