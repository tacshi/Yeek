package desktop

import (
	"context"
	"errors"
	"io"
	"path"

	"github.com/egoist/mygo"
	"yeek/internal/engine"
)

// watchSyncDir is the engine's DirWatcher: mygo.WatchFiles, which uses
// FSEvents on macOS as Yaak's sync does, without a handle per file.
func watchSyncDir(ctx context.Context, dir string, changed func(), onError func(error)) error {
	w, err := mygo.WatchFiles(ctx, dir, mygo.FileWatchOptions{
		Ignore: func(p string, isDir bool) bool { return !isDir && engine.SyncTempFile(path.Base(p)) },
	})
	if err != nil {
		return err
	}
	go func() {
		defer func() { _ = w.Close() }()
		for {
			if _, err := w.Next(ctx); err != nil {
				if !errors.Is(err, io.EOF) && ctx.Err() == nil {
					onError(err)
				}
				return
			}
			changed()
		}
	}()
	return nil
}
