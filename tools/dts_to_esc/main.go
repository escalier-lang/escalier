// Command dts_to_esc converts a TypeScript `.d.ts` file to an Escalier
// `.esc` source written to stdout. MVP per planning/builtins/implementation_plan.md
// §5: AST-to-AST translation only (no checker), with trio recognition,
// namespace flattening, and `@js(...)` decorator emission.
//
// Usage:
//
//	dts_to_esc <path-to-d.ts>
package main

import (
	"fmt"
	"os"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/dts_parser"
	"github.com/escalier-lang/escalier/internal/interop"
)

func main() {
	if err := run(os.Args[1:], os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "dts_to_esc:", err)
		os.Exit(1)
	}
}

func run(args []string, out *os.File) error {
	if len(args) != 1 {
		return fmt.Errorf("usage: dts_to_esc <path-to-d.ts>")
	}
	path := args[0]
	contents, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("reading %s: %w", path, err)
	}

	source := &ast.Source{
		Path:     path,
		Contents: string(contents),
		ID:       0,
	}
	parser := dts_parser.NewDtsParser(source)
	dtsModule, errors := parser.ParseModule()
	if len(errors) > 0 {
		return fmt.Errorf("parse errors in %s: %v", path, errors)
	}

	standalone, err := interop.ConvertToStandaloneModule(dtsModule)
	if err != nil {
		return fmt.Errorf("converting %s: %w", path, err)
	}

	return interop.WriteStandaloneModule(standalone, out)
}
