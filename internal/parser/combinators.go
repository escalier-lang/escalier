package parser

func parseDelimSeq[T any](
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
	if any(item) == nil {
		return items
	}
	items = append(items, item)

	for {
		// Check if context has been cancelled (timeout or cancellation)
		select {
		case <-p.ctx.Done():
			// Return what we have so far when context is done
			return items
		default:
			// continue
		}

		token = p.lexer.peek()
		if token.Type == EndOfFile {
			// If we hit EOF before finding terminator, return what we have
			return items
		} else if token.Type == separator {
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
