//go:build !unix

package store

import "os"

// Appends are not locked on platforms without flock. Hooks may interleave
// there; readers skip any line that does not parse.
func lock(*os.File) error   { return nil }
func unlock(*os.File) error { return nil }
