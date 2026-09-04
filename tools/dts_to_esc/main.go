// Command dts_to_esc converts TypeScript `.d.ts` files into Escalier
// `.esc` source. `generate` is the TS-version-bump workflow of §6.6.
// See tools/dts_to_esc/README.md for a walkthrough of a bump.
//
//	dts_to_esc <path-to-d.ts>
//	    Single-file MVP path: convert one .d.ts to a standalone .esc
//	    written to stdout. Per planning/builtins/implementation_plan.md
//	    §5: AST-to-AST translation only (no checker), with trio
//	    recognition, namespace flattening, and `@js(...)` decorator
//	    emission.
//
//	dts_to_esc generate [--cfg <cfg.json>] [--overlay <dir>] [--update-digests] <lib-dir> <esc-dir>
//	    Writes the whole `.esc` tree under <esc-dir> from the three
//	    inputs of §6.4: the pinned lib set under <lib-dir>, the
//	    ECMA-262 derived facts the converter applies, and the overlay.
//	    The run reads no file it wrote, so seeding an empty tree and
//	    re-running against a populated one are the same operation and
//	    `git diff` is the review surface for a version bump. Generated
//	    packages the run no longer emits are deleted. <esc-dir>/node/ is
//	    scaffolded with a README explaining its reserved status per
//	    §6.1/§6.3; no `.esc` files are emitted there. The unmapped-symbol
//	    fail-safe aborts the run with the offending name and source file.
//	    The overlay defaults to <esc-dir>/../overlay. --update-digests
//	    rewrites the digest sidecar beside each overlay `replace` file,
//	    recording the converted form it stands in for, instead of
//	    checking what those sidecars already record.
//
//	    With --cfg, the run also joins every std:* member it emits
//	    against the ECMA-262 effect facts derived from that control-flow
//	    graph, and reports the names present on one side only. It reports
//	    what the curated layer did to those facts alongside it, and diffs
//	    every receiver claim against the hand-written mutability sources.
//	    See planning/ecma-262/implementation_plan.md.
package main

import (
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/dts_parser"
	"github.com/escalier-lang/escalier/internal/dts_to_esc"
	"github.com/escalier-lang/escalier/internal/ecma262"
)

func main() {
	if err := run(os.Args[1:], os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, "dts_to_esc:", err)
		os.Exit(1)
	}
}

const usage = `usage:
  dts_to_esc <path-to-d.ts>
  dts_to_esc generate [--cfg <cfg.json>] [--overlay <dir>] [--update-digests] <lib-dir> <esc-dir>`

func run(args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		return fmt.Errorf("%s", usage)
	}
	if args[0] == "generate" {
		return runGenerate(args[1:], stderr)
	}
	return runSingleFile(args, stdout)
}

// runSingleFile converts the one `.d.ts` named on the command line. Any
// first argument other than `generate` lands here, so a word typed as a
// subcommand arrives as a filename. Both failures below therefore end
// with the full usage, which is what names the subcommand the tool has.
func runSingleFile(args []string, out io.Writer) error {
	if len(args) != 1 {
		return fmt.Errorf("%s", usage)
	}
	path := args[0]
	contents, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("reading %s: %w\n%s", path, err, usage)
	}
	source := &ast.Source{Path: path, Contents: string(contents)}
	parser := dts_parser.NewDtsParser(source)
	dtsModule, errs := parser.ParseModule()
	if len(errs) > 0 {
		return fmt.Errorf("parse errors in %s: %v", path, errs)
	}
	standalone, err := dts_to_esc.ConvertToStandaloneModule(dtsModule)
	if err != nil {
		return fmt.Errorf("converting %s: %w", path, err)
	}
	return dts_to_esc.WriteStandaloneModule(standalone, out)
}

// generateUsage is the one-line synopsis every generate-mode argument
// error ends with.
const generateUsage = "usage: dts_to_esc generate [--cfg <cfg.json>] [--overlay <dir>] [--update-digests] <lib-dir> <esc-dir>"

// defaultOverlayDirName is the overlay tree's directory name. It sits
// beside the generated tree, so `internal/interop/data` as <esc-dir>
// resolves the overlay to `internal/interop/overlay`.
const defaultOverlayDirName = "overlay"

func runGenerate(args []string, stderr io.Writer) error {
	// The flag package reports its own errors, which main would then print a
	// second time. Discarding its output leaves one report per error.
	flags := flag.NewFlagSet("generate", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	cfgPath := flags.String("cfg", "", "path to the ECMA-262 cfg.json; adds the curation, coercion-filter, receiver-validation, and effect-fact join reports")
	overlayDir := flags.String("overlay", "", "`path` to the overlay tree; defaults to the overlay directory beside <esc-dir>")
	updateDigests := flags.Bool("update-digests", false, "record what every overlay replace stands in for, rather than checking the recorded digests")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			fmt.Fprintln(stderr, generateUsage)
			flags.SetOutput(stderr)
			flags.PrintDefaults()
			return nil
		}
		return fmt.Errorf("%w\n%s", err, generateUsage)
	}
	if flags.NArg() != 2 {
		return errors.New(generateUsage)
	}
	libDir, escDir := flags.Arg(0), flags.Arg(1)
	if *overlayDir == "" {
		// filepath.Dir("internal/interop/data/") is the argument itself,
		// so a trailing separator would look inside the generated tree
		// rather than beside it. Cleaning the path first drops the
		// separator.
		*overlayDir = filepath.Join(filepath.Dir(filepath.Clean(escDir)), defaultOverlayDirName)
	}

	facts, join, err := loadFacts(*cfgPath, stderr)
	if err != nil {
		return err
	}

	res, err := dts_to_esc.Generate(dts_to_esc.GenerateOptions{
		LibDir:        libDir,
		OverlayDir:    *overlayDir,
		OutDir:        escDir,
		HandAuthored:  dts_to_esc.HandAuthoredPackages,
		RecordDigests: *updateDigests,
	})
	if err != nil {
		return err
	}

	fmt.Fprintf(stderr, "discovered %d lib files\n", len(res.LibFiles))
	fmt.Fprintf(stderr, "wrote %d packages under %s\n", len(res.Written), escDir)
	for _, rel := range res.Removed {
		fmt.Fprintf(stderr, "removed %s: no input accounts for it\n", rel)
	}
	if err := dts_to_esc.ReportPartition(res.Partition, stderr); err != nil {
		return err
	}
	if err := dts_to_esc.ReportTypeOnlyRouting(res.Partition, stderr); err != nil {
		return err
	}
	if err := dts_to_esc.ReportSingletonKeyDrops(res.Modules, stderr); err != nil {
		return err
	}
	return writeFactReports(facts, join, res.Modules, stderr)
}

// loadFacts derives the ECMA-262 facts a run reports against, or returns
// nils when no cfg.json was named.
//
// The facts are derived before any output is written, so neither a bad
// --cfg path nor a fact with a hole in it leaves a half-joined tree on
// disk. The tree does not read the facts, but a run that ends in an
// error should not leave output behind that looks like it succeeded.
func loadFacts(cfgPath string, stderr io.Writer) (*ecma262.Facts, *ecma262.Join, error) {
	if cfgPath == "" {
		return nil, nil, nil
	}
	cfg, err := ecma262.LoadCFG(cfgPath)
	if err != nil {
		return nil, nil, fmt.Errorf("loading %s: %w", cfgPath, err)
	}
	facts, err := ecma262.NewFacts(cfg)
	if err != nil {
		return nil, nil, err
	}
	if holes := facts.Incomplete(); len(holes) > 0 {
		// The curation report goes first, because a hole is often the
		// downstream of a refused entry and that line names the cause. The
		// error below names only the axis left unanswered.
		if err := ecma262.WriteCurationReport(facts.Curation(), stderr); err != nil {
			return nil, nil, err
		}
		// A hole would leave the converter to guess a determination nobody
		// answered, and §7 auto-applies the receiver, so the guess would be
		// silent. Refuse the run instead.
		return nil, nil, fmt.Errorf("%s leaves determinations unanswered:\n  %s",
			cfgPath, strings.Join(holes, "\n  "))
	}
	return facts, ecma262.NewJoin(facts), nil
}

// writeFactReports prints the four ECMA-262 reports for a run that named
// a cfg.json, and prints nothing for one that did not.
//
// All four are informational. A curated entry the analysis caught up
// with is an entry to delete. The spec and the TypeScript lib drift
// independently, so a name on one side only is a gap to close. FR11's
// coercion filter is a heuristic, so what it dropped is read rather than
// trusted. A receiver the two sources answer differently is a triage
// item. None of them is a failed run.
func writeFactReports(
	facts *ecma262.Facts,
	join *ecma262.Join,
	mods map[string]*dts_to_esc.StandaloneModule,
	stderr io.Writer,
) error {
	if join == nil {
		return nil
	}
	if err := ecma262.WriteCurationReport(facts.Curation(), stderr); err != nil {
		return err
	}
	if err := ecma262.WriteFilterReport(facts.Filter(), stderr); err != nil {
		return err
	}
	if err := dts_to_esc.WriteValidationReport(dts_to_esc.ValidateReceivers(facts), stderr); err != nil {
		return err
	}
	return ecma262.WriteJoinReport(join.Match(dts_to_esc.StdDeclarations(mods)), stderr)
}
