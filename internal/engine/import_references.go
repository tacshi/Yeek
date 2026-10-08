package engine

import (
	"context"
	"encoding/json/v2"
	"errors"
	"fmt"
	"net/url"
	"path/filepath"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

func (e *Engine) resolveImportReferences(ctx context.Context, data []byte, origin, workspace string) ([]byte, error) {
	var root Object
	if json.Unmarshal(data, &root) != nil {
		if err := yaml.Unmarshal(data, &root); err != nil {
			return nil, err
		}
	}
	name := "x-yeek-import-references"
	for root[name] != nil {
		name += "_"
	}
	external := Object{}
	loaded := map[string]string{}
	total := len(data)
	baseDir := filepath.Dir(origin)
	var rewrite func(any, string, string, int) error
	rewrite = func(value any, base, prefix string, depth int) error {
		if depth > 128 {
			return errors.New("OpenAPI reference nesting exceeds 128 levels")
		}
		switch v := value.(type) {
		case map[string]any:
			if raw := str(v, "$ref"); raw != "" {
				path, fragment, _ := strings.Cut(raw, "#")
				if path == "" {
					if prefix != "" {
						v["$ref"] = "#/" + name + "/" + prefix + fragment
					}
				} else {
					if origin == "" {
						return errors.New("external OpenAPI references need a file or URL source")
					}
					location := ""
					remote := strings.HasPrefix(base, "https://") || strings.HasPrefix(base, "http://")
					if remote {
						u, err := url.Parse(base)
						if err != nil {
							return err
						}
						ref, err := url.Parse(path)
						if err != nil {
							return err
						}
						target := u.ResolveReference(ref)
						if target.Scheme != "http" && target.Scheme != "https" {
							return errors.New("remote OpenAPI references must use HTTP or HTTPS")
						}
						location = target.String()
					} else {
						if strings.Contains(path, "://") {
							location = path
						} else {
							decoded, err := url.PathUnescape(path)
							if err != nil {
								return err
							}
							location = filepath.Join(filepath.Dir(base), decoded)
						}
						if !strings.HasPrefix(location, "http://") && !strings.HasPrefix(location, "https://") {
							resolved, err := filepath.EvalSymlinks(location)
							if err != nil {
								return err
							}
							rel, err := filepath.Rel(baseDir, resolved)
							if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
								return errors.New("OpenAPI reference leaves the source directory")
							}
							location = resolved
						}
					}
					key, exists := loaded[location]
					if !exists {
						if len(loaded) >= 64 {
							return errors.New("OpenAPI contains more than 64 external documents")
						}
						key = importHash(location)[:24]
						loaded[location] = key
						referenceOrigin := location
						var raw []byte
						var err error
						if strings.HasPrefix(location, "http://") || strings.HasPrefix(location, "https://") {
							raw, referenceOrigin, err = e.downloadImport(ctx, location, workspace)
						} else {
							raw, err = readImportFile(location)
						}
						if err != nil {
							return fmt.Errorf("OpenAPI reference: %w", err)
						}
						total += len(raw)
						if total > maxImportBytes {
							return errors.New("OpenAPI and its references exceed 64 MiB")
						}
						var document Object
						if json.Unmarshal(raw, &document) != nil {
							if err = yaml.Unmarshal(raw, &document); err != nil {
								return err
							}
						}
						external[key] = document
						if err = rewrite(document, referenceOrigin, key, depth+1); err != nil {
							return err
						}
					}
					v["$ref"] = "#/" + name + "/" + key + fragment
				}
			}
			for _, child := range v {
				if err := rewrite(child, base, prefix, depth+1); err != nil {
					return err
				}
			}
		case []any:
			for _, child := range v {
				if err := rewrite(child, base, prefix, depth+1); err != nil {
					return err
				}
			}
		}
		return nil
	}
	if err := rewrite(root, origin, "", 0); err != nil {
		return nil, err
	}
	if len(external) > 0 {
		root[name] = external
	}
	var validate func(any) error
	validate = func(value any) error {
		switch v := value.(type) {
		case map[string]any:
			if ref := str(v, "$ref"); ref != "" {
				if _, err := importPointer(root, ref); err != nil {
					return err
				}
			}
			for _, child := range v {
				if err := validate(child); err != nil {
					return err
				}
			}
		case []any:
			for _, child := range v {
				if err := validate(child); err != nil {
					return err
				}
			}
		}
		return nil
	}
	if err := validate(root); err != nil {
		return nil, err
	}
	return json.Marshal(root)
}
func importPointer(root Object, ref string) (Object, error) {
	if ref == "#" {
		return root, nil
	}
	if !strings.HasPrefix(ref, "#/") {
		return nil, fmt.Errorf("OpenAPI reference %q must use a JSON Pointer", ref)
	}
	fragment, err := url.PathUnescape(strings.TrimPrefix(ref, "#/"))
	if err != nil {
		return nil, err
	}
	var current any = root
	for key := range strings.SplitSeq(fragment, "/") {
		key = strings.ReplaceAll(strings.ReplaceAll(key, "~1", "/"), "~0", "~")
		switch value := current.(type) {
		case map[string]any:
			current = value[key]
		case []any:
			i, err := strconv.Atoi(key)
			if err != nil || i < 0 || i >= len(value) || strconv.Itoa(i) != key {
				return nil, fmt.Errorf("OpenAPI reference %q has an invalid array index", ref)
			}
			current = value[i]
		default:
			return nil, fmt.Errorf("OpenAPI reference %q does not resolve to an object", ref)
		}
	}
	model, ok := current.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("OpenAPI reference %q was not found", ref)
	}
	return model, nil
}
func importRef(root, model Object) Object {
	seen := map[string]bool{}
	overlay := Object{}
	for {
		for key, value := range model {
			if key != "$ref" {
				if previous, exists := overlay[key]; !exists {
					overlay[key] = value
				} else {
					overlay[key] = mergeOpenAPIKeyword(key, value, previous, 0)
				}
			}
		}
		ref := str(model, "$ref")
		if !strings.HasPrefix(ref, "#") || seen[ref] {
			if ref != "" {
				overlay["$ref"] = ref
			}
			return overlay
		}
		seen[ref] = true
		next, err := importPointer(root, ref)
		if err != nil {
			return Object{}
		}
		model = next
	}
}
