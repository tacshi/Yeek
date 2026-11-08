package desktop

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/egoist/mygo/yeekui"
	"yeek/internal/mcp"
	"yeek/locales"
)

var agentClients = []struct{ label, value string }{
	{"Claude Code", "claude_code"}, {"Codex", "codex"}, {"Cursor", "cursor"}, {"VS Code", "vscode"}, {"Other", "other"},
}

type agentSetup struct{ where, snippet string }

func appAgentSetup(client, url, token string) agentSetup {
	authorization := "Bearer " + token
	switch client {
	case "claude_code":
		return agentSetup{locales.RunInATerminal(), fmt.Sprintf(`claude mcp add --transport http yeek %s --header "Authorization: %s"`, url, authorization)}
	case "codex":
		quoted := func(s string) string { data, _ := json.Marshal(s); return string(data) }
		return agentSetup{locales.AddToCodexConfigToml(), strings.Join([]string{
			"[mcp_servers.yeek]",
			"url = " + quoted(url),
			`http_headers = { "Authorization" = ` + quoted(authorization) + " }",
		}, "\n")}
	case "cursor":
		return agentSetup{locales.AddToCursorMcpJson(), setupJSON(map[string]any{"mcpServers": map[string]any{"yeek": map[string]any{"url": url, "headers": map[string]any{"Authorization": authorization}}}})}
	case "vscode":
		return agentSetup{locales.AddToYourUserMcpJson(), setupJSON(map[string]any{"servers": map[string]any{"yeek": orderedSetup{{"type", "http"}, {"url", url}, {"headers", map[string]any{"Authorization": authorization}}}}})}
	}
	return agentSetup{locales.ForClientsThatReadAnMcpServers(), setupJSON(map[string]any{"mcpServers": map[string]any{"yeek": orderedSetup{
		{"command", "npx"},
		{"args", []string{"-y", "mcp-remote", url, "--header", "Authorization: " + authorization}},
	}}})}
}

type orderedSetup []struct {
	key   string
	value any
}

func (o orderedSetup) MarshalJSON() ([]byte, error) {
	var b strings.Builder
	b.WriteByte('{')
	for i, member := range o {
		if i > 0 {
			b.WriteByte(',')
		}
		key, _ := json.Marshal(member.key)
		value, err := json.Marshal(member.value)
		if err != nil {
			return nil, err
		}
		b.Write(key)
		b.WriteByte(':')
		b.Write(value)
	}
	b.WriteByte('}')
	return []byte(b.String()), nil
}

var plainArray = regexp.MustCompile(`\[\n\s*([^\[\]{}]*?)\n\s*\]`)

func setupJSON(value any) string {
	data, _ := json.MarshalIndent(value, "", "  ")
	return plainArray.ReplaceAllStringFunc(string(data), func(m string) string {
		items := plainArray.FindStringSubmatch(m)[1]
		return "[" + strings.Join(regexp.MustCompile(`,\n\s*`).Split(items, -1), ", ") + "]"
	})
}

func (a *App) agentClient() string {
	for _, kv := range a.list("key_value") {
		if s(kv, "namespace") == "global" && s(kv, "key") == "agents_setup_client" {
			var client string
			_ = json.Unmarshal([]byte(s(kv, "value")), &client)
			for _, c := range agentClients {
				if c.value == client {
					return client
				}
			}
		}
	}
	return "claude_code"
}

func (a *App) setAgentClient(client string) {
	value, _ := json.Marshal(client)
	model := map[string]any{"model": "key_value", "namespace": "global", "key": "agents_setup_client", "value": string(value)}
	for _, kv := range a.list("key_value") {
		if s(kv, "namespace") == "global" && s(kv, "key") == "agents_setup_client" {
			model["id"] = kv["id"]
		}
	}
	a.saveModel(model)
}

func describedSection(c *ui.Context, p colors, title, description string, rows func()) {
	ui.Column(c).Key(title).FillWidth().Children(func() {
		ui.Column(c).FillWidth().Gap(4).Padding(0, 0, 8, 0).BorderWidth(0, 0, 1, 0).BorderColor(p.border).Children(func() {
			ui.Text(c, title).FontSize(13).TextColor(p.muted)
			ui.Text(c, description).FontSize(12).TextColor(p.subtle).MaxLines(3)
		})
		rows()
	})
}

func cliAgentSetup(client, command string, args []string) agentSetup {
	line := shellWord(command)
	for _, arg := range args {
		line += " " + shellWord(arg)
	}
	server := orderedSetup{{"command", command}, {"args", args}}
	switch client {
	case "claude_code":
		return agentSetup{locales.RunInATerminal(), "claude mcp add -s user yeek -- " + line}
	case "codex":
		return agentSetup{locales.RunInATerminal(), "codex mcp add yeek -- " + line}
	case "cursor":
		return agentSetup{locales.AddToCursorMcpJson(), setupJSON(map[string]any{"mcpServers": map[string]any{"yeek": server}})}
	case "vscode":
		return agentSetup{locales.AddToYourUserMcpJson(), setupJSON(map[string]any{"servers": map[string]any{"yeek": append(orderedSetup{{"type", "stdio"}}, server...)}})}
	}
	return agentSetup{locales.ForClientsThatReadAnMcpServersConfig(), setupJSON(map[string]any{"mcpServers": map[string]any{"yeek": server}})}
}

func setupSnippet(c *ui.Context, p colors, key, title string, setup agentSetup) {
	ui.Column(c).Key(key).FillWidth().Padding(14, 0).Gap(4).BorderWidth(0, 0, 1, 0).BorderColor(p.border).Children(func() {
		ui.Text(c, title).FontSize(13)
		ui.Text(c, setup.where).FontSize(12).TextColor(p.muted)
		ui.Row(c).FillWidth().Margin(8, 0, 0, 0).Padding(8, 8, 8, 10).Gap(8).Radius(6).Border(1, p.border).AlignItems(ui.Start).Children(func() {
			ui.Text(c, setup.snippet).Font("monospace").FontSize(12).Wrap().Selectable().Grow(1).MinWidth(0)
			if smallIconButton(c, "copy", locales.CopySetup()).Border(1, p.border).Clicked() {
				c.WriteClipboard(setup.snippet)
			}
		})
	})
}

func (a *App) commandRow(c *ui.Context, p colors) {
	st := &a.command
	s := st.status
	name := commandName()
	description, color := locales.AddsCommandToFolder(name, filepath.Dir(s.link)), p.muted
	switch {
	case s.translocated:
		description, color = locales.MoveYeekToApplicationsFirst(), p.orange
	case s.taken != "":
		description, color = locales.CommandTaken(s.taken), p.orange
	case s.other:
		description, color = locales.CommandRunsAnotherCopy(s.link), p.orange
	case s.installed:
		description, color = locales.CommandInstalledAt(s.link), p.green
	}
	ui.Row(c).Key("command").FillWidth().Padding(14, 0).Gap(16).BorderWidth(0, 0, 1, 0).BorderColor(p.border).Children(func() {
		ui.Column(c).Grow(1).MinWidth(0).Gap(4).Children(func() {
			ui.Text(c, locales.TheCommand(name)).FontSize(13)
			ui.Text(c, description).FontSize(12).TextColor(color).MaxLines(3)
		})
		ui.Row(c).Shrink(0).Gap(6).Children(func() {
			switch {
			case st.busy:
				label := locales.Installing()
				if st.removing {
					label = locales.Removing()
				}
				ui.Button(c, label).Disabled(true)
			case s.installed:
				if ui.Button(c, locales.Remove()).Clicked() {
					a.changeCommand(true)
				}
			case s.taken != "":
			default:
				label := locales.Install()
				if s.other {
					label = locales.UpdateCommand()
				}
				if ui.PrimaryButton(c, label).Disabled(s.translocated).Clicked() {
					a.changeCommand(false)
				}
			}
		})
	})
}

func (a *App) settingsAgents(c *ui.Context, p colors) {
	settingsHeading(c, p, locales.Agents(), locales.ConnectAICodingAgentsLikeClaude())
	url := "http://" + mcp.DefaultAddr + mcp.Path
	enabled := b(a.settings, "mcpServerEnabled")
	token := mcpToken(a.list("key_value"))
	describedSection(c, p, locales.MCPServerInTheApp(), locales.ForAgentsThatConnectOverHTTP(), func() {
		settingRow(c, p, locales.EnableMCPServer(), locales.ServeYeeksToolsAtUrlWhile(url), nil, func() {
			on := enabled
			if ui.Checkbox(c, &on, "").Label(locales.MCPServerEnabled()).Changed() {
				a.saveSetting("mcpServerEnabled", on)
			}
		})
		if enabled && token != "" {
			settingRow(c, p, locales.Token(), locales.AgentsSendThisAsABearer(), nil, func() { settingValue(c, p, token, nil) })
		}
	})
	a.checkCommand()
	if enabled && token != "" {
		a.connectAgent(c, p, url, token)
	}
	if a.command.status.supported {
		describedSection(c, p, locales.CommandLine(), locales.AgentsLaunchCommandToReachYeek(commandName()+" mcp"), func() {
			a.commandRow(c, p)
		})
	}
}

func (a *App) connectAgent(c *ui.Context, p colors, url, token string) {
	command, args := a.cliInvocation(a.command.status)
	describedSection(c, p, locales.ConnectAnAgent(), locales.PickYourAgentOrEditorTo(), func() {
		client := a.agentClient()
		ui.Row(c).FillWidth().Padding(14, 0).BorderWidth(0, 0, 1, 0).BorderColor(p.border).Children(func() {
			selected := 0
			labels := make([]string, len(agentClients))
			for i, option := range agentClients {
				labels[i] = option.label
				if option.value == "other" {
					labels[i] = locales.Other()
				}
				if option.value == client {
					selected = i
				}
			}
			if ui.Segmented(c, &selected, labels...).Label(locales.Agent()).Changed() {
				a.setAgentClient(agentClients[selected].value)
			}
			ui.Spacer(c)
		})
		setupSnippet(c, p, "Through the CLI", locales.ThroughTheCLI(), cliAgentSetup(client, command, args))
		setupSnippet(c, p, "Through the app", locales.ThroughTheApp(), appAgentSetup(client, url, token))
	})
}
