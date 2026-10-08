package engine

import (
	"compress/gzip"
	"context"
	"encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

const maxImportBytes = 64 << 20

func readImportFile(path string) ([]byte, error) {
	file, err := os.Open(path) // #nosec G304 -- the user chooses an import source or a file inside its collection.
	if err != nil {
		return nil, err
	}
	defer func() { _ = file.Close() }()
	info, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, errors.New("import source must be a regular file")
	}
	data, err := io.ReadAll(io.LimitReader(file, maxImportBytes+1))
	if err != nil {
		return nil, err
	}
	if len(data) > maxImportBytes {
		return nil, errors.New("import source exceeds 64 MiB")
	}
	return data, nil
}
func (e *Engine) downloadImport(ctx context.Context, raw, workspace string) ([]byte, string, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Hostname() == "" || u.Scheme != "http" && u.Scheme != "https" {
		return nil, "", errors.New("collection URL must use HTTP or HTTPS")
	}
	w := Object{"settingValidateCertificates": true, "settingHttpVersion": "auto"}
	if workspace != "" {
		w, err = e.Store.Get(ctx, workspace)
		if err != nil {
			return nil, "", err
		}
	}
	transport, err := e.transport(ctx, resolvedRequest{Model: Object{"url": u.String()}, Workspace: w, Settings: w})
	if err != nil {
		return nil, "", err
	}
	defer transport.CloseIdleConnections()
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return nil, "", err
	}
	request.Header.Set("Accept-Encoding", "gzip")
	response, err := (&http.Client{Transport: transport}).Do(request)
	if err != nil {
		return nil, "", err
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return nil, "", fmt.Errorf("collection download returned %s", response.Status)
	}
	var reader io.Reader = response.Body
	if strings.EqualFold(response.Header.Get("Content-Encoding"), "gzip") {
		decoded, err := gzip.NewReader(response.Body)
		if err != nil {
			return nil, "", fmt.Errorf("collection compression: %w", err)
		}
		defer func() { _ = decoded.Close() }()
		reader = decoded
	}
	data, err := io.ReadAll(io.LimitReader(reader, maxImportBytes+1))
	if err != nil {
		return nil, "", err
	}
	if len(data) > maxImportBytes {
		return nil, "", errors.New("import source exceeds 64 MiB")
	}
	return data, response.Request.URL.String(), nil
}
func importFormat(data []byte) string {
	text := strings.TrimSpace(string(data))
	if strings.HasPrefix(text, "curl ") {
		return "cURL"
	}
	if strings.HasPrefix(text, "meta {") || strings.HasPrefix(text, "meta{") || strings.HasPrefix(text, "vars {") {
		return "Bruno"
	}
	var root Object
	if json.Unmarshal(data, &root) != nil {
		_ = yaml.Unmarshal(data, &root)
	}
	switch {
	case len(obj(root, "resources")) > 0:
		return "Yaak"
	case root["item"] != nil || root["_postman_variable_scope"] != nil:
		return "Postman"
	case root["openapi"] != nil:
		return "OpenAPI"
	case root["swagger"] != nil:
		return "Swagger"
	case str(root, "_type") == "export":
		return "Insomnia"
	default:
		return "Collection"
	}
}
func (e *Engine) ReadImport(ctx context.Context, input ImportInput, workspace string) (ImportDocument, error) {
	origin, kind := strings.TrimSpace(input.Origin), input.Kind
	if kind == "" {
		kind = "file"
		if strings.Contains(origin, "://") {
			kind = "url"
		}
	}
	var data []byte
	var err error
	label := "Pasted collection"
	referenceOrigin := ""
	switch kind {
	case "text":
		data = []byte(input.Content)
		origin = ""
	case "url":
		if !strings.Contains(origin, "://") {
			origin = "https://" + origin
		}
		data, referenceOrigin, err = e.downloadImport(ctx, origin, workspace)
		label = origin
	case "file", "folder":
		origin, err = filepath.Abs(origin)
		if err == nil {
			origin, err = filepath.EvalSymlinks(origin)
		}
		if err != nil {
			return ImportDocument{}, err
		}
		info, statErr := os.Stat(origin)
		if statErr != nil {
			return ImportDocument{}, statErr
		}
		label = filepath.Base(origin)
		if info.IsDir() {
			models, warnings, err := readBrunoDirectory(ctx, origin)
			return ImportDocument{Format: "Bruno", Origin: origin, Label: label, Models: models, Warnings: warnings}, err
		}
		referenceOrigin = origin
		data, err = readImportFile(origin)
	default:
		return ImportDocument{}, errors.New("choose a file, folder, URL, or text source")
	}
	if err != nil {
		return ImportDocument{}, err
	}
	if len(data) > maxImportBytes {
		return ImportDocument{}, errors.New("import source exceeds 64 MiB")
	}
	format := importFormat(data)
	if format == "OpenAPI" || format == "Swagger" {
		data, err = e.resolveImportReferences(ctx, data, referenceOrigin, workspace)
		if err != nil {
			return ImportDocument{}, err
		}
	}
	var models []Object
	if format == "OpenAPI" || format == "Swagger" {
		var root Object
		if err = json.Unmarshal(data, &root); err == nil {
			models, err = parseOpenAPIAt(root, referenceOrigin)
		}
	} else {
		models, err = ParseImport(data)
	}
	if err != nil {
		if pluginModels, pluginErr := e.pluginImport(ctx, string(data)); pluginErr == nil {
			models = pluginModels
			format = "Go plugin"
		} else {
			return ImportDocument{}, err
		}
	}
	if len(models) == 0 {
		return ImportDocument{}, errors.New("collection contains no supported resources")
	}
	if sourceID := input.SourceWorkspaceID; sourceID != "" {
		if !slices.ContainsFunc(models, func(m Object) bool { return str(m, "model") == "workspace" && str(m, "id") == sourceID }) {
			return ImportDocument{}, errors.New("the source no longer contains its linked workspace")
		}
		models = slices.DeleteFunc(models, func(m Object) bool { return str(m, "id") != sourceID && str(m, "workspaceId") != sourceID })
	}
	warnings := []ImportWarning{}
	for _, model := range models {
		if notes, ok := model["importWarnings"].([]string); ok {
			for _, note := range notes {
				warnings = append(warnings, ImportWarning{Title: "Import settings", Detail: note})
			}
		}
		if len(array(model, "importScripts")) > 0 {
			warnings = append(warnings, ImportWarning{Title: "Scripts retained as source metadata", Detail: "Imported JavaScript scripts are not executed by the Go request engine."})
			break
		}
	}
	return ImportDocument{Format: format, Origin: origin, Label: label, Models: models, Warnings: warnings, SourceWorkspaceID: input.SourceWorkspaceID}, nil
}
func stableImportID(kind, key string) string {
	return "source_" + prefixes[kind] + importHash(key)[:24]
}

func readBrunoDirectory(ctx context.Context, root string) ([]Object, []ImportWarning, error) {
	files := []string{}
	var total int64
	err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err != nil {
			return err
		}
		if entry.IsDir() {
			if path != root && (strings.HasPrefix(entry.Name(), ".") || entry.Name() == "node_modules") {
				return filepath.SkipDir
			}
			return nil
		}
		if entry.Type()&os.ModeSymlink != 0 {
			return nil
		}
		if !strings.EqualFold(filepath.Ext(path), ".bru") {
			return nil
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		total += info.Size()
		if total > maxImportBytes || len(files) >= 10000 {
			return errors.New("collection exceeds the Bruno import limit")
		}
		files = append(files, path)
		return nil
	})
	if err != nil {
		return nil, nil, err
	}
	if len(files) == 0 {
		return nil, nil, errors.New("folder contains no Bruno .bru files")
	}
	slices.Sort(files)
	wid := "bruno_workspace"
	workspace := Object{"model": "workspace", "id": wid, "name": filepath.Base(root)}
	if data, err := readImportFile(filepath.Join(root, "bruno.json")); err == nil {
		var config Object
		if json.Unmarshal(data, &config) == nil && str(config, "name") != "" {
			workspace["name"] = str(config, "name")
		}
	}
	models := []Object{workspace}
	folders := map[string]string{}
	warnings := []ImportWarning{}
	var folderFor func(string) string
	folderFor = func(path string) string {
		if path == "." || path == "" {
			return ""
		}
		if id := folders[path]; id != "" {
			return id
		}
		parent := folderFor(filepath.Dir(path))
		id := stableImportID("folder", filepath.ToSlash(path))
		folders[path] = id
		models = append(models, Object{"model": "folder", "id": id, "workspaceId": wid, "folderId": nilIfBlank(parent), "name": filepath.Base(path)})
		return id
	}
	for _, path := range files {
		if ctx.Err() != nil {
			return nil, nil, ctx.Err()
		}
		rel, _ := filepath.Rel(root, path)
		data, err := readImportFile(path)
		if err != nil {
			return nil, nil, err
		}
		blocks, err := brunoBlocks(string(data))
		if err != nil {
			return nil, nil, fmt.Errorf("%s: %w", rel, err)
		}
		if filepath.Base(path) == "folder.bru" || filepath.Base(path) == "collection.bru" {
			target := workspace
			if filepath.Base(path) == "folder.bru" {
				id := folderFor(filepath.Dir(rel))
				for _, m := range models {
					if str(m, "id") == id {
						target = m
						break
					}
				}
			}
			if name := str(brunoFields(blocks["meta"]), "name"); name != "" {
				target["name"] = name
			}
			target["headers"] = brunoPairs(blocks["headers"])
			for block, kind := range map[string]string{"auth:basic": "basic", "auth:bearer": "bearer", "auth:apikey": "apikey", "auth:oauth2": "oauth2"} {
				if value := blocks[block]; value != "" {
					target["authenticationType"] = kind
					target["authentication"] = brunoFields(value)
				}
			}
			if vars := brunoPairs(blocks["vars"]); len(vars) > 0 {
				parent, kind := wid, "workspace"
				if str(target, "model") == "folder" {
					parent, kind = str(target, "id"), "folder"
				}
				models = append(models, Object{"model": "environment", "id": stableImportID("environment", filepath.ToSlash(rel)), "workspaceId": wid, "parentModel": kind, "parentId": parent, "name": "Global Variables", "variables": vars})
			}
			continue
		}
		parsed, err := parseBruno(string(data))
		if err != nil {
			return nil, nil, fmt.Errorf("%s: %w", rel, err)
		}
		for _, m := range parsed {
			kind := str(m, "model")
			if kind == "workspace" {
				continue
			}
			m["workspaceId"] = wid
			m["id"] = stableImportID(kind, filepath.ToSlash(rel)+":"+kind)
			if kind == "environment" {
				m["parentModel"] = "environment"
				m["parentId"] = nil
				m["name"] = strings.TrimSuffix(filepath.Base(path), filepath.Ext(path))
			} else {
				m["folderId"] = nilIfBlank(folderFor(filepath.Dir(rel)))
			}
			models = append(models, m)
		}
		if blocks["script:pre-request"] != "" || blocks["script:post-response"] != "" {
			warnings = append(warnings, ImportWarning{Title: "Bruno scripts were not executed", Detail: rel})
		}
	}
	return models, warnings, nil
}
