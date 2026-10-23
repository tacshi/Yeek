package engine

import (
	"os"
	"path/filepath"
)

const (
	legacySyncPrefix          = "yaak."
	legacyBaseEnvironmentSeed = "yaak-base:"
	legacyPromptNamespace     = "plugin:@yaak/template-function-prompt"
)

const syncPrefix = "yeek."

func SyncFileName(dir, id string) string {
	for _, name := range []string{syncPrefix + id + ".yaml", legacySyncPrefix + id + ".yaml"} {
		if _, err := os.Stat(filepath.Join(dir, name)); err == nil {
			return name
		}
	}
	return syncPrefix + id + ".yaml"
}
