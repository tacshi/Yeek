package desktop

import (
	"regexp"
	"strings"

	"github.com/egoist/mygo/yeekui"
	"uuid"
)

// bulkPreference names the editors whose bulk mode is remembered together, as Yaak's preferenceName does.
func bulkPreference(name string) string {
	switch name {
	case "Header":
		return "headers"
	case "Parameter":
		return "url_parameters"
	case "Variable":
		return "environment"
	}
	return "form"
}

// formatBulkPairs writes pairs as Yaak's bulk editor does: one "name: value"
// per line, disabled pairs commented out as "# name: value", newlines escaped.
func formatBulkPairs(rows []KV) string {
	lines := []string{}
	for _, row := range rows {
		if strings.TrimSpace(row.Name) == "" && strings.TrimSpace(row.Value) == "" {
			continue
		}
		line := row.Name + ": " + strings.ReplaceAll(row.Value, "\n", `\n`)
		if !row.Enabled {
			line = "# " + line
		}
		lines = append(lines, line)
	}
	return strings.Join(lines, "\n")
}

var (
	bulkPair          = regexp.MustCompile(`^([^:]+):\s+(.*)$`)
	bulkCommentedPair = regexp.MustCompile(`^([^:]+):(?:\s+(.*))?$`)
	bulkComment       = regexp.MustCompile(`^\s*#(?:\s+|$)`)
)

// parseBulkPairs reads Yaak's bulk format. A "# " line is a disabled pair
// when the rest parses as one, else a comment that is dropped; "#foo: bar"
// stays an enabled pair named "#foo". A line without ": " is a name alone.
func parseBulkPairs(text string) []KV {
	rows := []KV{}
	for line := range strings.SplitSeq(text, "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		enabled, match := true, []string(nil)
		if prefix := bulkComment.FindString(line); prefix != "" {
			line = line[len(prefix):]
			if match = bulkCommentedPair.FindStringSubmatch(line); match == nil {
				continue
			}
			enabled = false
		} else {
			match = bulkPair.FindStringSubmatch(line)
		}
		name, value := line, ""
		if match != nil {
			name, value = match[1], match[2]
		}
		rows = append(rows, KV{ID: uuid.NewV4().String(), Name: strings.TrimSpace(name), Value: strings.TrimSpace(strings.ReplaceAll(value, `\n`, "\n")), Enabled: enabled})
	}
	return rows
}

// bulkEditor edits rows as text. The text is kept while it parses back to the
// same rows, so typing is not reformatted under the caret.
func (a *App) bulkEditor(c *ui.Context, p colors, rows *[]KV, name string, dirty *bool) {
	if a.bulkText == nil {
		a.bulkText = map[string]string{}
	}
	key := bulkPreference(name) + ":" + a.active + ":" + a.dialog + ":" + a.dialogID
	text, ok := a.bulkText[key]
	if !ok || formatBulkPairs(parseBulkPairs(text)) != formatBulkPairs(*rows) {
		text = formatBulkPairs(*rows)
	}
	ui.Column(c).Grow(1).MinHeight(0).Padding(8, 0, 8, 0).Children(func() {
		if a.nativeEditor(c, p, &text, "Bulk edit "+name, "text", false) {
			*rows = parseBulkPairs(text)
			*dirty = true
		}
	})
	a.bulkText[key] = text
}

// pairOrBulkEditor is Yaak's PairOrBulkEditor: the row editor or the bulk
// text, switched by a button in the corner that shows on hover.
func (a *App) pairOrBulkEditor(c *ui.Context, p colors, rows *[]KV, name, value string, dirty *bool) {
	preference := bulkPreference(name)
	bulk := a.bulkEdit[preference]
	area := ui.Box(c).Grow(1).MinHeight(0)
	area.Children(func() {
		ui.Column(c).Fill().Children(func() {
			if bulk {
				a.bulkEditor(c, p, rows, name, dirty)
			} else {
				a.pairEditor(c, p, rows, name, value, dirty)
			}
		})
		if !area.Hovered() {
			return
		}
		glyph, label := "fileCode", "Enable bulk edit"
		if bulk {
			glyph, label = "table", "Enable form edit"
		}
		toggle := sizedIconButton(c, glyph, label, 28, 15).Absolute().Right(0).Bottom(0).Border(1, p.border).Background(p.background).Shadow(0, 1, 3, 0, ui.RGBA(0, 0, 0, .12))
		if toggle.Clicked() {
			if a.bulkEdit == nil {
				a.bulkEdit = map[string]bool{}
			}
			a.bulkEdit[preference] = !bulk
			a.persistSession()
		}
	})
}
