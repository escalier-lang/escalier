package dts_to_esc

import "fmt"

// This file names every way `generate` can fail while one package is
// being converted or written. The pass itself is in generate.go and the
// two steps that produce these failures are in partition_writer.go:
// ConvertBuckets and WriteConvertedTree.
//
// Each failure is its own type rather than a formatted string, so a
// caller can ask what went wrong instead of matching on a message, and
// so adding a failure mode means adding a type here rather than another
// fmt.Errorf at the site.
//
// Two things are deliberately outside this vocabulary. A failure that
// belongs to no single package, such as reading the lib directory or
// loading the overlay, stays a plain error. So do the argument and usage
// errors of the CLI, which are the command's own.

// Error is one failure a run reports while working on a single package.
// The set of implementations is closed to this file, so a type switch
// over an Error covers every case a run can produce.
type Error interface {
	error

	// Pkg is the package URI the failure belongs to, e.g. "std:array".
	Pkg() string

	// packageError closes the set. It has no behavior.
	packageError()
}

// pkgError holds the package URI every failure names, so each concrete
// type states it once by embedding this.
type pkgError struct {
	// PkgURI is the package the failure happened under.
	PkgURI string
}

func (e pkgError) Pkg() string { return e.PkgURI }

func (e pkgError) packageError() {}

// BucketConvertError is a routed bucket the converter could not turn
// into a module, so the package has nothing to write.
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
// marks a bug in the routing pass rather than anything about the inputs.
type UnknownPackageError struct {
	pkgError
}

func (e *UnknownPackageError) Error() string {
	return fmt.Sprintf("unknown package URI %q; every bucket should come from "+
		"Route, which only returns URIs in PackageList", e.Pkg())
}

// PackageDirError is the directory holding a package file that the run
// could not create.
type PackageDirError struct {
	pkgError

	// Dir is the path on disk that could not be created.
	Dir string

	// Cause is what the filesystem returned.
	Cause error
}

func (e *PackageDirError) Error() string {
	return fmt.Sprintf("creating package dir %s for %s: %s", e.Dir, e.Pkg(), e.Cause)
}

func (e *PackageDirError) Unwrap() error { return e.Cause }

// FileWriteError is a package file the run could not lay down. Opening
// it, printing the module into it, and closing it all reach this. Each
// one leaves the same package unwritten, and Cause names which step
// failed.
type FileWriteError struct {
	pkgError

	// File is the path on disk that could not be written.
	File string

	// Cause is what the filesystem or the printer returned.
	Cause error
}

func (e *FileWriteError) Error() string {
	return fmt.Sprintf("writing %s: %s", e.File, e.Cause)
}

func (e *FileWriteError) Unwrap() error { return e.Cause }
