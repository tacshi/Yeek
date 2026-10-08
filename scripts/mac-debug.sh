#!/usr/bin/env bash
# Builds "Yeek Debug.app", a macOS debug build that runs beside a release Yeek:
# it keeps MyGo's development features (such as the web inspector), has its own
# name, bundle identifier, data directory and single-instance lock, and wears
# the debug icon (the Yeek mark in pink on black).
#
#   scripts/mac-debug.sh          # build into build/debug
#   scripts/mac-debug.sh --open   # build, then launch it
set -euo pipefail

if [[ "$(uname -s)" != "Darwin" ]]; then
	echo "mac-debug.sh builds a macOS app; run it on macOS." >&2
	exit 1
fi
for tool in go iconutil sips codesign /usr/libexec/PlistBuddy; do
	if ! command -v "$tool" >/dev/null 2>&1; then
		echo "mac-debug.sh needs $tool." >&2
		exit 1
	fi
done

open_app=false
for arg in "$@"; do
	case "$arg" in
	--open) open_app=true ;;
	-h | --help)
		sed -n '2,9p' "$0" | sed 's/^# \{0,1\}//'
		exit 0
		;;
	*)
		echo "Unknown option: $arg" >&2
		exit 1
		;;
	esac
done

root="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$root"

name="Yeek Debug"
identifier="app.yeek.desktop.debug"
case "$(uname -m)" in
arm64) arch=arm64 ;;
x86_64) arch=amd64 ;;
*)
	echo "Unsupported architecture: $(uname -m)" >&2
	exit 1
	;;
esac
out="build/debug"
built="$out/darwin-$arch/Yeek.app"
app="$out/$name.app"

echo "==> Building debug app for darwin/$arch"
go tool mygo build -debug -skip-dmg -platform "darwin/$arch" -o "$out"
if [[ ! -d "$built" ]]; then
	echo "Expected $built after the build." >&2
	exit 1
fi

work="$(mktemp -d)"
trap 'rm -rf "$work"' EXIT

echo "==> Drawing the debug icon"
go run ./cmd/icon -variant debug -o "$work/icon.png"
iconset="$work/AppIcon.iconset"
mkdir "$iconset"
for size in 16 32 128 256 512; do
	sips -z "$size" "$size" "$work/icon.png" --out "$iconset/icon_${size}x${size}.png" >/dev/null
	double=$((size * 2))
	sips -z "$double" "$double" "$work/icon.png" --out "$iconset/icon_${size}x${size}@2x.png" >/dev/null
done

echo "==> Packaging $name.app"
rm -rf "$app"
mv "$built" "$app"
plist="$app/Contents/Info.plist"
icon_file="$(/usr/libexec/PlistBuddy -c 'Print :CFBundleIconFile' "$plist" 2>/dev/null || echo AppIcon)"
icon_file="${icon_file%.icns}"
iconutil -c icns "$iconset" -o "$app/Contents/Resources/$icon_file.icns"
cp "$work/icon.png" "$app/Contents/Resources/icon.png"
set_plist() {
	/usr/libexec/PlistBuddy -c "Set :$1 $2" "$plist" 2>/dev/null ||
		/usr/libexec/PlistBuddy -c "Add :$1 string $2" "$plist"
}
set_plist CFBundleName "$name"
set_plist CFBundleDisplayName "$name"
set_plist CFBundleIdentifier "$identifier"

echo "==> Signing (ad hoc)"
codesign --force --deep --sign - "$app"
codesign --verify --deep --strict "$app"

echo "Built $root/$app"
if $open_app; then
	open "$app"
fi
