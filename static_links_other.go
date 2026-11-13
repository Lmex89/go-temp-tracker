//go:build !unix

package main

import "os"

// Without a link count, do not serve files when private configuration is selected.
func singleLink(os.FileInfo) bool { return false }
