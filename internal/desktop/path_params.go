package desktop

import (
	"regexp"
	"slices"
	"strconv"
	"strings"
)

var (
	pathPlaceholder     = regexp.MustCompile(`/(:[^/?#:]+)`)
	validPlaceholder    = regexp.MustCompile(`^:[^/?#:\s]+$`)
	placeholderRowIDTag = "path-placeholder:"
)

func extractPathPlaceholders(url string) []string {
	names := []string{}
	for _, m := range pathPlaceholder.FindAllStringSubmatch(url, -1) {
		names = append(names, m[1])
	}
	return names
}

func (a *App) syncPathPlaceholders(d *Draft) {
	if a.placeholderNames == nil {
		a.placeholderNames = map[string]string{}
	}
	names := extractPathPlaceholders(d.URL)
	for i := range d.Parameters {
		row := &d.Parameters[i]
		old, tracked := a.placeholderNames[row.ID]
		if !tracked || old == row.Name || !slices.Contains(names, old) {
			continue
		}
		if url, ok := renamePathPlaceholder(d.URL, old, row.Name); ok {
			if !strings.HasPrefix(row.Name, ":") {
				row.Name = ":" + row.Name
			}
			d.URL, d.Dirty = url, true
			names = extractPathPlaceholders(d.URL)
		}
	}
	d.Parameters = slices.DeleteFunc(d.Parameters, func(row KV) bool {
		return strings.HasPrefix(row.ID, placeholderRowIDTag) && row.Value == "" && !slices.Contains(names, row.Name)
	})
	taken := map[string]bool{}
	for _, row := range d.Parameters {
		taken[row.ID] = true
	}
	seen := map[string]bool{}
	for index, name := range names {
		if seen[name] {
			continue
		}
		seen[name] = true
		if slices.ContainsFunc(d.Parameters, func(row KV) bool { return row.Name == name }) {
			continue
		}
		id := placeholderRowIDTag + strconv.Itoa(index)
		for bump := index + 1; taken[id]; bump++ {
			id = placeholderRowIDTag + strconv.Itoa(bump)
		}
		taken[id] = true
		row := KV{ID: id, Name: name, Enabled: true}
		if last := len(d.Parameters) - 1; last >= 0 && d.Parameters[last].Name == "" && d.Parameters[last].Value == "" {
			d.Parameters = slices.Insert(d.Parameters, last, row)
		} else {
			d.Parameters = append(d.Parameters, row)
		}
	}
	for _, row := range d.Parameters {
		if slices.Contains(names, row.Name) {
			a.placeholderNames[row.ID] = row.Name
		} else {
			delete(a.placeholderNames, row.ID)
		}
	}
}

func renamePathPlaceholder(url, old, name string) (string, bool) {
	if !strings.HasPrefix(name, ":") {
		name = ":" + name
	}
	if !validPlaceholder.MatchString(name) {
		return "", false
	}
	names := extractPathPlaceholders(url)
	if !slices.Contains(names, old) || name != old && slices.Contains(names, name) {
		return "", false
	}
	var b strings.Builder
	last := 0
	for _, m := range pathPlaceholder.FindAllStringSubmatchIndex(url, -1) {
		if url[m[2]:m[3]] != old {
			continue
		}
		b.WriteString(url[last:m[2]])
		b.WriteString(name)
		last = m[3]
	}
	b.WriteString(url[last:])
	return b.String(), true
}
