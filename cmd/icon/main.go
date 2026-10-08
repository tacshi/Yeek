// Icon generates the app icon using MyGo's native vector renderer.
//
//	go run ./cmd/icon                                   # resources/icon.png
//	go run ./cmd/icon -variant debug -o build/icon.png  # the debug build's icon
package main

import (
	"flag"
	"image/png"
	"log"
	"os"
	"path/filepath"

	"github.com/egoist/mygo/yeekui"
)

// mark is Yeek's logo: a Y held in curly braces, on a 100×100 grid.
const mark = `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 100 100" fill="none" stroke="currentColor" stroke-linecap="round" stroke-linejoin="round">` +
	`<path stroke-width="8" d="M30 21c-7 0-9 3-9 9v9.5c0 5-2.6 8.2-7 10.5 4.4 2.3 7 5.5 7 10.5V70c0 6 2 9 9 9M70 21c7 0 9 3 9 9v9.5c0 5 2.6 8.2 7 10.5-4.4 2.3-7 5.5-7 10.5V70c0 6-2 9-9 9"/>` +
	`<path stroke-width="9" d="M50 51 38.5 34M50 51 61.5 34M50 51v16"/>` +
	`</svg>`

type variant struct{ from, to, mark ui.Color }

var variants = map[string]variant{
	"release": {ui.Hex("#b28cff"), ui.Hex("#5a36dc"), ui.Hex("#ffffff")},
	// Debug builds follow Yaak's convention: black, with a bright pink mark.
	"debug": {ui.Hex("#17151f"), ui.Hex("#050507"), ui.Hex("#ff6ad5")},
}

func main() {
	name := flag.String("variant", "release", "icon variant: release or debug")
	out := flag.String("o", filepath.Join("resources", "icon.png"), "output PNG path")
	flag.Parse()
	v, ok := variants[*name]
	if !ok {
		log.Fatalf("unknown variant %q", *name)
	}
	logo := ui.MustParseSVG([]byte(mark))
	// Apple's macOS icon grid: an 824-point rounded square centred on a
	// 1024-point canvas, leaving room for its shadow.
	img := ui.Render(func(c *ui.Context) {
		c.Root().Background(ui.Transparent)
		ui.Box(c).Fill().Padding(100).Children(func() {
			ui.Box(c).Fill().Radius(185).Gradient(v.from, v.to, 135).Shadow(0, 12, 28, 0, ui.RGBA(0, 0, 0, .28)).Center().Children(func() {
				ui.Icon(c, logo).FontSize(600).TextColor(v.mark)
			})
		})
	}, 1024, 1024, 1)
	if err := os.MkdirAll(filepath.Dir(*out), 0750); err != nil {
		log.Fatal(err)
	}
	file, err := os.Create(*out) // #nosec G304 -- the developer running the generator chooses the output path.
	if err != nil {
		log.Fatal(err)
	}
	if err = png.Encode(file, img); err != nil {
		_ = file.Close()
		log.Fatal(err)
	}
	if err = file.Close(); err != nil {
		log.Fatal(err)
	}
}
