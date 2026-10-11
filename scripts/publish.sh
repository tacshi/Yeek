#!/usr/bin/env bash
# Publishes a Yeek release to GitHub, where installed apps find their
# updates: the macOS disk images that scripts/mac-release.sh builds (signed
# and notarized) and the Windows installers of scripts/windows-release.sh.
#
#   scripts/publish.sh 0.2.0                 # build, review the notes, publish
#   scripts/publish.sh 0.2.0 --no-upload     # build into build/release only
#   scripts/publish.sh 0.2.0 --skip-windows  # macOS only (or --skip-mac)
#   scripts/publish.sh 0.2.0 --allow-branch  # release from a branch, not main
#
# Release notes come from release-notes/Yeek-<version>.md, used verbatim when
# it exists. Otherwise they are drafted from the commits since the last v*
# tag and shown for review: [y]es publishes them, [e]dit opens them in
# $EDITOR and shows them again, anything else aborts.
#
# The update signing key is MYGO_UPDATER_PRIVATE_KEY, or the file that
# `go tool mygo keygen` wrote (YEEK_UPDATE_KEY names another); signing and
# notarization are as in the build scripts. The committed mygo.json is never
# edited: the version and notes are given to the builds for the run only, so
# a release needs no preparatory commit.
set -euo pipefail
source "$(dirname "${BASH_SOURCE[0]}")/lib/release.sh"
cd "$ROOT_DIR"

VERSION=""
NO_UPLOAD=false
ALLOW_BRANCH=false
MAC=true
WINDOWS=true
for arg in "$@"; do
	case "$arg" in
	--no-upload) NO_UPLOAD=true ;;
	--allow-branch) ALLOW_BRANCH=true ;;
	--skip-mac) MAC=false ;;
	--skip-windows) WINDOWS=false ;;
	-h | --help)
		sed -n '2,20p' "$0" | sed 's/^# \{0,1\}//'
		exit 0
		;;
	-*) die "unknown option: $arg" ;;
	*)
		[[ -z "$VERSION" ]] || die "one version, please: $VERSION or $arg"
		VERSION="${arg#v}"
		;;
	esac
done
[[ -n "$VERSION" ]] || die "usage: scripts/publish.sh <version> [--no-upload] [--skip-mac] [--skip-windows] [--allow-branch]"
check_version "$VERSION"
$MAC || $WINDOWS || die "--skip-mac and --skip-windows leave nothing to publish"
for tool in go git python3; do
	command -v "$tool" >/dev/null 2>&1 || die "needs $tool"
done
$NO_UPLOAD || command -v gh >/dev/null 2>&1 || die "needs the GitHub CLI (gh) to upload"
# What the builds need, checked before the notes are reviewed.
if $MAC; then
	[[ "$(uname -s)" == "Darwin" ]] || die "the macOS build needs macOS (--skip-mac to leave it out)"
	[[ -n "${DEVELOPER_ID_APPLICATION:-}" ]] || die "set DEVELOPER_ID_APPLICATION for the macOS build"
fi
if $WINDOWS; then
	command -v makensis >/dev/null 2>&1 || die "the Windows build needs NSIS: brew install makensis (or --skip-windows)"
fi

TAG="v$VERSION"
REPO="$(python3 -I -c 'import json,sys; print(json.load(open(sys.argv[1]))["updates"]["github"])' mygo.json)"
STAGE_DIR="$ROOT_DIR/build/release"

# ------------------------------------------------------------------- checks

BRANCH="$(git branch --show-current)"
if [[ "$BRANCH" != "main" ]] && ! $ALLOW_BRANCH; then
	die "releases are cut from main, and this is ${BRANCH:-a detached HEAD} (--allow-branch to override)"
fi
[[ -z "$(git status --porcelain)" ]] || die "the working tree has changes; commit or stash them first"
if git rev-parse -q --verify "refs/tags/$TAG" >/dev/null; then
	die "the tag $TAG already exists"
fi
if ! $NO_UPLOAD; then
	git fetch -q origin "$BRANCH" --tags
	[[ "$(git rev-parse HEAD)" == "$(git rev-parse "origin/$BRANCH")" ]] ||
		die "HEAD is not what origin/$BRANCH is; push or pull first, so the tag names a published commit"
	git rev-parse -q --verify "refs/tags/$TAG" >/dev/null && die "the tag $TAG already exists on origin"
	if [[ "$(gh release view "$TAG" --repo "$REPO" --json isDraft --jq .isDraft 2>/dev/null)" == "false" ]]; then
		die "the release $TAG of $REPO is already published"
	fi
fi

load_update_key required

# ------------------------------------------------------------------- notes

# These notes are PUBLISHED, to the release page of a public repository, so
# drafts from commit messages are always reviewed before an upload.
NOTES_SRC="$ROOT_DIR/release-notes/Yeek-$VERSION.md"
NOTES_FILE="$STAGE_DIR/notes.md"
rm -rf "$STAGE_DIR"
mkdir -p "$STAGE_DIR"

if [[ -f "$NOTES_SRC" ]]; then
	cp "$NOTES_SRC" "$NOTES_FILE"
	info "notes:    release-notes/Yeek-$VERSION.md (hand-written)"
else
	PREV_TAG="$(git describe --tags --abbrev=0 --match 'v*' 2>/dev/null || true)"
	if [[ -n "$PREV_TAG" ]]; then
		LOG_ARGS=("$PREV_TAG..HEAD")
		info "notes:    drafted from $PREV_TAG..HEAD"
	else
		# The first release: capped, so the whole history doesn't become notes.
		LOG_ARGS=(-n 30 HEAD)
		info "notes:    drafted from the last 30 commits (no previous tag)"
	fi

	# One line per change, not per commit: a squash merge's subject is only
	# its PR title, and its changes are the "- " or "* " bullets of its body
	# (whose wrapped lines are joined). Without bullets, the subject is it.
	collect_changes() {
		local sha bullets
		for sha in $(git log --no-merges --pretty=tformat:'%H' "${LOG_ARGS[@]}"); do
			bullets="$(git log -1 --pretty=tformat:'%b' "$sha" | awk '
				/^[-*][ \t]+/ { if (item != "") print item; sub(/^[-*][ \t]+/, ""); item = $0; next }
				/^[ \t]+[^ \t]/ && item != "" { sub(/^[ \t]+/, ""); item = item " " $0; next }
				{ if (item != "") print item; item = "" }
				END { if (item != "") print item }')"
			if [[ -n "$bullets" ]]; then
				printf '%s\n' "$bullets"
			else
				git log -1 --pretty=tformat:'%s' "$sha"
			fi
		done
	}

	collect_changes |
		grep -viE '^(chore|ci|build|test|style|refactor|docs)(\([^)]*\))?!?:' |
		grep -viE '^(release|bump|merge|wip)\b' |
		sed -E 's/^[a-zA-Z]+(\([^)]*\))?!?: *//' |
		sed -E 's/^/- /' \
			>"$NOTES_FILE" || true

	if ! grep -qE '[^[:space:]]' "$NOTES_FILE"; then
		printf 'warning: no user-facing changes since %s; the notes are empty\n' "${PREV_TAG:-the start}" >&2
	fi

	if ! $NO_UPLOAD && [[ ! -t 0 ]]; then
		die "drafted release notes need an interactive review before upload.
Write the public notes, then run again:
  release-notes/Yeek-$VERSION.md
It may be empty when this release has no summary on purpose."
	fi
	if ! $NO_UPLOAD; then
		while :; do
			printf '\n--- release notes for %s (%s) ---\n' "$VERSION" "$NOTES_FILE"
			if grep -qE '[^[:space:]]' "$NOTES_FILE"; then
				cat "$NOTES_FILE"
			else
				printf '(no public summary)\n'
			fi
			printf -- '---\n\nPublish these notes? [y]es / [e]dit / [N]o: '
			read -r reply
			case "$reply" in
			y | Y) break ;;
			e | E) "${VISUAL:-${EDITOR:-vi}}" "$NOTES_FILE" ;;
			*) die "aborted.
To keep this draft and edit it outside a release run:
  mkdir -p release-notes && cp \"$NOTES_FILE\" release-notes/Yeek-$VERSION.md
then edit that file and run again: it is used verbatim." ;;
			esac
		done
	fi
fi

# ------------------------------------------------------------------- build

# MyGo takes the "## <version>" section of a changelog as the notes. Each
# build uploads to the same draft release, which the first one creates.
BUILD_ARGS=("$VERSION" -o "$STAGE_DIR/out")
if grep -qE '[^[:space:]]' "$NOTES_FILE"; then
	printf '## %s\n\n%s\n' "$VERSION" "$(cat "$NOTES_FILE")" >"$STAGE_DIR/CHANGELOG.md"
	BUILD_ARGS+=(--changelog "build/release/CHANGELOG.md")
fi
$NO_UPLOAD || BUILD_ARGS+=(--upload)

info "building Yeek $VERSION at $(git rev-parse --short HEAD)"
$MAC && "$ROOT_DIR/scripts/mac-release.sh" "${BUILD_ARGS[@]}"
$WINDOWS && "$ROOT_DIR/scripts/windows-release.sh" "${BUILD_ARGS[@]}"

if $NO_UPLOAD; then
	info "built into build/release/out (installers in build/release/out/dist); nothing was published"
	exit 0
fi

# ------------------------------------------------------------------- publish

# The assets are on the draft before the release is published, so apps never
# read a manifest whose archive is missing.
if grep -qE '[^[:space:]]' "$NOTES_FILE"; then
	git tag -a "$TAG" -F "$NOTES_FILE"
else
	git tag -a "$TAG" -m "Yeek $VERSION"
fi
git push origin "$TAG"
gh release edit "$TAG" --repo "$REPO" --draft=false --latest --title "Yeek $VERSION" --notes-file "$NOTES_FILE"
info "published https://github.com/$REPO/releases/tag/$TAG"
