//go:build !unix && !windows

package auth

import "os"

// Everywhere else there is no file lock to take, so the store is serialized within the
// process and no further. hey is built for macOS, Linux and Windows; this keeps a build
// for anything else honest rather than failing to compile.

func acquireLock(*os.File) error { return nil }

func releaseLock(*os.File) error { return nil }
