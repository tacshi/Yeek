package desktop

import (
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// pathPlaceholder matches Yaak's :name path placeholders: a colon at the
// start of a path segment, then anything but / ? # or another colon.
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

// syncPathPlaceholders is Yaak's derivePathPlaceholderPairs for a draft: a
// Params row for each placeholder in the URL, and renaming a placeholder's
// row rewrites the URL to match. Rows Yeek added that still have no value
// leave with their placeholder and are not saved.
func (a *App) syncPathPlaceholders(d *Draft) {
	if a.placeholderNames == nil {
		a.placeholderNames = map[string]string{}
	}
	names := extractPathPlaceholders(d.URL)
	// A placeholder row renamed since the last frame renames the placeholder.
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
	// Drop derived rows nobody filled in whose placeholder is gone.
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
		// Ids come from the position, so re-deriving never gives a row a new identity.
		id := placeholderRowIDTag + strconv.Itoa(index)
		for bump := index + 1; taken[id]; bump++ {
			id = placeholderRowIDTag + strconv.Itoa(bump)
		}
		taken[id] = true
		// Before the trailing empty row the editor keeps for new parameters.
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

// renamePathPlaceholder is Yaak's renamePathPlaceholder for the URL: every
// occurrence of old becomes the new name, which gets a leading colon if it
// lacks one. It fails for a name that would not parse as a placeholder, or
// that another placeholder in the URL already has.
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
	// Replace the placeholders the extractor finds, so a segment or query
	// value that merely looks the same is left alone.
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
