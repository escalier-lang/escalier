package main

import (
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/checker"
	"github.com/escalier-lang/escalier/internal/compiler"
)

// loadSources reads and validates source files, returning a slice of sources and a map for quick lookup
func loadSources(stdout io.Writer, files []string) ([]*ast.Source, map[int]*ast.Source) {
	sources := make([]*ast.Source, 0, len(files))
	idToSource := make(map[int]*ast.Source)
	nextID := 0

	for _, file := range files {
		source, err := loadSource(file, nextID)
		if err != nil {
			fmt.Fprintln(stdout, err.Error())
			continue
		}

		sources = append(sources, source)
		idToSource[source.ID] = source
		nextID++
	}

	return sources, idToSource
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
		fmt.Fprintln(stderr, err)
	}

	// TODO: sort by err.Location()
	for _, err := range output.TypeErrors {
		fmt.Printf("Type Error: %#v\n", err)
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
	// TODO: cache this to avoid splitting the contents every time
	lines := strings.Split(source.Contents, "\n")

	if err.Span().Start.String() == "0:0" {
		return fmt.Sprintf("%s:%s: %s\n", source.Path, err.Span().Start, err.Message())
	}

	var message strings.Builder
	message.WriteString(fmt.Sprintf("%s:%s: %s\n", source.Path, err.Span().Start, err.Message()))
	message.WriteString("\n")

	lineNum := strconv.Itoa(err.Span().Start.Line) + ":"
	message.WriteString(fmt.Sprintf("%-4s", lineNum))
	message.WriteString(lines[err.Span().Start.Line-1] + "\n")

	// Add spaces before the caret
	for range 4 + err.Span().Start.Column - 1 {
		message.WriteString(" ")
	}

	// Add carets to highlight the error
	for range err.Span().End.Column - err.Span().Start.Column {
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
func writeModuleOutputs(stderr io.Writer, moduleName string, output compiler.ModuleOutput) error {
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

func build(stdout io.Writer, stderr io.Writer, files []string) {
	fmt.Fprintln(stdout, "building module...")

	sources, idToSource := loadSources(stdout, files)

	output := compiler.CompilePackage(sources)

	printErrors(stderr, output, idToSource)

	for moduleName, moduleOutput := range output.Modules {
		if err := writeModuleOutputs(stderr, moduleName, moduleOutput); err != nil {
			fmt.Fprintln(stderr, err.Error())
			return
		}
	}
}
