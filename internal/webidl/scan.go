package webidl

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// scan.go reads the four facts of the table out of a directory of WebIDL
// files. It is a scanner for those facts and not a WebIDL parser. It finds each
// top-level definition, reads the extended attributes in front of it, and looks
// inside an interface body for a `constructor(` operation. Everything else in
// the grammar is skipped by matching brackets up to the `;` that ends the
// definition.
//
// The input is the curated `@webref/idl` set, which webref validates with
// webidl2 before publishing. The scanner can rely on the files being
// well-formed, so it reports a malformed one as an error rather than
// recovering from it.

// ScanDir reads every `.idl` file directly under dir and returns the facts of
// each interface, callback interface and namespace they define, keyed by name.
//
// Partial definitions are merged into the definition they extend. A partial
// can add a constructor, and it can add `[Transferable]`, which describes the
// interface wherever it is written. `mediacapture-extensions.idl` makes
// `MediaStreamTrack` transferable that way. A partial's `[Exposed]` limits only
// the members it declares, so it leaves the interface's Exposed alone. A
// partial names the identifier it extends, so a partial of an interface keyed
// under its `[LegacyNamespace]` path merges into that key. A name that only
// partial definitions mention, or that two non-partial definitions declare, is
// an error. Interface mixins are left out because a mixin has no interface
// object of its own.
func ScanDir(dir string) (map[string]Definition, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, err
	}
	var names []string
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".idl") {
			names = append(names, entry.Name())
		}
	}
	sort.Strings(names)

	sources := make(map[string]string, len(names))
	for _, name := range names {
		data, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			return nil, err
		}
		sources[name] = string(data)
	}
	return Scan(sources)
}

// Scan returns the facts the given WebIDL sources define, keyed by definition
// name. sources maps a file name, used only in error messages, to its text.
// The merge rules are the ones ScanDir documents.
func Scan(sources map[string]string) (map[string]Definition, error) {
	files := make([]string, 0, len(sources))
	for name := range sources {
		files = append(files, name)
	}
	sort.Strings(files)

	defs := map[string]Definition{}
	// declaredIn records the file of each non-partial definition, for the
	// duplicate error.
	declaredIn := map[string]string{}
	// keyOf maps each non-partial definition's identifier to the key the table
	// holds it under, which differs for an interface with `[LegacyNamespace]`.
	keyOf := map[string]string{}
	// partials are merged after every file is read, because a partial can
	// come before the definition it extends. They keep file and source order,
	// so the first bad partial is the one an error names.
	type partialIn struct {
		file string
		scanned
	}
	var partials []partialIn

	for _, file := range files {
		parsed, err := scanFile(sources[file])
		if err != nil {
			return nil, fmt.Errorf("%s: %w", file, err)
		}
		for _, p := range parsed {
			if p.partial {
				partials = append(partials, partialIn{file: file, scanned: p})
				continue
			}
			if prev, dup := declaredIn[p.name]; dup {
				return nil, fmt.Errorf("%s: %s is also defined in %s", file, p.name, prev)
			}
			// WebIDL requires an identifier to name one definition, whatever
			// `[LegacyNamespace]` it carries. A second one would leave a partial
			// with two definitions to extend, so it is an error.
			if prev, dup := keyOf[p.ident]; dup {
				return nil, fmt.Errorf("%s: %s is defined as both %s in %s and %s",
					file, p.ident, prev, declaredIn[prev], p.name)
			}
			declaredIn[p.name] = file
			keyOf[p.ident] = p.name
			defs[p.name] = p.def
		}
	}

	for _, p := range partials {
		key, ok := keyOf[p.ident]
		if !ok {
			return nil, fmt.Errorf("%s: partial %s extends no definition", p.file, p.ident)
		}
		def := defs[key]
		if def.Kind != p.def.Kind {
			return nil, fmt.Errorf("%s: partial %s %s extends %s %s in %s",
				p.file, p.def.Kind.keyword(), p.ident, def.Kind.keyword(), key, declaredIn[key])
		}
		def.Constructible = def.Constructible || p.def.Constructible
		def.Transferable = def.Transferable || p.def.Transferable
		defs[key] = def
	}
	return defs, nil
}

// scanned is one top-level definition as scanFile read it, before partials
// are merged.
type scanned struct {
	// ident is the identifier the source writes, and name is the key the table
	// holds the definition under.
	ident   string
	name    string
	partial bool
	def     Definition
}

// scanFile returns the interfaces, callback interfaces and namespaces one
// WebIDL source defines, in source order.
func scanFile(src string) ([]scanned, error) {
	toks, err := tokenize(src)
	if err != nil {
		return nil, err
	}
	s := &scanner{toks: toks}
	var out []scanned
	for !s.done() {
		var attrs []extAttr
		if s.peek() == "[" {
			attrs, err = s.extAttrs()
			if err != nil {
				return nil, err
			}
		}

		partial := s.accept("partial")
		var kind Kind
		switch {
		case s.accept("callback"):
			if !s.accept("interface") {
				// A callback function, `callback Name = ...;`.
				if err := s.skipDefinition(); err != nil {
					return nil, err
				}
				continue
			}
			kind = KindCallbackInterface
		case s.accept("interface"):
			if s.accept("mixin") {
				if err := s.skipDefinition(); err != nil {
					return nil, err
				}
				continue
			}
			kind = KindInterface
		case s.accept("namespace"):
			kind = KindNamespace
		default:
			// A dictionary, enum, typedef, or includes statement.
			if err := s.skipDefinition(); err != nil {
				return nil, err
			}
			continue
		}

		ident := s.next()
		if !isIdent(ident) {
			return nil, fmt.Errorf("expected a definition name, found %q", ident)
		}
		name := ident
		ctor, err := s.body()
		if err != nil {
			return nil, fmt.Errorf("%s: %w", name, err)
		}

		def := Definition{Kind: kind, Constructible: ctor}
		for _, attr := range attrs {
			switch attr.name {
			case "LegacyNamespace":
				// `[LegacyNamespace=WebAssembly] interface Module` is reached as
				// `WebAssembly.Module`, so that path is its name.
				if len(attr.values) == 1 {
					name = attr.values[0] + "." + name
				}
			case "Exposed":
				def.Exposed = attr.values
			case "Global":
				def.Global = attr.values
			case "Transferable":
				def.Transferable = true
			}
		}
		out = append(out, scanned{ident: ident, name: name, partial: partial, def: def})
	}
	return out, nil
}

// extAttr is one entry of an extended attribute list. values holds the
// identifiers after `=`, a single one for `Name=Ident` and each list member for
// `Name=(A, B)`. `Exposed=*` yields the value `*`.
type extAttr struct {
	name   string
	values []string
}

type scanner struct {
	toks []string
	pos  int
}

func (s *scanner) done() bool { return s.pos >= len(s.toks) }

// peek returns the current token, or "" at the end of input.
func (s *scanner) peek() string {
	if s.done() {
		return ""
	}
	return s.toks[s.pos]
}

// next returns the current token and advances past it.
func (s *scanner) next() string {
	tok := s.peek()
	if !s.done() {
		s.pos++
	}
	return tok
}

// accept advances past the current token when it is tok, and reports
// whether it did.
func (s *scanner) accept(tok string) bool {
	if s.peek() == tok {
		s.pos++
		return true
	}
	return false
}

// extAttrs reads a bracketed extended attribute list, starting at its `[`
// and ending after its `]`.
func (s *scanner) extAttrs() ([]extAttr, error) {
	s.next()
	var attrs []extAttr
	for {
		name := s.next()
		if !isIdent(name) {
			return nil, fmt.Errorf("expected an extended attribute name, found %q", name)
		}
		attr := extAttr{name: name}
		if s.peek() == "(" {
			// An argument list directly after the name, as in
			// `[Constructor(DOMString type)]`.
			if err := s.skipBalanced(); err != nil {
				return nil, err
			}
		}
		if s.accept("=") {
			if s.accept("(") {
				for !s.accept(")") {
					tok := s.next()
					if tok == "" {
						return nil, fmt.Errorf("unterminated value list for %s", name)
					}
					if tok != "," {
						attr.values = append(attr.values, tok)
					}
				}
			} else {
				attr.values = []string{s.next()}
				// An argument list after the value, as in
				// `[LegacyFactoryFunction=Image(optional unsigned long width)]`.
				if s.peek() == "(" {
					if err := s.skipBalanced(); err != nil {
						return nil, err
					}
				}
			}
		}
		attrs = append(attrs, attr)

		switch tok := s.next(); tok {
		case ",":
			continue
		case "]":
			return attrs, nil
		default:
			return nil, fmt.Errorf("expected , or ] in extended attributes, found %q", tok)
		}
	}
}

// body reads from after a definition's name to after the `;` that ends it,
// and reports whether the body declares a `constructor(` operation.
//
// The keyword `constructor` may also name an argument, as in
// `undefined define(DOMString name, CustomElementConstructor constructor)`, but
// there it is followed by `,` or `)` and never by `(`.
func (s *scanner) body() (bool, error) {
	for s.peek() != "{" {
		if s.next() == "" {
			return false, fmt.Errorf("expected a body")
		}
	}
	s.next()
	ctor := false
	depth := 1
	for depth > 0 {
		tok := s.next()
		switch tok {
		case "":
			return false, fmt.Errorf("unterminated body")
		case "{":
			depth++
		case "}":
			depth--
		case "constructor":
			if s.peek() == "(" {
				ctor = true
			}
		}
	}
	if !s.accept(";") {
		return false, fmt.Errorf("expected ; after the body, found %q", s.peek())
	}
	return ctor, nil
}

// skipDefinition advances past the `;` that ends the current definition,
// stepping over any braces, brackets and parentheses in between.
func (s *scanner) skipDefinition() error {
	for {
		switch s.peek() {
		case "":
			return fmt.Errorf("unterminated definition")
		case ";":
			s.next()
			return nil
		case "{", "(", "[":
			if err := s.skipBalanced(); err != nil {
				return err
			}
		default:
			s.next()
		}
	}
}

// skipBalanced advances past the bracketed group that starts at the current
// token.
func (s *scanner) skipBalanced() error {
	depth := 0
	for {
		switch s.next() {
		case "":
			return fmt.Errorf("unbalanced brackets")
		case "{", "(", "[":
			depth++
		case "}", ")", "]":
			depth--
			if depth == 0 {
				return nil
			}
		}
	}
}

// tokenize splits WebIDL source into identifiers, numbers, string literals
// and single punctuation characters, dropping whitespace and comments.
func tokenize(src string) ([]string, error) {
	var toks []string
	for i := 0; i < len(src); {
		c := src[i]
		switch {
		case c == ' ' || c == '\t' || c == '\n' || c == '\r':
			i++
		case strings.HasPrefix(src[i:], "//"):
			end := strings.IndexByte(src[i:], '\n')
			if end < 0 {
				return toks, nil
			}
			i += end + 1
		case strings.HasPrefix(src[i:], "/*"):
			end := strings.Index(src[i+2:], "*/")
			if end < 0 {
				return nil, fmt.Errorf("unterminated comment")
			}
			i += 2 + end + 2
		case c == '"':
			end := strings.IndexByte(src[i+1:], '"')
			if end < 0 {
				return nil, fmt.Errorf("unterminated string")
			}
			toks = append(toks, src[i:i+1+end+1])
			i += 1 + end + 1
		case isIdentStart(c):
			j := i + 1
			for j < len(src) && (isIdentStart(src[j]) || isDigit(src[j]) || src[j] == '-') {
				j++
			}
			toks = append(toks, src[i:j])
			i = j
		case isDigit(c):
			j := i + 1
			for j < len(src) && (isIdentStart(src[j]) || isDigit(src[j]) || src[j] == '.') {
				j++
			}
			toks = append(toks, src[i:j])
			i = j
		default:
			toks = append(toks, src[i:i+1])
			i++
		}
	}
	return toks, nil
}

func isIdentStart(c byte) bool {
	return c == '_' || ('a' <= c && c <= 'z') || ('A' <= c && c <= 'Z')
}

func isDigit(c byte) bool { return '0' <= c && c <= '9' }

// isIdent reports whether tok is an identifier token.
func isIdent(tok string) bool {
	return tok != "" && isIdentStart(tok[0])
}
