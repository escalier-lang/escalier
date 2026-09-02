package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/escalier-lang/escalier/internal/ast"
	"github.com/escalier-lang/escalier/internal/checker"
	"github.com/escalier-lang/escalier/internal/compiler"
	"github.com/escalier-lang/escalier/internal/parser"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoadSource(t *testing.T) {
	t.Run("valid .esc file", func(t *testing.T) {
		// Create a temporary .esc file
		tmpDir := t.TempDir()
		testFile := filepath.Join(tmpDir, "test.esc")
		content := "let x = 5;"
		err := os.WriteFile(testFile, []byte(content), 0644)
		require.NoError(t, err)

		source, err := loadSource(testFile, 0)

		assert.NoError(t, err)
		assert.Equal(t, 0, source.ID)
		assert.Equal(t, testFile, source.Path)
		assert.Equal(t, content, source.Contents)
	})

	t.Run("file without .esc extension", func(t *testing.T) {
		tmpDir := t.TempDir()
		testFile := filepath.Join(tmpDir, "test.txt")
		err := os.WriteFile(testFile, []byte("content"), 0644)
		require.NoError(t, err)

		source, err := loadSource(testFile, 0)

		assert.Nil(t, source)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "does not have .esc extension")
	})

	t.Run("non-existent file", func(t *testing.T) {
		source, err := loadSource("/nonexistent/file.esc", 0)

		assert.Nil(t, source)
		assert.Error(t, err)
		assert.Contains(t, err.Error(), "file does not exist")
	})
}

func TestLoadSources(t *testing.T) {
	t.Run("multiple valid files", func(t *testing.T) {
		tmpDir := t.TempDir()
		file1 := filepath.Join(tmpDir, "file1.esc")
		file2 := filepath.Join(tmpDir, "file2.esc")

		err := os.WriteFile(file1, []byte("let x = 1;"), 0644)
		require.NoError(t, err)
		err = os.WriteFile(file2, []byte("let y = 2;"), 0644)
		require.NoError(t, err)

		stdout := &bytes.Buffer{}
		sources, idToSource := loadSources(stdout, []string{file1, file2})

		assert.Len(t, sources, 2)
		assert.Len(t, idToSource, 2)
		assert.Equal(t, "let x = 1;", sources[0].Contents)
		assert.Equal(t, "let y = 2;", sources[1].Contents)
		assert.Equal(t, sources[0], idToSource[0])
		assert.Equal(t, sources[1], idToSource[1])
	})

	t.Run("mix of valid and invalid files", func(t *testing.T) {
		tmpDir := t.TempDir()
		validFile := filepath.Join(tmpDir, "valid.esc")
		invalidFile := filepath.Join(tmpDir, "invalid.txt")

		err := os.WriteFile(validFile, []byte("let x = 1;"), 0644)
		require.NoError(t, err)
		err = os.WriteFile(invalidFile, []byte("content"), 0644)
		require.NoError(t, err)

		stdout := &bytes.Buffer{}
		sources, idToSource := loadSources(stdout, []string{validFile, invalidFile})

		// Should only load the valid file
		assert.Len(t, sources, 1)
		assert.Len(t, idToSource, 1)
		assert.Equal(t, validFile, sources[0].Path)
		assert.Contains(t, stdout.String(), "does not have .esc extension")
	})

	t.Run("empty file list", func(t *testing.T) {
		stdout := &bytes.Buffer{}
		sources, idToSource := loadSources(stdout, []string{})

		assert.Len(t, sources, 0)
		assert.Len(t, idToSource, 0)
	})
}

// Note: formatTypeError is tested indirectly through printErrors tests
// since checker.Error types have unexported fields

func TestWriteOutputFile(t *testing.T) {
	t.Run("write JavaScript file", func(t *testing.T) {
		tmpDir := t.TempDir()
		// Change to temp dir and create build directory
		oldCwd, err := os.Getwd()
		require.NoError(t, err)
		defer func() { _ = os.Chdir(oldCwd) }()
		err = os.Chdir(tmpDir)
		require.NoError(t, err)
		err = os.MkdirAll("build", 0755)
		require.NoError(t, err)

		stderr := &bytes.Buffer{}
		content := "console.log('hello');"
		err = writeOutputFile(stderr, "module", ".js", content)

		assert.NoError(t, err)

		// Verify file was created with correct content
		filePath := filepath.Join("build", "module.js")
		data, err := os.ReadFile(filePath)
		assert.NoError(t, err)
		assert.Equal(t, content, string(data))
	})

	t.Run("write TypeScript declaration file", func(t *testing.T) {
		tmpDir := t.TempDir()
		oldCwd, err := os.Getwd()
		require.NoError(t, err)
		defer func() { _ = os.Chdir(oldCwd) }()
		err = os.Chdir(tmpDir)
		require.NoError(t, err)
		err = os.MkdirAll("build", 0755)
		require.NoError(t, err)

		stderr := &bytes.Buffer{}
		content := "export declare const x: number;"
		err = writeOutputFile(stderr, "types", ".d.ts", content)

		assert.NoError(t, err)

		filePath := filepath.Join("build", "types.d.ts")
		data, err := os.ReadFile(filePath)
		assert.NoError(t, err)
		assert.Equal(t, content, string(data))
	})

	t.Run("write to subdirectory module", func(t *testing.T) {
		tmpDir := t.TempDir()
		oldCwd, err := os.Getwd()
		require.NoError(t, err)
		defer func() { _ = os.Chdir(oldCwd) }()
		err = os.Chdir(tmpDir)
		require.NoError(t, err)
		err = os.MkdirAll(filepath.Join("build", "utils"), 0755)
		require.NoError(t, err)

		stderr := &bytes.Buffer{}
		content := "export const helper = () => {};"
		err = writeOutputFile(stderr, "utils/helper", ".js", content)

		assert.NoError(t, err)

		filePath := filepath.Join("build", "utils", "helper.js")
		data, err := os.ReadFile(filePath)
		assert.NoError(t, err)
		assert.Equal(t, content, string(data))
	})
}

func TestWriteModuleOutputs(t *testing.T) {
	t.Run("write all module outputs", func(t *testing.T) {
		tmpDir := t.TempDir()
		oldCwd, err := os.Getwd()
		require.NoError(t, err)
		defer func() { _ = os.Chdir(oldCwd) }()
		err = os.Chdir(tmpDir)
		require.NoError(t, err)

		stderr := &bytes.Buffer{}
		output := compiler.CompUnitOutput{
			JS:        "console.log('test');",
			DTS:       "export declare const test: string;",
			SourceMap: `{"version":3,"sources":["test.esc"]}`,
		}

		err = writeModuleOutputs(stderr, "test", output)

		assert.NoError(t, err)

		// Check all three files were created
		jsData, err := os.ReadFile(filepath.Join("build", "test.js"))
		assert.NoError(t, err)
		assert.Equal(t, output.JS, string(jsData))

		dtsData, err := os.ReadFile(filepath.Join("build", "test.d.ts"))
		assert.NoError(t, err)
		assert.Equal(t, output.DTS, string(dtsData))

		mapData, err := os.ReadFile(filepath.Join("build", "test.js.map"))
		assert.NoError(t, err)
		assert.Equal(t, output.SourceMap, string(mapData))
	})

	t.Run("create nested directory structure", func(t *testing.T) {
		tmpDir := t.TempDir()
		oldCwd, err := os.Getwd()
		require.NoError(t, err)
		defer func() { _ = os.Chdir(oldCwd) }()
		err = os.Chdir(tmpDir)
		require.NoError(t, err)

		stderr := &bytes.Buffer{}
		output := compiler.CompUnitOutput{
			JS:        "export const x = 1;",
			DTS:       "export declare const x: number;",
			SourceMap: "{}",
		}

		err = writeModuleOutputs(stderr, "lib/utils/helper", output)

		assert.NoError(t, err)

		// Verify directory was created
		_, err = os.Stat(filepath.Join("build", "lib", "utils"))
		assert.NoError(t, err)

		// Verify file exists in nested directory
		jsData, err := os.ReadFile(filepath.Join("build", "lib", "utils", "helper.js"))
		assert.NoError(t, err)
		assert.Equal(t, output.JS, string(jsData))
	})
}

func TestPrintErrors(t *testing.T) {
	t.Run("print parse errors", func(t *testing.T) {
		stderr := &bytes.Buffer{}
		span := ast.Span{
			Start:    ast.Location{Offset: 0},
			End:      ast.Location{Offset: 4},
			SourceID: 0,
		}
		parseErr1 := &parser.Error{
			Message: "Parse error 1",
			Span:    span,
		}
		parseErr2 := &parser.Error{
			Message: "Parse error 2",
			Span:    span,
		}
		output := compiler.CompilerOutput{
			ParseErrors: []*parser.Error{parseErr1, parseErr2},
			TypeErrors:  []checker.Error{},
			CompUnits:   map[string]compiler.CompUnitOutput{},
		}
		idToSource := make(map[int]*ast.Source)

		printErrors(stderr, output, idToSource)

		result := stderr.String()
		assert.Contains(t, result, "Parse error 1")
		assert.Contains(t, result, "Parse error 2")
	})

	t.Run("no errors to print", func(t *testing.T) {
		stderr := &bytes.Buffer{}
		output := compiler.CompilerOutput{
			ParseErrors: []*parser.Error{},
			TypeErrors:  []checker.Error{},
			CompUnits:   map[string]compiler.CompUnitOutput{},
		}
		idToSource := make(map[int]*ast.Source)

		printErrors(stderr, output, idToSource)

		result := stderr.String()
		assert.Empty(t, result)
	})
}

func TestFormatTypeError(t *testing.T) {
	t.Parallel()
	// `π` takes two bytes, so a caret placed by byte offset would sit one
	// column too far right on the line that follows it.
	source := &ast.Source{
		ID:       0,
		Path:     "lib/index.esc",
		Contents: "val π = 1\nval x: number = \"hi\"\nval y = (1 +\n    2)\n",
	}

	tests := []struct {
		name  string
		start int
		end   int
		want  string
	}{
		{
			name:  "underlines the offending text",
			start: 27,
			end:   31,
			want: "lib/index.esc:2:17: mismatch\n\n" +
				"2:  val x: number = \"hi\"\n" +
				"                    ^^^^\n",
		},
		{
			name:  "a span running onto a later line stops at the first line's end",
			start: 40,
			end:   51,
			want: "lib/index.esc:3:9: mismatch\n\n" +
				"3:  val y = (1 +\n" +
				"            ^^^^\n",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			span := ast.NewSpan(ast.Location{Offset: tc.start}, ast.Location{Offset: tc.end}, source.ID)
			got := formatTypeError(checker.NewGenericError("mismatch", span), source)
			require.Equal(t, tc.want, got)
		})
	}
}

// A span the checker synthesized covers no source text, so there is no line to
// quote under it.
func TestFormatTypeErrorOnASynthesizedSpan(t *testing.T) {
	t.Parallel()
	source := &ast.Source{ID: 0, Path: "lib/index.esc", Contents: "val x = 1\n"}
	err := checker.NewGenericError("mismatch", checker.DEFAULT_SPAN)
	require.Equal(t, "lib/index.esc: mismatch\n", formatTypeError(err, source))
}
