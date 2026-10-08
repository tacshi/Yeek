package desktop

import (
	"slices"
	"strconv"
	"strings"

	"github.com/egoist/mygo/yeekui"
	"github.com/vektah/gqlparser/v2/ast"
)

// graphQLDocsOpen reports whether a GraphQL request shows Yaak's
// documentation explorer beside it.
func (a *App) graphQLDocsOpen(d *Draft) bool {
	return d != nil && d.BodyType == "graphql" && a.docsTrail[d.ID] != nil && a.graphQLIndex(d) != nil
}

// openGraphQLDocs opens the explorer of a request, at a type when one is
// named.
func (a *App) openGraphQLDocs(id, typ string) {
	if a.docsTrail == nil {
		a.docsTrail = map[string][]string{}
	}
	trail := []string{}
	if typ != "" {
		trail = append(trail, typ)
	}
	a.docsTrail[id] = trail
}

func (a *App) closeGraphQLDocs(id string) { delete(a.docsTrail, id) }

// graphQLDocsPane is Yaak's GraphQLDocsExplorer: the types it went through,
// with a search, over the root types or the type it is at.
func (a *App) graphQLDocsPane(c *ui.Context, p colors, d *Draft) {
	index := a.graphQLIndex(d)
	schema := index.Schema
	trail := a.docsTrail[d.ID]
	visit := func(name string) {
		if schema.Types[name] != nil {
			a.docsTrail[d.ID] = append(slices.Clone(trail), name)
			a.docsSearch = ""
		}
	}
	ui.Column(c).Fill().Padding(12, 12, 12, 0).Children(func() {
		ui.Column(c).Fill().Radius(8).Children(func() {
			// The header: where the explorer is, a search, and close.
			ui.Row(c).Height(40).Shrink(0).Padding(0, 4, 0, 12).Gap(6).Children(func() {
				icon(c, "book").FontSize(14).TextColor(p.muted)
				ui.Row(c).Gap(6).Shrink(1).MinWidth(0).Children(func() {
					for i, name := range trail {
						if i > 0 {
							icon(c, "chevron").FontSize(11).TextColor(p.subtle)
						}
						if i == len(trail)-1 {
							ui.Text(c, name).FontSize(13).TextColor(p.muted).SingleLine()
							continue
						}
						link := ui.ButtonBase(c).Key(i).Label("Go to " + name)
						link.Children(func() { ui.Text(c, name).FontSize(13).TextColor(p.notice).SingleLine() })
						if link.Clicked() {
							a.docsTrail[d.ID] = slices.Clone(trail[:i+1])
						}
					}
				})
				ui.TextInput(c, &a.docsSearch).Label("Search GraphQL schema").Placeholder("Search").Grow(1).MinWidth(60).Height(26).FontSize(12)
				if smallIconButton(c, "close", "Close documentation explorer").Clicked() {
					a.closeGraphQLDocs(d.ID)
				}
			})
			ui.Scroll(c).Grow(1).MinHeight(0).Padding(4, 16, 24, 12).Gap(10).Children(func() {
				if query := strings.ToLower(strings.TrimSpace(a.docsSearch)); query != "" {
					a.graphQLDocsSearch(c, p, d, query, visit)
					return
				}
				if len(trail) == 0 {
					a.graphQLDocsHome(c, p, d, visit)
					return
				}
				a.graphQLDocsType(c, p, d, trail[len(trail)-1], visit)
			})
		}).Draw(func(pt *ui.Painter, r ui.Rect) {
			// A dashed border, as Yaak's explorer has.
			for x := r.X; x < r.X+r.W; x += 6 {
				pt.Fill(ui.Rect{X: x, Y: r.Y, W: 3, H: 1}, p.border, 0)
				pt.Fill(ui.Rect{X: x, Y: r.Y + r.H - 1, W: 3, H: 1}, p.border, 0)
			}
			for y := r.Y; y < r.Y+r.H; y += 6 {
				pt.Fill(ui.Rect{X: r.X, Y: y, W: 1, H: 3}, p.border, 0)
				pt.Fill(ui.Rect{X: r.X + r.W - 1, Y: y, W: 1, H: 3}, p.border, 0)
			}
		})
	})
}

func docsHeading(c *ui.Context, text string) {
	ui.Text(c, text).FontSize(17).FontWeight(600).Margin(6, 0, 2, 0)
}

// graphQLDocsHome is the explorer's first page: the root types, then all.
func (a *App) graphQLDocsHome(c *ui.Context, p colors, d *Draft, visit func(string)) {
	schema := a.graphQLIndex(d).Schema
	docsHeading(c, "Root Types")
	for _, root := range []struct {
		label string
		name  string
	}{{"query", typeName(schema.Query)}, {"mutation", typeName(schema.Mutation)}, {"subscription", typeName(schema.Subscription)}} {
		ui.Row(c).Key(root.label).Gap(6).Children(func() {
			ui.Text(c, root.label).Font("monospace").FontSize(12).TextColor(p.accent)
			ui.Text(c, ":").Font("monospace").FontSize(12).TextColor(p.subtle)
			if root.name == "" {
				ui.Text(c, "null").Font("monospace").FontSize(12).TextColor(p.subtle).Italic()
				return
			}
			docsLink(c, p, root.name, visit)
		})
	}
	names := make([]string, 0, len(schema.Types))
	for name := range schema.Types {
		names = append(names, name)
	}
	slices.Sort(names)
	ui.Row(c).Gap(6).Margin(10, 0, 0, 0).Children(func() {
		ui.Text(c, "All Schema Types").FontSize(14).FontWeight(600)
		(&countBadge{count: len(names)}).view(c, p)
	})
	if schema.Description != "" {
		ui.Text(c, schema.Description).FontSize(12).TextColor(p.muted).Selectable()
	}
	ui.Column(c).Gap(4).Children(func() {
		for _, name := range names {
			ui.Box(c).Key(name).Children(func() { docsLink(c, p, name, visit) })
		}
	})
}

func typeName(t *ast.Definition) string {
	if t == nil {
		return ""
	}
	return t.Name
}

// docsLink is Yaak's GqlTypeLink.
func docsLink(c *ui.Context, p colors, name string, visit func(string)) {
	link := ui.ButtonBase(c).Label("Go to " + name)
	link.Children(func() { ui.Text(c, name).Font("monospace").FontSize(12).TextColor(p.notice) })
	if link.Clicked() {
		visit(name)
	}
}

// graphQLDocsType is Yaak's GqlTypeInfo for a type.
func (a *App) graphQLDocsType(c *ui.Context, p colors, d *Draft, name string, visit func(string)) {
	typ := a.graphQLIndex(d).Schema.Types[name]
	if typ == nil {
		return
	}
	docsHeading(c, typ.Name)
	description := typ.Description
	if description == "" {
		description = "No description"
	}
	ui.Text(c, description).FontSize(12).TextColor(p.muted).Selectable()
	if links := append(slices.Clone(typ.Interfaces), typ.Types...); len(links) > 0 {
		ui.Text(c, map[bool]string{true: "Possible Types", false: "Interfaces"}[len(typ.Types) > 0]).FontSize(14).FontWeight(600).Margin(8, 0, 0, 0)
		for _, link := range links {
			ui.Box(c).Key("link-" + link).Children(func() { docsLink(c, p, link, visit) })
		}
	}
	fields := slices.DeleteFunc(slices.Clone(typ.Fields), func(f *ast.FieldDefinition) bool {
		return strings.HasPrefix(f.Name, "__") && !strings.HasPrefix(typ.Name, "__")
	})
	if len(fields) > 0 {
		ui.Row(c).Gap(6).Margin(8, 0, 0, 0).Children(func() {
			ui.Text(c, "Fields").FontSize(14).FontWeight(600)
			(&countBadge{count: len(fields)}).view(c, p)
		})
	}
	for _, field := range fields {
		ui.Column(c).Key(field.Name).Gap(5).Padding(6, 0).Children(func() {
			ui.Row(c).Gap(4).Wrap().Children(func() {
				ui.Text(c, field.Name).Font("monospace").FontSize(12).TextColor(p.accent)
				if len(field.Arguments) > 0 {
					ui.Text(c, "(…)").Font("monospace").FontSize(12).TextColor(p.subtle)
				}
				ui.Text(c, ":").Font("monospace").FontSize(12).TextColor(p.subtle)
				ui.Box(c).Children(func() { docsLink(c, p, field.Type.String(), func(string) { visit(field.Type.Name()) }) })
			})
			if field.Description != "" {
				ui.Text(c, field.Description).FontSize(12).TextColor(p.muted).Selectable()
			}
			for _, arg := range field.Arguments {
				label := arg.Name + ": " + arg.Type.String()
				if arg.DefaultValue != nil {
					label += " = " + arg.DefaultValue.String()
				}
				ui.Text(c, label).FontSize(11).Font("monospace").TextColor(p.muted).Margin(0, 0, 0, 12)
			}
			graphQLDeprecated(c, p, field.Directives)
		})
	}
	if len(typ.EnumValues) > 0 {
		ui.Text(c, "Enum Values").FontSize(14).FontWeight(600).Margin(8, 0, 0, 0)
	}
	for _, value := range typ.EnumValues {
		ui.Column(c).Key("enum-" + value.Name).Gap(3).Children(func() {
			ui.Text(c, value.Name).Font("monospace").FontSize(12).TextColor(p.accent)
			if value.Description != "" {
				ui.Text(c, value.Description).TextColor(p.muted).FontSize(12)
			}
			graphQLDeprecated(c, p, value.Directives)
		})
	}
}

// graphQLDocsSearch is Yaak's GqlSchemaSearch: types and fields whose
// names hold the query.
func (a *App) graphQLDocsSearch(c *ui.Context, p colors, d *Draft, query string, visit func(string)) {
	schema := a.graphQLIndex(d).Schema
	names := make([]string, 0, len(schema.Types))
	for name := range schema.Types {
		names = append(names, name)
	}
	slices.Sort(names)
	shown := 0
	for _, name := range names {
		typ := schema.Types[name]
		if strings.Contains(strings.ToLower(name), query) {
			ui.Box(c).Key("type-" + name).Children(func() { docsLink(c, p, name, visit) })
			shown++
		}
		for _, field := range typ.Fields {
			if strings.HasPrefix(field.Name, "__") || !strings.Contains(strings.ToLower(field.Name), query) {
				continue
			}
			ui.Row(c).Key(name + "." + field.Name).Gap(4).Children(func() {
				ui.Text(c, name+".").Font("monospace").FontSize(12).TextColor(p.subtle)
				link := ui.ButtonBase(c).Label("Go to " + name + "." + field.Name)
				link.Children(func() { ui.Text(c, field.Name).Font("monospace").FontSize(12).TextColor(p.accent) })
				if link.Clicked() {
					visit(name)
				}
			})
			shown++
		}
	}
	if shown == 0 {
		ui.Text(c, "No results for "+strconv.Quote(query)).Italic().FontSize(12).TextColor(p.subtle)
	}
}
