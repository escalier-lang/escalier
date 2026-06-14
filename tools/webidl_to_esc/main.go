// Command webidl_to_esc is stage 2 of the WebIDL -> Escalier pipeline. It
// reads the JSON IR artifacts produced by extract.mjs (stage 1) and renders
// Escalier `.esc` declarations.
//
// Usage:
//
//	webidl_to_esc <artifact.json> [more.json ...]
//	    Convert each JSON artifact and write `<spec>.esc` next to it
//	    (or to stdout when a single artifact is given and -stdout is set).
//
//	webidl_to_esc -o <out-dir> <artifact.json> [more.json ...]
//	    Write each `<spec>.esc` under <out-dir>.
//
// The actual conversion lives in internal/webidl so it is unit-testable
// without the CLI.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/escalier-lang/escalier/internal/webidl"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, "webidl_to_esc:", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	fs := flag.NewFlagSet("webidl_to_esc", flag.ContinueOnError)
	outDir := fs.String("o", "", "output directory for .esc files (default: alongside each artifact)")
	toStdout := fs.Bool("stdout", false, "write to stdout instead of files")
	if err := fs.Parse(args); err != nil {
		return err
	}
	paths := fs.Args()
	if len(paths) == 0 {
		return fmt.Errorf("usage: webidl_to_esc [-o dir] [-stdout] <artifact.json> [more.json ...]")
	}

	for _, path := range paths {
		raw, err := os.ReadFile(path)
		if err != nil {
			return fmt.Errorf("reading %s: %w", path, err)
		}
		var artifact webidl.Artifact
		if err := json.Unmarshal(raw, &artifact); err != nil {
			return fmt.Errorf("parsing %s: %w", path, err)
		}
		esc := webidl.ConvertArtifact(artifact)

		if *toStdout {
			fmt.Print(esc)
			continue
		}

		dir := *outDir
		if dir == "" {
			dir = filepath.Dir(path)
		}
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("creating %s: %w", dir, err)
		}
		dest := filepath.Join(dir, artifact.Spec+".esc")
		if err := os.WriteFile(dest, []byte(esc), 0o644); err != nil {
			return fmt.Errorf("writing %s: %w", dest, err)
		}
		fmt.Fprintf(os.Stderr, "wrote %s\n", dest)
	}
	return nil
}
