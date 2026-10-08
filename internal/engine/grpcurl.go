package engine

import (
	"context"
	"encoding/base64"
	"net/url"
	"path"
	"path/filepath"
	"strings"
)

// grpcurlNewline continues a grpcurl command on the next line.
const grpcurlNewline = "\\\n "

// Grpcurl is Yaak's Copy as gRPCurl: a grpcurl command for the gRPC
// request as it would be sent, with its proto files and import paths.
func (e *Engine) Grpcurl(ctx context.Context, id, environment string, protoFiles []string) (string, error) {
	model, err := e.Store.Get(ctx, id)
	if err != nil {
		return "", err
	}
	resolved, err := e.resolve(ctx, model, environment)
	if err != nil {
		return "", err
	}
	return grpcurl(resolved.Model, protoFiles), nil
}

func grpcurl(request Object, allProtoFiles []string) string {
	xs := []string{"grpcurl"}
	target := str(request, "url")
	if strings.HasPrefix(target, "http://") {
		xs = append(xs, "-plaintext")
	}
	var includes, protos []string
	for _, f := range allProtoFiles {
		if strings.HasSuffix(f, ".proto") {
			protos = append(protos, f)
		} else {
			includes = append(includes, f)
		}
	}
	var inferred []string
	infer := func(dir string) {
		for _, seen := range inferred {
			if seen == dir {
				return
			}
		}
		inferred = append(inferred, dir)
	}
	for _, f := range protos {
		if dir := parentProtoDir(f); dir != "" {
			infer(dir)
		} else {
			infer(path.Join(f, ".."))
			infer(path.Join(f, "..", ".."))
		}
	}
	for _, f := range append(includes, inferred...) {
		xs = append(xs, "-import-path", shellQuote(f), grpcurlNewline)
	}
	for _, f := range protos {
		xs = append(xs, "-proto", shellQuote(f), grpcurlNewline)
	}
	for _, h := range objects(array(request, "headers")) {
		if enabled(h) && str(h, "name") != "" {
			xs = append(xs, "-H", shellQuote(str(h, "name")+": "+str(h, "value")), grpcurlNewline)
		}
	}
	auth := obj(request, "authentication")
	if !boolean(auth, "disabled") {
		switch str(request, "authenticationType") {
		case "basic":
			encoded := base64.StdEncoding.EncodeToString([]byte(str(auth, "username") + ":" + str(auth, "password")))
			xs = append(xs, "-H", shellQuote("Authorization: Basic "+encoded), grpcurlNewline)
		case "bearer":
			xs = append(xs, "-H", shellQuote("Authorization: Bearer "+str(auth, "token")), grpcurlNewline)
		case "apikey":
			if str(auth, "location") == "query" {
				sep := "?"
				if strings.Contains(target, "?") {
					sep = "&"
				}
				key := str(auth, "key")
				if key == "" {
					key = "token"
				}
				target += sep + encodeURIComponent(key) + "=" + encodeURIComponent(str(auth, "value"))
			} else {
				key := str(auth, "key")
				if key == "" {
					key = "X-Api-Key"
				}
				xs = append(xs, "-H", shellQuote(key+": "+str(auth, "value")))
			}
			xs = append(xs, grpcurlNewline)
		}
	}
	if message := str(request, "message"); message != "" {
		xs = append(xs, "-d", shellQuote(message), grpcurlNewline)
	}
	if target != "" {
		server := strings.TrimPrefix(strings.TrimPrefix(target, "http://"), "https://")
		xs = append(xs, server, grpcurlNewline)
	}
	if str(request, "service") != "" && str(request, "method") != "" {
		xs = append(xs, str(request, "service")+"/"+str(request, "method"), grpcurlNewline)
	}
	if xs[len(xs)-1] == grpcurlNewline {
		xs = xs[:len(xs)-1]
	}
	return strings.Join(xs, " ")
}

// parentProtoDir is the nearest directory named proto around a file.
func parentProtoDir(file string) string {
	dir, err := filepath.Abs(file)
	if err != nil {
		return ""
	}
	for {
		if filepath.Base(dir) == "proto" {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}

// encodeURIComponent is JavaScript's, which Yaak's plugins use.
func encodeURIComponent(s string) string {
	return strings.NewReplacer("+", "%20", "%21", "!", "%27", "'", "%28", "(", "%29", ")", "%2A", "*").Replace(url.QueryEscape(s))
}
