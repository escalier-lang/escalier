package dts_to_esc

import (
	"errors"
	"fmt"
)

// This file names every way a pass over the committed `.esc` tree can
// fail. The passes are in rerun.go: planning a package, checking it, and
// writing it back.
//
// Each failure is its own type rather than a formatted string, so a
// caller can ask what went wrong instead of matching on a message, and
// so adding a failure mode means adding a type here rather than another
// fmt.Errorf at the site. The passes return `[]Error`, which lets one
// run report every package that failed instead of stopping at the first.
//
// Two things are deliberately outside this vocabulary. A pure printer
// failure stays a plain `error` until the caller that knows the package
// wraps it, since the printer knows nothing about packages. And the
// argument and usage errors of the CLI are the command's own, not a
// pass's.

// Error is one failure a pass over the committed tree reports. The set
// of implementations is closed to this file, so a type switch over an
// Error covers every case the passes can produce.
type Error interface {
	error

	// Pkg is the package URI the failure belongs to, e.g. "std:array".
	// Every failure happens while one package is being planned,
	// checked, or written.
	Pkg() string

	// rerunError closes the set. It has no behavior.
	rerunError()
}

// pkgError holds the package URI every failure names, so each concrete
// type states it once by embedding this.
type pkgError struct {
	// PkgURI is the package the failure happened under.
	PkgURI string
}

func (e pkgError) Pkg() string { return e.PkgURI }

func (e pkgError) rerunError() {}

// ErrUnreadableTree marks a run that could not parse part of the
// committed `.esc` tree. Both modes keep going and report every package
// they could read, so this error is what a caller keys on to tell a tree
// the tool could not read from a tree that is merely out of date.
var ErrUnreadableTree = errors.New("committed .esc files could not be parsed")

// UnparseableFileError is a committed `.esc` file the Escalier parser
// rejected.
//
// It is the one failure a pass carries on from. Its package is left out
// of the diff, since neither mode can tell what a file it cannot read
// already declares, and every other package is planned as usual. It
// reaches the caller in the report's Unreadable list rather than in the
// error slice, so the run still prints what it found.
type UnparseableFileError struct {
	pkgError

	// Path is the file's path relative to the `.esc` tree root, e.g.
	// "std/array.esc". The reports print this, so their output does not
	// depend on where the tree lives.
	Path string

	// File is the same file's path on disk, which the message names so a
	// contributor can open it.
	File string

	// Reason is the first parse error, in the parser's own
	// "line:col-line:col: message" form.
	Reason string
}

func (e *UnparseableFileError) Error() string {
	return fmt.Sprintf("parsing %s: %s", e.File, e.Reason)
}

// Is matches ErrUnreadableTree, so a caller can key an exit status on
// the sentinel without walking the list for this type.
func (e *UnparseableFileError) Is(target error) bool { return target == ErrUnreadableTree }

// FileReadError is a committed file the process could not read at all,
// as opposed to one the parser rejected. The tree is broken rather than
// out of date, so the run stops.
type FileReadError struct {
	pkgError

	// File is the path on disk that could not be read.
	File string

	// Cause is what the filesystem returned.
	Cause error
}

func (e *FileReadError) Error() string {
	return fmt.Sprintf("reading %s: %s", e.File, e.Cause)
}

func (e *FileReadError) Unwrap() error { return e.Cause }

// FileWriteError is a package file the write pass could not replace.
type FileWriteError struct {
	pkgError

	// File is the path on disk that could not be written.
	File string

	// Cause is what the filesystem returned.
	Cause error
}

func (e *FileWriteError) Error() string {
	return fmt.Sprintf("writing %s: %s", e.File, e.Cause)
}

func (e *FileWriteError) Unwrap() error { return e.Cause }

// PackageDirError is the directory holding a package file that the write
// pass could not create.
type PackageDirError struct {
	pkgError

	// Dir is the path on disk that could not be created.
	Dir string

	// Cause is what the filesystem returned.
	Cause error
}

func (e *PackageDirError) Error() string {
	return fmt.Sprintf("creating package dir for %s: %s", e.Pkg(), e.Cause)
}

func (e *PackageDirError) Unwrap() error { return e.Cause }

// BucketConvertError is a routed bucket the converter could not turn
// into a module, so the package has no converted side to compare the
// committed one against.
type BucketConvertError struct {
	pkgError

	// Cause is what the converter returned.
	Cause error
}

func (e *BucketConvertError) Error() string {
	return fmt.Sprintf("converting bucket %s: %s", e.Pkg(), e.Cause)
}

func (e *BucketConvertError) Unwrap() error { return e.Cause }

// UnknownPackageError is a bucket whose package URI is not in
// PackageList. Route only ever returns URIs from that list, so this
// marks a bug in the routing pass rather than anything about the tree.
type UnknownPackageError struct {
	pkgError
}

func (e *UnknownPackageError) Error() string {
	return fmt.Sprintf("unknown package URI %q; every bucket should come from "+
		"Route, which only returns URIs in PackageList", e.Pkg())
}

// DeclRenderError is a converted declaration the printer would not turn
// into Escalier source, so the pass has no text to add for it.
type DeclRenderError struct {
	pkgError

	// Name is the declaration's name.
	Name string

	// Cause is what the printer returned.
	Cause error
}

func (e *DeclRenderError) Error() string {
	return fmt.Sprintf("rendering %s in %s: %s", e.Name, e.Pkg(), e.Cause)
}

func (e *DeclRenderError) Unwrap() error { return e.Cause }

// MemberCompareError is a declaration whose members could not be
// compared against the committed side, because the printer would not
// render one of them.
type MemberCompareError struct {
	pkgError

	// Name is the name both sides share.
	Name string

	// Cause is what the printer returned.
	Cause error
}

func (e *MemberCompareError) Error() string {
	return fmt.Sprintf("comparing members of %s in %s: %s", e.Name, e.Pkg(), e.Cause)
}

func (e *MemberCompareError) Unwrap() error { return e.Cause }

// DiffRenderError is a package whose unified diff could not be built, so
// the check has the findings but not the patch that shows them.
type DiffRenderError struct {
	pkgError

	// Cause is what the diff renderer returned.
	Cause error
}

func (e *DiffRenderError) Error() string {
	return fmt.Sprintf("rendering the diff for %s: %s", e.Pkg(), e.Cause)
}

func (e *DiffRenderError) Unwrap() error { return e.Cause }

// Join folds a pass's failures into one error and returns nil when there
// were none. errors.Is and errors.As see through the result, so a caller
// that wants one failure back can still ask for it.
func Join(errs []Error) error {
	if len(errs) == 0 {
		return nil
	}
	joined := make([]error, len(errs))
	for i, err := range errs {
		joined[i] = err
	}
	return errors.Join(joined...)
}
