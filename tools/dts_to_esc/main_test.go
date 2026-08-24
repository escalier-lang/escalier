package main

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// seedLib writes one synthetic `lib.*.d.ts` into a fresh directory and
// returns that directory.
func seedLib(t *testing.T, contents string) string {
	t.Helper()
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(
		filepath.Join(dir, "lib.es5.d.ts"), []byte(contents), 0o644))
	return dir
}

const arrayLib = `
interface Array<T> { length: number; }
interface ArrayConstructor { new <T>(): Array<T>; isArray(arg: any): boolean; readonly prototype: Array<any>; }
declare var Array: ArrayConstructor;
`

func TestRun_CheckFailsOnAnEmptyTree(t *testing.T) {
	t.Parallel()
	libDir := seedLib(t, arrayLib)
	escDir := t.TempDir()

	var stdout strings.Builder
	err := run([]string{"check", libDir, escDir}, &stdout, io.Discard)
	require.ErrorIs(t, err, errCheckFailed)
	require.Contains(t, stdout.String(), "missing declaration: Array (class)")
}

func TestRun_CheckPassesAfterRegenerate(t *testing.T) {
	t.Parallel()
	libDir := seedLib(t, arrayLib)
	escDir := t.TempDir()

	var regenOut strings.Builder
	require.NoError(t, run([]string{"regenerate", libDir, escDir}, &regenOut, io.Discard))
	require.Contains(t, regenOut.String(), "created std:array (std/array.esc)")

	var checkOut strings.Builder
	require.NoError(t, run([]string{"check", libDir, escDir}, &checkOut, io.Discard))
	require.Contains(t, checkOut.String(),
		"check: 0 missing declarations, 0 missing members, 0 extra declarations")
}

func TestRun_RejectsWrongArgumentCounts(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name    string
		args    []string
		message string
	}{
		{"check without esc dir", []string{"check", "lib"},
			"usage: dts_to_esc check <lib-dir> <esc-dir>"},
		{"regenerate without esc dir", []string{"regenerate", "lib"},
			"usage: dts_to_esc regenerate <lib-dir> <esc-dir>"},
		{"no subcommand", nil, usage},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := run(tc.args, io.Discard, io.Discard)
			require.EqualError(t, err, tc.message)
		})
	}
}
