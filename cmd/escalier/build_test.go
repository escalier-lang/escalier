package main

import (
	"bytes"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"

	"github.com/escalier-lang/escalier/internal/ast"
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

		stderr := &bytes.Buffer{}
		sources, idToSource, failed := loadSources(stderr, []string{file1, file2})

		assert.Equal(t, 0, failed)
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

		stderr := &bytes.Buffer{}
		sources, idToSource, failed := loadSources(stderr, []string{validFile, invalidFile})

		// Should only load the valid file, and report the one it could not.
		assert.Equal(t, 1, failed)
		assert.Len(t, sources, 1)
		assert.Len(t, idToSource, 1)
		assert.Equal(t, validFile, sources[0].Path)
		assert.Equal(t, invalidFile+": file does not have .esc extension\n", stderr.String())
	})

	t.Run("empty file list", func(t *testing.T) {
		stderr := &bytes.Buffer{}
		sources, idToSource, failed := loadSources(stderr, []string{})

		assert.Equal(t, 0, failed)
		assert.Len(t, sources, 0)
		assert.Len(t, idToSource, 0)
	})
}

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
			TypeErrors:  []compiler.Diagnostic{},
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
			TypeErrors:  []compiler.Diagnostic{},
			CompUnits:   map[string]compiler.CompUnitOutput{},
		}
		idToSource := make(map[int]*ast.Source)

		printErrors(stderr, output, idToSource)

		result := stderr.String()
		assert.Empty(t, result)
	})
}

func TestErrorCount(t *testing.T) {
	t.Parallel()
	tests := []struct {
		n    int
		want string
	}{
		{n: 1, want: "1 error"},
		{n: 2, want: "2 errors"},
		{n: 13, want: "13 errors"},
	}
	for _, tt := range tests {
		require.Equal(t, tt.want, errorCount(tt.n))
	}
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
			got := formatTypeError(testDiagnostic{span: span, message: "mismatch"}, source)
			require.Equal(t, tc.want, got)
		})
	}
}

// A span the checker synthesized covers no source text, so there is no line to
// quote under it.
func TestFormatTypeErrorOnASynthesizedSpan(t *testing.T) {
	t.Parallel()
	source := &ast.Source{ID: 0, Path: "lib/index.esc", Contents: "val x = 1\n"}
	err := testDiagnostic{span: ast.Span{}, message: "mismatch"}
	require.Equal(t, "lib/index.esc: mismatch\n", formatTypeError(err, source))
}

// testDiagnostic is a compiler.Diagnostic with a fixed span and message.
type testDiagnostic struct {
	span    ast.Span
	message string
}

func (d testDiagnostic) Span() ast.Span  { return d.span }
func (d testDiagnostic) Message() string { return d.message }

func TestBuildEndsTheStatusLineBeforeDiagnostics(t *testing.T) {
	stdlibDir, err := filepath.Abs(filepath.Join("..", "..", "internal", "interop", "data"))
	require.NoError(t, err)
	t.Setenv("ESCALIER_STDLIB_DIR", stdlibDir)

	// writePackage returns the directory of a new package whose lib/index.esc
	// holds source. Each entry of extra writes one more file, keyed by its path
	// in the package, and replaces package.json when it names that file.
	writePackage := func(t *testing.T, source string, extra map[string]string) string {
		dir := t.TempDir()
		files := map[string]string{
			"package.json":  `{"name": "pkg", "version": "0.0.0", "type": "module"}`,
			"lib/index.esc": source,
		}
		for path, contents := range extra {
			files[path] = contents
		}
		for path, contents := range files {
			path = filepath.Join(dir, filepath.FromSlash(path))
			require.NoError(t, os.MkdirAll(filepath.Dir(path), 0755))
			require.NoError(t, os.WriteFile(path, []byte(contents), 0644))
		}
		return dir
	}

	badConfigDir := writePackage(
		t, "export val x = 1\n", map[string]string{"package.json": "{"},
	)
	// Listing lib/private fails once its permissions are cleared. Root reads it
	// anyway, and Windows ignores the mode, so that case skips on both.
	unreadableDir := writePackage(
		t, "export val x = 1\n", map[string]string{"lib/private/a.esc": ""},
	)
	privateDir := filepath.Join(unreadableDir, "lib", "private")
	require.NoError(t, os.Chmod(privateDir, 0))
	t.Cleanup(func() { _ = os.Chmod(privateDir, 0755) })
	canMakeUnreadable := runtime.GOOS != "windows" && os.Geteuid() != 0
	missingDir := filepath.Join(t.TempDir(), "missing")
	// The message os.Chdir gives for a missing directory differs between
	// operating systems, so the expected output quotes whatever it returns.
	chdirErr := os.Chdir(missingDir)
	require.Error(t, chdirErr)

	tests := []struct {
		name   string
		pkg    string
		ok     bool
		status string // a regexp for the text after `building: <pkg>` on its line
		diags  string
		skip   bool
	}{
		{
			name:   "a package with a type error",
			pkg:    writePackage(t, "export val x: number = \"hello\"\n", nil),
			status: regexp.QuoteMeta(" - failed (1 error)"),
			diags: "lib/index.esc:1:24: cannot constrain \"hello\" <: number\n\n" +
				"1:  export val x: number = \"hello\"\n" +
				"                           ^^^^^^^\n\n",
		},
		{
			name: "a package with a warning",
			pkg: writePackage(
				t, "export fn f() -> number throws string { return 1 }\n", nil,
			),
			ok:     true,
			status: ` - ok \(.+\)`,
			diags: "lib/index.esc:1:32: the body raises nothing, so the declared " +
				"`throws string` is unreachable; drop the clause\n\n" +
				"1:  export fn f() -> number throws string { return 1 }\n" +
				"                                   ^^^^^^\n\n",
		},
		{
			name:   "a package whose package.json doesn't parse",
			pkg:    badConfigDir,
			status: regexp.QuoteMeta(" - failed"),
			diags: "failed to read the package config for " + badConfigDir +
				": failed to parse " + filepath.Join(badConfigDir, "package.json") +
				": unexpected end of JSON input\n",
		},
		{
			name:   "a package whose sources can't be listed",
			pkg:    unreadableDir,
			status: regexp.QuoteMeta(" - failed"),
			diags: "failed to find source files for " + unreadableDir +
				": open lib/private: permission denied\n",
			skip: !canMakeUnreadable,
		},
		{
			// A file named build stops the output directory from being created.
			name: "a package whose output can't be written",
			pkg: writePackage(
				t, "export val x = 1\n", map[string]string{"build": ""},
			),
			status: regexp.QuoteMeta(" - failed"),
			diags:  "failed to create directory for module\n",
		},
		{
			name:   "a package directory that doesn't exist",
			pkg:    missingDir,
			status: regexp.QuoteMeta(" - failed"),
			diags: "failed to change directory to " + missingDir + ": " +
				chdirErr.Error() + "\n",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.skip {
				t.Skip("this OS or user can read a directory without permissions")
			}
			// One buffer stands in for a terminal that shows stdout and stderr
			// together, so the test sees the order the two streams interleave in.
			out := &bytes.Buffer{}
			require.Equal(t, tt.ok, build(out, out, []string{tt.pkg}))
			statusLine, diags, found := strings.Cut(out.String(), "\n")
			require.True(t, found)
			require.Regexp(
				t,
				"^"+regexp.QuoteMeta("building: "+tt.pkg)+tt.status+"$",
				statusLine,
			)
			require.Equal(t, tt.diags, diags)
		})
	}
}

// TestBuildReportsACompilerPanic asserts that a panic while compiling a package is
// reported as one internal-error diagnostic naming the file and phase, that the
// package writes no output, and that the build fails.
func TestBuildReportsACompilerPanic(t *testing.T) {
	tests := map[string]struct {
		panicValue any
		wantStderr string
	}{
		"a panic inside a compiler phase": {
			panicValue: &compiler.InternalError{
				File:  "lib/index.esc",
				Phase: "generating JavaScript for",
				Value: "TODO - buildExpr - default case",
			},
			wantStderr: "the Escalier compiler crashed while generating JavaScript for lib/index.esc: " +
				"TODO - buildExpr - default case. This is a bug in the compiler; please report it.\n",
		},
		"a panic outside every compiler phase": {
			panicValue: "boom",
			wantStderr: "the Escalier compiler crashed while building .: boom. " +
				"This is a bug in the compiler; please report it.\n",
		},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			tmpDir := t.TempDir()
			require.NoError(t, os.Mkdir(filepath.Join(tmpDir, "lib"), 0755))
			require.NoError(t, os.WriteFile(filepath.Join(tmpDir, "lib", "index.esc"), []byte("export val x = 1\n"), 0644))

			oldCwd, err := os.Getwd()
			require.NoError(t, err)
			t.Cleanup(func() { _ = os.Chdir(oldCwd) })
			require.NoError(t, os.Chdir(tmpDir))

			// No known program makes the compiler panic, so the test replaces the
			// compile with one that does.
			original := compilePackage
			t.Cleanup(func() { compilePackage = original })
			compilePackage = func([]*ast.Source, compiler.PackageConfig) compiler.CompilerOutput {
				panic(test.panicValue)
			}

			stdout := &bytes.Buffer{}
			stderr := &bytes.Buffer{}
			ok := build(stdout, stderr, []string{"."})

			require.False(t, ok)
			require.Equal(t, test.wantStderr, stderr.String())
			require.Equal(t, "building: . - failed (1 error)\n", stdout.String())
			_, statErr := os.Stat(filepath.Join(tmpDir, "build"))
			require.True(t, os.IsNotExist(statErr), "a crashed package writes no output")
		})
	}
}
