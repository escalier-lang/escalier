package parser

import "github.com/moznion/go-optional"

func parseDelimSeq[T any](
	p *Parser,
	terminator TokenType,
	separator TokenType,
	// TODO: update this to return `nil` instead of `optional.None` when there
	// is no item
	parserCombinator func() (optional.Option[T], []*Error),
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
	if item.IsNone() {
		return items, errors
	}
	item.IfSome(func(item T) {
		items = append(items, item)
	})

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
			if item.IsNone() {
				return items, errors
			}
			item.IfSome(func(item T) {
				items = append(items, item)
			})
		} else {
			return items, errors
		}
	}
}
