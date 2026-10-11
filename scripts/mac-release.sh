#!/usr/bin/env bash
# Builds Yeek's macOS release disk images, signed with a Developer ID and
# notarized: one for Apple silicon, one for Intel and a universal one.
#
#   scripts/mac-release.sh                  # the version in mygo.json
#   scripts/mac-release.sh 0.2.0            # another version
#   scripts/mac-release.sh --arch arm64     # only some: arm64, x64, universal
#   scripts/mac-release.sh --skip-notarize  # signed, not notarized (faster)
#
# The disk images are copied to build/mac-release/dist as
# Yeek-<version>-{arm64,x64,universal}.dmg. With the update signing key
# (MYGO_UPDATER_PRIVATE_KEY, or the file `go tool mygo keygen` wrote, which
# YEEK_UPDATE_KEY overrides), each build also gets its signed update archive
# and manifest.
#
# Needs DEVELOPER_ID_APPLICATION, the signing identity, and a notarytool
# keychain profile: YEEK_NOTARY_PROFILE, "Yeek" by default, made with
#   xcrun notarytool store-credentials Yeek --apple-id <id> --team-id <team>
# scripts/publish.sh runs this with --changelog, -o and --upload.
set -euo pipefail
source "$(dirname "${BASH_SOURCE[0]}")/lib/release.sh"
cd "$ROOT_DIR"

VERSION=""
ARCHES=()
NOTARIZE=true
UPLOAD=false
CHANGELOG=""
OUT_DIR="$ROOT_DIR/build/mac-release"
while (($#)); do
	case "$1" in
	--arch)
		[[ $# -ge 2 ]] || die "--arch needs arm64, x64 or universal"
		ARCHES+=("$2")
		shift
		;;
	--skip-notarize) NOTARIZE=false ;;
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
		sed -n '2,20p' "$0" | sed 's/^# \{0,1\}//'
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

[[ "$(uname -s)" == "Darwin" ]] || die "macOS releases are built on macOS"
for tool in go python3 xcrun; do
	command -v "$tool" >/dev/null 2>&1 || die "needs $tool"
done
[[ -n "$VERSION" ]] || VERSION="$(config_version)"
check_version "$VERSION"

((${#ARCHES[@]})) || ARCHES=(arm64 x64 universal)
PLATFORMS=()
for arch in "${ARCHES[@]}"; do
	case "$arch" in
	arm64) PLATFORMS+=(darwin/arm64) ;;
	x64 | amd64) PLATFORMS+=(darwin/amd64) ;;
	universal) PLATFORMS+=(darwin/universal) ;;
	*) die "unknown --arch $arch: arm64, x64 or universal" ;;
	esac
done

[[ -n "${DEVELOPER_ID_APPLICATION:-}" ]] || die "set DEVELOPER_ID_APPLICATION to the Developer ID Application identity"
security find-identity -v -p codesigning | grep -qF "\"$DEVELOPER_ID_APPLICATION\"" ||
	die "no signing identity \"$DEVELOPER_ID_APPLICATION\" in the keychain"
NOTARY_PROFILE=""
if $NOTARIZE; then
	NOTARY_PROFILE="${YEEK_NOTARY_PROFILE:-Yeek}"
	xcrun notarytool history --keychain-profile "$NOTARY_PROFILE" >/dev/null 2>&1 ||
		die "the notarytool keychain profile \"$NOTARY_PROFILE\" doesn't work; make it with xcrun notarytool store-credentials"
fi
if $UPLOAD; then load_update_key required; else load_update_key; fi

# Notarization stays out of the committed mygo.json: it fails ad hoc builds.
stage_config "$VERSION" "$CHANGELOG" "$NOTARY_PROFILE"

BUILD_ARGS=(-platform "$(
	IFS=,
	echo "${PLATFORMS[*]}"
)" -o "$OUT_DIR" -sign "$DEVELOPER_ID_APPLICATION")
$NOTARIZE || BUILD_ARGS+=(-skip-notarize)
$UPLOAD && BUILD_ARGS+=(-upload)

info "building Yeek $VERSION for macOS ${ARCHES[*]}"
# cgo: the 1Password SDK signs in through the desktop app with it.
CGO_ENABLED=1 go tool mygo build "${BUILD_ARGS[@]}"

# MyGo names disk images by its platforms; dist names them by the usual ones.
DIST="$OUT_DIR/dist"
mkdir -p "$DIST"
for arch in "${ARCHES[@]}"; do
	case "$arch" in
	arm64) dmg="$OUT_DIR/darwin-arm64/Yeek $VERSION arm64.dmg" ;;
	x64 | amd64) dmg="$OUT_DIR/darwin-amd64/Yeek $VERSION amd64.dmg" arch=x64 ;;
	universal) dmg="$OUT_DIR/darwin-universal/Yeek $VERSION.dmg" ;;
	esac
	[[ -f "$dmg" ]] || die "expected $dmg after the build"
	if $NOTARIZE; then
		xcrun stapler validate "$dmg" >/dev/null || die "$dmg is not notarized"
	fi
	cp "$dmg" "$DIST/Yeek-$VERSION-$arch.dmg"
	info "$DIST/Yeek-$VERSION-$arch.dmg"
done
