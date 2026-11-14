package main

import (
	"bytes"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/escalier-lang/escalier/internal/compiler"
)

// playgroundProject is one example or template the playground offers.
type playgroundProject struct {
	// name is the project's category and directory, such as "examples/hello-world".
	name string
	dir  string
}

// playgroundProjects returns every example and template under playground/.
func playgroundProjects(t *testing.T, repoRoot string) []playgroundProject {
	var projects []playgroundProject
	for _, category := range []string{"examples", "templates"} {
		categoryDir := filepath.Join(repoRoot, "playground", category)
		entries, err := os.ReadDir(categoryDir)
		require.NoError(t, err)
		for _, entry := range entries {
			if entry.IsDir() {
				projects = append(projects, playgroundProject{
					name: category + "/" + entry.Name(),
					dir:  filepath.Join(categoryDir, entry.Name()),
				})
			}
		}
	}
	return projects
}

// preparePlaygroundBuildDir copies project into a temporary build directory, as
// prepareBuildDir does for a fixture, and adds the project's package.json, which
// the playground reads. It returns the temporary directory.
func preparePlaygroundBuildDir(t *testing.T, repoRoot string, project playgroundProject) string {
	tmpDir := prepareBuildDir(t, repoRoot, project.dir)
	pkg, err := os.ReadFile(filepath.Join(project.dir, "package.json"))
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(tmpDir, "package.json"), pkg, 0644))
	return tmpDir
}

// restoreWorkingDir changes back to the current working directory when t ends,
// since prepareBuildDir changes into the build directory.
func restoreWorkingDir(t *testing.T) {
	origDir, err := os.Getwd()
	require.NoError(t, err)
	t.Cleanup(func() {
		_ = os.Chdir(origDir)
	})
}

// TestPlaygroundExamplesBuild asserts that every example and template the playground
// offers builds without a diagnostic, so a checker change that breaks one fails here
// rather than in the playground.
func TestPlaygroundExamplesBuild(t *testing.T) {
	repoRoot, err := findRepoRoot()
	require.NoError(t, err, "failed to find repository root")
	restoreWorkingDir(t)

	for _, project := range playgroundProjects(t, repoRoot) {
		t.Run(project.name, func(t *testing.T) {
			preparePlaygroundBuildDir(t, repoRoot, project)

			stdout := bytes.NewBuffer(nil)
			stderr := bytes.NewBuffer(nil)
			buildOK := build(stdout, stderr, []string{"."})

			require.Empty(t, stderr.String())
			require.True(t, buildOK)
		})
	}
}

// errorNoteMarker opens an error note in a playground example. A note quotes the
// error that a commented-out line produces, so a reader can uncomment the line to
// see it. The note looks like this:
//
//	// Uncomment the line below to see the error:
//	// > cannot assign to 'id' because it is a readonly property
//	// tasks[0].id = 7
//
// Each line after the marker that starts with errorNoteQuote holds one line of the
// message. The line after those is the commented-out code.
const errorNoteMarker = "// Uncomment the line below to see the error:"

const errorNoteQuote = "// >"

// errorNote is one error note in a source file. The line numbers index the file's
// lines from zero.
type errorNote struct {
	// path is the source file's path relative to its project, such as "lib/index.esc".
	path string
	// marker is the line holding errorNoteMarker.
	marker int
	// code is the commented-out line the note quotes the error of.
	code int
	// message is the quoted message, with its lines joined by newlines.
	message string
}

// findErrorNotes returns the error notes in the source file at path, which lines
// holds. It fails t when a note is malformed, or when a comment asks the reader to
// uncomment something without using errorNoteMarker, since the test could not
// check that comment's message.
func findErrorNotes(t *testing.T, path string, lines []string) []errorNote {
	var notes []errorNote
	for i := 0; i < len(lines); i++ {
		trimmed := strings.TrimSpace(lines[i])
		if trimmed != errorNoteMarker {
			require.NotContains(t, trimmed, "Uncomment",
				"%s:%d: write the note as %q, then the message, then the line", path, i+1, errorNoteMarker)
			continue
		}
		var message []string
		j := i + 1
		for ; j < len(lines); j++ {
			quoted, ok := strings.CutPrefix(strings.TrimSpace(lines[j]), errorNoteQuote)
			if !ok {
				break
			}
			message = append(message, strings.TrimPrefix(quoted, " "))
		}
		require.NotEmpty(t, message, "%s:%d: the note quotes no message", path, i+1)
		require.Less(t, j, len(lines), "%s:%d: the note has no line to uncomment", path, i+1)
		require.True(t, strings.HasPrefix(strings.TrimSpace(lines[j]), "// "),
			"%s:%d: the line after the message is not commented out", path, j+1)
		notes = append(notes, errorNote{
			path:    path,
			marker:  i,
			code:    j,
			message: strings.Join(message, "\n"),
		})
		i = j
	}
	return notes
}

// uncomment returns line with the `// ` that comments it out removed, keeping its
// indentation.
func uncomment(line string) string {
	indent := line[:len(line)-len(strings.TrimLeft(line, " \t"))]
	return indent + strings.TrimPrefix(strings.TrimLeft(line, " \t"), "// ")
}

// packageDiagnostics compiles the package in the working directory and returns
// the message of each diagnostic, parse errors first.
func packageDiagnostics(t *testing.T) []string {
	files, err := compiler.FindSourceFiles()
	require.NoError(t, err)
	config, err := compiler.ReadPackageConfig(".")
	require.NoError(t, err)
	sources, _, unreadable := loadSources(io.Discard, files)
	require.Zero(t, unreadable)

	output := compiler.CompilePackage(sources, config)
	var messages []string
	for _, parseErr := range output.ParseErrors {
		messages = append(messages, parseErr.Message)
	}
	for _, typeErr := range output.TypeErrors {
		messages = append(messages, typeErr.Message())
	}
	return messages
}

// TestPlaygroundExampleErrorNotes asserts that uncommenting the line under each
// error note in a playground example produces the error the note quotes, as the
// first diagnostic the package reports. With UPDATE_FIXTURES=true it rewrites each
// note's quoted message to the diagnostic instead.
func TestPlaygroundExampleErrorNotes(t *testing.T) {
	repoRoot, err := findRepoRoot()
	require.NoError(t, err, "failed to find repository root")
	restoreWorkingDir(t)
	update := os.Getenv("UPDATE_FIXTURES") == "true"

	for _, project := range playgroundProjects(t, repoRoot) {
		for _, dir := range []string{"lib", "bin"} {
			sourceFiles, _ := filepath.Glob(filepath.Join(project.dir, dir, "*.esc"))
			for _, sourceFile := range sourceFiles {
				path, err := filepath.Rel(project.dir, sourceFile)
				require.NoError(t, err)
				content, err := os.ReadFile(sourceFile)
				require.NoError(t, err)
				lines := strings.Split(string(content), "\n")
				notes := findErrorNotes(t, path, lines)

				// An update can change how many lines a message takes, which moves
				// the lines below it, so the notes are checked from the bottom up.
				for k := len(notes) - 1; k >= 0; k-- {
					note := notes[k]
					t.Run(project.name+"/"+path+":"+strconv.Itoa(note.code+1), func(t *testing.T) {
						tmpDir := preparePlaygroundBuildDir(t, repoRoot, project)
						edited := append([]string(nil), lines...)
						edited[note.code] = uncomment(edited[note.code])
						require.NoError(t, os.WriteFile(filepath.Join(tmpDir, note.path),
							[]byte(strings.Join(edited, "\n")), 0644))

						messages := packageDiagnostics(t)
						require.NotEmpty(t, messages, "uncommenting the line reports no error")

						if update {
							lines = replaceQuote(lines, note, messages[0])
							return
						}
						require.Equal(t, note.message, messages[0])
					})
				}
				if update {
					require.NoError(t, os.WriteFile(sourceFile, []byte(strings.Join(lines, "\n")), 0644))
				}
			}
		}
	}
}

// replaceQuote returns lines with note's quoted message replaced by message, one
// line of the message per quote line, indented like the note's marker.
func replaceQuote(lines []string, note errorNote, message string) []string {
	marker := lines[note.marker]
	indent := marker[:len(marker)-len(strings.TrimLeft(marker, " \t"))]
	var quote []string
	for _, line := range strings.Split(message, "\n") {
		quote = append(quote, strings.TrimRight(indent+errorNoteQuote+" "+line, " "))
	}
	updated := append([]string(nil), lines[:note.marker+1]...)
	updated = append(updated, quote...)
	return append(updated, lines[note.code:]...)
}

func TestFindErrorNotes(t *testing.T) {
	lines := []string{
		"fn f() {",
		"    // Uncomment the line below to see the error:",
		"    // > first line",
		"    // >   second line",
		"    // x = 1",
		"}",
	}

	require.Equal(t, []errorNote{{
		path:    "lib/index.esc",
		marker:  1,
		code:    4,
		message: "first line\n  second line",
	}}, findErrorNotes(t, "lib/index.esc", lines))
}

func TestUncomment(t *testing.T) {
	tests := []struct {
		line     string
		expected string
	}{
		{"// val x = 1", "val x = 1"},
		{"    // x.done = true", "    x.done = true"},
		{"\t// f()", "\tf()"},
	}
	for _, test := range tests {
		t.Run(test.line, func(t *testing.T) {
			require.Equal(t, test.expected, uncomment(test.line))
		})
	}
}

func TestReplaceQuote(t *testing.T) {
	lines := []string{
		"    // Uncomment the line below to see the error:",
		"    // > old message",
		"    // x = 1",
		"    return x",
	}
	note := errorNote{marker: 0, code: 2, message: "old message"}

	require.Equal(t, []string{
		"    // Uncomment the line below to see the error:",
		"    // > new message",
		"    // >   with a second line",
		"    // x = 1",
		"    return x",
	}, replaceQuote(lines, note, "new message\n  with a second line"))
}
