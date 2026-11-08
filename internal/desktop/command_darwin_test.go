package desktop

import (
	"os"
	"path/filepath"
	"testing"
)

func TestCommandOnlyReplacesLinksToAnotherCopy(t *testing.T) {
	saved := commandDir
	t.Cleanup(func() { commandDir = saved })
	commandDir = t.TempDir()
	apps := t.TempDir()
	tool := filepath.Join(apps, "Other.app", "Contents", "MacOS", "tool")
	if err := os.MkdirAll(filepath.Dir(tool), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(tool, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	exe := "/Applications/Yeek.app/Contents/MacOS/Yeek"
	for target, want := range map[string]commandStatus{
		tool: {taken: filepath.Join(commandDir, "yeek")},
		filepath.Join(apps, "Gone.app", "Contents", "MacOS", "helper"): {taken: filepath.Join(commandDir, "yeek")},
		filepath.Join(apps, "Old.app", "Contents", "MacOS", "Yeek"):    {other: true},
	} {
		link := filepath.Join(commandDir, "yeek")
		_ = os.Remove(link)
		if err := os.Symlink(target, link); err != nil {
			t.Fatal(err)
		}
		got := commandState("yeek", exe)
		if got.taken != want.taken || got.other != want.other || got.installed {
			t.Errorf("%s: %+v", target, got)
		}
	}
}
