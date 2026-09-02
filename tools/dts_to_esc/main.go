// Command dts_to_esc converts TypeScript `.d.ts` files into Escalier
// `.esc` source. The three subcommands that operate on the pinned lib
// set are the TS-version-bump workflow of §6.6. See
// tools/dts_to_esc/README.md for a walkthrough of a bump.
//
//	dts_to_esc <path-to-d.ts>
//	    Single-file MVP path: convert one .d.ts to a standalone .esc
//	    written to stdout. Per planning/builtins/implementation_plan.md
//	    §5: AST-to-AST translation only (no checker), with trio
//	    recognition, namespace flattening, and `@js(...)` decorator
//	    emission.
//
//	dts_to_esc generate [--cfg <cfg.json>] [--overlay <dir>] <lib-dir> <esc-dir>
//	    Writes the whole `.esc` tree under <esc-dir> from the three
//	    inputs of §6.4: the pinned lib set under <lib-dir>, the
//	    ECMA-262 derived facts the converter applies, and the overlay.
//	    The run reads no file it wrote, so seeding an empty tree and
//	    re-running against a populated one are the same operation and
//	    `git diff` is the review surface for a version bump. Generated
//	    packages the run no longer emits are deleted. The overlay
//	    defaults to <esc-dir>/../overlay.
//
//	dts_to_esc bootstrap [--cfg <cfg.json>] <lib-dir> <out-dir>
//	    One-time seeding of a fresh `.esc` tree per §6.6: discover every
//	    lib.*.d.ts under <lib-dir>, parse each, route every top-level
//	    declaration through dts_to_esc.Route, and write the partitioned
//	    tree (std/*.esc, web/*.esc) under <out-dir>. Every package file
//	    is written whole, so a committed tree under <out-dir> is
//	    overwritten and its hand-edits lost. Use regenerate to fold
//	    upstream changes into a tree that already exists. <out-dir>/node/
//	    is scaffolded with a README explaining its reserved status per
//	    §6.1/§6.3; no `.esc` files are emitted there. The unmapped-
//	    symbol fail-safe aborts the run with the offending name +
//	    source file.
//
//	    With --cfg, the run also joins every std:* member it emits
//	    against the ECMA-262 effect facts derived from that control-flow
//	    graph, and reports the names present on one side only. It reports
//	    what the curated layer did to those facts alongside it, and diffs
//	    every receiver claim against the hand-written mutability sources.
//	    See planning/ecma-262/implementation_plan.md.
//
//	dts_to_esc check <lib-dir> <esc-dir>
//	    Read-only verification per §6.4: convert the pinned lib set and
//	    print the unified diff a regenerate run would apply to the
//	    committed `.esc` tree under <esc-dir>. Exits non-zero
//	    when anything is missing. Signature and property-type drift are
//	    not checked yet — see internal/dts_to_esc/rerun.go.
//
//	dts_to_esc regenerate <lib-dir> <esc-dir>
//	    Additive write per §6.4: add the declarations and members
//	    `check` reports to the committed tree, leaving every existing
//	    declaration byte-for-byte intact so hand-edits survive.
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
  dts_to_esc generate [--cfg <cfg.json>] [--overlay <dir>] <lib-dir> <esc-dir>
  dts_to_esc bootstrap [--cfg <cfg.json>] <lib-dir> <out-dir>
  dts_to_esc check <lib-dir> <esc-dir>
  dts_to_esc regenerate <lib-dir> <esc-dir>`

func run(args []string, stdout, stderr io.Writer) error {
	if len(args) == 0 {
		return fmt.Errorf("%s", usage)
	}
	switch args[0] {
	case "generate":
		return runGenerate(args[1:], stderr)
	case "bootstrap":
		return runBootstrap(args[1:], stderr)
	case "check":
		return runCheck(args[1:], stdout, stderr)
	case "regenerate":
		return runRegenerate(args[1:], stdout, stderr)
	}
	return runSingleFile(args, stdout)
}

// errCheckFailed is returned when `check` finds a missing declaration
// or member. main turns any error into a non-zero exit, which is what
// CI keys off; the message stays short because the report itself has
// already been printed.
var errCheckFailed = errors.New("committed .esc tree is out of date with the .d.ts inputs")

func runCheck(args []string, stdout, stderr io.Writer) error {
	if len(args) != 2 {
		return fmt.Errorf("usage: dts_to_esc check <lib-dir> <esc-dir>")
	}
	result, err := partitionLibDir(args[0], stderr)
	if err != nil {
		return err
	}
	report, err := dts_to_esc.CheckPartition(result, args[1])
	if err != nil {
		return err
	}
	if err := report.Write(stdout); err != nil {
		return err
	}
	if report.Failed() {
		return errCheckFailed
	}
	return nil
}

func runRegenerate(args []string, stdout, stderr io.Writer) error {
	if len(args) != 2 {
		return fmt.Errorf("usage: dts_to_esc regenerate <lib-dir> <esc-dir>")
	}
	result, err := partitionLibDir(args[0], stderr)
	if err != nil {
		return err
	}
	report, err := dts_to_esc.RegeneratePartition(result, args[1])
	if err != nil {
		return err
	}
	return report.Write(stdout)
}

// partitionLibDir discovers, parses, and routes every lib.*.d.ts under
// libDir. Shared by the bootstrap, check, and regenerate subcommands.
func partitionLibDir(libDir string, stderr io.Writer) (*dts_to_esc.PartitionResult, error) {
	basenames, err := dts_to_esc.DiscoverLibFiles(libDir)
	if err != nil {
		return nil, err
	}
	if len(basenames) == 0 {
		return nil, fmt.Errorf("no lib.*.d.ts files found under %s", libDir)
	}
	fmt.Fprintf(stderr, "discovered %d lib files\n", len(basenames))

	inputs, err := dts_to_esc.ParseLibFiles(libDir, basenames)
	if err != nil {
		return nil, err
	}
	return dts_to_esc.PartitionLib(inputs)
}

func runSingleFile(args []string, out io.Writer) error {
	if len(args) != 1 {
		return fmt.Errorf("usage: dts_to_esc <path-to-d.ts>")
	}
	path := args[0]
	contents, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("reading %s: %w", path, err)
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
const generateUsage = "usage: dts_to_esc generate [--cfg <cfg.json>] [--overlay <dir>] <lib-dir> <esc-dir>"

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
	overlayDir := flags.String("overlay", "", "path to the overlay tree; defaults to the `overlay` directory beside <esc-dir>")
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
		*overlayDir = filepath.Join(filepath.Dir(escDir), defaultOverlayDirName)
	}

	facts, join, err := loadFacts(*cfgPath, stderr)
	if err != nil {
		return err
	}

	res, err := dts_to_esc.Generate(dts_to_esc.GenerateOptions{
		LibDir:       libDir,
		OverlayDir:   *overlayDir,
		OutDir:       escDir,
		HandAuthored: dts_to_esc.HandAuthoredPackages,
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
	if err := dts_to_esc.ReportSingletonKeyDrops(res.Modules, stderr); err != nil {
		return err
	}
	return writeFactReports(facts, join, res.Modules, stderr)
}

// bootstrapUsage is the one-line synopsis every bootstrap-mode argument error
// ends with.
const bootstrapUsage = "usage: dts_to_esc bootstrap [--cfg <cfg.json>] <lib-dir> <out-dir>"

func runBootstrap(args []string, stderr io.Writer) error {
	// The flag package reports its own errors, which main would then print a
	// second time. Discarding its output leaves one report per error.
	flags := flag.NewFlagSet("bootstrap", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	cfgPath := flags.String("cfg", "", "path to the ECMA-262 cfg.json; adds the curation, coercion-filter, receiver-validation, and effect-fact join reports")
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			fmt.Fprintln(stderr, bootstrapUsage)
			flags.SetOutput(stderr)
			flags.PrintDefaults()
			return nil
		}
		return fmt.Errorf("%w\n%s", err, bootstrapUsage)
	}
	if flags.NArg() != 2 {
		return errors.New(bootstrapUsage)
	}
	libDir, outDir := flags.Arg(0), flags.Arg(1)

	facts, join, err := loadFacts(*cfgPath, stderr)
	if err != nil {
		return err
	}

	result, err := partitionLibDir(libDir, stderr)
	if err != nil {
		return err
	}

	mods, err := dts_to_esc.ConvertBuckets(result)
	if err != nil {
		return err
	}

	written, err := dts_to_esc.WriteConvertedTree(mods, outDir)
	if err != nil {
		return err
	}
	if err := dts_to_esc.ScaffoldNodeDir(outDir); err != nil {
		return err
	}

	fmt.Fprintf(stderr, "wrote %d packages under %s\n", len(written), outDir)
	if err := dts_to_esc.ReportPartition(result, stderr); err != nil {
		return err
	}
	if err := dts_to_esc.ReportSingletonKeyDrops(mods, stderr); err != nil {
		return err
	}
	return writeFactReports(facts, join, mods, stderr)
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
