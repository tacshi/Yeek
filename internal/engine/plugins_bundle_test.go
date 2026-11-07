package engine

//go:generate go run github.com/evanw/esbuild/cmd/esbuild@v0.25.5 testdata/plugin-bundle/src/index.ts --bundle --format=esm --platform=neutral --outfile=testdata/plugin-bundle/plugin.js
//go:generate go run github.com/evanw/esbuild/cmd/esbuild@v0.25.5 testdata/plugin-bundle/src/index.ts --bundle --format=esm --platform=neutral --minify --outfile=testdata/plugin-bundle/plugin.min.js
//go:generate go run github.com/evanw/esbuild/cmd/esbuild@v0.25.5 testdata/plugin-bundle/src/index.ts --bundle --format=esm --platform=neutral --target=es2017 --supported:bigint=true --outfile=testdata/plugin-bundle/plugin.es2017.js

import (
	"io"
	"net/http"
	"path/filepath"
	"strings"
	"testing"
)

func TestBundledPlugins(t *testing.T) {
	for _, name := range []string{"plugin.js", "plugin.min.js", "plugin.es2017.js"} {
		t.Run(name, func(t *testing.T) {
			e := testEngine(t)
			if _, err := e.InstallPlugin(t.Context(), filepath.Join("testdata", "plugin-bundle", name)); err != nil {
				t.Fatal(err)
			}
			template := func(function string, args map[string]string) string {
				t.Helper()
				got, err := e.pluginTemplate(t.Context(), function, args, "")
				if err != nil {
					t.Fatal(function, err)
				}
				return got
			}
			rows := `[{"name":"a","tags":["x","y"],"size":5},{"name":"b","tags":["y"],"size":20},{"name":"c"}]`
			if got := template("bundled.summary", map[string]string{"rows": rows}); got != `{"tags":["x","y"],"total":25,"counts":{"small":2,"big":1},"big":"18446744073709551616","day":"2026-11-08","version":"2"}` {
				t.Fatal(got)
			}
			header := `<https://api.test/items?page=2>; rel="next", <https://api.test/items?page=9>; rel="last"`
			if got := template("bundled.links", map[string]string{"header": header}); got != `{"next":"2","last":"9"}` {
				t.Fatal(got)
			}
			if got := template("bundled.stream", nil); got != "ABC:0:0" {
				t.Fatal(got)
			}
			for _, tc := range []struct {
				args Object
				want string
			}{{Object{"token": "tk"}, "Bearer tk"}, {Object{"user": "u", "password": "p"}, "Basic dTpw"}} {
				args, want := tc.args, tc.want
				request, err := http.NewRequest("POST", "https://api.test", strings.NewReader("hi {{user}}"))
				if err != nil {
					t.Fatal(err)
				}
				if handled, err := e.pluginAuth(t.Context(), "bundled.auth", args, request, ""); !handled || err != nil {
					t.Fatal(handled, err)
				}
				body, _ := io.ReadAll(request.Body)
				if request.Header.Get("Authorization") != want || string(body) != "hi "+str(args, "user") {
					t.Fatal(request.Header, string(body))
				}
			}
			if got := template("bundled.stream", nil); got != "ABC:0:2" {
				t.Fatal(got)
			}
		})
	}
}
