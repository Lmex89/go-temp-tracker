//go:build !linux

package main

import "os"

// Without a reliable change time, fail closed for selected configurations.
func sameStaticFile(os.FileInfo, os.FileInfo) bool { return false }
