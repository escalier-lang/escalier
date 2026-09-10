package main

import (
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/checker"
	"github.com/escalier-lang/escalier/internal/compiler"
)

// loadSources reads and validates each file in files. It returns the sources it
// loaded, a lookup from source ID to source, and the number of files it could not
// read. A file that fails to load takes its declarations out of the package, so a
// non-zero count means the compile ran against an incomplete tree.
func loadSources(stderr io.Writer, files []string) ([]*ast.Source, map[int]*ast.Source, int) {
	sources := make([]*ast.Source, 0, len(files))
	idToSource := make(map[int]*ast.Source)
	nextID := 0
	failed := 0

	for _, file := range files {
		source, err := loadSource(file, nextID)
		if err != nil {
			fmt.Fprintf(stderr, "%s: %s\n", file, err.Error())
			failed++
			continue
		}

		sources = append(sources, source)
		idToSource[source.ID] = source
		nextID++
	}

	return sources, idToSource, failed
}

// loadSource reads a single source file and creates an ast.Source
func loadSource(file string, id int) (*ast.Source, error) {
	// check that file has .esc extension
	if path.Ext(file) != ".esc" {
		return nil, fmt.Errorf("file does not have .esc extension")
	}

	// check if file exists
	if _, err := os.Stat(file); os.IsNotExist(err) {
		return nil, fmt.Errorf("file does not exist")
	}

	// read file content
	bytes, err := os.ReadFile(file)
	if err != nil {
		return nil, fmt.Errorf("failed to read file content")
	}

	return &ast.Source{
		ID:       id,
		Path:     file,
		Contents: string(bytes),
	}, nil
}

// printErrors outputs parse and type errors to stderr with formatted context
func printErrors(stderr io.Writer, output compiler.CompilerOutput, idToSource map[int]*ast.Source) {
	for _, err := range output.ParseErrors {
		// A span holds byte offsets, so naming a line needs the file it came
		// from. SpanString falls back to the offsets when the file is unknown.
		fmt.Fprintf(stderr, "%s: %s\n",
			ast.SpanString(idToSource[err.Span.SourceID], err.Span), err.Message)
	}

	// TODO: sort by err.Location()
	for _, err := range output.TypeErrors {
		source, ok := idToSource[err.Span().SourceID]
		if !ok {
			fmt.Fprintln(stderr, "source not found for error")
			continue
		}

		message := formatTypeError(err, source)
		fmt.Fprintln(stderr, message)
	}
}

// formatTypeError formats a type error with source context and location highlighting
func formatTypeError(err checker.Error, source *ast.Source) string {
	span := err.Span()
	lineMap := source.LineMap()

	// A span the checker synthesized covers no source text, so there is no
	// line to quote and no column to point at.
	if span.End.Offset <= span.Start.Offset {
		return fmt.Sprintf("%s: %s\n", source.Path, err.Message())
	}

	// Columns count code points so the caret lands under the character the
	// span starts at, whatever bytes it takes to encode.
	startLine, startColumn := lineMap.Position(span.Start.Offset, ast.CodePointColumns)
	endLine, endColumn := lineMap.Position(span.End.Offset, ast.CodePointColumns)

	var message strings.Builder
	fmt.Fprintf(&message, "%s:%d:%d: %s\n\n", source.Path, startLine, startColumn, err.Message())

	lineText := lineMap.LineText(startLine)
	message.WriteString(fmt.Sprintf("%-4s", strconv.Itoa(startLine)+":"))
	message.WriteString(lineText + "\n")

	// Indent past the four-column line-number gutter, then to the column the
	// error starts at.
	for range 4 + startColumn - 1 {
		message.WriteString(" ")
	}

	// Only the first line of the span is quoted, so a span running onto a
	// later line is underlined to the end of that first line.
	caretEnd := endColumn
	if endLine != startLine {
		caretEnd = 1 + utf8.RuneCountInString(lineText)
	}
	for range max(caretEnd-startColumn, 1) {
		message.WriteString("^")
	}
	message.WriteString("\n")

	return message.String()
}

// writeOutputFile writes content to a file in the build directory with the given extension
func writeOutputFile(stderr io.Writer, moduleName, extension, content string) error {
	filePath := filepath.Join("build", moduleName+extension)
	file, err := os.Create(filePath)
	if err != nil {
		return fmt.Errorf("failed to create %s file", extension)
	}
	defer file.Close()

	_, err = file.WriteString(content)
	if err != nil {
		return fmt.Errorf("failed to write %s to file", extension)
	}

	return nil
}

// writeModuleOutputs writes all module outputs (JS, DTS, sourcemap) to the build directory
func writeModuleOutputs(stderr io.Writer, moduleName string, output compiler.CompUnitOutput) error {
	// Create directory structure
	dir := filepath.Join("build", filepath.Dir(moduleName))
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("failed to create directory for module")
	}

	// Write .js file
	if err := writeOutputFile(stderr, moduleName, ".js", output.JS); err != nil {
		return err
	}

	// Write .d.ts file
	if err := writeOutputFile(stderr, moduleName, ".d.ts", output.DTS); err != nil {
		return err
	}

	// Write sourcemap file
	if err := writeOutputFile(stderr, moduleName, ".js.map", output.SourceMap); err != nil {
		return err
	}

	return nil
}

// build compiles each package in pkgs, writes its output, and reports whether every
// package succeeded. A package writes nothing unless every source file loaded and
// the package parsed and checked clean. The JS its declarations lower to is only
// meaningful when those declarations check. The `.d.ts` beside it is worse, since it
// would assert the types the checker just rejected. Output from an earlier successful
// build stays on disk, so an importer keeps resolving against the last artifact that
// did check.
func build(stdout io.Writer, stderr io.Writer, pkgs []string) bool {
	cwd, err := os.Getwd()
	if err != nil {
		fmt.Fprintln(stderr, "failed to get current working directory:", err)
		return false
	}

	allOK := true
	for _, pkg := range pkgs {
		start := time.Now()
		fmt.Fprint(stdout, "building: ", pkg)

		err := os.Chdir(pkg)
		if err != nil {
			fmt.Fprintf(stderr, "failed to change directory to %s: %v\n", pkg, err)
			allOK = false
			continue
		}

		files, err := compiler.FindSourceFiles()
		if err != nil {
			fmt.Fprintf(stderr, "failed to find source files for %s: %v\n", pkg, err)
			_ = os.Chdir(cwd)
			allOK = false
			continue
		}

		sources, idToSource, unreadable := loadSources(stderr, files)
		output := compiler.CompilePackage(sources)

		printErrors(stderr, output, idToSource)

		if n := unreadable + len(output.ParseErrors) + len(output.TypeErrors); n > 0 {
			fmt.Fprintf(stdout, " - failed (%s)\n", errorCount(n))
			allOK = false
			_ = os.Chdir(cwd)
			continue
		}

		for moduleName, moduleOutput := range output.CompUnits {
			if err := writeModuleOutputs(stderr, moduleName, moduleOutput); err != nil {
				fmt.Fprintln(stderr, err.Error())
				_ = os.Chdir(cwd)
				return false
			}
		}

		duration := time.Since(start)
		fmt.Fprintf(stdout, " - ok (%s)\n", duration)

		_ = os.Chdir(cwd)
	}
	return allOK
}

// errorCount renders n as the count phrase the failure line ends with, so a
// single error reads "1 error" rather than "1 errors".
func errorCount(n int) string {
	if n == 1 {
		return "1 error"
	}
	return fmt.Sprintf("%d errors", n)
}
