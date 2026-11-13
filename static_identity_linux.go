package main

import (
	"os"
	"syscall"
)

func sameStaticFile(a, b os.FileInfo) bool {
	x, okA := a.Sys().(*syscall.Stat_t)
	y, okB := b.Sys().(*syscall.Stat_t)
	return okA && okB && os.SameFile(a, b) && x.Ctim == y.Ctim
}
