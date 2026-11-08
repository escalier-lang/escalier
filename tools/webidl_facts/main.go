// Command webidl_facts writes internal/webidl/table.go from the installed
// `@webref/idl` package. Run it from the repository root after `pnpm install`
// whenever package.json pins a new `@webref/idl` version:
//
//	go run ./tools/webidl_facts
//
// The flags name a different input or output for trying a version that is not
// pinned.
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/escalier-lang/escalier/internal/webidl"
)

func main() {
	idlDir := flag.String("idl", filepath.Join("node_modules", "@webref", "idl"), "directory of the installed @webref/idl package")
	out := flag.String("out", filepath.Join("internal", "webidl", "table.go"), "path of the Go table to write")
	flag.Parse()

	src, err := webidl.Generate(*idlDir)
	if err != nil {
		fmt.Fprintf(os.Stderr, "webidl_facts: %v\n", err)
		os.Exit(1)
	}
	if err := os.WriteFile(*out, src, 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "webidl_facts: %v\n", err)
		os.Exit(1)
	}
}
