package main

import (
	"runtime"

	"github.com/ebitengine/purego"
	"github.com/ebitengine/purego/objc"
)

// setProcessName gives an unbundled run, as `go run .`, the app's name in the
// macOS menu bar, which otherwise shows the executable's name. A packaged app
// already gets it from CFBundleName in its Info.plist. It must run before
// AppKit builds the menu bar.
func setProcessName(name string) {
	if _, err := purego.Dlopen("/System/Library/Frameworks/Foundation.framework/Foundation", purego.RTLD_GLOBAL|purego.RTLD_NOW); err != nil {
		return
	}
	bytes := append([]byte(name), 0)
	value := objc.ID(objc.GetClass("NSString")).Send(objc.RegisterName("stringWithUTF8String:"), &bytes[0])
	runtime.KeepAlive(bytes)
	if value == 0 {
		return
	}
	bundle := objc.ID(objc.GetClass("NSBundle")).Send(objc.RegisterName("mainBundle"))
	if bundle != 0 && bundle.Send(objc.RegisterName("bundleIdentifier")) == 0 {
		info := bundle.Send(objc.RegisterName("infoDictionary"))
		set := objc.RegisterName("setObject:forKey:")
		if info != 0 && info.Send(objc.RegisterName("respondsToSelector:"), set) != 0 {
			for _, key := range []string{"CFBundleName", "CFBundleDisplayName"} {
				keyBytes := append([]byte(key), 0)
				info.Send(set, value, objc.ID(objc.GetClass("NSString")).Send(objc.RegisterName("stringWithUTF8String:"), &keyBytes[0]))
				runtime.KeepAlive(keyBytes)
			}
		}
	}
	objc.ID(objc.GetClass("NSProcessInfo")).Send(objc.RegisterName("processInfo")).Send(objc.RegisterName("setProcessName:"), value)
}
