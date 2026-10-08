package desktop

import (
	"encoding/hex"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/yeekui"
	"yeek/internal/engine"
)

// protocolTabs are Yaak's request tabs for a gRPC request (Metadata, no
// Params) and a WebSocket request.
func (a *App) protocolTabs(d *Draft) []tabItem {
	tabs := []tabItem{{Label: "Message"}}
	if d.Kind == "grpc_request" {
		tabs = append(tabs, tabItem{Label: "Metadata", Badge: badgeCount(a.headerCount(d))})
	} else {
		tabs = append(tabs, tabItem{Label: "Params", Badge: badgeCount(countNamed(d.Parameters, true))}, tabItem{Label: "Headers", Badge: badgeCount(a.headerCount(d))})
	}
	return append(tabs,
		tabItem{Label: authTabLabel(a, d.AuthType), Menu: func(m *ui.Menu) { a.authTypeMenu(m, d) }},
		tabItem{Label: "Settings", Badge: badgeCount(overriddenSettings(d))},
		tabItem{Label: "Info", Badge: badgeDot(strings.TrimSpace(d.Description) != "")},
	)
}

// protocolRequest is Yaak's GrpcRequestPane or WebsocketRequestPane, under
// the URL bar.
func (a *App) protocolRequest(c *ui.Context, p colors, d *Draft) {
	tabs := a.protocolTabs(d)
	d.Tab = max(0, min(d.Tab, len(tabs)-1))
	tabBar(c, p, &d.Tab, tabs)
	switch tabs[d.Tab].Label {
	case "Message":
		a.messageEditor(c, p, d)
	case "Params":
		a.kvEditor(c, p, &d.Parameters, "Parameter", "Value", &d.Dirty)
	case "Metadata", "Headers":
		a.headersEditor(c, p, d)
	case "Settings":
		a.requestSettings(c, p, d)
	case "Info":
		a.descriptionEditor(c, p, d, "Request description")
	default:
		a.authEditor(c, p, d)
	}
}

// messageEditor is the Message tab; a gRPC request's has Yaak's schema
// menu in its corner.
func (a *App) messageEditor(c *ui.Context, p colors, d *Draft) {
	// A gRPC message completes and checks its method's input fields.
	doc := a.editorDocument("Message body")
	doc.jsonSchema = nil
	if m := a.grpcMethod(d); d.Kind == "grpc_request" && m != nil {
		doc.jsonSchema = m.Input
	}
	defer func() {
		if doc.jsonSchema == nil {
			return
		}
		problems := checkJSONMessage(doc.jsonSchema, d.Message)
		if len(problems) == 0 {
			return
		}
		r := []rune(d.Message)
		ui.Scroll(c).Shrink(0).Padding(5, 12).Gap(3).MaxHeight(82).Children(func() {
			for i, problem := range problems {
				line := 1 + strings.Count(string(r[:problem.start]), "\n")
				column := problem.start - strings.LastIndex(string(r[:problem.start]), "\n")
				button := ui.ButtonBase(c).Key(i).FillWidth().Padding(2)
				button.Children(func() {
					ui.Text(c, fmt.Sprintf("%d:%d  %s", line, column, problem.message)).FontSize(11).TextColor(p.red).MaxLines(2)
				})
				if button.Clicked() {
					doc.state.GoTo(line, column)
				}
			}
		})
	}()
	ui.Box(c).Grow(1).MinHeight(0).FillWidth().Children(func() {
		a.codeInput(c, &d.Message, "Message body", &d.Dirty)
		if d.Kind != "grpc_request" {
			return
		}
		schema := a.grpcSchema(d)
		files := a.protoFiles(d.ID)
		label := "Select Schema"
		switch {
		case schema.loading:
			label = "Inspecting Schema"
		case schema.unimplemented():
			label = "Select Proto Files"
		case schema.err != "":
			label = "Server Error"
		case len(files) > 0:
			label = pluralizeCount("File", len(files))
		case schema.services != nil:
			label = "Schema Detected"
		}
		button := ui.MenuButton(c, label, func(m *ui.Menu) {
			if schema.err != "" && !schema.unimplemented() && m.Item("Reflection failed: View Error").Chosen() {
				a.ask("Reflection Failed", schema.err, "Retry", false, nil, func([]string) { a.reloadSchema(d) })
			}
			if m.Item("Generate Example Message").Disabled(a.grpcMethod(d) == nil).Chosen() {
				a.generateExample(d)
			}
			m.Separator()
			if m.Item("Reload Schema").Chosen() {
				a.reloadSchema(d)
			}
			label := "Configure Schema…"
			if len(files) > 0 {
				label = "Select Proto Files…"
			}
			if m.Item(label).Chosen() {
				a.prompt("grpc_schema", "Configure Schema", "", d.ID)
			}
		}).Label("Schema").FontSize(11).Padding(2, 8).Absolute().Top(4).Right(16)
		switch {
		case schema.unimplemented():
			button.TextColor(p.blue).Border(1, p.blue.Alpha(.5))
		case schema.err != "":
			button.TextColor(p.red).Border(1, p.red.Alpha(.5))
		}
	})
}

// protoFiles are a gRPC request's proto files and import folders, kept as
// Yaak keeps them.
func (a *App) protoFiles(id string) []string {
	for _, m := range a.list("key_value") {
		if s(m, "namespace") == "global" && s(m, "key") == "proto_files::"+id {
			var files []string
			_ = json.Unmarshal([]byte(s(m, "value")), &files)
			return files
		}
	}
	return nil
}

func (a *App) setProtoFiles(id string, files []string) {
	data, _ := json.Marshal(files)
	model := engine.Object{"model": "key_value", "namespace": "global", "key": "proto_files::" + id, "value": string(data)}
	a.run(func() (func(), error) {
		saved, err := a.Engine.Save(a.ctx, model)
		return func() {
			a.applyModel(saved)
			if d := a.drafts[id]; d != nil {
				a.reloadSchema(d)
			}
		}, err
	})
}

// grpcSchemaState is what reflection found for a gRPC request, as Yaak's
// grpc_reflect query keeps it, by the URL and proto files it is of.
type grpcSchemaState struct {
	key, wanted   string
	wantedAt      time.Time
	loading       bool
	scheduled     bool
	services      []engine.GRPCService
	err           string
	reflectedOnce bool
}

func (s *grpcSchemaState) unimplemented() bool {
	return strings.Contains(strings.ToLower(s.err), "unimplemented")
}

// grpcSchema reflects the request's schema when its URL or proto files
// change, half a second after the last change.
func (a *App) grpcSchema(d *Draft) *grpcSchemaState {
	if a.grpcSchemas == nil {
		a.grpcSchemas = map[string]*grpcSchemaState{}
	}
	s := a.grpcSchemas[d.ID]
	if s == nil {
		s = &grpcSchemaState{}
		a.grpcSchemas[d.ID] = s
	}
	key := d.URL + "\x00" + strings.Join(a.protoFiles(d.ID), "\x00") + "\x00" + a.environment
	if strings.TrimSpace(d.URL) == "" || key == s.key || s.loading {
		return s
	}
	if s.wanted != key {
		s.wanted, s.wantedAt = key, time.Now()
	}
	if wait := 500*time.Millisecond - time.Since(s.wantedAt); wait > 0 && s.reflectedOnce && !a.testMode {
		// One frame after the wait reflects, if nothing changed since.
		if !s.scheduled && a.Window != nil {
			s.scheduled = true
			go func() {
				time.Sleep(wait)
				a.Window.Update(func() { s.scheduled = false })
			}()
		}
		return s
	}
	a.reflect(d, s, key)
	return s
}

func (a *App) reloadSchema(d *Draft) {
	if s := a.grpcSchemas[d.ID]; s != nil {
		s.key = ""
		s.wantedAt = time.Time{}
	}
}

func (a *App) reflect(d *Draft, s *grpcSchemaState, key string) {
	a.save(d)
	s.key, s.loading, s.reflectedOnce = key, true, true
	id, env, files := d.ID, a.environment, a.protoFiles(d.ID)
	a.background(func() (func(), error) {
		services, err := a.Engine.ReflectGRPC(a.ctx, id, env, files)
		return func() {
			s.loading = false
			if s.key != key {
				return
			}
			s.services, s.err = services, ""
			if err != nil {
				s.services, s.err = nil, err.Error()
				return
			}
			if s.services == nil {
				s.services = []engine.GRPCService{}
			}
			// Yaak selects the first service and method the schema has
			// when the request's are not in it.
			if draft := a.drafts[id]; draft != nil && len(services) > 0 {
				service := slices.IndexFunc(services, func(sv engine.GRPCService) bool { return sv.Name == draft.Service })
				if service < 0 {
					draft.Service, draft.RPCMethod = services[0].Name, ""
					if len(services[0].Methods) > 0 {
						draft.RPCMethod = services[0].Methods[0].Name
					}
					draft.Dirty = true
				} else if !slices.ContainsFunc(services[service].Methods, func(m engine.GRPCMethod) bool { return m.Name == draft.RPCMethod }) {
					draft.RPCMethod = ""
					if len(services[service].Methods) > 0 {
						draft.RPCMethod = services[service].Methods[0].Name
					}
					draft.Dirty = true
				}
			}
		}, nil
	})
}

// grpcMethod is the request's method in its schema.
func (a *App) grpcMethod(d *Draft) *engine.GRPCMethod {
	s := a.grpcSchemas[d.ID]
	if s == nil {
		return nil
	}
	for _, service := range s.services {
		if service.Name == d.Service {
			for i, method := range service.Methods {
				if method.Name == d.RPCMethod {
					return &service.Methods[i]
				}
			}
		}
	}
	return nil
}

// grpcMethodType is Yaak's methodType.
func (a *App) grpcMethodType(d *Draft) string {
	s := a.grpcSchemas[d.ID]
	if s == nil || s.services == nil {
		return "no-schema"
	}
	m := a.grpcMethod(d)
	switch {
	case m == nil:
		return "no-method"
	case m.ClientStreaming && m.ServerStreaming:
		return "streaming"
	case m.ClientStreaming:
		return "client_streaming"
	case m.ServerStreaming:
		return "server_streaming"
	}
	return "unary"
}

func (a *App) generateExample(d *Draft) {
	m := a.grpcMethod(d)
	if m == nil {
		return
	}
	example := m.Example
	if strings.TrimSpace(d.Message) == "" {
		d.Message, d.Dirty = example, true
		return
	}
	a.confirmDialog("Generate Example", "The current message will be replaced with an example.", "Generate", false, func() {
		if draft := a.drafts[d.ID]; draft != nil {
			draft.Message, draft.Dirty = example, true
		}
	})
}

// grpcControls are the method menu and buttons beside a gRPC request's URL.
func (a *App) grpcControls(c *ui.Context, p colors, d *Draft) {
	schema := a.grpcSchema(d)
	running := a.running[d.ID]
	label := "No Schema"
	for _, service := range schema.services {
		short := service.Name[strings.LastIndex(service.Name, ".")+1:]
		for _, method := range service.Methods {
			if service.Name == d.Service && method.Name == d.RPCMethod {
				label = short + "/" + method.Name
			}
		}
	}
	ui.MenuButton(c, label, func(m *ui.Menu) {
		for _, service := range schema.services {
			short := service.Name[strings.LastIndex(service.Name, ".")+1:]
			for _, method := range service.Methods {
				if m.Item(short + "/" + method.Name).Checked(service.Name == d.Service && method.Name == d.RPCMethod).Chosen() {
					d.Service, d.RPCMethod, d.Dirty = service.Name, method.Name, true
				}
			}
		}
	}).Label("gRPC method").Disabled(running || schema.services == nil).Height(34).MinWidth(80).Padding(0, 10).Radius(5).Background(ui.Transparent).Border(1, p.border).Font("monospace").FontSize(12)
	kind := a.grpcMethodType(d)
	bordered := func(glyph, title string) ui.Element {
		return iconButton(c, glyph, title).Size(34, 34).Radius(5).Border(1, p.border)
	}
	if kind == "client_streaming" || kind == "streaming" {
		if running {
			if bordered("close", "Cancel").Clicked() {
				a.Engine.Cancel(d.ID)
			}
			if bordered("check", "Commit").Clicked() {
				a.finishGRPC(d)
			}
			if bordered("send", "Send").Clicked() {
				a.sendMessage(d)
			}
			return
		}
		if bordered("arrowUpDown", "Connect").Clicked() {
			a.send()
		}
		return
	}
	glyph, title := "send", "Send"
	switch {
	case running:
		glyph, title = "close", "Cancel"
	case strings.Contains(kind, "streaming"):
		glyph, title = "arrowUpDown", "Connect"
	}
	if bordered(glyph, title).Disabled(!running && (kind == "no-schema" || kind == "no-method")).Clicked() {
		a.send()
	}
}

// connected reports whether a WebSocket or gRPC request has a connection open.
func (a *App) connected(d *Draft) bool {
	conn := a.connections[d.ID]
	return conn != nil && s(conn, "state") != "closed" && a.running[d.ID]
}

// sendMessage sends the Message tab on the open connection.
func (a *App) sendMessage(d *Draft) {
	conn := a.connections[d.ID]
	if conn == nil {
		return
	}
	a.save(d)
	id, text, kind := s(conn, "id"), d.Message, d.Kind
	a.run(func() (func(), error) {
		if kind == "websocket_request" {
			return nil, a.Engine.SendWebSocket(a.ctx, id, text, false)
		}
		return nil, a.Engine.SendGRPCMessage(a.ctx, id, text)
	})
}

func (a *App) finishGRPC(d *Draft) {
	if conn := a.connections[d.ID]; conn != nil {
		id := s(conn, "id")
		a.run(func() (func(), error) { return nil, a.Engine.FinishGRPC(id) })
	}
}

// grpcSchemaDialog is Yaak's GrpcProtoSelectionDialog.
func (a *App) grpcSchemaDialog(c *ui.Context, p colors) {
	d := a.drafts[a.dialogID]
	if d == nil {
		return
	}
	schema := a.grpcSchema(d)
	files := a.protoFiles(d.ID)
	banner := func(color ui.Color, text string) {
		ui.Text(c, text).FontSize(13).Padding(10, 12).Radius(6).Border(1, color.Alpha(.45)).Background(color.Alpha(.08)).MaxLines(8).Selectable().FillWidth()
	}
	names := func() string {
		var list []string
		for i, s := range schema.services {
			if i == 5 {
				list = append(list, pluralizeCount("other", len(schema.services)-5))
				break
			}
			list = append(list, s.Name)
		}
		if len(list) > 1 {
			return strings.Join(list[:len(list)-1], ", ") + " and " + list[len(list)-1]
		}
		return strings.Join(list, "")
	}
	ui.Column(c).Padding(20).Gap(16).Children(func() {
		if schema.err != "" && !schema.unimplemented() {
			url := d.URL
			if url == "" {
				url = "n/a"
			}
			banner(p.orange, "Reflection failed on URL "+url+"\n"+strings.TrimSpace(schema.err))
		}
		switch {
		case len(files) > 0 && len(schema.services) > 0:
			banner(p.border, "Found services "+names())
		case len(files) == 0 && len(schema.services) > 0:
			banner(p.border, "Server reflection found services "+names()+". You can override this schema by manually selecting *.proto files.")
		}
		ui.Column(c).Gap(4).Children(func() {
			for _, path := range files {
				ui.Row(c).Key(path).Height(30).Gap(8).Padding(0, 8).Radius(4).Border(1, p.border).Children(func() {
					glyph := "fileCode"
					if !strings.HasSuffix(path, ".proto") {
						glyph = "folder"
					}
					icon(c, glyph).FontSize(13).TextColor(p.muted)
					ui.Text(c, path).Font("monospace").FontSize(12).SingleLine().Grow(1).MinWidth(0)
					if smallIconButton(c, "close", "Remove "+path).Clicked() {
						a.setProtoFiles(d.ID, slices.DeleteFunc(slices.Clone(files), func(f string) bool { return f == path }))
					}
				})
			}
			ui.MenuButton(c, "Add proto files or an import folder", func(m *ui.Menu) {
				if m.Item("Proto files").Chosen() {
					a.chooseProtoFiles(d.ID, false)
				}
				if m.Item("Import folder").Chosen() {
					a.chooseProtoFiles(d.ID, true)
				}
			}).FontSize(12).AlignSelf(ui.Start)
		})
		if schema.unimplemented() && len(files) == 0 {
			banner(p.border, d.URL+" doesn't implement Server Reflection. Please manually add the .proto file to get started.")
		}
		ui.Row(c).Justify(ui.End).Gap(8).Children(func() {
			label := "Refresh Schema"
			if schema.loading {
				label = "Refreshing…"
			}
			if ui.Button(c, label).Disabled(schema.loading).Clicked() {
				a.reloadSchema(d)
			}
			if ui.PrimaryButton(c, "Done").Clicked() {
				a.dialogOpen = false
			}
		})
	})
}

func (a *App) chooseProtoFiles(id string, directory bool) {
	a.background(func() (func(), error) {
		options := mygo.OpenDialogOptions{Parent: a.Window, Title: "Select Proto Files", Multiple: true, Filters: []mygo.FileFilter{{Name: "Proto Files", Extensions: []string{"proto"}}}}
		if directory {
			options = mygo.OpenDialogOptions{Parent: a.Window, Title: "Select Proto Directory", Directory: true}
		}
		paths, err := mygo.Dialog.Open(options)
		if err != nil || len(paths) == 0 {
			return nil, err
		}
		return func() {
			files := a.protoFiles(id)
			if directory {
				files = append(slices.DeleteFunc(files, func(f string) bool { return f == paths[0] }), paths[0])
			} else {
				for _, path := range paths {
					if !slices.Contains(files, path) {
						files = append(files, path)
					}
				}
			}
			a.setProtoFiles(id, files)
		}, nil
	})
}

// requestConnections lists a request's connections, newest first.
func (a *App) requestConnections(d *Draft) []engine.Object {
	kind := "websocket_connection"
	if d.Kind == "grpc_request" {
		kind = "grpc_connection"
	}
	var out []engine.Object
	for _, m := range a.list(kind) {
		if s(m, "requestId") == d.ID {
			out = append(out, m)
		}
	}
	slices.SortFunc(out, func(x, y engine.Object) int { return strings.Compare(s(y, "createdAt"), s(x, "createdAt")) })
	return out
}

// activeConnection is the pinned connection, else the latest.
func (a *App) activeConnection(d *Draft) engine.Object {
	connections := a.requestConnections(d)
	if len(connections) == 0 {
		return nil
	}
	if id := a.pinnedConnections[d.ID]; id != "" {
		for _, conn := range connections {
			if s(conn, "id") == id {
				return conn
			}
		}
	}
	return connections[0]
}

// connectionEvents are a connection's events, oldest first.
func (a *App) connectionEvents(d *Draft, conn engine.Object) []engine.Object {
	kind := "websocket_event"
	if d.Kind == "grpc_request" {
		kind = "grpc_event"
	}
	var out []engine.Object
	for _, event := range a.list(kind) {
		if s(event, "connectionId") == s(conn, "id") {
			out = append(out, event)
		}
	}
	slices.SortStableFunc(out, func(x, y engine.Object) int { return strings.Compare(s(x, "createdAt"), s(y, "createdAt")) })
	return out
}

// protocolResponse is Yaak's GrpcResponsePane or WebsocketResponsePane.
func (a *App) protocolResponse(c *ui.Context, p colors, d *Draft) {
	ui.Column(c).Fill().MinWidth(0).Background(p.response).Radius(6).Border(1, p.border).Children(func() {
		conn := a.activeConnection(d)
		if conn == nil {
			a.hotkeyList(c, p, []string{"request.send", "model.create", "sidebar.focus", "url_bar.focus"})
			return
		}
		events := a.connectionEvents(d, conn)
		ui.Row(c).Height(32).Shrink(0).Padding(0, 4, 0, 12).Gap(8).Children(func() {
			mono := func(text string, color ui.Color) {
				ui.Text(c, text).Font("monospace").FontSize(12).TextColor(color).SingleLine()
			}
			if d.Kind == "websocket_request" {
				if s(conn, "state") != "closed" {
					ui.Spinner(c).Size(12, 12).TextColor(p.subtle)
				}
				label, color := websocketStatus(conn, p)
				mono(label, color)
				mono("•", p.subtle)
				mono(pluralizeCount("Message", len(events)), p.muted)
			} else {
				mono(pluralizeCount("Message", len(events)), p.muted)
				if s(conn, "state") != "closed" {
					ui.Spinner(c).Size(12, 12).TextColor(p.subtle)
				}
			}
			ui.Spacer(c)
			a.connectionHistoryButton(c, d, conn)
		})
		// A unary call shows the first message received.
		if d.ConnEvent < 0 && d.ConnEventFor != s(conn, "id") && a.grpcMethodType(d) == "unary" {
			if i := slices.IndexFunc(events, func(e engine.Object) bool { return s(e, "eventType") == "server_message" }); i >= 0 {
				d.ConnEvent, d.ConnEventFor = i, s(conn, "id")
			}
		}
		a.eventViewer(c, p, d, conn, events)
	})
}

// websocketStatus is Yaak's WebsocketStatusTag.
func websocketStatus(conn engine.Object, p colors) (string, ui.Color) {
	switch {
	case s(conn, "error") != "":
		return "ERROR", p.red
	case s(conn, "state") == "connected":
		return "CONNECTED", p.green
	case s(conn, "state") == "closing":
		return "CLOSING", p.muted
	case s(conn, "state") == "closed":
		return "CLOSED", p.orange
	}
	return "CONNECTING", p.muted
}

// connectionHistoryButton is Yaak's RecentGrpcConnectionsDropdown (and
// its WebSocket one).
func (a *App) connectionHistoryButton(c *ui.Context, d *Draft, active engine.Object) {
	connections := a.requestConnections(d)
	glyph := "history"
	if len(connections) > 0 && s(connections[0], "id") != s(active, "id") {
		glyph = "pin"
	}
	iconButton(c, glyph, "Show connection history").Menu(func(m *ui.Menu) {
		if m.Item("Clear Connection").Disabled(len(connections) == 0).Chosen() {
			a.deleteConnections(d, []engine.Object{active})
		}
		if len(connections) > 1 && m.Item("Clear "+pluralizeCount("Connection", len(connections))).Chosen() {
			a.deleteConnections(d, connections)
		}
		m.Separator()
		m.Item("History").Disabled(true)
		now := time.Now()
		last, recent, shownEmpty := "", false, false
		for _, conn := range connections {
			created, _ := time.Parse("2006-01-02T15:04:05.999999999", s(conn, "createdAt"))
			group := historyGroup(created.Local(), now)
			if group == "Just now" {
				recent = true
			} else if !recent && !shownEmpty {
				m.Item("No recent connections").Disabled(true)
				shownEmpty = true
			}
			if group != "Just now" && group != last {
				m.Separator()
				m.Item(group).Disabled(true)
				last = group
			}
			id := s(conn, "id")
			if m.Item(formatMillis(n(conn, "elapsed")) + "  " + created.Local().Format("15:04:05")).Checked(id == s(active, "id")).Chosen() {
				if a.pinnedConnections == nil {
					a.pinnedConnections = map[string]string{}
				}
				a.pinnedConnections[d.ID] = id
				d.ConnEvent = -1
			}
		}
		if !recent && !shownEmpty {
			m.Item("No recent connections").Disabled(true)
		}
	})
}

func (a *App) deleteConnections(d *Draft, connections []engine.Object) {
	for _, conn := range connections {
		id := s(conn, "id")
		a.run(func() (func(), error) {
			return func() {
				delete(a.models, id)
				a.modelVersion++
				if s(a.connections[d.ID], "id") == id {
					delete(a.connections, d.ID)
				}
			}, a.Engine.Delete(a.ctx, id)
		})
	}
	delete(a.pinnedConnections, d.ID)
	d.ConnEvent = -1
}

// eventDisplay of a gRPC or WebSocket event: its icon, color and row text.
func connectionEventDisplay(d *Draft, e engine.Object, p colors) (glyph string, color ui.Color, content string, italic bool) {
	if d.Kind == "grpc_request" {
		content = s(e, "content")
		if len([]rune(content)) > 1000 {
			content = string([]rune(content)[:1000])
		}
		if s(e, "error") != "" {
			content += " (" + s(e, "error") + ")"
		}
		switch {
		case s(e, "eventType") == "server_message":
			return "arrowDown", p.blue, content, false
		case s(e, "eventType") == "client_message":
			return "arrowUp", p.accent, content, false
		case s(e, "eventType") == "error" || n(e, "status") > 0:
			return "alert", p.red, content, false
		case s(e, "eventType") == "connection_end":
			return "check", p.green, content, false
		}
		return "info", p.muted, content, false
	}
	message := string(websocketBytes(e))
	switch {
	case s(e, "messageType") == "error":
		return "alert", p.orange, message, false
	case s(e, "messageType") == "close":
		return "info", p.muted, "Disconnected from server", false
	case s(e, "messageType") == "open":
		return "info", p.muted, "Connected to server", false
	case message == "":
		glyph, color = "arrowUp", p.accent
		if b(e, "isServer") {
			glyph, color = "arrowDown", p.blue
		}
		return glyph, color, "No content", true
	}
	if len([]rune(message)) > 1000 {
		message = string([]rune(message)[:1000])
	}
	if b(e, "isServer") {
		return "arrowDown", p.blue, message, false
	}
	return "arrowUp", p.accent, message, false
}

// websocketBytes is a WebSocket event's message.
func websocketBytes(e engine.Object) []byte {
	values, _ := e["message"].([]any)
	data := make([]byte, 0, len(values))
	for _, value := range values {
		switch v := value.(type) {
		case float64:
			if v >= 0 && v <= 255 {
				data = append(data, byte(v))
			}
		case int:
			if v >= 0 && v <= 255 {
				data = append(data, byte(v))
			}
		}
	}
	return data
}

// eventViewer is Yaak's EventViewer: the events, and the one chosen in a
// panel below them.
func (a *App) eventViewer(c *ui.Context, p colors, d *Draft, conn engine.Object, events []engine.Object) {
	if len(events) == 0 && s(conn, "error") == "" {
		ui.Text(c, "No events recorded").Italic().FontSize(13).TextColor(p.subtle).Padding(12)
		return
	}
	if d.ConnEvent >= len(events) {
		d.ConnEvent = -1
	}
	list := func() {
		ui.Scroll(c).Fill().Padding(0, 12, 4, 4).Gap(1).Children(func() {
			if message := s(conn, "error"); message != "" {
				ui.Text(c, message).FontSize(12).Margin(8).Padding(8, 10).Radius(5).Border(1, p.red.Alpha(.4)).Background(p.red.Alpha(.08)).TextColor(p.red).MaxLines(6).Selectable().FillWidth()
			}
			for i, e := range events {
				glyph, color, content, italic := connectionEventDisplay(d, e, p)
				// A row shows its text on one line, as HTML folds whitespace.
				content = strings.Join(strings.Fields(content), " ")
				row := ui.ButtonBase(c).Key(s(e, "id")).Label(content).FillWidth().Height(24).Padding(0, 6).Gap(8).Radius(3).Justify(ui.Start)
				if i == d.ConnEvent {
					row.Background(p.border.Alpha(.55))
				} else if row.Hovered() {
					row.Background(p.border.Alpha(.25))
				}
				row.Children(func() {
					icon(c, glyph).FontSize(13).TextColor(color).Shrink(0)
					text := ui.Text(c, content).Font("monospace").FontSize(12).SingleLine().Grow(1).MinWidth(0)
					if italic {
						text.Italic().TextColor(p.subtle)
					} else if i != d.ConnEvent {
						text.TextColor(p.muted)
					}
					ui.Text(c, eventTime(e)).Font("monospace").FontSize(12).TextColor(p.subtle).Shrink(0)
				})
				if row.Clicked() {
					d.ConnEvent = i
				}
			}
		})
	}
	if d.ConnEvent < 0 {
		ui.Box(c).Grow(1).MinHeight(0).FillWidth().Children(list)
		return
	}
	if d.EventSplit == 0 {
		d.EventSplit = 200
	}
	ui.SplitVertical(c, &d.EventSplit, list, func() {
		ui.Column(c).Fill().Padding(10, 12, 0, 12).Gap(8).BorderWidth(1, 0, 0, 0).BorderColor(p.border).Children(func() {
			a.eventDetail(c, p, d, events[d.ConnEvent])
		})
	}).Grow(1).MinHeight(0)
}

// eventDetail is Yaak's GrpcEventDetail or WebsocketEventDetail.
func (a *App) eventDetail(c *ui.Context, p colors, d *Draft, e engine.Object) {
	var title, body string
	var metadata []keyValue
	message := false
	if d.Kind == "grpc_request" {
		switch s(e, "eventType") {
		case "client_message", "server_message":
			title, body, message = "Message Received", s(e, "content"), true
			if s(e, "eventType") == "client_message" {
				title = "Message Sent"
			}
		default:
			title = s(e, "content")
			for key, value := range o(e, "metadata") {
				metadata = append(metadata, keyValue{label: key, value: fmt.Sprint(value)})
			}
			slices.SortFunc(metadata, func(x, y keyValue) int { return strings.Compare(x.label, y.label) })
		}
	} else {
		data := websocketBytes(e)
		hexdump := d.HexDump[s(e, "id")]
		if _, set := d.HexDump[s(e, "id")]; !set {
			hexdump = s(e, "messageType") == "binary"
		}
		body = string(data)
		if hexdump {
			body = hex.Dump(data)
		}
		switch s(e, "messageType") {
		case "close":
			title = "Connection Closed"
		case "open":
			title = "Connection Open"
		case "error":
			title = "WebSocket Error"
		default:
			title = "Message Sent"
			if b(e, "isServer") {
				title = "Message Received"
			}
		}
		if len(data) > 0 {
			label := "Show Hexdump"
			if hexdump {
				label = "Show Message"
			}
			a.eventHeader(c, p, d, title, e, body, func() {
				if ui.Button(c, label).FontSize(11).Padding(2, 8).Clicked() {
					if d.HexDump == nil {
						d.HexDump = map[string]bool{}
					}
					d.HexDump[s(e, "id")] = !hexdump
				}
			})
		} else {
			a.eventHeader(c, p, d, title, e, "", nil)
		}
		if len(data) == 0 {
			emptyState(c, p, "No Content")
			return
		}
		if !hexdump {
			body = prettyMessage(body)
		}
		a.eventBody(c, p, d, e, body)
		return
	}
	copyText := ""
	if message {
		copyText = body
	}
	a.eventHeader(c, p, d, title, e, copyText, nil)
	if !message {
		if s(e, "error") != "" {
			ui.Text(c, s(e, "error")).Font("monospace").FontSize(12).TextColor(p.orange).Selectable()
		}
		if len(metadata) == 0 {
			what := "metadata"
			if s(e, "eventType") == "connection_end" {
				what = "trailers"
			}
			emptyState(c, p, "No "+what)
			return
		}
		keyValueRows(c, p, metadata)
		return
	}
	a.eventBody(c, p, d, e, prettyMessage(body))
}

// prettyMessage formats a JSON message, as Yaak's useFormatText does.
func prettyMessage(text string) string {
	var v any
	if json.Unmarshal([]byte(text), &v) != nil {
		return text
	}
	out, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return text
	}
	return string(out)
}

// eventHeader is Yaak's EventDetailHeader.
func (a *App) eventHeader(c *ui.Context, p colors, d *Draft, title string, e engine.Object, copyText string, actions func()) {
	ui.Row(c).Height(24).Shrink(0).Gap(8).Children(func() {
		ui.Text(c, title).FontSize(13).FontWeight(600).SingleLine().Grow(1).MinWidth(0).Selectable()
		if actions != nil {
			actions()
		}
		if copyText != "" && smallIconButton(c, "copy", "Copy").Clicked() {
			c.WriteClipboard(copyText)
		}
		ui.Text(c, eventTime(e)).Font("monospace").FontSize(12).TextColor(p.subtle)
		if smallIconButton(c, "close", "Close event panel").Clicked() {
			d.ConnEvent = -1
		}
	})
}

// eventBody shows a message, hiding one over a megabyte until asked, as
// Yaak does.
func (a *App) eventBody(c *ui.Context, p colors, d *Draft, e engine.Object, body string) {
	if len(body) > 1000*1000 && !d.ShowLarge {
		ui.Column(c).Gap(8).Children(func() {
			ui.Text(c, "Message previews larger than 1MB are hidden").Italic().FontSize(13).TextColor(p.subtle)
			if ui.Button(c, "Try Showing").FontSize(11).Padding(2, 8).AlignSelf(ui.Start).Clicked() {
				d.ShowLarge = true
			}
		})
		return
	}
	ui.Box(c).Grow(1).MinHeight(0).FillWidth().Children(func() {
		a.codeView(c, p, body, "json", "Event message "+s(e, "id"))
	})
}
