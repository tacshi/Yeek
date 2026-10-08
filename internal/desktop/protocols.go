package desktop

import (
	"encoding/base64"
	"fmt"
	"strings"
	"time"

	"github.com/egoist/mygo"
	"github.com/egoist/mygo/yeekui"
)

func (a *App) protocolRequest(c *ui.Context, p colors, d *Draft) {
	if d.Kind == "grpc_request" {
		ui.Column(c).Padding(5, 12).Gap(8).Children(func() {
			ui.Row(c).Gap(8).Children(func() {
				if ui.Button(c, "Load Services").Label("Load gRPC services").Clicked() {
					a.reflectGRPC(d)
				}
				if ui.Button(c, "Proto Files…").Clicked() {
					a.chooseProtoFiles(d)
				}
				if len(d.ProtoFiles) > 0 {
					ui.Textf(c, "%d files", len(d.ProtoFiles)).FontSize(11).TextColor(p.muted)
				}
			})
			names := []string{}
			for _, service := range d.Services {
				names = append(names, service.Name)
			}
			if len(names) > 0 {
				if ui.Select(c, &d.Service, names).Label("gRPC service").FillWidth().Changed() {
					d.Dirty = true
					d.RPCMethod = ""
				}
				methods := []string{}
				for _, service := range d.Services {
					if service.Name == d.Service {
						for _, method := range service.Methods {
							methods = append(methods, method.Name)
						}
					}
				}
				if len(methods) > 0 {
					if ui.Select(c, &d.RPCMethod, methods).Label("gRPC method").FillWidth().Changed() {
						d.Dirty = true
						for _, service := range d.Services {
							if service.Name == d.Service {
								for _, method := range service.Methods {
									if method.Name == d.RPCMethod {
										d.Message = method.Example
									}
								}
							}
						}
					}
				}
			} else {
				if ui.TextInput(c, &d.Service).Label("gRPC service").Placeholder("package.Service").FillWidth().Changed() {
					d.Dirty = true
				}
				if ui.TextInput(c, &d.RPCMethod).Label("gRPC method").Placeholder("Method").FillWidth().Changed() {
					d.Dirty = true
				}
			}
		})
	}
	tabBar(c, p, &d.Tab, []tabItem{
		{Label: "Message"},
		{Label: "Params", Badge: badgeCount(countNamed(d.Parameters, true))},
		{Label: "Headers", Badge: badgeCount(a.headerCount(d))},
		{Label: authTabLabel(a, d.AuthType), Menu: func(m *ui.Menu) { a.authTypeMenu(m, d) }},
		{Label: "Settings", Badge: badgeCount(overriddenSettings(d))},
		{Label: "Info", Badge: badgeDot(strings.TrimSpace(d.Description) != "")},
	})
	switch d.Tab {
	case 0:
		ui.Row(c).Height(38).Padding(5, 12).Gap(8).Children(func() {
			if d.Kind == "websocket_request" {
				kind := "Text"
				if d.MessageType == "binary" {
					kind = "Binary"
				}
				if ui.Select(c, &kind, []string{"Text", "Binary"}).Label("Message type").Width(110).Changed() {
					d.MessageType = strings.ToLower(kind)
				}
			}
			ui.Spacer(c)
			conn := a.connections[d.ID]
			if d.Kind == "grpc_request" {
				if ui.Button(c, "Finish").Disabled(conn == nil || !a.running[d.ID]).Clicked() {
					id := s(conn, "id")
					a.run(func() (func(), error) { return nil, a.Engine.FinishGRPC(id) })
				}
			}
			if ui.PrimaryButton(c, "Send Message").Disabled(conn == nil || !a.running[d.ID]).Clicked() {
				id, text, kind, binary := s(conn, "id"), d.Message, d.Kind, d.MessageType == "binary"
				a.run(func() (func(), error) {
					if kind == "websocket_request" {
						return nil, a.Engine.SendWebSocket(a.ctx, id, text, binary)
					}
					return nil, a.Engine.SendGRPCMessage(a.ctx, id, text)
				})
			}
		})
		a.codeInput(c, &d.Message, "Message body", &d.Dirty)
	case 1:
		a.kvEditor(c, p, &d.Parameters, "Parameter", "Value", &d.Dirty)
	case 2:
		a.headersEditor(c, p, d)
	case 3:
		a.authEditor(c, p, d)
	case 4:
		a.requestSettings(c, p, d)
	case 5:
		a.descriptionEditor(c, p, d, "Request description")
	}
}
func (a *App) protocolResponse(c *ui.Context, p colors, d *Draft) {
	conn := a.connections[d.ID]
	ui.Column(c).Fill().Radius(6).Border(1, p.border).Background(p.response).Children(func() {
		ui.Row(c).Height(44).Padding(0, 12).Gap(10).BorderWidth(0, 0, 1, 0).BorderColor(p.border).Children(func() {
			status := "Not connected"
			color := p.muted
			if conn != nil {
				status = fieldLabel(s(conn, "state"))
				if s(conn, "state") == "connected" {
					color = p.green
				}
				if s(conn, "state") == "closed" && d.Kind == "grpc_request" {
					status = fmt.Sprintf("%.0f %s", n(conn, "status"), grpcStatusName(int(n(conn, "status"))))
					if n(conn, "status") == 0 {
						color = p.green
					}
				}
				if s(conn, "error") != "" {
					color = p.red
				}
			}
			ui.Text(c, status).FontSize(12).TextColor(color)
			ui.Spacer(c)
			if d.Kind == "websocket_request" && a.running[d.ID] {
				if ui.Button(c, "Ping").FontSize(11).Clicked() {
					id := s(conn, "id")
					a.run(func() (func(), error) { return nil, a.Engine.PingWebSocket(a.ctx, id) })
				}
			}
		})
		if conn == nil {
			ui.Column(c).Grow(1).Center().Gap(10).Children(func() {
				icon(c, "globe").FontSize(26).TextColor(p.subtle)
				ui.Text(c, "Connect to start a session").TextColor(p.muted).FontSize(12)
			})
			return
		}
		if message := s(conn, "error"); message != "" {
			ui.Text(c, message).Padding(12).TextColor(p.red).FontSize(12).Selectable()
		}
		kind := "websocket_event"
		if d.Kind == "grpc_request" {
			kind = "grpc_event"
		}
		events := a.list(kind)
		ui.Scroll(c).Grow(1).MinHeight(0).Padding(10).Gap(8).Children(func() {
			for _, event := range events {
				if s(event, "connectionId") != s(conn, "id") {
					continue
				}
				content := s(event, "content")
				label := s(event, "eventType")
				color := p.muted
				if d.Kind == "websocket_request" {
					label = s(event, "messageType")
					values, _ := event["message"].([]any)
					data := make([]byte, 0, len(values))
					for _, value := range values {
						switch n := value.(type) {
						case float64:
							if n >= 0 && n <= 255 {
								data = append(data, byte(n))
							}
						case int:
							if n >= 0 && n <= 255 {
								data = append(data, byte(n))
							}
						}
					}
					content = string(data)
					if label == "binary" {
						content = base64.StdEncoding.EncodeToString(data)
					}
					if b(event, "isServer") {
						label = "Received · " + label
						color = p.green
					} else {
						label = "Sent · " + label
						color = p.blue
					}
				} else {
					switch label {
					case "connection_start":
						label = "Connected"
					case "connection_end":
						label = "Disconnected"
					case "info":
						label = "Information"
					case "error":
						label = "Error"
						color = p.red
					case "server_message":
						label = "Received"
						color = p.green
					case "client_message":
						label = "Sent"
						color = p.blue
					}
				}
				ui.Column(c).Key(s(event, "id")).Padding(10).Gap(7).Border(1, p.border).Radius(5).Children(func() {
					ui.Row(c).Gap(8).Children(func() {
						ui.Text(c, label).FontSize(11).TextColor(color)
						ui.Spacer(c)
						timestamp, _ := time.Parse("2006-01-02T15:04:05.999999999", s(event, "createdAt"))
						ui.Text(c, timestamp.Local().Format("15:04:05.000")).FontSize(10).TextColor(p.subtle)
					})
					ui.Text(c, content).Font("monospace").FontSize(12).Selectable()
				})
			}
		})
	})
}
func (a *App) reflectGRPC(d *Draft) {
	a.save(d)
	id, env, files := d.ID, a.environment, append([]string{}, d.ProtoFiles...)
	a.background(func() (func(), error) {
		services, err := a.Engine.ReflectGRPC(a.ctx, id, env, files)
		return func() {
			draft := a.drafts[id]
			if draft == nil {
				return
			}
			draft.Services = services
			if len(services) > 0 && draft.Service == "" {
				draft.Service = services[0].Name
				if len(services[0].Methods) > 0 {
					draft.RPCMethod = services[0].Methods[0].Name
					draft.Message = services[0].Methods[0].Example
				}
				draft.Tab = 0
				draft.Dirty = true
			}
		}, err
	})
}
func (a *App) chooseProtoFiles(d *Draft) {
	id := d.ID
	a.background(func() (func(), error) {
		paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{Parent: a.Window, Title: "Protocol Buffer Definitions", Multiple: true, Filters: []mygo.FileFilter{{Name: "Protocol Buffers", Extensions: []string{"proto"}}}})
		if err != nil || len(paths) == 0 {
			return nil, err
		}
		return func() {
			if d := a.drafts[id]; d != nil {
				d.ProtoFiles = paths
				a.reflectGRPC(d)
			}
		}, nil
	})
}

func grpcStatusName(code int) string {
	names := []string{"OK", "Canceled", "Unknown", "Invalid Argument", "Deadline Exceeded", "Not Found", "Already Exists", "Permission Denied", "Resource Exhausted", "Failed Precondition", "Aborted", "Out of Range", "Unimplemented", "Internal", "Unavailable", "Data Loss", "Unauthenticated"}
	if code >= 0 && code < len(names) {
		return names[code]
	}
	return "Unknown"
}
