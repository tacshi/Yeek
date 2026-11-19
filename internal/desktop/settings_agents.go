package desktop

import (
	"encoding/json"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/egoist/mygo/yeekui"
	"yeek/locales"
)

var agentClients = []struct{ label, value string }{
	{"Claude Code", "claude_code"}, {"Codex", "codex"}, {"Cursor", "cursor"}, {"VS Code", "vscode"}, {"Other", "other"},
}

type agentSetup struct {
	where, snippet string
	shell          bool
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

func cliAgentSetup(client, command string, args []string) agentSetup {
	line := shellWord(command)
	for _, arg := range args {
		line += " " + shellWord(arg)
	}
	server := orderedSetup{{"command", command}, {"args", args}}
	switch client {
	case "claude_code":
		return agentSetup{"", "claude mcp add -s user yeek -- " + line, true}
	case "codex":
		return agentSetup{"", "codex mcp add yeek -- " + line, true}
	case "cursor":
		return agentSetup{locales.AddToCursorMcpJson(), setupJSON(map[string]any{"mcpServers": map[string]any{"yeek": server}}), false}
	case "vscode":
		return agentSetup{locales.AddToYourUserMcpJson(), setupJSON(map[string]any{"servers": map[string]any{"yeek": append(orderedSetup{{"type", "stdio"}}, server...)}}), false}
	}
	return agentSetup{locales.ForClientsThatReadAnMcpServersConfig(), setupJSON(map[string]any{"mcpServers": map[string]any{"yeek": server}}), false}
}

func settingSnippet(c *ui.Context, p colors, setup agentSetup) {
	ui.Row(c).FillWidth().Margin(8, 0, 0, 0).Padding(6, 6, 6, 12).Gap(8).Radius(6).Border(1, p.border).Background(p.border.Alpha(.2)).AlignItems(ui.Start).Children(func() {
		ui.Row(c).Grow(1).MinWidth(0).Padding(4, 0).Gap(8).AlignItems(ui.Start).Children(func() {
			if setup.shell {
				ui.Text(c, "$").Font("monospace").FontSize(12).TextColor(p.subtle)
			}
			ui.Text(c, setup.snippet).Font("monospace").FontSize(12).Wrap().Selectable().Grow(1).MinWidth(0)
		})
		if smallIconButton(c, "copy", locales.Copy()).Border(1, p.border).Clicked() {
			c.WriteClipboard(setup.snippet)
		}
	})
}

func settingCommand(c *ui.Context, p colors, key, title, warning string, setup agentSetup) {
	ui.Column(c).Key(key).FillWidth().Padding(16, 0).Gap(4).BorderWidth(0, 0, 1, 0).BorderColor(p.border).Children(func() {
		ui.Text(c, title).FontSize(13)
		if setup.where != "" {
			ui.Text(c, setup.where).FontSize(12).TextColor(p.muted).MaxWidth(640)
		}
		if warning != "" {
			ui.Text(c, warning).FontSize(12).TextColor(p.orange)
		}
		settingSnippet(c, p, setup)
	})
}

func (a *App) commandButtons(c *ui.Context) {
	st := &a.command
	s := st.status
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
}

func (a *App) commandStatusRow(c *ui.Context, p colors) {
	s := a.command.status
	name := commandName()
	if s.installed && !a.command.busy {
		ui.Row(c).Key("command").FillWidth().Padding(12, 0).Gap(8).BorderWidth(0, 0, 1, 0).BorderColor(p.border).Children(func() {
			icon(c, "check").FontSize(14).TextColor(p.green)
			ui.Text(c, locales.CommandIsInstalledAt(name, s.link)).FontSize(12).TextColor(p.muted).Grow(1).MinWidth(0).MaxLines(2)
			a.commandButtons(c)
		})
		return
	}
	description, color := locales.AddsCommandToFolder(name, filepath.Dir(s.link)), p.subtle
	switch {
	case s.translocated:
		description, color = locales.MoveYeekToApplicationsFirst(), p.orange
	case s.taken != "":
		description, color = locales.CommandTaken(s.taken), p.orange
	case s.other:
		description, color = locales.CommandRunsAnotherCopy(s.link), p.orange
	}
	ui.Column(c).Key("command").FillWidth().Padding(16, 0).BorderWidth(0, 0, 1, 0).BorderColor(p.border).Children(func() {
		ui.Column(c).FillWidth().Padding(12, 14).Gap(4).Radius(6).Border(1, p.blue.Alpha(.35)).Background(p.blue.Alpha(.08)).Children(func() {
			ui.Row(c).Gap(8).Children(func() {
				icon(c, "square_terminal").FontSize(16).TextColor(p.blue)
				ui.Text(c, locales.InstallTheCommand(name)).FontSize(13)
			})
			ui.Text(c, locales.AgentsLaunchItToReachYeek()).FontSize(12).TextColor(p.muted).MaxLines(3)
			ui.Row(c).FillWidth().Margin(8, 0, 0, 0).Gap(12).Children(func() {
				ui.Text(c, description).FontSize(12).TextColor(color).MaxLines(3).Grow(1).MinWidth(0)
				ui.Row(c).Shrink(0).Gap(6).Children(func() { a.commandButtons(c) })
			})
		})
	})
}

func (a *App) settingsAgents(c *ui.Context, p colors) {
	settingsHeading(c, p, locales.Agents(), locales.ConnectAICodingAgentsLikeClaude())
	a.checkCommand()
	client := a.agentClient()
	enabled := b(a.settings, "mcpServerEnabled")
	ui.Column(c).FillWidth().Children(func() {
		ui.Column(c).FillWidth().Margin(0, 0, 16, 0).Children(func() {
			settingRow(c, p, locales.EnableMCPServer(), locales.LetsAgentsReadEditAndSend(commandName()), nil, func() {
				on := enabled
				if ui.Checkbox(c, &on, "").Label(locales.MCPServerEnabled()).Changed() {
					a.saveSetting("mcpServerEnabled", on)
				}
			})
		})
		if !enabled {
			return
		}
		ui.Row(c).FillWidth().Padding(0, 0, 16, 0).BorderWidth(0, 0, 1, 0).BorderColor(p.border).Children(func() {
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
		if a.command.status.supported {
			a.commandStatusRow(c, p)
		}
		command, args := a.cliInvocation(a.command.status)
		warning := ""
		if s := a.command.status; s.translocated && !s.installed {
			warning = locales.MoveYeekToApplicationsFirst()
		}
		settingCommand(c, p, "Add the MCP server", locales.AddTheMCPServer(), warning, cliAgentSetup(client, command, args))
	})
}
