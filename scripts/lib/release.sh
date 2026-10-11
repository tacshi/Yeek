# Shared by mac-release.sh, win-release.sh and publish.sh; source it.

die() {
	printf '%s: %s\n' "$(basename "$0")" "$*" >&2
	exit 1
}
info() { printf '==> %s\n' "$*"; }

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/../.." && pwd)"

# NSIS (makensis) crashes with std::bad_alloc when no locale is set.
export LANG="${LANG:-en_US.UTF-8}"

# check_version fails unless $1 looks like 1.2.3.
check_version() {
	[[ "$1" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]] || die "the version must look like 1.2.3, not $1"
}

# config_version prints the version in mygo.json.
config_version() {
	python3 -I -c 'import json,sys; print(json.load(open(sys.argv[1]))["version"])' "$ROOT_DIR/mygo.json"
}

# load_update_key exports MYGO_UPDATER_PRIVATE_KEY, which signs updates, from
# the file `go tool mygo keygen` wrote unless it is set (YEEK_UPDATE_KEY
# names another file). With "required" it fails without one; otherwise it
# warns, and MyGo builds without update archives.
load_update_key() {
	[[ -n "${MYGO_UPDATER_PRIVATE_KEY:-}" ]] && { export MYGO_UPDATER_PRIVATE_KEY; return; }
	local file="${YEEK_UPDATE_KEY:-$HOME/Library/Application Support/mygo/update-keys/mygo-update.key}"
	if [[ -f "$file" ]]; then
		MYGO_UPDATER_PRIVATE_KEY="$(cat "$file")"
		export MYGO_UPDATER_PRIVATE_KEY
	elif [[ "${1:-}" == required ]]; then
		die "no update signing key: set MYGO_UPDATER_PRIVATE_KEY, or YEEK_UPDATE_KEY to the key file"
	else
		printf 'warning: no update signing key; building without update archives\n' >&2
	fi
}

# stage_config gives the build its version ($1), its changelog ($2, may be
# empty) and its notarytool keychain profile ($3, may be empty) in mygo.json
# for this run, and restores the committed file however the script ends.
stage_config() {
	STAGED_CONFIG_BACKUP="$(mktemp)"
	cp "$ROOT_DIR/mygo.json" "$STAGED_CONFIG_BACKUP"
	trap 'cp "$STAGED_CONFIG_BACKUP" "$ROOT_DIR/mygo.json"; rm -f "$STAGED_CONFIG_BACKUP"' EXIT
	python3 -I - "$ROOT_DIR/mygo.json" "$1" "$2" "$3" <<'EOF'
import json, sys
path, version, changelog, profile = sys.argv[1:]
with open(path) as f:
    config = json.load(f)
config["version"] = version
if changelog:
    config["updates"]["changelog"] = changelog
if profile:
    config.setdefault("macos", {})["notarize"] = {"keychainProfile": profile}
with open(path, "w") as f:
    json.dump(config, f, indent=2)
    f.write("\n")
EOF
}
