package dts_to_esc

import (
	"errors"
	"io/fs"
	"testing"

	"github.com/escalier-lang/escalier/internal/dts_parser"
	"github.com/stretchr/testify/require"
)

// cause stands in for whatever the filesystem, the converter, or the
// printer handed back.
var cause = errors.New("disk on fire")

// everyError is one value of each failure a pass can report, paired with
// the package it names and the line it prints. A run that aborts prints
// exactly these, so they are worth holding still.
var everyError = []struct {
	name    string
	err     Error
	pkg     string
	message string
}{
	{
		name: "unparseable file",
		err: &UnparseableFileError{
			pkgError: pkgError{PkgURI: "std:array"},
			Path:     "std/array.esc",
			File:     "/tmp/tree/std/array.esc",
			Reason:   "313:5-313:9: Expected a property name",
		},
		pkg:     "std:array",
		message: "parsing /tmp/tree/std/array.esc: 313:5-313:9: Expected a property name",
	},
	{
		name: "unreadable file",
		err: &FileReadError{
			pkgError: pkgError{PkgURI: "std:array"},
			File:     "/tmp/tree/std/array.esc",
			Cause:    cause,
		},
		pkg:     "std:array",
		message: "reading /tmp/tree/std/array.esc: disk on fire",
	},
	{
		name: "unwritable file",
		err: &FileWriteError{
			pkgError: pkgError{PkgURI: "std:array"},
			File:     "/tmp/tree/std/array.esc",
			Cause:    cause,
		},
		pkg:     "std:array",
		message: "writing /tmp/tree/std/array.esc: disk on fire",
	},
	{
		name: "uncreatable package dir",
		err: &PackageDirError{
			pkgError: pkgError{PkgURI: "std:array"},
			Dir:      "/tmp/tree/std",
			Cause:    cause,
		},
		pkg:     "std:array",
		message: "creating package dir for std:array: disk on fire",
	},
	{
		name: "bucket that would not convert",
		err: &BucketConvertError{
			pkgError: pkgError{PkgURI: "std:array"},
			Cause:    cause,
		},
		pkg:     "std:array",
		message: "converting bucket std:array: disk on fire",
	},
	{
		name: "URI outside PackageList",
		err:  &UnknownPackageError{pkgError: pkgError{PkgURI: "std:nope"}},
		pkg:  "std:nope",
		message: `unknown package URI "std:nope"; every bucket should come from ` +
			"Route, which only returns URIs in PackageList",
	},
	{
		name: "declaration the printer refused",
		err: &DeclRenderError{
			pkgError: pkgError{PkgURI: "std:array"},
			Name:     "Array",
			Cause:    cause,
		},
		pkg:     "std:array",
		message: "rendering Array in std:array: disk on fire",
	},
	{
		name: "member the printer refused",
		err: &MemberCompareError{
			pkgError: pkgError{PkgURI: "std:array"},
			Name:     "Array",
			Cause:    cause,
		},
		pkg:     "std:array",
		message: "comparing members of Array in std:array: disk on fire",
	},
	{
		name: "diff that would not render",
		err: &DiffRenderError{
			pkgError: pkgError{PkgURI: "std:array"},
			Cause:    cause,
		},
		pkg:     "std:array",
		message: "rendering the diff for std:array: disk on fire",
	},
}

// TestError_NamesItsPackageAndPrintsItsFailure covers every case of the
// Error interface. The message is what a contributor reads when a run
// aborts, and Pkg is what tells them which package to look at.
func TestError_NamesItsPackageAndPrintsItsFailure(t *testing.T) {
	t.Parallel()
	for _, tc := range everyError {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tc.pkg, tc.err.Pkg())
			require.EqualError(t, tc.err, tc.message)
		})
	}
}

// TestError_UnwrapsToTheUnderlyingFailure pins that a failure carrying a
// cause hands it back, so a caller can ask what the filesystem or the
// printer actually said rather than reading it out of the message.
func TestError_UnwrapsToTheUnderlyingFailure(t *testing.T) {
	t.Parallel()
	for _, tc := range everyError {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			// The two failures with nothing underneath them are the
			// parse rejection, which carries the parser's message rather
			// than an error, and the unknown URI, which is a routing bug
			// with no cause to name.
			switch tc.err.(type) {
			case *UnparseableFileError, *UnknownPackageError:
				require.NotErrorIs(t, tc.err, cause)
				return
			}
			require.ErrorIs(t, tc.err, cause)
		})
	}
}

// TestUnparseableFileError_MatchesTheUnreadableTreeSentinel covers the
// one failure a caller keys an exit status on: `check` and `regenerate`
// end differently for a tree they could not read than for one that is
// out of date.
func TestUnparseableFileError_MatchesTheUnreadableTreeSentinel(t *testing.T) {
	t.Parallel()
	unparseable := &UnparseableFileError{
		pkgError: pkgError{PkgURI: "std:array"},
		Path:     "std/array.esc",
		File:     "/tmp/tree/std/array.esc",
		Reason:   "313:5-313:9: Expected a property name",
	}
	require.ErrorIs(t, unparseable, ErrUnreadableTree)
	require.NotErrorIs(t, unparseable, fs.ErrNotExist)

	// A different failure against the same file is not the same outcome.
	read := &FileReadError{
		pkgError: pkgError{PkgURI: "std:array"},
		File:     "/tmp/tree/std/array.esc",
		Cause:    fs.ErrPermission,
	}
	require.NotErrorIs(t, read, ErrUnreadableTree)
	require.ErrorIs(t, read, fs.ErrPermission)
}

func TestJoin(t *testing.T) {
	t.Parallel()
	first := &BucketConvertError{
		pkgError: pkgError{PkgURI: "std:array"},
		Cause:    cause,
	}
	second := &UnknownPackageError{pkgError: pkgError{PkgURI: "std:nope"}}

	require.NoError(t, Join(nil))
	require.EqualError(t, Join([]Error{first}),
		"converting bucket std:array: disk on fire")
	require.EqualError(t, Join([]Error{first, second}),
		"converting bucket std:array: disk on fire\n"+
			`unknown package URI "std:nope"; every bucket should come from `+
			"Route, which only returns URIs in PackageList")

	// A caller that wants one failure back still gets it through the
	// join, which is what the CLI's exit status depends on.
	var convert *BucketConvertError
	require.ErrorAs(t, Join([]Error{first, second}), &convert)
	require.Equal(t, "std:array", convert.Pkg())
}

// TestPlanPartition_ReportsABucketOutsideThePackageList covers the guard
// on Route's output. Every bucket is supposed to carry a URI from
// PackageList, so a bucket that does not is a routing bug: the pass
// names it and moves on to the packages it can place, rather than
// planning against a package it cannot locate on disk.
func TestPlanPartition_ReportsABucketOutsideThePackageList(t *testing.T) {
	t.Parallel()
	res := &PartitionResult{
		Buckets: map[string][]dts_parser.Statement{"std:nope": nil},
	}

	_, errs := CheckPartition(res, t.TempDir())
	require.Len(t, errs, 1)
	require.Equal(t, "std:nope", errs[0].Pkg())
	require.EqualError(t, errs[0],
		`unknown package URI "std:nope"; every bucket should come from `+
			"Route, which only returns URIs in PackageList")
}
