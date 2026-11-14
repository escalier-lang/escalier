// Command write_test_signatures writes the signature the checker infers for each function
// and method that leaves part of it out, in the Escalier source held by the string literals
// of Go test files. It applies the edits the language server's "Write the inferred
// signature" code action makes, as write_signatures does for fixtures.
//
// Usage:
//
//	go run ./tools/write_test_signatures [-stdlib-dir dir] [-n] [-v] <file.go>...
//
// Each string literal that parses as an Escalier module, or else as a script, is checked
// on its own against the standard library. Three kinds of source are left as they are:
//
//   - a literal joined to another with +, which holds only part of a source;
//   - a function that leaves out a parameter type, which tests parameter inference;
//   - a literal whose signature to write holds a type the checker could not resolve, or a
//     `never` return inferred from a body with an error.
//
// -n reports what would change without writing, and -v names each literal skipped and why.
package main

import (
	"flag"
	"fmt"
	goast "go/ast"
	goparser "go/parser"
	gotoken "go/token"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/compiler"
	"github.com/escalier-lang/escalier/internal/sigedit"
)

func main() {
	stdlibDir := flag.String("stdlib-dir", filepath.Join("internal", "interop", "data"),
		"directory containing the stdlib `.esc` files (std/, web/, node/)")
	dryRun := flag.Bool("n", false, "report what would change without writing")
	verbose := flag.Bool("v", false, "name each literal that is skipped and why")
	flag.Parse()
	if flag.NArg() == 0 {
		fmt.Fprintln(os.Stderr, "usage: write_test_signatures [-stdlib-dir dir] [-n] [-v] <file.go>...")
		os.Exit(2)
	}
	// The checker reads the standard library's location from the environment.
	if err := os.Setenv("ESCALIER_STDLIB_DIR", *stdlibDir); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	var total stats
	failed := false
	for _, path := range flag.Args() {
		s, err := writeFile(path, *dryRun, *verbose)
		if err != nil {
			fmt.Fprintf(os.Stderr, "%s: %v\n", path, err)
			failed = true
			continue
		}
		if s.edited > 0 || s.unresolved > 0 || s.unwritable > 0 {
			fmt.Printf("%s: %d literals edited, %d unresolved, %d signatures unwritable\n",
				path, s.edited, s.unresolved, s.unwritable)
		}
		total.add(s)
	}
	fmt.Printf("total: %d literals edited, %d unresolved, %d signatures unwritable\n",
		total.edited, total.unresolved, total.unwritable)
	if failed {
		os.Exit(1)
	}
}

// stats counts what writeFile did with the literals of one file.
type stats struct {
	// edited counts the literals that gained at least one signature part.
	edited int
	// unresolved counts the literals left as they are because the signature to write holds
	// a type the checker could not resolve.
	unresolved int
	// unwritable counts the functions whose inferred signature the edits cannot write.
	unwritable int
}

func (s *stats) add(o stats) {
	s.edited += o.edited
	s.unresolved += o.unresolved
	s.unwritable += o.unwritable
}

// literalEdit replaces the bytes [start, end) of a Go file with text.
type literalEdit struct {
	start, end int
	text       string
}

// writeFile rewrites the string literals of the Go file at path that hold Escalier source
// with a function whose inferred signature can be written.
func writeFile(path string, dryRun, verbose bool) (stats, error) {
	var s stats
	contents, err := os.ReadFile(path)
	if err != nil {
		return s, err
	}
	fset := gotoken.NewFileSet()
	file, err := goparser.ParseFile(fset, path, contents, goparser.ParseComments)
	if err != nil {
		return s, err
	}
	// A literal joined to another with + holds only part of a source, such as the body
	// after a shared `decls` prefix, so it does not check on its own.
	joined := map[*goast.BasicLit]bool{}
	goast.Inspect(file, func(n goast.Node) bool {
		if bin, ok := n.(*goast.BinaryExpr); ok && bin.Op == gotoken.ADD {
			for _, operand := range []goast.Expr{bin.X, bin.Y} {
				for {
					paren, ok := operand.(*goast.ParenExpr)
					if !ok {
						break
					}
					operand = paren.X
				}
				if lit, ok := operand.(*goast.BasicLit); ok {
					joined[lit] = true
				}
			}
		}
		return true
	})
	var edits []literalEdit
	goast.Inspect(file, func(n goast.Node) bool {
		lit, ok := n.(*goast.BasicLit)
		if !ok || lit.Kind != gotoken.STRING || joined[lit] {
			return true
		}
		value, err := strconv.Unquote(lit.Value)
		if err != nil || !strings.Contains(value, "fn ") {
			return true
		}
		rewritten, unresolved, unwritable, panicked := writeSourceSafely(value)
		if panicked != nil {
			fmt.Fprintf(os.Stderr, "%s: skipped, the checker panicked: %v\n", fset.Position(lit.Pos()), panicked)
			return true
		}
		s.unwritable += unwritable
		if unresolved != "" {
			s.unresolved++
			if verbose {
				fmt.Fprintf(os.Stderr, "%s: skipped, it would write %q\n", fset.Position(lit.Pos()), unresolved)
			}
			return true
		}
		if rewritten == value {
			return true
		}
		encoded, ok := encodeLike(lit.Value, rewritten)
		if !ok {
			fmt.Fprintf(os.Stderr, "%s: skipped, the edit cannot be written in this literal\n", fset.Position(lit.Pos()))
			return true
		}
		start := fset.Position(lit.Pos()).Offset
		edits = append(edits, literalEdit{start: start, end: start + len(lit.Value), text: encoded})
		s.edited++
		return true
	})
	if len(edits) == 0 || dryRun {
		return s, nil
	}
	sort.Slice(edits, func(i, j int) bool { return edits[i].start > edits[j].start })
	out := string(contents)
	for _, e := range edits {
		out = out[:e.start] + e.text + out[e.end:]
	}
	return s, os.WriteFile(path, []byte(out), 0o644)
}

// writeSourceSafely is writeSource, returning the value the checker panicked with, if any,
// rather than stopping the run.
func writeSourceSafely(src string) (rewritten, unresolved string, unwritable int, panicked any) {
	defer func() {
		if r := recover(); r != nil {
			rewritten, unresolved, unwritable, panicked = src, "", 0, r
		}
	}()
	rewritten, unresolved, unwritable = writeSource(src)
	return rewritten, unresolved, unwritable, nil
}

// writeSource returns src with the inferred signature written for each function and method
// that leaves part of it out, other than one that leaves out a parameter type. unresolved is
// the text of an edit that would write a type the checker could not resolve, in which case
// src is returned as it is, and the empty string otherwise. unwritable counts the functions
// whose signature the edits cannot write. src is checked as a module, or as a script when it
// does not parse as a module.
func writeSource(src string) (rewritten, unresolved string, unwritable int) {
	for _, path := range []string{"lib/index.esc", "bin/main.esc"} {
		source := &ast.Source{ID: 0, Path: path, Contents: src}
		co := compiler.CheckPackage([]*ast.Source{source}, compiler.PackageConfig{})
		if len(co.ParseErrors) > 0 {
			continue
		}
		info := co.InfoFor(0)
		if info == nil {
			return src, "", 0
		}
		var edits []sigedit.Edit
		for _, target := range sigedit.Targets(info, sigedit.FileDecls(co.Scripts[0], co.Module, 0), 0) {
			// A function that leaves out a parameter type is a test of parameter inference,
			// which keeps covering that inference until parameter inference is removed.
			if leavesOutParamType(target.Sig) {
				continue
			}
			targetEdits, ok := target.Edits(src)
			if !ok {
				unwritable++
				continue
			}
			edits = append(edits, targetEdits...)
		}
		if len(edits) == 0 {
			return src, "", unwritable
		}
		// A type the checker could not resolve renders as `error`, and a variable it left
		// unsolved as t{ID}. Either means the literal does not check on its own, as one file
		// of a several-file test does, so its signature is not the one the test means.
		for _, e := range edits {
			if unresolvedType.MatchString(e.Text) {
				return src, e.Text, unwritable
			}
		}
		// A body with an error can leave a type the checker infers as `never`, such as the
		// return type or a promise's value, which says nothing about what the test means, so
		// such a source is left as it is.
		if hasError(co) {
			for _, e := range edits {
				if neverType.MatchString(e.Text) {
					return src, e.Text, unwritable
				}
			}
		}
		return sigedit.Apply(src, edits), "", unwritable
	}
	return src, "", 0
}

// unresolvedType matches a rendered type that holds an unresolved type, a raw variable such
// as t4, or a recursive type, which renders as `μX0.T` and has no source form. A word
// followed by `:` or `?:` is a field name, as in `{t1: number}`, so it is not a variable.
var unresolvedType = regexp.MustCompile(`(?:^|[^\w"-])error(?:[^\w"-]|$)|(?:^|[^\w"-])t[0-9]+(?:[^\w"?:-]|$)|μ`)

// neverType matches a rendered type that names `never`.
var neverType = regexp.MustCompile(`\bnever\b`)

// encodeLike returns text written as a Go string literal of the same kind as original: a
// raw literal when original is one, and an interpreted literal otherwise. An interpreted
// literal keeps original's escapes for every unchanged byte, so only the inserted text is
// re-escaped. ok is false when text cannot be written in a raw literal.
func encodeLike(original, text string) (string, bool) {
	if strings.HasPrefix(original, "`") {
		if strings.Contains(text, "`") {
			return "", false
		}
		return "`" + text + "`", true
	}
	value, err := strconv.Unquote(original)
	if err != nil {
		return "", false
	}
	// pieces[i] is how original spells value's byte i, so the unchanged bytes keep their
	// spelling. A multi-byte rune's spelling goes on its first byte, and each byte that
	// continues the rune gets the empty string.
	pieces := make([]string, len(value))
	body := original[1 : len(original)-1]
	vi := 0
	for len(body) > 0 {
		r, _, tail, err := strconv.UnquoteChar(body, '"')
		if err != nil {
			return "", false
		}
		pieces[vi] = body[:len(body)-len(tail)]
		vi += len(string(r))
		body = tail
	}
	// Map text back onto value: a common prefix and suffix keep their spelling, and the
	// middle that changed is quoted afresh.
	prefix := 0
	for prefix < len(value) && prefix < len(text) && value[prefix] == text[prefix] {
		prefix++
	}
	suffix := 0
	for suffix < len(value)-prefix && suffix < len(text)-prefix &&
		value[len(value)-1-suffix] == text[len(text)-1-suffix] {
		suffix++
	}
	// Keep the prefix and suffix on rune boundaries, so a spelling is never split.
	for prefix > 0 && prefix < len(value) && pieces[prefix] == "" {
		prefix--
	}
	for suffix > 0 && pieces[len(value)-suffix] == "" {
		suffix--
	}
	var b strings.Builder
	b.WriteByte('"')
	for i := 0; i < prefix; i++ {
		b.WriteString(pieces[i])
	}
	middle := strconv.Quote(text[prefix : len(text)-suffix])
	b.WriteString(middle[1 : len(middle)-1])
	for i := len(value) - suffix; i < len(value); i++ {
		b.WriteString(pieces[i])
	}
	b.WriteByte('"')
	return b.String(), true
}

// leavesOutParamType reports whether sig writes a parameter with no type.
func leavesOutParamType(sig *ast.FuncSig) bool {
	for _, p := range sig.Params {
		if p.TypeAnn == nil {
			return true
		}
	}
	return false
}

// hasError reports whether co holds a diagnostic other than a warning.
func hasError(co compiler.CheckOutput) bool {
	for _, d := range co.TypeErrors {
		if !compiler.IsWarning(d) {
			return true
		}
	}
	return false
}
