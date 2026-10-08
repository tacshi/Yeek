package desktop

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json/v2"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/yeekui"
	"github.com/vektah/gqlparser/v2/ast"
	"yeek/internal/engine"
)

type graphQLState struct {
	index                               *engine.GraphQLIndex
	content, cacheKey, file, key, error string
	loading, attempted                  bool
	changed                             time.Time
	cancel                              context.CancelFunc
	generation                          uint64
	query, variables, operation         string
	operations                          []string
	diagnostics                         []engine.GraphQLDiagnostic
	validatedIndex                      *engine.GraphQLIndex
}

func (a *App) graphQLState(id string) *graphQLState {
	if a.graphqlStates == nil {
		a.graphqlStates = map[string]*graphQLState{}
	}
	state := a.graphqlStates[id]
	if state == nil {
		state = &graphQLState{}
		a.graphqlStates[id] = state
	}
	return state
}
func (a *App) applyGraphQLSchema(m engine.Object) {
	state := a.graphQLState(s(m, "requestId"))
	content := s(m, "content")
	if state.content != content {
		state.content = content
		state.index = nil
		if content != "" {
			state.index, _ = engine.LoadGraphQLSchema([]byte(content))
		}
	}
	state.cacheKey, state.file = s(m, "schemaKey"), s(m, "schemaFile")
}
func (a *App) graphQLKey(d *Draft) string {
	sources := []engine.Object{}
	for _, m := range a.models {
		kind := s(m, "model")
		if kind != "workspace" && kind != "environment" && kind != "folder" {
			continue
		}
		if s(m, "id") != a.workspace && s(m, "workspaceId") != a.workspace {
			continue
		}
		item := engine.Object{"id": s(m, "id")}
		for _, key := range []string{"headers", "authentication", "authenticationType", "variables", "parentId", "folderId"} {
			item[key] = m[key]
		}
		sources = append(sources, item)
	}
	slices.SortFunc(sources, func(a, b engine.Object) int { return strings.Compare(s(a, "id"), s(b, "id")) })
	config := engine.Object{"url": d.URL, "method": d.Method, "headers": rowObjects(d.Headers), "parameters": rowObjects(d.Parameters), "authentication": d.Auth, "authenticationType": d.AuthType, "environment": a.environment, "cookieJar": a.cookieJar, "folder": s(d.Model, "folderId"), "sources": sources}
	raw, _ := json.Marshal(config, json.Deterministic(true))
	sum := sha256.Sum256(raw)
	return hex.EncodeToString(sum[:])
}
func (a *App) graphQLIndex(d *Draft) *engine.GraphQLIndex {
	state := a.graphQLState(d.ID)
	file := s(o(d.Model, "body"), "schemaFilePath")
	if file != "" && file == state.file {
		return state.index
	}
	if file == "" && state.file == "" && (state.cacheKey == state.key || state.cacheKey == "") {
		return state.index
	}
	return nil
}
func (a *App) fetchGraphQL(d *Draft, showDocs bool) {
	state := a.graphQLState(d.ID)
	if state.cancel != nil {
		state.cancel()
	}
	ctx, cancel := context.WithCancel(a.ctx)
	state.cancel = cancel
	state.generation++
	generation := state.generation
	state.loading, state.attempted, state.error = true, true, ""
	id, key, request, options := d.ID, a.graphQLKey(d), d.object(), engine.SendOptions{EnvironmentID: a.environment, CookieJarID: a.cookieJar}
	file := s(o(d.Model, "body"), "schemaFilePath")
	a.background(func() (func(), error) {
		defer cancel()
		var model engine.Object
		var err error
		if file != "" {
			model, err = a.Engine.LoadGraphQLSchemaFile(ctx, id, file, key)
		} else {
			var raw []byte
			raw, err = a.Engine.IntrospectGraphQL(ctx, request, options)
			if err == nil {
				model, err = a.Engine.StoreGraphQLSchema(ctx, id, string(raw), key, "")
			}
		}
		return func() {
			if generation != state.generation {
				return
			}
			state.loading = false
			state.cancel = nil
			if err != nil {
				state.error = err.Error()
				return
			}
			a.applyModel(model)
			if showDocs {
				a.openGraphQLDocs(id, "")
			}
		}, nil
	})
}
func (a *App) openGraphQLDocs(id, typ string) {
	state := a.graphQLState(id)
	if state.index == nil {
		return
	}
	if typ == "" {
		typ = state.index.Schema.Query.Name
	}
	a.graphqlType = typ
	a.prompt("graphql_docs", "GraphQL Documentation", "", id)
}
func (a *App) chooseGraphQLFile(d *Draft) {
	id, key := d.ID, a.graphQLKey(d)
	state := a.graphQLState(id)
	if state.cancel != nil {
		state.cancel()
		state.cancel = nil
	}
	state.generation++
	generation := state.generation
	state.loading, state.attempted, state.key = true, true, key
	a.background(func() (func(), error) {
		paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{Parent: a.Window, Title: "Load GraphQL Schema", Filters: []mygo.FileFilter{{Name: "GraphQL Schema", Extensions: []string{"graphql", "graphqls", "gql", "json"}}}})
		if err != nil || len(paths) == 0 {
			return func() {
				if state.generation == generation {
					state.loading = false
					if err != nil {
						state.error = err.Error()
					} else {
						state.attempted = false
					}
				}
			}, nil
		}
		model, err := a.Engine.LoadGraphQLSchemaFile(a.ctx, id, paths[0], key)
		return func() {
			if state.generation != generation {
				return
			}
			state.loading = false
			if err != nil {
				state.error = err.Error()
				return
			}
			body := o(d.Model, "body")
			if body == nil {
				body = engine.Object{}
				d.Model["body"] = body
			}
			body["schemaFilePath"] = paths[0]
			d.Dirty = true
			a.save(d)
			a.applyModel(model)
			state.error = ""
			a.openGraphQLDocs(id, "")
		}, nil
	})
}
func (a *App) graphQLTools(c *ui.Context, p colors, d *Draft) {
	state := a.graphQLState(d.ID)
	key := a.graphQLKey(d)
	if state.key != key {
		state.key = key
		state.changed = c.Now()
		state.attempted = false
		if state.cancel != nil {
			state.cancel()
			state.cancel = nil
			state.loading = false
			state.generation++
		}
	}
	body := o(d.Model, "body")
	file := s(body, "schemaFilePath")
	auto := !b(body, "disableAutoIntrospect")
	if (auto || file != "") && (file != "" || strings.TrimSpace(d.URL) != "") && !state.loading && !state.attempted && a.graphQLIndex(d) == nil {
		remaining := 700*time.Millisecond - c.Now().Sub(state.changed)
		if remaining > 0 {
			c.After(remaining)
		} else {
			a.fetchGraphQL(d, false)
		}
	}
	label := "Schema"
	if state.loading {
		label = "Loading schema…"
	}
	ui.MenuButton(c, label, func(menu *ui.Menu) {
		if menu.Item("Documentation").Disabled(a.graphQLIndex(d) == nil).Chosen() {
			a.openGraphQLDocs(d.ID, "")
		}
		fetch := "Fetch from Server"
		if file != "" {
			fetch = "Reload Schema File"
		}
		if menu.Item(fetch).Disabled(state.loading).Chosen() {
			a.fetchGraphQL(d, true)
		}
		if menu.Item("Load Schema File…").Chosen() {
			a.chooseGraphQLFile(d)
		}
		if file != "" && menu.Item("Use Server Schema").Chosen() {
			delete(body, "schemaFilePath")
			d.Dirty = true
			state.attempted = false
			state.error = ""
		}
		menu.Separator()
		if menu.Item("Fetch Schema Automatically").Checked(auto).Disabled(file != "").Chosen() {
			if body == nil {
				body = engine.Object{}
				d.Model["body"] = body
			}
			body["disableAutoIntrospect"] = auto
			d.Dirty = true
			state.attempted = false
		}
		if menu.Item("Clear Cached Schema").Disabled(state.index == nil).Chosen() {
			id := d.ID
			state.attempted = true
			a.run(func() (func(), error) {
				m, err := a.Engine.StoreGraphQLSchema(a.ctx, id, "", key, file)
				return func() { a.applyModel(m) }, err
			})
		}
	}).FontSize(11)
}
func (a *App) graphQLQueryHeader(c *ui.Context, p colors, d *Draft) {
	state := a.graphQLState(d.ID)
	if state.query != d.Query {
		names := engine.GraphQLOperations(d.Query)
		state.operations = names
		if names != nil && (!d.OperationExplicit || d.OperationName != "" && !slices.Contains(names, d.OperationName)) {
			previous := d.OperationName
			d.OperationName = ""
			if len(names) > 0 {
				d.OperationName = names[0]
			}
			d.OperationExplicit = len(names) > 0
			if previous != d.OperationName {
				d.Dirty = true
			}
		}
	}
	index := a.graphQLIndex(d)
	if state.query != d.Query || state.variables != d.Variables || state.operation != d.OperationName || state.validatedIndex != index {
		state.query, state.variables, state.operation, state.validatedIndex = d.Query, d.Variables, d.OperationName, index
		state.diagnostics = nil
		if strings.TrimSpace(d.Query) != "" {
			state.diagnostics = engine.GraphQLDiagnostics(index, d.Query, d.Variables, d.OperationName)
		}
	}
	if state.error != "" {
		ui.Row(c).Padding(6, 12).Gap(8).Children(func() {
			ui.Text(c, state.error).TextColor(p.red).FontSize(11).Grow(1).MaxLines(2)
			if ui.Button(c, "Retry").FontSize(11).Disabled(state.loading).Clicked() {
				a.fetchGraphQL(d, false)
			}
		})
	}
	ui.Row(c).Padding(5, 14).Gap(8).Children(func() {
		ui.Text(c, "Query").TextColor(p.muted).FontSize(11)
		ui.Spacer(c)
		if len(state.operations) > 0 {
			name := d.OperationName
			if name == "" {
				name = "Operation not specified"
			}
			ui.MenuButton(c, name, func(menu *ui.Menu) {
				for _, op := range append([]string{""}, state.operations...) {
					label := op
					if label == "" {
						label = "Not specified"
					}
					if menu.Item(label).Checked(op == d.OperationName).Chosen() {
						d.OperationName = op
						d.OperationExplicit = true
						d.Dirty = true
					}
				}
			}).Label("GraphQL operation: " + name).FontSize(11)
		}
		if file := s(o(d.Model, "body"), "schemaFilePath"); file != "" {
			ui.Text(c, filepath.Base(file)).FontSize(10).TextColor(p.muted).MaxWidth(190).SingleLine().Tooltip(file)
		}
	})
}
func (a *App) graphQLProblems(c *ui.Context, p colors, d *Draft) {
	state := a.graphQLState(d.ID)
	if len(state.diagnostics) == 0 {
		return
	}
	ui.Scroll(c).Padding(5, 12).Gap(3).MaxHeight(82).Children(func() {
		for i, issue := range state.diagnostics {
			label := fmt.Sprintf("%d:%d  %s", issue.Line, issue.Column, issue.Message)
			if issue.Variables {
				label = "Variables: " + issue.Message
			}
			button := ui.ButtonBase(c).Key(i).FillWidth().Padding(2).Children(func() { ui.Text(c, label).FontSize(11).TextColor(p.red).MaxLines(2) })
			if button.Clicked() {
				editor := "GraphQL query"
				if issue.Variables {
					editor = "GraphQL variables"
				}
				a.editorDocument(editor).state.GoTo(issue.Line, issue.Column)
			}
		}
	})
}
func (a *App) graphqlDocs(c *ui.Context, p colors) {
	state := a.graphQLState(a.dialogID)
	if state.index == nil {
		ui.Text(c, "Load a schema to browse its types.").Padding(20)
		return
	}
	schema := state.index.Schema
	ui.Row(c).Height(520).AlignItems(ui.Stretch).Children(func() {
		ui.Column(c).Width(205).Padding(10).Gap(8).Background(p.sidebar).Children(func() {
			ui.TextInput(c, &a.graphqlSearch).Placeholder("Filter types").Label("Filter GraphQL types")
			ui.Scroll(c).Grow(1).Children(func() {
				names := []string{}
				for name := range schema.Types {
					if !strings.HasPrefix(name, "__") && strings.Contains(strings.ToLower(name), strings.ToLower(a.graphqlSearch)) {
						names = append(names, name)
					}
				}
				slices.Sort(names)
				for _, name := range names {
					if ui.ButtonBase(c).Key(name).FillWidth().Padding(7, 6).Children(func() { ui.Text(c, name).SingleLine().FontSize(12) }).Clicked() {
						a.graphqlType = name
					}
				}
			})
		})
		ui.Scroll(c).Grow(1).Padding(20).Gap(14).Children(func() {
			typ := schema.Types[a.graphqlType]
			if typ == nil {
				return
			}
			ui.Text(c, typ.Name).FontSize(21).FontWeight(600)
			if typ.Description != "" {
				ui.Text(c, typ.Description).FontSize(12).Selectable()
			}
			links := append(slices.Clone(typ.Interfaces), typ.Types...)
			for _, name := range links {
				if ui.Button(c, name).FontSize(12).Clicked() {
					a.graphqlType = name
				}
			}
			for _, field := range typ.Fields {
				if strings.HasPrefix(field.Name, "__") && !strings.HasPrefix(typ.Name, "__") {
					continue
				}
				ui.Column(c).Key(field.Name).Gap(7).Padding(8, 0).BorderWidth(0, 0, 1, 0).BorderColor(p.border).Children(func() {
					ui.Row(c).Gap(8).Children(func() {
						ui.Text(c, field.Name).Font("monospace").FontSize(12).TextColor(p.accent)
						if ui.ButtonBase(c).Children(func() { ui.Text(c, field.Type.String()).Font("monospace").FontSize(12).TextColor(p.blue) }).Clicked() {
							a.graphqlType = field.Type.Name()
						}
					})
					if field.Description != "" {
						ui.Text(c, field.Description).FontSize(12).TextColor(p.muted).Selectable()
					}
					for _, arg := range field.Arguments {
						label := arg.Name + ": " + arg.Type.String()
						if arg.DefaultValue != nil {
							label += " = " + arg.DefaultValue.String()
						}
						ui.Text(c, label).FontSize(11).Font("monospace").TextColor(p.muted)
						if arg.Description != "" {
							ui.Text(c, arg.Description).FontSize(11).TextColor(p.muted)
						}
					}
					if field.DefaultValue != nil {
						ui.Text(c, "Default: "+field.DefaultValue.String()).FontSize(11).TextColor(p.muted)
					}
					graphQLDeprecated(c, p, field.Directives)
				})
			}
			for _, value := range typ.EnumValues {
				ui.Text(c, value.Name).Font("monospace").FontSize(12).TextColor(p.accent)
				if value.Description != "" {
					ui.Text(c, value.Description).TextColor(p.muted).FontSize(12)
				}
				graphQLDeprecated(c, p, value.Directives)
			}
		})
	})
}
func graphQLDeprecated(c *ui.Context, p colors, directives ast.DirectiveList) {
	if d := directives.ForName("deprecated"); d != nil {
		reason := "Deprecated"
		if arg := d.Arguments.ForName("reason"); arg != nil {
			reason += " — " + arg.Value.Raw
		}
		ui.Text(c, reason).TextColor(p.orange).FontSize(11)
	}
}
