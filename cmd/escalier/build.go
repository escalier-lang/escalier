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
	"github.com/escalier-lang/escalier/internal/compiler"
)

func build(stdout io.Writer, stderr io.Writer, files []string) {
	fmt.Fprintln(stdout, "building module...")

	sources := make([]*ast.Source, 0, len(files))
	idToSource := make(map[int]*ast.Source)

	for _, file := range files {
		// check that file has .esc extension
		if path.Ext(file) != ".esc" {
			fmt.Fprintln(stdout, "file does not have .esc extension")
			continue
		}
		// check if file exists
		if _, err := os.Stat(file); os.IsNotExist(err) {
			fmt.Fprintln(stdout, "file does not exist")
			continue
		}

		// open the file
		f, err := os.Open(file)
		if err != nil {
			fmt.Fprintln(stdout, "failed to open file")
			continue
		}
		defer f.Close()

		// read file content
		bytes, err := os.ReadFile(file)
		if err != nil {
			fmt.Fprintln(stdout, "failed to read file content")
			continue
		}

		source := &ast.Source{
			ID:       len(sources),
			Path:     file,
			Contents: string(bytes),
		}
		sources = append(sources, source)
		idToSource[source.ID] = source
	}

	output := compiler.CompilePackage(sources)

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

		// TODO: cache this to avoid splitting the contents every time
		lines := strings.Split(source.Contents, "\n")

		if err.Span().Start.String() == "0:0" {
			message := fmt.Sprintf("%s:%s: %s\n", source.Path, err.Span().Start, err.Message())
			fmt.Fprintln(stderr, message)
			continue
		}

		message := fmt.Sprintf("%s:%s: %s\n", source.Path, err.Span().Start, err.Message())
		message += "\n"
		lineNum := strconv.Itoa(err.Span().Start.Line) + ":"
		message += fmt.Sprintf("%-4s", lineNum)
		message += lines[err.Span().Start.Line-1] + "\n"
		for range 4 + err.Span().Start.Column - 1 {
			message += " "
		}
		for range err.Span().End.Column - err.Span().Start.Column {
			message += "^"
		}
		message += "\n"

		fmt.Fprintln(stderr, message)
	}

	for moduleName, output := range output.Modules {
		dir := filepath.Join("build", filepath.Dir(moduleName))
		err := os.MkdirAll(dir, 0755)
		if err != nil {
			fmt.Fprintln(stderr, "failed to create directory for module")
			return
		}

		// create .js file
		jsFile := filepath.Join("build", moduleName+".js")
		jsOut, err := os.Create(jsFile)
		if err != nil {
			fmt.Fprintln(stderr, "failed to create .js file")
			return
		}

		// write .js output to file
		_, err = jsOut.WriteString(output.JS)
		if err != nil {
			fmt.Fprintln(stderr, "failed to write .js to file")
			return
		}

		// create .d.ts file
		defFile := filepath.Join("build", moduleName+".d.ts")
		defOut, err := os.Create(defFile)
		if err != nil {
			fmt.Fprintln(stderr, "failed to create .d.ts file")
			return
		}

		// write .d.ts output to file
		_, err = defOut.WriteString(output.DTS)
		if err != nil {
			fmt.Fprintln(stderr, "failed to write .d.ts to file")
			return
		}

		// create sourcemap file
		mapFile := filepath.Join("build", moduleName+".js.map")
		mapOut, err := os.Create(mapFile)
		if err != nil {
			fmt.Fprintln(stderr, "failed to create map file")
			return
		}

		// write sourcemap output to file
		_, err = mapOut.WriteString(output.SourceMap)
		if err != nil {
			fmt.Fprintln(stderr, "failed to write source map to file")
			return
		}
	}
}
