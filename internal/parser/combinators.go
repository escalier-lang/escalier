package parser

func parseDelimSeq[T interface{}](
	p *Parser,
	terminator TokenType,
	separator TokenType,
	// TODO: update this to return `nil` instead of `optional.None` when there
	// is no item
	parserCombinator func() (T, []*Error),
) ([]T, []*Error) {
	items := []T{}
	errors := []*Error{}

	// Empty sequence
	token := p.lexer.peek()
	if token.Type == terminator {
		return items, errors
	}

	item, itemErrors := parserCombinator()
	errors = append(errors, itemErrors...)
	if interface{}(item) == nil {
		return items, errors
	}
	items = append(items, item)

	for {
		token = p.lexer.peek()
		if token.Type == separator {
			p.lexer.consume() // consume separator

			token = p.lexer.peek()
			if token.Type == terminator {
				return items, errors
			}

			item, itemErrors := parserCombinator()
			errors = append(errors, itemErrors...)
			if interface{}(item) == nil {
				return items, errors
			}
			items = append(items, item)
		} else {
			return items, errors
		}
	}
}
