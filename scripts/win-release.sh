#!/usr/bin/env bash
# Builds Yeek's Windows release installers on macOS or Linux, for x64 and
# arm64: per-user installers that let the app update itself.
#
#   scripts/win-release.sh               # the version in mygo.json
#   scripts/win-release.sh 0.2.0         # another version
#   scripts/win-release.sh --arch x64    # only some: x64, arm64
#
# The installers are copied to build/windows-release as
# Yeek-<version>-{x64,arm64}-setup.exe. With the update signing key
# (MYGO_UPDATER_PRIVATE_KEY, or the file `go tool mygo keygen` wrote, which
# YEEK_UPDATE_KEY overrides), each build also gets its signed update archive
# and manifest.
#
# Needs NSIS (brew install makensis). The installers are not code signed, so
# SmartScreen warns about them until Yeek has a Windows signing certificate.
# scripts/publish.sh runs this with --changelog, -o and --upload.
set -euo pipefail
source "$(dirname "${BASH_SOURCE[0]}")/lib/release.sh"
cd "$ROOT_DIR"

VERSION=""
ARCHES=()
UPLOAD=false
CHANGELOG=""
OUT_DIR="$ROOT_DIR/build/windows-release"
while (($#)); do
	case "$1" in
	--arch)
		[[ $# -ge 2 ]] || die "--arch needs x64 or arm64"
		ARCHES+=("$2")
		shift
		;;
	--upload) UPLOAD=true ;;
	--changelog)
		[[ $# -ge 2 ]] || die "--changelog needs a file"
		CHANGELOG="$2"
		shift
		;;
	-o)
		[[ $# -ge 2 ]] || die "-o needs a directory"
		OUT_DIR="$2"
		shift
		;;
	-h | --help)
		sed -n '2,17p' "$0" | sed 's/^# \{0,1\}//'
		exit 0
		;;
	-*) die "unknown option: $1" ;;
	*)
		[[ -z "$VERSION" ]] || die "one version, please: $VERSION or $1"
		VERSION="${1#v}"
		;;
	esac
	shift
done

for tool in go python3 makensis; do
	command -v "$tool" >/dev/null 2>&1 || die "needs $tool (NSIS: brew install makensis)"
done
[[ -n "$VERSION" ]] || VERSION="$(config_version)"
check_version "$VERSION"

((${#ARCHES[@]})) || ARCHES=(x64 arm64)
PLATFORMS=()
for arch in "${ARCHES[@]}"; do
	case "$arch" in
	x64 | amd64) PLATFORMS+=(windows/amd64) ;;
	arm64) PLATFORMS+=(windows/arm64) ;;
	*) die "unknown --arch $arch: x64 or arm64" ;;
	esac
done
if $UPLOAD; then load_update_key required; else load_update_key; fi
stage_config "$VERSION" "$CHANGELOG" ""

BUILD_ARGS=(-platform "$(
	IFS=,
	echo "${PLATFORMS[*]}"
)" -o "$OUT_DIR")
$UPLOAD && BUILD_ARGS+=(-upload)

info "building Yeek $VERSION for Windows ${ARCHES[*]}"
# No cgo: Go cross-compiles, and the 1Password SDK needs none on Windows.
CGO_ENABLED=0 go tool mygo build "${BUILD_ARGS[@]}"

# MyGo names installers by its platforms, in a folder each; the copies
# beside those folders are named by the usual ones.
for arch in "${ARCHES[@]}"; do
	case "$arch" in
	x64 | amd64) setup="$OUT_DIR/windows-amd64/Yeek Setup $VERSION amd64.exe" arch=x64 ;;
	arm64) setup="$OUT_DIR/windows-arm64/Yeek Setup $VERSION arm64.exe" ;;
	esac
	[[ -f "$setup" ]] || die "expected $setup after the build"
	cp "$setup" "$OUT_DIR/Yeek-$VERSION-$arch-setup.exe"
	info "$OUT_DIR/Yeek-$VERSION-$arch-setup.exe"
done
