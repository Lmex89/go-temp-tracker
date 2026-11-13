package main

import (
	"encoding/binary"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"golang.org/x/sys/unix"
)

type configWatch struct {
	file  *os.File
	mu    sync.Mutex
	names map[int]map[string]bool
}

func newConfigWatch(path string) (*configWatch, error) {
	fd, err := unix.InotifyInit1(unix.IN_NONBLOCK | unix.IN_CLOEXEC)
	if err != nil {
		return nil, err
	}
	w := &configWatch{file: os.NewFile(uintptr(fd), "config-watch"), names: make(map[int]map[string]bool)}
	var addPath func(string, int) error
	addPath = func(path string, depth int) error {
		if depth > 40 {
			return unix.ELOOP
		}
		path, err := filepath.Abs(path)
		if err != nil {
			return err
		}
		parent := string(filepath.Separator)
		for _, name := range strings.Split(strings.TrimPrefix(path, parent), parent) {
			if name == "" {
				continue
			}
			wd, err := unix.InotifyAddWatch(fd, parent, unix.IN_CREATE|unix.IN_DELETE|unix.IN_MOVED_FROM|unix.IN_MOVED_TO|unix.IN_ATTRIB|unix.IN_CLOSE_WRITE|unix.IN_DELETE_SELF|unix.IN_MOVE_SELF)
			if err != nil {
				return err
			}
			if w.names[wd] == nil {
				w.names[wd] = make(map[string]bool)
			}
			w.names[wd][name] = true
			next := filepath.Join(parent, name)
			info, err := os.Lstat(next)
			if err != nil {
				return err
			}
			if info.Mode()&os.ModeSymlink != 0 {
				target, err := os.Readlink(next)
				if err != nil {
					return err
				}
				if !filepath.IsAbs(target) {
					target = filepath.Join(parent, target)
				}
				if err := addPath(target, depth+1); err != nil {
					return err
				}
				next, err = filepath.EvalSymlinks(next)
				if err != nil {
					return err
				}
			}
			parent = next
		}
		return nil
	}
	if err := addPath(path, 0); err != nil {
		w.file.Close()
		return nil, err
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err == nil {
		err = addPath(resolved, 0)
	}
	if err != nil {
		w.file.Close()
		return nil, err
	}
	return w, nil
}

// changed drains queued path events before each file is served.
func (w *configWatch) changed() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	buf := make([]byte, 4096)
	changed := false
	for {
		n, err := unix.Read(int(w.file.Fd()), buf)
		if errors.Is(err, unix.EAGAIN) {
			return changed
		}
		if errors.Is(err, unix.EINTR) {
			continue
		}
		if err != nil || n == 0 {
			return true
		}
		for offset := 0; offset < n; {
			if n-offset < unix.SizeofInotifyEvent {
				return true
			}
			wd := int(int32(binary.NativeEndian.Uint32(buf[offset:])))
			mask := binary.NativeEndian.Uint32(buf[offset+4:])
			length := int(binary.NativeEndian.Uint32(buf[offset+12:]))
			next := offset + unix.SizeofInotifyEvent + length
			if next > n {
				return true
			}
			name := strings.TrimRight(string(buf[offset+unix.SizeofInotifyEvent:next]), "\x00")
			if mask&(unix.IN_Q_OVERFLOW|unix.IN_IGNORED|unix.IN_DELETE_SELF|unix.IN_MOVE_SELF) != 0 || w.names[wd][name] {
				changed = true
			}
			offset = next
		}
	}
}
