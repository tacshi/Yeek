package applog

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
)

const (
	FileName = "yeek.log"
	MaxSize  = 5 << 20
)

type File struct {
	mu    sync.Mutex
	path  string
	limit int64
	file  *os.File
	size  int64
}

func Open(dir string, limit int64) (*File, error) {
	f := &File{path: filepath.Join(dir, FileName), limit: limit}
	if err := f.open(); err != nil {
		return nil, err
	}
	if f.size >= limit {
		if err := f.rotate(); err != nil && f.file == nil {
			return nil, err
		}
	}
	return f, nil
}

func (f *File) Path() string { return f.path }

func (f *File) Write(p []byte) (int, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.file == nil {
		return 0, fs.ErrClosed
	}
	if f.size > 0 && f.size+int64(len(p)) > f.limit {
		if err := f.rotate(); err != nil && f.file == nil {
			return 0, err
		}
	}
	n, err := f.file.Write(p)
	f.size += int64(n)
	return n, err
}

func (f *File) Close() error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.file == nil {
		return nil
	}
	err := f.file.Close()
	f.file = nil
	return err
}

func (f *File) open() error {
	file, err := os.OpenFile(f.path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0600) // #nosec G304 -- the path is the app's own log directory.
	if err != nil {
		return err
	}
	info, err := file.Stat()
	if err != nil {
		return errors.Join(err, file.Close())
	}
	f.file, f.size = file, info.Size()
	return nil
}

func (f *File) rotate() error {
	closed := f.file.Close()
	f.file = nil
	renamed := os.Rename(f.path, f.path+".1")
	if errors.Is(renamed, fs.ErrNotExist) || renamed != nil && os.Truncate(f.path, 0) == nil {
		renamed = nil
	}
	return errors.Join(closed, renamed, f.open())
}
