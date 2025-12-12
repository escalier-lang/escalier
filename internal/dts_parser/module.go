package dts_parser

import (
	"github.com/escalier-lang/escalier/internal/ast"
)

// ============================================================================
// Phase 7: Namespaces & Modules
// ============================================================================

// parseImportDeclaration parses import statements
// Supports:
// - import foo from "module"
// - import { foo, bar } from "module"
// - import { foo as bar } from "module"
// - import * as foo from "module"
// - import foo, { bar } from "module"
// - import type { foo } from "module"
// - import "module"
func (p *DtsParser) parseImportDeclaration() Statement {
	startToken := p.expect(Import)
	if startToken == nil {
		return nil
	}

	// Check for type-only import
	typeOnly := false
	if p.peek().Type == Type {
		// Look ahead to see if this is 'import type {'
		nextToken := p.lexer.SaveState()
		p.consume() // consume 'type'
		if p.peek().Type == OpenBrace || p.peek().Type == Asterisk {
			typeOnly = true
		} else {
			p.lexer.RestoreState(nextToken)
		}
	}

	var defaultImport *Ident
	var namedImports []*ImportSpecifier
	var namespaceAs *Ident
	var from string

	// Check for side-effect import: import "module"
	if p.peek().Type == StrLit {
		fromToken := p.consume()
		from = fromToken.Value

		span := ast.Span{
			Start:    startToken.Span.Start,
			End:      fromToken.Span.End,
			SourceID: startToken.Span.SourceID,
		}

		return &ImportDecl{
			From:     from,
			TypeOnly: typeOnly,
			span:     span,
		}
	}

	// Check for namespace import: import * as foo from "module"
	if p.peek().Type == Asterisk {
		p.consume() // consume '*'

		if p.expect(As) == nil {
			return nil
		}

		namespaceAs = p.parseIdent()
		if namespaceAs == nil {
			return nil
		}
	} else if p.peek().Type == OpenBrace {
		// Named imports: import { foo, bar } from "module"
		namedImports = p.parseNamedImports()
		if namedImports == nil {
			return nil
		}
	} else {
		// Default import: import foo from "module"
		// or: import foo, { bar } from "module"
		defaultImport = p.parseIdent()
		if defaultImport == nil {
			return nil
		}

		// Check for comma (default + named imports)
		if p.peek().Type == Comma {
			p.consume() // consume ','

			if p.peek().Type == OpenBrace {
				namedImports = p.parseNamedImports()
				if namedImports == nil {
					return nil
				}
			} else if p.peek().Type == Asterisk {
				// import foo, * as bar from "module"
				p.consume() // consume '*'

				if p.expect(As) == nil {
					return nil
				}

				namespaceAs = p.parseIdent()
				if namespaceAs == nil {
					return nil
				}
			}
		}
	}

	// Parse 'from "module"'
	if p.expect(From) == nil {
		return nil
	}

	fromToken := p.expect(StrLit)
	if fromToken == nil {
		return nil
	}
	from = fromToken.Value

	span := ast.Span{
		Start:    startToken.Span.Start,
		End:      fromToken.Span.End,
		SourceID: startToken.Span.SourceID,
	}

	return &ImportDecl{
		DefaultImport: defaultImport,
		NamedImports:  namedImports,
		NamespaceAs:   namespaceAs,
		From:          from,
		TypeOnly:      typeOnly,
		span:          span,
	}
}

// parseNamedImports parses: { foo, bar as baz, ... }
func (p *DtsParser) parseNamedImports() []*ImportSpecifier {
	if p.expect(OpenBrace) == nil {
		return nil
	}

	var specifiers []*ImportSpecifier

	for p.peek().Type != CloseBrace && p.peek().Type != EndOfFile {
		specifier := p.parseImportSpecifier()
		if specifier != nil {
			specifiers = append(specifiers, specifier)
		}

		// Check for comma
		if p.peek().Type == Comma {
			p.consume()
			// Allow trailing comma
			if p.peek().Type == CloseBrace {
				break
			}
		} else {
			break
		}
	}

	if p.expect(CloseBrace) == nil {
		return nil
	}

	return specifiers
}

// parseImportSpecifier parses: foo or foo as bar
func (p *DtsParser) parseImportSpecifier() *ImportSpecifier {
	startToken := p.peek()

	imported := p.parseIdent()
	if imported == nil {
		return nil
	}

	local := imported // default to same name

	// Check for 'as' alias
	if p.peek().Type == As {
		p.consume() // consume 'as'
		local = p.parseIdent()
		if local == nil {
			return nil
		}
	}

	span := ast.Span{
		Start:    startToken.Span.Start,
		End:      local.Span().End,
		SourceID: startToken.Span.SourceID,
	}

	return &ImportSpecifier{
		Imported: imported,
		Local:    local,
		span:     span,
	}
}

// parseExportDeclaration parses export statements
// Supports:
// - export { foo, bar }
// - export { foo as bar }
// - export * from "module"
// - export * as foo from "module"
// - export { foo } from "module"
// - export default foo
// - export = foo
// - export as namespace foo
// - export <declaration>
// - export type { foo }
func (p *DtsParser) parseExportDeclaration() Statement {
	startToken := p.expect(Export)
	if startToken == nil {
		return nil
	}

	// Check for type-only export
	typeOnly := false
	if p.peek().Type == Type {
		// Look ahead to see if this is 'export type {'
		nextToken := p.lexer.SaveState()
		p.consume() // consume 'type'
		if p.peek().Type == OpenBrace {
			typeOnly = true
		} else {
			p.lexer.RestoreState(nextToken)
		}
	}

	// Check for 'export =' (TypeScript-specific)
	if p.peek().Type == Equal {
		return p.parseExportAssignment(startToken)
	}

	// Check for 'export as namespace' (TypeScript-specific)
	if p.peek().Type == As {
		nextToken := p.lexer.SaveState()
		p.consume() // consume 'as'
		if p.peek().Type == Namespace {
			p.consume() // consume 'namespace'
			name := p.parseIdent()
			if name == nil {
				return nil
			}

			span := ast.Span{
				Start:    startToken.Span.Start,
				End:      name.Span().End,
				SourceID: startToken.Span.SourceID,
			}

			// Represent 'export as namespace X' as a special export
			return &ExportDecl{
				NamedExports: []*ExportSpecifier{
					{
						Local:    name,
						Exported: name,
						span:     name.Span(),
					},
				},
				TypeOnly: false,
				span:     span,
			}
		}
		p.lexer.RestoreState(nextToken)
	}

	var declaration Statement
	var namedExports []*ExportSpecifier
	var from string
	var exportDefault bool
	var exportAll bool

	// Check for 'export default'
	if p.peek().Type == Identifier && p.peek().Value == "default" {
		p.consume() // consume 'default'
		exportDefault = true

		// Parse the default export declaration
		// For export default, we also allow class declarations (with or without declare)
		nextToken := p.peek()
		if nextToken.Type == Declare {
			p.consume() // consume 'declare'
			declaration = p.parseAmbientDeclaration()
		} else if nextToken.Type == Class || nextToken.Type == Abstract {
			// export default class or export default abstract class
			declaration = p.parseClassDeclaration()
		} else {
			declaration = p.parseTopLevelDeclaration()
		}

		if declaration == nil {
			p.reportError(nextToken.Span, "Expected declaration after 'export default'")
			return nil
		}

		span := ast.Span{
			Start:    startToken.Span.Start,
			End:      declaration.Span().End,
			SourceID: startToken.Span.SourceID,
		}

		return &ExportDecl{
			Declaration:   declaration,
			ExportDefault: exportDefault,
			TypeOnly:      typeOnly,
			span:          span,
		}
	}

	// Check for 'export *'
	if p.peek().Type == Asterisk {
		p.consume() // consume '*'
		exportAll = true

		// Check for 'export * as foo from "module"'
		var asName *Ident
		if p.peek().Type == As {
			p.consume() // consume 'as'
			asName = p.parseIdent()
			if asName == nil {
				return nil
			}
		}

		// Parse 'from "module"'
		if p.expect(From) == nil {
			return nil
		}

		fromToken := p.expect(StrLit)
		if fromToken == nil {
			return nil
		}
		from = fromToken.Value

		span := ast.Span{
			Start:    startToken.Span.Start,
			End:      fromToken.Span.End,
			SourceID: startToken.Span.SourceID,
		}

		// If there's an 'as' name, treat it as a named export
		if asName != nil {
			return &ExportDecl{
				NamedExports: []*ExportSpecifier{
					{
						Local:    asName,
						Exported: asName,
						span:     asName.Span(),
					},
				},
				From:     from,
				TypeOnly: typeOnly,
				span:     span,
			}
		}

		return &ExportDecl{
			ExportAll: exportAll,
			From:      from,
			TypeOnly:  typeOnly,
			span:      span,
		}
	}

	// Check for named exports: export { foo, bar }
	if p.peek().Type == OpenBrace {
		namedExports = p.parseNamedExports()
		if namedExports == nil {
			return nil
		}

		var endSpan ast.Span
		if len(namedExports) > 0 {
			endSpan = namedExports[len(namedExports)-1].Span()
		} else {
			endSpan = startToken.Span
		}

		// Check for 'from "module"' (re-export)
		if p.peek().Type == From {
			p.consume() // consume 'from'

			moduleSpecifier := p.expect(StrLit)
			if moduleSpecifier == nil {
				return nil
			}
			from = moduleSpecifier.Value
			endSpan = moduleSpecifier.Span
		}

		span := ast.Span{
			Start:    startToken.Span.Start,
			End:      endSpan.End,
			SourceID: startToken.Span.SourceID,
		}

		return &ExportDecl{
			NamedExports: namedExports,
			From:         from,
			TypeOnly:     typeOnly,
			span:         span,
		}
	}

	// Export a declaration: export var/let/const/function/class/interface/type/enum/namespace
	// Also handles: export declare var/let/const/function/class
	if p.peek().Type == Declare {
		p.consume() // consume 'declare'
		declaration = p.parseAmbientDeclaration()
	} else {
		declaration = p.parseTopLevelDeclaration()
	}

	if declaration == nil {
		return nil
	}

	span := ast.Span{
		Start:    startToken.Span.Start,
		End:      declaration.Span().End,
		SourceID: startToken.Span.SourceID,
	}

	return &ExportDecl{
		Declaration: declaration,
		TypeOnly:    typeOnly,
		span:        span,
	}
}

// parseNamedExports parses: { foo, bar as baz, ... }
func (p *DtsParser) parseNamedExports() []*ExportSpecifier {
	if p.expect(OpenBrace) == nil {
		return nil
	}

	var specifiers []*ExportSpecifier

	for p.peek().Type != CloseBrace && p.peek().Type != EndOfFile {
		specifier := p.parseExportSpecifier()
		if specifier != nil {
			specifiers = append(specifiers, specifier)
		}

		// Check for comma
		if p.peek().Type == Comma {
			p.consume()
			// Allow trailing comma
			if p.peek().Type == CloseBrace {
				break
			}
		} else {
			break
		}
	}

	if p.expect(CloseBrace) == nil {
		return nil
	}

	return specifiers
}

// parseExportSpecifier parses: foo or foo as bar
func (p *DtsParser) parseExportSpecifier() *ExportSpecifier {
	startToken := p.peek()

	local := p.parseIdent()
	if local == nil {
		return nil
	}

	exported := local // default to same name

	// Check for 'as' alias
	if p.peek().Type == As {
		p.consume() // consume 'as'
		exported = p.parseIdent()
		if exported == nil {
			return nil
		}
	}

	span := ast.Span{
		Start:    startToken.Span.Start,
		End:      exported.Span().End,
		SourceID: startToken.Span.SourceID,
	}

	return &ExportSpecifier{
		Local:    local,
		Exported: exported,
		span:     span,
	}
}

// parseExportAssignment parses: export = foo
func (p *DtsParser) parseExportAssignment(startToken *Token) Statement {
	if p.expect(Equal) == nil {
		return nil
	}

	// Parse the identifier being exported
	name := p.parseIdent()
	if name == nil {
		return nil
	}

	span := ast.Span{
		Start:    startToken.Span.Start,
		End:      name.Span().End,
		SourceID: startToken.Span.SourceID,
	}

	// Represent 'export = X' as a special export
	return &ExportDecl{
		NamedExports: []*ExportSpecifier{
			{
				Local:    name,
				Exported: name,
				span:     name.Span(),
			},
		},
		span: span,
	}
}
