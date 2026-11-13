//go:build !linux

package main

import "errors"

type configWatch struct{}

func newConfigWatch(string) (*configWatch, error) {
	return nil, errors.New("config watcher unavailable")
}
func (*configWatch) changed() bool { return true }
