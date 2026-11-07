package applog

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func read(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path) // #nosec G304 -- the test reads files it wrote in its temporary directory.
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		t.Fatal(err)
	}
	return string(data)
}

func write(t *testing.T, f *File, text string) {
	t.Helper()
	if _, err := f.Write([]byte(text)); err != nil {
		t.Fatal(err)
	}
}

func TestLogKeepsAppendingAcrossLaunches(t *testing.T) {
	dir := t.TempDir()
	for _, line := range []string{"first\n", "second\n"} {
		f, err := Open(dir, 1<<10)
		if err != nil {
			t.Fatal(err)
		}
		write(t, f, line)
		if err := f.Close(); err != nil {
			t.Fatal(err)
		}
	}
	if got := read(t, filepath.Join(dir, FileName)); got != "first\nsecond\n" {
		t.Fatalf("%q", got)
	}
}

func TestLogRotatesAtTheLimit(t *testing.T) {
	dir := t.TempDir()
	f, err := Open(dir, 10)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	write(t, f, "123456\n")
	write(t, f, "abcdef\n")
	write(t, f, "ABCDEF\n")
	if got := read(t, f.Path()); got != "ABCDEF\n" {
		t.Fatalf("current %q", got)
	}
	if got := read(t, f.Path()+".1"); got != "abcdef\n" {
		t.Fatalf("previous %q", got)
	}
}

func TestLogLongerThanTheLimitIsStillWritten(t *testing.T) {
	f, err := Open(t.TempDir(), 4)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	write(t, f, "a long line\n")
	if got := read(t, f.Path()); got != "a long line\n" {
		t.Fatalf("%q", got)
	}
}

func TestOversizedLogRotatesWhenOpened(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, FileName)
	if err := os.WriteFile(path, []byte(strings.Repeat("x", 20)), 0600); err != nil {
		t.Fatal(err)
	}
	f, err := Open(dir, 10)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	write(t, f, "new\n")
	if got := read(t, path); got != "new\n" {
		t.Fatalf("%q", got)
	}
	if got := read(t, path+".1"); len(got) != 20 {
		t.Fatalf("%q", got)
	}
}

func TestClosedLogRefusesWrites(t *testing.T) {
	f, err := Open(t.TempDir(), 10)
	if err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := f.Write([]byte("late")); !errors.Is(err, fs.ErrClosed) {
		t.Fatal(err)
	}
}

func TestLogWritesFromManyGoroutines(t *testing.T) {
	f, err := Open(t.TempDir(), 1<<20)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			for range 100 {
				if _, err := f.Write([]byte("line\n")); err != nil {
					t.Error(err)
				}
			}
		})
	}
	wg.Wait()
	if got := strings.Count(read(t, f.Path()), "line\n"); got != 800 {
		t.Fatal(got)
	}
}
